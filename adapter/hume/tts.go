package hume

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/model"
)

type HumeTTS struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
}

type TTSOption func(*HumeTTS)

func WithTTSURL(url string) TTSOption {
	return func(t *HumeTTS) {
		t.baseURL = url
	}
}

func WithTTSHttpClient(client *http.Client) TTSOption {
	return func(t *HumeTTS) {
		t.httpClient = client
	}
}

func NewHumeTTS(apiKey string, model string, opts ...TTSOption) *HumeTTS {
	if model == "" {
		model = "hume-tts"
	}
	t := &HumeTTS{
		apiKey:     apiKey,
		model:      model,
		baseURL:    "https://api.hume.ai/v0/evi/tts",
		httpClient: http.DefaultClient,
	}

	for _, opt := range opts {
		opt(t)
	}

	return t
}

func (t *HumeTTS) Label() string { return "hume.TTS" }
func (t *HumeTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *HumeTTS) SampleRate() int { return 24000 }
func (t *HumeTTS) NumChannels() int { return 1 }

func (t *HumeTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	url := t.baseURL

	body := map[string]interface{}{
		"text":  text,
		"model": t.model,
	}

	jsonBody, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hume-Api-Key", t.apiKey)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("hume tts error: %s", string(respBody))
	}

	return &humeTTSChunkedStream{
		resp: resp,
	}, nil
}

func (t *HumeTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("streaming tts not natively supported by basic hume rest api")
}

type humeTTSChunkedStream struct {
	resp *http.Response
}

func (s *humeTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	buf := make([]byte, 4096)
	n, err := s.resp.Body.Read(buf)
	if n > 0 {
		return &tts.SynthesizedAudio{
			Frame: &model.AudioFrame{
				Data:              buf[:n],
				SampleRate:        24000,
				NumChannels:       1,
				SamplesPerChannel: uint32(n / 2),
			},
		}, nil
	}
	if err != nil {
		return nil, err
	}
	return nil, nil // Should not happen with well-behaved readers
}

func (s *humeTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

