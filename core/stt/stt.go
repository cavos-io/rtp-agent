package stt

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
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
	Language         string         `json:"language"`
	Text             string         `json:"text"`
	StartTime        float64        `json:"start_time"`
	EndTime          float64        `json:"end_time"`
	Confidence       float64        `json:"confidence"`
	SpeakerID        string         `json:"speaker_id"`
	IsPrimarySpeaker *bool          `json:"is_primary_speaker"`
	Words            []TimedString  `json:"words"`
	SourceLanguages  []string       `json:"source_languages"`
	SourceTexts      []string       `json:"source_texts"`
	TargetLanguages  []string       `json:"target_languages"`
	TargetTexts      []string       `json:"target_texts"`
	Metadata         map[string]any `json:"metadata"`
}

func (d SpeechData) MarshalJSON() ([]byte, error) {
	type speechDataPayload struct {
		Language         string         `json:"language"`
		Text             string         `json:"text"`
		StartTime        float64        `json:"start_time"`
		EndTime          float64        `json:"end_time"`
		Confidence       float64        `json:"confidence"`
		SpeakerID        *string        `json:"speaker_id"`
		IsPrimarySpeaker *bool          `json:"is_primary_speaker"`
		Words            []TimedString  `json:"words"`
		SourceLanguages  []string       `json:"source_languages"`
		SourceTexts      []string       `json:"source_texts"`
		TargetLanguages  []string       `json:"target_languages"`
		TargetTexts      []string       `json:"target_texts"`
		Metadata         map[string]any `json:"metadata"`
	}
	return json.Marshal(speechDataPayload{
		Language:         d.Language,
		Text:             d.Text,
		StartTime:        d.StartTime,
		EndTime:          d.EndTime,
		Confidence:       d.Confidence,
		SpeakerID:        optionalStringPointer(d.SpeakerID),
		IsPrimarySpeaker: d.IsPrimarySpeaker,
		Words:            d.Words,
		SourceLanguages:  d.SourceLanguages,
		SourceTexts:      d.SourceTexts,
		TargetLanguages:  d.TargetLanguages,
		TargetTexts:      d.TargetTexts,
		Metadata:         d.Metadata,
	})
}

type RecognitionUsage struct {
	AudioDuration float64 `json:"audio_duration"`
	InputTokens   int     `json:"input_tokens"`
	OutputTokens  int     `json:"output_tokens"`
}

type TimedString struct {
	Text            string  `json:"text"`
	StartTime       float64 `json:"start_time"`
	EndTime         float64 `json:"end_time"`
	Confidence      float64 `json:"confidence"`
	StartTimeOffset float64 `json:"start_time_offset"`
	SpeakerID       string  `json:"speaker_id"`
}

func (s TimedString) MarshalJSON() ([]byte, error) {
	type timedStringPayload struct {
		Text            string  `json:"text"`
		StartTime       float64 `json:"start_time"`
		EndTime         float64 `json:"end_time"`
		Confidence      float64 `json:"confidence"`
		StartTimeOffset float64 `json:"start_time_offset"`
		SpeakerID       *string `json:"speaker_id"`
	}
	return json.Marshal(timedStringPayload{
		Text:            s.Text,
		StartTime:       s.StartTime,
		EndTime:         s.EndTime,
		Confidence:      s.Confidence,
		StartTimeOffset: s.StartTimeOffset,
		SpeakerID:       optionalStringPointer(s.SpeakerID),
	})
}

func optionalStringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

type SpeechEvent struct {
	Type             SpeechEventType   `json:"type"`
	RequestID        string            `json:"request_id"`
	Alternatives     []SpeechData      `json:"alternatives"`
	RecognitionUsage *RecognitionUsage `json:"recognition_usage"`
	SpeechStartTime  *float64          `json:"speech_start_time"`
	Interrupted      bool              `json:"-"`
}

