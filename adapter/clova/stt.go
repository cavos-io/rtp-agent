package clova

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/cavos-io/conversation-worker/core/stt"
	"github.com/cavos-io/conversation-worker/model"
)

type ClovaSTT struct {
	clientID     string
	clientSecret string
}

func NewClovaSTT(clientID, clientSecret string) *ClovaSTT {
	return &ClovaSTT{
		clientID:     clientID,
		clientSecret: clientSecret,
	}
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

	apiURL := fmt.Sprintf("https://naveropenapi.apigw.ntruss.com/recog/v1/stt?lang=%s", language)

	var buf bytes.Buffer
	for _, f := range frames {
		buf.Write(f.Data)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(buf.Bytes()))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-NCP-APIGW-API-KEY-ID", s.clientID)
	req.Header.Set("X-NCP-APIGW-API-KEY", s.clientSecret)

	resp, err := http.DefaultClient.Do(req)
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
