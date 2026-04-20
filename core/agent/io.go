package agent

import (
	"context"
	"time"

	"github.com/cavos-io/rtp-agent/model"
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
	SetSegmentID(id string)
	Flush()
	OnAttached()
	OnDetached()
}

// VideoInput represents a source of video frames (e.g., camera or remote track)
type VideoInput interface {
	Label() string
	Stream() <-chan *model.VideoFrame
	OnAttached()
	OnDetached()
}

// VideoOutput represents a destination for video frames (e.g., screen or remote track)
type VideoOutput interface {
	Label() string
	CaptureVideoFrame(frame *model.VideoFrame) error
	Flush()
	OnAttached()
	OnDetached()
}

// TextInput represents a source of text (e.g., chat messages or remote text tracks)
type TextInput interface {
	Label() string
	OnAttached()
	OnDetached()
}

type AgentInput struct {
	Audio AudioInput
	Text  TextInput
	Video VideoInput
}

type AgentOutput struct {
	Audio         AudioOutput
	Transcription TextOutput
	Video         VideoOutput
}

type SessionInfo interface {
	LocalParticipantID() string
}

