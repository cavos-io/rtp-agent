package upliftai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	coreaudio "github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/codecs"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/library/tokenize"
	"github.com/gorilla/websocket"
)

const (
	defaultUpliftAIVoiceID    = "v_meklc281"
	defaultUpliftAISampleRate = 22050
	defaultUpliftAIFormat     = "MP3_22050_32"
	defaultUpliftAIBaseURL    = "wss://api.upliftai.org"
	upliftAISocketIONamespace = "/text-to-speech/multi-stream"
	upliftAISocketIOReadyWait = 5 * time.Second
	upliftAISocketIODialWait  = 10 * time.Second
	upliftAISocketIOAttempts  = 3
)

var (
	upliftAISocketIOAudioWait      = 30 * time.Second
	upliftAISocketIOReconnectDelay = time.Second
)

type UpliftAITTS struct {
	apiKey                    string
	voice                     string
	outputFormat              string
	numChannels               int
	baseURL                   string
	wordTokenizer             tokenize.WordTokenizer
	sentenceTokenizer         tokenize.SentenceTokenizer
	tokenizerKind             string
	phraseReplacementConfigID string
	mu                        sync.Mutex
	closed                    bool
	streams                   map[io.Closer]struct{}
	socketClient              *upliftAISocketIOClient
}

type UpliftAITTSOption func(*UpliftAITTS)
type UpliftAITTSUpdateOption func(*upliftAITTSUpdateOptions)

type upliftAISocketIOConn interface {
	ReadMessage() (int, []byte, error)
	WriteMessage(messageType int, data []byte) error
	Close() error
}

type upliftAISocketIODeadlineConn interface {
	SetReadDeadline(t time.Time) error
}

var upliftAISocketIODialContext = func(ctx context.Context, endpoint string) (upliftAISocketIOConn, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

type upliftAITTSUpdateOptions struct {
	voiceID      *string
	outputFormat *string
}

func WithUpliftAIBaseURL(baseURL string) UpliftAITTSOption {
	return func(t *UpliftAITTS) {
		t.baseURL = baseURL
	}
}

func WithUpliftAIOutputFormat(outputFormat string) UpliftAITTSOption {
	return func(t *UpliftAITTS) {
		t.outputFormat = outputFormat
	}
}

func WithUpliftAINumChannels(numChannels int) UpliftAITTSOption {
	return func(t *UpliftAITTS) {
		t.numChannels = numChannels
	}
}

func WithUpliftAISentenceTokenizer(tokenizer tokenize.SentenceTokenizer) UpliftAITTSOption {
	return func(t *UpliftAITTS) {
		if tokenizer == nil {
			return
		}
		t.sentenceTokenizer = tokenizer
		t.wordTokenizer = nil
		t.tokenizerKind = "sentence"
	}
}

func WithUpliftAIWordTokenizer(tokenizer tokenize.WordTokenizer) UpliftAITTSOption {
	return func(t *UpliftAITTS) {
		if tokenizer == nil {
			return
		}
		t.wordTokenizer = tokenizer
		t.sentenceTokenizer = nil
		t.tokenizerKind = "word"
	}
}

func WithUpliftAIPhraseReplacementConfigID(configID string) UpliftAITTSOption {
	return func(t *UpliftAITTS) {
		t.phraseReplacementConfigID = configID
	}
}

func WithUpliftAIUpdateVoiceID(voiceID string) UpliftAITTSUpdateOption {
	return func(opts *upliftAITTSUpdateOptions) {
		opts.voiceID = &voiceID
	}
}

func WithUpliftAIUpdateOutputFormat(outputFormat string) UpliftAITTSUpdateOption {
	return func(opts *upliftAITTSUpdateOptions) {
		opts.outputFormat = &outputFormat
	}
}

func NewUpliftAITTS(apiKey string, voice string, opts ...UpliftAITTSOption) *UpliftAITTS {
	if apiKey == "" {
		apiKey = os.Getenv("UPLIFTAI_API_KEY")
	}
	if voice == "" {
		voice = defaultUpliftAIVoiceID
	}
	baseURL := os.Getenv("UPLIFTAI_BASE_URL")
	if baseURL == "" {
		baseURL = defaultUpliftAIBaseURL
	}
	tts := &UpliftAITTS{
		apiKey:       apiKey,
		voice:        voice,
		outputFormat: defaultUpliftAIFormat,
		numChannels:  1,
		baseURL:      baseURL,
		streams:      make(map[io.Closer]struct{}),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(tts)
		}
	}
	return tts
}

func (t *UpliftAITTS) Label() string { return "upliftai.TTS" }
func (t *UpliftAITTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *UpliftAITTS) SampleRate() int { return defaultUpliftAISampleRate }
func (t *UpliftAITTS) NumChannels() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.numChannels
}

func (t *UpliftAITTS) UpdateOptions(opts ...UpliftAITTSUpdateOption) {
	var update upliftAITTSUpdateOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&update)
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if update.voiceID != nil {
		t.voice = *update.voiceID
	}
	if update.outputFormat != nil {
		t.outputFormat = *update.outputFormat
	}
}

