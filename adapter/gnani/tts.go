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
	defaultLanguage    = "hi"
	defaultNumChannels = 1
	defaultSampleWidth = 2
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
	language    string
	numChannels int
	sampleWidth int
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

func WithLanguage(language string) Option {
	return func(t *TTS) {
		if language != "" {
			t.language = language
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

func NewTTS(apiKey string, opts ...Option) *TTS {
	provider := &TTS{
		apiKey:      apiKey,
		baseURL:     defaultBaseURL,
		voice:       defaultVoice,
		model:       defaultModel,
		sampleRate:  defaultSampleRate,
		encoding:    defaultEncoding,
		container:   defaultContainer,
		language:    defaultLanguage,
		numChannels: defaultNumChannels,
		sampleWidth: defaultSampleWidth,
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
	reqBody := buildTTSPayload(t, text)
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
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildTTSWebsocketURL(t), buildTTSHeaders(t))
	if err != nil {
		return nil, fmt.Errorf("failed to dial gnani tts websocket: %w", err)
	}
	streamCtx, cancel := context.WithCancel(ctx)
	return &ttsStream{
		conn:        conn,
		ctx:         streamCtx,
		cancel:      cancel,
		provider:    t,
		sampleRate:  t.sampleRate,
		numChannels: t.numChannels,
	}, nil
}

func buildTTSWebsocketURL(t *TTS) string {
	u, err := url.Parse(strings.TrimRight(t.baseURL, "/"))
	if err != nil {
		return strings.TrimRight(t.baseURL, "/") + "/api/v1/tts"
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	case "":
		u.Scheme = "wss"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/tts"
	u.RawQuery = ""
	return u.String()
}

func buildTTSHeaders(t *TTS) http.Header {
	headers := make(http.Header)
	headers.Set("X-API-Key-ID", t.apiKey)
	headers.Set("Content-Type", "application/json")
	return headers
}

func buildTTSWebsocketRequest(t *TTS, text string) ([]byte, error) {
	reqBody := buildTTSPayload(t, text)
	reqBody["language"] = t.language
	return json.Marshal(reqBody)
}

func buildTTSPayload(t *TTS, text string) map[string]interface{} {
	return map[string]interface{}{
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

type ttsStream struct {
	conn        *websocket.Conn
	ctx         context.Context
	cancel      context.CancelFunc
	provider    *TTS
	sampleRate  int
	numChannels int
	pendingText bytes.Buffer
	mu          sync.Mutex
	closed      bool
}

func (s *ttsStream) PushText(text string) error {
	if text == "" {
		return nil
	}
	_, err := s.pendingText.WriteString(text)
	return err
}

func (s *ttsStream) Flush() error {
	text := strings.TrimSpace(s.pendingText.String())
	s.pendingText.Reset()
	if text == "" {
		return nil
	}
	payload, err := buildTTSWebsocketRequest(s.provider, text)
	if err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.TextMessage, payload)
}

func (s *ttsStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *ttsStream) Next() (*tts.SynthesizedAudio, error) {
	select {
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	default:
	}
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) || err == io.EOF {
				return nil, io.EOF
			}
			return nil, err
		}
		if msgType == websocket.BinaryMessage {
			return ttsAudioFrame(stripWAVHeader(payload), s.sampleRate, s.numChannels), nil
		}
		if msgType != websocket.TextMessage {
			continue
		}
		audio, done, err := ttsAudioFromWebsocketMessage(payload, s.sampleRate, s.numChannels)
		if err != nil {
			return nil, err
		}
		if audio != nil {
			return audio, nil
		}
		if done {
			return nil, io.EOF
		}
	}
}

func ttsAudioFromWebsocketMessage(payload []byte, sampleRate int, numChannels int) (*tts.SynthesizedAudio, bool, error) {
	if !json.Valid(payload) {
		return ttsAudioFrame(stripWAVHeader(payload), sampleRate, numChannels), false, nil
	}
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
		return ttsAudioFrame(stripWAVHeader(audio), sampleRate, numChannels), false, nil
	case "complete":
		if message.Data.Audio == "" {
			return nil, true, nil
		}
		audio, err := base64.StdEncoding.DecodeString(message.Data.Audio)
		if err != nil {
			return nil, false, err
		}
		return ttsAudioFrame(stripWAVHeader(audio), sampleRate, numChannels), true, nil
	case "error":
		if message.Message == "" {
			message.Message = "unknown gnani tts stream error"
		}
		return nil, false, fmt.Errorf("gnani tts stream error: %s", message.Message)
	default:
		return nil, false, nil
	}
}

func ttsAudioFrame(data []byte, sampleRate int, numChannels int) *tts.SynthesizedAudio {
	if numChannels <= 0 {
		numChannels = 1
	}
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              bytes.Clone(data),
			SampleRate:        uint32(sampleRate),
			NumChannels:       uint32(numChannels),
			SamplesPerChannel: uint32(len(data) / 2 / numChannels),
		},
	}
}
