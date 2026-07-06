package lmnt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/cavos-io/rtp-agent/core/audio/codecs"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
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
	if apiKey == "" {
		apiKey = os.Getenv("LMNT_API_KEY")
	}
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
func (t *LMNTTTS) Model() string    { return t.model }
func (t *LMNTTTS) Provider() string { return "LMNT" }

func (t *LMNTTTS) UpdateOptions(opts ...LMNTTTSOption) {
	for _, opt := range opts {
		opt(t)
	}
}

func (t *LMNTTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if err := validateLMNTAPIKey(t.apiKey); err != nil {
		return nil, err
	}
	return &lmntTTSChunkedStream{
		ctx:         ctx,
		text:        text,
		apiKey:      t.apiKey,
		voice:       t.voice,
		model:       t.model,
		language:    t.language,
		format:      t.format,
		sampleRate:  t.sampleRate,
		temperature: t.temperature,
		topP:        t.topP,
	}, nil
}

func buildLMNTTTSRequest(ctx context.Context, t *LMNTTTS, text string) (*http.Request, error) {
	return buildLMNTTTSRequestFromOptions(ctx, lmntTTSRequestOptions{
		text:        text,
		apiKey:      t.apiKey,
		voice:       t.voice,
		model:       t.model,
		language:    t.language,
		format:      t.format,
		sampleRate:  t.sampleRate,
		temperature: t.temperature,
		topP:        t.topP,
	})
}

type lmntTTSRequestOptions struct {
	text        string
	apiKey      string
	voice       string
	model       string
	language    string
	format      string
	sampleRate  int
	temperature float64
	topP        float64
}

func buildLMNTTTSRequestFromOptions(ctx context.Context, opts lmntTTSRequestOptions) (*http.Request, error) {
	body := map[string]interface{}{
		"text":        opts.text,
		"voice":       opts.voice,
		"language":    opts.language,
		"sample_rate": opts.sampleRate,
		"model":       opts.model,
		"format":      opts.format,
		"temperature": opts.temperature,
		"top_p":       opts.topP,
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
	req.Header.Set("X-API-Key", opts.apiKey)
	return req, nil
}

func validateLMNTAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("lmnt API key is required, either as argument or set LMNT_API_KEY environment variable")
	}
	return nil
}

func (t *LMNTTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("streaming tts is not supported by the LMNT TTS API")
}

type lmntTTSChunkedStream struct {
	resp         *http.Response
	ctx          context.Context
	text         string
	apiKey       string
	voice        string
	model        string
	language     string
	format       string
	sampleRate   int
	temperature  float64
	topP         float64
	decoder      codecs.AudioStreamDecoder
	requested    bool
	started      bool
	closed       bool
	hasAudio     bool
	pendingFinal bool
	finalSent    bool
}

func (s *lmntTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.closed {
		return nil, io.EOF
	}
	if err := s.ensureResponse(); err != nil {
		return nil, err
	}
	if s.resp == nil || s.resp.Body == nil {
		return nil, io.EOF
	}
	if s.format == "mp3" {
		return s.nextDecodedMP3()
	}
	if s.finalSent {
		return nil, io.EOF
	}
	if s.pendingFinal {
		s.pendingFinal = false
		s.finalSent = true
		return &tts.SynthesizedAudio{IsFinal: true}, nil
	}

	buf := make([]byte, 4096)
	for {
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
				s.finalSent = true
				return &tts.SynthesizedAudio{IsFinal: true}, nil
			}
			return nil, llm.NewAPIConnectionError(err.Error())
		}
	}
}

func (s *lmntTTSChunkedStream) ensureResponse() error {
	if s.resp != nil || s.requested || s.text == "" {
		return nil
	}
	s.requested = true
	req, err := buildLMNTTTSRequestFromOptions(s.ctx, lmntTTSRequestOptions{
		text:        s.text,
		apiKey:      s.apiKey,
		voice:       s.voice,
		model:       s.model,
		language:    s.language,
		format:      s.format,
		sampleRate:  s.sampleRate,
		temperature: s.temperature,
		topP:        s.topP,
	})
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return llm.NewAPIConnectionError(err.Error())
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return llm.NewAPIStatusError("LMNT TTS request failed", resp.StatusCode, "", string(respBody))
	}
	s.resp = resp
	return nil
}

func (s *lmntTTSChunkedStream) nextDecodedMP3() (*tts.SynthesizedAudio, error) {
	if s.finalSent {
		return nil, io.EOF
	}
	if !s.started {
		s.started = true
		data, err := io.ReadAll(s.resp.Body)
		if err != nil {
			return nil, llm.NewAPIConnectionError(err.Error())
		}
		if len(data) == 0 {
			s.finalSent = true
			return &tts.SynthesizedAudio{IsFinal: true}, nil
		}
		s.hasAudio = true
		decoder := codecs.NewMP3AudioStreamDecoder()
		s.decoder = decoder
		go func() {
			decoder.Push(data)
			decoder.EndInput()
		}()
	}

	frame, err := s.decoder.Next()
	if err != nil {
		if strings.Contains(err.Error(), "decoder closed") {
			if s.hasAudio && !s.finalSent {
				s.finalSent = true
				return &tts.SynthesizedAudio{IsFinal: true}, nil
			}
			return nil, io.EOF
		}
		return nil, err
	}
	return &tts.SynthesizedAudio{Frame: frame}, nil
}

func (s *lmntTTSChunkedStream) Close() error {
	s.closed = true
	if s.resp == nil || s.resp.Body == nil {
		return nil
	}
	body := s.resp.Body
	decoder := s.decoder
	s.resp = nil
	s.decoder = nil
	if decoder != nil {
		_ = decoder.Close()
	}
	return body.Close()
}