func (t *UpliftAITTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if t.apiKey == "" {
		return nil, fmt.Errorf("API key is required, either as argument or set UPLIFTAI_API_KEY environment variable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)

	stream := &upliftAITTSChunkedStream{
		owner:  t,
		ctx:    ctx,
		cancel: cancel,
		text:   text,
	}
	if !t.registerStream(stream) {
		_ = stream.Close()
		return nil, io.ErrClosedPipe
	}
	return stream, nil
}

func (t *UpliftAITTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	stream := newUpliftAITTSSynthesizeStream(t, ctx)
	if !t.registerStream(stream) {
		_ = stream.Close()
		return nil, io.ErrClosedPipe
	}
	return stream, nil
}

func (t *UpliftAITTS) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	streams := make([]io.Closer, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.streams = make(map[io.Closer]struct{})
	socketClient := t.socketClient
	t.socketClient = nil
	t.mu.Unlock()

	var closeErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	if socketClient != nil {
		if err := socketClient.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (t *UpliftAITTS) isClosed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

func (t *UpliftAITTS) registerStream(stream io.Closer) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	t.streams[stream] = struct{}{}
	return true
}

func (t *UpliftAITTS) unregisterStream(stream io.Closer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.streams, stream)
}

func (t *UpliftAITTS) requestOptions() (baseURL string, voiceID string, outputFormat string, phraseReplacementConfigID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.baseURL, t.voice, t.outputFormat, t.phraseReplacementConfigID
}

func (t *UpliftAITTS) outputNumChannels() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.numChannels
}

func (t *UpliftAITTS) streamSentenceTokenizer() tokenize.SentenceTokenizer {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tokenizerKind != "sentence" {
		return nil
	}
	return t.sentenceTokenizer
}

func (t *UpliftAITTS) streamWordTokenizer() tokenize.WordTokenizer {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tokenizerKind != "word" {
		return nil
	}
	return t.wordTokenizer
}

func (t *UpliftAITTS) socketIOSynthesis(ctx context.Context, baseURL string, text string, voiceID string, outputFormat string, phraseReplacementConfigID string) (io.ReadCloser, error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, io.ErrClosedPipe
	}
	client := t.socketClient
	if client == nil || client.baseURL != baseURL || client.apiKey != t.apiKey {
		if client != nil {
			_ = client.Close()
		}
		client = newUpliftAISocketIOClient(baseURL, t.apiKey)
		t.socketClient = client
	}
	t.mu.Unlock()
	return client.Synthesize(ctx, text, voiceID, outputFormat, phraseReplacementConfigID)
}

type upliftAITTSSynthesizeStream struct {
	owner  *UpliftAITTS
	ctx    context.Context
	cancel context.CancelFunc

	mu        sync.Mutex
	buf       strings.Builder
	inputCh   chan string
	eventCh   chan upliftAITTSStreamResult
	doneCh    chan struct{}
	active    tts.ChunkedStream
	closed    bool
	inputDone bool
	once      sync.Once
}

type upliftAITTSStreamResult struct {
	audio *tts.SynthesizedAudio
	err   error
}

func newUpliftAITTSSynthesizeStream(owner *UpliftAITTS, ctx context.Context) *upliftAITTSSynthesizeStream {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	stream := &upliftAITTSSynthesizeStream{
		owner:   owner,
		ctx:     ctx,
		cancel:  cancel,
		inputCh: make(chan string, 100),
		eventCh: make(chan upliftAITTSStreamResult, 100),
		doneCh:  make(chan struct{}),
	}
	go stream.run()
	return stream
}

func (s *upliftAITTSSynthesizeStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if s.inputDone {
		return nil
	}
	if text == "" {
		return nil
	}
	s.buf.WriteString(text)
	return nil
}

func (s *upliftAITTSSynthesizeStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if s.inputDone {
		return nil
	}
	text := s.buf.String()
	if text == "" {
		return nil
	}
	text = s.formatSegmentText(text)
	if text == "" {
		s.buf.Reset()
		return nil
	}
	s.buf.Reset()
	select {
	case s.inputCh <- text:
		return nil
	case <-s.doneCh:
		return io.ErrClosedPipe
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func (s *upliftAITTSSynthesizeStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.inputDone {
		return nil
	}
	text := s.buf.String()
	s.buf.Reset()
	if text != "" {
		text = s.formatSegmentText(text)
		if text == "" {
			s.inputDone = true
			close(s.inputCh)
			return nil
		}
		select {
		case s.inputCh <- text:
		case <-s.doneCh:
			return io.ErrClosedPipe
		case <-s.ctx.Done():
			return s.ctx.Err()
		}
	}
	s.inputDone = true
	close(s.inputCh)
	return nil
}

func (s *upliftAITTSSynthesizeStream) formatSegmentText(text string) string {
	if s.owner != nil {
		if tokenizer := s.owner.streamWordTokenizer(); tokenizer != nil {
			words := tokenizer.Tokenize(text, "")
			return strings.TrimSpace(tokenizer.FormatWords(words))
		}
		if tokenizer := s.owner.streamSentenceTokenizer(); tokenizer != nil {
			sentences := tokenizer.Tokenize(text, "")
			parts := make([]string, 0, len(sentences))
			for _, sentence := range sentences {
				if sentence = strings.TrimSpace(sentence); sentence != "" {
					parts = append(parts, sentence)
				}
			}
			return strings.Join(parts, " ")
		}
	}
	wordTokens := tokenize.SplitWords(text, false, false, false)
	words := make([]string, 0, len(wordTokens))
	for _, word := range wordTokens {
		words = append(words, word.Token)
	}
	if len(words) == 0 {
		return ""
	}
	return strings.Join(words, " ")
}

func (s *upliftAITTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return nil, io.EOF
		}
		s.mu.Unlock()

		select {
		case result, ok := <-s.eventCh:
			if !ok {
				return nil, io.EOF
			}
			if result.err != nil {
				return nil, result.err
			}
			return result.audio, nil
		case <-s.ctx.Done():
			return nil, io.EOF
		}
	}
}

func (s *upliftAITTSSynthesizeStream) run() {
	defer close(s.doneCh)
	defer close(s.eventCh)
	for {
		select {
		case <-s.ctx.Done():
			return
		case text, ok := <-s.inputCh:
			if !ok {
				return
			}
			if err := s.runSegment(text); err != nil {
				_ = s.sendResult(nil, llm.NewAPIConnectionError(fmt.Sprintf("UpliftAI TTS segment synthesis failed: %v", err)))
				return
			}
		}
	}
}

func (s *upliftAITTSSynthesizeStream) runSegment(text string) error {
	stream, err := s.owner.Synthesize(s.ctx, text)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = stream.Close()
		return nil
	}
	s.active = stream
	s.mu.Unlock()
	defer func() {
		_ = stream.Close()
		s.mu.Lock()
		if s.active == stream {
			s.active = nil
		}
		s.mu.Unlock()
	}()

	for {
		audio, err := stream.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if audio == nil {
			continue
		}
		if !s.sendResult(audio, nil) {
			return nil
		}
	}
}

