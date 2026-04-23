package simplismart

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

type SimplismartTTS struct {
	apiKey     string
	voice      string
	baseURL    string
	httpClient *http.Client
}

type TTSOption func(*SimplismartTTS)

func WithTTSURL(url string) TTSOption {
	return func(t *SimplismartTTS) {
		t.baseURL = url
	}
}

func WithTTSHttpClient(client *http.Client) TTSOption {
	return func(t *SimplismartTTS) {
		t.httpClient = client
	}
}

func NewSimplismartTTS(apiKey string, voice string, opts ...TTSOption) *SimplismartTTS {
	if voice == "" {
		voice = "default_voice"
	}
	t := &SimplismartTTS{
		apiKey:     apiKey,
		voice:      voice,
		baseURL:    "https://api.simplismart.live/tts",
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *SimplismartTTS) Label() string { return "simplismart.TTS" }
func (t *SimplismartTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *SimplismartTTS) SampleRate() int { return 24000 }
func (t *SimplismartTTS) NumChannels() int { return 1 }

func (t *SimplismartTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	url := t.baseURL

	reqBody := map[string]interface{}{
		"text":  text,
		"voice": t.voice,
	}

	jsonBody, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.apiKey)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("simplismart tts error: %s", string(respBody))
	}

	return &simplismartTTSChunkedStream{
		resp: resp,
	}, nil
}

func (t *SimplismartTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("simplismart streaming tts not natively supported by basic rest api")
}

type simplismartTTSChunkedStream struct {
	resp *http.Response
}

func (s *simplismartTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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
	return nil, nil
}

func (s *simplismartTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

