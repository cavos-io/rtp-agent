package stt

import (
	"context"
	"fmt"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
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

const STTErrorType = "stt_error"

type STTError struct {
	Type        string
	Timestamp   time.Time
	Label       string
	Err         error
	Recoverable bool
}

func NewSTTError(label string, err error, recoverable bool) *STTError {
	return &STTError{
		Type:        STTErrorType,
		Timestamp:   time.Now(),
		Label:       label,
		Err:         err,
		Recoverable: recoverable,
	}
}

func (e *STTError) Error() string {
	if e == nil || e.Err == nil {
		return "stt error"
	}
	return e.Err.Error()
}

func (e *STTError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type STT interface {
	Label() string
	Capabilities() STTCapabilities
	Stream(ctx context.Context, language string) (RecognizeStream, error)
	Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*SpeechEvent, error)
}

type modelProviderSTT interface {
	Model() string
}

type providerProviderSTT interface {
	Provider() string
}

type prewarmProviderSTT interface {
	Prewarm()
}

func Model(stt STT) string {
	if provider, ok := stt.(modelProviderSTT); ok {
		if model := provider.Model(); model != "" {
			return model
		}
	}
	return "unknown"
}

func Provider(stt STT) string {
	if provider, ok := stt.(providerProviderSTT); ok {
		if name := provider.Provider(); name != "" {
			return name
		}
	}
	return "unknown"
}

func Prewarm(stt STT) {
	if provider, ok := stt.(prewarmProviderSTT); ok {
		provider.Prewarm()
	}
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

func streamStartTimeNow() float64 {
	return float64(time.Now().UnixNano()) / float64(time.Second)
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