func (s *upliftAITTSSynthesizeStream) sendResult(audio *tts.SynthesizedAudio, err error) bool {
	select {
	case s.eventCh <- upliftAITTSStreamResult{audio: audio, err: err}:
		return true
	case <-s.ctx.Done():
		return false
	}
}

func (s *upliftAITTSSynthesizeStream) Close() error {
	var closeErr error
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		if !s.inputDone {
			s.inputDone = true
			close(s.inputCh)
		}
		active := s.active
		s.active = nil
		s.mu.Unlock()

		s.cancel()
		if s.owner != nil {
			s.owner.unregisterStream(s)
		}
		if active != nil {
			closeErr = active.Close()
		}
	})
	return closeErr
}

type upliftAITTSChunkedStream struct {
	owner          *UpliftAITTS
	ctx            context.Context
	cancel         context.CancelFunc
	text           string
	resp           *http.Response
	once           sync.Once
	err            error
	decoder        codecs.AudioStreamDecoder
	decodeMu       sync.Mutex
	decodeReadErr  error
	wav            *upliftAIWAVStream
	pcm            *coreaudio.AudioByteStream
	pcmFrames      []*model.AudioFrame
	pendingReadErr error
	outputFormat   string
	started        bool
	hasAudio       bool
	pendingFinal   bool
	finalSent      bool
	closed         bool
}

func (s *upliftAITTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.closed || s.finalSent {
		return nil, io.EOF
	}
	if err := s.ensureResponse(); err != nil {
		if s.closed {
			return nil, io.EOF
		}
		return nil, err
	}
	if s.resp == nil || s.resp.Body == nil {
		return nil, io.EOF
	}
	if strings.HasPrefix(s.currentOutputFormat(), "MP3") {
		return s.nextDecodedMP3()
	}
	if strings.HasPrefix(s.currentOutputFormat(), "WAV") {
		return s.nextDecodedWAV()
	}
	if strings.HasPrefix(s.currentOutputFormat(), "OGG") {
		return s.nextDecodedOGG()
	}
	if s.currentOutputFormat() == "ULAW_8000_8" {
		return s.nextBufferedULaw()
	}
	return s.nextRawPCM()
}

func (s *upliftAITTSChunkedStream) ensureResponse() error {
	if s.resp != nil || s.owner == nil {
		return nil
	}
	baseURL, voiceID, outputFormat, phraseReplacementConfigID := s.owner.requestOptions()
	s.outputFormat = outputFormat
	if err := validateUpliftAIOutputFormat(outputFormat); err != nil {
		return llm.NewAPIConnectionError(err.Error())
	}
	if upliftAIUsesSocketIO(baseURL) {
		body, err := s.owner.socketIOSynthesis(
			s.ctx,
			baseURL,
			s.text,
			voiceID,
			outputFormat,
			phraseReplacementConfigID,
		)
		if err != nil {
			return err
		}
		s.resp = &http.Response{Body: body}
		return nil
	}
	reqBody := map[string]interface{}{
		"text":         s.text,
		"voiceId":      voiceID,
		"outputFormat": outputFormat,
	}
	if phraseReplacementConfigID != "" {
		reqBody["phraseReplacementConfigId"] = phraseReplacementConfigID
	}
	jsonBody, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(s.ctx, "POST", baseURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.owner.apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return llm.NewAPITimeoutError(fmt.Sprintf("UpliftAI TTS request failed: %v", err))
		}
		return llm.NewAPIConnectionError(fmt.Sprintf("UpliftAI TTS request failed: %v", err))
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return llm.NewAPIStatusError("UpliftAI TTS request failed", resp.StatusCode, "", string(respBody))
	}
	s.resp = resp
	return nil
}

func upliftAIUsesSocketIO(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	return u.Scheme == "ws" || u.Scheme == "wss"
}

type upliftAISocketIOClient struct {
	baseURL string
	apiKey  string

	mu       sync.Mutex
	writeMu  sync.Mutex
	conn     upliftAISocketIOConn
	closed   bool
	requests map[string]*upliftAISocketIORequest
	seq      atomic.Uint64
}

type upliftAISocketIORequest struct {
	pw    *io.PipeWriter
	timer *time.Timer
}

type upliftAISocketIOEvent struct {
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Audio     string `json:"audio"`
	Message   string `json:"message"`
}

func newUpliftAISocketIOClient(baseURL string, apiKey string) *upliftAISocketIOClient {
	return &upliftAISocketIOClient{
		baseURL:  baseURL,
		apiKey:   apiKey,
		requests: make(map[string]*upliftAISocketIORequest),
	}
}

func (c *upliftAISocketIOClient) Synthesize(ctx context.Context, text string, voiceID string, outputFormat string, phraseReplacementConfigID string) (io.ReadCloser, error) {
	if err := c.ensureConnected(ctx); err != nil {
		return nil, err
	}
	requestID := fmt.Sprintf("upliftai-%p-%d", c, c.seq.Add(1))
	pr, pw := io.Pipe()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = pr.Close()
		_ = pw.Close()
		return nil, io.ErrClosedPipe
	}
	conn := c.conn
	c.requests[requestID] = &upliftAISocketIORequest{
		pw:    pw,
		timer: time.AfterFunc(upliftAISocketIOAudioWait, func() { c.finishRequest(requestID, nil) }),
	}
	c.mu.Unlock()

	c.writeMu.Lock()
	err := writeUpliftAISocketIOSynthesize(conn, requestID, text, voiceID, outputFormat, phraseReplacementConfigID)
	c.writeMu.Unlock()
	if err != nil {
		c.finishRequest(requestID, err)
		c.closeConn(conn, nil)
		_ = pr.Close()
		return nil, err
	}
	return &upliftAISocketIOBody{PipeReader: pr, client: c, requestID: requestID}, nil
}

