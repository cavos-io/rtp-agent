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
	"unicode/utf8"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/library/tokenize"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const defaultDeepgramTTSBaseURL = "https://api.deepgram.com/v1/speak"
const defaultDeepgramTTSStreamResponseTimeout = 10 * time.Second
const deepgramTTSCloseAckTimeout = time.Second
const deepgramTTSRequestTimeout = 30 * time.Second
const deepgramTTSPoolMaxSessionDuration = time.Hour
const deepgramTTSFlushMessage = `{"type": "Flush"}`
const deepgramTTSCloseMessage = `{"type": "Close"}`

var errDeepgramTTSReleasedToPool = errors.New("deepgram tts stream released to pool")

type DeepgramTTS struct {
	apiKey                string
	baseURL               string
	model                 string
	encoding              string
	sampleRate            int
	mipOptOut             bool
	streamResponseTimeout time.Duration
	mu                    sync.Mutex
	streams               map[*deepgramTTSStream]struct{}
	closed                bool
	prewarmConn           *websocket.Conn
	prewarmConnectedAt    time.Time
	prewarming            bool
	prewarmCancel         context.CancelFunc
}

type DeepgramTTSOption func(*DeepgramTTS)

func WithDeepgramTTSBaseURL(baseURL string) DeepgramTTSOption {
	return func(t *DeepgramTTS) {
		t.baseURL = strings.TrimRight(baseURL, "/")
	}
}

func WithDeepgramTTSMipOptOut(mipOptOut bool) DeepgramTTSOption {
	return func(t *DeepgramTTS) {
		t.mipOptOut = mipOptOut
	}
}

func WithDeepgramTTSAudioFormat(encoding string, sampleRate int) DeepgramTTSOption {
	return func(t *DeepgramTTS) {
		t.encoding = deepgramTTSNormalizeEncoding(encoding)
		t.sampleRate = sampleRate
	}
}

func WithDeepgramTTSStreamResponseTimeout(timeout time.Duration) DeepgramTTSOption {
	return func(t *DeepgramTTS) {
		if timeout > 0 {
			t.streamResponseTimeout = timeout
		}
	}
}

