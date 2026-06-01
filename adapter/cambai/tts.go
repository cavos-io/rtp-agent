package cambai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/model"
)

const (
	defaultCambaiBaseURL      = "https://client.camb.ai/apis"
	defaultCambaiVoiceID      = 147320
	defaultCambaiLanguage     = "en-us"
	defaultCambaiModel        = "mars-flash"
	defaultCambaiOutputFormat = "pcm_s16le"
)

var cambaiModelSampleRates = map[string]int{
	"mars-flash":    22050,
	"mars-pro":      48000,
	"mars-instruct": 22050,
}

type CambaiTTS struct {
	apiKey               string
	baseURL              string
	voiceID              int
	language             string
	model                string
	outputFormat         string
	userInstructions     string
	enhanceNamedEntities bool
	sampleRate           int
}

type CambaiTTSOption func(*CambaiTTS)

func WithCambaiTTSBaseURL(baseURL string) CambaiTTSOption {
	return func(t *CambaiTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithCambaiTTSVoiceID(voiceID int) CambaiTTSOption {
	return func(t *CambaiTTS) {
		if voiceID > 0 {
			t.voiceID = voiceID
		}
	}
}

func WithCambaiTTSLanguage(language string) CambaiTTSOption {
	return func(t *CambaiTTS) {
		if language != "" {
			t.language = language
		}
	}
}

func WithCambaiTTSModel(model string) CambaiTTSOption {
	return func(t *CambaiTTS) {
		if model != "" {
			t.model = model
			t.sampleRate = cambaiSampleRateForModel(model)
		}
	}
}

func WithCambaiTTSOutputFormat(outputFormat string) CambaiTTSOption {
	return func(t *CambaiTTS) {
		if outputFormat != "" {
			t.outputFormat = outputFormat
		}
	}
}

func WithCambaiTTSUserInstructions(userInstructions string) CambaiTTSOption {
	return func(t *CambaiTTS) {
		t.userInstructions = userInstructions
	}
}

func WithCambaiTTSEnhanceNamedEntities(enabled bool) CambaiTTSOption {
	return func(t *CambaiTTS) {
		t.enhanceNamedEntities = enabled
	}
}

func NewCambaiTTS(apiKey string, voice string, opts ...CambaiTTSOption) *CambaiTTS {
	provider := &CambaiTTS{
		apiKey:       apiKey,
		baseURL:      defaultCambaiBaseURL,
		voiceID:      defaultCambaiVoiceID,
		language:     defaultCambaiLanguage,
		model:        defaultCambaiModel,
		outputFormat: defaultCambaiOutputFormat,
		sampleRate:   cambaiSampleRateForModel(defaultCambaiModel),
	}
	if voice != "" {
		if voiceID, err := strconv.Atoi(voice); err == nil && voiceID > 0 {
			provider.voiceID = voiceID
		}
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (t *CambaiTTS) Label() string { return "cambai.TTS" }
func (t *CambaiTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *CambaiTTS) SampleRate() int  { return t.sampleRate }
func (t *CambaiTTS) NumChannels() int { return 1 }

func (t *CambaiTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildCambaiTTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("cambai tts error: %s", string(respBody))
	}

	return &cambaiTTSChunkedStream{
		resp:       resp,
		sampleRate: t.sampleRate,
	}, nil
}

func buildCambaiTTSRequest(ctx context.Context, t *CambaiTTS, text string) (*http.Request, error) {
	reqBody := map[string]interface{}{
		"text":                                 text,
		"voice_id":                             t.voiceID,
		"language":                             t.language,
		"speech_model":                         t.model,
		"enhance_named_entities_pronunciation": t.enhanceNamedEntities,
		"output_configuration":                 map[string]interface{}{"format": t.outputFormat},
	}
	if t.userInstructions != "" {
		reqBody["user_instructions"] = t.userInstructions
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+"/tts-stream", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", t.apiKey)
	return req, nil
}

func (t *CambaiTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("cambai streaming tts not natively supported by basic rest api")
}

type cambaiTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
}

func (s *cambaiTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *cambaiTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

func cambaiSampleRateForModel(model string) int {
	if sampleRate, ok := cambaiModelSampleRates[model]; ok {
		return sampleRate
	}
	return 22050
}
