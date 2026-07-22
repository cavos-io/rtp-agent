package neuphonic

import (
	"bufio"
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
	"strconv"
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
	defaultNeuphonicBaseURL    = "https://api.neuphonic.com"
	defaultNeuphonicVoice      = "8e9c4bc8-3979-48ab-8626-df53befc2090"
	defaultNeuphonicLangCode   = "en"
	defaultNeuphonicEncoding   = "pcm_linear"
	defaultNeuphonicSampleRate = 22050
)

type TTS struct {
	mu         sync.Mutex
	streams    map[*neuphonicTTSSynthesizeStream]struct{}
	apiKey     string
	baseURL    string
	voice      string
	langCode   string
	encoding   string
	sampleRate int
	speed      *float64
	closed     bool
}

type TTSOption func(*TTS)

func WithNeuphonicTTSBaseURL(baseURL string) TTSOption {
	return func(t *TTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithNeuphonicTTSVoice(voice string) TTSOption {
	return func(t *TTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithNeuphonicTTSLangCode(langCode string) TTSOption {
	return func(t *TTS) {
		if langCode != "" {
			t.langCode = langCode
		}
	}
}

func WithNeuphonicTTSEncoding(encoding string) TTSOption {
	return func(t *TTS) {
		if encoding != "" {
			t.encoding = encoding
		}
	}
}

func WithNeuphonicTTSSampleRate(sampleRate int) TTSOption {
	return func(t *TTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithNeuphonicTTSSpeed(speed float64) TTSOption {
	return func(t *TTS) {
		t.speed = &speed
	}
}

func cloneFloatPtr(value *float64) *float64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func NewTTS(apiKey string, voice string, opts ...TTSOption) *TTS {
	if apiKey == "" {
		apiKey = os.Getenv("NEUPHONIC_API_KEY")
	}
	defaultSpeed := 1.0
	provider := &TTS{
		streams:    make(map[*neuphonicTTSSynthesizeStream]struct{}),
		apiKey:     apiKey,
		baseURL:    defaultNeuphonicBaseURL,
		voice:      voice,
		langCode:   defaultNeuphonicLangCode,
		encoding:   defaultNeuphonicEncoding,
		sampleRate: defaultNeuphonicSampleRate,
		speed:      &defaultSpeed,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultNeuphonicVoice
	}
	return provider
}

func (t *TTS) Label() string { return "neuphonic.TTS" }
func (t *TTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *TTS) SampleRate() int  { return t.sampleRate }
func (t *TTS) NumChannels() int { return 1 }
func (t *TTS) Model() string    { return "Octave" }
func (t *TTS) Provider() string { return "Neuphonic" }

func (t *TTS) UpdateOptions(opts ...TTSOption) {
	t.mu.Lock()
	defer t.mu.Unlock()
	candidate := &TTS{
		apiKey:     t.apiKey,
		baseURL:    t.baseURL,
		voice:      t.voice,
		langCode:   t.langCode,
		encoding:   t.encoding,
		sampleRate: t.sampleRate,
		speed:      cloneFloatPtr(t.speed),
	}
	for _, opt := range opts {
		opt(candidate)
	}
	t.voice = candidate.voice
	t.langCode = candidate.langCode
	t.speed = cloneFloatPtr(candidate.speed)
}

func (t *TTS) Close() error {
	t.mu.Lock()
	t.closed = true
	streams := make([]*neuphonicTTSSynthesizeStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.streams = make(map[*neuphonicTTSSynthesizeStream]struct{})
	t.mu.Unlock()

	var closeErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (t *TTS) isClosed() bool {
	if t == nil {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

func (t *TTS) registerStream(stream *neuphonicTTSSynthesizeStream) bool {
	if t == nil || stream == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	if t.streams == nil {
		t.streams = make(map[*neuphonicTTSSynthesizeStream]struct{})
	}
	t.streams[stream] = struct{}{}
	stream.owner = t
	return true
}

func (t *TTS) unregisterStream(stream *neuphonicTTSSynthesizeStream) {
	if t == nil || stream == nil {
		return
	}
	t.mu.Lock()
	delete(t.streams, stream)
	t.mu.Unlock()
}

func (t *TTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if err := validateNeuphonicAPIKey(t.apiKey); err != nil {
		return nil, err
	}
	return &neuphonicTTSChunkedStream{
		ctx:        ctx,
		text:       text,
		apiKey:     t.apiKey,
		baseURL:    t.baseURL,
		voice:      t.voice,
		langCode:   t.langCode,
		encoding:   t.encoding,
		sampleRate: t.sampleRate,
		speed:      cloneFloat64Ptr(t.speed),
	}, nil
}

func buildNeuphonicTTSRequest(ctx context.Context, t *TTS, text string) (*http.Request, error) {
	return buildNeuphonicTTSRequestFromOptions(ctx, neuphonicTTSRequestOptions{
		text:       text,
		apiKey:     t.apiKey,
		baseURL:    t.baseURL,
		voice:      t.voice,
		langCode:   t.langCode,
		encoding:   t.encoding,
		sampleRate: t.sampleRate,
		speed:      cloneFloat64Ptr(t.speed),
	})
}

type neuphonicTTSRequestOptions struct {
	text       string
	apiKey     string
	baseURL    string
	voice      string
	langCode   string
	encoding   string
	sampleRate int
	speed      *float64
}

func buildNeuphonicTTSRequestFromOptions(ctx context.Context, opts neuphonicTTSRequestOptions) (*http.Request, error) {
	reqBody := map[string]interface{}{
		"text":          opts.text,
		"voice_id":      opts.voice,
		"lang_code":     opts.langCode,
		"encoding":      opts.encoding,
		"sampling_rate": opts.sampleRate,
		"speed":         optionalFloat(opts.speed),
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(opts.baseURL, "/")+"/sse/speak/"+opts.langCode, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", opts.apiKey)
	return req, nil
}

func (t *TTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if err := validateNeuphonicAPIKey(t.apiKey); err != nil {
		return nil, err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildNeuphonicTTSWebsocketURL(t).String(), buildNeuphonicTTSWebsocketHeaders(t))
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("failed to dial neuphonic tts websocket: %v", err))
	}
	if t.isClosed() {
		conn.Close()
		return nil, io.ErrClosedPipe
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &neuphonicTTSSynthesizeStream{
		conn:       conn,
		ctx:        streamCtx,
		cancel:     cancel,
		sampleRate: t.sampleRate,
		segmentID:  neuphonicTTSSegmentID(),
		events:     make(chan *tts.SynthesizedAudio, 100),
		errCh:      make(chan error, 1),
	}
	stream.writeMessage = stream.writeWebsocketMessage
	stream.closeConn = stream.closeWebsocketConn
	if !t.registerStream(stream) {
		cancel()
		conn.Close()
		return nil, io.ErrClosedPipe
	}
	go stream.readLoop()
	return stream, nil
}

func validateNeuphonicAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("neuphonic API key or JWT token is required, either as argument or set NEUPHONIC_API_KEY environment variable")
	}
	return nil
}

func buildNeuphonicTTSWebsocketURL(t *TTS) *url.URL {
	baseURL := strings.TrimRight(t.baseURL, "/")
	if strings.HasPrefix(baseURL, "http://") || strings.HasPrefix(baseURL, "https://") {
		baseURL = strings.Replace(baseURL, "http", "ws", 1)
	}
	wsURL, err := url.Parse(baseURL + "/speak/en")
	if err != nil {
		return &url.URL{Scheme: "wss", Host: strings.TrimPrefix(baseURL, "wss://"), Path: "/speak/en"}
	}
	query := wsURL.Query()
	if t.speed != nil {
		query.Set("speed", strconv.FormatFloat(*t.speed, 'f', -1, 64))
	}
	query.Set("lang_code", t.langCode)
	query.Set("sampling_rate", strconv.Itoa(t.sampleRate))
	query.Set("voice_id", t.voice)
	wsURL.RawQuery = query.Encode()
	return wsURL
}

func buildNeuphonicTTSWebsocketHeaders(t *TTS) http.Header {
	headers := make(http.Header)
	headers.Set("x-api-key", t.apiKey)
	return headers
}

func buildNeuphonicTTSTextMessage(text string, contextID string) ([]byte, error) {
	return json.Marshal(map[string]string{
		"text":       text + "<STOP>",
		"context_id": contextID,
	})
}

type neuphonicTTSChunkedStream struct {
	resp       *http.Response
	ctx        context.Context
	text       string
	apiKey     string
	baseURL    string
	voice      string
	langCode   string
	encoding   string
	sampleRate int
	speed      *float64
	requested  bool
	scanner    *bufio.Scanner
	finalSent  bool
}

func (s *neuphonicTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if err := s.ensureResponse(); err != nil {
		return nil, err
	}
	if s.resp == nil || s.resp.Body == nil {
		return nil, io.EOF
	}
	if s.scanner == nil {
		s.scanner = bufio.NewScanner(s.resp.Body)
	}
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		audio, err := neuphonicAudioFromSSEData(strings.TrimPrefix(line, "data: "))
		if err != nil {
			return nil, neuphonicTTSConnectionError("Neuphonic TTS stream decode failed", err)
		}
		if len(audio) == 0 {
			continue
		}
		return &tts.SynthesizedAudio{
			Frame: &model.AudioFrame{
				Data:              audio,
				SampleRate:        uint32(s.sampleRate),
				NumChannels:       1,
				SamplesPerChannel: uint32(len(audio) / 2),
			},
		}, nil
	}
	if err := s.scanner.Err(); err != nil {
		return nil, neuphonicTTSConnectionError("Neuphonic TTS stream read failed", err)
	}
	if s.finalSent {
		return nil, io.EOF
	}
	s.finalSent = true
	return &tts.SynthesizedAudio{IsFinal: true}, nil
}

func (s *neuphonicTTSChunkedStream) ensureResponse() error {
	if s.resp != nil || s.requested || s.text == "" {
		return nil
	}
	s.requested = true
	req, err := buildNeuphonicTTSRequestFromOptions(s.ctx, neuphonicTTSRequestOptions{
		text:       s.text,
		apiKey:     s.apiKey,
		baseURL:    s.baseURL,
		voice:      s.voice,
		langCode:   s.langCode,
		encoding:   s.encoding,
		sampleRate: s.sampleRate,
		speed:      cloneFloat64Ptr(s.speed),
	})
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return neuphonicTTSConnectionError("Neuphonic TTS request failed", err)
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return llm.NewAPIStatusError("Neuphonic TTS request failed", resp.StatusCode, "", string(respBody))
	}
	s.resp = resp
	return nil
}

func (s *neuphonicTTSChunkedStream) Close() error {
	if s.resp == nil || s.resp.Body == nil {
		return nil
	}
	body := s.resp.Body
	s.resp = nil
	s.finalSent = true
	return body.Close()
}

func neuphonicAudioFromSSEData(data string) ([]byte, error) {
	var parsed struct {
		StatusCode int `json:"status_code"`
		Data       struct {
			Audio string `json:"audio"`
		} `json:"data"`
		Errors interface{} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		return nil, err
	}
	if parsed.Errors != nil {
		return nil, fmt.Errorf("neuphonic tts error: %v", parsed.Errors)
	}
	if parsed.Data.Audio == "" {
		return nil, nil
	}
	return neuphonicDecodeBase64Audio(parsed.Data.Audio)
}

func neuphonicDecodeBase64Audio(data string) ([]byte, error) {
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

func neuphonicTTSConnectionError(message string, err error) error {
	if err == nil {
		return llm.NewAPIConnectionError(message)
	}
	return llm.NewAPIConnectionError(fmt.Sprintf("%s: %v", message, err))
}

func optionalFloat(value *float64) interface{} {
	if value == nil {
		return nil
	}
	return *value
}

func cloneFloat64Ptr(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

type neuphonicTTSSynthesizeStream struct {
	owner       *TTS
	conn        *websocket.Conn
	ctx         context.Context
	cancel      context.CancelFunc
	sampleRate  int
	segmentID   string
	events      chan *tts.SynthesizedAudio
	errCh       chan error
	mu          sync.Mutex
	closed      bool
	inputEnded  bool
	pendingText string

	writeMessage func(int, []byte) error
	closeConn    func() error
}

func (s *neuphonicTTSSynthesizeStream) PushText(text string) error {
	if text == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.inputEnded {
		return io.ErrClosedPipe
	}
	s.pendingText += text
	if err := s.sendCompleteSentencesLocked(); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

func (s *neuphonicTTSSynthesizeStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.inputEnded {
		return io.ErrClosedPipe
	}
	return s.flushPendingTextLocked(true)
}

func (s *neuphonicTTSSynthesizeStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.inputEnded {
		return nil
	}
	if err := s.flushPendingTextLocked(false); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	s.inputEnded = true
	return nil
}

func (s *neuphonicTTSSynthesizeStream) flushPendingTextLocked(advanceSegment bool) error {
	if s.pendingText != "" {
		text := strings.Join(tokenize.NewBasicSentenceTokenizer().Tokenize(s.pendingText, ""), " ")
		s.pendingText = ""
		if err := s.sendSentenceLocked(text); err != nil {
			s.closeAfterWriteFailureLocked()
			return err
		}
	}
	if advanceSegment {
		s.segmentID = neuphonicTTSSegmentID()
	}
	return nil
}

func (s *neuphonicTTSSynthesizeStream) sendCompleteSentencesLocked() error {
	for {
		tokens := tokenize.NewBasicSentenceTokenizer().Tokenize(s.pendingText, "")
		if len(tokens) <= 1 {
			return nil
		}
		sentence := tokens[0]
		if err := s.sendSentenceLocked(sentence); err != nil {
			return err
		}
		tokenIdx := strings.Index(s.pendingText, sentence)
		if tokenIdx < 0 {
			s.pendingText = strings.TrimSpace(strings.TrimPrefix(s.pendingText, sentence))
			continue
		}
		s.pendingText = strings.TrimLeftFunc(s.pendingText[tokenIdx+len(sentence):], func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\n' || r == '\r'
		})
	}
}

func (s *neuphonicTTSSynthesizeStream) sendSentenceLocked(text string) error {
	if text == "" {
		return nil
	}
	message, err := buildNeuphonicTTSTextMessage(text, s.segmentID)
	if err != nil {
		return err
	}
	return s.writeMessageData(websocket.TextMessage, message)
}

func (s *neuphonicTTSSynthesizeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
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

func (s *neuphonicTTSSynthesizeStream) writeMessageData(messageType int, data []byte) error {
	if s.writeMessage != nil {
		return s.writeMessage(messageType, data)
	}
	return s.writeWebsocketMessage(messageType, data)
}

func (s *neuphonicTTSSynthesizeStream) writeWebsocketMessage(messageType int, data []byte) error {
	return s.conn.WriteMessage(messageType, data)
}

func (s *neuphonicTTSSynthesizeStream) closeConnection() error {
	if s.closeConn != nil {
		return s.closeConn()
	}
	return s.closeWebsocketConn()
}

func (s *neuphonicTTSSynthesizeStream) closeWebsocketConn() error {
	if s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

func (s *neuphonicTTSSynthesizeStream) closeAfterWriteFailureLocked() {
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

func (s *neuphonicTTSSynthesizeStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *neuphonicTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *neuphonicTTSSynthesizeStream) readLoop() {
	defer close(s.events)
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			if !s.isClosed() {
				s.errCh <- neuphonicTTSReadError(err)
			}
			return
		}
		if msgType != websocket.TextMessage {
			continue
		}
		audio, done, err := neuphonicAudioFromStreamMessage(payload, s.segmentID, s.sampleRate)
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

func neuphonicTTSReadError(err error) error {
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return llm.NewAPIStatusError("NeuPhonic websocket connection closed unexpectedly", closeErr.Code, "", err.Error())
	}
	return llm.NewAPIConnectionError(fmt.Sprintf("NeuPhonic websocket receive failed: %v", err))
}

func neuphonicAudioFromStreamMessage(payload []byte, contextID string, sampleRate int) (*tts.SynthesizedAudio, bool, error) {
	var message struct {
		Type string `json:"type"`
		Data struct {
			Audio     string `json:"audio"`
			ContextID string `json:"context_id"`
			Stop      bool   `json:"stop"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, nil
	}
	if message.Type == "error" {
		body := string(payload)
		return nil, false, llm.NewAPIError(fmt.Sprintf("NeuPhonic returned error: %s", body), body, true)
	}
	if message.Data.ContextID != "" && message.Data.ContextID != contextID {
		return nil, false, nil
	}
	if message.Data.Audio != "" {
		audio, err := neuphonicDecodeBase64Audio(message.Data.Audio)
		if err != nil {
			return nil, false, nil
		}
		if len(audio) > 0 {
			return neuphonicTTSAudioFrame(audio, sampleRate), false, nil
		}
	}
	if message.Data.Stop {
		return &tts.SynthesizedAudio{IsFinal: true}, true, nil
	}
	return nil, message.Data.Stop, nil
}

func neuphonicTTSAudioFrame(audio []byte, sampleRate int) *tts.SynthesizedAudio {
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              bytes.Clone(audio),
			SampleRate:        uint32(sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(len(audio) / 2),
		},
	}
}

func neuphonicTTSSegmentID() string {
	return "segment-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

// Deprecated: use TTS.
type NeuphonicTTS = TTS

// Deprecated: use TTSOption.
type NeuphonicTTSOption = TTSOption

// Deprecated: use NewTTS.
func NewNeuphonicTTS(apiKey string, voice string, opts ...TTSOption) *TTS {
	return NewTTS(apiKey, voice, opts...)
}
