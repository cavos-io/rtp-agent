package cavos

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/adapter/cavos/spec"
	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/library/logger"
	protologger "github.com/livekit/protocol/logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// Tests use the real adapter with an in-memory RPC client; protocol tests below
// additionally run the generated client against a local gRPC server.
type bargePredictFunc func(context.Context, *spec.BargeInRequest, ...grpc.CallOption) (*spec.BargeInResponse, error)

func (f bargePredictFunc) Predict(ctx context.Context, req *spec.BargeInRequest, opts ...grpc.CallOption) (*spec.BargeInResponse, error) {
	return f(ctx, req, opts...)
}

func bargeFrame(start, count int) *model.AudioFrame {
	data := make([]byte, count*2)
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(data[i*2:], uint16(int16(start+i)))
	}
	return &model.AudioFrame{Data: data, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: uint32(count)}
}

func TestAdaptiveV2WaitsForExactTriggerAndKeepsShortSpeechSilence(t *testing.T) {
	requests := make(chan []byte, 2)
	s := newCavosInterruptionStream(context.Background(), bargePredictFunc(func(ctx context.Context, r *spec.BargeInRequest, _ ...grpc.CallOption) (*spec.BargeInResponse, error) {
		requests <- append([]byte(nil), r.Pcm...)
		return &spec.BargeInResponse{Probability: .8, Threshold: .9, IsBargeIn: false, PreprocessMs: 2, InferenceMs: 3}, nil
	}), nil, "")
	defer s.Close()
	// Remove wall-clock jitter from sample-window assertions.
	now := time.Now()
	s.mu.Lock()
	s.now = func() time.Time { return now }
	s.mu.Unlock()
	if err := s.PushFrame(bargeFrame(0, 16000)); err != nil {
		t.Fatal(err)
	}
	if err := s.StartAgentSpeech(); err != nil {
		t.Fatal(err)
	}
	if err := s.StartOverlap(now); err != nil {
		t.Fatal(err)
	}
	if err := s.EndOverlap(now.Add(200*time.Millisecond), false); err != nil {
		t.Fatal(err)
	}
	if err := s.PushFrame(bargeFrame(16000, 15999)); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	sent := s.overlap.queued
	s.mu.Unlock()
	if sent {
		t.Fatal("inference scheduled before onset + 1 s")
	}
	if err := s.PushFrame(bargeFrame(31999, 100)); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	queuedAt := s.overlap.queuedAt
	s.mu.Unlock()
	if !queuedAt.Equal(now) {
		t.Fatalf("window readiness timestamp = %v, want trigger timestamp %v", queuedAt, now)
	}
	select {
	case pcm := <-requests:
		if len(pcm) != 64000 {
			t.Fatalf("window bytes=%d", len(pcm))
		}
		for i := 0; i < 32000; i++ {
			if got := int16(binary.LittleEndian.Uint16(pcm[i*2:])); got != int16(i) {
				t.Fatalf("sample %d=%d", i, got)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("no inference")
	}
	ev, err := s.Next()
	if err != nil {
		t.Fatal(err)
	}
	if ev.IsInterruption || ev.AgentEnded || ev.OverlapStartedAt.UnixNano() != now.UnixNano() || ev.NumRequests != 1 {
		t.Fatalf("verdict=%+v", ev)
	}
	if ev.PredictionDuration != 5*time.Millisecond || ev.Probability != float64(float32(.8)) {
		t.Fatalf("metrics=%+v", ev)
	}
	if err := s.EndOverlap(now, false); err != nil {
		t.Fatal(err)
	}
	if err := s.PushFrame(bargeFrame(32099, 16000)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-requests:
		t.Fatal("duplicate inference")
	default:
	}
}

func adaptiveTestStream(t *testing.T, predict bargePredictFunc) (*cavosInterruptionStream, time.Time) {
	t.Helper()
	s := newCavosInterruptionStream(context.Background(), predict, nil, "")
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	s.mu.Lock()
	s.now = func() time.Time { return now }
	s.mu.Unlock()
	t.Cleanup(func() { s.Close() })
	return s, now
}
func adaptiveNext(t *testing.T, s *cavosInterruptionStream) (agent.OverlappingSpeechEvent, error) {
	t.Helper()
	type result struct {
		ev  agent.OverlappingSpeechEvent
		err error
	}
	ch := make(chan result, 1)
	go func() { ev, err := s.Next(); ch <- result{ev, err} }()
	select {
	case r := <-ch:
		return r.ev, r.err
	case <-time.After(3 * time.Second):
		t.Fatal("Next blocked")
		return agent.OverlappingSpeechEvent{}, nil
	}
}
func adaptiveOK(context.Context, *spec.BargeInRequest, ...grpc.CallOption) (*spec.BargeInResponse, error) {
	return &spec.BargeInResponse{Probability: .9, Threshold: .7, IsBargeIn: true}, nil
}
func adaptivePrepare(t *testing.T, s *cavosInterruptionStream, now time.Time) {
	t.Helper()
	if err := s.PushFrame(bargeFrame(0, 48000)); err != nil {
		t.Fatal(err)
	}
	if err := s.StartAgentSpeech(); err != nil {
		t.Fatal(err)
	}
	if err := s.StartOverlap(now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
}

func TestAdaptiveImmediateWindowSurvivesAgentBoundaries(t *testing.T) {
	requests := make(chan []byte, 1)
	s, now := adaptiveTestStream(t, func(ctx context.Context, r *spec.BargeInRequest, _ ...grpc.CallOption) (*spec.BargeInResponse, error) {
		requests <- r.Pcm
		return adaptiveOK(ctx, r)
	})
	if err := s.PushFrame(bargeFrame(0, 64000)); err != nil {
		t.Fatal(err)
	}
	s.StartAgentSpeech()
	s.EndAgentSpeech(now)
	s.StartAgentSpeech()
	// Trigger already passed by 0.5 s. Window is [24000, 56000), not the latest 2 s.
	if err := s.StartOverlap(now.Add(-1500 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	ev, err := adaptiveNext(t, s)
	if err != nil || !ev.IsInterruption {
		t.Fatalf("event=%+v err=%v", ev, err)
	}
	pcm := <-requests
	if len(pcm) != 64000 {
		t.Fatalf("bytes=%d", len(pcm))
	}
	for i := 0; i < 32000; i++ {
		if got := int16(binary.LittleEndian.Uint16(pcm[i*2:])); got != int16(24000+i) {
			t.Fatalf("sample %d=%d", i, got)
		}
	}
	s.mu.Lock()
	total := s.total
	s.mu.Unlock()
	if total != 64000 {
		t.Fatalf("timeline reset: %d", total)
	}
}
func TestAdaptiveShortHistoryIsSentWithoutPadding(t *testing.T) {
	requests := make(chan []byte, 1)
	s, now := adaptiveTestStream(t, func(ctx context.Context, r *spec.BargeInRequest, _ ...grpc.CallOption) (*spec.BargeInResponse, error) {
		requests <- r.Pcm
		return adaptiveOK(ctx, r)
	})
	s.StartAgentSpeech()
	s.StartOverlap(now)
	if err := s.PushFrame(bargeFrame(1, 16000)); err != nil {
		t.Fatal(err)
	}
	if _, err := adaptiveNext(t, s); err != nil {
		t.Fatal(err)
	}
	pcm := <-requests
	if len(pcm) != 32000 || binary.LittleEndian.Uint16(pcm) != 1 {
		t.Fatal("adapter padded or truncated short history")
	}
}
func TestAdaptiveSupersededCancellationNeverReachesNext(t *testing.T) {
	entered := make(chan struct{})
	calls := 0
	s, now := adaptiveTestStream(t, func(ctx context.Context, r *spec.BargeInRequest, _ ...grpc.CallOption) (*spec.BargeInResponse, error) {
		calls++
		if calls == 1 {
			close(entered)
			<-ctx.Done()
			return nil, status.Error(codes.Canceled, "owned cancellation")
		}
		return adaptiveOK(ctx, r)
	})
	adaptivePrepare(t, s, now)
	<-entered
	// A repeated boundary must not cancel the current call or send a duplicate.
	s.StartOverlap(now.Add(-time.Second))
	onset := now.Add(-1500 * time.Millisecond)
	if err := s.StartOverlap(onset); err != nil {
		t.Fatal(err)
	}
	ev, err := adaptiveNext(t, s)
	if err != nil || !ev.IsInterruption || ev.OverlapStartedAt.UnixNano() != onset.UnixNano() {
		t.Fatalf("supersession leaked: %+v %v", ev, err)
	}
	s.mu.Lock()
	failed := s.failure
	s.mu.Unlock()
	if failed != nil {
		t.Fatalf("cancel triggered fallback: %v", failed)
	}
}
func TestAdaptivePublishedVerdictCanPrecedeNewOnset(t *testing.T) {
	s, now := adaptiveTestStream(t, adaptiveOK)
	adaptivePrepare(t, s, now)
	// Read old verdict, then replace its overlap. It remains a verdict, not a skip.
	old, err := adaptiveNext(t, s)
	if err != nil {
		t.Fatal(err)
	}
	onset := now.Add(-1500 * time.Millisecond)
	s.StartOverlap(onset)
	ev, err := adaptiveNext(t, s)
	if err != nil || old.OverlapStartedAt.Equal(*ev.OverlapStartedAt) || !ev.OverlapStartedAt.Equal(onset) {
		t.Fatalf("events=%+v %+v err=%v", old, ev, err)
	}
}
func TestAdaptiveAgentEndBeforeTriggerAndDuringRPC(t *testing.T) {
	for _, during := range []bool{false, true} {
		t.Run(fmt.Sprint(during), func(t *testing.T) {
			entered := make(chan struct{})
			released := make(chan struct{})
			s, now := adaptiveTestStream(t, func(ctx context.Context, r *spec.BargeInRequest, _ ...grpc.CallOption) (*spec.BargeInResponse, error) {
				close(entered)
				<-ctx.Done()
				close(released)
				return adaptiveOK(ctx, r)
			})
			if during {
				adaptivePrepare(t, s, now)
				<-entered
			} else {
				s.StartAgentSpeech()
				s.StartOverlap(now)
			}
			s.EndOverlap(now, true)
			s.EndAgentSpeech(now)
			s.EndOverlap(now, true)
			ev, err := adaptiveNext(t, s)
			want := 0
			if during {
				want = 1
				<-released
			}
			if err != nil || !ev.AgentEnded || ev.IsInterruption || ev.NumRequests != want {
				t.Fatalf("event=%+v err=%v", ev, err)
			}
			s.mu.Lock()
			queued := len(s.events)
			s.mu.Unlock()
			if queued != 0 {
				t.Fatalf("duplicate terminal event queued: %d", queued)
			}
			s.Close()
			if _, err := s.Next(); err != io.EOF {
				t.Fatalf("extra event or error after close: %v", err)
			}
		})
	}
}
func TestAdaptiveGRPCErrorsRemainErrors(t *testing.T) {
	for _, code := range []codes.Code{codes.DeadlineExceeded, codes.ResourceExhausted, codes.Unavailable, codes.Unimplemented, codes.Canceled} {
		t.Run(code.String(), func(t *testing.T) {
			s, now := adaptiveTestStream(t, func(context.Context, *spec.BargeInRequest, ...grpc.CallOption) (*spec.BargeInResponse, error) {
				return nil, status.Error(code, "test cause")
			})
			adaptivePrepare(t, s, now)
			_, err := adaptiveNext(t, s)
			if err == nil || err == io.EOF || status.Code(err) != code || !strings.Contains(err.Error(), "test cause") {
				t.Fatalf("lost error: %v", err)
			}
		})
	}
}
func TestAdaptiveMalformedResponsesFail(t *testing.T) {
	for _, r := range []*spec.BargeInResponse{nil, {Probability: float32(math.NaN())}, {Probability: 2}, {Threshold: -1}, {InferenceMs: -1}, {PreprocessMs: float32(math.Inf(1))}} {
		t.Run(fmt.Sprint(r), func(t *testing.T) {
			s, now := adaptiveTestStream(t, func(context.Context, *spec.BargeInRequest, ...grpc.CallOption) (*spec.BargeInResponse, error) {
				return r, nil
			})
			adaptivePrepare(t, s, now)
			if _, err := adaptiveNext(t, s); err == nil || err == io.EOF {
				t.Fatalf("malformed response accepted: %v", err)
			}
		})
	}
}
func TestAdaptiveRejectsMalformedAudioAndFormatChanges(t *testing.T) {
	for _, f := range []*model.AudioFrame{{}, {Data: []byte{1}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}, {Data: make([]byte, 2), SampleRate: 16000, NumChannels: 2, SamplesPerChannel: 1}, {Data: make([]byte, 4), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}} {
		s, _ := adaptiveTestStream(t, adaptiveOK)
		if err := s.PushFrame(f); err == nil {
			t.Fatalf("accepted frame=%+v", f)
		}
	}
	s, _ := adaptiveTestStream(t, adaptiveOK)
	s.PushFrame(bargeFrame(0, 10))
	f := bargeFrame(0, 10)
	f.SampleRate = 48000
	if err := s.PushFrame(f); err == nil {
		t.Fatal("format change silently reset resampler")
	}
}
func TestAdaptiveStreamingResampleMatchesContinuousInput(t *testing.T) {
	for _, rate := range []uint32{24000, 48000} {
		t.Run(fmt.Sprint(rate), func(t *testing.T) {
			whole, _ := adaptiveTestStream(t, adaptiveOK)
			chunked, _ := adaptiveTestStream(t, adaptiveOK)
			data := make([]byte, int(rate)*4)
			for i := 0; i < int(rate); i++ {
				v := int16(10000 * math.Sin(float64(i)*.1))
				binary.LittleEndian.PutUint16(data[i*4:], uint16(v))
				binary.LittleEndian.PutUint16(data[i*4+2:], uint16(v/2))
			}
			if err := whole.PushFrame(&model.AudioFrame{Data: data, SampleRate: rate, NumChannels: 2, SamplesPerChannel: rate}); err != nil {
				t.Fatal(err)
			}
			for start := 0; start < int(rate); {
				end := min(start+137, int(rate))
				if err := chunked.PushFrame(&model.AudioFrame{Data: data[start*4 : end*4], SampleRate: rate, NumChannels: 2, SamplesPerChannel: uint32(end - start)}); err != nil {
					t.Fatal(err)
				}
				start = end
			}
			whole.mu.Lock()
			defer whole.mu.Unlock()
			chunked.mu.Lock()
			defer chunked.mu.Unlock()
			if whole.total != 15992 || chunked.total != 15992 {
				t.Fatalf("counts whole=%d chunked=%d", whole.total, chunked.total)
			}
			if whole.ring != chunked.ring {
				t.Fatal("resampling reset at frame boundaries")
			}
		})
	}
}
func TestAdaptiveSlowRPCDoesNotBlockAudioAndCloseUnblocksNext(t *testing.T) {
	entered := make(chan struct{})
	s, now := adaptiveTestStream(t, func(ctx context.Context, _ *spec.BargeInRequest, _ ...grpc.CallOption) (*spec.BargeInResponse, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	adaptivePrepare(t, s, now)
	<-entered
	done := make(chan error, 1)
	go func() { done <- s.PushFrame(bargeFrame(0, 160000)) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("PushFrame blocked behind RPC")
	}
	next := make(chan error, 1)
	go func() { _, err := s.Next(); next <- err }()
	s.Close()
	s.Close()
	select {
	case err := <-next:
		if err == nil {
			t.Fatal("expected close error")
		}
	case <-time.After(time.Second):
		t.Fatal("Next blocked after close")
	}
}
func TestAdaptiveContextCancellationStopsWorker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := newCavosInterruptionStream(ctx, bargePredictFunc(adaptiveOK), nil, "")
	defer s.Close()
	cancel()
	select {
	case <-s.done:
	case <-time.After(time.Second):
		t.Fatal("worker survived cancellation")
	}
	if _, err := s.Next(); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}
func TestAdaptiveQueueOverflowFailsInsteadOfBlockingBoundaries(t *testing.T) {
	s, now := adaptiveTestStream(t, adaptiveOK)
	s.StartAgentSpeech()
	for i := 0; i < 9; i++ {
		if err := s.StartOverlap(now.Add(time.Duration(i) * time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		err := s.EndOverlap(now, true)
		if i < 8 && err != nil {
			t.Fatal(err)
		}
		if i == 8 && err == nil {
			t.Fatal("overflow silently dropped a verdict")
		}
	}
	if _, err := s.Next(); err == nil || err == io.EOF {
		t.Fatal("overflow did not activate fallback")
	}
}

func TestAdaptiveDumpsEverySentWindowIncludingFailure(t *testing.T) {
	s, now := adaptiveTestStream(t, func(context.Context, *spec.BargeInRequest, ...grpc.CallOption) (*spec.BargeInResponse, error) {
		return nil, status.Error(codes.Unavailable, "offline")
	})
	dir := t.TempDir()
	s.mu.Lock()
	s.dumpDir = dir
	s.mu.Unlock()
	adaptivePrepare(t, s, now)
	adaptiveNext(t, s)
	s.Close()
	data, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("%d.wav", now.Add(-time.Second).UnixNano())))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 64044 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" || binary.LittleEndian.Uint32(data[24:]) != 16000 || binary.LittleEndian.Uint16(data[22:]) != 1 {
		t.Fatalf("invalid WAV len=%d", len(data))
	}
	for i := 0; i < 32000; i++ {
		if got := int16(binary.LittleEndian.Uint16(data[44+i*2:])); got != int16(16000+i) {
			t.Fatalf("WAV sample %d=%d", i, got)
		}
	}
}

type adaptiveGRPCServer struct {
	spec.UnimplementedBargeInServiceV1Server
	requests chan []byte
}

func (s *adaptiveGRPCServer) Predict(ctx context.Context, r *spec.BargeInRequest) (*spec.BargeInResponse, error) {
	s.requests <- r.Pcm
	<-ctx.Done()
	return nil, status.FromContextError(ctx.Err()).Err()
}
func TestAdaptiveGeneratedGRPCClientUsesDeadline(t *testing.T) {
	listener := bufconn.Listen(128 * 1024)
	server := grpc.NewServer()
	svc := &adaptiveGRPCServer{requests: make(chan []byte, 1)}
	spec.RegisterBargeInServiceV1Server(server, svc)
	go server.Serve(listener)
	defer server.Stop()
	defer listener.Close()
	conn, err := grpc.NewClient("passthrough:///barge-test", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	s := newCavosInterruptionStream(context.Background(), spec.NewBargeInServiceV1Client(conn), conn, "")
	defer s.Close()
	s.mu.Lock()
	now := s.now()
	s.now = func() time.Time { return now }
	s.mu.Unlock()
	adaptivePrepare(t, s, now)
	_, err = adaptiveNext(t, s)
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("deadline missing: %v", err)
	}
	if pcm := <-svc.requests; len(pcm) != 64000 {
		t.Fatalf("wire PCM bytes=%d", len(pcm))
	}
}

func TestAdaptiveSupersessionRaceLogsOneTerminalOutcome(t *testing.T) {
	core, observed := observer.New(zap.InfoLevel)
	log := adaptiveTestLogger{Logger: protologger.GetDiscardLogger(), log: zap.New(core).Sugar()}
	original := logger.Logger
	logger.SetLogger(log)
	defer logger.SetLogger(original)
	for trial := 0; trial < 50; trial++ {
		entered := make(chan struct{})
		release := make(chan struct{})
		calls := 0
		s, now := adaptiveTestStream(t, func(ctx context.Context, r *spec.BargeInRequest, _ ...grpc.CallOption) (*spec.BargeInResponse, error) {
			calls++
			if calls == 1 {
				close(entered)
				<-release
			}
			return adaptiveOK(ctx, r)
		})
		adaptivePrepare(t, s, now)
		<-entered
		nextOnset := now.Add(-1500 * time.Millisecond)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); close(release) }()
		go func() { defer wg.Done(); s.StartOverlap(nextOnset) }()
		wg.Wait()
		for {
			ev, err := adaptiveNext(t, s)
			if err != nil {
				t.Fatal(err)
			}
			if ev.OverlapStartedAt.Equal(nextOnset) {
				break
			}
		}
		s.Close()
		outcomes := map[int64]int{}
		for _, entry := range observed.TakeAll() {
			switch entry.Message {
			case "bargein.error":
				t.Fatalf("race logged error: %+v", entry)
			case "bargein.verdict", "bargein.skip":
				fields := entry.ContextMap()
				stamp := fields["overlap_started_at"].(int64)
				outcomes[stamp]++
				if entry.Message == "bargein.skip" && fields["reason"] != "superseded" {
					t.Fatalf("unexpected skip: %+v", fields)
				}
			}
		}
		if outcomes[now.Add(-time.Second).UnixNano()] != 1 || outcomes[nextOnset.UnixNano()] != 1 || len(outcomes) != 2 {
			t.Fatalf("terminal counts=%v", outcomes)
		}
	}
}

// The protocol Zap wrapper replaces its core. Capture through a narrow logger
// implementing the two methods under test, while embedding the discard logger.
type adaptiveTestLogger struct {
	protologger.Logger
	log *zap.SugaredLogger
}

func (l adaptiveTestLogger) Infow(msg string, fields ...any) { l.log.Infow(msg, fields...) }
func (l adaptiveTestLogger) Errorw(msg string, err error, fields ...any) {
	l.log.Errorw(msg, append(fields, "error", err)...)
}

func TestAdaptiveDownmixUsesEveryChannelWithoutNormalization(t *testing.T) {
	s, _ := adaptiveTestStream(t, adaptiveOK)
	// Hand-computed stereo means: 2000, 0 (integer truncation), -32768, 32767.
	pcm := []int16{1000, 3000, -32768, 32767, -32768, -32768, 32767, 32767}
	data := make([]byte, len(pcm)*2)
	for i, v := range pcm {
		binary.LittleEndian.PutUint16(data[i*2:], uint16(v))
	}
	if err := s.PushFrame(&model.AudioFrame{Data: data, SampleRate: 16000, NumChannels: 2, SamplesPerChannel: 4}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.total != 4 {
		t.Fatalf("sample count=%d", s.total)
	}
	for i, want := range []int16{2000, 0, -32768, 32767} {
		if s.ring[i] != want {
			t.Fatalf("sample %d=%d want %d", i, s.ring[i], want)
		}
	}
}

func TestAdaptiveUsesServerDecisionBelowPreviousThreshold(t *testing.T) {
	s, now := adaptiveTestStream(t, func(context.Context, *spec.BargeInRequest, ...grpc.CallOption) (*spec.BargeInResponse, error) {
		return &spec.BargeInResponse{Probability: .4, Threshold: .3, IsBargeIn: true}, nil
	})
	adaptivePrepare(t, s, now)
	ev, err := adaptiveNext(t, s)
	if err != nil || !ev.IsInterruption {
		t.Fatalf("server decision overridden: %+v %v", ev, err)
	}
}
