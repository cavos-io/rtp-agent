package stt

import (
	"context"
	"fmt"

	"github.com/cavos-io/conversation-worker/model"
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
	Words            []TimedString
	SourceLanguages  []string
	SourceTexts      []string
	TargetLanguages  []string
	TargetTexts      []string
	Metadata         map[string]any
}

type RecognitionUsage struct {
	AudioDuration float64
	InputTokens   int
	OutputTokens  int
}

type TimedString struct {
	Text            string
	StartTime       float64
	EndTime         float64
	Confidence      float64
	StartTimeOffset float64
	SpeakerID       string
}

type SpeechEvent struct {
	Type             SpeechEventType
	RequestID        string
	Alternatives     []SpeechData
	RecognitionUsage *RecognitionUsage
	SpeechStartTime  *float64
	Interrupted      bool
}

type STTCapabilities struct {
	Streaming         bool
	InterimResults    bool
	Diarization       bool
	AlignedTranscript string
	OfflineRecognize  bool
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

// SpeechStream is a deprecated alias kept for LiveKit Agents API compatibility.
type SpeechStream = RecognizeStream

type StreamTiming interface {
	StartTimeOffset() float64
	SetStartTimeOffset(offset float64)
	StartTime() float64
	SetStartTime(startTime float64)
}

func SetStreamStartTimeOffset(stream StreamTiming, offset float64) {
	stream.SetStartTimeOffset(nonNegativeStreamTime(offset))
}

func SetStreamStartTime(stream StreamTiming, startTime float64) {
	stream.SetStartTime(nonNegativeStreamTime(startTime))
}

func nonNegativeStreamTime(value float64) float64 {
	if value < 0 {
		return 0
	}
	return value
}

type InputEnding interface {
	EndInput() error
}

type SampleRateGuard struct {
	sampleRate uint32
}

func (g *SampleRateGuard) Check(frame *model.AudioFrame) error {
	if frame == nil {
		return nil
	}
	if g.sampleRate == 0 {
		g.sampleRate = frame.SampleRate
		return nil
	}
	if g.sampleRate != frame.SampleRate {
		return fmt.Errorf("stt stream sample rate changed from %d to %d", g.sampleRate, frame.SampleRate)
	}
	return nil
}
