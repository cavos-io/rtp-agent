package simplismart

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
)

const (
	defaultSimplismartTTSBaseURL           = "https://api.simplismart.live/tts"
	defaultSimplismartTTSQwenBaseURL       = "https://api.simplismart.live/v1/audio/speech"
	defaultSimplismartTTSModel             = "canopylabs/orpheus-3b-0.1-ft"
	defaultSimplismartTTSQwenModel         = "qwen-tts"
	defaultSimplismartTTSVoice             = "tara"
	defaultSimplismartTTSQwenVoice         = "Chelsie"
	defaultSimplismartTTSSampleRate        = 24000
	defaultSimplismartTTSTemperature       = 0.7
	defaultSimplismartTTSTopP              = 0.9
	defaultSimplismartTTSRepetitionPenalty = 1.5
	defaultSimplismartTTSMaxTokens         = 1000
	defaultSimplismartTTSQwenLanguage      = "English"
)

type SimplismartTTS struct {
	apiKey            string
	baseURL           string
	model             string
	voice             string
	sampleRate        int
	temperature       float64
	topP              float64
	repetitionPenalty float64
	maxTokens         int
	language          string
	leadingSilence    *bool
	baseURLExplicit   bool
}

type SimplismartTTSOption func(*SimplismartTTS)

func WithSimplismartTTSBaseURL(baseURL string) SimplismartTTSOption {
	return func(t *SimplismartTTS) {
		if baseURL != "" {
			t.baseURL = baseURL
			t.baseURLExplicit = true
		}
	}
}

func WithSimplismartTTSModel(model string) SimplismartTTSOption {
	return func(t *SimplismartTTS) {
		if model != "" {
			t.model = model
		}
	}
}

