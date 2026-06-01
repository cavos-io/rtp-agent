package lmnt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
)

const (
	lmntBaseURL             = "https://api.lmnt.com/v1/ai/speech/bytes"
	defaultLMNTModel        = "blizzard"
	defaultLMNTVoice        = "leah"
	defaultLMNTFormat       = "mp3"
	defaultLMNTSampleRate   = 24000
	defaultLMNTTemperature  = 1.0
	defaultLMNTTopP         = 0.8
	defaultLMNTBlizzardLang = "auto"
	defaultLMNTLanguage     = "en"
)

type LMNTTTS struct {
	apiKey      string
	voice       string
	model       string
	language    string
	format      string
	sampleRate  int
	temperature float64
	topP        float64
}

type LMNTTTSOption func(*LMNTTTS)

func WithLMNTTTSModel(model string) LMNTTTSOption {
	return func(t *LMNTTTS) {
		if model != "" {
			t.model = model
		}
	}
}

func WithLMNTTTSVoice(voice string) LMNTTTSOption {
	return func(t *LMNTTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithLMNTTTSLanguage(language string) LMNTTTSOption {
	return func(t *LMNTTTS) {
		if language != "" {
			t.language = language
		}
	}
}

func WithLMNTTTSFormat(format string) LMNTTTSOption {
	return func(t *LMNTTTS) {
		if format != "" {
			t.format = format
		}
	}
}

func WithLMNTTTSSampleRate(sampleRate int) LMNTTTSOption {
	return func(t *LMNTTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithLMNTTTSTemperature(temperature float64) LMNTTTSOption {
	return func(t *LMNTTTS) {
		t.temperature = temperature
	}
}

func WithLMNTTTSTopP(topP float64) LMNTTTSOption {
	return func(t *LMNTTTS) {
		t.topP = topP
	}
}

func NewLMNTTTS(apiKey string, voice string, opts ...LMNTTTSOption) *LMNTTTS {
	provider := &LMNTTTS{
		apiKey:      apiKey,
		voice:       voice,
		model:       defaultLMNTModel,
		format:      defaultLMNTFormat,
		sampleRate:  defaultLMNTSampleRate,
		temperature: defaultLMNTTemperature,
		topP:        defaultLMNTTopP,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultLMNTVoice
	}
	if provider.language == "" {
		provider.language = defaultLMNTLanguage
		if provider.model == defaultLMNTModel {
			provider.language = defaultLMNTBlizzardLang
		}
	}
	return provider
}

func (t *LMNTTTS) Label() string { return "lmnt.TTS" }
func (t *LMNTTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *LMNTTTS) SampleRate() int  { return t.sampleRate }
func (t *LMNTTTS) NumChannels() int { return 1 }

func (t *LMNTTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildLMNTTTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("lmnt tts error: %s", string(respBody))
	}

	return &lmntTTSChunkedStream{
		resp:       resp,
		sampleRate: t.sampleRate,
	}, nil
}

func buildLMNTTTSRequest(ctx context.Context, t *LMNTTTS, text string) (*http.Request, error) {
	body := map[string]interface{}{
		"text":        text,
		"voice":       t.voice,
		"language":    t.language,
		"sample_rate": t.sampleRate,
		"model":       t.model,
		"format":      t.format,
		"temperature": t.temperature,
		"top_p":       t.topP,
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, lmntBaseURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", t.apiKey)
	return req, nil
}

func (t *LMNTTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("streaming tts is not supported by the LMNT TTS API")
}

type lmntTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
}

func (s *lmntTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	buf := make([]byte, 4096)
	n, err := s.resp.Body.Read(buf)
	if err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, err
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

func (s *lmntTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}
