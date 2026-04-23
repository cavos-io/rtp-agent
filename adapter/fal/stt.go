package fal

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/model"
)

type FalSTT struct {
	apiKey     string
	httpClient *http.Client
}

type STTOption func(*FalSTT)

func WithSTTHttpClient(client *http.Client) STTOption {
	return func(s *FalSTT) {
		s.httpClient = client
	}
}

func NewFalSTT(apiKey string, opts ...STTOption) *FalSTT {
	s := &FalSTT{
		apiKey:     apiKey,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *FalSTT) Label() string { return "fal.STT" }
func (s *FalSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: false, InterimResults: false, Diarization: false, OfflineRecognize: true}
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

	// This is a basic implementation assuming fal.ai's Whisper endpoint
	// In reality, Fal often requires a pre-uploaded URL or base64 dataURI
	// We'll use the actual Fal endpoint
	url := "https://fal.run/fal-ai/wizper"

	b64 := base64.StdEncoding.EncodeToString(buf.Bytes())

	body := map[string]interface{}{
		"audio_url": fmt.Sprintf("data:audio/x-wav;base64,%s", b64),
	}
	if language != "" {
		body["language"] = language
	}

	jsonBody, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Key "+s.apiKey)

	resp, err := s.httpClient.Do(req)
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

