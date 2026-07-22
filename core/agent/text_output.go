package agent

import (
	"context"

	"github.com/cavos-io/rtp-agent/core/tts"
)

type TextOutputChunk struct {
	Text  string
	Timed *tts.TimedString
}

type TextOutput interface {
	CaptureText(context.Context, TextOutputChunk) error
	Flush()
}

type TranscriptionNode func(context.Context, <-chan TextOutputChunk) (<-chan TextOutputChunk, error)

func passthroughTranscriptionNode(_ context.Context, input <-chan TextOutputChunk) (<-chan TextOutputChunk, error) {
	return input, nil
}
