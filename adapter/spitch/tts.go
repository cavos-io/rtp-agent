package spitch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
)

const (
	defaultSpitchBaseURL      = "https://api.spitch.ai"
	defaultSpitchVoice        = "lina"
	defaultSpitchLanguage     = "en"
	defaultSpitchOutputFormat = "mp3"
	defaultSpitchSampleRate   = 24000
)

type SpitchTTS struct {
	apiKey       string
	baseURL      string
	voice        string
	language     string
	outputFormat string
	sampleRate   int
}

type SpitchTTSOption func(*SpitchTTS)

func WithSpitchTTSBaseURL(baseURL string) SpitchTTSOption {
	return func(t *SpitchTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithSpitchTTSVoice(voice string) SpitchTTSOption {
	return func(t *SpitchTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithSpitchTTSLanguage(language string) SpitchTTSOption {
	return func(t *SpitchTTS) {
		if language != "" {
			t.language = language
		}
	}
}

func WithSpitchTTSOutputFormat(outputFormat string) SpitchTTSOption {
	return func(t *SpitchTTS) {
		if outputFormat != "" {
			t.outputFormat = outputFormat
		}
	}
}

func WithSpitchTTSSampleRate(sampleRate int) SpitchTTSOption {
	return func(t *SpitchTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func NewSpitchTTS(apiKey string, voice string, opts ...SpitchTTSOption) *SpitchTTS {
	provider := &SpitchTTS{
		apiKey:       apiKey,
		baseURL:      defaultSpitchBaseURL,
		voice:        voice,
		language:     defaultSpitchLanguage,
		outputFormat: defaultSpitchOutputFormat,
		sampleRate:   defaultSpitchSampleRate,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultSpitchVoice
	}
	return provider
}

func (t *SpitchTTS) Label() string { return "spitch.TTS" }
func (t *SpitchTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *SpitchTTS) SampleRate() int  { return t.sampleRate }
func (t *SpitchTTS) NumChannels() int { return 1 }

func (t *SpitchTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildSpitchTTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("spitch tts error: %s", string(respBody))
	}

	return &spitchTTSChunkedStream{
		resp:       resp,
		sampleRate: t.sampleRate,
	}, nil
}

func buildSpitchTTSRequest(ctx context.Context, t *SpitchTTS, text string) (*http.Request, error) {
	reqBody := map[string]interface{}{
		"text":     text,
		"language": t.language,
		"voice":    t.voice,
		"format":   t.outputFormat,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+"/tts/v1/synthesize", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	return req, nil
}

func (t *SpitchTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("spitch streaming tts not natively supported by basic rest api")
}

type spitchTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
}

func (s *spitchTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *spitchTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}
