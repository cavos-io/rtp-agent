package neuphonic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/model"
)

const (
	defaultNeuphonicBaseURL    = "https://api.neuphonic.com"
	defaultNeuphonicVoice      = "8e9c4bc8-3979-48ab-8626-df53befc2090"
	defaultNeuphonicLangCode   = "en"
	defaultNeuphonicEncoding   = "pcm_linear"
	defaultNeuphonicSampleRate = 22050
)

type NeuphonicTTS struct {
	apiKey     string
	baseURL    string
	voice      string
	langCode   string
	encoding   string
	sampleRate int
	speed      *float64
}

type NeuphonicTTSOption func(*NeuphonicTTS)

func WithNeuphonicTTSBaseURL(baseURL string) NeuphonicTTSOption {
	return func(t *NeuphonicTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithNeuphonicTTSVoice(voice string) NeuphonicTTSOption {
	return func(t *NeuphonicTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithNeuphonicTTSLangCode(langCode string) NeuphonicTTSOption {
	return func(t *NeuphonicTTS) {
		if langCode != "" {
			t.langCode = langCode
		}
	}
}

func WithNeuphonicTTSEncoding(encoding string) NeuphonicTTSOption {
	return func(t *NeuphonicTTS) {
		if encoding != "" {
			t.encoding = encoding
		}
	}
}

func WithNeuphonicTTSSampleRate(sampleRate int) NeuphonicTTSOption {
	return func(t *NeuphonicTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithNeuphonicTTSSpeed(speed float64) NeuphonicTTSOption {
	return func(t *NeuphonicTTS) {
		t.speed = &speed
	}
}

func NewNeuphonicTTS(apiKey string, voice string, opts ...NeuphonicTTSOption) *NeuphonicTTS {
	defaultSpeed := 1.0
	provider := &NeuphonicTTS{
		apiKey:     apiKey,
		baseURL:    defaultNeuphonicBaseURL,
		voice:      voice,
		langCode:   defaultNeuphonicLangCode,
		encoding:   defaultNeuphonicEncoding,
		sampleRate: defaultNeuphonicSampleRate,
		speed:      &defaultSpeed,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultNeuphonicVoice
	}
	return provider
}

func (t *NeuphonicTTS) Label() string { return "neuphonic.TTS" }
func (t *NeuphonicTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *NeuphonicTTS) SampleRate() int  { return t.sampleRate }
func (t *NeuphonicTTS) NumChannels() int { return 1 }

func (t *NeuphonicTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildNeuphonicTTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("neuphonic tts error: %s", string(respBody))
	}

	return &neuphonicTTSChunkedStream{
		resp:       resp,
		sampleRate: t.sampleRate,
	}, nil
}

func buildNeuphonicTTSRequest(ctx context.Context, t *NeuphonicTTS, text string) (*http.Request, error) {
	reqBody := map[string]interface{}{
		"text":          text,
		"voice_id":      t.voice,
		"lang_code":     t.langCode,
		"encoding":      t.encoding,
		"sampling_rate": t.sampleRate,
		"speed":         optionalFloat(t.speed),
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+"/sse/speak/"+t.langCode, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", t.apiKey)
	return req, nil
}

func (t *NeuphonicTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("neuphonic streaming tts not natively supported by basic rest api")
}

type neuphonicTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
	scanner    *bufio.Scanner
}

func (s *neuphonicTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.scanner == nil {
		s.scanner = bufio.NewScanner(s.resp.Body)
	}
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		audio, err := neuphonicAudioFromSSEData(strings.TrimPrefix(line, "data: "))
		if err != nil {
			return nil, err
		}
		if len(audio) == 0 {
			continue
		}
		return &tts.SynthesizedAudio{
			Frame: &model.AudioFrame{
				Data:              audio,
				SampleRate:        uint32(s.sampleRate),
				NumChannels:       1,
				SamplesPerChannel: uint32(len(audio) / 2),
			},
		}, nil
	}
	if err := s.scanner.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func (s *neuphonicTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

func neuphonicAudioFromSSEData(data string) ([]byte, error) {
	var parsed struct {
		StatusCode int `json:"status_code"`
		Data       struct {
			Audio string `json:"audio"`
		} `json:"data"`
		Errors interface{} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		return nil, err
	}
	if parsed.Errors != nil {
		return nil, fmt.Errorf("neuphonic tts error: %v", parsed.Errors)
	}
	if parsed.Data.Audio == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(parsed.Data.Audio)
}

func optionalFloat(value *float64) interface{} {
	if value == nil {
		return nil
	}
	return *value
}
