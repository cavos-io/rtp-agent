package resemble

import (
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
	resembleRESTAPIURL        = "https://f.cluster.resemble.ai/synthesize"
	defaultResembleVoiceUUID  = "55592656"
	defaultResembleSampleRate = 44100
)

type ResembleTTS struct {
	apiKey     string
	voice      string
	sampleRate int
	model      string
}

type ResembleTTSOption func(*ResembleTTS)

func WithResembleTTSVoice(voice string) ResembleTTSOption {
	return func(t *ResembleTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithResembleTTSSampleRate(sampleRate int) ResembleTTSOption {
	return func(t *ResembleTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithResembleTTSModel(model string) ResembleTTSOption {
	return func(t *ResembleTTS) {
		t.model = model
	}
}

func NewResembleTTS(apiKey string, voice string, opts ...ResembleTTSOption) *ResembleTTS {
	provider := &ResembleTTS{
		apiKey:     apiKey,
		voice:      voice,
		sampleRate: defaultResembleSampleRate,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultResembleVoiceUUID
	}
	return provider
}

func (t *ResembleTTS) Label() string { return "resemble.TTS" }
func (t *ResembleTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *ResembleTTS) SampleRate() int  { return t.sampleRate }
func (t *ResembleTTS) NumChannels() int { return 1 }

func (t *ResembleTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildResembleTTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("resemble tts error: %s", string(respBody))
	}

	return &resembleTTSChunkedStream{
		resp:       resp,
		sampleRate: t.sampleRate,
	}, nil
}

func buildResembleTTSRequest(ctx context.Context, t *ResembleTTS, text string) (*http.Request, error) {
	reqBody := map[string]interface{}{
		"voice_uuid":  t.voice,
		"data":        text,
		"sample_rate": t.sampleRate,
		"precision":   "PCM_16",
	}
	if t.model != "" {
		reqBody["model"] = t.model
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resembleRESTAPIURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (t *ResembleTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("resemble streaming tts not natively supported by basic rest api")
}

type resembleTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
	done       bool
}

func (s *resembleTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.done {
		return nil, io.EOF
	}
	s.done = true

	var result struct {
		Success      bool     `json:"success"`
		AudioContent string   `json:"audio_content"`
		Issues       []string `json:"issues"`
	}
	if err := json.NewDecoder(s.resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if !result.Success {
		issues := "unknown error"
		if len(result.Issues) > 0 {
			issues = strings.Join(result.Issues, "; ")
		}
		return nil, fmt.Errorf("resemble api returned failure: %s", issues)
	}
	audio, err := base64.StdEncoding.DecodeString(result.AudioContent)
	if err != nil {
		return nil, err
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

func (s *resembleTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}
