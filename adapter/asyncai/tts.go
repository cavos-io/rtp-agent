package asyncai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

const (
	defaultAsyncAITTSBaseURL    = "https://api.async.com"
	defaultAsyncAITTSModel      = "async_flash_v1.0"
	defaultAsyncAITTSEncoding   = "pcm_s16le"
	defaultAsyncAITTSVoice      = "e0f39dc4-f691-4e78-bba5-5c636692cc04"
	defaultAsyncAITTSSampleRate = 32000
	asyncAIAPIKeyEnv            = "ASYNCAI_API_KEY"
	asyncAIAPIVersion           = "v1"
	asyncAITTSNumChannels       = 1
)

type AsyncAITTS struct {
	apiKey     string
	baseURL    string
	model      string
	language   string
	encoding   string
	voice      string
	sampleRate int
	mu         sync.Mutex
	streams    map[*asyncAITTSStream]struct{}
}

type AsyncAITTSOption func(*AsyncAITTS)

func WithAsyncAITTSBaseURL(baseURL string) AsyncAITTSOption {
	return func(t *AsyncAITTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithAsyncAITTSModel(model string) AsyncAITTSOption {
	return func(t *AsyncAITTS) {
		if model != "" {
			t.model = model
		}
	}
}

func WithAsyncAITTSVoice(voice string) AsyncAITTSOption {
	return func(t *AsyncAITTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithAsyncAITTSLanguage(language string) AsyncAITTSOption {
	return func(t *AsyncAITTS) {
		if language != "" {
			t.language = language
		}
	}
}

func WithAsyncAITTSEncoding(encoding string) AsyncAITTSOption {
	return func(t *AsyncAITTS) {
		if encoding != "" {
			t.encoding = encoding
		}
	}
}

func WithAsyncAITTSSampleRate(sampleRate int) AsyncAITTSOption {
	return func(t *AsyncAITTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func NewAsyncAITTS(apiKey string, voice string, opts ...AsyncAITTSOption) *AsyncAITTS {
	if apiKey == "" {
		apiKey = os.Getenv(asyncAIAPIKeyEnv)
	}
	provider := &AsyncAITTS{
		apiKey:     apiKey,
		baseURL:    defaultAsyncAITTSBaseURL,
		model:      defaultAsyncAITTSModel,
		encoding:   defaultAsyncAITTSEncoding,
		voice:      voice,
		sampleRate: defaultAsyncAITTSSampleRate,
		streams:    make(map[*asyncAITTSStream]struct{}),
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultAsyncAITTSVoice
	}
	return provider
}

func (t *AsyncAITTS) Label() string { return "asyncai.TTS" }
func (t *AsyncAITTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *AsyncAITTS) SampleRate() int  { return t.sampleRate }
func (t *AsyncAITTS) NumChannels() int { return asyncAITTSNumChannels }
func (t *AsyncAITTS) Model() string    { return t.model }
func (t *AsyncAITTS) Provider() string { return "AsyncAI" }

func (t *AsyncAITTS) UpdateOptions(opts ...AsyncAITTSOption) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, opt := range opts {
		opt(t)
	}
}

func (t *AsyncAITTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	return nil, fmt.Errorf("asyncai tts supports streaming only; use tts.stream()")
}

func (t *AsyncAITTS) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	streams := make([]*asyncAITTSStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.streams = make(map[*asyncAITTSStream]struct{})
	t.mu.Unlock()

	var closeErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (t *AsyncAITTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if err := t.validateStreamConfig(); err != nil {
		return nil, err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildAsyncAITTSWebsocketURL(t), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to dial asyncai tts websocket: %w", err)
	}
	initPayload, err := buildAsyncAITTSInitMessage(t)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.WriteMessage(websocket.TextMessage, initPayload); err != nil {
		conn.Close()
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &asyncAITTSStream{
		owner:      t,
		conn:       conn,
		ctx:        streamCtx,
		cancel:     cancel,
		contextID:  fmt.Sprintf("ctx-%d", time.Now().UnixNano()),
		sampleRate: t.sampleRate,
	}
	stream.writeMessage = stream.writeWebsocketMessage
	stream.closeConn = stream.closeWebsocketConn
	t.registerStream(stream)
	return stream, nil
}

func (t *AsyncAITTS) registerStream(stream *asyncAITTSStream) {
	if t == nil || stream == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.streams == nil {
		t.streams = make(map[*asyncAITTSStream]struct{})
	}
	t.streams[stream] = struct{}{}
	stream.owner = t
}

func (t *AsyncAITTS) unregisterStream(stream *asyncAITTSStream) {
	if t == nil || stream == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.streams, stream)
	if stream.owner == t {
		stream.owner = nil
	}
}

func (t *AsyncAITTS) validateStreamConfig() error {
	if t.apiKey == "" {
		return fmt.Errorf("AsyncAI API key is required, either as argument or set ASYNCAI_API_KEY environment variable")
	}
	return nil
}

func buildAsyncAITTSWebsocketURL(t *AsyncAITTS) string {
	u, _ := url.Parse(t.baseURL)
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}
	u.Path = "/text_to_speech/websocket/ws"
	q := u.Query()
	q.Set("api_key", t.apiKey)
	q.Set("version", asyncAIAPIVersion)
	u.RawQuery = q.Encode()
	return u.String()
}

func buildAsyncAITTSInitMessage(t *AsyncAITTS) ([]byte, error) {
	message := map[string]any{
		"model_id": t.model,
		"voice": map[string]any{
			"mode": "id",
			"id":   t.voice,
		},
		"output_format": map[string]any{
			"container":   "raw",
			"encoding":    t.encoding,
			"sample_rate": t.sampleRate,
		},
	}
	if t.language != "" {
		message["language"] = t.language
	}
	return json.Marshal(message)
}

func buildAsyncAITTSTextMessage(contextID, text string) ([]byte, error) {
	transcript := text
	if transcript != "" && !strings.HasSuffix(transcript, " ") {
		transcript += " "
	}
	return json.Marshal(map[string]any{
		"transcript": transcript,
		"context_id": contextID,
		"force":      true,
	})
}

func buildAsyncAITTSEndMessage(contextID string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"transcript": "",
		"context_id": contextID,
	})
}

type asyncAITTSWebsocketChunkedStream struct {
	conn       *websocket.Conn
	sampleRate int
}

func (s *asyncAITTSWebsocketChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.conn == nil {
		return nil, io.EOF
	}
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) || err == io.EOF {
				return nil, io.EOF
			}
			return nil, err
		}
		if msgType != websocket.TextMessage {
			continue
		}
		audio, done, err := asyncAITTSAudioFromWebsocketMessage(payload, s.sampleRate)
		if err != nil {
			return nil, err
		}
		if done {
			return nil, io.EOF
		}
		if audio != nil {
			return audio, nil
		}
	}
}

