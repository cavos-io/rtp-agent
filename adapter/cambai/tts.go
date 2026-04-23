package cambai

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

type CambaiTTS struct {
	apiKey     string
	voice      string
	baseURL    string
	httpClient *http.Client
}

type TTSOption func(*CambaiTTS)

func WithTTSBaseURL(url string) TTSOption {
	return func(t *CambaiTTS) {
		t.baseURL = url
	}
}

func WithHTTPClient(client *http.Client) TTSOption {
	return func(t *CambaiTTS) {
		t.httpClient = client
	}
}

func NewCambaiTTS(apiKey string, voice string, opts ...TTSOption) *CambaiTTS {
	if voice == "" {
		voice = "default_voice"
	}
	t := &CambaiTTS{
		apiKey:     apiKey,
		voice:      voice,
		baseURL:    "https://api.cambai.com/v1/tts",
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *CambaiTTS) Label() string { return "cambai.TTS" }
func (t *CambaiTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *CambaiTTS) SampleRate() int { return 24000 }
func (t *CambaiTTS) NumChannels() int { return 1 }

func (t *CambaiTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {

	reqBody := map[string]interface{}{
		"text":  text,
		"voice": t.voice,
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
		return nil, fmt.Errorf("cambai tts error: %s", string(respBody))
	}

	return &cambaiTTSChunkedStream{
		resp: resp,
	}, nil
}

func (t *CambaiTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("cambai streaming tts not natively supported by basic rest api")
}

type cambaiTTSChunkedStream struct {
	resp *http.Response
}

func (s *cambaiTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	buf := make([]byte, 4096)
	n, err := s.resp.Body.Read(buf)
	if n == 0 && err != nil {
		return nil, err
	}

	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              buf[:n],
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: uint32(n / 2),
		},
	}, nil
}

func (s *cambaiTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

