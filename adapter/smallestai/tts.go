package smallestai

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
	defaultSmallestAIBaseURL       = "https://api.smallest.ai/waves/v1"
	defaultSmallestAIModel         = "lightning_v3.1_pro"
	defaultSmallestAIProVoice      = "meher"
	defaultSmallestAIStandardVoice = "sophia"
	defaultSmallestAISampleRate    = 24000
	defaultSmallestAISpeed         = 1.0
	defaultSmallestAILanguage      = "en"
	defaultSmallestAIOutputFormat  = "pcm"
)

type SmallestAITTS struct {
	apiKey       string
	baseURL      string
	model        string
	voice        string
	sampleRate   int
	speed        float64
	language     string
	outputFormat string
}

type SmallestAITTSOption func(*SmallestAITTS)

func WithSmallestAITTSBaseURL(baseURL string) SmallestAITTSOption {
	return func(t *SmallestAITTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithSmallestAITTSModel(model string) SmallestAITTSOption {
	return func(t *SmallestAITTS) {
		if model != "" {
			t.model = model
			if t.voice == "" {
				t.voice = defaultSmallestAIVoice(model)
			}
		}
	}
}

func WithSmallestAITTSVoice(voice string) SmallestAITTSOption {
	return func(t *SmallestAITTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithSmallestAITTSSampleRate(sampleRate int) SmallestAITTSOption {
	return func(t *SmallestAITTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithSmallestAITTSSpeed(speed float64) SmallestAITTSOption {
	return func(t *SmallestAITTS) {
		t.speed = speed
	}
}

func WithSmallestAITTSLanguage(language string) SmallestAITTSOption {
	return func(t *SmallestAITTS) {
		if language != "" {
			t.language = language
		}
	}
}

func WithSmallestAITTSOutputFormat(outputFormat string) SmallestAITTSOption {
	return func(t *SmallestAITTS) {
		if outputFormat != "" {
			t.outputFormat = outputFormat
		}
	}
}

func NewSmallestAITTS(apiKey string, voice string, opts ...SmallestAITTSOption) *SmallestAITTS {
	provider := &SmallestAITTS{
		apiKey:       apiKey,
		baseURL:      defaultSmallestAIBaseURL,
		model:        defaultSmallestAIModel,
		voice:        voice,
		sampleRate:   defaultSmallestAISampleRate,
		speed:        defaultSmallestAISpeed,
		language:     defaultSmallestAILanguage,
		outputFormat: defaultSmallestAIOutputFormat,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultSmallestAIVoice(provider.model)
	}
	return provider
}

func (t *SmallestAITTS) Label() string { return "smallestai.TTS" }
func (t *SmallestAITTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *SmallestAITTS) SampleRate() int  { return t.sampleRate }
func (t *SmallestAITTS) NumChannels() int { return 1 }

func (t *SmallestAITTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildSmallestAITTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("smallestai tts error: %s", string(respBody))
	}

	return &smallestaiTTSChunkedStream{
		resp:       resp,
		sampleRate: t.sampleRate,
	}, nil
}

func buildSmallestAITTSRequest(ctx context.Context, t *SmallestAITTS, text string) (*http.Request, error) {
	reqBody := map[string]interface{}{
		"model":         t.model,
		"voice_id":      t.voice,
		"text":          text,
		"sample_rate":   t.sampleRate,
		"speed":         t.speed,
		"language":      t.language,
		"output_format": t.outputFormat,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+"/tts", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Source", "livekit")
	return req, nil
}

func (t *SmallestAITTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("smallestai streaming tts not natively supported by basic rest api")
}

type smallestaiTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
}

func (s *smallestaiTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *smallestaiTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

func defaultSmallestAIVoice(model string) string {
	if model == defaultSmallestAIModel {
		return defaultSmallestAIProVoice
	}
	return defaultSmallestAIStandardVoice
}
