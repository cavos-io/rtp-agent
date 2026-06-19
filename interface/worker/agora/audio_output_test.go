package agora

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

type fakePCMPublisher struct {
	frames []PCMFrame
	ctxs   []context.Context
	err    error
}

func (f *fakePCMPublisher) PublishPCM(ctx context.Context, frame PCMFrame) error {
	f.ctxs = append(f.ctxs, ctx)
	if f.err != nil {
		return f.err
	}
	copied := frame
	copied.Data = append([]byte(nil), frame.Data...)
	f.frames = append(f.frames, copied)
	return nil
}

func TestAudioOutputPublishesTenMillisecondPCMFrames(t *testing.T) {
	publisher := &fakePCMPublisher{}
	output := NewAudioOutput(publisher)
	frame := &model.AudioFrame{
		Data:              make([]byte, 320),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 160,
	}

	if err := output.PublishAudio(context.Background(), frame); err != nil {
		t.Fatalf("PublishAudio() error = %v", err)
	}

	if len(publisher.frames) != 1 {
		t.Fatalf("published frames = %d, want 1", len(publisher.frames))
	}
	published := publisher.frames[0]
	if published.SampleRate != 16000 {
		t.Fatalf("published sample rate = %d, want 16000", published.SampleRate)
	}
	if published.Channels != 1 {
		t.Fatalf("published channels = %d, want 1", published.Channels)
	}
	if published.StartPTSMS != 0 {
		t.Fatalf("published StartPTSMS = %d, want 0", published.StartPTSMS)
	}
	if len(published.Data) != 320 {
		t.Fatalf("published data length = %d, want 320", len(published.Data))
	}
}

func TestAudioOutputPublishAudioNormalizesNilContext(t *testing.T) {
	publisher := &fakePCMPublisher{}
	output := NewAudioOutput(publisher)
	frame := &model.AudioFrame{
		Data:              make([]byte, 320),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 160,
	}
	var nilCtx context.Context

	if err := output.PublishAudio(nilCtx, frame); err != nil {
		t.Fatalf("PublishAudio() error = %v", err)
	}
	if len(publisher.ctxs) != 1 {
		t.Fatalf("published contexts = %d, want 1", len(publisher.ctxs))
	}
	if publisher.ctxs[0] == nil {
		t.Fatal("PublishAudio() passed nil context to PCM publisher")
	}
}

