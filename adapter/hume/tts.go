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
	"os"
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
	voiceID         string
	voiceName       string
	voiceProvider   string
	description     string
	speed           *float64
	trailingSilence *float64
	instantMode     bool
	audioFormat     string
	sampleRate      int
	contextID       string
	context         []HumeTTSUtterance
}

type HumeTTSVoice struct {
	ID       string
	Name     string
	Provider string
}

type HumeTTSUtterance struct {
	Text            string
	Description     string
	Speed           *float64
	Voice           *HumeTTSVoice
	TrailingSilence *float64
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
		t.voiceID = ""
		t.voiceName = name
		t.voiceProvider = provider
		t.instantMode = name != ""
	}
}

func WithHumeTTSVoiceID(id string, provider string) HumeTTSOption {
	return func(t *HumeTTS) {
		t.voiceID = id
		t.voiceName = ""
		t.voiceProvider = provider
		t.instantMode = id != ""
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

func WithHumeTTSContextGenerationID(generationID string) HumeTTSOption {
	return func(t *HumeTTS) {
		t.contextID = generationID
		t.context = nil
	}
}

func WithHumeTTSContextUtterances(context []HumeTTSUtterance) HumeTTSOption {
	return func(t *HumeTTS) {
		t.context = context
		t.contextID = ""
	}
}

func NewHumeTTS(apiKey string, model string, opts ...HumeTTSOption) *HumeTTS {
	if apiKey == "" {
		apiKey = os.Getenv("HUME_API_KEY")
	}
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
func (t *HumeTTS) Model() string    { return "Octave" }
func (t *HumeTTS) Provider() string { return "Hume" }

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
	if t.apiKey == "" {
		return nil, fmt.Errorf("hume API key is required via api_key or HUME_API_KEY env var")
	}
	if err := validateHumeTTSOptions(t); err != nil {
		return nil, err
	}

	utterance := map[string]interface{}{
		"text": text,
	}
	if t.voiceID != "" || t.voiceName != "" {
		utterance["voice"] = humeTTSVoicePayload(HumeTTSVoice{
			ID:       t.voiceID,
			Name:     t.voiceName,
			Provider: t.voiceProvider,
		})
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
	if t.contextID != "" {
		body["context"] = map[string]interface{}{"generation_id": t.contextID}
	} else if len(t.context) > 0 {
		contextUtterances := make([]map[string]interface{}, 0, len(t.context))
		for _, utterance := range t.context {
			contextUtterances = append(contextUtterances, humeTTSUtterancePayload(utterance))
		}
		body["context"] = map[string]interface{}{"utterances": contextUtterances}
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

func validateHumeTTSOptions(t *HumeTTS) error {
	if t.instantMode && t.voiceID == "" && t.voiceName == "" {
		return fmt.Errorf("hume TTS: instant_mode cannot be enabled without specifying a voice")
	}
	return nil
}

func humeTTSUtterancePayload(utterance HumeTTSUtterance) map[string]interface{} {
	payload := map[string]interface{}{"text": utterance.Text}
	if utterance.Description != "" {
		payload["description"] = utterance.Description
	}
	if utterance.Speed != nil {
		payload["speed"] = *utterance.Speed
	}
	if utterance.TrailingSilence != nil {
		payload["trailing_silence"] = *utterance.TrailingSilence
	}
	if utterance.Voice != nil {
		voice := humeTTSVoicePayload(*utterance.Voice)
		if len(voice) > 0 {
			payload["voice"] = voice
		}
	}
	return payload
}

func humeTTSVoicePayload(voice HumeTTSVoice) map[string]interface{} {
	payload := map[string]interface{}{}
	if voice.ID != "" {
		payload["id"] = voice.ID
	}
	if voice.Name != "" {
		payload["name"] = voice.Name
	}
	if voice.Provider != "" {
		payload["provider"] = voice.Provider
	}
	return payload
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
