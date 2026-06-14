package agora

import (
	"context"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

type fakePCMPublisher struct {
	frames []PCMFrame
	err    error
}

func (f *fakePCMPublisher) PublishPCM(ctx context.Context, frame PCMFrame) error {
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
