package baseten

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/model"
)

type BasetenTTS struct {
	apiKey string
	model  string
}

func NewBasetenTTS(apiKey string, model string) *BasetenTTS {
	if model == "" {
		model = "xtts-v2"
	}
	return &BasetenTTS{
		apiKey: apiKey,
		model:  model,
	}
}

func (t *BasetenTTS) Label() string { return "baseten.TTS" }
func (t *BasetenTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *BasetenTTS) SampleRate() int { return 24000 }
func (t *BasetenTTS) NumChannels() int { return 1 }

func (t *BasetenTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	url := fmt.Sprintf("https://model-%s.api.baseten.co/environments/production/predict", t.model)

	reqBody := map[string]interface{}{
		"text":     text,
		"language": "en",
	}

	jsonBody, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Api-Key "+t.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("baseten tts error: %s", string(respBody))
	}

	return &basetenTTSChunkedStream{
		resp: resp,
	}, nil
}

func (t *BasetenTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("baseten streaming tts not natively supported by basic rest api")
}

type basetenTTSChunkedStream struct {
	resp *http.Response
}

func (s *basetenTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	var result struct {
		Audio string `json:"audio"` // Assuming base64 encoded audio in JSON
	}

	if err := json.NewDecoder(s.resp.Body).Decode(&result); err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, err
	}

	if result.Audio == "" {
		return nil, io.EOF
	}

	data, err := base64.StdEncoding.DecodeString(result.Audio)
	if err != nil {
		return nil, err
	}

	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              data,
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: uint32(len(data) / 2),
		},
	}, nil
}

func (s *basetenTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