func TestAudioOutputPublishAudioRejectsCanceledContext(t *testing.T) {
	publisher := &fakePCMPublisher{}
	output := NewAudioOutput(publisher)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := output.PublishAudio(ctx, &model.AudioFrame{
		Data:              make([]byte, 320),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 160,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PublishAudio() error = %v, want context canceled", err)
	}
	if len(publisher.frames) != 0 {
		t.Fatalf("published frames = %d, want 0", len(publisher.frames))
	}
	if len(publisher.ctxs) != 0 {
		t.Fatalf("publisher calls = %d, want 0", len(publisher.ctxs))
	}
}

func TestAudioOutputPublishAudioRechecksContextAfterLock(t *testing.T) {
	publisher := &fakePCMPublisher{}
	output := NewAudioOutput(publisher)
	frame := &model.AudioFrame{
		Data:              make([]byte, 320),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 160,
	}
	baseCtx, cancel := context.WithCancel(context.Background())
	ctx := &notifyingContext{
		Context:    baseCtx,
		doneCalled: make(chan struct{}),
	}
	output.mu.Lock()
	done := make(chan error, 1)
	go func() {
		done <- output.PublishAudio(ctx, frame)
	}()
	select {
	case <-ctx.doneCalled:
	case <-time.After(time.Second):
		t.Fatal("PublishAudio() did not check context before waiting on lock")
	}
	cancel()
	output.mu.Unlock()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("PublishAudio() error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("PublishAudio() did not finish")
	}
	if len(publisher.frames) != 0 {
		t.Fatalf("published frames = %d, want 0", len(publisher.frames))
	}
}

func TestAudioOutputRejectsPartialSampleFrames(t *testing.T) {
	publisher := &fakePCMPublisher{}
	output := NewAudioOutput(publisher)

	err := output.PublishAudio(context.Background(), &model.AudioFrame{
		Data:              []byte{1, 2, 3},
		SampleRate:        100,
		NumChannels:       2,
		SamplesPerChannel: 1,
	})
	if err == nil {
		t.Fatal("PublishAudio() error = nil, want sample alignment error")
	}
	if !strings.Contains(err.Error(), "whole 16-bit interleaved samples") {
		t.Fatalf("PublishAudio() error = %q, want whole sample alignment error", err.Error())
	}
	if len(publisher.frames) != 0 {
		t.Fatalf("published frames after invalid input = %d, want 0", len(publisher.frames))
	}

	err = output.PublishAudio(context.Background(), &model.AudioFrame{
		Data:              []byte{4, 5, 6, 7},
		SampleRate:        100,
		NumChannels:       2,
		SamplesPerChannel: 1,
	})
	if err != nil {
		t.Fatalf("PublishAudio(valid) error = %v", err)
	}
	if len(publisher.frames) != 1 {
		t.Fatalf("published frames after valid input = %d, want 1", len(publisher.frames))
	}
	if got := publisher.frames[0].Data; len(got) != 4 || got[0] != 4 {
		t.Fatalf("published data = %v, want only valid 4-byte frame", got)
	}
}

func TestAudioOutputRejectsSampleCountMismatch(t *testing.T) {
	publisher := &fakePCMPublisher{}
	output := NewAudioOutput(publisher)

	err := output.PublishAudio(context.Background(), &model.AudioFrame{
		Data:              make([]byte, 320),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 999,
	})
	if err == nil {
		t.Fatal("PublishAudio() error = nil, want sample count mismatch")
	}
	if !strings.Contains(err.Error(), "samples per channel") {
		t.Fatalf("PublishAudio() error = %q, want samples per channel mismatch", err.Error())
	}
	if len(publisher.frames) != 0 {
		t.Fatalf("published frames after invalid metadata = %d, want 0", len(publisher.frames))
	}

	err = output.PublishAudio(context.Background(), &model.AudioFrame{
		Data:              make([]byte, 320),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 160,
	})
	if err != nil {
		t.Fatalf("PublishAudio(valid) error = %v", err)
	}
	if len(publisher.frames) != 1 {
		t.Fatalf("published frames after valid input = %d, want 1", len(publisher.frames))
	}
}

func TestAudioOutputBuffersPartialPCMFrames(t *testing.T) {
	publisher := &fakePCMPublisher{}
	output := NewAudioOutput(publisher)
	first := &model.AudioFrame{
		Data:              make([]byte, 160),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 80,
	}
	second := &model.AudioFrame{
		Data:              make([]byte, 160),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 80,
	}

	if err := output.PublishAudio(context.Background(), first); err != nil {
		t.Fatalf("PublishAudio(first) error = %v", err)
	}
	if len(publisher.frames) != 0 {
		t.Fatalf("published frames after partial input = %d, want 0", len(publisher.frames))
	}
	if err := output.PublishAudio(context.Background(), second); err != nil {
		t.Fatalf("PublishAudio(second) error = %v", err)
	}
	if len(publisher.frames) != 1 {
		t.Fatalf("published frames after completing chunk = %d, want 1", len(publisher.frames))
	}
	if len(publisher.frames[0].Data) != 320 {
		t.Fatalf("published data length = %d, want 320", len(publisher.frames[0].Data))
	}
}

func TestAudioOutputRejectsFormatChangeWithPendingAudio(t *testing.T) {
	publisher := &fakePCMPublisher{}
	output := NewAudioOutput(publisher)

	err := output.PublishAudio(context.Background(), &model.AudioFrame{
		Data:              make([]byte, 160),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 80,
	})
	if err != nil {
		t.Fatalf("PublishAudio(partial) error = %v", err)
	}

	err = output.PublishAudio(context.Background(), &model.AudioFrame{
		Data:              make([]byte, 160),
		SampleRate:        8000,
		NumChannels:       1,
		SamplesPerChannel: 80,
	})
	if err == nil {
		t.Fatal("PublishAudio(format change) error = nil, want error")
	}
	if !strings.Contains(err.Error(), "format changed") {
		t.Fatalf("PublishAudio(format change) error = %q, want format changed", err.Error())
	}
	if len(publisher.frames) != 0 {
		t.Fatalf("published frames = %d, want 0", len(publisher.frames))
	}
}

type notifyingContext struct {
	context.Context
	once       sync.Once
	doneCalled chan struct{}
}

func (c *notifyingContext) Done() <-chan struct{} {
	c.once.Do(func() {
		close(c.doneCalled)
	})
	return c.Context.Done()
}