func (e SpeechEvent) MarshalJSON() ([]byte, error) {
	type speechEventPayload SpeechEvent
	payload := speechEventPayload(e)
	if payload.Alternatives == nil {
		payload.Alternatives = []SpeechData{}
	}
	return json.Marshal(payload)
}

func (e *SpeechEvent) UnmarshalJSON(data []byte) error {
	type speechEventPayload SpeechEvent
	var payload speechEventPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	*e = SpeechEvent(payload)
	if e.Alternatives == nil {
		e.Alternatives = []SpeechData{}
	}
	return nil
}

type STTCapabilities struct {
	Streaming         bool   `json:"streaming"`
	InterimResults    bool   `json:"interim_results"`
	Diarization       bool   `json:"diarization"`
	AlignedTranscript string `json:"aligned_transcript"`
	OfflineRecognize  bool   `json:"offline_recognize"`
}

func (c STTCapabilities) MarshalJSON() ([]byte, error) {
	alignedTranscript := any(false)
	if c.AlignedTranscript != "" {
		alignedTranscript = c.AlignedTranscript
	}
	type sttCapabilitiesPayload struct {
		Streaming         bool `json:"streaming"`
		InterimResults    bool `json:"interim_results"`
		Diarization       bool `json:"diarization"`
		AlignedTranscript any  `json:"aligned_transcript"`
		OfflineRecognize  bool `json:"offline_recognize"`
	}
	return json.Marshal(sttCapabilitiesPayload{
		Streaming:         c.Streaming,
		InterimResults:    c.InterimResults,
		Diarization:       c.Diarization,
		AlignedTranscript: alignedTranscript,
		OfflineRecognize:  c.OfflineRecognize,
	})
}

func (c *STTCapabilities) UnmarshalJSON(data []byte) error {
	type sttCapabilitiesPayload struct {
		Streaming         bool            `json:"streaming"`
		InterimResults    bool            `json:"interim_results"`
		Diarization       bool            `json:"diarization"`
		AlignedTranscript json.RawMessage `json:"aligned_transcript"`
		OfflineRecognize  *bool           `json:"offline_recognize"`
	}
	var payload sttCapabilitiesPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}

	alignedTranscript, err := decodeAlignedTranscript(payload.AlignedTranscript)
	if err != nil {
		return err
	}
	c.Streaming = payload.Streaming
	c.InterimResults = payload.InterimResults
	c.Diarization = payload.Diarization
	c.AlignedTranscript = alignedTranscript
	c.OfflineRecognize = true
	if payload.OfflineRecognize != nil {
		c.OfflineRecognize = *payload.OfflineRecognize
	}
	return nil
}

func decodeAlignedTranscript(data json.RawMessage) (string, error) {
	if len(data) == 0 || string(data) == "null" {
		return "", nil
	}
	var enabled bool
	if err := json.Unmarshal(data, &enabled); err == nil {
		if enabled {
			return "", fmt.Errorf("aligned_transcript boolean true is not supported")
		}
		return "", nil
	}
	var granularity string
	if err := json.Unmarshal(data, &granularity); err != nil {
		return "", err
	}
	switch granularity {
	case "", "word", "chunk":
		return granularity, nil
	default:
		return "", fmt.Errorf("unsupported aligned_transcript %s", strconv.Quote(granularity))
	}
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

func (e *STTError) MarshalJSON() ([]byte, error) {
	type sttErrorPayload struct {
		Type        string  `json:"type"`
		Timestamp   float64 `json:"timestamp"`
		Label       string  `json:"label"`
		Recoverable bool    `json:"recoverable"`
	}
	if e == nil {
		return json.Marshal((*sttErrorPayload)(nil))
	}
	return json.Marshal(sttErrorPayload{
		Type:        e.Type,
		Timestamp:   float64(e.Timestamp.UnixNano()) / float64(time.Second),
		Label:       e.Label,
		Recoverable: e.Recoverable,
	})
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
		return fmt.Errorf("the sample rate of the input frames must be consistent")
	}
	return nil
}
