package agora

import (
	"context"
	"fmt"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

type AudioFrameReceiver interface {
	OnAudioFrame(context.Context, *model.AudioFrame)
}

type AudioHandler func(*model.AudioFrame)

type AudioInput struct {
	ctx      context.Context
	receiver AudioFrameReceiver
}

func NewAudioInput(ctx context.Context, receiver AudioFrameReceiver) *AudioInput {
	if ctx == nil {
		ctx = context.Background()
	}
	return &AudioInput{ctx: ctx, receiver: receiver}
}

func (i *AudioInput) HandlePCM(frame PCMFrame) error {
	audioFrame, err := AudioFrameFromPCM(frame)
	if err != nil {
		return err
	}
	i.HandleAudioFrame(audioFrame)
	return nil
}

func (i *AudioInput) HandleAudioFrame(frame *model.AudioFrame) {
	if i == nil || i.receiver == nil || frame == nil || len(frame.Data) == 0 {
		return
	}
	select {
	case <-i.ctx.Done():
		return
	default:
	}
	cloned := *frame
	cloned.Data = append([]byte(nil), frame.Data...)
	i.receiver.OnAudioFrame(i.ctx, &cloned)
}

func AudioFrameFromPCM(frame PCMFrame) (*model.AudioFrame, error) {
	if err := frame.Validate(); err != nil {
		return nil, err
	}
	bytesPerSample := 2
	samplesPerChannel := len(frame.Data) / frame.Channels / bytesPerSample
	if samplesPerChannel <= 0 {
		return nil, fmt.Errorf("agora PCM frame samples per channel must be positive")
	}
	return &model.AudioFrame{
		Data:              append([]byte(nil), frame.Data...),
		SampleRate:        uint32(frame.SampleRate),
		NumChannels:       uint32(frame.Channels),
		SamplesPerChannel: uint32(samplesPerChannel),
	}, nil
}
