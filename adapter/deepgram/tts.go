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
const deepgramTTSCloseAckTimeout = time.Second
const deepgramTTSFlushMessage = `{"type": "Flush"}`
const deepgramTTSCloseMessage = `{"type": "Close"}`

type DeepgramTTS struct {
	apiKey     string
	baseURL    string
	model      string
	encoding   string
	sampleRate int
	mipOptOut  bool
	mu         sync.Mutex
	streams    map[*deepgramTTSStream]struct{}
	closed     bool
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
	t.closed = true
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
		if resp.StatusCode == 499 {
			resp.Body.Close()
			return &deepgramTTSChunkedStream{requestID: uuid.NewString()}, nil
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, llm.NewAPIStatusError("Deepgram TTS request failed", resp.StatusCode, "", string(respBody))
	}

	return &deepgramTTSChunkedStream{
		resp:       resp,
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
	header := make(map[string][]string)
	header["Authorization"] = []string{"Token " + t.apiKey}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildDeepgramTTSStreamURL(t), header)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, llm.NewAPITimeoutError(err.Error())
		}
		return nil, llm.NewAPIConnectionError(err.Error())
	}
	if t.isClosed() {
		conn.Close()
		return nil, io.ErrClosedPipe
	}

	stream := &deepgramTTSStream{
		provider:   t,
		conn:       conn,
		audio:      make(chan *tts.SynthesizedAudio, 10),
		errCh:      make(chan error, 1),
		flushed:    make(chan struct{}, 1),
		sampleRate: t.sampleRate,
		encoding:   t.encoding,
		requestID:  uuid.NewString(),
		segmentID:  uuid.NewString(),
	}
	stream.writeJSON = stream.writeJSONMessage
	stream.closeConn = stream.closeWebsocketConn
	if !t.registerStream(stream) {
		conn.Close()
		return nil, io.ErrClosedPipe
	}

	go stream.readLoop()

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
	resp         *http.Response
	sampleRate   int
	encoding     string
	requestID    string
	pendingFinal bool
	finalSent    bool
	mu           sync.Mutex
}

func (s *deepgramTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resp == nil || s.resp.Body == nil || s.finalSent {
		return nil, io.EOF
	}
	if s.pendingFinal {
		s.pendingFinal = false
		return s.emitFinal()
	}
	buf := make([]byte, 4096)
	n, err := s.resp.Body.Read(buf)
	if n > 0 && err == io.EOF {
		s.pendingFinal = true
	}
	if err != nil {
		if err == io.EOF && n == 0 {
			return s.emitFinal()
		}
		if err != io.EOF && errors.Is(err, context.DeadlineExceeded) {
			return nil, llm.NewAPITimeoutError(err.Error())
		}
		if err != io.EOF {
			return nil, llm.NewAPIConnectionError(err.Error())
		}
	}

	frameData := deepgramTTSTelephonyToPCM(s.encoding, buf[:n])
	return &tts.SynthesizedAudio{
		RequestID: s.requestID,
		Frame: &model.AudioFrame{
			Data:              frameData,
			SampleRate:        uint32(s.sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(len(frameData) / 2),
		},
	}, nil
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
	return &tts.SynthesizedAudio{RequestID: s.requestID, IsFinal: true}, nil
}

func (s *deepgramTTSChunkedStream) Close() error {
	s.mu.Lock()
	if s.resp == nil || s.resp.Body == nil {
		s.mu.Unlock()
		return nil
	}
	body := s.resp.Body
	s.resp = nil
	s.finalSent = true
	s.mu.Unlock()
	return body.Close()
}

type deepgramTTSStream struct {
	provider    *DeepgramTTS
	conn        *websocket.Conn
	audio       chan *tts.SynthesizedAudio
	errCh       chan error
	mu          sync.Mutex
	closed      bool
	inputEnded  bool
	drainClosed bool

	sampleRate   int
	encoding     string
	writeJSON    func(any) error
	writeText    func(string) error
	closeConn    func() error
	flushed      chan struct{}
	pendingText  string
	requestID    string
	segmentID    string
	segmentOpen  bool
	segmentSent  bool
	flushPending bool
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
			s.audio <- audio
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
		return llm.NewAPIConnectionError(err.Error())
	}
	switch metadata["type"] {
	case "Flushed":
		audio := &tts.SynthesizedAudio{IsFinal: true}
		s.annotateAudio(audio)
		s.audio <- audio
		s.signalFlushed()
		s.mu.Lock()
		s.flushPending = false
		s.mu.Unlock()
		s.segmentID = uuid.NewString()
		s.closeAfterFinal()
	case "Error", "error":
		return llm.NewAPIError("Deepgram TTS returned error", metadata, true)
	}
	return nil
}

func deepgramTTSTelephonyToPCM(encoding string, data []byte) []byte {
	switch strings.ToLower(encoding) {
	case "mulaw", "mu-law", "ulaw", "u-law":
		return deepgramTTSDecodeMuLaw(data)
	case "alaw", "a-law":
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

func (s *deepgramTTSStream) PushText(text string) error {
	if text == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
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
	if err := s.writeTextData(deepgramTTSFlushMessage, map[string]interface{}{"type": "Flush"}); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	s.segmentOpen = false
	s.flushPending = true
	return nil
}

func (s *deepgramTTSStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
		if err := s.writeTextData(deepgramTTSFlushMessage, map[string]interface{}{"type": "Flush"}); err != nil {
			s.closeAfterWriteFailureLocked()
			return err
		}
		s.segmentOpen = false
		s.flushPending = true
	}
	s.pendingText = ""
	s.inputEnded = true
	if !s.flushPending {
		s.closed = true
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
	speakText := deepgramTTSSpeakText(text)
	payload := fmt.Sprintf(`{"type": "Speak", "text": %s}`, deepgramTTSPythonJSONString(speakText))
	return s.writeTextData(payload, map[string]interface{}{
		"type": "Speak",
		"text": speakText,
	})
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	flushErr := s.writeTextData(deepgramTTSFlushMessage, map[string]interface{}{"type": "Flush"})
	closeErr := s.writeTextData(deepgramTTSCloseMessage, map[string]interface{}{"type": "Close"})
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
	return s.conn.Close()
}

func (s *deepgramTTSStream) closeAfterWriteFailureLocked() {
	if s.closed {
		return
	}
	s.closed = true
	_ = s.closeConnection()
}

func (s *deepgramTTSStream) closeAfterFinal() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.inputEnded {
		return
	}
	if s.closed {
		return
	}
	s.closed = true
	s.drainClosed = true
	if s.provider != nil {
		s.provider.unregisterStream(s)
	}
	_ = s.closeConnection()
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