func (c *upliftAISocketIOClient) ensureConnected(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return io.ErrClosedPipe
	}
	if c.conn != nil {
		c.mu.Unlock()
		return nil
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, upliftAISocketIODialWait)
		defer cancel()
	}
	socketURL, err := buildUpliftAISocketIOURL(c.baseURL)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	var lastErr error
	for attempt := 1; attempt <= upliftAISocketIOAttempts; attempt++ {
		conn, err := upliftAISocketIODialContext(ctx, socketURL)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				c.mu.Unlock()
				return context.Canceled
			}
			lastErr = llm.NewAPIConnectionError(fmt.Sprintf("UpliftAI TTS socket.io dial failed: %v", err))
		} else if err := upliftAISocketIOConnect(ctx, conn, c.apiKey); err != nil {
			_ = conn.Close()
			if errors.Is(err, context.Canceled) {
				c.mu.Unlock()
				return context.Canceled
			}
			lastErr = err
		} else {
			if c.closed {
				c.mu.Unlock()
				_ = conn.Close()
				return io.ErrClosedPipe
			}
			c.conn = conn
			c.mu.Unlock()

			go c.readLoop(conn)
			return nil
		}
		if attempt < upliftAISocketIOAttempts && upliftAISocketIOReconnectDelay > 0 {
			timer := time.NewTimer(upliftAISocketIOReconnectDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				if errors.Is(ctx.Err(), context.Canceled) {
					c.mu.Unlock()
					return context.Canceled
				}
				c.mu.Unlock()
				return llm.NewAPIConnectionError(fmt.Sprintf("UpliftAI TTS socket.io reconnect failed: %v", ctx.Err()))
			case <-timer.C:
			}
		}
	}
	c.mu.Unlock()
	if lastErr != nil {
		return lastErr
	}
	return llm.NewAPIConnectionError("UpliftAI TTS socket.io dial failed")
}

func (c *upliftAISocketIOClient) readLoop(conn upliftAISocketIOConn) {
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			c.closeConn(conn, nil)
			return
		}
		packet := string(msg)
		if packet == "2" {
			c.writeMu.Lock()
			err := conn.WriteMessage(websocket.TextMessage, []byte("3"))
			c.writeMu.Unlock()
			if err != nil {
				c.closeConn(conn, nil)
				return
			}
			continue
		}
		payload, ok := strings.CutPrefix(packet, "42"+upliftAISocketIONamespace+",")
		if !ok {
			continue
		}
		event, ok := parseUpliftAISocketIOEvent(payload)
		if !ok {
			continue
		}
		c.handleEvent(event)
	}
}

func (c *upliftAISocketIOClient) handleEvent(event upliftAISocketIOEvent) {
	if event.RequestID == "" {
		return
	}
	c.mu.Lock()
	req := c.requests[event.RequestID]
	c.mu.Unlock()
	if req == nil {
		return
	}

	switch event.Type {
	case "audio":
		audio, err := decodeUpliftAIBase64Audio(event.Audio)
		if err != nil {
			return
		}
		if len(audio) > 0 {
			if _, err := req.pw.Write(audio); err != nil {
				c.finishRequest(event.RequestID, err)
				return
			}
			c.resetRequestTimer(event.RequestID)
		}
	case "audio_end", "error":
		c.finishRequest(event.RequestID, nil)
	}
}

func decodeUpliftAIBase64Audio(value string) ([]byte, error) {
	audio, err := base64.StdEncoding.DecodeString(value)
	if err == nil {
		return audio, nil
	}
	clean := make([]byte, 0, len(value))
	for i := 0; i < len(value); i++ {
		b := value[i]
		switch {
		case b >= 'A' && b <= 'Z':
			clean = append(clean, b)
		case b >= 'a' && b <= 'z':
			clean = append(clean, b)
		case b >= '0' && b <= '9':
			clean = append(clean, b)
		case b == '+' || b == '/' || b == '=':
			clean = append(clean, b)
		}
	}
	return base64.StdEncoding.DecodeString(string(clean))
}

func (c *upliftAISocketIOClient) resetRequestTimer(requestID string) {
	c.mu.Lock()
	req := c.requests[requestID]
	c.mu.Unlock()
	if req != nil && req.timer != nil {
		req.timer.Reset(upliftAISocketIOAudioWait)
	}
}

func (c *upliftAISocketIOClient) finishRequest(requestID string, err error) {
	c.mu.Lock()
	req := c.requests[requestID]
	delete(c.requests, requestID)
	c.mu.Unlock()
	if req == nil {
		return
	}
	if req.timer != nil {
		req.timer.Stop()
	}
	if err != nil {
		_ = req.pw.CloseWithError(err)
		return
	}
	_ = req.pw.Close()
}

func (c *upliftAISocketIOClient) closeConn(conn upliftAISocketIOConn, err error) {
	c.mu.Lock()
	if c.conn == conn {
		c.conn = nil
	}
	requests := c.requests
	c.requests = make(map[string]*upliftAISocketIORequest)
	c.mu.Unlock()
	_ = conn.Close()
	for _, req := range requests {
		if req.timer != nil {
			req.timer.Stop()
		}
		if err != nil {
			_ = req.pw.CloseWithError(err)
			continue
		}
		_ = req.pw.Close()
	}
}

func (c *upliftAISocketIOClient) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	conn := c.conn
	c.conn = nil
	requests := c.requests
	c.requests = make(map[string]*upliftAISocketIORequest)
	c.mu.Unlock()

	for _, req := range requests {
		if req.timer != nil {
			req.timer.Stop()
		}
		_ = req.pw.Close()
	}
	if conn != nil {
		return conn.Close()
	}
	return nil
}

