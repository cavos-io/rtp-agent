package fal

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/cavos-io/conversation-worker/core/stt"
	"github.com/cavos-io/conversation-worker/model"
)

type FalSTT struct {
	apiKey     string
	language   string
	task       string
	chunkLevel string
	version    string
}

type FalSTTOption func(*FalSTT)

func WithFalSTTLanguage(language string) FalSTTOption {
	return func(s *FalSTT) {
		if language != "" {
			s.language = language
		}
	}
}

func WithFalSTTTask(task string) FalSTTOption {
	return func(s *FalSTT) {
		if task != "" {
			s.task = task
		}
	}
}

func WithFalSTTChunkLevel(chunkLevel string) FalSTTOption {
	return func(s *FalSTT) {
		if chunkLevel != "" {
			s.chunkLevel = chunkLevel
		}
	}
}

func WithFalSTTVersion(version string) FalSTTOption {
	return func(s *FalSTT) {
		if version != "" {
			s.version = version
		}
	}
}

func NewFalSTT(apiKey string, opts ...FalSTTOption) *FalSTT {
	provider := &FalSTT{
		apiKey:     apiKey,
		language:   "en",
		task:       "transcribe",
		chunkLevel: "segment",
		version:    "3",
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *FalSTT) Label() string { return "fal.STT" }
func (s *FalSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: false, InterimResults: true, Diarization: false, OfflineRecognize: true}
}

func (s *FalSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	return nil, fmt.Errorf("streaming stt not natively supported by standard fal REST API")
}

func (s *FalSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	// For Fal, we typically need to provide an audio URL or base64 encoded data
	var buf bytes.Buffer
	for _, f := range frames {
		buf.Write(f.Data)
	}

	req, err := buildFalSTTRequest(ctx, s, buf.Bytes(), language)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fal stt error: %s", string(respBody))
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			{Text: result.Text},
		},
	}, nil
}

func buildFalSTTRequest(ctx context.Context, s *FalSTT, audio []byte, language string) (*http.Request, error) {
	b64 := base64.StdEncoding.EncodeToString(audio)
	resolvedLanguage := s.language
	if language != "" {
		resolvedLanguage = language
	}

	body := map[string]interface{}{
		"audio_url":   fmt.Sprintf("data:audio/x-wav;base64,%s", b64),
		"task":        s.task,
		"language":    resolvedLanguage,
		"chunk_level": s.chunkLevel,
		"version":     s.version,
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://fal.run/fal-ai/wizper", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Key "+s.apiKey)
	return req, nil
}
