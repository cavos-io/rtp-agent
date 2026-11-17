package nativeroom

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	model "github.com/cavos-io/rtp-agent/core/audio/model"
)

func frame(n int) *model.AudioFrame {
	return &model.AudioFrame{Data: make([]byte, n*2), SampleRate: 24000, NumChannels: 1, SamplesPerChannel: uint32(n)}
}
func TestPlaybackDrainTailAndClear(t *testing.T) {
	var commands []string
	var samples int
	p := newPlayback(context.Background(), func(_ context.Context, event string, data []byte) error {
		commands = append(commands, event)
		samples += len(data) / 2
		return nil
	}, func(*model.AudioFrame) {}, func(error) {})
	if err := p.Publish(context.Background(), frame(2401)); err != nil {
		t.Fatal(err)
	}
	p.Flush()
	if samples != 2401 {
		t.Fatalf("tail lost or padded: %d", samples)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := p.WaitForPlayout(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("capture treated as playout: %v", err)
	}
	p.ClearBuffer()
	result, err := p.WaitForPlayout(context.Background())
	if err != nil || !result.Interrupted || result.PlaybackPosition >= 100*time.Millisecond {
		t.Fatalf("bad interruption: %+v %v", result, err)
	}
	if commands[len(commands)-1] != "clear" {
		t.Fatal(commands)
	}
	if err := p.Publish(context.Background(), frame(240)); err != nil {
		t.Fatal(err)
	}
	p.Flush()
	result, err = p.WaitForPlayout(context.Background())
	if err != nil || result.Interrupted || result.PlaybackPosition != 10*time.Millisecond {
		t.Fatalf("next segment: %+v %v", result, err)
	}
}
func TestClearWaitsForPendingCapture(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var commands []string
	p := newPlayback(context.Background(), func(_ context.Context, e string, _ []byte) error {
		mu.Lock()
		commands = append(commands, e)
		mu.Unlock()
		if e == "capture" {
			close(entered)
			<-release
		}
		return nil
	}, func(*model.AudioFrame) {}, func(error) {})
	published := make(chan error, 1)
	go func() { published <- p.Publish(context.Background(), frame(100)) }()
	<-entered
	cleared := make(chan struct{})
	go func() { p.ClearBuffer(); close(cleared) }()
	select {
	case <-cleared:
		t.Fatal("clear overtook capture")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-published; err != nil {
		t.Fatal(err)
	}
	<-cleared
	mu.Lock()
	defer mu.Unlock()
	if len(commands) != 2 || commands[0] != "capture" || commands[1] != "clear" {
		t.Fatal(commands)
	}
}
func TestPlaybackFailureVisible(t *testing.T) {
	failure := errors.New("native capture failed")
	var reported error
	p := newPlayback(context.Background(), func(context.Context, string, []byte) error { return failure }, func(*model.AudioFrame) {}, func(err error) { reported = err })
	if err := p.Publish(context.Background(), frame(200)); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if _, err := p.WaitForPlayout(context.Background()); !errors.Is(err, failure) || reported != failure {
		t.Fatal(err, reported)
	}
}

func TestPublishCancellationDoesNotFailHelper(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := newPlayback(context.Background(), func(c context.Context, _ string, _ []byte) error { cancel(); return c.Err() }, func(*model.AudioFrame) {}, func(err error) { t.Errorf("interruption treated as helper failure: %v", err) })
	if err := p.Publish(ctx, frame(100)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if p.err != nil {
		t.Fatal(p.err)
	}
}
func TestClearStopsLongPublishBeforeNextCapture(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var captureCount int
	p := newPlayback(context.Background(), func(_ context.Context, event string, _ []byte) error {
		if event == "capture" {
			captureCount++
			if captureCount == 1 {
				close(entered)
				<-release
			}
		}
		return nil
	}, func(*model.AudioFrame) {}, func(err error) { t.Errorf("interruption failed helper: %v", err) })
	done := make(chan error, 1)
	go func() { done <- p.Publish(context.Background(), frame(24000)) }()
	<-entered
	cleared := make(chan struct{})
	go func() { p.ClearBuffer(); close(cleared) }()
	deadline := time.Now().Add(time.Second)
	for p.epoch.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if p.epoch.Load() == 0 {
		t.Fatal("clear did not enter")
	}
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	<-cleared
	if captureCount != 1 {
		t.Fatalf("cleared generation kept submitting %d captures", captureCount)
	}
}

func TestPlaybackResamplesStereoWithoutPadding(t *testing.T) {
	var samples int
	p := newPlayback(context.Background(), func(_ context.Context, event string, data []byte) error {
		if event == "capture" {
			samples += len(data) / 2
		}
		return nil
	}, func(*model.AudioFrame) {}, func(err error) { t.Fatal(err) })
	for range 3 {
		f := &model.AudioFrame{SampleRate: 48000, NumChannels: 2, SamplesPerChannel: 960, Data: make([]byte, 3840)}
		if err := p.Publish(context.Background(), f); err != nil {
			t.Fatal(err)
		}
	}
	p.Flush()
	if samples != 1440 {
		t.Fatalf("resampled length=%d want1440", samples)
	}
}

func TestOldPlayoutWaitKeepsInterruptedSegmentResult(t *testing.T) {
	p := newPlayback(context.Background(), func(context.Context, string, []byte) error { return nil }, func(*model.AudioFrame) {}, func(err error) { t.Fatal(err) })
	if err := p.Publish(context.Background(), frame(24000)); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	old := p.completion
	p.mu.Unlock()
	p.ClearBuffer()
	if err := p.Publish(context.Background(), frame(12000)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result, err := p.waitForSegment(ctx, old)
	if err != nil || !result.Interrupted {
		t.Fatalf("old waiter followed new segment: %+v %v", result, err)
	}
	p.ClearBuffer()
}
