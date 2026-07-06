package telnyx

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/codecs"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

const (
	defaultTelnyxTTSBaseURL    = "wss://api.telnyx.com/v2/text-to-speech/speech"
	defaultTelnyxTTSVoice      = "Telnyx.NaturalHD.astra"
	defaultTelnyxTTSSampleRate = 16000
	telnyxTTSNumChannels       = 1
)

type TelnyxTTS struct {
	mu         sync.Mutex
	streams    map[*telnyxTTSStream]struct{}
	apiKey     string
	baseURL    string
	voice      string
	sampleRate int
	closed     bool
}

type TelnyxTTSOption func(*TelnyxTTS)

func WithTelnyxTTSBaseURL(baseURL string) TelnyxTTSOption {
	return func(t *TelnyxTTS) {
		if baseURL != "" {
			t.baseURL = baseURL
		}
	}
}

func NewTelnyxTTS(apiKey string, voice string, opts ...TelnyxTTSOption) *TelnyxTTS {
	if apiKey == "" {
		apiKey = os.Getenv(telnyxAPIKeyEnv)
	}
	if voice == "" {
		voice = defaultTelnyxTTSVoice
	}
	provider := &TelnyxTTS{
		streams:    make(map[*telnyxTTSStream]struct{}),
		apiKey:     apiKey,
		baseURL:    defaultTelnyxTTSBaseURL,
		voice:      voice,
		sampleRate: defaultTelnyxTTSSampleRate,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (t *TelnyxTTS) Label() string { return "telnyx.TTS" }
func (t *TelnyxTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *TelnyxTTS) SampleRate() int  { return t.sampleRate }
func (t *TelnyxTTS) NumChannels() int { return telnyxTTSNumChannels }
func (t *TelnyxTTS) Model() string    { return t.voice }
func (t *TelnyxTTS) Provider() string { return "telnyx" }

func (t *TelnyxTTS) Close() error {
	t.mu.Lock()
	t.closed = true
	streams := make([]*telnyxTTSStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.streams = make(map[*telnyxTTSStream]struct{})
	t.mu.Unlock()

	var closeErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (t *TelnyxTTS) isClosed() bool {
	if t == nil {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

func (t *TelnyxTTS) registerStream(stream *telnyxTTSStream) bool {
	if t == nil || stream == nil {
		return false
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		_ = stream.Close()
		return false
	}
	if t.streams == nil {
		t.streams = make(map[*telnyxTTSStream]struct{})
	}
	t.streams[stream] = struct{}{}
	stream.owner = t
	t.mu.Unlock()
	return true
}

func (t *TelnyxTTS) unregisterStream(stream *telnyxTTSStream) {
	if t == nil || stream == nil {
		return
	}
	t.mu.Lock()
	delete(t.streams, stream)
	t.mu.Unlock()
}

func (t *TelnyxTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if err := validateTelnyxAPIKey(t.apiKey); err != nil {
		return nil, err
	}
	return &telnyxTTSChunkedStream{provider: t, ctx: ctx, text: text}, nil
}

func (t *TelnyxTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if err := validateTelnyxAPIKey(t.apiKey); err != nil {
		return nil, err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildTelnyxTTSStreamURL(t), buildTelnyxTTSHeaders(t))
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("failed to dial telnyx tts websocket: %v", err))
	}
	if t.isClosed() {
		_ = conn.Close()
		return nil, io.ErrClosedPipe
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &telnyxTTSStream{
		conn:       conn,
		ctx:        streamCtx,
		cancel:     cancel,
		sampleRate: t.sampleRate,
		events:     make(chan *tts.SynthesizedAudio, 100),
		errCh:      make(chan error, 1),
	}
	stream.startDecoder()
	if err := writeTelnyxTTSMessage(conn, buildTelnyxTTSTextMessage(" ")); err != nil {
		conn.Close()
		cancel()
		return nil, err
	}
	if t.isClosed() {
		conn.Close()
		cancel()
		return nil, io.ErrClosedPipe
	}
	stream.writeMessage = stream.writeTTSMessage
	stream.closeConn = stream.closeWebsocketConn
	if !t.registerStream(stream) {
		return nil, io.ErrClosedPipe
	}
	go stream.readLoop()
	return stream, nil
}

func buildTelnyxTTSStreamURL(t *TelnyxTTS) string {
	u, err := url.Parse(t.baseURL)
	if err != nil {
		return t.baseURL
	}
	q := u.Query()
	q.Set("voice", t.voice)
	u.RawQuery = q.Encode()
	return u.String()
}

func buildTelnyxTTSHeaders(t *TelnyxTTS) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+t.apiKey)
	return headers
}

func buildTelnyxTTSTextMessage(text string) map[string]string {
	return map[string]string{"text": text}
}

func writeTelnyxTTSMessage(conn *websocket.Conn, message map[string]string) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, payload)
}

type telnyxTTSChunkedStream struct {
	provider *TelnyxTTS
	ctx      context.Context
	text     string
	stream   tts.SynthesizeStream
	closed   bool
	started  bool
}

func (s *telnyxTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.closed {
		return nil, io.EOF
	}
	if err := s.ensureStream(); err != nil {
		return nil, err
	}
	if s.stream == nil {
		return nil, io.EOF
	}
	return s.stream.Next()
}

func (s *telnyxTTSChunkedStream) ensureStream() error {
	if s.started {
		return nil
	}
	s.started = true
	stream, err := s.provider.Stream(s.ctx)
	if err != nil {
		s.closed = true
		return err
	}
	if err := stream.PushText(s.text); err != nil {
		_ = stream.Close()
		s.closed = true
		return err
	}
	if err := stream.Flush(); err != nil {
		_ = stream.Close()
		s.closed = true
		return err
	}
	s.stream = stream
	return nil
}

func (s *telnyxTTSChunkedStream) Close() error {
	s.closed = true
	if s.stream == nil {
		return nil
	}
	return s.stream.Close()
}

type telnyxTTSStream struct {
	owner       *TelnyxTTS
	conn        *websocket.Conn
	ctx         context.Context
	cancel      context.CancelFunc
	sampleRate  int
	events      chan *tts.SynthesizedAudio
	errCh       chan error
	decoder     codecs.AudioStreamDecoder
	mu          sync.Mutex
	closed      bool
	inputEnded  bool
	pendingText string

	writeMessage func(map[string]string) error
	closeConn    func() error
}

func (s *telnyxTTSStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.inputEnded {
		return nil
	}
	s.pendingText += text
	return nil
}

func (s *telnyxTTSStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.inputEnded {
		return nil
	}
	return s.flushPendingTextLocked()
}

func (s *telnyxTTSStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.inputEnded {
		return nil
	}
	if err := s.flushPendingTextLocked(); err != nil {
		return err
	}
	s.inputEnded = true
	return nil
}

func (s *telnyxTTSStream) flushPendingTextLocked() error {
	if s.pendingText == "" {
		return nil
	}
	text := s.pendingText
	s.pendingText = ""
	if err := s.writeMessageData(buildTelnyxTTSTextMessage(text)); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	if err := s.writeMessageData(buildTelnyxTTSTextMessage("")); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

func (s *telnyxTTSStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.inputEnded = true
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	if s.decoder != nil {
		_ = s.decoder.Close()
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

func (s *telnyxTTSStream) writeMessageData(message map[string]string) error {
	if s.writeMessage != nil {
		return s.writeMessage(message)
	}
	return s.writeTTSMessage(message)
}

func (s *telnyxTTSStream) writeTTSMessage(message map[string]string) error {
	return writeTelnyxTTSMessage(s.conn, message)
}

func (s *telnyxTTSStream) closeConnection() error {
	if s.closeConn != nil {
		return s.closeConn()
	}
	return s.closeWebsocketConn()
}

func (s *telnyxTTSStream) closeWebsocketConn() error {
	if s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

func (s *telnyxTTSStream) closeAfterWriteFailureLocked() {
	if s.closed {
		return
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	if s.decoder != nil {
		_ = s.decoder.Close()
	}
	_ = s.closeConnection()
	if s.owner != nil {
		s.owner.unregisterStream(s)
	}
}

func (s *telnyxTTSStream) Next() (*tts.SynthesizedAudio, error) {
	if s.isClosed() {
		return nil, io.EOF
	}
	select {
	case audio, ok := <-s.events:
		if ok {
			return audio, nil
		}
		select {
		case err := <-s.errCh:
			return nil, err
		default:
			return nil, io.EOF
		}
	default:
	}
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
		if s.isClosed() {
			return nil, io.EOF
		}
		return nil, s.ctx.Err()
	}
}

func (s *telnyxTTSStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *telnyxTTSStream) readLoop() {
	defer s.endAudioInput()
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) && err != io.EOF {
				s.errCh <- llm.NewAPIConnectionError(fmt.Sprintf("Telnyx TTS WebSocket closed unexpectedly: %v", err))
			}
			return
		}
		if msgType != websocket.TextMessage {
			continue
		}
		audio, done, err := telnyxTTSAudioBytesFromMessage(payload)
		if err != nil {
			s.errCh <- err
			return
		}
		if len(audio) > 0 {
			s.pushAudioData(audio)
		}
		if done {
			return
		}
	}
}

func (s *telnyxTTSStream) startDecoder() {
	if s.decoder != nil {
		return
	}
	s.decoder = codecs.NewMP3AudioStreamDecoder()
	go s.decodeLoop()
}

func (s *telnyxTTSStream) pushAudioData(audio []byte) {
	s.startDecoder()
	s.decoder.Push(audio)
}

func (s *telnyxTTSStream) endAudioInput() {
	if s.decoder != nil {
		s.decoder.EndInput()
	}
}

func (s *telnyxTTSStream) decodeLoop() {
	defer close(s.events)
	for {
		frame, err := s.decoder.Next()
		if err != nil {
			if strings.Contains(err.Error(), "decoder closed") {
				s.events <- &tts.SynthesizedAudio{IsFinal: true}
				return
			}
			s.errCh <- err
			return
		}
		s.events <- &tts.SynthesizedAudio{Frame: frame}
	}
}

func telnyxTTSAudioBytesFromMessage(payload []byte) ([]byte, bool, error) {
	var message struct {
		Audio string `json:"audio"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, nil
	}
	if message.Audio == "" {
		return nil, false, nil
	}
	data, err := telnyxDecodeBase64Audio(message.Audio)
	if err != nil {
		return nil, false, llm.NewAPIConnectionError(fmt.Sprintf("Telnyx TTS audio decode failed: %v", err))
	}
	return data, false, nil
}

func telnyxTTSAudioFromMessage(payload []byte, sampleRate int) (*tts.SynthesizedAudio, bool, error) {
	var message struct {
		Audio string `json:"audio"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, err
	}
	if message.Audio == "" {
		return nil, false, nil
	}
	data, err := telnyxDecodeBase64Audio(message.Audio)
	if err != nil {
		return nil, false, err
	}
	if len(data) == 0 {
		return nil, false, nil
	}
	return telnyxTTSAudioFrame(data, sampleRate), false, nil
}

func telnyxDecodeBase64Audio(data string) ([]byte, error) {
	clean := make([]byte, 0, len(data))
	dataChars := 0
	for i := 0; i < len(data); i++ {
		b := data[i]
		switch {
		case b >= 'A' && b <= 'Z',
			b >= 'a' && b <= 'z',
			b >= '0' && b <= '9',
			b == '+',
			b == '/':
			clean = append(clean, b)
			dataChars++
		case b == '=':
			clean = append(clean, b)
		}
	}
	if dataChars == 0 {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(string(clean))
}

func telnyxTTSAudioFrame(audio []byte, sampleRate int) *tts.SynthesizedAudio {
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              bytes.Clone(audio),
			SampleRate:        uint32(sampleRate),
			NumChannels:       telnyxTTSNumChannels,
			SamplesPerChannel: uint32(len(audio) / 2),
		},
	}
}
