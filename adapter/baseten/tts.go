package baseten

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

const (
	defaultBasetenTTSVoice       = "tara"
	defaultBasetenTTSLanguage    = "en"
	defaultBasetenTTSTemperature = 0.6
	defaultBasetenTTSSampleRate  = 24000
	defaultBasetenTTSMaxTokens   = 2000
	defaultBasetenTTSBufferSize  = 10
	basetenTTSEndSentinel        = "__END__"
)

type BasetenTTS struct {
	apiKey        string
	modelEndpoint string
	voice         string
	language      string
	temperature   float64
	maxTokens     int
	bufferSize    int
	sampleRate    int
}

type BasetenTTSOption func(*BasetenTTS)

func WithBasetenTTSModelEndpoint(endpoint string) BasetenTTSOption {
	return func(t *BasetenTTS) {
		if endpoint != "" {
			t.modelEndpoint = endpoint
		}
	}
}

func WithBasetenTTSVoice(voice string) BasetenTTSOption {
	return func(t *BasetenTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithBasetenTTSLanguage(language string) BasetenTTSOption {
	return func(t *BasetenTTS) {
		if language != "" {
			t.language = language
		}
	}
}

func WithBasetenTTSTemperature(temperature float64) BasetenTTSOption {
	return func(t *BasetenTTS) {
		t.temperature = temperature
	}
}

func WithBasetenTTSMaxTokens(maxTokens int) BasetenTTSOption {
	return func(t *BasetenTTS) {
		if maxTokens > 0 {
			t.maxTokens = maxTokens
		}
	}
}

func WithBasetenTTSBufferSize(bufferSize int) BasetenTTSOption {
	return func(t *BasetenTTS) {
		if bufferSize > 0 {
			t.bufferSize = bufferSize
		}
	}
}

func NewBasetenTTS(apiKey string, model string, opts ...BasetenTTSOption) *BasetenTTS {
	endpoint := model
	if endpoint == "" {
		endpoint = "xtts-v2"
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") &&
		!strings.HasPrefix(endpoint, "ws://") && !strings.HasPrefix(endpoint, "wss://") {
		endpoint = fmt.Sprintf("https://model-%s.api.baseten.co/environments/production/predict", endpoint)
	}
	provider := &BasetenTTS{
		apiKey:        apiKey,
		modelEndpoint: endpoint,
		voice:         defaultBasetenTTSVoice,
		language:      defaultBasetenTTSLanguage,
		temperature:   defaultBasetenTTSTemperature,
		maxTokens:     defaultBasetenTTSMaxTokens,
		bufferSize:    defaultBasetenTTSBufferSize,
		sampleRate:    defaultBasetenTTSSampleRate,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (t *BasetenTTS) Label() string { return "baseten.TTS" }
func (t *BasetenTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: strings.HasPrefix(t.modelEndpoint, "ws://") || strings.HasPrefix(t.modelEndpoint, "wss://"), AlignedTranscript: false}
}
func (t *BasetenTTS) SampleRate() int  { return t.sampleRate }
func (t *BasetenTTS) NumChannels() int { return 1 }

func (t *BasetenTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildBasetenTTSRequest(ctx, t, text)
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
		return nil, fmt.Errorf("baseten tts error: %s", string(respBody))
	}
	return &basetenTTSChunkedStream{body: resp.Body, sampleRate: t.sampleRate}, nil
}

func buildBasetenTTSRequest(ctx context.Context, t *BasetenTTS, text string) (*http.Request, error) {
	reqBody := map[string]interface{}{
		"prompt":      text,
		"voice":       t.voice,
		"temperature": t.temperature,
		"language":    t.language,
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.modelEndpoint, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Api-Key "+t.apiKey)
	return req, nil
}

func (t *BasetenTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if !t.Capabilities().Streaming {
		return nil, fmt.Errorf("baseten tts streaming requires a websocket model endpoint")
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, t.modelEndpoint, buildBasetenTTSHeaders(t))
	if err != nil {
		return nil, fmt.Errorf("failed to dial baseten tts websocket: %w", err)
	}
	if err := writeBasetenTTSWebsocketJSON(conn, buildBasetenTTSStreamSetup(t)); err != nil {
		conn.Close()
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	return &basetenTTSSynthesizeStream{
		conn:       conn,
		ctx:        streamCtx,
		cancel:     cancel,
		sampleRate: t.sampleRate,
	}, nil
}

func buildBasetenTTSHeaders(t *BasetenTTS) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", "Api-Key "+t.apiKey)
	return headers
}

func buildBasetenTTSStreamSetup(t *BasetenTTS) map[string]any {
	return map[string]any{
		"voice":       t.voice,
		"max_tokens":  t.maxTokens,
		"buffer_size": t.bufferSize,
	}
}

func writeBasetenTTSWebsocketJSON(conn *websocket.Conn, message map[string]any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, payload)
}

type basetenTTSChunkedStream struct {
	body       io.ReadCloser
	sampleRate int
}

func (s *basetenTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	buf := make([]byte, 4096)
	n, err := s.body.Read(buf)
	if err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, err
	}
	if n == 0 {
		return nil, io.EOF
	}
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              buf[:n],
			SampleRate:        uint32(s.sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(n / 2),
		},
	}, nil
}

func (s *basetenTTSChunkedStream) Close() error {
	return s.body.Close()
}

type basetenTTSSynthesizeStream struct {
	conn       *websocket.Conn
	ctx        context.Context
	cancel     context.CancelFunc
	sampleRate int
	mu         sync.Mutex
	closed     bool
}

func (s *basetenTTSSynthesizeStream) PushText(text string) error {
	if text == "" {
		return nil
	}
	return s.conn.WriteMessage(websocket.TextMessage, []byte(text))
}

func (s *basetenTTSSynthesizeStream) Flush() error {
	return nil
}

func (s *basetenTTSSynthesizeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	_ = s.conn.WriteMessage(websocket.TextMessage, []byte(basetenTTSEndSentinel))
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *basetenTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
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
		if msgType != websocket.BinaryMessage {
			continue
		}
		return basetenTTSAudioFrame(payload, s.sampleRate), nil
	}
}

func basetenTTSAudioFrame(audio []byte, sampleRate int) *tts.SynthesizedAudio {
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              bytes.Clone(audio),
			SampleRate:        uint32(sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(len(audio) / 2),
		},
	}
}
