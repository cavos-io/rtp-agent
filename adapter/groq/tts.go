package groq

import (
	"bytes"
	"context"
	"encoding/binary"
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
func (t *GroqTTS) Model() string    { return t.model }
func (t *GroqTTS) Provider() string { return "Groq" }

func (t *GroqTTS) UpdateOptions(model string, voice string) {
	if model != "" {
		t.model = model
	}
	if voice != "" {
		t.voice = voice
	}
}

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
	emitted    bool
}

func (s *groqTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.resp == nil || s.resp.Body == nil {
		return nil, io.EOF
	}
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
	frame, err := decodeGroqWAVPCM16(data)
	if err != nil {
		return nil, err
	}
	return &tts.SynthesizedAudio{
		Frame: frame,
	}, nil
}

func (s *groqTTSChunkedStream) Close() error {
	if s.resp == nil || s.resp.Body == nil {
		return nil
	}
	body := s.resp.Body
	s.resp = nil
	return body.Close()
}

func decodeGroqWAVPCM16(data []byte) (*model.AudioFrame, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("invalid groq wav data")
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
			return nil, fmt.Errorf("invalid groq wav chunk size")
		}
		chunk := data[offset : offset+chunkSize]
		switch chunkID {
		case "fmt ":
			if len(chunk) < 16 {
				return nil, fmt.Errorf("invalid groq wav fmt chunk")
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
		return nil, fmt.Errorf("unsupported groq wav format: audio_format=%d bits_per_sample=%d", audioFormat, bitsPerSample)
	}
	if sampleRate == 0 || numChannels == 0 {
		return nil, fmt.Errorf("missing groq wav format metadata")
	}
	if pcmData == nil {
		return nil, fmt.Errorf("missing groq wav data chunk")
	}
	return &model.AudioFrame{
		Data:              pcmData,
		SampleRate:        sampleRate,
		NumChannels:       uint32(numChannels),
		SamplesPerChannel: uint32(len(pcmData) / (int(numChannels) * 2)),
	}, nil
}
