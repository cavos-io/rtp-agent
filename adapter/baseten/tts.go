package baseten

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/model"
)

const (
	defaultBasetenTTSVoice       = "tara"
	defaultBasetenTTSLanguage    = "en"
	defaultBasetenTTSTemperature = 0.6
	defaultBasetenTTSSampleRate  = 24000
)

type BasetenTTS struct {
	apiKey        string
	modelEndpoint string
	voice         string
	language      string
	temperature   float64
	sampleRate    int
}

type BasetenTTSOption func(*BasetenTTS)

func WithBasetenTTSModelEndpoint(endpoint string) BasetenTTSOption {
	return func(t *BasetenTTS) {
		if endpoint != "" {
			t.modelEndpoint = endpoint
		}
	}
}

func WithBasetenTTSVoice(voice string) BasetenTTSOption {
	return func(t *BasetenTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithBasetenTTSLanguage(language string) BasetenTTSOption {
	return func(t *BasetenTTS) {
		if language != "" {
			t.language = language
		}
	}
}

func WithBasetenTTSTemperature(temperature float64) BasetenTTSOption {
	return func(t *BasetenTTS) {
		t.temperature = temperature
	}
}

func NewBasetenTTS(apiKey string, model string, opts ...BasetenTTSOption) *BasetenTTS {
	endpoint := model
	if endpoint == "" {
		endpoint = "xtts-v2"
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") &&
		!strings.HasPrefix(endpoint, "ws://") && !strings.HasPrefix(endpoint, "wss://") {
		endpoint = fmt.Sprintf("https://model-%s.api.baseten.co/environments/production/predict", endpoint)
	}
	provider := &BasetenTTS{
		apiKey:        apiKey,
		modelEndpoint: endpoint,
		voice:         defaultBasetenTTSVoice,
		language:      defaultBasetenTTSLanguage,
		temperature:   defaultBasetenTTSTemperature,
		sampleRate:    defaultBasetenTTSSampleRate,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (t *BasetenTTS) Label() string { return "baseten.TTS" }
func (t *BasetenTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: strings.HasPrefix(t.modelEndpoint, "ws://") || strings.HasPrefix(t.modelEndpoint, "wss://"), AlignedTranscript: false}
}
func (t *BasetenTTS) SampleRate() int  { return t.sampleRate }
func (t *BasetenTTS) NumChannels() int { return 1 }

func (t *BasetenTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildBasetenTTSRequest(ctx, t, text)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("baseten tts error: %s", string(respBody))
	}
	return &basetenTTSChunkedStream{body: resp.Body, sampleRate: t.sampleRate}, nil
}

func buildBasetenTTSRequest(ctx context.Context, t *BasetenTTS, text string) (*http.Request, error) {
	reqBody := map[string]interface{}{
		"prompt":      text,
		"voice":       t.voice,
		"temperature": t.temperature,
		"language":    t.language,
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.modelEndpoint, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Api-Key "+t.apiKey)
	return req, nil
}

func (t *BasetenTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("baseten websocket tts streaming is not implemented")
}

type basetenTTSChunkedStream struct {
	body       io.ReadCloser
	sampleRate int
}

func (s *basetenTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	buf := make([]byte, 4096)
	n, err := s.body.Read(buf)
	if err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, err
	}
	if n == 0 {
		return nil, io.EOF
	}
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              buf[:n],
			SampleRate:        uint32(s.sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(n / 2),
		},
	}, nil
}

func (s *basetenTTSChunkedStream) Close() error {
	return s.body.Close()
}
