package cavos

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
)

const (
	defaultTTSModel          = "supertonic-3"
	defaultTTSVoice          = "F1"
	defaultTTSLanguage       = "en"
	defaultTTSResponseFormat = "pcm"
	defaultTTSSampleRate     = 44100
)

type TTS struct {
	httpClient     httpDoer
	baseURL        string
	model          string
	voice          string
	language       string
	responseFormat string
	sampleRate     int
}

type TTSOption func(*TTS)

func NewTTS(opts ...TTSOption) *TTS {
	provider := &TTS{
		httpClient:     http.DefaultClient,
		baseURL:        defaultBaseURL,
		model:          defaultTTSModel,
		voice:          defaultTTSVoice,
		language:       defaultTTSLanguage,
		responseFormat: defaultTTSResponseFormat,
		sampleRate:     defaultTTSSampleRate,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func WithTTSBaseURL(baseURL string) TTSOption {
	return func(t *TTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithTTSModel(model string) TTSOption {
	return func(t *TTS) {
		if model != "" {
			t.model = model
		}
	}
}

func WithTTSVoice(voice string) TTSOption {
	return func(t *TTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithTTSLanguage(language string) TTSOption {
	return func(t *TTS) {
		if language != "" {
			t.language = language
		}
	}
}

func WithTTSResponseFormat(format string) TTSOption {
	return func(t *TTS) {
		if format != "" {
			t.responseFormat = format
		}
	}
}

func WithTTSSampleRate(sampleRate int) TTSOption {
	return func(t *TTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func withTTSHTTPClient(client http.RoundTripper) TTSOption {
	return func(t *TTS) {
		if client != nil {
			t.httpClient = &http.Client{Transport: client}
		}
	}
}

func (t *TTS) Label() string { return "cavos.TTS" }
func (t *TTS) Provider() string {
	return "cavos"
}
func (t *TTS) Model() string { return t.model }
func (t *TTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *TTS) SampleRate() int  { return t.sampleRate }
func (t *TTS) NumChannels() int { return 1 }

func (t *TTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildTTSRequest(ctx, t, text)
	if err != nil {
		return nil, err
	}
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("cavos tts error: %s", strings.TrimSpace(string(body)))
	}
	sampleRate := t.sampleRate
	if header := resp.Header.Get("X-Sample-Rate"); header != "" {
		if parsed, err := strconv.Atoi(header); err == nil && parsed > 0 {
			sampleRate = parsed
		}
	}
	return &ttsStream{resp: resp.Body, sampleRate: sampleRate}, nil
}

func buildTTSRequest(ctx context.Context, t *TTS, text string) (*http.Request, error) {
	payload := map[string]any{
		"model":           t.model,
		"voice":           t.voice,
		"input":           text,
		"lang":            t.language,
		"response_format": t.responseFormat,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+"/audio/speech", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (t *TTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("cavos streaming tts is not natively supported")
}

type ttsStream struct {
	resp       io.ReadCloser
	sampleRate int
}

func (s *ttsStream) Next() (*tts.SynthesizedAudio, error) {
	buf := make([]byte, 4096)
	n, err := s.resp.Read(buf)
	if err != nil && n == 0 {
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

func (s *ttsStream) Close() error {
	return s.resp.Close()
}
