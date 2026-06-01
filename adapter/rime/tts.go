package rime

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
	defaultRimeHTTPBaseURL = "https://users.rime.ai/v1/rime-tts"
	defaultRimeModel       = "arcana"
	defaultRimeArcanaVoice = "astra"
	defaultRimeMistVoice   = "cove"
	defaultRimeCodaVoice   = "lyra"
	defaultRimeLang        = "eng"
	defaultRimeSampleRate  = 22050
)

type RimeTTS struct {
	apiKey          string
	baseURL         string
	model           string
	voice           string
	lang            string
	sampleRate      int
	timeScaleFactor *float64
}

type RimeTTSOption func(*RimeTTS)

func WithRimeTTSBaseURL(baseURL string) RimeTTSOption {
	return func(t *RimeTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithRimeTTSModel(model string) RimeTTSOption {
	return func(t *RimeTTS) {
		if model != "" {
			t.model = model
			if t.voice == "" {
				t.voice = defaultRimeVoice(model)
			}
		}
	}
}

func WithRimeTTSSampleRate(sampleRate int) RimeTTSOption {
	return func(t *RimeTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithRimeTTSLang(lang string) RimeTTSOption {
	return func(t *RimeTTS) {
		if lang != "" {
			t.lang = lang
		}
	}
}

func WithRimeTTSTimeScaleFactor(timeScaleFactor float64) RimeTTSOption {
	return func(t *RimeTTS) {
		t.timeScaleFactor = &timeScaleFactor
	}
}

func NewRimeTTS(apiKey string, voice string, opts ...RimeTTSOption) *RimeTTS {
	provider := &RimeTTS{
		apiKey:     apiKey,
		baseURL:    defaultRimeHTTPBaseURL,
		model:      defaultRimeModel,
		lang:       defaultRimeLang,
		sampleRate: defaultRimeSampleRate,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if voice == "" {
		voice = defaultRimeVoice(provider.model)
	}
	provider.voice = voice
	return provider
}

func defaultRimeVoice(model string) string {
	switch {
	case model == "coda":
		return defaultRimeCodaVoice
	case strings.Contains(model, "mist"):
		return defaultRimeMistVoice
	default:
		return defaultRimeArcanaVoice
	}
}

func (t *RimeTTS) Label() string { return "rime.TTS" }
func (t *RimeTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *RimeTTS) SampleRate() int  { return t.sampleRate }
func (t *RimeTTS) NumChannels() int { return 1 }

func (t *RimeTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildRimeTTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("rime tts error: %s", string(respBody))
	}

	return &rimeTTSChunkedStream{
		resp:       resp,
		sampleRate: t.sampleRate,
	}, nil
}

func buildRimeTTSRequest(ctx context.Context, t *RimeTTS, text string) (*http.Request, error) {
	reqBody := map[string]interface{}{
		"speaker":      t.voice,
		"text":         text,
		"modelId":      t.model,
		"lang":         t.lang,
		"samplingRate": t.sampleRate,
	}
	if t.timeScaleFactor != nil {
		reqBody["timeScaleFactor"] = *t.timeScaleFactor
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "audio/pcm")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	return req, nil
}

func (t *RimeTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("streaming tts is not supported by the Rime TTS API")
}

type rimeTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
}

func (s *rimeTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *rimeTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}