func buildUpliftAISocketIOURL(baseURL string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/socket.io/"
	q := u.Query()
	q.Set("EIO", "4")
	q.Set("transport", "websocket")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func upliftAISocketIOConnect(ctx context.Context, conn upliftAISocketIOConn, apiKey string) error {
	if deadline, ok := ctx.Deadline(); ok {
		upliftAISetSocketIOReadDeadline(conn, deadline)
		defer upliftAISetSocketIOReadDeadline(conn, time.Time{})
	}
	namespaceConnected := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, msg, err := conn.ReadMessage()
		if err != nil {
			if namespaceConnected && upliftAIIsTimeoutError(err) {
				upliftAISetSocketIOReadDeadline(conn, time.Time{})
				return nil
			}
			return llm.NewAPIConnectionError(fmt.Sprintf("UpliftAI TTS socket.io connect failed: %v", err))
		}
		packet := string(msg)
		if packet == "2" {
			if err := conn.WriteMessage(websocket.TextMessage, []byte("3")); err != nil {
				if errors.Is(err, context.Canceled) {
					return context.Canceled
				}
				return llm.NewAPIConnectionError(fmt.Sprintf("UpliftAI TTS socket.io ping failed: %v", err))
			}
			continue
		}
		if strings.HasPrefix(packet, "0") {
			payload, _ := json.Marshal(map[string]string{"token": apiKey})
			if err := conn.WriteMessage(websocket.TextMessage, []byte("40"+upliftAISocketIONamespace+","+string(payload))); err != nil {
				if errors.Is(err, context.Canceled) {
					return context.Canceled
				}
				return llm.NewAPIConnectionError(fmt.Sprintf("UpliftAI TTS socket.io auth failed: %v", err))
			}
			continue
		}
		if strings.HasPrefix(packet, "42"+upliftAISocketIONamespace+",") {
			payload := strings.TrimPrefix(packet, "42"+upliftAISocketIONamespace+",")
			if upliftAISocketIOMessageType(payload) == "ready" {
				upliftAISetSocketIOReadDeadline(conn, time.Time{})
				return nil
			}
			continue
		}
		if strings.HasPrefix(packet, "40"+upliftAISocketIONamespace) {
			namespaceConnected = true
			upliftAISetSocketIOReadDeadline(conn, time.Now().Add(upliftAISocketIOReadyWait))
		}
	}
}

func upliftAISetSocketIOReadDeadline(conn upliftAISocketIOConn, deadline time.Time) {
	if deadlineConn, ok := conn.(upliftAISocketIODeadlineConn); ok {
		_ = deadlineConn.SetReadDeadline(deadline)
	}
}

func upliftAIIsTimeoutError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func upliftAISocketIOMessageType(payload string) string {
	var event []json.RawMessage
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return ""
	}
	if len(event) != 2 || string(event[0]) != `"message"` {
		return ""
	}
	var message struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(event[1], &message); err != nil {
		return ""
	}
	return message.Type
}

func writeUpliftAISocketIOSynthesize(conn upliftAISocketIOConn, requestID string, text string, voiceID string, outputFormat string, phraseReplacementConfigID string) error {
	payload := map[string]string{
		"type":         "synthesize",
		"requestId":    requestID,
		"text":         text,
		"voiceId":      voiceID,
		"outputFormat": outputFormat,
	}
	if phraseReplacementConfigID != "" {
		payload["phraseReplacementConfigId"] = phraseReplacementConfigID
	}
	event, err := json.Marshal([]interface{}{"synthesize", payload})
	if err != nil {
		return err
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte("42"+upliftAISocketIONamespace+","+string(event))); err != nil {
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		return llm.NewAPIConnectionError(fmt.Sprintf("UpliftAI TTS socket.io synthesize failed: %v", err))
	}
	return nil
}

func parseUpliftAISocketIOEvent(payload string) (upliftAISocketIOEvent, bool) {
	var event []json.RawMessage
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return upliftAISocketIOEvent{}, false
	}
	if len(event) != 2 || string(event[0]) != `"message"` {
		return upliftAISocketIOEvent{}, false
	}
	var message upliftAISocketIOEvent
	if err := json.Unmarshal(event[1], &message); err != nil {
		return upliftAISocketIOEvent{}, false
	}
	return message, true
}

type upliftAISocketIOBody struct {
	*io.PipeReader
	client    *upliftAISocketIOClient
	requestID string
}

func (b *upliftAISocketIOBody) Close() error {
	if b.client != nil {
		b.client.finishRequest(b.requestID, nil)
	}
	return b.PipeReader.Close()
}

func (s *upliftAITTSChunkedStream) currentOutputFormat() string {
	if s.outputFormat != "" {
		return s.outputFormat
	}
	if s.owner == nil {
		return ""
	}
	_, _, outputFormat, _ := s.owner.requestOptions()
	return outputFormat
}

func (s *upliftAITTSChunkedStream) currentNumChannels() int {
	if s.owner == nil {
		return 1
	}
	numChannels := s.owner.outputNumChannels()
	if numChannels <= 0 {
		return 1
	}
	return numChannels
}

func validateUpliftAIOutputFormat(outputFormat string) error {
	switch {
	case outputFormat == "PCM_22050_16":
		return nil
	case outputFormat == "WAV_22050_16", outputFormat == "WAV_22050_32":
		return nil
	case strings.HasPrefix(outputFormat, "MP3"):
		return nil
	case strings.HasPrefix(outputFormat, "OGG"):
		return nil
	case outputFormat == "ULAW_8000_8":
		return nil
	default:
		return fmt.Errorf("unsupported output format: %s", outputFormat)
	}
}

func (s *upliftAITTSChunkedStream) nextDecodedMP3() (*tts.SynthesizedAudio, error) {
	if s.finalSent {
		return nil, io.EOF
	}
	if !s.started {
		s.started = true
		decoder := codecs.NewMP3AudioStreamDecoder()
		s.decoder = decoder
		if audio, done, err := s.startCompressedDecoder(decoder, "UpliftAI TTS MP3 read failed"); done {
			return audio, err
		}
	}

	frame, err := s.decoder.Next()
	if err != nil {
		if strings.Contains(err.Error(), "decoder closed") {
			if readErr := s.compressedReadError(); readErr != nil && !s.hasAudio {
				return nil, upliftAITTSReadError("UpliftAI TTS MP3 read failed", readErr)
			}
			if s.hasAudio && !s.finalSent {
				s.finalSent = true
				return &tts.SynthesizedAudio{IsFinal: true}, nil
			}
			return nil, io.EOF
		}
		if readErr := s.compressedReadError(); readErr != nil && !s.hasAudio {
			return nil, upliftAITTSReadError("UpliftAI TTS MP3 read failed", readErr)
		}
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("UpliftAI TTS MP3 decode failed: %v", err))
	}
	frame = upliftAINormalizeChannels(frame, s.currentNumChannels())
	if frame.SampleRate != defaultUpliftAISampleRate {
		resampled, err := coreaudio.ResampleAudioFrame(frame, defaultUpliftAISampleRate)
		if err != nil {
			return nil, llm.NewAPIConnectionError(fmt.Sprintf("UpliftAI TTS MP3 resample failed: %v", err))
		}
		frame = resampled
	}
	s.hasAudio = true
	return &tts.SynthesizedAudio{Frame: frame}, nil
}

