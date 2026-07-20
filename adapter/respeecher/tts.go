package respeecher

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
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

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	cavosmath "github.com/cavos-io/rtp-agent/library/math"
	"github.com/cavos-io/rtp-agent/library/tokenize"
	"github.com/gorilla/websocket"
)

const (
	respeecherAPIVersion      = "v0.4.0"
	defaultRespeecherBaseURL  = "https://api.respeecher.com/v1"
	defaultRespeecherModel    = "/public/tts/en-rt"
	defaultRespeecherEncoding = "pcm_s16le"
	defaultRespeecherRate     = 24000
)

var defaultRespeecherVoices = map[string]string{
	"/public/tts/en-rt": "samantha",
	"/public/tts/ua-rt": "olesia-conversation",
}

type RespeecherTTS struct {
	mu             sync.Mutex
	streams        map[*respeecherTTSSynthesizeStream]struct{}
	apiKey         string
	baseURL        string
	model          string
	voiceID        string
	encoding       string
	sampleRate     int
	samplingParams map[string]any
	closed         bool
}

type RespeecherTTSOption func(*RespeecherTTS)

func WithRespeecherTTSBaseURL(baseURL string) RespeecherTTSOption {
	return func(t *RespeecherTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithRespeecherTTSModel(model string) RespeecherTTSOption {
	return func(t *RespeecherTTS) {
		if model != "" {
			t.model = model
			if voice := defaultRespeecherVoices[model]; voice != "" {
				t.voiceID = voice
			}
		}
	}
}

func WithRespeecherTTSVoice(voiceID string) RespeecherTTSOption {
	return func(t *RespeecherTTS) {
		if voiceID != "" {
			t.voiceID = voiceID
		}
	}
}

func WithRespeecherTTSSampleRate(sampleRate int) RespeecherTTSOption {
	return func(t *RespeecherTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithRespeecherTTSSamplingParams(params map[string]any) RespeecherTTSOption {
	return func(t *RespeecherTTS) {
		t.samplingParams = params
	}
}

func NewRespeecherTTS(apiKey string, voiceID string, opts ...RespeecherTTSOption) *RespeecherTTS {
	if apiKey == "" {
		apiKey = os.Getenv("RESPEECHER_API_KEY")
	}
	provider := &RespeecherTTS{
		streams:    make(map[*respeecherTTSSynthesizeStream]struct{}),
		apiKey:     apiKey,
		baseURL:    defaultRespeecherBaseURL,
		model:      defaultRespeecherModel,
		voiceID:    voiceID,
		encoding:   defaultRespeecherEncoding,
		sampleRate: defaultRespeecherRate,
	}
	if provider.voiceID == "" {
		provider.voiceID = defaultRespeecherVoices[provider.model]
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voiceID == "" {
		provider.voiceID = defaultRespeecherVoices[provider.model]
	}
	return provider
}

func (t *RespeecherTTS) Label() string { return "respeecher.TTS" }
func (t *RespeecherTTS) Model() string { return t.model }
func (t *RespeecherTTS) Provider() string {
	return "Respeecher"
}

func (t *RespeecherTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *RespeecherTTS) SampleRate() int  { return t.sampleRate }
func (t *RespeecherTTS) NumChannels() int { return 1 }

func (t *RespeecherTTS) Close() error {
	t.mu.Lock()
	t.closed = true
	streams := make([]*respeecherTTSSynthesizeStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.streams = make(map[*respeecherTTSSynthesizeStream]struct{})
	t.mu.Unlock()

	var closeErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (t *RespeecherTTS) isClosed() bool {
	if t == nil {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

func (t *RespeecherTTS) registerStream(stream *respeecherTTSSynthesizeStream) bool {
	if t == nil || stream == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	if t.streams == nil {
		t.streams = make(map[*respeecherTTSSynthesizeStream]struct{})
	}
	t.streams[stream] = struct{}{}
	stream.provider = t
	return true
}

func (t *RespeecherTTS) unregisterStream(stream *respeecherTTSSynthesizeStream) {
	if t == nil || stream == nil {
		return
	}
	t.mu.Lock()
	delete(t.streams, stream)
	t.mu.Unlock()
}

func (t *RespeecherTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if err := validateRespeecherAPIKey(t.apiKey); err != nil {
		return nil, err
	}

	return &respeecherTTSChunkedStream{
		ctx:        ctx,
		provider:   t,
		text:       text,
		sampleRate: t.sampleRate,
	}, nil
}

func buildRespeecherTTSRequest(ctx context.Context, t *RespeecherTTS, text string) (*http.Request, error) {
	voice := map[string]interface{}{"id": t.voiceID}
	if len(t.samplingParams) > 0 {
		voice["sampling_params"] = t.samplingParams
	}
	reqBody := map[string]interface{}{
		"transcript": text,
		"voice":      voice,
		"output_format": map[string]interface{}{
			"sample_rate": t.sampleRate,
			"encoding":    t.encoding,
		},
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+t.model+"/tts/bytes", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", t.apiKey)
	req.Header.Set("LiveKit-Plugin-Respeecher-Version", respeecherAPIVersion)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (t *RespeecherTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if err := validateRespeecherAPIKey(t.apiKey); err != nil {
		return nil, err
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildRespeecherTTSWebsocketURL(t).String(), nil)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("failed to dial respeecher tts websocket: %v", err))
	}
	if t.isClosed() {
		conn.Close()
		return nil, io.ErrClosedPipe
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &respeecherTTSSynthesizeStream{
		conn:      conn,
		ctx:       streamCtx,
		cancel:    cancel,
		provider:  t,
		contextID: cavosmath.ShortUUID(""),
		events:    make(chan *tts.SynthesizedAudio, 100),
		errCh:     make(chan error, 1),
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

func validateRespeecherAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("RESPEECHER_API_KEY must be set")
	}
	return nil
}

type respeecherTTSChunkedStream struct {
	ctx        context.Context
	provider   *RespeecherTTS
	text       string
	resp       *http.Response
	sampleRate int
	decoded    bool
	emitted    bool
	audio      *model.AudioFrame
	closed     bool
	finalSent  bool
}

func (s *respeecherTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.closed || s.finalSent {
		return nil, io.EOF
	}
	if err := s.ensureResponse(); err != nil {
		return nil, err
	}
	if !s.decoded {
		s.decoded = true
		data, err := io.ReadAll(s.resp.Body)
		if err != nil {
			return nil, respeecherTTSHTTPReadError(err)
		}
		if len(data) > 0 {
			frame, err := decodeRespeecherWAVPCM16(data)
			if err != nil {
				return nil, llm.NewAPIConnectionError(fmt.Sprintf("Respeecher TTS response decode failed: %v", err))
			}
			s.audio = frame
		}
	}
	if s.audio != nil && !s.emitted {
		s.emitted = true
		return &tts.SynthesizedAudio{
			Frame: s.audio,
		}, nil
	}
	return s.emitFinal()
}

func respeecherTTSHTTPReadError(err error) error {
	msg := fmt.Sprintf("Respeecher TTS response read failed: %v", err)
	if errors.Is(err, context.DeadlineExceeded) {
		return llm.NewAPITimeoutError(msg)
	}
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return llm.NewAPITimeoutError(msg)
	}
	return llm.NewAPIConnectionError(msg)
}

func (s *respeecherTTSChunkedStream) ensureResponse() error {
	if s.resp != nil {
		return nil
	}
	req, err := buildRespeecherTTSRequest(s.ctx, s.provider, s.text)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return llm.NewAPIStatusError("Respeecher TTS request failed", resp.StatusCode, "", string(respBody))
	}
	s.resp = resp
	return nil
}

func (s *respeecherTTSChunkedStream) emitFinal() (*tts.SynthesizedAudio, error) {
	if s.finalSent {
		return nil, io.EOF
	}
	s.finalSent = true
	return &tts.SynthesizedAudio{IsFinal: true}, nil
}

func (s *respeecherTTSChunkedStream) Close() error {
	s.closed = true
	s.finalSent = true
	if s.resp == nil || s.resp.Body == nil {
		return nil
	}
	return s.resp.Body.Close()
}

func decodeRespeecherWAVPCM16(data []byte) (*model.AudioFrame, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("invalid respeecher wav data")
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
			return nil, fmt.Errorf("invalid respeecher wav chunk size")
		}
		switch chunkID {
		case "fmt ":
			if chunkSize < 16 {
				return nil, fmt.Errorf("invalid respeecher wav fmt chunk")
			}
			audioFormat := binary.LittleEndian.Uint16(data[offset : offset+2])
			channels = binary.LittleEndian.Uint16(data[offset+2 : offset+4])
			sampleRate = binary.LittleEndian.Uint32(data[offset+4 : offset+8])
			bitsPerSample = binary.LittleEndian.Uint16(data[offset+14 : offset+16])
			if audioFormat != 1 || bitsPerSample != 16 {
				return nil, fmt.Errorf("unsupported respeecher wav format: audio_format=%d bits_per_sample=%d", audioFormat, bitsPerSample)
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
		return nil, fmt.Errorf("missing respeecher wav format metadata")
	}
	if pcm == nil {
		return nil, fmt.Errorf("missing respeecher wav data chunk")
	}
	return &model.AudioFrame{
		Data:              pcm,
		SampleRate:        sampleRate,
		NumChannels:       uint32(channels),
		SamplesPerChannel: uint32(len(pcm) / int(channels) / 2),
	}, nil
}

func buildRespeecherTTSWebsocketURL(t *RespeecherTTS) *url.URL {
	baseURL := strings.TrimRight(t.baseURL, "/")
	if strings.HasPrefix(baseURL, "http://") || strings.HasPrefix(baseURL, "https://") {
		baseURL = strings.Replace(baseURL, "http", "ws", 1)
	}
	wsURL, err := url.Parse(baseURL + t.model + "/tts/websocket")
	if err != nil {
		return &url.URL{Scheme: "wss", Host: strings.TrimPrefix(baseURL, "wss://"), Path: t.model + "/tts/websocket"}
	}
	query := wsURL.Query()
	query.Set("api_key", t.apiKey)
	query.Set("source", "LiveKit-Plugin-Respeecher-Version")
	query.Set("version", respeecherAPIVersion)
	wsURL.RawQuery = query.Encode()
	return wsURL
}

func buildRespeecherTTSTextMessage(t *RespeecherTTS, contextID string, text string, continuation bool) ([]byte, error) {
	voice := map[string]interface{}{"id": t.voiceID}
	if len(t.samplingParams) > 0 {
		voice["sampling_params"] = t.samplingParams
	}
	return json.Marshal(map[string]interface{}{
		"context_id":    contextID,
		"transcript":    text,
		"voice":         voice,
		"continue":      continuation,
		"output_format": respeecherTTSOutputFormat(t),
	})
}

func buildRespeecherTTSEndMessage(t *RespeecherTTS, contextID string) ([]byte, error) {
	return buildRespeecherTTSTextMessage(t, contextID, " ", false)
}

func respeecherTTSOutputFormat(t *RespeecherTTS) map[string]interface{} {
	return map[string]interface{}{
		"sample_rate": t.sampleRate,
		"encoding":    t.encoding,
	}
}

type respeecherTTSSynthesizeStream struct {
	conn        *websocket.Conn
	ctx         context.Context
	cancel      context.CancelFunc
	provider    *RespeecherTTS
	contextID   string
	events      chan *tts.SynthesizedAudio
	errCh       chan error
	mu          sync.Mutex
	closed      bool
	inputEnded  bool
	pendingText string

	writeMessage func([]byte) error
	closeConn    func() error
}

func (s *respeecherTTSSynthesizeStream) PushText(text string) error {
	if text == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.inputEnded {
		return io.ErrClosedPipe
	}
	s.pendingText += text
	return s.sendCompleteSentencesLocked()
}

func (s *respeecherTTSSynthesizeStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.inputEnded {
		return io.ErrClosedPipe
	}
	return s.flushPendingTextLocked()
}

func (s *respeecherTTSSynthesizeStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	return s.endInputLocked()
}

func (s *respeecherTTSSynthesizeStream) flushPendingTextLocked() error {
	if s.pendingText != "" {
		text := strings.Join(tokenize.NewBasicSentenceTokenizer().Tokenize(s.pendingText, ""), " ")
		s.pendingText = ""
		if err := s.sendTextLocked(text); err != nil {
			return err
		}
	}
	return nil
}

func (s *respeecherTTSSynthesizeStream) endInputLocked() error {
	if s.inputEnded {
		return nil
	}
	if err := s.flushPendingTextLocked(); err != nil {
		return err
	}
	message, err := buildRespeecherTTSEndMessage(s.provider, s.contextID)
	if err != nil {
		return err
	}
	if err := s.writeMessageData(message); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	s.inputEnded = true
	return nil
}

func (s *respeecherTTSSynthesizeStream) sendCompleteSentencesLocked() error {
	for {
		tokens := tokenize.NewBasicSentenceTokenizer().Tokenize(s.pendingText, "")
		if len(tokens) <= 1 {
			return nil
		}
		sentence := tokens[0]
		if err := s.sendTextLocked(sentence); err != nil {
			return err
		}
		s.pendingText = strings.TrimPrefix(s.pendingText, sentence)
	}
}

func (s *respeecherTTSSynthesizeStream) sendTextLocked(text string) error {
	message, err := buildRespeecherTTSTextMessage(s.provider, s.contextID, text, true)
	if err != nil {
		return err
	}
	if err := s.writeMessageData(message); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

func (s *respeecherTTSSynthesizeStream) Close() error {
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
	err := s.closeConnection()
	if s.provider != nil {
		s.provider.unregisterStream(s)
	}
	return err
}

func (s *respeecherTTSSynthesizeStream) writeMessageData(message []byte) error {
	if s.writeMessage != nil {
		return s.writeMessage(message)
	}
	return s.writeWebsocketMessage(message)
}

func (s *respeecherTTSSynthesizeStream) writeWebsocketMessage(message []byte) error {
	return s.conn.WriteMessage(websocket.TextMessage, message)
}

func (s *respeecherTTSSynthesizeStream) closeConnection() error {
	if s.closeConn != nil {
		return s.closeConn()
	}
	return s.closeWebsocketConn()
}

func (s *respeecherTTSSynthesizeStream) closeWebsocketConn() error {
	if s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

func (s *respeecherTTSSynthesizeStream) closeAfterWriteFailureLocked() {
	if s.closed {
		return
	}
	s.closed = true
	s.cancel()
	_ = s.closeConnection()
	if s.provider != nil {
		s.provider.unregisterStream(s)
	}
}

func (s *respeecherTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
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

func (s *respeecherTTSSynthesizeStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *respeecherTTSSynthesizeStream) readLoop() {
	defer close(s.events)
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			if !s.isClosed() {
				s.errCh <- respeecherTTSReadError(err)
			}
			return
		}
		if msgType != websocket.TextMessage {
			continue
		}
		audio, done, err := respeecherTTSAudioFromStreamMessage(payload, s.contextID, s.provider.sampleRate)
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

func respeecherTTSReadError(err error) error {
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return llm.NewAPIStatusError("Respeecher connection closed unexpectedly", closeErr.Code, "", err.Error())
	}
	return llm.NewAPIConnectionError(fmt.Sprintf("Respeecher WebSocket transport error: %v", err))
}

func respeecherTTSAudioFromStreamMessage(payload []byte, contextID string, sampleRate int) (*tts.SynthesizedAudio, bool, error) {
	var message struct {
		ContextID string `json:"context_id"`
		Type      string `json:"type"`
		Data      string `json:"data"`
		Error     any    `json:"error"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, err
	}
	if message.ContextID != "" && message.ContextID != contextID {
		return nil, false, nil
	}
	switch message.Type {
	case "chunk":
		if message.Data == "" {
			return nil, false, nil
		}
		audio, err := respeecherDecodeBase64Audio(message.Data)
		if err != nil {
			return nil, false, err
		}
		if len(audio) == 0 {
			return nil, false, nil
		}
		return respeecherTTSAudioFrame(audio, sampleRate), false, nil
	case "done":
		return &tts.SynthesizedAudio{IsFinal: true}, true, nil
	case "error":
		return nil, false, llm.NewAPIError(fmt.Sprintf("Respeecher returned error: %v", message.Error), nil, true)
	default:
		return nil, false, nil
	}
}

func respeecherDecodeBase64Audio(data string) ([]byte, error) {
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

func respeecherTTSAudioFrame(audio []byte, sampleRate int) *tts.SynthesizedAudio {
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              bytes.Clone(audio),
			SampleRate:        uint32(sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(len(audio) / 2),
		},
	}
}
