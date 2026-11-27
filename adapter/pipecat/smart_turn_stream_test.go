package pipecat

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

func turnFrame(values ...int16) *model.AudioFrame {
	data := make([]byte, len(values)*2)
	for i, v := range values {
		binary.LittleEndian.PutUint16(data[i*2:], uint16(v))
	}
	return &model.AudioFrame{Data: data, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: uint32(len(values))}
}
func TestSmartTurnStreamDiscardPreservesAcceptedAndFutureAudio(t *testing.T) {
	var got []float32
	d := NewSmartTurn(WithProbabilityEstimator(func(_ context.Context, samples []float32) (float64, error) {
		got = append([]float32(nil), samples...)
		return .9, nil
	}))
	defer d.Close()
	stream, err := d.Stream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	frame := turnFrame(100, 200, 300, 400, 500)
	if err := stream.PushAudio(frame, 0); err != nil {
		t.Fatal(err)
	}
	frame.Data[0] = 0 // stream owns its audio
	if err := stream.DiscardAudio(sampleDuration(1), sampleDuration(3)); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Predict(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i, want := range []float32{100, 400, 500} {
		if got[len(got)-3+i] != want/32768 {
			t.Fatalf("retained tail %v", got[len(got)-3:])
		}
	}
	if err := stream.Reset(sampleDuration(4)); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Predict(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got[len(got)-1] != 500.0/32768 || got[len(got)-2] != 0 {
		t.Fatal("reset erased future audio or retained old audio")
	}
}
func TestSmartTurnStreamIsolationCloseAndCancellation(t *testing.T) {
	entered := make(chan struct{}, 1)
	d := NewSmartTurn(WithProbabilityEstimator(func(ctx context.Context, _ []float32) (float64, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return 0, ctx.Err()
	}))
	defer d.Close()
	s1, _ := d.Stream(context.Background())
	s2, _ := d.Stream(context.Background())
	defer s2.Close()
	_ = s1.PushAudio(turnFrame(1, 2), 0)
	if len(s2.(*smartTurnStream).audio) != 0 {
		t.Fatal("shared audio")
	}
	done := make(chan error, 1)
	go func() { _, err := s1.Predict(context.Background()); done <- err }()
	<-entered
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("close prediction %v", err)
	}
	if err := s1.PushAudio(turnFrame(1), 0); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal("push after close")
	}
	if err := s2.PushAudio(turnFrame(3), 0); err != nil {
		t.Fatal("sibling closed")
	}
	if err := s2.EndInput(); err != nil {
		t.Fatal(err)
	}
	if err := s2.PushAudio(turnFrame(3), 0); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal("push after end")
	}
	_ = s1.Close()
}
func TestSmartTurnStreamConcurrentPredictions(t *testing.T) {
	d := NewSmartTurn(WithProbabilityEstimator(func(ctx context.Context, x []float32) (float64, error) { return float64(x[len(x)-1]), nil }))
	defer d.Close()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := d.Stream(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			defer s.Close()
			_ = s.PushAudio(turnFrame(16384), 0)
			result, err := s.Predict(context.Background())
			if err != nil || result.Probability != .5 || result.IsComplete {
				t.Errorf("%+v %v", result, err)
			}
		}()
	}
	wg.Wait()
}
func TestSmartTurnStreamBoundsAndValidation(t *testing.T) {
	d := NewSmartTurn()
	defer d.Close()
	stream, _ := d.Stream(context.Background())
	s := stream.(*smartTurnStream)
	defer s.Close()
	for i := 0; i < 10; i++ {
		values := make([]int16, 16000)
		if err := s.PushAudio(turnFrame(values...), time.Duration(i)*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	count := 0
	for _, chunk := range s.audio {
		count += len(chunk.samples)
	}
	if count != 128000 {
		t.Fatalf("buffer samples %d", count)
	}
	if err := s.PushAudio(&model.AudioFrame{Data: []byte{1}}, 0); err == nil {
		t.Fatal("invalid frame accepted")
	}
	if !s.SupportsLanguage("id-ID") || s.SupportsLanguage("xx") {
		t.Fatal("language support")
	}
	stereo := &model.AudioFrame{SampleRate: 48000, NumChannels: 2, SamplesPerChannel: 480, Data: make([]byte, 1920)}
	if err := s.PushAudio(stereo, 10*time.Second); err != nil {
		t.Fatal(err)
	}
}
