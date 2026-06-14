package agora

import (
	"context"
	"fmt"
	"sync"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

type PCMFramePublisher interface {
	PublishPCM(context.Context, PCMFrame) error
}

type AudioOutput struct {
	publisher PCMFramePublisher

	mu         sync.Mutex
	buffer     []byte
	sampleRate int
	channels   int
	nextPTSMS  int64
}

func NewAudioOutput(publisher PCMFramePublisher) *AudioOutput {
	return &AudioOutput{publisher: publisher}
}

func (o *AudioOutput) PublishAudio(ctx context.Context, frame *model.AudioFrame) error {
	ctx = normalizeContext(ctx)
	if o == nil {
		return fmt.Errorf("agora audio output is nil")
	}
	if o.publisher == nil {
		return fmt.Errorf("agora audio output publisher is required")
	}
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	sampleRate := int(frame.SampleRate)
	channels := int(frame.NumChannels)
	if sampleRate <= 0 {
		return fmt.Errorf("agora audio frame sample rate must be positive")
	}
	if channels <= 0 {
		return fmt.Errorf("agora audio frame channels must be positive")
	}
	if sampleRate%100 != 0 {
		return fmt.Errorf("agora audio frame sample rate must produce whole 10 ms frames")
	}
	bytesPer10MS := (sampleRate / 100) * channels * 2
	if bytesPer10MS <= 0 {
		return fmt.Errorf("agora audio frame format is invalid")
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	if len(o.buffer) > 0 && (sampleRate != o.sampleRate || channels != o.channels) {
		return fmt.Errorf("agora audio frame format changed with pending audio")
	}
	if len(o.buffer) == 0 {
		o.sampleRate = sampleRate
		o.channels = channels
	}
	o.buffer = append(o.buffer, frame.Data...)

	for len(o.buffer) >= bytesPer10MS {
		chunk := append([]byte(nil), o.buffer[:bytesPer10MS]...)
		pcm := PCMFrame{
			Data:       chunk,
			SampleRate: sampleRate,
			Channels:   channels,
			StartPTSMS: o.nextPTSMS,
		}
		if err := o.publisher.PublishPCM(ctx, pcm); err != nil {
			return err
		}
		copy(o.buffer, o.buffer[bytesPer10MS:])
		o.buffer = o.buffer[:len(o.buffer)-bytesPer10MS]
		o.nextPTSMS += 10
	}
	return nil
}
