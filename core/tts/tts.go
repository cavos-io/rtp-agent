package tts

import (
	"context"
	"encoding/json"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

type SynthesizedAudio struct {
	Frame           *model.AudioFrame `json:"frame"`
	RequestID       string            `json:"request_id"`
	IsFinal         bool              `json:"is_final"`
	SegmentID       string            `json:"segment_id"`
	DeltaText       string            `json:"delta_text"`
	TimedTranscript []TimedString     `json:"timed_transcript,omitempty"`
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

type TTSCapabilities struct {
	Streaming         bool `json:"streaming"`
	AlignedTranscript bool `json:"aligned_transcript"`
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
