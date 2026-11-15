package nativereceive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	model "github.com/cavos-io/rtp-agent/core/audio/model"
	"os"
	"os/signal"
	"testing"
	"time"
)

func TestPipeRejectsPCMBeforeSubscriptionAndMalformedFrames(t *testing.T) {
	for _, input := range []string{`{"event":"pcm","data":"AA=="}`, "{bad}\n"} {
		err := readFrames(context.Background(), bytes.NewBufferString(input), make(chan *model.AudioFrame, 8), make(chan struct{}, 1), nil)
		if err == nil {
			t.Fatal("invalid stream accepted")
		}
	}
}
func TestPipePreservesPCM(t *testing.T) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.Encode(map[string]any{"event": "helper_protocol", "version": 1})
	e.Encode(map[string]any{"event": "subscribed"})
	pcm := make([]byte, 2400)
	pcm[0] = 42
	e.Encode(map[string]any{"event": "pcm", "data": pcm})
	frames := make(chan *model.AudioFrame, 1)
	ready := make(chan struct{}, 1)
	_ = readFrames(context.Background(), &b, frames, ready, nil)
	if len(ready) != 1 || len(frames) != 1 {
		t.Fatal("missing readiness/PCM")
	}
	if f := <-frames; f.Data[0] != 42 || f.SampleRate != 24000 || f.SamplesPerChannel != 1200 {
		t.Fatal("PCM changed")
	}

}

// Exercise real pipe/process teardown without RTC or network dependencies.
func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "-mode" && os.Args[2] == "helper" {
		var config struct{ URL string }
		if json.NewDecoder(os.Stdin).Decode(&config) != nil {
			os.Exit(2)
		}
		if config.URL == "invalid" {
			fmt.Fprintln(os.Stdout, "not-json")
			os.Exit(0)
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		e := json.NewEncoder(os.Stdout)
		e.Encode(map[string]any{"event": "helper_protocol", "version": 1})
		e.Encode(map[string]any{"event": "subscribed"})
		count := 1
		if config.URL == "burst" {
			count = 40
		}
		for i := 0; i < count; i++ {
			pcm := make([]byte, 2400)
			pcm[0] = byte(i)
			e.Encode(map[string]any{"event": "pcm", "data": pcm})
		}
		<-ctx.Done()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestRunCancellationAndInvalidChild(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{}, 1)
	frames := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Binary: executable}, ready, func(context.Context, *model.AudioFrame) { frames <- struct{}{} })
	}()
	for _, channel := range []<-chan struct{}{ready, frames} {
		select {
		case <-channel:
		case err := <-done:
			t.Fatalf("early exit: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("startup timeout")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child cleanup timeout")
	}
	if err := Run(context.Background(), Options{Binary: executable, URL: "invalid"}, make(chan struct{}, 1), func(context.Context, *model.AudioFrame) {}); err == nil {
		t.Fatal("malformed child accepted")
	}
}

func TestBurstWaitsForConsumerAndPreservesOrder(t *testing.T) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.Encode(map[string]any{"event": "helper_protocol", "version": 1})
	e.Encode(map[string]any{"event": "subscribed"})
	for i := 0; i < 40; i++ {
		pcm := make([]byte, 2400)
		pcm[0] = byte(i)
		e.Encode(map[string]any{"event": "pcm", "data": pcm})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	frames := make(chan *model.AudioFrame, 8)
	done := make(chan error, 1)
	go func() { done <- readFrames(ctx, &b, frames, make(chan struct{}, 1), nil) }()
	// Let the reader fill its existing eight-frame queue before draining it.
	until := time.After(time.Second)
	for len(frames) != 8 {
		select {
		case err := <-done:
			t.Fatalf("burst failed before drain: %v", err)
		case <-until:
			t.Fatal("queue did not fill")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	select {
	case err := <-done:
		t.Fatalf("burst was rejected instead of backpressured: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	for i := 0; i < 40; i++ {
		select {
		case f := <-frames:
			if f.Data[0] != byte(i) {
				t.Fatalf("frame %d: got %d", i, f.Data[0])
			}
		case <-time.After(time.Second):
			t.Fatalf("lost frame %d", i)
		}
	}
	if err := <-done; err == nil {
		t.Fatal("EOF should still signal a disconnected helper")
	}
}

func TestFullQueueCancellation(t *testing.T) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.Encode(map[string]any{"event": "helper_protocol", "version": 1})
	e.Encode(map[string]any{"event": "subscribed"})
	for i := 0; i < 10; i++ {
		e.Encode(map[string]any{"event": "pcm", "data": make([]byte, 2400)})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	frames := make(chan *model.AudioFrame, 8)
	done := make(chan error, 1)
	go func() { done <- readFrames(ctx, &b, frames, make(chan struct{}, 1), nil) }()
	until := time.After(time.Second)
	for len(frames) != 8 {
		select {
		case <-until:
			t.Fatal("queue did not fill")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked enqueue ignored cancellation")
	}
}

func TestRunStalledConsumerStopsChild(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	stats := new(Stats)
	started := time.Now()
	err = Run(context.Background(), Options{Binary: executable, Stats: stats}, make(chan struct{}, 1), func(ctx context.Context, _ *model.AudioFrame) { <-ctx.Done() })
	if !errors.Is(err, ErrConsumerStalled) {
		t.Fatalf("got %v", err)
	}
	if elapsed := time.Since(started); elapsed < consumerTimeout || elapsed > 6*time.Second {
		t.Fatalf("cleanup elapsed %v", elapsed)
	}
	if stats.delivered.Load() != 1 {
		t.Fatal("consumer did not settle")
	}
}

func TestRunTemporaryConsumerStallRecoversEveryFrame(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stats := new(Stats)
	frames := make(chan byte, 40)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Binary: executable, URL: "burst", Stats: stats}, make(chan struct{}, 1), func(ctx context.Context, f *model.AudioFrame) {
			if f.Data[0] == 0 {
				select {
				case <-time.After(600 * time.Millisecond):
				case <-ctx.Done():
					return
				}
			}
			frames <- f.Data[0]
		})
	}()
	for i := 0; i < 40; i++ {
		select {
		case value := <-frames:
			if value != byte(i) {
				t.Fatalf("frame %d became %d", i, value)
			}
		case err := <-done:
			t.Fatalf("early failure: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatalf("missing frame %d", i)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if stats.received.Load() != 40 || stats.delivered.Load() != 40 || stats.waits.Load() == 0 {
		t.Fatalf("unexpected accounting: %v", stats.Snapshot())
	}
	if stats.maxConsume.Load() < int64(500*time.Millisecond) {
		t.Fatalf("stall not measured: %v", stats.Snapshot())
	}
}
