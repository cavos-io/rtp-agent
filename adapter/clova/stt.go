package clova

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/model"
)

type ClovaSTT struct {
	clientID     string
	clientSecret string
	apiURL       string
	httpClient   *http.Client
}

type STTOption func(*ClovaSTT)

func WithSTTBaseURL(url string) STTOption {
	return func(s *ClovaSTT) {
		s.apiURL = url
	}
}

func WithSTTHTTPClient(client *http.Client) STTOption {
	return func(s *ClovaSTT) {
		s.httpClient = client
	}
}

func NewClovaSTT(clientID, clientSecret string, opts ...STTOption) *ClovaSTT {
	s := &ClovaSTT{
		clientID:     clientID,
		clientSecret: clientSecret,
		apiURL:       "https://naveropenapi.apigw.ntruss.com/recog/v1/stt",
		httpClient:   http.DefaultClient,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *ClovaSTT) Label() string { return "clova.STT" }
func (s *ClovaSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: false, InterimResults: false, Diarization: false, OfflineRecognize: true}
}

func (s *ClovaSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	return nil, fmt.Errorf("streaming stt not natively supported by clova basic rest api")
}

func (s *ClovaSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	if language == "" {
		language = "Kor" // Defaulting to Korean as Clova is primarily Korean
	}

	fullURL := fmt.Sprintf("%s?lang=%s", s.apiURL, language)
	
	var buf bytes.Buffer
	for _, f := range frames {
		buf.Write(f.Data)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", fullURL, bytes.NewReader(buf.Bytes()))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-NCP-APIGW-API-KEY-ID", s.clientID)
	req.Header.Set("X-NCP-APIGW-API-KEY", s.clientSecret)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("clova stt error: %s", string(respBody))
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

