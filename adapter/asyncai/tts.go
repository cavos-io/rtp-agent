package asyncai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/library/tokenize"
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

type TTS struct {
	apiKey     string
	baseURL    string
	model      string
	language   string
	encoding   string
	voice      string
	sampleRate int
	mu         sync.Mutex
	closed     bool
	streams    map[*asyncAITTSStream]struct{}
}

type TTSOption func(*TTS)

func WithAsyncAITTSBaseURL(baseURL string) TTSOption {
	return func(t *TTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithAsyncAITTSModel(model string) TTSOption {
	return func(t *TTS) {
		if model != "" {
			t.model = model
		}
	}
}

func WithAsyncAITTSVoice(voice string) TTSOption {
	return func(t *TTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithAsyncAITTSLanguage(language string) TTSOption {
	return func(t *TTS) {
		if language != "" {
			t.language = language
		}
	}
}

func WithAsyncAITTSEncoding(encoding string) TTSOption {
	return func(t *TTS) {
		if encoding != "" {
			t.encoding = encoding
		}
	}
}

func WithAsyncAITTSSampleRate(sampleRate int) TTSOption {
	return func(t *TTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func NewTTS(apiKey string, voice string, opts ...TTSOption) *TTS {
	if apiKey == "" {
		apiKey = os.Getenv(asyncAIAPIKeyEnv)
	}
	provider := &TTS{
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

func (t *TTS) Label() string { return "asyncai.TTS" }
func (t *TTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *TTS) SampleRate() int  { return t.sampleRate }
func (t *TTS) NumChannels() int { return asyncAITTSNumChannels }
func (t *TTS) Model() string    { return t.model }
func (t *TTS) Provider() string { return "AsyncAI" }

func (t *TTS) UpdateOptions(opts ...TTSOption) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	candidate := &TTS{
		apiKey:     t.apiKey,
		baseURL:    t.baseURL,
		model:      t.model,
		language:   t.language,
		encoding:   t.encoding,
		voice:      t.voice,
		sampleRate: t.sampleRate,
	}
	for _, opt := range opts {
		opt(candidate)
	}
	t.model = candidate.model
	t.language = candidate.language
	t.voice = candidate.voice
}

func (t *TTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	return nil, fmt.Errorf("asyncai tts supports streaming only; use tts.stream()")
}

func (t *TTS) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
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

func (t *TTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if err := t.validateStreamConfig(); err != nil {
		return nil, err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildAsyncAITTSWebsocketURL(t), nil)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, llm.NewAPITimeoutError(err.Error())
		}
		var timeoutErr interface{ Timeout() bool }
		if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
			return nil, llm.NewAPITimeoutError(err.Error())
		}
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("failed to dial asyncai tts websocket: %v", err))
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
	if !t.registerStream(stream) {
		stream.Close()
		return nil, io.ErrClosedPipe
	}
	return stream, nil
}

func (t *TTS) registerStream(stream *asyncAITTSStream) bool {
	if t == nil || stream == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	if t.streams == nil {
		t.streams = make(map[*asyncAITTSStream]struct{})
	}
	t.streams[stream] = struct{}{}
	stream.owner = t
	return true
}

func (t *TTS) unregisterStream(stream *asyncAITTSStream) {
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

func (t *TTS) isClosed() bool {
	if t == nil {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

func (t *TTS) validateStreamConfig() error {
	if t.apiKey == "" {
		return fmt.Errorf("AsyncAI API key is required, either as argument or set ASYNCAI_API_KEY environment variable")
	}
	return nil
}

func buildAsyncAITTSWebsocketURL(t *TTS) string {
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

func buildAsyncAITTSInitMessage(t *TTS) ([]byte, error) {
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
	finalSeen  bool
}

func (s *asyncAITTSWebsocketChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.conn == nil {
		return nil, io.EOF
	}
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			if s.finalSeen && (websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) || err == io.EOF) {
				return nil, io.EOF
			}
			return nil, asyncAITTSReadError(err)
		}
		if msgType != websocket.TextMessage {
			continue
		}
		audio, done, err := asyncAITTSAudioFromWebsocketMessage(payload, s.sampleRate)
		if err != nil {
			return nil, err
		}
		if done {
			s.finalSeen = true
			return audio, nil
		}
		if audio != nil {
			return audio, nil
		}
	}
}

func asyncAITTSReadError(err error) error {
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return llm.NewAPIStatusError("Async connection closed unexpectedly", closeErr.Code, "", err.Error())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return llm.NewAPITimeoutError(err.Error())
	}
	var timeoutErr interface{ Timeout() bool }
	if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
		return llm.NewAPITimeoutError(err.Error())
	}
	return llm.NewAPIConnectionError(fmt.Sprintf("Async websocket receive failed: %v", err))
}

type asyncAITTSStream struct {
	owner       *TTS
	conn        *websocket.Conn
	ctx         context.Context
	cancel      context.CancelFunc
	contextID   string
	sampleRate  int
	pendingText bytes.Buffer
	finalSeen   bool
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
		return io.ErrClosedPipe
	}
	if _, err := s.pendingText.WriteString(text); err != nil {
		return err
	}
	if s.conn == nil && s.writeMessage == nil {
		return nil
	}
	if err := s.sendCompleteSentencesLocked(); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

func (s *asyncAITTSStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.conn == nil && s.writeMessage == nil {
		s.pendingText.Reset()
		return nil
	}
	if err := s.flushPendingTextLocked(); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

func (s *asyncAITTSStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.conn == nil && s.writeMessage == nil {
		s.pendingText.Reset()
		return nil
	}
	if err := s.flushPendingTextLocked(); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
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

func (s *asyncAITTSStream) flushPendingTextLocked() error {
	text := s.pendingText.String()
	s.pendingText.Reset()
	if text == "" {
		return nil
	}
	text = strings.Join(tokenize.NewBasicSentenceTokenizer().Tokenize(text, ""), " ")
	return s.sendTextLocked(text)
}

func (s *asyncAITTSStream) sendCompleteSentencesLocked() error {
	for {
		text := s.pendingText.String()
		tokens := tokenize.NewBasicSentenceTokenizer().Tokenize(text, "")
		if len(tokens) <= 1 {
			return nil
		}
		sentence := tokens[0]
		if err := s.sendTextLocked(sentence); err != nil {
			return err
		}
		tokenIdx := strings.Index(text, sentence)
		if tokenIdx < 0 {
			s.pendingText.Reset()
			s.pendingText.WriteString(strings.TrimSpace(strings.TrimPrefix(text, sentence)))
			continue
		}
		tail := strings.TrimLeftFunc(text[tokenIdx+len(sentence):], func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\n' || r == '\r'
		})
		s.pendingText.Reset()
		s.pendingText.WriteString(tail)
	}
}

func (s *asyncAITTSStream) sendTextLocked(text string) error {
	if text == "" {
		return nil
	}
	payload, err := buildAsyncAITTSTextMessage(s.contextID, text)
	if err != nil {
		return err
	}
	return s.writeMessageData(payload)
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
	if s.isClosed() {
		return nil, io.EOF
	}
	if s.ctx != nil {
		select {
		case <-s.ctx.Done():
			if s.isClosed() {
				return nil, io.EOF
			}
			return nil, s.ctx.Err()
		default:
		}
	}
	s.mu.Lock()
	finalSeen := s.finalSeen
	s.mu.Unlock()
	chunked := &asyncAITTSWebsocketChunkedStream{conn: s.conn, sampleRate: s.sampleRate, finalSeen: finalSeen}
	audio, err := chunked.Next()
	s.mu.Lock()
	s.finalSeen = chunked.finalSeen
	s.mu.Unlock()
	return audio, err
}

func (s *asyncAITTSStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
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
		return nil, false, nil
	}
	if message.Final {
		return &tts.SynthesizedAudio{IsFinal: true, SegmentID: message.ContextID}, true, nil
	}
	if message.Audio == "" {
		return nil, false, nil
	}
	audio, err := asyncAITTSDecodeBase64Audio(message.Audio)
	if err != nil {
		return nil, false, nil
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

func asyncAITTSDecodeBase64Audio(data string) ([]byte, error) {
	clean := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		b := data[i]
		switch {
		case b >= 'A' && b <= 'Z',
			b >= 'a' && b <= 'z',
			b >= '0' && b <= '9',
			b == '+',
			b == '/',
			b == '=':
			clean = append(clean, b)
		}
	}
	return base64.StdEncoding.DecodeString(string(clean))
}

// Deprecated: use TTS.
type AsyncAITTS = TTS

// Deprecated: use TTSOption.
type AsyncAITTSOption = TTSOption

// Deprecated: use NewTTS.
func NewAsyncAITTS(apiKey string, voice string, opts ...TTSOption) *TTS {
	return NewTTS(apiKey, voice, opts...)
}
