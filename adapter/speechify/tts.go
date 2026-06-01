package speechify

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
	defaultSpeechifyBaseURL  = "https://api.sws.speechify.com/v1"
	defaultSpeechifyVoice    = "jack"
	defaultSpeechifyEncoding = "ogg_24000"
)

type SpeechifyTTS struct {
	apiKey                string
	baseURL               string
	voice                 string
	encoding              string
	sampleRate            int
	language              string
	model                 string
	loudnessNormalization *bool
	textNormalization     *bool
}

type SpeechifyTTSOption func(*SpeechifyTTS)

func WithSpeechifyTTSBaseURL(baseURL string) SpeechifyTTSOption {
	return func(t *SpeechifyTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithSpeechifyTTSVoice(voice string) SpeechifyTTSOption {
	return func(t *SpeechifyTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithSpeechifyTTSEncoding(encoding string) SpeechifyTTSOption {
	return func(t *SpeechifyTTS) {
		if encoding != "" {
			t.encoding = encoding
			t.sampleRate = speechifySampleRateFromEncoding(encoding)
		}
	}
}

func WithSpeechifyTTSLanguage(language string) SpeechifyTTSOption {
	return func(t *SpeechifyTTS) {
		t.language = language
	}
}

func WithSpeechifyTTSModel(model string) SpeechifyTTSOption {
	return func(t *SpeechifyTTS) {
		t.model = model
	}
}

func WithSpeechifyTTSLoudnessNormalization(enabled bool) SpeechifyTTSOption {
	return func(t *SpeechifyTTS) {
		t.loudnessNormalization = &enabled
	}
}

func WithSpeechifyTTSTextNormalization(enabled bool) SpeechifyTTSOption {
	return func(t *SpeechifyTTS) {
		t.textNormalization = &enabled
	}
}

func NewSpeechifyTTS(apiKey string, voice string, opts ...SpeechifyTTSOption) *SpeechifyTTS {
	provider := &SpeechifyTTS{
		apiKey:     apiKey,
		baseURL:    defaultSpeechifyBaseURL,
		voice:      voice,
		encoding:   defaultSpeechifyEncoding,
		sampleRate: speechifySampleRateFromEncoding(defaultSpeechifyEncoding),
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultSpeechifyVoice
	}
	return provider
}

func (t *SpeechifyTTS) Label() string { return "speechify.TTS" }
func (t *SpeechifyTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *SpeechifyTTS) SampleRate() int  { return t.sampleRate }
func (t *SpeechifyTTS) NumChannels() int { return 1 }

func (t *SpeechifyTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildSpeechifyTTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("speechify tts error: %s", string(respBody))
	}

	return &speechifyTTSChunkedStream{
		resp:       resp,
		sampleRate: t.sampleRate,
	}, nil
}

func buildSpeechifyTTSRequest(ctx context.Context, t *SpeechifyTTS, text string) (*http.Request, error) {
	body := map[string]interface{}{
		"input":        text,
		"voice_id":     t.voice,
		"language":     optionalString(t.language),
		"model":        optionalString(t.model),
		"audio_format": speechifyAudioFormatFromEncoding(t.encoding),
		"options": map[string]interface{}{
			"loudness_normalization": optionalBool(t.loudnessNormalization),
			"text_normalization":     optionalBool(t.textNormalization),
		},
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+"/audio/stream", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("x-caller", "livekit")
	if accept := speechifyAcceptHeader(t.encoding); accept != "" {
		req.Header.Set("Accept", accept)
	}
	return req, nil
}

func (t *SpeechifyTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("streaming tts is not supported by the Speechify TTS API")
}

type speechifyTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
}

func (s *speechifyTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *speechifyTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

func speechifySampleRateFromEncoding(encoding string) int {
	parts := strings.Split(encoding, "_")
	if len(parts) < 2 {
		return 24000
	}
	sampleRate, err := strconv.Atoi(parts[1])
	if err != nil {
		return 24000
	}
	return sampleRate
}

func speechifyAudioFormatFromEncoding(encoding string) string {
	parts := strings.Split(encoding, "_")
	if len(parts) == 0 || parts[0] == "" {
		return "ogg"
	}
	return parts[0]
}

func speechifyAcceptHeader(encoding string) string {
	switch speechifyAudioFormatFromEncoding(encoding) {
	case "ogg":
		return "audio/ogg"
	case "mp3":
		return "audio/mpeg"
	case "aac":
		return "audio/aac"
	default:
		return ""
	}
}

func optionalString(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}

func optionalBool(value *bool) interface{} {
	if value == nil {
		return nil
	}
	return *value
}