func (s *upliftAITTSChunkedStream) nextDecodedOGG() (*tts.SynthesizedAudio, error) {
	if s.finalSent {
		return nil, io.EOF
	}
	if !s.started {
		s.started = true
		decoder := codecs.NewOpusAudioStreamDecoder(defaultUpliftAISampleRate, s.currentNumChannels())
		s.decoder = decoder
		if audio, done, err := s.startCompressedDecoder(decoder, "UpliftAI TTS OGG read failed"); done {
			return audio, err
		}
	}

	frame, err := s.decoder.Next()
	if err != nil {
		if strings.Contains(err.Error(), "decoder closed") {
			if readErr := s.compressedReadError(); readErr != nil && !s.hasAudio {
				return nil, upliftAITTSReadError("UpliftAI TTS OGG read failed", readErr)
			}
			if s.hasAudio && !s.finalSent {
				s.finalSent = true
				return &tts.SynthesizedAudio{IsFinal: true}, nil
			}
			return nil, io.EOF
		}
		if readErr := s.compressedReadError(); readErr != nil && !s.hasAudio {
			return nil, upliftAITTSReadError("UpliftAI TTS OGG read failed", readErr)
		}
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("UpliftAI TTS OGG decode failed: %v", err))
	}
	frame = upliftAINormalizeChannels(frame, s.currentNumChannels())
	if frame.SampleRate != defaultUpliftAISampleRate {
		resampled, err := coreaudio.ResampleAudioFrame(frame, defaultUpliftAISampleRate)
		if err != nil {
			return nil, llm.NewAPIConnectionError(fmt.Sprintf("UpliftAI TTS OGG resample failed: %v", err))
		}
		frame = resampled
	}
	s.hasAudio = true
	return &tts.SynthesizedAudio{Frame: frame}, nil
}

func (s *upliftAITTSChunkedStream) startCompressedDecoder(decoder codecs.AudioStreamDecoder, readErrorPrefix string) (*tts.SynthesizedAudio, bool, error) {
	buf := make([]byte, 8192)
	n, err := s.resp.Body.Read(buf)
	if n == 0 {
		if err == io.EOF {
			s.finalSent = true
			_ = decoder.Close()
			return &tts.SynthesizedAudio{IsFinal: true}, true, nil
		}
		if err != nil {
			_ = decoder.Close()
			return nil, true, upliftAITTSReadError(readErrorPrefix, err)
		}
	}
	first := make([]byte, n)
	copy(first, buf[:n])
	go s.streamCompressedResponse(decoder, first, err)
	return nil, false, nil
}

func (s *upliftAITTSChunkedStream) streamCompressedResponse(decoder codecs.AudioStreamDecoder, first []byte, firstErr error) {
	if len(first) > 0 {
		decoder.Push(first)
	}
	if firstErr != nil {
		if firstErr == io.EOF {
			decoder.EndInput()
			return
		}
		s.setCompressedReadError(firstErr)
		_ = decoder.Close()
		return
	}
	buf := make([]byte, 8192)
	for {
		n, err := s.resp.Body.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			decoder.Push(chunk)
		}
		if err == nil {
			continue
		}
		if err == io.EOF {
			decoder.EndInput()
			return
		}
		s.setCompressedReadError(err)
		_ = decoder.Close()
		return
	}
}

func (s *upliftAITTSChunkedStream) setCompressedReadError(err error) {
	s.decodeMu.Lock()
	defer s.decodeMu.Unlock()
	s.decodeReadErr = err
}

func (s *upliftAITTSChunkedStream) compressedReadError() error {
	s.decodeMu.Lock()
	defer s.decodeMu.Unlock()
	return s.decodeReadErr
}

func (s *upliftAITTSChunkedStream) nextDecodedWAV() (*tts.SynthesizedAudio, error) {
	if s.finalSent {
		return nil, io.EOF
	}
	if !s.started {
		s.started = true
		s.wav = &upliftAIWAVStream{r: s.resp.Body}
	}
	frame, done, err := s.wav.nextFrame()
	if err != nil {
		if errors.Is(err, io.EOF) && !s.hasAudio {
			s.finalSent = true
			return &tts.SynthesizedAudio{IsFinal: true}, nil
		}
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("UpliftAI TTS WAV decode failed: %v", err))
	}
	if done {
		if s.hasAudio && !s.finalSent {
			s.finalSent = true
			return &tts.SynthesizedAudio{IsFinal: true}, nil
		}
		s.finalSent = true
		return &tts.SynthesizedAudio{IsFinal: true}, nil
	}
	frame = upliftAINormalizeChannels(frame, s.currentNumChannels())
	if frame.SampleRate != defaultUpliftAISampleRate {
		resampled, err := coreaudio.ResampleAudioFrame(frame, defaultUpliftAISampleRate)
		if err != nil {
			return nil, llm.NewAPIConnectionError(fmt.Sprintf("UpliftAI TTS WAV resample failed: %v", err))
		}
		frame = resampled
	}
	s.hasAudio = true
	if s.pendingFinal {
		s.pendingFinal = false
		s.finalSent = true
		return &tts.SynthesizedAudio{IsFinal: true}, nil
	}
	return &tts.SynthesizedAudio{Frame: frame}, nil
}

type upliftAIWAVStream struct {
	r             io.Reader
	parsed        bool
	done          bool
	sampleRate    uint32
	channels      uint16
	bitsPerSample uint16
	dataRemaining uint32
}

