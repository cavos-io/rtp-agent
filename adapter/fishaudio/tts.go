package fishaudio

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/library/tokenize"
	"github.com/gorilla/websocket"
	"github.com/vmihailenco/msgpack/v5"
)

const (
	defaultFishAudioModel       = "s2-pro"
	defaultFishAudioVoiceID     = "933563129e564b19a115bedd57b7406a"
	defaultFishAudioBaseURL     = "https://api.fish.audio"
	defaultFishAudioFormat      = "wav"
	defaultFishAudioLatencyMode = "balanced"
	defaultFishAudioChunkLength = 100
	fishAudioTTSUserAgent       = "livekit-plugins-fishaudio/go"
	fishAudioReferenceAPIKeyEnv = "FISH_API_KEY"
	fishAudioPrimaryAPIKeyEnv   = "FISHAUDIO_API_KEY"
	fishAudioFallbackAPIKeyEnv  = "FISH_AUDIO_API_KEY"
)

type TTS struct {
	apiKey       string
	baseURL      string
	model        string
	voice        string
	outputFormat string
	sampleRate   int
	latencyMode  string
	chunkLength  int
	mu           sync.Mutex
	streams      map[*fishAudioTTSSynthesizeStream]struct{}
	closed       bool
}

type TTSOption func(*TTS)