type asyncAITTSStream struct {
	owner       *AsyncAITTS
	conn        *websocket.Conn
	ctx         context.Context
	cancel      context.CancelFunc
	contextID   string
	sampleRate  int
	pendingText bytes.Buffer
	mu          sync.Mutex
	closed      bool

	writeMessage func([]byte) error
	closeConn    func() error
}

func (s *asyncAITTSStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if text == "" {
		return nil
	}
	if s.closed {
		return fmt.Errorf("asyncai tts stream is closed")
	}
	_, err := s.pendingText.WriteString(text)
	return err
}

func (s *asyncAITTSStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("asyncai tts stream is closed")
	}
	if s.conn == nil && s.writeMessage == nil {
		s.pendingText.Reset()
		return nil
	}
	text := s.pendingText.String()
	s.pendingText.Reset()
	if text != "" {
		payload, err := buildAsyncAITTSTextMessage(s.contextID, text)
		if err != nil {
			return err
		}
		if err := s.writeMessageData(payload); err != nil {
			s.closeAfterWriteFailureLocked()
			return err
		}
	}
	endPayload, err := buildAsyncAITTSEndMessage(s.contextID)
	if err != nil {
		return err
	}
	if err := s.writeMessageData(endPayload); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

func (s *asyncAITTSStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	if s.conn == nil && s.closeConn == nil {
		if s.owner != nil {
			s.owner.unregisterStream(s)
		}
		return nil
	}
	if s.conn != nil {
		_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	}
	err := s.closeConnection()
	if s.owner != nil {
		s.owner.unregisterStream(s)
	}
	return err
}

func (s *asyncAITTSStream) writeMessageData(payload []byte) error {
	if s.writeMessage != nil {
		return s.writeMessage(payload)
	}
	return s.writeWebsocketMessage(payload)
}

func (s *asyncAITTSStream) writeWebsocketMessage(payload []byte) error {
	return s.conn.WriteMessage(websocket.TextMessage, payload)
}

func (s *asyncAITTSStream) closeConnection() error {
	if s.closeConn != nil {
		return s.closeConn()
	}
	return s.closeWebsocketConn()
}

func (s *asyncAITTSStream) closeWebsocketConn() error {
	if s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

func (s *asyncAITTSStream) closeAfterWriteFailureLocked() {
	if s.closed {
		return
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	_ = s.closeConnection()
	if s.owner != nil {
		s.owner.unregisterStream(s)
	}
}

func (s *asyncAITTSStream) Next() (*tts.SynthesizedAudio, error) {
	if s.ctx != nil {
		select {
		case <-s.ctx.Done():
			return nil, s.ctx.Err()
		default:
		}
	}
	return (&asyncAITTSWebsocketChunkedStream{conn: s.conn, sampleRate: s.sampleRate}).Next()
}

func asyncAITTSAudioFromWebsocketMessage(payload []byte, sampleRate int) (*tts.SynthesizedAudio, bool, error) {
	var message struct {
		ContextID string `json:"context_id"`
		Audio     string `json:"audio"`
		Final     bool   `json:"final"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, err
	}
	if message.Error != "" {
		return nil, false, fmt.Errorf("asyncai tts error: %s", message.Error)
	}
	if message.Final {
		return nil, true, nil
	}
	if message.Audio == "" {
		return nil, false, nil
	}
	audio, err := base64.StdEncoding.DecodeString(message.Audio)
	if err != nil {
		return nil, false, err
	}
	if len(audio) == 0 {
		return nil, false, nil
	}
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              bytes.Clone(audio),
			SampleRate:        uint32(sampleRate),
			NumChannels:       asyncAITTSNumChannels,
			SamplesPerChannel: uint32(len(audio) / 2),
		},
		SegmentID: message.ContextID,
	}, false, nil
}