func (w *upliftAIWAVStream) nextFrame() (*model.AudioFrame, bool, error) {
	if w.done {
		return nil, true, nil
	}
	if !w.parsed {
		if err := w.parseHeader(); err != nil {
			return nil, false, err
		}
		w.parsed = true
	}
	if w.dataRemaining == 0 {
		w.resetSegment()
		if err := w.parseHeader(); err != nil {
			if errors.Is(err, io.EOF) {
				w.done = true
				return nil, true, nil
			}
			return nil, false, err
		}
		w.parsed = true
	}
	bytesPerInputSample := int(w.bitsPerSample / 8)
	blockAlign := int(w.channels) * bytesPerInputSample
	if blockAlign <= 0 {
		return nil, false, fmt.Errorf("invalid upliftai wav block alignment")
	}
	readSize := int(w.dataRemaining)
	if readSize > 4096 {
		readSize = 4096
	}
	if readSize > blockAlign {
		readSize -= readSize % blockAlign
	}
	if readSize == 0 || readSize%blockAlign != 0 {
		return nil, false, fmt.Errorf("invalid upliftai wav data size")
	}
	buf := make([]byte, readSize)
	if _, err := io.ReadFull(w.r, buf); err != nil {
		return nil, false, fmt.Errorf("read upliftai wav data: %w", err)
	}
	w.dataRemaining -= uint32(readSize)
	pcm := buf
	bytesPerOutputSample := bytesPerInputSample
	if w.bitsPerSample == 32 {
		pcm16 := make([]byte, len(buf)/2)
		for in, out := 0, 0; in+4 <= len(buf); in, out = in+4, out+2 {
			sample := int32(binary.LittleEndian.Uint32(buf[in : in+4]))
			binary.LittleEndian.PutUint16(pcm16[out:out+2], uint16(int16(sample>>16)))
		}
		pcm = pcm16
		bytesPerOutputSample = 2
	}
	return &model.AudioFrame{
		Data:              pcm,
		SampleRate:        w.sampleRate,
		NumChannels:       uint32(w.channels),
		SamplesPerChannel: uint32(len(pcm) / int(w.channels) / bytesPerOutputSample),
	}, false, nil
}

func (w *upliftAIWAVStream) parseHeader() error {
	header := make([]byte, 12)
	if _, err := io.ReadFull(w.r, header); err != nil {
		return fmt.Errorf("read upliftai wav header: %w", err)
	}
	if string(header[:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
		return fmt.Errorf("invalid upliftai wav data")
	}
	for {
		chunkHeader := make([]byte, 8)
		if _, err := io.ReadFull(w.r, chunkHeader); err != nil {
			return fmt.Errorf("read upliftai wav chunk header: %w", err)
		}
		chunkID := string(chunkHeader[:4])
		chunkSize := binary.LittleEndian.Uint32(chunkHeader[4:8])
		switch chunkID {
		case "fmt ":
			if chunkSize < 16 {
				return fmt.Errorf("invalid upliftai wav fmt chunk")
			}
			fmtChunk := make([]byte, chunkSize)
			if _, err := io.ReadFull(w.r, fmtChunk); err != nil {
				return fmt.Errorf("read upliftai wav fmt chunk: %w", err)
			}
			audioFormat := binary.LittleEndian.Uint16(fmtChunk[0:2])
			w.channels = binary.LittleEndian.Uint16(fmtChunk[2:4])
			w.sampleRate = binary.LittleEndian.Uint32(fmtChunk[4:8])
			w.bitsPerSample = binary.LittleEndian.Uint16(fmtChunk[14:16])
			if audioFormat != 1 || (w.bitsPerSample != 16 && w.bitsPerSample != 32) {
				return fmt.Errorf("unsupported upliftai wav format: audio_format=%d bits_per_sample=%d", audioFormat, w.bitsPerSample)
			}
			if chunkSize%2 == 1 {
				if err := discardUpliftAIWAVBytes(w.r, 1); err != nil {
					return err
				}
			}
		case "data":
			if w.sampleRate == 0 || w.channels == 0 || w.bitsPerSample == 0 {
				return fmt.Errorf("missing upliftai wav format metadata")
			}
			w.dataRemaining = chunkSize
			return nil
		default:
			skip := int64(chunkSize)
			if chunkSize%2 == 1 {
				skip++
			}
			if err := discardUpliftAIWAVBytes(w.r, skip); err != nil {
				return err
			}
		}
	}
}

func (w *upliftAIWAVStream) resetSegment() {
	w.parsed = false
	w.sampleRate = 0
	w.channels = 0
	w.bitsPerSample = 0
	w.dataRemaining = 0
}

func discardUpliftAIWAVBytes(r io.Reader, n int64) error {
	if n <= 0 {
		return nil
	}
	if _, err := io.CopyN(io.Discard, r, n); err != nil {
		return fmt.Errorf("discard upliftai wav chunk: %w", err)
	}
	return nil
}

func (s *upliftAITTSChunkedStream) nextRawPCM() (*tts.SynthesizedAudio, error) {
	if s.pendingFinal {
		s.pendingFinal = false
		s.finalSent = true
		return &tts.SynthesizedAudio{IsFinal: true}, nil
	}
	if len(s.pcmFrames) > 0 {
		frame := s.pcmFrames[0]
		s.pcmFrames = s.pcmFrames[1:]
		return &tts.SynthesizedAudio{Frame: frame}, nil
	}
	if s.pendingReadErr != nil {
		err := s.pendingReadErr
		s.pendingReadErr = nil
		s.finalSent = true
		return nil, upliftAITTSReadError("UpliftAI TTS stream read failed", err)
	}
	if s.pcm == nil {
		numChannels := uint32(s.currentNumChannels())
		s.pcm = coreaudio.NewAudioByteStreamWithOptions(
			defaultUpliftAISampleRate,
			numChannels,
			defaultUpliftAISampleRate*200/1000,
			coreaudio.AudioByteStreamOptions{Progressive: true},
		)
	}
	buf := make([]byte, 4096)
	for {
		n, err := s.resp.Body.Read(buf)
		if n > 0 {
			s.pcmFrames = append(s.pcmFrames, s.pcm.Push(buf[:n])...)
		}
		if err != nil {
			if err == io.EOF {
				s.pcmFrames = append(s.pcmFrames, s.pcm.Flush()...)
				if len(s.pcmFrames) > 0 {
					frame := s.pcmFrames[0]
					s.pcmFrames = s.pcmFrames[1:]
					s.pendingFinal = true
					return &tts.SynthesizedAudio{Frame: frame}, nil
				}
				if !s.finalSent {
					s.finalSent = true
					return &tts.SynthesizedAudio{IsFinal: true}, nil
				}
				return nil, io.EOF
			}
			s.pcmFrames = append(s.pcmFrames, s.pcm.Flush()...)
			if len(s.pcmFrames) > 0 {
				frame := s.pcmFrames[0]
				s.pcmFrames = s.pcmFrames[1:]
				s.pendingReadErr = err
				return &tts.SynthesizedAudio{Frame: frame}, nil
			}
			return nil, upliftAITTSReadError("UpliftAI TTS stream read failed", err)
		}
		if len(s.pcmFrames) > 0 {
			frame := s.pcmFrames[0]
			s.pcmFrames = s.pcmFrames[1:]
			return &tts.SynthesizedAudio{Frame: frame}, nil
		}
	}
}

func (s *upliftAITTSChunkedStream) nextBufferedULaw() (*tts.SynthesizedAudio, error) {
	if s.pendingFinal {
		s.pendingFinal = false
		s.finalSent = true
		return &tts.SynthesizedAudio{IsFinal: true}, nil
	}
	if len(s.pcmFrames) > 0 {
		frame := s.pcmFrames[0]
		s.pcmFrames = s.pcmFrames[1:]
		return &tts.SynthesizedAudio{Frame: frame}, nil
	}
	if s.pendingReadErr != nil {
		err := s.pendingReadErr
		s.pendingReadErr = nil
		s.finalSent = true
		return nil, upliftAITTSReadError("UpliftAI TTS mu-law read failed", err)
	}
	if s.pcm == nil {
		numChannels := uint32(s.currentNumChannels())
		s.pcm = coreaudio.NewAudioByteStreamWithOptions(
			8000,
			numChannels,
			8000*200/1000,
			coreaudio.AudioByteStreamOptions{Progressive: true},
		)
	}
	buf := make([]byte, 4096)
	for {
		n, err := s.resp.Body.Read(buf)
		if n > 0 {
			decoded := decodeUpliftAIMuLaw(buf[:n])
			decoded = upliftAIExpandPCM16Channels(decoded, s.currentNumChannels())
			s.pcmFrames = append(s.pcmFrames, s.pcm.Push(decoded)...)
			if len(s.pcmFrames) > 0 {
				frame := s.pcmFrames[0]
				s.pcmFrames = s.pcmFrames[1:]
				return &tts.SynthesizedAudio{Frame: frame}, nil
			}
		}
		if err != nil {
			if err == io.EOF {
				s.pcmFrames = append(s.pcmFrames, s.pcm.Flush()...)
				if len(s.pcmFrames) > 0 {
					frame := s.pcmFrames[0]
					s.pcmFrames = s.pcmFrames[1:]
					s.pendingFinal = true
					return &tts.SynthesizedAudio{Frame: frame}, nil
				}
				if !s.finalSent {
					s.finalSent = true
					return &tts.SynthesizedAudio{IsFinal: true}, nil
				}
				return nil, io.EOF
			}
			s.pcmFrames = append(s.pcmFrames, s.pcm.Flush()...)
			if len(s.pcmFrames) > 0 {
				frame := s.pcmFrames[0]
				s.pcmFrames = s.pcmFrames[1:]
				s.pendingReadErr = err
				return &tts.SynthesizedAudio{Frame: frame}, nil
			}
			return nil, upliftAITTSReadError("UpliftAI TTS mu-law read failed", err)
		}
	}
}

func upliftAITTSReadError(prefix string, err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return llm.NewAPITimeoutError(fmt.Sprintf("%s: %v", prefix, err))
	}
	return llm.NewAPIConnectionError(fmt.Sprintf("%s: %v", prefix, err))
}

