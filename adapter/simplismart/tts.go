package simplismart

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
)

const (
	defaultSimplismartTTSBaseURL           = "https://api.simplismart.live/tts"
	defaultSimplismartTTSModel             = "canopylabs/orpheus-3b-0.1-ft"
	defaultSimplismartTTSVoice             = "tara"
	defaultSimplismartTTSSampleRate        = 24000
	defaultSimplismartTTSTemperature       = 0.7
	defaultSimplismartTTSTopP              = 0.9
	defaultSimplismartTTSRepetitionPenalty = 1.5
	defaultSimplismartTTSMaxTokens         = 1000
)

type SimplismartTTS struct {
	apiKey            string
	baseURL           string
	model             string
	voice             string
	sampleRate        int
	temperature       float64
	topP              float64
	repetitionPenalty float64
	maxTokens         int
}

func NewSimplismartTTS(apiKey string, voice string) *SimplismartTTS {
	if apiKey == "" {
		apiKey = os.Getenv(simplismartAPIKeyEnv)
	}
	if voice == "" {
		voice = defaultSimplismartTTSVoice
	}
	return &SimplismartTTS{
		apiKey:            apiKey,
		baseURL:           defaultSimplismartTTSBaseURL,
		model:             defaultSimplismartTTSModel,
		voice:             voice,
		sampleRate:        defaultSimplismartTTSSampleRate,
		temperature:       defaultSimplismartTTSTemperature,
		topP:              defaultSimplismartTTSTopP,
		repetitionPenalty: defaultSimplismartTTSRepetitionPenalty,
		maxTokens:         defaultSimplismartTTSMaxTokens,
	}
}

func (t *SimplismartTTS) Label() string { return "simplismart.TTS" }
func (t *SimplismartTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *SimplismartTTS) SampleRate() int  { return t.sampleRate }
func (t *SimplismartTTS) NumChannels() int { return 1 }
func (t *SimplismartTTS) Model() string    { return t.model }
func (t *SimplismartTTS) Provider() string { return "SimpliSmart" }

func (t *SimplismartTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if t.apiKey == "" {
		return nil, fmt.Errorf("%s is not set", simplismartAPIKeyEnv)
	}

	req, err := buildSimplismartTTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("simplismart tts error: %s", string(respBody))
	}

	return &simplismartTTSChunkedStream{
		resp:       resp,
		sampleRate: t.sampleRate,
	}, nil
}

func (t *SimplismartTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("simplismart streaming tts not natively supported by basic rest api")
}

func buildSimplismartTTSRequest(ctx context.Context, t *SimplismartTTS, text string) (*http.Request, error) {
	reqBody := map[string]interface{}{
		"prompt":             text,
		"voice":              t.voice,
		"model":              t.model,
		"temperature":        t.temperature,
		"top_p":              t.topP,
		"repetition_penalty": t.repetitionPenalty,
		"max_tokens":         t.maxTokens,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	return req, nil
}

type simplismartTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
}

func (s *simplismartTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *simplismartTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}
