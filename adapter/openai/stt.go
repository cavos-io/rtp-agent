package openai

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/sashabaranov/go-openai"
)

type OpenAISTT struct {
	client         *openai.Client
	model          string
	language       string
	detectLanguage bool
	prompt         string
}

type OpenAISTTOption func(*OpenAISTT)

func WithOpenAISTTLanguage(language string) OpenAISTTOption {
	return func(s *OpenAISTT) {
		s.language = language
	}
}

func WithOpenAISTTDetectLanguage(detect bool) OpenAISTTOption {
	return func(s *OpenAISTT) {
		s.detectLanguage = detect
	}
}

func WithOpenAISTTPrompt(prompt string) OpenAISTTOption {
	return func(s *OpenAISTT) {
		s.prompt = prompt
	}
}

func NewOpenAISTT(apiKey string, model string, opts ...OpenAISTTOption) *OpenAISTT {
	if model == "" {
		model = "gpt-4o-mini-transcribe"
	}
	provider := &OpenAISTT{
		client:   openai.NewClient(apiKey),
		model:    model,
		language: "en",
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *OpenAISTT) Label() string { return "openai.STT" }
func (s *OpenAISTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: false, InterimResults: false, Diarization: false, AlignedTranscript: "word", OfflineRecognize: true}
}

func (s *OpenAISTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	// OpenAI does not have a native streaming API for STT (Whisper) via standard REST.
	// We'd have to implement chunking ourselves or use another provider.
	return nil, fmt.Errorf("streaming stt not yet implemented for openai")
}

func (s *OpenAISTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	// Concatenate frames into a single buffer
	var buf bytes.Buffer
	for _, f := range frames {
		buf.Write(f.Data)
	}

	req := openAIAudioRequest(s, bytes.NewReader(buf.Bytes()), language)

	resp, err := s.client.CreateTranscription(ctx, req)
	if err != nil {
		return nil, err
	}

	return openAISpeechEvent(resp), nil
}

func openAIAudioRequest(s *OpenAISTT, reader io.Reader, language string) openai.AudioRequest {
	requestLanguage := s.language
	if language != "" {
		requestLanguage = language
	}
	if s.detectLanguage {
		requestLanguage = ""
	}
	return openai.AudioRequest{
		Model:    s.model,
		FilePath: "audio.wav", // Static filename required by API when Reader is used.
		Reader:   reader,
		Language: requestLanguage,
		Prompt:   s.prompt,
		Format:   openai.AudioResponseFormatVerboseJSON,
		TimestampGranularities: []openai.TranscriptionTimestampGranularity{
			openai.TranscriptionTimestampGranularityWord,
		},
	}
}

func openAISpeechEvent(resp openai.AudioResponse) *stt.SpeechEvent {
	return &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			{
				Text:  resp.Text,
				Words: openAITimedStrings(resp.Words),
			},
		},
	}
}

func openAITimedStrings(words []struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}) []stt.TimedString {
	if len(words) == 0 {
		return nil
	}

	timed := make([]stt.TimedString, 0, len(words))
	for _, word := range words {
		timed = append(timed, stt.TimedString{
			Text:      word.Word,
			StartTime: word.Start,
			EndTime:   word.End,
		})
	}
	return timed
}