func decodeUpliftAIMuLaw(data []byte) []byte {
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

func upliftAIExpandPCM16Channels(mono []byte, numChannels int) []byte {
	if numChannels <= 1 || len(mono) == 0 {
		return mono
	}
	if len(mono)%2 != 0 {
		return mono
	}
	expanded := make([]byte, len(mono)*numChannels)
	out := 0
	for in := 0; in+1 < len(mono); in += 2 {
		for ch := 0; ch < numChannels; ch++ {
			expanded[out] = mono[in]
			expanded[out+1] = mono[in+1]
			out += 2
		}
	}
	return expanded
}

func upliftAINormalizeChannels(frame *model.AudioFrame, numChannels int) *model.AudioFrame {
	if numChannels > 1 {
		return frame
	}
	return upliftAIDownmixToMono(frame)
}

func upliftAIDownmixToMono(frame *model.AudioFrame) *model.AudioFrame {
	if frame == nil || frame.NumChannels <= 1 {
		return frame
	}
	channels := int(frame.NumChannels)
	sampleCount := len(frame.Data) / (2 * channels)
	data := make([]byte, sampleCount*2)
	for i := 0; i < sampleCount; i++ {
		sum := 0
		for ch := 0; ch < channels; ch++ {
			offset := (i*channels + ch) * 2
			sum += int(int16(binary.LittleEndian.Uint16(frame.Data[offset:])))
		}
		binary.LittleEndian.PutUint16(data[i*2:], uint16(int16(sum/channels)))
	}
	clone := *frame
	clone.Data = data
	clone.NumChannels = 1
	clone.SamplesPerChannel = uint32(sampleCount)
	return &clone
}

func (s *upliftAITTSChunkedStream) Close() error {
	s.once.Do(func() {
		s.closed = true
		if s.cancel != nil {
			s.cancel()
		}
		if s.owner != nil {
			s.owner.unregisterStream(s)
		}
		if s.resp != nil && s.resp.Body != nil {
			s.err = s.resp.Body.Close()
		}
		if s.decoder != nil {
			_ = s.decoder.Close()
		}
	})
	return s.err
}
