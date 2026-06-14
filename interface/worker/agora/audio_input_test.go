package agora

import (
	"context"
	"testing"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

type fakeAudioReceiver struct {
	frames []*model.AudioFrame
}

func (f *fakeAudioReceiver) OnAudioFrame(ctx context.Context, frame *model.AudioFrame) {
	copied := *frame
	copied.Data = append([]byte(nil), frame.Data...)
	f.frames = append(f.frames, &copied)
}

func TestAudioInputConvertsPCMToSessionAudioFrame(t *testing.T) {
	receiver := &fakeAudioReceiver{}
	input := NewAudioInput(context.Background(), receiver)
	data := make([]byte, 320)
	data[0] = 7

	if err := input.HandlePCM(PCMFrame{Data: data, SampleRate: 16000, Channels: 1}); err != nil {
		t.Fatalf("HandlePCM() error = %v", err)
	}
	data[0] = 9

	if len(receiver.frames) != 1 {
		t.Fatalf("received frames = %d, want 1", len(receiver.frames))
	}
	frame := receiver.frames[0]
	if frame.SampleRate != 16000 {
		t.Fatalf("frame sample rate = %d, want 16000", frame.SampleRate)
	}
	if frame.NumChannels != 1 {
		t.Fatalf("frame channels = %d, want 1", frame.NumChannels)
	}
	if frame.SamplesPerChannel != 160 {
		t.Fatalf("frame samples per channel = %d, want 160", frame.SamplesPerChannel)
	}
	if frame.Data[0] != 7 {
		t.Fatalf("frame data was not cloned, first byte = %d, want 7", frame.Data[0])
	}
}

func TestAudioInputHandleAudioFrameClonesBeforeForwarding(t *testing.T) {
	receiver := &fakeAudioReceiver{}
	input := NewAudioInput(context.Background(), receiver)
	frame := &model.AudioFrame{
		Data:              []byte{1, 2, 3},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}

	input.HandleAudioFrame(frame)
	frame.Data[0] = 9

	if len(receiver.frames) != 1 {
		t.Fatalf("received frames = %d, want 1", len(receiver.frames))
	}
	if receiver.frames[0].Data[0] != 1 {
		t.Fatalf("forwarded frame data was not cloned, first byte = %d, want 1", receiver.frames[0].Data[0])
	}
}