func deepgramTTSNormalizeEncoding(encoding string) string {
	switch strings.ToLower(encoding) {
	case "pcm_s16le", "linear_pcm", "pcm_linear":
		return "linear16"
	case "pcm_mulaw":
		return "mulaw"
	case "pcm_alaw":
		return "alaw"
	default:
		return encoding
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
		apiKey:                apiKey,
		baseURL:               defaultDeepgramTTSBaseURL,
		model:                 model,
		encoding:              "linear16",
		sampleRate:            24000,
		streamResponseTimeout: defaultDeepgramTTSStreamResponseTimeout,
		streams:               make(map[*deepgramTTSStream]struct{}),
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
	t.model = model
}

func (t *DeepgramTTS) Prewarm() {
	if t == nil || validateDeepgramTTSAPIKey(t.apiKey) != nil {
		return
	}
	streamURL := buildDeepgramTTSStreamURL(t)
	apiKey := t.apiKey

	t.mu.Lock()
	if t.closed || t.prewarming || t.prewarmConn != nil {
		t.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.prewarming = true
	t.prewarmCancel = cancel
	t.mu.Unlock()

	go func() {
		defer cancel()
		header := make(http.Header)
		header.Set("Authorization", "Token "+apiKey)
		conn, _, err := websocket.DefaultDialer.DialContext(ctx, streamURL, header)

		t.mu.Lock()
		defer t.mu.Unlock()
		t.prewarming = false
		t.prewarmCancel = nil
		if err != nil || t.closed || t.prewarmConn != nil {
			if conn != nil {
				closeDeepgramTTSPrewarmedConn(conn)
			}
			return
		}
		t.prewarmConn = conn
		t.prewarmConnectedAt = time.Now()
	}()
}

func (t *DeepgramTTS) Close() error {
	t.mu.Lock()
	t.closed = true
	streams := make([]*deepgramTTSStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.streams = make(map[*deepgramTTSStream]struct{})
	prewarmConn := t.prewarmConn
	t.prewarmConn = nil
	t.prewarmConnectedAt = time.Time{}
	prewarmCancel := t.prewarmCancel
	t.prewarmCancel = nil
	t.mu.Unlock()

	if prewarmCancel != nil {
		prewarmCancel()
	}
	var closeErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	if prewarmConn != nil {
		closeDeepgramTTSPrewarmedConn(prewarmConn)
	}
	return closeErr
}

func (t *DeepgramTTS) isClosed() bool {
	if t == nil {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

func (t *DeepgramTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if err := validateDeepgramTTSAPIKey(t.apiKey); err != nil {
		return nil, err
	}
	u, jsonBody := buildDeepgramTTSSynthesizeRequest(t, text)
	return &deepgramTTSChunkedStream{
		ctx:        ctx,
		requestURL: u,
		body:       jsonBody,
		apiKey:     t.apiKey,
		sampleRate: t.sampleRate,
		encoding:   t.encoding,
		requestID:  uuid.NewString(),
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
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if err := validateDeepgramTTSAPIKey(t.apiKey); err != nil {
		return nil, err
	}

	streamCtx, cancel := context.WithCancel(ctx)
	stream := &deepgramTTSStream{
		provider:   t,
		ctx:        streamCtx,
		cancel:     cancel,
		apiKey:     t.apiKey,
		streamURL:  buildDeepgramTTSStreamURL(t),
		audio:      make(chan *tts.SynthesizedAudio, 10),
		errCh:      make(chan error, 1),
		flushed:    make(chan struct{}, 1),
		closeAck:   make(chan struct{}, 1),
		inputSent:  make(chan struct{}),
		done:       make(chan struct{}),
		sampleRate: t.sampleRate,
		encoding:   t.encoding,
		timeout:    t.streamResponseTimeout,
		requestID:  uuid.NewString(),
		segmentID:  uuid.NewString(),
	}
	if !t.registerStream(stream) {
		return nil, io.ErrClosedPipe
	}

	return stream, nil
}

func (t *DeepgramTTS) registerStream(stream *deepgramTTSStream) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	if t.streams == nil {
		t.streams = make(map[*deepgramTTSStream]struct{})
	}
	t.streams[stream] = struct{}{}
	return true
}

func (t *DeepgramTTS) unregisterStream(stream *deepgramTTSStream) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.streams, stream)
}

func (t *DeepgramTTS) takePrewarmedConn() *websocket.Conn {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	conn := t.prewarmConn
	if conn == nil {
		return nil
	}
	connectedAt := t.prewarmConnectedAt
	t.prewarmConn = nil
	t.prewarmConnectedAt = time.Time{}
	if !connectedAt.IsZero() && time.Since(connectedAt) > deepgramTTSPoolMaxSessionDuration {
		closeDeepgramTTSPrewarmedConn(conn)
		return nil
	}
	return conn
}

func (t *DeepgramTTS) storePrewarmedConn(conn *websocket.Conn) bool {
	if t == nil || conn == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.prewarmConn != nil {
		return false
	}
	t.prewarmConn = conn
	t.prewarmConnectedAt = time.Now()
	return true
}

func closeDeepgramTTSPrewarmedConn(conn *websocket.Conn) {
	_ = conn.WriteMessage(websocket.TextMessage, []byte(deepgramTTSFlushMessage))
	_ = conn.WriteMessage(websocket.TextMessage, []byte(deepgramTTSCloseMessage))
	_ = conn.SetReadDeadline(time.Now().Add(deepgramTTSCloseAckTimeout))
	_, _, _ = conn.ReadMessage()
	_ = conn.SetReadDeadline(time.Time{})
	_ = conn.Close()
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

func deepgramTTSStatusMessage(resp *http.Response) string {
	if resp == nil {
		return "Deepgram TTS request failed"
	}
	if message := http.StatusText(resp.StatusCode); message != "" {
		return message
	}
	if resp.Status != "" {
		return resp.Status
	}
	return "Deepgram TTS request failed"
}

type deepgramTTSChunkedStream struct {
	ctx          context.Context
	requestURL   string
	body         []byte
	apiKey       string
	resp         *http.Response
	sampleRate   int
	encoding     string
	requestID    string
	cancel       context.CancelFunc
	started      bool
	pendingFinal bool
	pendingErr   error
	finalSent    bool
	mu           sync.Mutex
	readMu       sync.Mutex
}

func (s *deepgramTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	s.readMu.Lock()
	defer s.readMu.Unlock()

	s.mu.Lock()
	if s.resp == nil || s.resp.Body == nil || s.finalSent {
		if !s.started && !s.finalSent {
			if err := s.startRequestLocked(); err != nil {
				s.mu.Unlock()
				return nil, err
			}
			if s.resp == nil || s.resp.Body == nil || s.finalSent {
				s.mu.Unlock()
				return nil, io.EOF
			}
		} else {
			s.mu.Unlock()
			return nil, io.EOF
		}
	}
	if s.resp == nil || s.resp.Body == nil || s.finalSent {
		s.mu.Unlock()
		return nil, io.EOF
	}
	if s.pendingFinal {
		s.pendingFinal = false
		audio, err := s.emitFinal()
		s.mu.Unlock()
		return audio, err
	}
	if s.pendingErr != nil {
		err := s.pendingErr
		s.pendingErr = nil
		s.finalSent = true
		if s.resp != nil && s.resp.Body != nil {
			body := s.resp.Body
			s.resp = nil
			_ = body.Close()
		}
		s.cancelRequestLocked()
		s.mu.Unlock()
		return nil, err
	}
	body := s.resp.Body
	encoding := s.encoding
	sampleRate := s.sampleRate
	requestID := s.requestID
	s.mu.Unlock()

	buf := make([]byte, 4096)
	n, err := body.Read(buf)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finalSent {
		return nil, io.EOF
	}
	if err != nil {
		if err == io.EOF {
			if n == 0 {
				return s.emitFinal()
			}
			s.pendingFinal = true
		} else if n > 0 {
			s.pendingErr = deepgramTTSChunkedReadError(err)
		} else {
			s.cancelRequestLocked()
			s.finalSent = true
			return nil, deepgramTTSChunkedReadError(err)
		}
	}

	frameData := deepgramTTSTelephonyToPCM(encoding, buf[:n])
	return &tts.SynthesizedAudio{
		RequestID: requestID,
		Frame: &model.AudioFrame{
			Data:              frameData,
			SampleRate:        uint32(sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(len(frameData) / 2),
		},
	}, nil
}

func deepgramTTSChunkedReadError(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return llm.NewAPITimeoutError(err.Error())
	}
	return llm.NewAPIConnectionError(err.Error())
}

func (s *deepgramTTSChunkedStream) startRequestLocked() error {
	s.started = true
	ctx, cancel := context.WithTimeout(s.ctx, deepgramTTSRequestTimeout)
	s.cancel = cancel
	req, err := http.NewRequestWithContext(ctx, "POST", s.requestURL, bytes.NewBuffer(s.body))
	if err != nil {
		s.cancelRequestLocked()
		s.finalSent = true
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Token "+s.apiKey)

	s.mu.Unlock()
	resp, err := http.DefaultClient.Do(req)
	s.mu.Lock()
	if s.finalSent {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		s.cancelRequestLocked()
		return io.EOF
	}
	if err != nil {
		s.cancelRequestLocked()
		s.finalSent = true
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return llm.NewAPITimeoutError(err.Error())
		}
		return llm.NewAPIConnectionError(err.Error())
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == 499 {
			resp.Body.Close()
			s.cancelRequestLocked()
			s.finalSent = true
			return nil
		}
		resp.Body.Close()
		s.cancelRequestLocked()
		s.finalSent = true
		return llm.NewAPIStatusError(deepgramTTSStatusMessage(resp), resp.StatusCode, "", nil)
	}
	s.resp = resp
	return nil
}

func (s *deepgramTTSChunkedStream) emitFinal() (*tts.SynthesizedAudio, error) {
	if s.finalSent {
		return nil, io.EOF
	}
	s.finalSent = true
	if s.resp != nil && s.resp.Body != nil {
		body := s.resp.Body
		s.resp = nil
		_ = body.Close()
	}
	s.cancelRequestLocked()
	return &tts.SynthesizedAudio{RequestID: s.requestID, IsFinal: true}, nil
}

func (s *deepgramTTSChunkedStream) Close() error {
	s.mu.Lock()
	if s.resp == nil || s.resp.Body == nil {
		s.finalSent = true
		s.cancelRequestLocked()
		s.mu.Unlock()
		return nil
	}
	body := s.resp.Body
	s.resp = nil
	s.finalSent = true
	s.cancelRequestLocked()
	s.mu.Unlock()
	return body.Close()
}

func (s *deepgramTTSChunkedStream) cancelRequestLocked() {
	if s.cancel == nil {
		return
	}
	s.cancel()
	s.cancel = nil
}

type deepgramTTSStream struct {
	provider    *DeepgramTTS
	ctx         context.Context
	cancel      context.CancelFunc
	apiKey      string
	streamURL   string
	conn        *websocket.Conn
	audio       chan *tts.SynthesizedAudio
	errCh       chan error
	mu          sync.Mutex
	closed      bool
	closing     bool
	inputClosed bool
	inputEnded  bool
	drainClosed bool
	readDone    bool

	sampleRate    int
	encoding      string
	writeJSON     func(any) error
	writeText     func(string) error
	closeConn     func() error
	flushed       chan struct{}
	closeAck      chan struct{}
	inputSent     chan struct{}
	inputSentOnce sync.Once
	done          chan struct{}
	doneOnce      sync.Once
	timeout       time.Duration
	pendingText   string
	requestID     string
	segmentID     string
	segmentOpen   bool
	segmentSent   bool
	flushPending  bool
}

func (s *deepgramTTSStream) readLoop() {
	defer close(s.audio)
	if !s.waitForInputSent() {
		return
	}
	for {
		if s.timeout > 0 {
			_ = s.conn.SetReadDeadline(time.Now().Add(s.timeout))
		}
		msgType, message, err := s.conn.ReadMessage()
		if err != nil {
			if !s.isClosed() {
				s.markReadDone()
				s.sendError(deepgramTTSReadError(err))
			}
			return
		}

		if msgType == websocket.BinaryMessage {
			frameData := deepgramTTSTelephonyToPCM(s.encoding, message)
			audio := &tts.SynthesizedAudio{
				Frame: &model.AudioFrame{
					Data:              frameData,
					SampleRate:        uint32(s.sampleRate),
					NumChannels:       1,
					SamplesPerChannel: uint32(len(frameData) / 2),
				},
			}
			s.annotateAudio(audio)
			if !s.sendAudio(audio) {
				s.markReadDone()
				return
			}
			s.signalCloseAck()
		} else {
			if err := s.handleTextMessage(message); err != nil {
				if errors.Is(err, errDeepgramTTSReleasedToPool) {
					s.markReadDone()
					return
				}
				s.markReadDone()
				s.sendError(err)
				return
			}
			s.signalCloseAck()
		}
	}
}

func (s *deepgramTTSStream) markReadDone() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readDone = true
}

func deepgramTTSReadError(err error) error {
	var timeoutErr interface{ Timeout() bool }
	if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
		return llm.NewAPITimeoutError(err.Error())
	}
	return deepgramTTSUnexpectedCloseError(err)
}

func (s *deepgramTTSStream) waitForInputSent() bool {
	if s.inputSent == nil {
		return true
	}
	<-s.inputSent
	return true
}

func (s *deepgramTTSStream) markInputSent() {
	if s.inputSent == nil {
		return
	}
	s.inputSentOnce.Do(func() {
		close(s.inputSent)
	})
}

func (s *deepgramTTSStream) closeDone() {
	if s.done == nil {
		return
	}
	s.doneOnce.Do(func() {
		close(s.done)
	})
}

func (s *deepgramTTSStream) sendAudio(audio *tts.SynthesizedAudio) bool {
	if s.done == nil {
		s.audio <- audio
		return true
	}
	select {
	case s.audio <- audio:
		return true
	case <-s.done:
		return false
	}
}

func (s *deepgramTTSStream) sendError(err error) {
	if s.errCh == nil {
		return
	}
	select {
	case s.errCh <- err:
	default:
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
		return llm.NewAPIConnectionError(err.Error())
	}
	if metadata == nil {
		return llm.NewAPIConnectionError("Deepgram TTS returned null text control")
	}
	switch metadata["type"] {
	case "Flushed":
		audio := &tts.SynthesizedAudio{IsFinal: true}
		s.annotateAudio(audio)
		s.mu.Lock()
		s.flushPending = false
		s.mu.Unlock()
		s.segmentID = uuid.NewString()
		released := s.closeAfterFinal()
		if !s.sendAudio(audio) {
			return nil
		}
		s.signalFlushed()
		if released {
			return errDeepgramTTSReleasedToPool
		}
	case "Error", "error":
		return llm.NewAPIError("Deepgram TTS returned error", metadata, true)
	}
	return nil
}

func deepgramTTSTelephonyToPCM(encoding string, data []byte) []byte {
	switch strings.ToLower(encoding) {
	case "mulaw", "mu-law", "ulaw", "u-law", "pcm_mulaw":
		return deepgramTTSDecodeMuLaw(data)
	case "alaw", "a-law", "pcm_alaw":
		return deepgramTTSDecodeALaw(data)
	default:
		return bytes.Clone(data)
	}
}

func deepgramTTSDecodeMuLaw(data []byte) []byte {
	pcm := make([]byte, len(data)*2)
	for i, encoded := range data {
		u := ^encoded
		sign := 1
		if u&0x80 != 0 {
			sign = -1
		}
		exponent := int((u >> 4) & 0x07)
		mantissa := int(u & 0x0f)
		sample := ((mantissa << 3) + 0x84) << exponent
		value := int16(sign * (sample - 0x84))
		pcm[i*2] = byte(value)
		pcm[i*2+1] = byte(value >> 8)
	}
	return pcm
}

func deepgramTTSDecodeALaw(data []byte) []byte {
	pcm := make([]byte, len(data)*2)
	for i, encoded := range data {
		a := encoded ^ 0x55
		sign := -1
		if a&0x80 != 0 {
			sign = 1
		}
		exponent := int((a >> 4) & 0x07)
		mantissa := int(a & 0x0f)
		sample := 0
		if exponent == 0 {
			sample = (mantissa << 4) + 8
		} else {
			sample = ((mantissa << 4) + 0x108) << (exponent - 1)
		}
		value := int16(sign * sample)
		pcm[i*2] = byte(value)
		pcm[i*2+1] = byte(value >> 8)
	}
	return pcm
}

func (s *deepgramTTSStream) annotateAudio(audio *tts.SynthesizedAudio) {
	if audio == nil {
		return
	}
	audio.RequestID = s.requestID
	audio.SegmentID = s.segmentID
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

func (s *deepgramTTSStream) signalCloseAck() {
	if s.closeAck == nil {
		return
	}
	select {
	case s.closeAck <- struct{}{}:
	default:
	}
}

func (s *deepgramTTSStream) PushText(text string) error {
	if text == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputClosed {
		return nil
	}
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}
	if !s.segmentOpen && s.segmentSent {
		return nil
	}
	s.segmentOpen = true
	s.segmentSent = true
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
	if s.inputClosed {
		return nil
	}
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}
	if !s.segmentOpen {
		s.pendingText = ""
		return nil
	}
	if err := s.sendPendingWordsLocked(); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	if err := s.ensureConnectedLocked(); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	if err := s.writeTextData(deepgramTTSFlushMessage, map[string]interface{}{"type": "Flush"}); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	s.markInputSent()
	s.segmentOpen = false
	s.flushPending = true
	return nil
}

func (s *deepgramTTSStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputClosed {
		return nil
	}
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.segmentOpen {
		if err := s.sendPendingWordsLocked(); err != nil {
			s.closeAfterWriteFailureLocked()
			return err
		}
		if err := s.ensureConnectedLocked(); err != nil {
			s.closeAfterWriteFailureLocked()
			return err
		}
		if err := s.writeTextData(deepgramTTSFlushMessage, map[string]interface{}{"type": "Flush"}); err != nil {
			s.closeAfterWriteFailureLocked()
			return err
		}
		s.markInputSent()
		s.segmentOpen = false
		s.flushPending = true
	}
	s.pendingText = ""
	s.inputEnded = true
	s.inputClosed = true
	if !s.flushPending {
		s.closed = true
		s.markInputSent()
		if s.provider != nil {
			s.provider.unregisterStream(s)
		}
		return s.closeConnection()
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
	if err := s.ensureConnectedLocked(); err != nil {
		return err
	}
	speakText := deepgramTTSSpeakText(text)
	payload := fmt.Sprintf(`{"type": "Speak", "text": %s}`, deepgramTTSPythonJSONString(speakText))
	if err := s.writeTextData(payload, map[string]interface{}{
		"type": "Speak",
		"text": speakText,
	}); err != nil {
		return err
	}
	s.markInputSent()
	return nil
}

func deepgramTTSPythonJSONString(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			switch {
			case r < 0x20:
				fmt.Fprintf(&b, `\u%04x`, r)
			case r < utf8.RuneSelf:
				b.WriteRune(r)
			case r <= 0xffff:
				fmt.Fprintf(&b, `\u%04x`, r)
			default:
				r -= 0x10000
				fmt.Fprintf(&b, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func (s *deepgramTTSStream) consumePendingToken(token string) {
	idx := strings.Index(s.pendingText, token)
	if idx < 0 {
		return
	}
	s.pendingText = strings.TrimLeftFunc(s.pendingText[idx+len(token):], unicode.IsSpace)
}

func (s *deepgramTTSStream) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Lock()
	if s.closed || s.closing {
		s.mu.Unlock()
		return nil
	}
	s.closing = true
	s.inputClosed = true
	if !s.hasConnectionLocked() {
		s.closed = true
		s.closing = false
		s.markInputSent()
		if s.provider != nil {
			s.provider.unregisterStream(s)
		}
		s.mu.Unlock()
		return nil
	}
	s.drainCloseAckLocked()
	flushErr := s.writeTextData(deepgramTTSFlushMessage, map[string]interface{}{"type": "Flush"})
	closeErr := s.writeTextData(deepgramTTSCloseMessage, map[string]interface{}{"type": "Close"})
	s.markInputSent()
	s.closeDoneIfAudioDeliveryWouldBlockLocked()
	shouldWait := flushErr == nil && closeErr == nil && !s.readDone
	s.mu.Unlock()
	if shouldWait {
		s.waitForFlushedAckLocked()
	}
	s.mu.Lock()
	s.closed = true
	s.closing = false
	if s.provider != nil {
		s.provider.unregisterStream(s)
	}
	if err := s.closeConnection(); err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	return nil
}

func (s *deepgramTTSStream) ensureConnectedLocked() error {
	if s.hasConnectionLocked() {
		return nil
	}
	if s.provider != nil {
		if conn := s.provider.takePrewarmedConn(); conn != nil {
			if s.closed || s.provider.isClosed() {
				closeDeepgramTTSPrewarmedConn(conn)
				return io.ErrClosedPipe
			}
			s.conn = conn
			s.writeJSON = s.writeJSONMessage
			s.closeConn = s.closeWebsocketConn
			go s.readLoop()
			return nil
		}
	}
	header := make(map[string][]string)
	header["Authorization"] = []string{"Token " + s.apiKey}
	conn, resp, err := websocket.DefaultDialer.DialContext(s.ctx, s.streamURL, header)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return llm.NewAPITimeoutError(err.Error())
		}
		if resp != nil && resp.StatusCode != 0 {
			if resp.Body != nil {
				_ = resp.Body.Close()
			}
			return llm.NewAPIStatusError(deepgramTTSStatusMessage(resp), resp.StatusCode, "", nil)
		}
		return llm.NewAPIConnectionError(err.Error())
	}
	if s.provider != nil && s.provider.isClosed() {
		_ = conn.Close()
		return io.ErrClosedPipe
	}
	if s.closed {
		_ = conn.Close()
		return io.ErrClosedPipe
	}
	s.conn = conn
	s.writeJSON = s.writeJSONMessage
	s.closeConn = s.closeWebsocketConn
	go s.readLoop()
	return nil
}

func (s *deepgramTTSStream) hasConnectionLocked() bool {
	return s.conn != nil || s.writeText != nil || s.writeJSON != nil || s.closeConn != nil
}

func (s *deepgramTTSStream) closeDoneIfAudioDeliveryWouldBlockLocked() {
	if s.audio == nil {
		return
	}
	if cap(s.audio) == 0 || len(s.audio) >= cap(s.audio) {
		s.readDone = true
		s.closeDone()
	}
}

func (s *deepgramTTSStream) drainCloseAckLocked() {
	if s.closeAck == nil {
		return
	}
	for {
		select {
		case <-s.closeAck:
		default:
			return
		}
	}
}

func (s *deepgramTTSStream) waitForFlushedAckLocked() {
	ack := s.closeAck
	if ack == nil {
		ack = s.flushed
	}
	if ack == nil {
		return
	}
	select {
	case <-ack:
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

func (s *deepgramTTSStream) writeTextData(payload string, fallback any) error {
	if s.writeText != nil {
		return s.writeText(payload)
	}
	if s.conn != nil {
		return s.conn.WriteMessage(websocket.TextMessage, []byte(payload))
	}
	return s.writeJSONData(fallback)
}

func (s *deepgramTTSStream) closeConnection() error {
	if s.closeConn != nil {
		return s.closeConn()
	}
	return s.closeWebsocketConn()
}

func (s *deepgramTTSStream) closeWebsocketConn() error {
	if s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

func (s *deepgramTTSStream) closeAfterWriteFailureLocked() {
	if s.closed {
		return
	}
	s.closed = true
	s.markInputSent()
	if s.provider != nil {
		s.provider.unregisterStream(s)
	}
	_ = s.closeConnection()
}

func (s *deepgramTTSStream) closeAfterFinal() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.inputEnded {
		return false
	}
	if s.closed || s.closing {
		return false
	}
	s.closed = true
	s.drainClosed = true
	conn := s.conn
	released := false
	if s.provider != nil && s.provider.storePrewarmedConn(conn) {
		s.conn = nil
		s.writeJSON = nil
		s.writeText = nil
		s.closeConn = nil
		released = true
	}
	if s.provider != nil {
		s.provider.unregisterStream(s)
	}
	if !released {
		_ = s.closeConnection()
	}
	return released
}

func (s *deepgramTTSStream) isClosed() bool {
	closed, _ := s.closedState()
	return closed
}

func (s *deepgramTTSStream) closedState() (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed, s.drainClosed
}

func (s *deepgramTTSStream) Next() (*tts.SynthesizedAudio, error) {
	if closed, drainClosed := s.closedState(); closed {
		if drainClosed {
			select {
			case audio, ok := <-s.audio:
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
				return nil, io.EOF
			}
		}
		for {
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
				if audio != nil && audio.IsFinal {
					return audio, nil
				}
			default:
				return nil, io.EOF
			}
		}
	}

	select {
	case audio, ok := <-s.audio:
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
