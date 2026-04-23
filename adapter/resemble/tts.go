package resemble

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/model"
)

type ResembleTTS struct {
	apiKey     string
	voice      string
	baseURL    string
	httpClient *http.Client
}

type Option func(*ResembleTTS)

func WithBaseURL(url string) Option {
	return func(t *ResembleTTS) {
		t.baseURL = url
	}
}

func WithHTTPClient(client *http.Client) Option {
	return func(t *ResembleTTS) {
		t.httpClient = client
	}
}

func NewResembleTTS(apiKey string, voice string, opts ...Option) *ResembleTTS {
	if voice == "" {
		voice = "default_voice"
	}
	t := &ResembleTTS{
		apiKey:     apiKey,
		voice:      voice,
		baseURL:    "https://f.cluster.resemble.ai/synthesize",
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *ResembleTTS) Label() string { return "resemble.TTS" }
func (t *ResembleTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *ResembleTTS) SampleRate() int { return 44100 }
func (t *ResembleTTS) NumChannels() int { return 1 }

func (t *ResembleTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {

	reqBody := map[string]interface{}{
		"data":  text,
		"voice_uuid": t.voice,
	}

	jsonBody, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", t.baseURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.apiKey)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("resemble tts error: %s", string(respBody))
	}

	return &resembleTTSChunkedStream{
		resp: resp,
	}, nil
}

func (t *ResembleTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("resemble streaming tts not natively supported by basic rest api")
}

type resembleTTSChunkedStream struct {
	resp *http.Response
}

func (s *resembleTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	buf := make([]byte, 4096)
	n, err := s.resp.Body.Read(buf)
	if n == 0 && err != nil {
		return nil, err
	}

	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              buf[:n],
			SampleRate:        44100,
			NumChannels:       1,
			SamplesPerChannel: uint32(n / 2),
		},
	}, nil
}

func (s *resembleTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

