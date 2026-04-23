package rime

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

type RimeTTS struct {
	apiKey     string
	voice      string
	baseURL    string
	httpClient *http.Client
}

type Option func(*RimeTTS)

func WithBaseURL(url string) Option {
	return func(t *RimeTTS) {
		t.baseURL = url
	}
}

func WithHTTPClient(client *http.Client) Option {
	return func(t *RimeTTS) {
		t.httpClient = client
	}
}

func NewRimeTTS(apiKey string, voice string, opts ...Option) *RimeTTS {
	if voice == "" {
		voice = "default_voice"
	}
	t := &RimeTTS{
		apiKey:     apiKey,
		voice:      voice,
		baseURL:    "https://api.rime.ai/v1/tts",
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *RimeTTS) Label() string { return "rime.TTS" }
func (t *RimeTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *RimeTTS) SampleRate() int { return 22050 }
func (t *RimeTTS) NumChannels() int { return 1 }

func (t *RimeTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {

	reqBody := map[string]interface{}{
		"text":    text,
		"speaker": t.voice,
		"modelId": "v1", // Assumption based on common patterns
		"audioFormat": "pcm",
		"samplingRate": 22050, // Typical Rime fallback
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", t.baseURL, bytes.NewBuffer(jsonBody))
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
		return nil, fmt.Errorf("rime tts error: %s", string(respBody))
	}

	return &rimeTTSChunkedStream{
		resp: resp,
	}, nil
}

func (t *RimeTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("streaming tts is not supported by the Rime TTS API")
}

type rimeTTSChunkedStream struct {
	resp *http.Response
}

func (s *rimeTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	buf := make([]byte, 4096)
	n, err := s.resp.Body.Read(buf)
	if n == 0 && err != nil {
		return nil, err
	}

	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              buf[:n],
			SampleRate:        22050,
			NumChannels:       1,
			SamplesPerChannel: uint32(n / 2),
		},
	}, nil
}

func (s *rimeTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

