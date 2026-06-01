package respeecher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/model"
)

const (
	respeecherAPIVersion      = "1.5.15"
	defaultRespeecherBaseURL  = "https://api.respeecher.com/v1"
	defaultRespeecherModel    = "/public/tts/en-rt"
	defaultRespeecherEncoding = "pcm_s16le"
	defaultRespeecherRate     = 24000
)

var defaultRespeecherVoices = map[string]string{
	"/public/tts/en-rt": "samantha",
	"/public/tts/ua-rt": "olesia-conversation",
}

type RespeecherTTS struct {
	apiKey         string
	baseURL        string
	model          string
	voiceID        string
	encoding       string
	sampleRate     int
	samplingParams map[string]any
}

type RespeecherTTSOption func(*RespeecherTTS)

func WithRespeecherTTSBaseURL(baseURL string) RespeecherTTSOption {
	return func(t *RespeecherTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithRespeecherTTSModel(model string) RespeecherTTSOption {
	return func(t *RespeecherTTS) {
		if model != "" {
			t.model = model
			if voice := defaultRespeecherVoices[model]; voice != "" {
				t.voiceID = voice
			}
		}
	}
}

func WithRespeecherTTSVoice(voiceID string) RespeecherTTSOption {
	return func(t *RespeecherTTS) {
		if voiceID != "" {
			t.voiceID = voiceID
		}
	}
}

func WithRespeecherTTSSampleRate(sampleRate int) RespeecherTTSOption {
	return func(t *RespeecherTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithRespeecherTTSSamplingParams(params map[string]any) RespeecherTTSOption {
	return func(t *RespeecherTTS) {
		t.samplingParams = params
	}
}

func NewRespeecherTTS(apiKey string, voiceID string, opts ...RespeecherTTSOption) *RespeecherTTS {
	provider := &RespeecherTTS{
		apiKey:     apiKey,
		baseURL:    defaultRespeecherBaseURL,
		model:      defaultRespeecherModel,
		voiceID:    voiceID,
		encoding:   defaultRespeecherEncoding,
		sampleRate: defaultRespeecherRate,
	}
	if provider.voiceID == "" {
		provider.voiceID = defaultRespeecherVoices[provider.model]
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voiceID == "" {
		provider.voiceID = defaultRespeecherVoices[provider.model]
	}
	return provider
}

func (t *RespeecherTTS) Label() string { return "respeecher.TTS" }
func (t *RespeecherTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *RespeecherTTS) SampleRate() int  { return t.sampleRate }
func (t *RespeecherTTS) NumChannels() int { return 1 }

func (t *RespeecherTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildRespeecherTTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("respeecher tts error: %s", string(respBody))
	}
	return &respeecherTTSChunkedStream{resp: resp, sampleRate: t.sampleRate}, nil
}

func buildRespeecherTTSRequest(ctx context.Context, t *RespeecherTTS, text string) (*http.Request, error) {
	voice := map[string]interface{}{"id": t.voiceID}
	if len(t.samplingParams) > 0 {
		voice["sampling_params"] = t.samplingParams
	}
	reqBody := map[string]interface{}{
		"transcript": text,
		"voice":      voice,
		"output_format": map[string]interface{}{
			"sample_rate": t.sampleRate,
			"encoding":    t.encoding,
		},
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+t.model+"/tts/bytes", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", t.apiKey)
	req.Header.Set("LiveKit-Plugin-Respeecher-Version", respeecherAPIVersion)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (t *RespeecherTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("respeecher websocket streaming tts not implemented")
}

type respeecherTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
}

func (s *respeecherTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *respeecherTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}
