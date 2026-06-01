package tts

import (
	"context"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

type SynthesizedAudio struct {
	Frame     *model.AudioFrame
	RequestID string
	IsFinal   bool
	SegmentID string
	DeltaText string
}

type TTSCapabilities struct {
	Streaming         bool
	AlignedTranscript bool
}

type TTS interface {
	Label() string
	Capabilities() TTSCapabilities
	SampleRate() int
	NumChannels() int
	Synthesize(ctx context.Context, text string) (ChunkedStream, error)
	Stream(ctx context.Context) (SynthesizeStream, error)
}

type modelProviderTTS interface {
	Model() string
}

type providerProviderTTS interface {
	Provider() string
}

type prewarmProviderTTS interface {
	Prewarm()
}

func Model(t TTS) string {
	if provider, ok := t.(modelProviderTTS); ok {
		if model := provider.Model(); model != "" {
			return model
		}
	}
	return "unknown"
}

func Provider(t TTS) string {
	if provider, ok := t.(providerProviderTTS); ok {
		if name := provider.Provider(); name != "" {
			return name
		}
	}
	return "unknown"
}

func Prewarm(t TTS) {
	if provider, ok := t.(prewarmProviderTTS); ok {
		provider.Prewarm()
	}
}

type ChunkedStream interface {
	Next() (*SynthesizedAudio, error)
	Close() error
}

type DoneStream interface {
	Done() bool
}

type ExceptionStream interface {
	Exception() error
}

type SynthesizeStream interface {
	PushText(text string) error
	Flush() error
	Close() error
	Next() (*SynthesizedAudio, error)
}

func cloneSynthesizedAudio(audio *SynthesizedAudio) *SynthesizedAudio {
	if audio == nil {
		return nil
	}
	clone := *audio
	return &clone
}
