package speechmatics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
)

const (
	defaultSpeechmaticsTTSBaseURL    = "https://preview.tts.speechmatics.com"
	defaultSpeechmaticsTTSVoice      = "sarah"
	defaultSpeechmaticsTTSSampleRate = 16000
	speechmaticsTTSSDKParam          = "livekit-plugins-go"
	speechmaticsTTSAppParam          = "rtp-agent"
)

type SpeechmaticsTTS struct {
	apiKey     string
	voice      string
	sampleRate int
	baseURL    string
}

type SpeechmaticsTTSOption func(*SpeechmaticsTTS)

func WithSpeechmaticsTTSVoice(voice string) SpeechmaticsTTSOption {
	return func(t *SpeechmaticsTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithSpeechmaticsTTSSampleRate(sampleRate int) SpeechmaticsTTSOption {
	return func(t *SpeechmaticsTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithSpeechmaticsTTSBaseURL(baseURL string) SpeechmaticsTTSOption {
	return func(t *SpeechmaticsTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func NewSpeechmaticsTTS(apiKey string, opts ...SpeechmaticsTTSOption) *SpeechmaticsTTS {
	if apiKey == "" {
		apiKey = os.Getenv(speechmaticsAPIKeyEnv)
	}
	provider := &SpeechmaticsTTS{
		apiKey:     apiKey,
		voice:      defaultSpeechmaticsTTSVoice,
		sampleRate: defaultSpeechmaticsTTSSampleRate,
		baseURL:    defaultSpeechmaticsTTSBaseURL,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (t *SpeechmaticsTTS) Label() string { return "speechmatics.TTS" }
func (t *SpeechmaticsTTS) Model() string { return "unknown" }
func (t *SpeechmaticsTTS) Provider() string {
	return "Speechmatics"
}

func (t *SpeechmaticsTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *SpeechmaticsTTS) SampleRate() int  { return t.sampleRate }
func (t *SpeechmaticsTTS) NumChannels() int { return 1 }

func (t *SpeechmaticsTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildSpeechmaticsTTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("speechmatics tts error: %s", string(respBody))
	}

	return &speechmaticsTTSChunkedStream{
		stream:     resp.Body,
		sampleRate: t.sampleRate,
	}, nil
}

func buildSpeechmaticsTTSRequest(ctx context.Context, t *SpeechmaticsTTS, text string) (*http.Request, error) {
	u, err := url.Parse(t.baseURL + "/generate/" + url.PathEscape(t.voice))
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("output_format", fmt.Sprintf("pcm_%d", t.sampleRate))
	q.Set("sm-sdk", speechmaticsTTSSDKParam)
	q.Set("sm-app", speechmaticsTTSAppParam)
	u.RawQuery = q.Encode()

	body := map[string]string{"text": text}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (t *SpeechmaticsTTS) UpdateOptions(opts ...SpeechmaticsTTSOption) {
	for _, opt := range opts {
		opt(t)
	}
}

func (t *SpeechmaticsTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("speechmatics streaming tts is unsupported")
}

type speechmaticsTTSChunkedStream struct {
	stream     io.ReadCloser
	sampleRate int
	pending    []byte
}

func (s *speechmaticsTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	for {
		buf := make([]byte, 4096)
		n, err := s.stream.Read(buf)
		if n > 0 {
			data := append(s.pending, buf[:n]...)
			completeLen := len(data) - len(data)%2
			if completeLen == 0 {
				s.pending = data
				if err != nil {
					if err == io.EOF {
						s.pending = nil
						return nil, io.EOF
					}
					return nil, err
				}
				continue
			}
			frameData := data[:completeLen]
			s.pending = data[completeLen:]
			return &tts.SynthesizedAudio{
				Frame: &model.AudioFrame{
					Data:              frameData,
					SampleRate:        uint32(s.sampleRate),
					NumChannels:       1,
					SamplesPerChannel: uint32(len(frameData) / 2),
				},
			}, nil
		}
		if err != nil {
			if err == io.EOF {
				s.pending = nil
				return nil, io.EOF
			}
			return nil, err
		}
	}
}

func (s *speechmaticsTTSChunkedStream) Close() error {
	return s.stream.Close()
}