func WithSimplismartTTSSampleRate(sampleRate int) SimplismartTTSOption {
	return func(t *SimplismartTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithSimplismartTTSTemperature(temperature float64) SimplismartTTSOption {
	return func(t *SimplismartTTS) {
		t.temperature = temperature
	}
}

func WithSimplismartTTSTopP(topP float64) SimplismartTTSOption {
	return func(t *SimplismartTTS) {
		t.topP = topP
	}
}

func WithSimplismartTTSRepetitionPenalty(repetitionPenalty float64) SimplismartTTSOption {
	return func(t *SimplismartTTS) {
		t.repetitionPenalty = repetitionPenalty
	}
}

func WithSimplismartTTSMaxTokens(maxTokens int) SimplismartTTSOption {
	return func(t *SimplismartTTS) {
		if maxTokens > 0 {
			t.maxTokens = maxTokens
		}
	}
}

func WithSimplismartTTSLanguage(language string) SimplismartTTSOption {
	return func(t *SimplismartTTS) {
		if language != "" {
			t.language = language
		}
	}
}

func WithSimplismartTTSLeadingSilence(leadingSilence bool) SimplismartTTSOption {
	return func(t *SimplismartTTS) {
		t.leadingSilence = &leadingSilence
	}
}

func NewSimplismartTTS(apiKey string, voice string, opts ...SimplismartTTSOption) *SimplismartTTS {
	if apiKey == "" {
		apiKey = os.Getenv(simplismartAPIKeyEnv)
	}
	provider := &SimplismartTTS{
		apiKey:            apiKey,
		baseURL:           defaultSimplismartTTSBaseURL,
		model:             defaultSimplismartTTSModel,
		voice:             voice,
		sampleRate:        defaultSimplismartTTSSampleRate,
		temperature:       defaultSimplismartTTSTemperature,
		topP:              defaultSimplismartTTSTopP,
		repetitionPenalty: defaultSimplismartTTSRepetitionPenalty,
		maxTokens:         defaultSimplismartTTSMaxTokens,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if isSimplismartQwenModel(provider.model) {
		if !provider.baseURLExplicit {
			provider.baseURL = defaultSimplismartTTSQwenBaseURL
		}
		if provider.voice == "" {
			provider.voice = defaultSimplismartTTSQwenVoice
		}
		if provider.language == "" {
			provider.language = defaultSimplismartTTSQwenLanguage
		}
		if provider.leadingSilence == nil {
			leadingSilence := true
			provider.leadingSilence = &leadingSilence
		}
	} else if provider.voice == "" {
		provider.voice = defaultSimplismartTTSVoice
	}
	return provider
}

func (t *SimplismartTTS) Label() string { return "simplismart.TTS" }
func (t *SimplismartTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *SimplismartTTS) SampleRate() int  { return t.sampleRate }
func (t *SimplismartTTS) NumChannels() int { return 1 }
func (t *SimplismartTTS) Model() string    { return t.model }
func (t *SimplismartTTS) Provider() string { return "SimpliSmart" }

func (t *SimplismartTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if t.apiKey == "" {
		return nil, fmt.Errorf("%s is not set", simplismartAPIKeyEnv)
	}

	return &simplismartTTSChunkedStream{
		ctx:        ctx,
		provider:   t,
		text:       text,
		sampleRate: t.sampleRate,
	}, nil
}

func (t *SimplismartTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("simplismart streaming tts not natively supported by basic rest api")
}

func buildSimplismartTTSRequest(ctx context.Context, t *SimplismartTTS, text string) (*http.Request, error) {
	if isSimplismartQwenModel(t.model) {
		return buildSimplismartTTSQwenRequest(ctx, t, text)
	}
	reqBody := map[string]interface{}{
		"prompt":             text,
		"voice":              t.voice,
		"model":              t.model,
		"temperature":        t.temperature,
		"top_p":              t.topP,
		"repetition_penalty": t.repetitionPenalty,
		"max_tokens":         t.maxTokens,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	return req, nil
}

func buildSimplismartTTSQwenRequest(ctx context.Context, t *SimplismartTTS, text string) (*http.Request, error) {
	reqBody := map[string]interface{}{
		"model":           t.model,
		"text":            text,
		"language":        t.language,
		"voice":           t.voice,
		"leading_silence": true,
	}
	if t.leadingSilence != nil {
		reqBody["leading_silence"] = *t.leadingSilence
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/L16")
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	return req, nil
}

func isSimplismartQwenModel(model string) bool {
	return strings.Contains(strings.ToLower(model), "qwen")
}

type simplismartTTSChunkedStream struct {
	ctx          context.Context
	provider     *SimplismartTTS
	text         string
	resp         *http.Response
	sampleRate   int
	pendingFinal bool
	finalSent    bool
	closed       bool
}

func (s *simplismartTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.closed || s.finalSent {
		return nil, io.EOF
	}
	if err := s.ensureResponse(); err != nil {
		return nil, err
	}
	if s.resp == nil || s.resp.Body == nil {
		return nil, io.EOF
	}
	if s.pendingFinal {
		s.pendingFinal = false
		return s.emitFinal()
	}
	buf := make([]byte, 4096)
	n, err := s.resp.Body.Read(buf)
	if n > 0 {
		if err == io.EOF {
			s.pendingFinal = true
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
	if err != nil {
		if err == io.EOF {
			return s.emitFinal()
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

func (s *simplismartTTSChunkedStream) ensureResponse() error {
	if s.resp != nil || s.provider == nil {
		return nil
	}
	req, err := buildSimplismartTTSRequest(s.ctx, s.provider, s.text)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		return simplismartHTTPTransportError("Simplismart TTS request failed", err)
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return llm.NewAPIStatusError("Simplismart TTS request failed", resp.StatusCode, "", string(respBody))
	}
	s.resp = resp
	return nil
}

func (s *simplismartTTSChunkedStream) emitFinal() (*tts.SynthesizedAudio, error) {
	if s.finalSent {
		return nil, io.EOF
	}
	s.finalSent = true
	return &tts.SynthesizedAudio{IsFinal: true}, nil
}

func (s *simplismartTTSChunkedStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.finalSent = true
	if s.resp == nil || s.resp.Body == nil {
		return nil
	}
	return s.resp.Body.Close()
}
