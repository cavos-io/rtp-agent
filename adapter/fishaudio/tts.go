package fishaudio

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cavos-io/conversation-worker/core/tts"
	"github.com/cavos-io/conversation-worker/model"
	"github.com/vmihailenco/msgpack/v5"
)

const (
	defaultFishAudioModel       = "s2-pro"
	defaultFishAudioVoiceID     = "933563129e564b19a115bedd57b7406a"
	defaultFishAudioBaseURL     = "https://api.fish.audio"
	defaultFishAudioFormat      = "wav"
	defaultFishAudioLatencyMode = "balanced"
	defaultFishAudioChunkLength = 100
)

type FishAudioTTS struct {
	apiKey       string
	baseURL      string
	model        string
	voice        string
	outputFormat string
	sampleRate   int
	latencyMode  string
	chunkLength  int
}

type FishAudioTTSOption func(*FishAudioTTS)

func WithFishAudioTTSBaseURL(baseURL string) FishAudioTTSOption {
	return func(t *FishAudioTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithFishAudioTTSModel(model string) FishAudioTTSOption {
	return func(t *FishAudioTTS) {
		if model != "" {
			t.model = model
		}
	}
}

func WithFishAudioTTSVoice(voice string) FishAudioTTSOption {
	return func(t *FishAudioTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithFishAudioTTSOutputFormat(outputFormat string) FishAudioTTSOption {
	return func(t *FishAudioTTS) {
		if outputFormat != "" {
			t.outputFormat = outputFormat
			t.sampleRate = defaultFishAudioSampleRate(outputFormat)
		}
	}
}

func WithFishAudioTTSSampleRate(sampleRate int) FishAudioTTSOption {
	return func(t *FishAudioTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithFishAudioTTSLatencyMode(latencyMode string) FishAudioTTSOption {
	return func(t *FishAudioTTS) {
		if latencyMode != "" {
			t.latencyMode = latencyMode
		}
	}
}

func WithFishAudioTTSChunkLength(chunkLength int) FishAudioTTSOption {
	return func(t *FishAudioTTS) {
		if chunkLength > 0 {
			t.chunkLength = chunkLength
		}
	}
}

func NewFishAudioTTS(apiKey string, voice string, opts ...FishAudioTTSOption) *FishAudioTTS {
	provider := &FishAudioTTS{
		apiKey:       apiKey,
		baseURL:      defaultFishAudioBaseURL,
		model:        defaultFishAudioModel,
		voice:        voice,
		outputFormat: defaultFishAudioFormat,
		sampleRate:   defaultFishAudioSampleRate(defaultFishAudioFormat),
		latencyMode:  defaultFishAudioLatencyMode,
		chunkLength:  defaultFishAudioChunkLength,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultFishAudioVoiceID
	}
	return provider
}

func defaultFishAudioSampleRate(outputFormat string) int {
	switch outputFormat {
	case "opus":
		return 48000
	case "mp3":
		return 32000
	default:
		return 24000
	}
}

func (t *FishAudioTTS) Label() string { return "fishaudio.TTS" }
func (t *FishAudioTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *FishAudioTTS) SampleRate() int  { return t.sampleRate }
func (t *FishAudioTTS) NumChannels() int { return 1 }

func (t *FishAudioTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildFishAudioTTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("fishaudio tts error: %s", string(respBody))
	}

	return &fishaudioTTSChunkedStream{
		resp:       resp,
		sampleRate: t.sampleRate,
	}, nil
}

func buildFishAudioTTSRequest(ctx context.Context, t *FishAudioTTS, text string) (*http.Request, error) {
	reqBody := map[string]interface{}{
		"text":         text,
		"chunk_length": t.chunkLength,
		"format":       t.outputFormat,
		"sample_rate":  t.sampleRate,
		"mp3_bitrate":  64,
		"opus_bitrate": 64000,
		"references":   []interface{}{},
		"reference_id": t.voice,
		"normalize":    true,
		"latency":      t.latencyMode,
		"prosody":      nil,
		"top_p":        0.7,
		"temperature":  0.7,
	}

	packedBody, err := msgpack.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+"/v1/tts", bytes.NewBuffer(packedBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("Content-Type", "application/msgpack")
	req.Header.Set("model", t.model)
	return req, nil
}

func (t *FishAudioTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("fishaudio streaming tts not natively supported by basic rest api")
}

type fishaudioTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
}

func (s *fishaudioTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *fishaudioTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}
