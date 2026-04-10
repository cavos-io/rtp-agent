package stt

import (
	"context"

	"github.com/cavos-io/rtp-agent/model"
)

type SpeechEventType string

const (
	SpeechEventStartOfSpeech       SpeechEventType = "start_of_speech"
	SpeechEventInterimTranscript   SpeechEventType = "interim_transcript"
	SpeechEventPreflightTranscript SpeechEventType = "preflight_transcript"
	SpeechEventFinalTranscript     SpeechEventType = "final_transcript"
	SpeechEventRecognitionUsage    SpeechEventType = "recognition_usage"
	SpeechEventEndOfSpeech         SpeechEventType = "end_of_speech"
)

type SpeechData struct {
	Language         string
	Text             string
	StartTime        float64
	EndTime          float64
	Confidence       float64
	SpeakerID        string
	IsPrimarySpeaker *bool
}

type SpeechEvent struct {
	Type         SpeechEventType
	RequestID    string
	Alternatives []SpeechData
	Interrupted  bool
}

type STTCapabilities struct {
	Streaming        bool
	InterimResults   bool
	Diarization      bool
	OfflineRecognize bool
}

type STT interface {
	Label() string
	Capabilities() STTCapabilities
	Stream(ctx context.Context, language string) (RecognizeStream, error)
	Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*SpeechEvent, error)
}

type SearchStream interface {
	PushFrame(frame *model.AudioFrame) error
	Close() error
	Next() (*SpeechEvent, error)
}

type RecognizeStream interface {
	PushFrame(frame *model.AudioFrame) error
	Flush() error
	Close() error
	Next() (*SpeechEvent, error)
}
