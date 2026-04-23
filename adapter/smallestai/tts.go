package smallestai

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

type SmallestAITTS struct {
	apiKey     string
	voice      string
	baseURL    string
	httpClient *http.Client
}

type TTSOption func(*SmallestAITTS)

func WithTTSURL(url string) TTSOption {
	return func(t *SmallestAITTS) {
		t.baseURL = url
	}
}

func WithTTSHttpClient(client *http.Client) TTSOption {
	return func(t *SmallestAITTS) {
		t.httpClient = client
	}
}

func NewSmallestAITTS(apiKey string, voice string, opts ...TTSOption) *SmallestAITTS {
	if voice == "" {
		voice = "default_voice"
	}
	t := &SmallestAITTS{
		apiKey:     apiKey,
		voice:      voice,
		baseURL:    "https://waves-api.smallest.ai/api/v1/tts",
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *SmallestAITTS) Label() string { return "smallestai.TTS" }
func (t *SmallestAITTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *SmallestAITTS) SampleRate() int { return 24000 }
func (t *SmallestAITTS) NumChannels() int { return 1 }

func (t *SmallestAITTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	url := t.baseURL

	reqBody := map[string]interface{}{
		"text":  text,
		"voice": t.voice,
	}

	jsonBody, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
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
		return nil, fmt.Errorf("smallestai tts error: %s", string(respBody))
	}

	return &smallestaiTTSChunkedStream{
		resp: resp,
	}, nil
}

func (t *SmallestAITTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("smallestai streaming tts not natively supported by basic rest api")
}

type smallestaiTTSChunkedStream struct {
	resp *http.Response
}

func (s *smallestaiTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	buf := make([]byte, 4096)
	n, err := s.resp.Body.Read(buf)
	if n > 0 {
		return &tts.SynthesizedAudio{
			Frame: &model.AudioFrame{
				Data:              buf[:n],
				SampleRate:        24000,
				NumChannels:       1,
				SamplesPerChannel: uint32(n / 2),
			},
		}, nil
	}
	if err != nil {
		return nil, err
	}
	return nil, nil
}

func (s *smallestaiTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

