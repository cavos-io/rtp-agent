package deepgram

import (
	"bytes"
	"context"
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
	"unicode"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/library/tokenize"
	"github.com/gorilla/websocket"
)

const defaultDeepgramTTSBaseURL = "https://api.deepgram.com/v1/speak"
const deepgramTTSCloseAckTimeout = time.Second

type DeepgramTTS struct {
	apiKey     string
	baseURL    string
	model      string
	encoding   string
	sampleRate int
	mipOptOut  bool
	mu         sync.Mutex
	streams    map[*deepgramTTSStream]struct{}
}

type DeepgramTTSOption func(*DeepgramTTS)

func WithDeepgramTTSBaseURL(baseURL string) DeepgramTTSOption {
	return func(t *DeepgramTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithDeepgramTTSMipOptOut(mipOptOut bool) DeepgramTTSOption {
	return func(t *DeepgramTTS) {
		t.mipOptOut = mipOptOut
	}
}

func WithDeepgramTTSAudioFormat(encoding string, sampleRate int) DeepgramTTSOption {
	return func(t *DeepgramTTS) {
		if encoding != "" {
			t.encoding = encoding
		}
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func NewDeepgramTTS(apiKey string, model string, opts ...DeepgramTTSOption) *DeepgramTTS {
	if apiKey == "" {
		apiKey = os.Getenv("DEEPGRAM_API_KEY")
	}
	if model == "" {
		model = "aura-2-andromeda-en"
	}
	provider := &DeepgramTTS{
		apiKey:     apiKey,
		baseURL:    defaultDeepgramTTSBaseURL,
		model:      model,
		encoding:   "linear16",
		sampleRate: 24000,
		streams:    make(map[*deepgramTTSStream]struct{}),
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (t *DeepgramTTS) Label() string { return "deepgram.TTS" }
func (t *DeepgramTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *DeepgramTTS) SampleRate() int  { return t.sampleRate }
func (t *DeepgramTTS) NumChannels() int { return 1 }
func (t *DeepgramTTS) Model() string    { return t.model }
func (t *DeepgramTTS) Provider() string { return "Deepgram" }

func (t *DeepgramTTS) UpdateOptions(model string) {
	if model != "" {
		t.model = model
	}
}

func (t *DeepgramTTS) Close() error {
	t.mu.Lock()
	streams := make([]*deepgramTTSStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.streams = make(map[*deepgramTTSStream]struct{})
	t.mu.Unlock()

	var closeErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (t *DeepgramTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if err := validateDeepgramTTSAPIKey(t.apiKey); err != nil {
		return nil, err
	}
	u, jsonBody := buildDeepgramTTSSynthesizeRequest(t, text)

	req, err := http.NewRequestWithContext(ctx, "POST", u, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Token "+t.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, llm.NewAPITimeoutError(err.Error())
		}
		return nil, llm.NewAPIConnectionError(err.Error())
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, llm.NewAPIStatusError("Deepgram TTS request failed", resp.StatusCode, "", string(respBody))
	}

	return &deepgramTTSChunkedStream{
		resp:       resp,
		sampleRate: t.sampleRate,
	}, nil
}

func buildDeepgramTTSSynthesizeRequest(t *DeepgramTTS, text string) (string, []byte) {
	u := deepgramTTSBaseURL(t, false)
	q := u.Query()
	q.Set("model", t.model)
	q.Set("encoding", t.encoding)
	q.Set("sample_rate", fmt.Sprintf("%d", t.sampleRate))
	q.Set("container", "none")
	q.Set("mip_opt_out", fmt.Sprintf("%t", t.mipOptOut))
	u.RawQuery = q.Encode()
	body := map[string]interface{}{"text": text}
	jsonBody, _ := json.Marshal(body)
	return u.String(), jsonBody
}

func (t *DeepgramTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if err := validateDeepgramTTSAPIKey(t.apiKey); err != nil {
		return nil, err
	}
	header := make(map[string][]string)
	header["Authorization"] = []string{"Token " + t.apiKey}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildDeepgramTTSStreamURL(t), header)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, llm.NewAPITimeoutError(err.Error())
		}
		return nil, llm.NewAPIConnectionError(err.Error())
	}

	stream := &deepgramTTSStream{
		provider:   t,
		conn:       conn,
		audio:      make(chan *tts.SynthesizedAudio, 10),
		errCh:      make(chan error, 1),
		flushed:    make(chan struct{}, 1),
		sampleRate: t.sampleRate,
	}
	stream.writeJSON = stream.writeJSONMessage
	stream.closeConn = stream.closeWebsocketConn
	t.registerStream(stream)

	go stream.readLoop()

	return stream, nil
}

func (t *DeepgramTTS) registerStream(stream *deepgramTTSStream) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.streams == nil {
		t.streams = make(map[*deepgramTTSStream]struct{})
	}
	t.streams[stream] = struct{}{}
}

func (t *DeepgramTTS) unregisterStream(stream *deepgramTTSStream) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.streams, stream)
}

func validateDeepgramTTSAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("deepgram API key required. Set DEEPGRAM_API_KEY or provide api_key")
	}
	return nil
}

