package speechify

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
)

const (
	defaultSpeechifyBaseURL  = "https://api.sws.speechify.com/v1"
	defaultSpeechifyVoice    = "jack"
	defaultSpeechifyEncoding = "ogg_24000"
	speechifyAPIKeyEnv       = "SPEECHIFY_API_KEY"
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
	if apiKey == "" {
		apiKey = os.Getenv(speechifyAPIKeyEnv)
	}
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
func (t *SpeechifyTTS) Model() string {
	if t.model == "" {
		return "unknown"
	}
	return t.model
}
func (t *SpeechifyTTS) Provider() string { return "Speechify" }

func (t *SpeechifyTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if err := validateSpeechifyAPIKey(t.apiKey); err != nil {
		return nil, err
	}
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
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "audio/") {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("speechify tts returned non-audio data: %s", string(respBody))
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
	req.Header.Set("Authorization", speechifyAuthorizationHeader(t.apiKey))
	req.Header.Set("x-caller", "livekit")
	if accept := speechifyAcceptHeader(t.encoding); accept != "" {
		req.Header.Set("Accept", accept)
	}
	return req, nil
}

func validateSpeechifyAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("speechify API key is required, either as argument or set SPEECHIFY_API_KEY environment variable")
	}
	return nil
}

func (t *SpeechifyTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("streaming tts is not supported by the Speechify TTS API")
}

type speechifyTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
	emitted    bool
}

func (s *speechifyTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.emitted {
		return nil, io.EOF
	}
	s.emitted = true

	data, err := io.ReadAll(s.resp.Body)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, io.EOF
	}
	frame, err := decodeSpeechifyWAVPCM16(data)
	if err != nil {
		return nil, err
	}

	return &tts.SynthesizedAudio{
		Frame: frame,
	}, nil
}

func (s *speechifyTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

func decodeSpeechifyWAVPCM16(data []byte) (*model.AudioFrame, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("invalid speechify wav data")
	}

	var (
		audioFormat   uint16
		numChannels   uint16
		sampleRate    uint32
		bitsPerSample uint16
		pcmData       []byte
	)
	for offset := 12; offset+8 <= len(data); {
		chunkID := string(data[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		offset += 8
		if chunkSize < 0 || offset+chunkSize > len(data) {
			return nil, fmt.Errorf("invalid speechify wav chunk size")
		}
		chunk := data[offset : offset+chunkSize]
		switch chunkID {
		case "fmt ":
			if len(chunk) < 16 {
				return nil, fmt.Errorf("invalid speechify wav fmt chunk")
			}
			audioFormat = binary.LittleEndian.Uint16(chunk[0:2])
			numChannels = binary.LittleEndian.Uint16(chunk[2:4])
			sampleRate = binary.LittleEndian.Uint32(chunk[4:8])
			bitsPerSample = binary.LittleEndian.Uint16(chunk[14:16])
		case "data":
			pcmData = append([]byte(nil), chunk...)
		}
		offset += chunkSize
		if chunkSize%2 == 1 {
			offset++
		}
	}
	if audioFormat != 1 || bitsPerSample != 16 {
		return nil, fmt.Errorf("unsupported speechify wav format: audio_format=%d bits_per_sample=%d", audioFormat, bitsPerSample)
	}
	if sampleRate == 0 || numChannels == 0 {
		return nil, fmt.Errorf("missing speechify wav format metadata")
	}
	if pcmData == nil {
		return nil, fmt.Errorf("missing speechify wav data chunk")
	}
	return &model.AudioFrame{
		Data:              pcmData,
		SampleRate:        sampleRate,
		NumChannels:       uint32(numChannels),
		SamplesPerChannel: uint32(len(pcmData) / (int(numChannels) * 2)),
	}, nil
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

func speechifyAuthorizationHeader(apiKey string) string {
	if strings.HasPrefix(apiKey, "Bearer ") {
		return apiKey
	}
	return "Bearer " + apiKey
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
