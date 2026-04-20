package tts

import (
	"context"

	"github.com/cavos-io/rtp-agent/model"
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

type ChunkedStream interface {
	Next() (*SynthesizedAudio, error)
	Close() error
}

type SynthesizeStream interface {
	PushText(text string) error
	Flush() error
	Close() error
	Next() (*SynthesizedAudio, error)
}

