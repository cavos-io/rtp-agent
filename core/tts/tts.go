package tts

import (
	"context"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

type SynthesizedAudio struct {
	Frame           *model.AudioFrame
	RequestID       string
	IsFinal         bool
	SegmentID       string
	DeltaText       string
	TimedTranscript []TimedString
}

type TimedString struct {
	Text            string
	StartTime       float64
	EndTime         float64
	Confidence      float64
	StartTimeOffset float64
	SpeakerID       string
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

type closeProviderTTS interface {
	Close() error
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

func Close(t TTS) error {
	if provider, ok := t.(closeProviderTTS); ok {
		return provider.Close()
	}
	return nil
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
	clone.TimedTranscript = append([]TimedString(nil), audio.TimedTranscript...)
	return &clone
}
