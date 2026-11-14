package nativereceive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	model "github.com/cavos-io/rtp-agent/core/audio/model"
	"os"
	"os/signal"
	"testing"
	"time"
)

func TestPipeRejectsPCMBeforeSubscriptionAndMalformedFrames(t *testing.T) {
	for _, input := range []string{`{"event":"pcm","data":"AA=="}`, "{bad}\n"} {
		err := readFrames(context.Background(), bytes.NewBufferString(input), make(chan *model.AudioFrame, 8), make(chan struct{}, 1))
		if err == nil {
			t.Fatal("invalid stream accepted")
		}
	}
}
func TestPipePreservesPCMAndRejectsBacklog(t *testing.T) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.Encode(map[string]any{"event": "helper_protocol", "version": 1})
	e.Encode(map[string]any{"event": "subscribed"})
	pcm := make([]byte, 2400)
	pcm[0] = 42
	e.Encode(map[string]any{"event": "pcm", "data": pcm})
	frames := make(chan *model.AudioFrame, 1)
	ready := make(chan struct{}, 1)
	_ = readFrames(context.Background(), &b, frames, ready)
	if len(ready) != 1 || len(frames) != 1 {
		t.Fatal("missing readiness/PCM")
	}
	if f := <-frames; f.Data[0] != 42 || f.SampleRate != 24000 || f.SamplesPerChannel != 1200 {
		t.Fatal("PCM changed")
	}
	e.Encode(map[string]any{"event": "helper_protocol", "version": 1})
	e.Encode(map[string]any{"event": "subscribed"})
	e.Encode(map[string]any{"event": "pcm", "data": pcm})
	e.Encode(map[string]any{"event": "pcm", "data": pcm})
	if err := readFrames(context.Background(), &b, frames, make(chan struct{}, 1)); err == nil {
		t.Fatal("backlog accepted")
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
		e.Encode(map[string]any{"event": "pcm", "data": make([]byte, 2400)})
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
