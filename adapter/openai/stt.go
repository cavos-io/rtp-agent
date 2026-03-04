package openai

import (
	"bytes"
	"context"
	"fmt"

	"github.com/cavos-io/conversation-worker/core/stt"
	"github.com/cavos-io/conversation-worker/model"
	"github.com/sashabaranov/go-openai"
)

type OpenAISTT struct {
	client *openai.Client
	model  string
}

func NewOpenAISTT(apiKey string, model string) *OpenAISTT {
	if model == "" {
		model = openai.Whisper1
	}
	return &OpenAISTT{
		client: openai.NewClient(apiKey),
		model:  model,
	}
}

func (s *OpenAISTT) Label() string { return "openai.STT" }
func (s *OpenAISTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: false, InterimResults: false, Diarization: false, OfflineRecognize: true}
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

	req := openai.AudioRequest{
		Model:    s.model,
		FilePath: "audio.wav", // Static filename required by API
		Reader:   bytes.NewReader(buf.Bytes()),
		Language: language,
	}

	resp, err := s.client.CreateTranscription(ctx, req)
	if err != nil {
		return nil, err
	}

	return &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			{
				Text: resp.Text,
			},
		},
	}, nil
}
