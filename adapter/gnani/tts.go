package gnani

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

const (
	defaultBaseURL     = "https://api.vachana.ai"
	defaultVoice       = "Karan"
	defaultModel       = "vachana-voice-v3"
	defaultSampleRate  = 16000
	defaultEncoding    = "linear_pcm"
	defaultContainer   = "wav"
	defaultNumChannels = 1
	defaultSampleWidth = 2
	defaultTTSLanguage = "hi"
	wavHeaderSize      = 44
)

type TTS struct {
	apiKey      string
	baseURL     string
	voice       string
	model       string
	sampleRate  int
	encoding    string
	container   string
	numChannels int
	sampleWidth int
	language    string
}

type Option func(*TTS)

func WithBaseURL(baseURL string) Option {
	return func(t *TTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithVoice(voice string) Option {
	return func(t *TTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithModel(model string) Option {
	return func(t *TTS) {
		if model != "" {
			t.model = model
		}
	}
}

func WithSampleRate(sampleRate int) Option {
	return func(t *TTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithEncoding(encoding string) Option {
	return func(t *TTS) {
		if encoding != "" {
			t.encoding = encoding
		}
	}
}

func WithContainer(container string) Option {
	return func(t *TTS) {
		if container != "" {
			t.container = container
		}
	}
}

func WithNumChannels(numChannels int) Option {
	return func(t *TTS) {
		if numChannels > 0 {
			t.numChannels = numChannels
		}
	}
}

func WithSampleWidth(sampleWidth int) Option {
	return func(t *TTS) {
		if sampleWidth > 0 {
			t.sampleWidth = sampleWidth
		}
	}
}

func WithLanguage(language string) Option {
	return func(t *TTS) {
		if language != "" {
			t.language = language
		}
	}
}

func NewTTS(apiKey string, opts ...Option) *TTS {
	provider := &TTS{
		apiKey:      resolveGnaniAPIKey(apiKey),
		baseURL:     defaultBaseURL,
		voice:       defaultVoice,
		model:       defaultModel,
		sampleRate:  defaultSampleRate,
		encoding:    defaultEncoding,
		container:   defaultContainer,
		numChannels: defaultNumChannels,
		sampleWidth: defaultSampleWidth,
		language:    defaultTTSLanguage,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (t *TTS) Label() string { return "gnani.TTS" }
func (t *TTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *TTS) SampleRate() int  { return t.sampleRate }
func (t *TTS) NumChannels() int { return t.numChannels }

func (t *TTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildTTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("gnani tts error: %s", string(respBody))
	}
	return &ttsChunkedStream{resp: resp, sampleRate: t.sampleRate, numChannels: t.numChannels}, nil
}

func buildTTSRequest(ctx context.Context, t *TTS, text string) (*http.Request, error) {
	reqBody := map[string]interface{}{
		"text":  text,
		"voice": t.voice,
		"model": t.model,
		"audio_config": map[string]interface{}{
			"sample_rate":  t.sampleRate,
			"encoding":     t.encoding,
			"num_channels": t.numChannels,
			"sample_width": t.sampleWidth,
			"container":    t.container,
		},
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+"/api/v1/tts/inference", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key-ID", t.apiKey)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (t *TTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	return &gnaniTTSSynthesizeStream{
		ctx:         streamCtx,
		cancel:      cancel,
		provider:    t,
		sampleRate:  t.sampleRate,
		numChannels: t.numChannels,
		events:      make(chan *tts.SynthesizedAudio, 100),
		errCh:       make(chan error, 1),
	}, nil
}

type ttsChunkedStream struct {
	resp        *http.Response
	sampleRate  int
	numChannels int
}

func (s *ttsChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	buf := make([]byte, 4096)
	n, err := s.resp.Body.Read(buf)
	if err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, err
	}
	data := stripWAVHeader(buf[:n])
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              data,
			SampleRate:        uint32(s.sampleRate),
			NumChannels:       uint32(s.numChannels),
			SamplesPerChannel: uint32(len(data) / 2 / s.numChannels),
		},
	}, nil
}

func (s *ttsChunkedStream) Close() error {
	return s.resp.Body.Close()
}

func stripWAVHeader(data []byte) []byte {
	if len(data) > wavHeaderSize && string(data[:4]) == "RIFF" && string(data[8:12]) == "WAVE" {
		return data[wavHeaderSize:]
	}
	return data
}

func buildGnaniTTSWebsocketURL(t *TTS) *url.URL {
	baseURL := strings.TrimRight(t.baseURL, "/")
	switch {
	case strings.HasPrefix(baseURL, "https://"):
		baseURL = "wss://" + strings.TrimPrefix(baseURL, "https://")
	case strings.HasPrefix(baseURL, "http://"):
		baseURL = "ws://" + strings.TrimPrefix(baseURL, "http://")
	case !strings.HasPrefix(baseURL, "wss://") && !strings.HasPrefix(baseURL, "ws://"):
		baseURL = "wss://" + baseURL
	}
	wsURL, err := url.Parse(baseURL + "/api/v1/tts")
	if err != nil {
		return &url.URL{Scheme: "wss", Host: strings.TrimPrefix(baseURL, "wss://"), Path: "/api/v1/tts"}
	}
	return wsURL
}

func buildGnaniTTSWebsocketHeaders(t *TTS) http.Header {
	headers := make(http.Header)
	headers.Set("X-API-Key-ID", t.apiKey)
	headers.Set("Content-Type", "application/json")
	return headers
}

func buildGnaniTTSWebsocketRequest(t *TTS, text string) ([]byte, error) {
	reqBody := map[string]interface{}{
		"text":     text,
		"voice":    t.voice,
		"model":    t.model,
		"language": t.language,
		"audio_config": map[string]interface{}{
			"sample_rate":  t.sampleRate,
			"encoding":     t.encoding,
			"num_channels": t.numChannels,
			"sample_width": t.sampleWidth,
			"container":    t.container,
		},
	}
	return json.Marshal(reqBody)
}

type gnaniTTSSynthesizeStream struct {
	ctx         context.Context
	cancel      context.CancelFunc
	provider    *TTS
	sampleRate  int
	numChannels int
	events      chan *tts.SynthesizedAudio
	errCh       chan error
	textParts   []string
	mu          sync.Mutex
	closed      bool
	started     bool
	conn        *websocket.Conn
}

func (s *gnaniTTSSynthesizeStream) PushText(text string) error {
	if text == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return fmt.Errorf("gnani tts stream already flushed")
	}
	s.textParts = append(s.textParts, text)
	return nil
}

func (s *gnaniTTSSynthesizeStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}
	fullText := strings.TrimSpace(strings.Join(s.textParts, ""))
	if fullText == "" {
		s.started = true
		close(s.events)
		return nil
	}
	conn, _, err := websocket.DefaultDialer.DialContext(s.ctx, buildGnaniTTSWebsocketURL(s.provider).String(), buildGnaniTTSWebsocketHeaders(s.provider))
	if err != nil {
		return fmt.Errorf("failed to dial gnani tts websocket: %w", err)
	}
	request, err := buildGnaniTTSWebsocketRequest(s.provider, fullText)
	if err != nil {
		conn.Close()
		return err
	}
	if err := conn.WriteMessage(websocket.TextMessage, request); err != nil {
		conn.Close()
		return err
	}
	s.conn = conn
	s.started = true
	go s.readLoop()
	return nil
}

func (s *gnaniTTSSynthesizeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	if s.conn == nil {
		return nil
	}
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *gnaniTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
	select {
	case audio, ok := <-s.events:
		if !ok {
			select {
			case err := <-s.errCh:
				return nil, err
			default:
				return nil, io.EOF
			}
		}
		return audio, nil
	case err := <-s.errCh:
		return nil, err
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

func (s *gnaniTTSSynthesizeStream) readLoop() {
	defer close(s.events)
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) && err != io.EOF {
				s.errCh <- err
			}
			return
		}
		switch msgType {
		case websocket.BinaryMessage:
			data := stripWAVHeader(payload)
			if len(data) > 0 {
				s.events <- gnaniTTSAudioFrame(data, s.sampleRate, s.numChannels, false)
			}
		case websocket.TextMessage:
			audio, done, err := gnaniTTSAudioFromWebsocketMessage(payload, s.sampleRate, s.numChannels)
			if err != nil {
				s.errCh <- err
				return
			}
			if audio != nil {
				s.events <- audio
			}
			if done {
				return
			}
		}
	}
}

func gnaniTTSAudioFromWebsocketMessage(payload []byte, sampleRate int, numChannels int) (*tts.SynthesizedAudio, bool, error) {
	var message struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Data    struct {
			Audio string `json:"audio"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, err
	}
	switch message.Type {
	case "audio":
		if message.Data.Audio == "" {
			return nil, false, nil
		}
		audio, err := base64.StdEncoding.DecodeString(message.Data.Audio)
		if err != nil {
			return nil, false, err
		}
		audio = stripWAVHeader(audio)
		if len(audio) == 0 {
			return nil, false, nil
		}
		return gnaniTTSAudioFrame(audio, sampleRate, numChannels, false), false, nil
	case "complete":
		if message.Data.Audio == "" {
			return nil, true, nil
		}
		audio, err := base64.StdEncoding.DecodeString(message.Data.Audio)
		if err != nil {
			return nil, false, err
		}
		audio = stripWAVHeader(audio)
		if len(audio) == 0 {
			return nil, true, nil
		}
		return gnaniTTSAudioFrame(audio, sampleRate, numChannels, true), true, nil
	case "error":
		if message.Message == "" {
			message.Message = string(payload)
		}
		return nil, false, fmt.Errorf("gnani tts stream error: %s", message.Message)
	default:
		return nil, false, nil
	}
}

func gnaniTTSAudioFrame(audio []byte, sampleRate int, numChannels int, final bool) *tts.SynthesizedAudio {
	if numChannels <= 0 {
		numChannels = 1
	}
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              bytes.Clone(audio),
			SampleRate:        uint32(sampleRate),
			NumChannels:       uint32(numChannels),
			SamplesPerChannel: uint32(len(audio) / 2 / numChannels),
		},
		IsFinal: final,
	}
}
