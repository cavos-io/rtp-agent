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
	"sync"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
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
	streamCtx, cancel := context.WithCancel(ctx)
	return &speechmaticsTTSChunkedStream{
		ctx:        streamCtx,
		cancel:     cancel,
		text:       text,
		apiKey:     t.apiKey,
		baseURL:    t.baseURL,
		voice:      t.voice,
		sampleRate: t.sampleRate,
	}, nil
}

func buildSpeechmaticsTTSRequest(ctx context.Context, t *SpeechmaticsTTS, text string) (*http.Request, error) {
	return buildSpeechmaticsTTSRequestFromOptions(ctx, speechmaticsTTSRequestOptions{
		text:       text,
		apiKey:     t.apiKey,
		baseURL:    t.baseURL,
		voice:      t.voice,
		sampleRate: t.sampleRate,
	})
}

type speechmaticsTTSRequestOptions struct {
	text       string
	apiKey     string
	baseURL    string
	voice      string
	sampleRate int
}

func buildSpeechmaticsTTSRequestFromOptions(ctx context.Context, opts speechmaticsTTSRequestOptions) (*http.Request, error) {
	u, err := url.Parse(opts.baseURL + "/generate/" + url.PathEscape(opts.voice))
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("output_format", fmt.Sprintf("pcm_%d", opts.sampleRate))
	q.Set("sm-sdk", speechmaticsTTSSDKParam)
	q.Set("sm-app", speechmaticsTTSAppParam)
	u.RawQuery = q.Encode()

	body := map[string]string{"text": opts.text}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+opts.apiKey)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (t *SpeechmaticsTTS) UpdateOptions(opts ...SpeechmaticsTTSOption) {
	sampleRate := t.sampleRate
	baseURL := t.baseURL
	for _, opt := range opts {
		opt(t)
	}
	t.sampleRate = sampleRate
	t.baseURL = baseURL
}

func (t *SpeechmaticsTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("speechmatics streaming tts is unsupported")
}

type speechmaticsTTSChunkedStream struct {
	mu         sync.Mutex
	stream     io.ReadCloser
	ctx        context.Context
	cancel     context.CancelFunc
	text       string
	apiKey     string
	baseURL    string
	voice      string
	sampleRate int
	requested  bool
	pending    []byte
	finalReady bool
	finalSent  bool
	closed     bool
}

func (s *speechmaticsTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.isClosedOrFinal() {
		return nil, io.EOF
	}
	if err := s.ensureStream(); err != nil {
		if s.isClosedOrFinal() {
			return nil, io.EOF
		}
		return nil, err
	}
	if s.isClosedOrFinal() {
		return nil, io.EOF
	}
	if s.stream == nil {
		return nil, io.EOF
	}
	if s.finalReady {
		s.finalReady = false
		return s.emitFinal()
	}
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
						return s.emitFinal()
					}
					return nil, err
				}
				continue
			}
			frameData := data[:completeLen]
			s.pending = data[completeLen:]
			if err == io.EOF {
				s.pending = nil
				s.finalReady = true
			}
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
				return s.emitFinal()
			}
			return nil, err
		}
	}
}

func (s *speechmaticsTTSChunkedStream) ensureStream() error {
	if s.stream != nil || s.requested || s.text == "" {
		return nil
	}
	s.requested = true
	req, err := buildSpeechmaticsTTSRequestFromOptions(s.ctx, speechmaticsTTSRequestOptions{
		text:       s.text,
		apiKey:     s.apiKey,
		baseURL:    s.baseURL,
		voice:      s.voice,
		sampleRate: s.sampleRate,
	})
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return llm.NewAPIConnectionError(err.Error())
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return llm.NewAPIStatusError("Speechmatics TTS request failed", resp.StatusCode, "", string(respBody))
	}
	s.mu.Lock()
	if s.closed || s.finalSent {
		s.mu.Unlock()
		resp.Body.Close()
		return io.EOF
	}
	s.stream = resp.Body
	s.mu.Unlock()
	return nil
}

func (s *speechmaticsTTSChunkedStream) emitFinal() (*tts.SynthesizedAudio, error) {
	if s.finalSent {
		return nil, io.EOF
	}
	s.finalSent = true
	return &tts.SynthesizedAudio{IsFinal: true}, nil
}

func (s *speechmaticsTTSChunkedStream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.finalSent = true
	stream := s.stream
	cancel := s.cancel
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if stream == nil {
		return nil
	}
	return stream.Close()
}

func (s *speechmaticsTTSChunkedStream) isClosedOrFinal() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed || s.finalSent
}
