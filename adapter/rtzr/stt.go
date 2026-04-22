package rtzr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/model"
)

type RtzrSTT struct {
	apiKey     string
	apiURL     string
	httpClient *http.Client
}

type STTOption func(*RtzrSTT)

func WithSTTBaseURL(url string) STTOption {
	return func(s *RtzrSTT) {
		s.apiURL = url
	}
}

func WithHTTPClient(client *http.Client) STTOption {
	return func(s *RtzrSTT) {
		s.httpClient = client
	}
}

func NewRtzrSTT(apiKey string, opts ...STTOption) *RtzrSTT {
	s := &RtzrSTT{
		apiKey:     apiKey,
		apiURL:     "https://api.rtzr.ai/v1/stt",
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *RtzrSTT) Label() string { return "rtzr.STT" }
func (s *RtzrSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: false, InterimResults: false, Diarization: false, OfflineRecognize: true}
}

func (s *RtzrSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	return nil, fmt.Errorf("streaming stt not natively supported by basic rest api")
}

func (s *RtzrSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	url := "https://api.rtzr.ai/v1/stt"

	var buf bytes.Buffer
	for _, f := range frames {
		buf.Write(f.Data)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "audio.wav")
	part.Write(buf.Bytes())
	if language != "" {
		writer.WriteField("language", language)
	}
	writer.Close()

	req, err := http.NewRequestWithContext(ctx, "POST", s.apiURL, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("rtzr stt error: %s", string(respBody))
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

