package agent

import (
	"context"
	"time"

	"github.com/cavos-io/conversation-worker/model"
)

// AudioInput represents a source of audio frames (e.g., mic or remote track)
type AudioInput interface {
	Label() string
	Stream() <-chan *model.AudioFrame
	OnAttached()
	OnDetached()
}

type PlaybackStartedEvent struct {
	CreatedAt time.Time
}

type PlaybackFinishedEvent struct {
	PlaybackPosition      time.Duration
	Interrupted           bool
	SynchronizedTranscript string
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
	OnPlaybackStarted(func(ev PlaybackStartedEvent))
	OnPlaybackFinished(func(ev PlaybackFinishedEvent))
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
