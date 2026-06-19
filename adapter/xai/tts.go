package xai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/library/tokenize"
	"github.com/gorilla/websocket"
)

const (
	defaultXaiTTSWebsocketURL = "wss://api.x.ai/v1/tts"
	defaultXaiTTSVoice        = "ara"
	defaultXaiTTSLanguage     = "auto"
	xaiTTSSampleRate          = 24000
	xaiTTSNumChannels         = 1
)

type XaiTTS struct {
	apiKey       string
	websocketURL string
	voice        string
	language     string
}

type XaiTTSOption func(*XaiTTS)

func WithXaiTTSWebsocketURL(websocketURL string) XaiTTSOption {
	return func(t *XaiTTS) {
		if websocketURL != "" {
			t.websocketURL = websocketURL
		}
	}
}

func WithXaiTTSVoice(voice string) XaiTTSOption {
	return func(t *XaiTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithXaiTTSLanguage(language string) XaiTTSOption {
	return func(t *XaiTTS) {
		if language != "" {
			t.language = language
		}
	}
}

func NewXaiTTS(apiKey string, voice string, opts ...XaiTTSOption) *XaiTTS {
	if apiKey == "" {
		apiKey = os.Getenv(xaiAPIKeyEnv)
	}
	provider := &XaiTTS{
		apiKey:       apiKey,
		websocketURL: defaultXaiTTSWebsocketURL,
		voice:        voice,
		language:     defaultXaiTTSLanguage,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultXaiTTSVoice
	}
	return provider
}

func (t *XaiTTS) Label() string { return "xai.TTS" }
func (t *XaiTTS) Model() string { return "unknown" }
func (t *XaiTTS) Provider() string {
	return "xAI"
}

func (t *XaiTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *XaiTTS) SampleRate() int  { return xaiTTSSampleRate }
func (t *XaiTTS) NumChannels() int { return xaiTTSNumChannels }

func (t *XaiTTS) UpdateOptions(opts ...XaiTTSOption) {
	for _, opt := range opts {
		opt(t)
	}
}

func (t *XaiTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if err := validateXaiAPIKey(t.apiKey); err != nil {
		return nil, err
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildXaiTTSStreamURL(t), buildXaiTTSHeaders(t))
	if err != nil {
		return nil, llm.NewAPIConnectionError("failed to connect to xAI")
	}
	if err := writeXaiTTSTokenizedText(func(message map[string]any) error {
		return writeXaiTTSMessage(conn, message)
	}, text, t.language); err != nil {
		conn.Close()
		return nil, err
	}
	return &xaiTTSWebsocketChunkedStream{conn: conn}, nil
}

func (t *XaiTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if err := validateXaiAPIKey(t.apiKey); err != nil {
		return nil, err
	}

	streamCtx, cancel := context.WithCancel(ctx)
	stream := &xaiTTSSynthesizeStream{
		streamURL:     buildXaiTTSStreamURL(t),
		headers:       buildXaiTTSHeaders(t),
		ctx:           streamCtx,
		cancel:        cancel,
		tokenLanguage: t.language,
	}
	stream.writeMessage = stream.writeTTSMessage
	stream.closeConn = stream.closeWebsocketConn
	return stream, nil
}

func validateXaiAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("xAI API key is required, either as argument or set XAI_API_KEY environment variable")
	}
	return nil
}

func buildXaiTTSStreamURL(t *XaiTTS) string {
	u, _ := url.Parse(t.websocketURL)
	q := u.Query()
	q.Set("voice", t.voice)
	q.Set("language", t.language)
	q.Set("codec", "pcm")
	q.Set("sample_rate", strconv.Itoa(xaiTTSSampleRate))
	u.RawQuery = q.Encode()
	return u.String()
}

func buildXaiTTSHeaders(t *XaiTTS) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+t.apiKey)
	return headers
}

func buildXaiTTSTextDeltaMessage(text string) map[string]any {
	return map[string]any{"type": "text.delta", "delta": text}
}

func buildXaiTTSTextDoneMessage() map[string]any {
	return map[string]any{"type": "text.done"}
}

func writeXaiTTSMessage(conn *websocket.Conn, message map[string]any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, payload)
}

func writeXaiTTSTokenizedText(write func(map[string]any) error, text string, language string) error {
	for _, token := range xaiTTSTokenizeWords(text, language) {
		if err := write(buildXaiTTSTextDeltaMessage(token)); err != nil {
			return err
		}
	}
	return write(buildXaiTTSTextDoneMessage())
}

type xaiTTSWebsocketChunkedStream struct {
	conn *websocket.Conn
}

func (s *xaiTTSWebsocketChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			return nil, xaiTTSUnexpectedCloseError(err)
		}
		if msgType != websocket.TextMessage {
			continue
		}
		audio, done, err := xaiTTSAudioFromMessage(payload)
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

func (s *xaiTTSWebsocketChunkedStream) Close() error {
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

type xaiTTSSynthesizeStream struct {
	conn          *websocket.Conn
	streamURL     string
	headers       http.Header
	ctx           context.Context
	cancel        context.CancelFunc
	mu            sync.Mutex
	closed        bool
	writeMessage  func(map[string]any) error
	closeConn     func() error
	tokenBuffer   string
	tokenLanguage string
	inputEnded    bool
}

func (s *xaiTTSSynthesizeStream) PushText(text string) error {
	if text == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("xai tts stream is closed")
	}
	for _, token := range s.pushTextTokensLocked(text) {
		if err := s.writeMessageData(buildXaiTTSTextDeltaMessage(token)); err != nil {
			s.closeAfterWriteFailureLocked()
			return err
		}
	}
	return nil
}

func (s *xaiTTSSynthesizeStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("xai tts stream is closed")
	}
	tokens := s.flushTextTokensLocked()
	if len(tokens) == 0 && s.conn == nil {
		return nil
	}
	for _, token := range tokens {
		if err := s.writeMessageData(buildXaiTTSTextDeltaMessage(token)); err != nil {
			s.closeAfterWriteFailureLocked()
			return err
		}
	}
	if err := s.writeMessageData(buildXaiTTSTextDoneMessage()); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	s.inputEnded = true
	return nil
}

func (s *xaiTTSSynthesizeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	if s.conn != nil {
		_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	}
	return s.closeConnection()
}

func (s *xaiTTSSynthesizeStream) writeMessageData(message map[string]any) error {
	if s.writeMessage != nil {
		return s.writeMessage(message)
	}
	return s.writeTTSMessage(message)
}

func (s *xaiTTSSynthesizeStream) writeTTSMessage(message map[string]any) error {
	if err := s.ensureConnLocked(); err != nil {
		return err
	}
	return writeXaiTTSMessage(s.conn, message)
}

func (s *xaiTTSSynthesizeStream) closeConnection() error {
	if s.closeConn != nil {
		return s.closeConn()
	}
	return s.closeWebsocketConn()
}

func (s *xaiTTSSynthesizeStream) closeWebsocketConn() error {
	if s.conn == nil {
		return nil
	}
	conn := s.conn
	s.conn = nil
	return conn.Close()
}

func (s *xaiTTSSynthesizeStream) ensureConnLocked() error {
	if s.conn != nil {
		return nil
	}
	conn, _, err := websocket.DefaultDialer.DialContext(s.ctx, s.streamURL, s.headers)
	if err != nil {
		return llm.NewAPIConnectionError("failed to connect to xAI")
	}
	s.conn = conn
	s.inputEnded = false
	return nil
}

func (s *xaiTTSSynthesizeStream) closeAfterWriteFailureLocked() {
	if s.closed {
		return
	}
	s.closed = true
	s.tokenBuffer = ""
	s.cancel()
	_ = s.closeConnection()
}

func (s *xaiTTSSynthesizeStream) pushTextTokensLocked(text string) []string {
	s.tokenBuffer += text
	if len(s.tokenBuffer) == 0 {
		return nil
	}
	var ready []string
	for {
		tokens := xaiTTSTokenizeWords(s.tokenBuffer, s.tokenLanguage)
		if len(tokens) <= 1 {
			return ready
		}
		token := tokens[0]
		ready = append(ready, token)
		tokenIdx := strings.Index(s.tokenBuffer, token)
		if tokenIdx < 0 {
			s.tokenBuffer = ""
			return ready
		}
		s.tokenBuffer = strings.TrimLeftFunc(s.tokenBuffer[tokenIdx+len(token):], unicode.IsSpace)
	}
}

func (s *xaiTTSSynthesizeStream) flushTextTokensLocked() []string {
	if s.tokenBuffer == "" {
		return nil
	}
	tokens := xaiTTSTokenizeWords(s.tokenBuffer, s.tokenLanguage)
	s.tokenBuffer = ""
	return tokens
}

func (s *xaiTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
	select {
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	default:
	}
	for {
		conn := s.currentConn()
		if conn == nil {
			return nil, io.EOF
		}
		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			s.clearCurrentConn(conn)
			return nil, xaiTTSUnexpectedCloseError(err)
		}
		if msgType != websocket.TextMessage {
			continue
		}
		audio, done, err := xaiTTSAudioFromMessage(payload)
		if err != nil {
			return nil, err
		}
		if done {
			if !s.realtimeInputEnded() {
				continue
			}
			s.clearCurrentConn(conn)
			return nil, io.EOF
		}
		if audio != nil {
			return audio, nil
		}
	}
}

func (s *xaiTTSSynthesizeStream) currentConn() *websocket.Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn
}

func (s *xaiTTSSynthesizeStream) realtimeInputEnded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inputEnded
}

func (s *xaiTTSSynthesizeStream) clearCurrentConn(conn *websocket.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != conn {
		return
	}
	_ = s.conn.Close()
	s.conn = nil
}

func xaiTTSUnexpectedCloseError(err error) error {
	message := "xAI connection closed unexpectedly"
	if err != nil && err != io.EOF {
		message = fmt.Sprintf("%s: %v", message, err)
	}
	return llm.NewAPIStatusError(message, -1, "", nil)
}

func xaiTTSAudioFromMessage(payload []byte) (*tts.SynthesizedAudio, bool, error) {
	var message struct {
		Type    string `json:"type"`
		Delta   string `json:"delta"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, llm.NewAPIConnectionError(err.Error())
	}
	switch message.Type {
	case "audio.delta":
		audio, err := base64.StdEncoding.DecodeString(message.Delta)
		if err != nil {
			return nil, false, llm.NewAPIConnectionError(err.Error())
		}
		return xaiTTSAudioFrame(audio), false, nil
	case "audio.done":
		return nil, true, nil
	case "error":
		if message.Message == "" {
			message.Message = "unknown xai tts error"
		}
		return nil, false, llm.NewAPIStatusError(message.Message, -1, "", string(payload))
	default:
		return nil, false, nil
	}
}

func xaiTTSAudioFrame(audio []byte) *tts.SynthesizedAudio {
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              bytes.Clone(audio),
			SampleRate:        xaiTTSSampleRate,
			NumChannels:       xaiTTSNumChannels,
			SamplesPerChannel: uint32(len(audio) / 2),
		},
	}
}

func xaiTTSTokenizeWords(text string, language string) []string {
	parts := tokenize.SplitWords(text, false, false, false)
	tokens := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Token != "" {
			tokens = append(tokens, part.Token)
		}
	}
	return tokens
}
