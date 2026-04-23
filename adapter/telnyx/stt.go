package telnyx

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

type TelnyxSTT struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

type STTOption func(*TelnyxSTT)

func WithSTTBaseURL(url string) STTOption {
	return func(s *TelnyxSTT) {
		s.baseURL = url
	}
}

func WithSTTHTTPClient(client *http.Client) STTOption {
	return func(s *TelnyxSTT) {
		s.httpClient = client
	}
}

func NewTelnyxSTT(apiKey string, opts ...STTOption) *TelnyxSTT {
	s := &TelnyxSTT{
		apiKey:     apiKey,
		baseURL:    "https://api.telnyx.com/v2/ai/audio/transcriptions",
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *TelnyxSTT) Label() string { return "telnyx.STT" }
func (s *TelnyxSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: false, InterimResults: false, Diarization: false, OfflineRecognize: true}
}

func (s *TelnyxSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	return nil, fmt.Errorf("telnyx streaming stt not natively supported by basic rest api")
}

func (s *TelnyxSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {

	var buf bytes.Buffer
	for _, f := range frames {
		buf.Write(f.Data)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "audio.wav")
	part.Write(buf.Bytes())
	writer.WriteField("model", "whisper-1")
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
		return nil, fmt.Errorf("telnyx stt error: %s", string(respBody))
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

