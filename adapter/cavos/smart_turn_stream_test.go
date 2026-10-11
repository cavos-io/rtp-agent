package cavos

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/cavos-io/rtp-agent/adapter/cavos/spec"
	"github.com/cavos-io/rtp-agent/adapter/pipecat"
	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/audio/model"
)

var _ agent.StreamingTurnDetector = (*SmartTurn)(nil)

type turnTestServer struct {
	spec.UnimplementedSmartTurnServiceV1Server
	predict func(context.Context, *spec.SmartTurnRequest) (*spec.SmartTurnResponse, error)
}

func (s *turnTestServer) Predict(ctx context.Context, req *spec.SmartTurnRequest) (*spec.SmartTurnResponse, error) {
	return s.predict(ctx, req)
}

func newTurnTestDetector(t *testing.T, predict func(context.Context, *spec.SmartTurnRequest) (*spec.SmartTurnResponse, error), opts ...SmartTurnOption) *SmartTurn {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	spec.RegisterSmartTurnServiceV1Server(server, &turnTestServer{predict: predict})
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(func() { server.Stop(); _ = lis.Close() })
	d, err := NewSmartTurn(append([]SmartTurnOption{WithSmartTurnAddr(lis.Addr().String())}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestCavosSmartTurnStreamPredictsRetainedAudio(t *testing.T) {
	first := pcm16Frame([]int16{1000, 1000, 1000, 1000}, 16000, 1)
	second := pcm16Frame([]int16{2000, 2000, 2000, 2000}, 16000, 1)
	third := pcm16Frame([]int16{3000, 3000, 3000, 3000}, 16000, 1)
	wantSamples, err := framesToMono([]*model.AudioFrame{first, third}, 16000)
	if err != nil {
		t.Fatal(err)
	}
	wantFeatures, err := pipecat.NewWhisperFeatureExtractor().Extract(context.Background(), wantSamples)
	if err != nil {
		t.Fatal(err)
	}
	want := encodeFloat32LE(wantFeatures)
	d := newTurnTestDetector(t, func(_ context.Context, req *spec.SmartTurnRequest) (*spec.SmartTurnResponse, error) {
		if req.GetMelBins() != 80 || req.GetTimeSteps() != 800 || !bytes.Equal(req.GetFeatures(), want) {
			return nil, errors.New("unexpected retained audio features")
		}
		return &spec.SmartTurnResponse{Probability: .5, IsComplete: true, InferenceMs: 12}, nil
	})
	stream, err := d.Stream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	step := 250 * time.Microsecond
	for i, frame := range []*model.AudioFrame{first, second, third} {
		if err := stream.PushAudio(frame, time.Duration(i)*step); err != nil {
			t.Fatal(err)
		}
	}
	if err := stream.DiscardAudio(step, 2*step); err != nil {
		t.Fatal(err)
	}
	result, err := stream.Predict(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsComplete || result.Probability != .5 || result.InferenceMs != 12 {
		t.Fatalf("prediction = %+v, want server completion verdict", result)
	}
	if err := stream.Reset(step); err != nil {
		t.Fatal(err)
	}
	if err := stream.EndInput(); err != nil {
		t.Fatal(err)
	}
	if err := stream.PushAudio(first, 3*step); err == nil {
		t.Fatal("push after end input succeeded")
	}
}

func TestCavosSmartTurnStreamUsesServerIncompleteVerdict(t *testing.T) {
	d := newTurnTestDetector(t, func(context.Context, *spec.SmartTurnRequest) (*spec.SmartTurnResponse, error) {
		return &spec.SmartTurnResponse{Probability: .6, IsComplete: false}, nil
	})
	stream, err := d.Stream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := stream.PushAudio(pcm16Frame([]int16{1000, 1000}, 16000, 1), 0); err != nil {
		t.Fatal(err)
	}
	result, err := stream.Predict(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.IsComplete || float32(result.Probability) != float32(.6) {
		t.Fatalf("prediction = %+v, want server incomplete verdict", result)
	}
}

func TestCavosSmartTurnStreamSessionsAndCancellation(t *testing.T) {
	started := make(chan struct{}, 1)
	d := newTurnTestDetector(t, func(ctx context.Context, _ *spec.SmartTurnRequest) (*spec.SmartTurnResponse, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	a, err := d.Stream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := d.Stream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	frame := pcm16Frame([]int16{1, 2, 3, 4}, 16000, 1)
	if err := a.PushAudio(frame, 0); err != nil {
		t.Fatal(err)
	}
	if err := b.PushAudio(frame, 0); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := a.Predict(context.Background()); result <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("RPC did not start")
	}
	if err := a.PushAudio(frame, 250*time.Microsecond); err != nil {
		t.Fatalf("push blocked by in-flight prediction: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("canceled RPC returned no error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("close did not join prediction")
	}
	if err := b.PushAudio(frame, 250*time.Microsecond); err != nil {
		t.Fatalf("other stream closed: %v", err)
	}
	if got := len(b.(*cavosTurnStream).audio); got != 2 {
		t.Fatalf("other session retained %d chunks, want 2", got)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Stream(context.Background()); err == nil {
		t.Fatal("stream after detector close succeeded")
	}
}

func TestCavosSmartTurnStreamBufferBoundaries(t *testing.T) {
	d := newTurnTestDetector(t, func(context.Context, *spec.SmartTurnRequest) (*spec.SmartTurnResponse, error) {
		return &spec.SmartTurnResponse{Probability: .8}, nil
	}, WithSmartTurnMaxAudioSecs(1))
	stream, err := d.Stream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	s := stream.(*cavosTurnStream)
	for i := 0; i < 3; i++ {
		samples := make([]int16, 8000)
		samples[0] = 1000
		frame := pcm16Frame(samples, 16000, 1)
		if err := stream.PushAudio(frame, time.Duration(i)*500*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		frame.Data[0] = 0xff // retained samples must not alias caller memory
	}
	if len(s.audio) != 2 || s.audio[0].start != 500*time.Millisecond {
		t.Fatalf("bounded audio = %+v, want last second", s.audio)
	}
	if s.audio[0].samples[0] != float32(1000)/32768 {
		t.Fatal("retained audio changed with caller frame")
	}
	if err := stream.Reset(750 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := stream.DiscardAudio(800*time.Millisecond, 1250*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if len(s.audio) != 2 || s.audio[0].start != 750*time.Millisecond || len(s.audio[0].samples) != 800 || s.audio[1].start != 1250*time.Millisecond || len(s.audio[1].samples) != 4000 {
		t.Fatalf("retained audio = %+v, want [750,800) and [1250,1500) ms", s.audio)
	}
	if stream.SupportsLanguage("xx") || !stream.SupportsLanguage("en-US") || stream.Model() != "smart-turn-v3.2" || stream.Provider() != "cavos" || stream.PredictionTimeout() != time.Second {
		t.Fatal("unexpected Cavos Smart Turn v3.2 stream capabilities")
	}
}

func TestCavosSmartTurnRejectsNon16kSampleRate(t *testing.T) {
	d, err := NewSmartTurn(WithSmartTurnSampleRate(8000))
	if err == nil {
		_ = d.Close()
		t.Fatal("8 kHz detector accepted for a 16 kHz Smart Turn v3.2 model")
	}
}