func WithFishAudioTTSBaseURL(baseURL string) TTSOption {
	return func(t *TTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithFishAudioTTSModel(model string) TTSOption {
	return func(t *TTS) {
		if model != "" {
			t.model = model
		}
	}
}

func WithFishAudioTTSVoice(voice string) TTSOption {
	return func(t *TTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithFishAudioTTSOutputFormat(outputFormat string) TTSOption {
	return func(t *TTS) {
		if outputFormat != "" {
			t.outputFormat = outputFormat
			t.sampleRate = defaultFishAudioSampleRate(outputFormat)
		}
	}
}

func WithFishAudioTTSSampleRate(sampleRate int) TTSOption {
	return func(t *TTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithFishAudioTTSLatencyMode(latencyMode string) TTSOption {
	return func(t *TTS) {
		if latencyMode != "" {
			t.latencyMode = latencyMode
		}
	}
}

func WithFishAudioTTSChunkLength(chunkLength int) TTSOption {
	return func(t *TTS) {
		if chunkLength > 0 {
			t.chunkLength = chunkLength
		}
	}
}

func NewTTS(apiKey string, voice string, opts ...TTSOption) *TTS {
	if apiKey == "" {
		apiKey = os.Getenv(fishAudioReferenceAPIKeyEnv)
	}
	if apiKey == "" {
		apiKey = os.Getenv(fishAudioPrimaryAPIKeyEnv)
	}
	if apiKey == "" {
		apiKey = os.Getenv(fishAudioFallbackAPIKeyEnv)
	}
	provider := &TTS{
		apiKey:       apiKey,
		baseURL:      defaultFishAudioBaseURL,
		model:        defaultFishAudioModel,
		voice:        voice,
		outputFormat: defaultFishAudioFormat,
		sampleRate:   defaultFishAudioSampleRate(defaultFishAudioFormat),
		latencyMode:  defaultFishAudioLatencyMode,
		chunkLength:  defaultFishAudioChunkLength,
		streams:      make(map[*fishAudioTTSSynthesizeStream]struct{}),
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultFishAudioVoiceID
	}
	return provider
}

func defaultFishAudioSampleRate(outputFormat string) int {
	switch outputFormat {
	case "opus":
		return 48000
	case "mp3":
		return 32000
	default:
		return 24000
	}
}

func (t *TTS) Label() string { return "fishaudio.TTS" }
func (t *TTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *TTS) SampleRate() int  { return t.sampleRate }
func (t *TTS) NumChannels() int { return 1 }
func (t *TTS) Model() string    { return t.model }
func (t *TTS) Provider() string { return "FishAudio" }

func (t *TTS) UpdateOptions(opts ...TTSOption) error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	before := t.chunkLength
	for _, opt := range opts {
		opt(t)
	}
	if t.chunkLength < 100 || t.chunkLength > 300 {
		t.chunkLength = before
		return fmt.Errorf("chunk_length must be between 100 and 300")
	}
	return nil
}

func (t *TTS) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	t.closed = true
	streams := make([]*fishAudioTTSSynthesizeStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.streams = make(map[*fishAudioTTSSynthesizeStream]struct{})
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

func (t *TTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if err := validateFishAudioAPIKey(t.apiKey); err != nil {
		return nil, err
	}
	opts := *t

	return &fishaudioTTSChunkedStream{
		ctx:        ctx,
		text:       text,
		opts:       opts,
		sampleRate: t.sampleRate,
		format:     t.outputFormat,
	}, nil
}

func buildFishAudioTTSRequest(ctx context.Context, t *TTS, text string) (*http.Request, error) {
	packedBody, err := msgpack.Marshal(fishAudioTTSRequestPayload(t, text))
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+"/v1/tts", bytes.NewBuffer(packedBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("Content-Type", "application/msgpack")
	req.Header.Set("model", t.model)
	return req, nil
}

func validateFishAudioAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("fish audio API key is required, either as argument or set FISH_API_KEY environment variable")
	}
	return nil
}

func fishAudioTTSRequestPayload(t *TTS, text string) map[string]interface{} {
	return map[string]interface{}{
		"text":         text,
		"chunk_length": t.chunkLength,
		"format":       t.outputFormat,
		"sample_rate":  t.sampleRate,
		"mp3_bitrate":  64,
		"opus_bitrate": 64000,
		"references":   []interface{}{},
		"reference_id": t.voice,
		"normalize":    true,
		"latency":      t.latencyMode,
		"prosody":      nil,
		"top_p":        0.7,
		"temperature":  0.7,
	}
}

func buildFishAudioTTSWebsocketURL(t *TTS) string {
	baseURL := strings.TrimRight(t.baseURL, "/")
	if strings.HasPrefix(baseURL, "http://") || strings.HasPrefix(baseURL, "https://") {
		baseURL = strings.Replace(baseURL, "http", "ws", 1)
	}
	return baseURL + "/v1/tts/live"
}

func buildFishAudioTTSWebsocketHeaders(t *TTS) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+t.apiKey)
	headers.Set("User-Agent", fishAudioTTSUserAgent)
	headers.Set("model", t.model)
	return headers
}

func buildFishAudioTTSStartMessage(t *TTS) ([]byte, error) {
	return msgpack.Marshal(map[string]interface{}{
		"event":   "start",
		"request": fishAudioTTSRequestPayload(t, ""),
	})
}

func buildFishAudioTTSTextMessage(text string) ([]byte, error) {
	return msgpack.Marshal(map[string]interface{}{
		"event": "text",
		"text":  text + " ",
	})
}

func buildFishAudioTTSSimpleEvent(event string) ([]byte, error) {
	return msgpack.Marshal(map[string]interface{}{"event": event})
}

func (t *TTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if err := validateFishAudioAPIKey(t.apiKey); err != nil {
		return nil, err
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildFishAudioTTSWebsocketURL(t), buildFishAudioTTSWebsocketHeaders(t))
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
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("failed to dial fishaudio tts websocket: %v", err))
	}
	if t.isClosed() {
		conn.Close()
		return nil, io.ErrClosedPipe
	}
	startMessage, err := buildFishAudioTTSStartMessage(t)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, startMessage); err != nil {
		conn.Close()
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &fishAudioTTSSynthesizeStream{
		owner:      t,
		conn:       conn,
		ctx:        streamCtx,
		cancel:     cancel,
		sampleRate: t.sampleRate,
		format:     t.outputFormat,
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

func (t *TTS) registerStream(stream *fishAudioTTSSynthesizeStream) bool {
	if t == nil || stream == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	if t.streams == nil {
		t.streams = make(map[*fishAudioTTSSynthesizeStream]struct{})
	}
	t.streams[stream] = struct{}{}
	stream.owner = t
	return true
}

func (t *TTS) unregisterStream(stream *fishAudioTTSSynthesizeStream) {
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

type fishaudioTTSChunkedStream struct {
	resp         *http.Response
	ctx          context.Context
	text         string
	opts         TTS
	sampleRate   int
	format       string
	requested    bool
	pendingFinal bool
	finalSent    bool
}

func (s *fishaudioTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.finalSent {
		return nil, io.EOF
	}
	if err := s.ensureResponse(); err != nil {
		return nil, err
	}
	if s.resp == nil || s.resp.Body == nil {
		return nil, io.EOF
	}
	if s.pendingFinal {
		s.pendingFinal = false
		return s.emitFinal()
	}
	if s.format == "wav" {
		data, err := io.ReadAll(s.resp.Body)
		if err != nil {
			return nil, fishAudioTTSReadBodyError(err)
		}
		if len(data) == 0 {
			return s.emitFinal()
		}
		audio, err := fishAudioDecodeTTSFrame(data, s.sampleRate, s.format)
		if err != nil {
			return nil, fishAudioTTSConnectionError("Fish Audio TTS audio decode failed", err)
		}
		return audio, nil
	}

	buf := make([]byte, 4096)
	n, err := s.resp.Body.Read(buf)
	if n > 0 {
		if err == io.EOF {
			s.pendingFinal = true
		}
		audio, decodeErr := fishAudioDecodeTTSFrame(buf[:n], s.sampleRate, s.format)
		if decodeErr != nil {
			return nil, fishAudioTTSConnectionError("Fish Audio TTS audio decode failed", decodeErr)
		}
		return audio, nil
	}
	if err != nil {
		if err == io.EOF {
			return s.emitFinal()
		}
		return nil, fishAudioTTSReadBodyError(err)
	}
	audio, err := fishAudioDecodeTTSFrame(buf[:n], s.sampleRate, s.format)
	if err != nil {
		return nil, fishAudioTTSConnectionError("Fish Audio TTS audio decode failed", err)
	}
	return audio, nil
}

func (s *fishaudioTTSChunkedStream) ensureResponse() error {
	if s.resp != nil || s.requested || s.text == "" {
		return nil
	}
	s.requested = true
	req, err := buildFishAudioTTSRequest(s.ctx, &s.opts, s.text)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return llm.NewAPITimeoutError(err.Error())
		}
		var timeoutErr interface{ Timeout() bool }
		if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
			return llm.NewAPITimeoutError(err.Error())
		}
		return llm.NewAPIConnectionError(err.Error())
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return llm.NewAPIStatusError("FishAudio TTS request failed", resp.StatusCode, "", string(respBody))
	}
	s.resp = resp
	return nil
}

func (s *fishaudioTTSChunkedStream) emitFinal() (*tts.SynthesizedAudio, error) {
	if s.finalSent {
		return nil, io.EOF
	}
	s.finalSent = true
	return &tts.SynthesizedAudio{IsFinal: true}, nil
}

func (s *fishaudioTTSChunkedStream) Close() error {
	if s.resp == nil || s.resp.Body == nil {
		return nil
	}
	s.finalSent = true
	body := s.resp.Body
	s.resp = nil
	return body.Close()
}

type fishAudioTTSSynthesizeStream struct {
	owner       *TTS
	conn        *websocket.Conn
	ctx         context.Context
	cancel      context.CancelFunc
	sampleRate  int
	format      string
	events      chan *tts.SynthesizedAudio
	errCh       chan error
	mu          sync.Mutex
	closed      bool
	inputEnded  bool
	pendingText string

	writeMessage func(int, []byte) error
	closeConn    func() error
}

func (s *fishAudioTTSSynthesizeStream) PushText(text string) error {
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

func (s *fishAudioTTSSynthesizeStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.inputEnded {
		return io.ErrClosedPipe
	}
	return s.flushPendingTextLocked()
}

func (s *fishAudioTTSSynthesizeStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	return s.endInputLocked()
}

func (s *fishAudioTTSSynthesizeStream) flushPendingTextLocked() error {
	if s.pendingText == "" {
		return nil
	}
	text := strings.Join(tokenize.NewBasicSentenceTokenizer().Tokenize(s.pendingText, ""), " ")
	s.pendingText = ""
	if err := s.sendSentenceLocked(text); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

func (s *fishAudioTTSSynthesizeStream) sendCompleteSentencesLocked() error {
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

func (s *fishAudioTTSSynthesizeStream) sendSentenceLocked(text string) error {
	if text == "" {
		return nil
	}
	message, err := buildFishAudioTTSTextMessage(text)
	if err != nil {
		return err
	}
	if err := s.writeMessageData(websocket.BinaryMessage, message); err != nil {
		return err
	}
	message, err = buildFishAudioTTSSimpleEvent("flush")
	if err != nil {
		return err
	}
	return s.writeMessageData(websocket.BinaryMessage, message)
}

func (s *fishAudioTTSSynthesizeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	_ = s.endInputLocked()
	if s.conn != nil {
		_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	}
	err := s.closeConnection()
	if s.owner != nil {
		s.owner.unregisterStream(s)
	}
	return err
}

func (s *fishAudioTTSSynthesizeStream) endInputLocked() error {
	if s.inputEnded {
		return nil
	}
	if err := s.flushPendingTextLocked(); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	stopMessage, err := buildFishAudioTTSSimpleEvent("stop")
	if err != nil {
		return err
	}
	if err := s.writeMessageData(websocket.BinaryMessage, stopMessage); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	s.inputEnded = true
	return nil
}

func (s *fishAudioTTSSynthesizeStream) writeMessageData(messageType int, data []byte) error {
	if s.writeMessage != nil {
		return s.writeMessage(messageType, data)
	}
	return s.writeWebsocketMessage(messageType, data)
}

func (s *fishAudioTTSSynthesizeStream) writeWebsocketMessage(messageType int, data []byte) error {
	return s.conn.WriteMessage(messageType, data)
}

func (s *fishAudioTTSSynthesizeStream) closeConnection() error {
	if s.closeConn != nil {
		return s.closeConn()
	}
	return s.closeWebsocketConn()
}

func (s *fishAudioTTSSynthesizeStream) closeWebsocketConn() error {
	if s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

func (s *fishAudioTTSSynthesizeStream) closeAfterWriteFailureLocked() {
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

func (s *fishAudioTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *fishAudioTTSSynthesizeStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *fishAudioTTSSynthesizeStream) readLoop() {
	defer close(s.events)
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			if !s.isClosed() {
				s.errCh <- fishAudioTTSReadError(err)
			}
			return
		}
		if msgType != websocket.BinaryMessage {
			continue
		}
		audio, done, err := fishAudioTTSAudioFromStreamMessage(payload, s.sampleRate, s.format)
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

func fishAudioTTSReadError(err error) error {
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return llm.NewAPIStatusError("Fish Audio websocket connection closed unexpectedly", closeErr.Code, "", err.Error())
	}
	return llm.NewAPIConnectionError(fmt.Sprintf("Fish Audio websocket receive failed: %v", err))
}

func fishAudioTTSAudioFromStreamMessage(payload []byte, sampleRate int, format string) (*tts.SynthesizedAudio, bool, error) {
	var message map[string]interface{}
	if err := msgpack.Unmarshal(payload, &message); err != nil {
		return nil, false, fishAudioTTSConnectionError("Fish Audio websocket payload decode failed", err)
	}
	event, _ := message["event"].(string)
	switch event {
	case "audio":
		audio, ok := fishAudioBytes(message["audio"])
		if !ok || len(audio) == 0 {
			return nil, false, nil
		}
		decoded, err := fishAudioDecodeTTSFrame(audio, sampleRate, format)
		if err != nil {
			return nil, false, fishAudioTTSConnectionError("Fish Audio websocket audio decode failed", err)
		}
		return decoded, false, nil
	case "finish":
		if reason, _ := message["reason"].(string); reason == "error" {
			return nil, false, llm.NewAPIStatusError("Fish Audio TTS reported an error", -1, "", fmt.Sprint(message))
		}
		return &tts.SynthesizedAudio{IsFinal: true}, true, nil
	default:
		return nil, false, nil
	}
}

func fishAudioTTSConnectionError(message string, err error) *llm.APIConnectionError {
	if err == nil {
		return llm.NewAPIConnectionError(message)
	}
	return llm.NewAPIConnectionError(fmt.Sprintf("%s: %v", message, err))
}

func fishAudioTTSReadBodyError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return llm.NewAPITimeoutError(err.Error())
	}
	var timeoutErr interface{ Timeout() bool }
	if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
		return llm.NewAPITimeoutError(err.Error())
	}
	return fishAudioTTSConnectionError("Fish Audio TTS stream read failed", err)
}

func fishAudioBytes(value interface{}) ([]byte, bool) {
	switch v := value.(type) {
	case []byte:
		return v, true
	case string:
		return []byte(v), true
	default:
		return nil, false
	}
}

func fishAudioTTSAudioFrame(audio []byte, sampleRate int) *tts.SynthesizedAudio {
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              bytes.Clone(audio),
			SampleRate:        uint32(sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(len(audio) / 2),
		},
	}
}

func fishAudioDecodeTTSFrame(audio []byte, sampleRate int, format string) (*tts.SynthesizedAudio, error) {
	if format == "wav" {
		frame, err := decodeFishAudioWAVPCM16(audio)
		if err != nil {
			return nil, err
		}
		return &tts.SynthesizedAudio{Frame: frame}, nil
	}
	return fishAudioTTSAudioFrame(audio, sampleRate), nil
}

func decodeFishAudioWAVPCM16(data []byte) (*model.AudioFrame, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("invalid fishaudio wav data")
	}
	offset := 12
	var sampleRate uint32
	var channels uint16
	var bitsPerSample uint16
	var pcm []byte
	for offset+8 <= len(data) {
		chunkID := string(data[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		offset += 8
		if chunkSize < 0 || offset+chunkSize > len(data) {
			return nil, fmt.Errorf("invalid fishaudio wav chunk size")
		}
		switch chunkID {
		case "fmt ":
			if chunkSize < 16 {
				return nil, fmt.Errorf("invalid fishaudio wav fmt chunk")
			}
			audioFormat := binary.LittleEndian.Uint16(data[offset : offset+2])
			channels = binary.LittleEndian.Uint16(data[offset+2 : offset+4])
			sampleRate = binary.LittleEndian.Uint32(data[offset+4 : offset+8])
			bitsPerSample = binary.LittleEndian.Uint16(data[offset+14 : offset+16])
			if audioFormat != 1 || bitsPerSample != 16 {
				return nil, fmt.Errorf("unsupported fishaudio wav format: audio_format=%d bits_per_sample=%d", audioFormat, bitsPerSample)
			}
		case "data":
			pcm = bytes.Clone(data[offset : offset+chunkSize])
		}
		offset += chunkSize
		if chunkSize%2 == 1 {
			offset++
		}
	}
	if sampleRate == 0 || channels == 0 || bitsPerSample == 0 {
		return nil, fmt.Errorf("missing fishaudio wav format metadata")
	}
	if pcm == nil {
		return nil, fmt.Errorf("missing fishaudio wav data chunk")
	}
	return &model.AudioFrame{
		Data:              pcm,
		SampleRate:        sampleRate,
		NumChannels:       uint32(channels),
		SamplesPerChannel: uint32(len(pcm) / int(channels) / 2),
	}, nil
}

// Deprecated: use TTS.
type FishAudioTTS = TTS

// Deprecated: use TTSOption.
type FishAudioTTSOption = TTSOption

// Deprecated: use NewTTS.
func NewFishAudioTTS(apiKey string, voice string, opts ...TTSOption) *TTS {
	return NewTTS(apiKey, voice, opts...)
}
