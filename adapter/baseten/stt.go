package baseten

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

type BasetenSTT struct {
	apiKey     string
	model      string
	httpClient *http.Client
}

type STTOption func(*BasetenSTT)

func WithSTTHttpClient(client *http.Client) STTOption {
	return func(s *BasetenSTT) {
		s.httpClient = client
	}
}

func NewBasetenSTT(apiKey string, model string, opts ...STTOption) *BasetenSTT {
	if model == "" {
		model = "whisper-v3"
	}
	s := &BasetenSTT{
		apiKey:     apiKey,
		model:      model,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *BasetenSTT) Label() string { return "baseten.STT" }
func (s *BasetenSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: false, InterimResults: false, Diarization: false, OfflineRecognize: true}
}

func (s *BasetenSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	return nil, fmt.Errorf("baseten streaming stt not natively supported by basic rest api")
}

func (s *BasetenSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	url := fmt.Sprintf("https://model-%s.api.baseten.co/environments/production/predict", s.model)

	var buf bytes.Buffer
	for _, f := range frames {
		buf.Write(f.Data)
	}

	b64 := base64.StdEncoding.EncodeToString(buf.Bytes())

	reqBody := map[string]interface{}{
		"audio": b64,
	}
	if language != "" {
		reqBody["language"] = language
	}

	jsonBody, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Api-Key "+s.apiKey)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("baseten stt error: %s", string(respBody))
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

