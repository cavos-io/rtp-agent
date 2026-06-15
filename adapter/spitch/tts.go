package spitch

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
)

const (
	defaultSpitchBaseURL      = "https://api.spitch.ai"
	defaultSpitchVoice        = "lina"
	defaultSpitchLanguage     = "en"
	defaultSpitchOutputFormat = "mp3"
	defaultSpitchSampleRate   = 24000
)

type SpitchTTS struct {
	apiKey       string
	baseURL      string
	voice        string
	language     string
	outputFormat string
	sampleRate   int
}

type SpitchTTSOption func(*SpitchTTS)

func WithSpitchTTSBaseURL(baseURL string) SpitchTTSOption {
	return func(t *SpitchTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithSpitchTTSVoice(voice string) SpitchTTSOption {
	return func(t *SpitchTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithSpitchTTSLanguage(language string) SpitchTTSOption {
	return func(t *SpitchTTS) {
		if language != "" {
			t.language = language
		}
	}
}

func WithSpitchTTSOutputFormat(outputFormat string) SpitchTTSOption {
	return func(t *SpitchTTS) {
		if outputFormat != "" {
			t.outputFormat = outputFormat
		}
	}
}

func WithSpitchTTSSampleRate(sampleRate int) SpitchTTSOption {
	return func(t *SpitchTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func NewSpitchTTS(apiKey string, voice string, opts ...SpitchTTSOption) *SpitchTTS {
	provider := &SpitchTTS{
		apiKey:       resolveSpitchAPIKey(apiKey),
		baseURL:      defaultSpitchBaseURL,
		voice:        voice,
		language:     defaultSpitchLanguage,
		outputFormat: defaultSpitchOutputFormat,
		sampleRate:   defaultSpitchSampleRate,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultSpitchVoice
	}
	return provider
}

func (t *SpitchTTS) Label() string { return "spitch.TTS" }
func (t *SpitchTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *SpitchTTS) SampleRate() int  { return t.sampleRate }
func (t *SpitchTTS) NumChannels() int { return 1 }
func (t *SpitchTTS) Model() string    { return "unknown" }
func (t *SpitchTTS) Provider() string { return "Spitch" }

func (t *SpitchTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildSpitchTTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("spitch tts error: %s", string(respBody))
	}

	return &spitchTTSChunkedStream{
		resp:       resp,
		sampleRate: t.sampleRate,
	}, nil
}

func buildSpitchTTSRequest(ctx context.Context, t *SpitchTTS, text string) (*http.Request, error) {
	reqBody := map[string]interface{}{
		"text":     text,
		"language": t.language,
		"voice":    t.voice,
		"format":   t.outputFormat,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+"/tts/v1/synthesize", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	return req, nil
}

func (t *SpitchTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("spitch streaming tts not natively supported by basic rest api")
}

type spitchTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
	emitted    bool
}

func (s *spitchTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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
	frame, err := decodeSpitchWAVPCM16(data)
	if err != nil {
		return nil, err
	}

	return &tts.SynthesizedAudio{
		Frame: frame,
	}, nil
}

func (s *spitchTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

func decodeSpitchWAVPCM16(data []byte) (*model.AudioFrame, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("invalid spitch wav data")
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
			return nil, fmt.Errorf("invalid spitch wav chunk size")
		}
		chunk := data[offset : offset+chunkSize]
		switch chunkID {
		case "fmt ":
			if len(chunk) < 16 {
				return nil, fmt.Errorf("invalid spitch wav fmt chunk")
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
		return nil, fmt.Errorf("unsupported spitch wav format: audio_format=%d bits_per_sample=%d", audioFormat, bitsPerSample)
	}
	if sampleRate == 0 || numChannels == 0 {
		return nil, fmt.Errorf("missing spitch wav format metadata")
	}
	if pcmData == nil {
		return nil, fmt.Errorf("missing spitch wav data chunk")
	}
	return &model.AudioFrame{
		Data:              pcmData,
		SampleRate:        sampleRate,
		NumChannels:       uint32(numChannels),
		SamplesPerChannel: uint32(len(pcmData) / (int(numChannels) * 2)),
	}, nil
}
