package groq

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
)

const (
	defaultGroqTTSBaseURL        = "https://api.groq.com/openai/v1"
	defaultGroqTTSModel          = "canopylabs/orpheus-v1-english"
	defaultGroqTTSVoice          = "autumn"
	defaultGroqTTSResponseFormat = "wav"
	defaultGroqTTSSampleRate     = 48000
)

type GroqTTS struct {
	apiKey         string
	baseURL        string
	model          string
	voice          string
	responseFormat string
	sampleRate     int
}

type GroqTTSOption func(*GroqTTS)

func WithGroqTTSBaseURL(baseURL string) GroqTTSOption {
	return func(t *GroqTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithGroqTTSModel(model string) GroqTTSOption {
	return func(t *GroqTTS) {
		if model != "" {
			t.model = model
		}
	}
}

func WithGroqTTSVoice(voice string) GroqTTSOption {
	return func(t *GroqTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func NewGroqTTS(apiKey string, voice string, opts ...GroqTTSOption) *GroqTTS {
	if apiKey == "" {
		apiKey = os.Getenv("GROQ_API_KEY")
	}
	provider := &GroqTTS{
		apiKey:         apiKey,
		baseURL:        defaultGroqTTSBaseURL,
		model:          defaultGroqTTSModel,
		voice:          voice,
		responseFormat: defaultGroqTTSResponseFormat,
		sampleRate:     defaultGroqTTSSampleRate,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultGroqTTSVoice
	}
	return provider
}

func (t *GroqTTS) Label() string { return "groq.TTS" }
func (t *GroqTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *GroqTTS) SampleRate() int  { return t.sampleRate }
func (t *GroqTTS) NumChannels() int { return 1 }

func (t *GroqTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if err := validateGroqTTSAPIKey(t.apiKey); err != nil {
		return nil, err
	}

	req, err := buildGroqTTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("groq tts error: %s", string(respBody))
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "audio") {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("groq tts returned non-audio data: %s", string(respBody))
	}

	return &groqTTSChunkedStream{resp: resp, sampleRate: t.sampleRate}, nil
}

func validateGroqTTSAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("groq API key is required, either as argument or set GROQ_API_KEY environment variable")
	}
	return nil
}

func buildGroqTTSRequest(ctx context.Context, t *GroqTTS, text string) (*http.Request, error) {
	reqBody := map[string]interface{}{
		"model":           t.model,
		"voice":           t.voice,
		"input":           text,
		"response_format": t.responseFormat,
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+"/audio/speech", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (t *GroqTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("groq streaming tts not natively supported")
}

type groqTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
}

func (s *groqTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *groqTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}
