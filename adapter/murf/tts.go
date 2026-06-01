package murf

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
	defaultMurfBaseURL    = "https://global.api.murf.ai"
	defaultMurfModel      = "FALCON"
	defaultMurfVoice      = "en-US-matthew"
	defaultMurfStyle      = "Conversation"
	defaultMurfEncoding   = "pcm"
	defaultMurfSampleRate = 24000
)

type MurfTTS struct {
	apiKey     string
	baseURL    string
	model      string
	locale     string
	voice      string
	style      string
	speed      *int
	pitch      *int
	sampleRate int
	encoding   string
}

type MurfTTSOption func(*MurfTTS)

func WithMurfTTSBaseURL(baseURL string) MurfTTSOption {
	return func(t *MurfTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithMurfTTSModel(model string) MurfTTSOption {
	return func(t *MurfTTS) {
		if model != "" {
			t.model = model
		}
	}
}

func WithMurfTTSLocale(locale string) MurfTTSOption {
	return func(t *MurfTTS) {
		t.locale = locale
	}
}

func WithMurfTTSVoice(voice string) MurfTTSOption {
	return func(t *MurfTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithMurfTTSStyle(style string) MurfTTSOption {
	return func(t *MurfTTS) {
		t.style = style
	}
}

func WithMurfTTSSpeed(speed int) MurfTTSOption {
	return func(t *MurfTTS) {
		t.speed = &speed
	}
}

func WithMurfTTSPitch(pitch int) MurfTTSOption {
	return func(t *MurfTTS) {
		t.pitch = &pitch
	}
}

func WithMurfTTSSampleRate(sampleRate int) MurfTTSOption {
	return func(t *MurfTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func NewMurfTTS(apiKey string, voice string, opts ...MurfTTSOption) *MurfTTS {
	provider := &MurfTTS{
		apiKey:     apiKey,
		baseURL:    defaultMurfBaseURL,
		model:      defaultMurfModel,
		voice:      voice,
		style:      defaultMurfStyle,
		sampleRate: defaultMurfSampleRate,
		encoding:   defaultMurfEncoding,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultMurfVoice
	}
	return provider
}

func (t *MurfTTS) Label() string { return "murf.TTS" }
func (t *MurfTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *MurfTTS) SampleRate() int  { return t.sampleRate }
func (t *MurfTTS) NumChannels() int { return 1 }

func (t *MurfTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildMurfTTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("murf tts error: %s", string(respBody))
	}
	return &murfTTSChunkedStream{resp: resp, sampleRate: t.sampleRate}, nil
}

func buildMurfTTSRequest(ctx context.Context, t *MurfTTS, text string) (*http.Request, error) {
	reqBody := map[string]interface{}{
		"text":              text,
		"model":             t.model,
		"multiNativeLocale": nil,
		"voice_id":          t.voice,
		"style":             t.style,
		"rate":              nil,
		"pitch":             nil,
		"format":            t.encoding,
		"sample_rate":       t.sampleRate,
	}
	if t.locale != "" {
		reqBody["multiNativeLocale"] = t.locale
	}
	if t.speed != nil {
		reqBody["rate"] = *t.speed
	}
	if t.pitch != nil {
		reqBody["pitch"] = *t.pitch
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+"/v1/speech/stream", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("api-key", t.apiKey)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (t *MurfTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("murf websocket streaming tts not implemented")
}

type murfTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
}

func (s *murfTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *murfTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}
