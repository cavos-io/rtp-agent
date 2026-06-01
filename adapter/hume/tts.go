package hume

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

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
)

const (
	defaultHumeBaseURL       = "https://api.hume.ai"
	defaultHumeModelVersion  = "1"
	defaultHumeVoiceName     = "Male English Actor"
	defaultHumeVoiceProvider = "HUME_AI"
	defaultHumeAudioFormat   = "mp3"
	defaultHumeSampleRate    = 48000
)

type HumeTTS struct {
	apiKey          string
	baseURL         string
	modelVersion    string
	voiceName       string
	voiceProvider   string
	description     string
	speed           *float64
	trailingSilence *float64
	instantMode     bool
	audioFormat     string
	sampleRate      int
}

type HumeTTSOption func(*HumeTTS)

func WithHumeTTSBaseURL(baseURL string) HumeTTSOption {
	return func(t *HumeTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithHumeTTSModelVersion(modelVersion string) HumeTTSOption {
	return func(t *HumeTTS) {
		if modelVersion != "" {
			t.modelVersion = modelVersion
		}
	}
}

func WithHumeTTSVoiceName(name string, provider string) HumeTTSOption {
	return func(t *HumeTTS) {
		t.voiceName = name
		t.voiceProvider = provider
		t.instantMode = name != ""
	}
}

func WithHumeTTSDescription(description string) HumeTTSOption {
	return func(t *HumeTTS) {
		t.description = description
	}
}

func WithHumeTTSSpeed(speed float64) HumeTTSOption {
	return func(t *HumeTTS) {
		t.speed = &speed
	}
}

func WithHumeTTSTrailingSilence(trailingSilence float64) HumeTTSOption {
	return func(t *HumeTTS) {
		t.trailingSilence = &trailingSilence
	}
}

func WithHumeTTSInstantMode(enabled bool) HumeTTSOption {
	return func(t *HumeTTS) {
		t.instantMode = enabled
	}
}

func WithHumeTTSAudioFormat(audioFormat string) HumeTTSOption {
	return func(t *HumeTTS) {
		if audioFormat != "" {
			t.audioFormat = audioFormat
		}
	}
}

func NewHumeTTS(apiKey string, model string, opts ...HumeTTSOption) *HumeTTS {
	provider := &HumeTTS{
		apiKey:        apiKey,
		baseURL:       defaultHumeBaseURL,
		modelVersion:  defaultHumeModelVersion,
		voiceName:     defaultHumeVoiceName,
		voiceProvider: defaultHumeVoiceProvider,
		instantMode:   true,
		audioFormat:   defaultHumeAudioFormat,
		sampleRate:    defaultHumeSampleRate,
	}
	if model != "" {
		provider.modelVersion = model
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (t *HumeTTS) Label() string { return "hume.TTS" }
func (t *HumeTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *HumeTTS) SampleRate() int  { return t.sampleRate }
func (t *HumeTTS) NumChannels() int { return 1 }

func (t *HumeTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildHumeTTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("hume tts error: %s", string(respBody))
	}

	return &humeTTSChunkedStream{
		resp:       resp,
		sampleRate: t.sampleRate,
	}, nil
}

func buildHumeTTSRequest(ctx context.Context, t *HumeTTS, text string) (*http.Request, error) {
	utterance := map[string]interface{}{
		"text": text,
	}
	if t.voiceName != "" {
		voice := map[string]interface{}{"name": t.voiceName}
		if t.voiceProvider != "" {
			voice["provider"] = t.voiceProvider
		}
		utterance["voice"] = voice
	}
	if t.description != "" {
		utterance["description"] = t.description
	}
	if t.speed != nil {
		utterance["speed"] = *t.speed
	}
	if t.trailingSilence != nil {
		utterance["trailing_silence"] = *t.trailingSilence
	}

	body := map[string]interface{}{
		"utterances":    []map[string]interface{}{utterance},
		"version":       t.modelVersion,
		"strip_headers": true,
		"instant_mode":  t.instantMode,
		"format":        map[string]interface{}{"type": t.audioFormat},
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+"/v0/tts/stream/json", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hume-Api-Key", t.apiKey)
	req.Header.Set("X-Hume-Client-Name", "livekit")
	req.Header.Set("X-Hume-Client-Version", "go")
	return req, nil
}

func (t *HumeTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("streaming tts not natively supported by basic hume rest api")
}

type humeTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
	scanner    *bufio.Scanner
}

func (s *humeTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.scanner == nil {
		s.scanner = bufio.NewScanner(s.resp.Body)
	}
	for s.scanner.Scan() {
		line := strings.TrimSpace(s.scanner.Text())
		if line == "" {
			continue
		}
		audio, err := humeAudioFromJSONLine(line)
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

func (s *humeTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

func humeAudioFromJSONLine(line string) ([]byte, error) {
	var data struct {
		Type  string `json:"type"`
		Audio string `json:"audio"`
	}
	if err := json.Unmarshal([]byte(line), &data); err != nil {
		return nil, err
	}
	if data.Type == "error" {
		return nil, fmt.Errorf("hume tts error: %s", line)
	}
	if data.Audio == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(data.Audio)
}
