package gnani

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cavos-io/conversation-worker/core/tts"
	"github.com/cavos-io/conversation-worker/model"
)

const (
	defaultBaseURL     = "https://api.vachana.ai"
	defaultVoice       = "Karan"
	defaultModel       = "vachana-voice-v3"
	defaultSampleRate  = 16000
	defaultEncoding    = "linear_pcm"
	defaultContainer   = "wav"
	defaultNumChannels = 1
	defaultSampleWidth = 2
	wavHeaderSize      = 44
)

type TTS struct {
	apiKey      string
	baseURL     string
	voice       string
	model       string
	sampleRate  int
	encoding    string
	container   string
	numChannels int
	sampleWidth int
}

type Option func(*TTS)

func WithBaseURL(baseURL string) Option {
	return func(t *TTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithVoice(voice string) Option {
	return func(t *TTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithModel(model string) Option {
	return func(t *TTS) {
		if model != "" {
			t.model = model
		}
	}
}

func WithSampleRate(sampleRate int) Option {
	return func(t *TTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithEncoding(encoding string) Option {
	return func(t *TTS) {
		if encoding != "" {
			t.encoding = encoding
		}
	}
}

func WithContainer(container string) Option {
	return func(t *TTS) {
		if container != "" {
			t.container = container
		}
	}
}

func WithNumChannels(numChannels int) Option {
	return func(t *TTS) {
		if numChannels > 0 {
			t.numChannels = numChannels
		}
	}
}

func WithSampleWidth(sampleWidth int) Option {
	return func(t *TTS) {
		if sampleWidth > 0 {
			t.sampleWidth = sampleWidth
		}
	}
}

func NewTTS(apiKey string, opts ...Option) *TTS {
	provider := &TTS{
		apiKey:      apiKey,
		baseURL:     defaultBaseURL,
		voice:       defaultVoice,
		model:       defaultModel,
		sampleRate:  defaultSampleRate,
		encoding:    defaultEncoding,
		container:   defaultContainer,
		numChannels: defaultNumChannels,
		sampleWidth: defaultSampleWidth,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (t *TTS) Label() string { return "gnani.TTS" }
func (t *TTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *TTS) SampleRate() int  { return t.sampleRate }
func (t *TTS) NumChannels() int { return t.numChannels }

func (t *TTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildTTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("gnani tts error: %s", string(respBody))
	}
	return &ttsChunkedStream{resp: resp, sampleRate: t.sampleRate, numChannels: t.numChannels}, nil
}

func buildTTSRequest(ctx context.Context, t *TTS, text string) (*http.Request, error) {
	reqBody := map[string]interface{}{
		"text":  text,
		"voice": t.voice,
		"model": t.model,
		"audio_config": map[string]interface{}{
			"sample_rate":  t.sampleRate,
			"encoding":     t.encoding,
			"num_channels": t.numChannels,
			"sample_width": t.sampleWidth,
			"container":    t.container,
		},
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+"/api/v1/tts/inference", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key-ID", t.apiKey)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (t *TTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("gnani websocket streaming tts not implemented")
}

type ttsChunkedStream struct {
	resp        *http.Response
	sampleRate  int
	numChannels int
}

func (s *ttsChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	buf := make([]byte, 4096)
	n, err := s.resp.Body.Read(buf)
	if err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, err
	}
	data := stripWAVHeader(buf[:n])
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              data,
			SampleRate:        uint32(s.sampleRate),
			NumChannels:       uint32(s.numChannels),
			SamplesPerChannel: uint32(len(data) / 2 / s.numChannels),
		},
	}, nil
}

func (s *ttsChunkedStream) Close() error {
	return s.resp.Body.Close()
}

func stripWAVHeader(data []byte) []byte {
	if len(data) > wavHeaderSize && string(data[:4]) == "RIFF" && string(data[8:12]) == "WAVE" {
		return data[wavHeaderSize:]
	}
	return data
}
