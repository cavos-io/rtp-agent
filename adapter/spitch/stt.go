package spitch

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

type SpitchSTT struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

type Option func(*SpitchSTT)

func WithBaseURL(url string) Option {
	return func(s *SpitchSTT) {
		s.baseURL = url
	}
}

func WithHTTPClient(client *http.Client) Option {
	return func(s *SpitchSTT) {
		s.httpClient = client
	}
}

func NewSpitchSTT(apiKey string, opts ...Option) *SpitchSTT {
	s := &SpitchSTT{
		apiKey:     apiKey,
		baseURL:    "https://api.spitch.ai/stt/v1/recognize",
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *SpitchSTT) Label() string { return "spitch.STT" }
func (s *SpitchSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: false, InterimResults: false, Diarization: false, OfflineRecognize: true}
}

func (s *SpitchSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	return nil, fmt.Errorf("spitch streaming stt not natively supported by basic rest api")
}

func (s *SpitchSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {

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

	req, err := http.NewRequestWithContext(ctx, "POST", s.baseURL, body)
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
		return nil, fmt.Errorf("spitch stt error: %s", string(respBody))
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

