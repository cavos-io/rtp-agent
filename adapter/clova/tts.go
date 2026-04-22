package clova

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/model"
)

type ClovaTTS struct {
	clientID     string
	clientSecret string
	voice        string
	apiURL       string
	httpClient   *http.Client
}

type TTSOption func(*ClovaTTS)

func WithTTSBaseURL(url string) TTSOption {
	return func(t *ClovaTTS) {
		t.apiURL = url
	}
}

func WithTTSHTTPClient(client *http.Client) TTSOption {
	return func(t *ClovaTTS) {
		t.httpClient = client
	}
}

func NewClovaTTS(clientID, clientSecret, voice string, opts ...TTSOption) *ClovaTTS {
	if voice == "" {
		voice = "nara"
	}
	t := &ClovaTTS{
		clientID:     clientID,
		clientSecret: clientSecret,
		voice:        voice,
		apiURL:       "https://naveropenapi.apigw.ntruss.com/tts-premium/v1/tts",
		httpClient:   http.DefaultClient,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *ClovaTTS) Label() string { return "clova.TTS" }
func (t *ClovaTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *ClovaTTS) SampleRate() int { return 24000 }
func (t *ClovaTTS) NumChannels() int { return 1 }

func (t *ClovaTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	data := url.Values{}
	data.Set("speaker", t.voice)
	data.Set("volume", "0")
	data.Set("speed", "0")
	data.Set("pitch", "0")
	data.Set("text", text)
	data.Set("format", "mp3")

	req, err := http.NewRequestWithContext(ctx, "POST", t.apiURL, bytes.NewBufferString(data.Encode()))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-NCP-APIGW-API-KEY-ID", t.clientID)
	req.Header.Set("X-NCP-APIGW-API-KEY", t.clientSecret)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("clova tts error: %s", string(respBody))
	}

	return &clovaTTSChunkedStream{
		resp: resp,
	}, nil
}

func (t *ClovaTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("streaming tts not natively supported by clova rest api")
}

type clovaTTSChunkedStream struct {
	resp *http.Response
}

func (s *clovaTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *clovaTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