func buildDeepgramTTSStreamURL(t *DeepgramTTS) string {
	u := deepgramTTSBaseURL(t, true)
	q := u.Query()
	q.Set("model", t.model)
	q.Set("encoding", t.encoding)
	q.Set("sample_rate", fmt.Sprintf("%d", t.sampleRate))
	q.Set("mip_opt_out", fmt.Sprintf("%t", t.mipOptOut))
	u.RawQuery = q.Encode()
	return u.String()
}

func deepgramTTSBaseURL(t *DeepgramTTS, websocketURL bool) url.URL {
	baseURL := t.baseURL
	if websocketURL && strings.HasPrefix(baseURL, "http") {
		baseURL = strings.Replace(baseURL, "http", "ws", 1)
	} else if !websocketURL && strings.HasPrefix(baseURL, "ws") {
		baseURL = strings.Replace(baseURL, "ws", "http", 1)
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		if websocketURL {
			return url.URL{Scheme: "wss", Host: "api.deepgram.com", Path: "/v1/speak"}
		}
		return url.URL{Scheme: "https", Host: "api.deepgram.com", Path: "/v1/speak"}
	}
	return *parsed
}

type deepgramTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
	mu         sync.Mutex
}

func (s *deepgramTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resp == nil || s.resp.Body == nil {
		return nil, io.EOF
	}
	buf := make([]byte, 4096)
	n, err := s.resp.Body.Read(buf)
	if err != nil {
		if err == io.EOF && n == 0 {
			return nil, io.EOF
		}
		if err != io.EOF && errors.Is(err, context.DeadlineExceeded) {
			return nil, llm.NewAPITimeoutError(err.Error())
		}
		if err != io.EOF {
			return nil, llm.NewAPIConnectionError(err.Error())
		}
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

func (s *deepgramTTSChunkedStream) Close() error {
	s.mu.Lock()
	if s.resp == nil || s.resp.Body == nil {
		s.mu.Unlock()
		return nil
	}
	body := s.resp.Body
	s.resp = nil
	s.mu.Unlock()
	return body.Close()
}

type deepgramTTSStream struct {
	provider *DeepgramTTS
	conn     *websocket.Conn
	audio    chan *tts.SynthesizedAudio
	errCh    chan error
	mu       sync.Mutex
	closed   bool

	sampleRate  int
	writeJSON   func(any) error
	closeConn   func() error
	flushed     chan struct{}
	pendingText string
}

func (s *deepgramTTSStream) readLoop() {
	defer close(s.audio)
	for {
		msgType, message, err := s.conn.ReadMessage()
		if err != nil {
			if !s.isClosed() {
				s.errCh <- deepgramTTSUnexpectedCloseError(err)
			}
			return
		}

		if msgType == websocket.BinaryMessage {
			s.audio <- &tts.SynthesizedAudio{
				Frame: &model.AudioFrame{
					Data:              message,
					SampleRate:        uint32(s.sampleRate),
					NumChannels:       1,
					SamplesPerChannel: uint32(len(message) / 2),
				},
			}
		} else {
			if err := s.handleTextMessage(message); err != nil {
				s.errCh <- err
				return
			}
		}
	}
}

func deepgramTTSUnexpectedCloseError(err error) error {
	statusCode := -1
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) && closeErr.Code != 0 {
		statusCode = closeErr.Code
	}
	return llm.NewAPIStatusError("Deepgram websocket connection closed unexpectedly", statusCode, "", err.Error())
}

func (s *deepgramTTSStream) handleTextMessage(message []byte) error {
	var metadata map[string]interface{}
	if err := json.Unmarshal(message, &metadata); err != nil {
		return nil
	}
	switch metadata["type"] {
	case "Flushed":
		s.audio <- &tts.SynthesizedAudio{IsFinal: true}
		s.signalFlushed()
	case "Error", "error":
		return llm.NewAPIError("Deepgram TTS returned error", metadata, true)
	}
	return nil
}

func (s *deepgramTTSStream) signalFlushed() {
	if s.flushed == nil {
		return
	}
	select {
	case s.flushed <- struct{}{}:
	default:
	}
}

func (s *deepgramTTSStream) PushText(text string) error {
	if text == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	s.pendingText += text
	return s.sendCompletedWordsLocked()
}

func deepgramTTSSpeakText(text string) string {
	if text == "" || strings.HasSuffix(text, " ") || strings.HasSuffix(text, "\n") || strings.HasSuffix(text, "\t") || strings.HasSuffix(text, "\r") {
		return text
	}
	return text + " "
}

func (s *deepgramTTSStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if err := s.sendPendingWordsLocked(); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	msg := map[string]interface{}{
		"type": "Flush",
	}
	if err := s.writeJSONData(msg); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

func (s *deepgramTTSStream) sendCompletedWordsLocked() error {
	tokens := tokenize.SplitWords(s.pendingText, false, false, false)
	if len(tokens) <= 1 {
		return nil
	}
	for _, token := range tokens[:len(tokens)-1] {
		if err := s.sendSpeakLocked(token.Token); err != nil {
			s.closeAfterWriteFailureLocked()
			return err
		}
		s.consumePendingToken(token.Token)
	}
	return nil
}

func (s *deepgramTTSStream) sendPendingWordsLocked() error {
	tokens := tokenize.SplitWords(s.pendingText, false, false, false)
	for _, token := range tokens {
		if err := s.sendSpeakLocked(token.Token); err != nil {
			return err
		}
	}
	s.pendingText = ""
	return nil
}

func (s *deepgramTTSStream) sendSpeakLocked(text string) error {
	msg := map[string]interface{}{
		"type": "Speak",
		"text": deepgramTTSSpeakText(text),
	}
	return s.writeJSONData(msg)
}

func (s *deepgramTTSStream) consumePendingToken(token string) {
	idx := strings.Index(s.pendingText, token)
	if idx < 0 {
		return
	}
	s.pendingText = strings.TrimLeftFunc(s.pendingText[idx+len(token):], unicode.IsSpace)
}

func (s *deepgramTTSStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	flushErr := s.writeJSONData(map[string]interface{}{"type": "Flush"})
	closeErr := s.writeJSONData(map[string]interface{}{"type": "Close"})
	if flushErr == nil && closeErr == nil {
		s.waitForFlushedAckLocked()
	}
	if s.provider != nil {
		s.provider.unregisterStream(s)
	}
	if err := s.closeConnection(); err != nil {
		return err
	}
	return nil
}

func (s *deepgramTTSStream) waitForFlushedAckLocked() {
	if s.flushed == nil {
		return
	}
	select {
	case <-s.flushed:
	case <-time.After(deepgramTTSCloseAckTimeout):
	}
}

func (s *deepgramTTSStream) writeJSONData(v any) error {
	if s.writeJSON != nil {
		return s.writeJSON(v)
	}
	return s.writeJSONMessage(v)
}

func (s *deepgramTTSStream) writeJSONMessage(v any) error {
	return s.conn.WriteJSON(v)
}

func (s *deepgramTTSStream) closeConnection() error {
	if s.closeConn != nil {
		return s.closeConn()
	}
	return s.closeWebsocketConn()
}

func (s *deepgramTTSStream) closeWebsocketConn() error {
	return s.conn.Close()
}

func (s *deepgramTTSStream) closeAfterWriteFailureLocked() {
	if s.closed {
		return
	}
	s.closed = true
	_ = s.closeConnection()
}

func (s *deepgramTTSStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *deepgramTTSStream) Next() (*tts.SynthesizedAudio, error) {
	select {
	case audio, ok := <-s.audio:
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
	}
}
