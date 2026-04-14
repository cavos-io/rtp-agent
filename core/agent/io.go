package agent

import (
	"context"

	"github.com/cavos-io/conversation-worker/model"
)

// AudioInput represents a source of audio frames (e.g., mic or remote track)
type AudioInput interface {
	Label() string
	Stream() <-chan *model.AudioFrame
	OnAttached()
	OnDetached()
}

// AudioOutput represents a destination for audio frames (e.g., speakers or remote track)
type AudioOutput interface {
	Label() string
	CaptureFrame(frame *model.AudioFrame) error
	Flush()
	WaitForPlayout(ctx context.Context) error
	ClearBuffer()
	OnAttached()
	OnDetached()
	Pause()
	Resume()
}

// TextOutput represents a destination for text (e.g., transcriptions)
type TextOutput interface {
	Label() string
	CaptureText(text string) error
	Flush()
	OnAttached()
	OnDetached()
}

type AgentInput struct {
	Audio AudioInput
}

type AgentOutput struct {
	Audio         AudioOutput
	Transcription TextOutput
}
