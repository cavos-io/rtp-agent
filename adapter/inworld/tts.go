package inworld

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

type InworldTTS struct {
	apiKey     string
	voice      string
	baseURL    string
	httpClient *http.Client
}

type TTSOption func(*InworldTTS)

func WithTTSURL(url string) TTSOption {
	return func(t *InworldTTS) {
		t.baseURL = url
	}
}

func WithTTSHttpClient(client *http.Client) TTSOption {
	return func(t *InworldTTS) {
		t.httpClient = client
	}
}

func NewInworldTTS(apiKey string, voice string, opts ...TTSOption) *InworldTTS {
	if voice == "" {
		voice = "default_voice"
	}
	t := &InworldTTS{
		apiKey:     apiKey,
		voice:      voice,
		baseURL:    "https://api.inworld.ai/v1/tts",
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *InworldTTS) Label() string { return "inworld.TTS" }
func (t *InworldTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *InworldTTS) SampleRate() int { return 24000 }
func (t *InworldTTS) NumChannels() int { return 1 }

func (t *InworldTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
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
		return nil, fmt.Errorf("inworld tts error: %s", string(respBody))
	}

	return &inworldTTSChunkedStream{
		resp: resp,
	}, nil
}

func (t *InworldTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("inworld streaming tts not natively supported by basic rest api")
}

type inworldTTSChunkedStream struct {
	resp *http.Response
}

func (s *inworldTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *inworldTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

