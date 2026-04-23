package telnyx

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

type TelnyxTTS struct {
	apiKey     string
	voice      string
	baseURL    string
	httpClient *http.Client
}

type TTSOption func(*TelnyxTTS)

func WithTTSBaseURL(url string) TTSOption {
	return func(t *TelnyxTTS) {
		t.baseURL = url
	}
}

func WithTTSHTTPClient(client *http.Client) TTSOption {
	return func(t *TelnyxTTS) {
		t.httpClient = client
	}
}

func NewTelnyxTTS(apiKey string, voice string, opts ...TTSOption) *TelnyxTTS {
	if voice == "" {
		voice = "female-1"
	}
	t := &TelnyxTTS{
		apiKey:     apiKey,
		voice:      voice,
		baseURL:    "https://api.telnyx.com/v2/ai/audio/speech",
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *TelnyxTTS) Label() string { return "telnyx.TTS" }
func (t *TelnyxTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *TelnyxTTS) SampleRate() int { return 24000 }
func (t *TelnyxTTS) NumChannels() int { return 1 }

func (t *TelnyxTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {

	reqBody := map[string]interface{}{
		"input": text,
		"voice": t.voice,
		"model": "eleven_multilingual_v2", // Telnyx proxy
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
		return nil, fmt.Errorf("telnyx tts error: %s", string(respBody))
	}

	return &telnyxTTSChunkedStream{
		resp: resp,
	}, nil
}

func (t *TelnyxTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("telnyx streaming tts not natively supported by basic rest api")
}

type telnyxTTSChunkedStream struct {
	resp *http.Response
}

func (s *telnyxTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *telnyxTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

