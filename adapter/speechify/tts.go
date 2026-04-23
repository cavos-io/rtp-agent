package speechify

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

type SpeechifyTTS struct {
	apiKey     string
	voice      string
	baseURL    string
	httpClient *http.Client
}

type Option func(*SpeechifyTTS)

func WithBaseURL(url string) Option {
	return func(t *SpeechifyTTS) {
		t.baseURL = url
	}
}

func WithHTTPClient(client *http.Client) Option {
	return func(t *SpeechifyTTS) {
		t.httpClient = client
	}
}

func NewSpeechifyTTS(apiKey string, voice string, opts ...Option) *SpeechifyTTS {
	if voice == "" {
		voice = "snoop"
	}
	t := &SpeechifyTTS{
		apiKey:     apiKey,
		voice:      voice,
		baseURL:    "https://api.speechify.com/v1/tts/synthesize",
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *SpeechifyTTS) Label() string { return "speechify.TTS" }
func (t *SpeechifyTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *SpeechifyTTS) SampleRate() int { return 24000 }
func (t *SpeechifyTTS) NumChannels() int { return 1 }

func (t *SpeechifyTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {

	reqBody := map[string]interface{}{
		"text":    text,
		"voiceId": t.voice,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

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
		return nil, fmt.Errorf("speechify tts error: %s", string(respBody))
	}

	return &speechifyTTSChunkedStream{
		resp: resp,
	}, nil
}

func (t *SpeechifyTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("streaming tts is not supported by the Speechify TTS API")
}

type speechifyTTSChunkedStream struct {
	resp *http.Response
}

func (s *speechifyTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *speechifyTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

