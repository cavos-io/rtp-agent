package soniox

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

type SonioxSTT struct {
	apiKey string
}

func NewSonioxSTT(apiKey string) *SonioxSTT {
	return &SonioxSTT{
		apiKey: apiKey,
	}
}

func (s *SonioxSTT) Label() string { return "soniox.STT" }
func (s *SonioxSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: false, InterimResults: false, Diarization: false, OfflineRecognize: true}
}

func (s *SonioxSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	return nil, fmt.Errorf("streaming stt not natively supported by basic rest api")
}

func (s *SonioxSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	url := "https://api.soniox.com/transcribe"

	var buf bytes.Buffer
	for _, f := range frames {
		buf.Write(f.Data)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(buf.Bytes()))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "audio/wav")
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("soniox stt error: %s", string(respBody))
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

