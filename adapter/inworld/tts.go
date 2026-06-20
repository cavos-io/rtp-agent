package inworld

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
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	cavosmath "github.com/cavos-io/rtp-agent/library/math"
	"github.com/gorilla/websocket"
)

const (
	inworldPluginVersion                     = "1.5.15"
	inworldUserAgent                         = "livekit-agents-py/" + inworldPluginVersion
	defaultInworldBaseURL                    = "https://api.inworld.ai/"
	defaultInworldWebsocketURL               = "wss://api.inworld.ai/"
	defaultInworldModel                      = "inworld-tts-1.5-max"
	defaultInworldVoice                      = "Ashley"
	defaultInworldEncoding                   = "PCM"
	defaultInworldBitRate                    = 64000
	defaultInworldSampleRate                 = 24000
	defaultInworldSpeakingRate               = 1.0
	defaultInworldTemperature                = 1.0
	defaultInworldTimestampTransportStrategy = "ASYNC"
	defaultInworldBufferCharThreshold        = 120
	defaultInworldMaxBufferDelayMS           = 3000
	inworldTTSSendTextChunkLimit             = 1000
	inworldTTSMaxResponseLineBytes           = 16 * 1024 * 1024
)

type InworldTTS struct {
	mu                         sync.Mutex
	apiKey                     string
	baseURL                    string
	wsURL                      string
	voice                      string
	model                      string
	encoding                   string
	bitRate                    int
	sampleRate                 int
	speakingRate               float64
	temperature                float64
	language                   string
	timestampType              string
	textNormalization          string
	deliveryMode               string
	timestampTransportStrategy string
	bufferCharThreshold        int
	maxBufferDelayMS           int
	streams                    map[*inworldTTSSynthesizeStream]struct{}
}

type InworldTTSOption func(*InworldTTS)

func WithInworldTTSBaseURL(baseURL string) InworldTTSOption {
	return func(t *InworldTTS) {
		if baseURL != "" {
			t.baseURL = ensureTrailingSlash(baseURL)
		}
	}
}

func WithInworldTTSWebsocketURL(wsURL string) InworldTTSOption {
	return func(t *InworldTTS) {
		if wsURL != "" {
			t.wsURL = ensureTrailingSlash(wsURL)
		}
	}
}

func WithInworldTTSVoice(voice string) InworldTTSOption {
	return func(t *InworldTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithInworldTTSModel(model string) InworldTTSOption {
	return func(t *InworldTTS) {
		if model != "" {
			t.model = model
		}
	}
}

func WithInworldTTSEncoding(encoding string) InworldTTSOption {
	return func(t *InworldTTS) {
		if encoding != "" {
			t.encoding = encoding
		}
	}
}

func WithInworldTTSBitRate(bitRate int) InworldTTSOption {
	return func(t *InworldTTS) {
		if bitRate > 0 {
			t.bitRate = bitRate
		}
	}
}

func WithInworldTTSSampleRate(sampleRate int) InworldTTSOption {
	return func(t *InworldTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithInworldTTSSpeakingRate(speakingRate float64) InworldTTSOption {
	return func(t *InworldTTS) {
		if speakingRate > 0 {
			t.speakingRate = speakingRate
		}
	}
}

func WithInworldTTSTemperature(temperature float64) InworldTTSOption {
	return func(t *InworldTTS) {
		if temperature > 0 {
			t.temperature = temperature
		}
	}
}

func WithInworldTTSLanguage(language string) InworldTTSOption {
	return func(t *InworldTTS) {
		t.language = language
	}
}

func WithInworldTTSTimestampType(timestampType string) InworldTTSOption {
	return func(t *InworldTTS) {
		t.timestampType = timestampType
	}
}

func WithInworldTTSTextNormalization(enabled bool) InworldTTSOption {
	return func(t *InworldTTS) {
		if enabled {
			t.textNormalization = "ON"
		} else {
			t.textNormalization = "OFF"
		}
	}
}

func WithInworldTTSDeliveryMode(deliveryMode string) InworldTTSOption {
	return func(t *InworldTTS) {
		t.deliveryMode = deliveryMode
	}
}

func WithInworldTTSTimestampTransportStrategy(strategy string) InworldTTSOption {
	return func(t *InworldTTS) {
		if strategy != "" {
			t.timestampTransportStrategy = strategy
		}
	}
}

func WithInworldTTSBufferCharThreshold(threshold int) InworldTTSOption {
	return func(t *InworldTTS) {
		if threshold > 0 {
			t.bufferCharThreshold = threshold
		}
	}
}

func WithInworldTTSMaxBufferDelayMS(delayMS int) InworldTTSOption {
	return func(t *InworldTTS) {
		if delayMS > 0 {
			t.maxBufferDelayMS = delayMS
		}
	}
}

func NewInworldTTS(apiKey string, voice string, opts ...InworldTTSOption) *InworldTTS {
	provider := &InworldTTS{
		apiKey:                     resolveInworldAPIKey(apiKey),
		baseURL:                    defaultInworldBaseURL,
		wsURL:                      defaultInworldWebsocketURL,
		voice:                      voice,
		model:                      defaultInworldModel,
		encoding:                   defaultInworldEncoding,
		bitRate:                    defaultInworldBitRate,
		sampleRate:                 defaultInworldSampleRate,
		speakingRate:               defaultInworldSpeakingRate,
		temperature:                defaultInworldTemperature,
		timestampTransportStrategy: defaultInworldTimestampTransportStrategy,
		bufferCharThreshold:        defaultInworldBufferCharThreshold,
		maxBufferDelayMS:           defaultInworldMaxBufferDelayMS,
	}
	if provider.voice == "" {
		provider.voice = defaultInworldVoice
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultInworldVoice
	}
	return provider
}

func (t *InworldTTS) Label() string { return "inworld.TTS" }
func (t *InworldTTS) Model() string { return t.model }
func (t *InworldTTS) Provider() string {
	return "Inworld"
}

func (t *InworldTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{
		Streaming:         true,
		AlignedTranscript: t.timestampType != "" && t.timestampType != "TIMESTAMP_TYPE_UNSPECIFIED",
	}
}

func (t *InworldTTS) SampleRate() int  { return t.sampleRate }
func (t *InworldTTS) NumChannels() int { return 1 }

func (t *InworldTTS) UpdateOptions(opts ...InworldTTSOption) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, opt := range opts {
		opt(t)
	}
}

func (t *InworldTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildInworldTTSRequest(ctx, t, text)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, llm.NewAPIConnectionError(err.Error())
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, llm.NewAPIStatusError("Inworld TTS request failed", resp.StatusCode, req.Header.Get("X-Request-Id"), string(respBody))
	}
	return &inworldTTSChunkedStream{resp: resp, sampleRate: t.sampleRate}, nil
}

func buildInworldTTSRequest(ctx context.Context, t *InworldTTS, text string) (*http.Request, error) {
	payload := inworldTTSRequestPayload(t, text)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+"/tts/v1/voice:stream", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for key, values := range buildInworldTTSHeaders(t) {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func inworldTTSRequestPayload(t *InworldTTS, text string) map[string]interface{} {
	payload := inworldTTSBasePayload(t)
	payload["text"] = text
	return payload
}

func (t *InworldTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildInworldTTSWebsocketURL(t), buildInworldTTSWebsocketHeaders(t))
	if err != nil {
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("failed to dial inworld tts websocket: %v", err))
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &inworldTTSSynthesizeStream{
		conn:       conn,
		ctx:        streamCtx,
		cancel:     cancel,
		provider:   t,
		contextID:  cavosmath.ShortUUID(""),
		events:     make(chan *tts.SynthesizedAudio, 100),
		errCh:      make(chan error, 1),
		sampleRate: t.sampleRate,
	}
	stream.writeMessage = stream.writeWebsocketMessage
	stream.closeConn = stream.closeWebsocketConn
	createMessage, err := buildInworldTTSCreateMessage(t, stream.contextID)
	if err != nil {
		_ = conn.Close()
		cancel()
		return nil, err
	}
	if err := conn.WriteMessage(websocket.TextMessage, createMessage); err != nil {
		_ = conn.Close()
		cancel()
		return nil, err
	}
	t.registerStream(stream)
	go stream.readLoop()
	return stream, nil
}

func (t *InworldTTS) Close() error {
	t.mu.Lock()
	streams := make([]*inworldTTSSynthesizeStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.mu.Unlock()

	var firstErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (t *InworldTTS) registerStream(stream *inworldTTSSynthesizeStream) {
	if stream == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.streams == nil {
		t.streams = make(map[*inworldTTSSynthesizeStream]struct{})
	}
	stream.provider = t
	t.streams[stream] = struct{}{}
}

func (t *InworldTTS) unregisterStream(stream *inworldTTSSynthesizeStream) {
	if stream == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.streams, stream)
}

func buildInworldTTSWebsocketURL(t *InworldTTS) string {
	return strings.TrimRight(t.wsURL, "/") + "/tts/v1/voice:streamBidirectional"
}

func buildInworldTTSHeaders(t *InworldTTS) http.Header {
	headers := http.Header{}
	headers.Set("Authorization", "Basic "+t.apiKey)
	headers.Set("X-User-Agent", inworldUserAgent)
	headers.Set("X-Request-Id", cavosmath.ShortUUID(""))
	return headers
}

func buildInworldTTSWebsocketHeaders(t *InworldTTS) http.Header {
	return buildInworldTTSHeaders(t)
}

func buildInworldTTSCreateMessage(t *InworldTTS, contextID string) ([]byte, error) {
	create := inworldTTSBasePayload(t)
	delete(create, "text")
	if audioConfig, ok := create["audioConfig"].(map[string]interface{}); ok {
		delete(audioConfig, "temperature")
	}
	create["temperature"] = t.temperature
	create["bufferCharThreshold"] = t.bufferCharThreshold
	create["maxBufferDelayMs"] = t.maxBufferDelayMS
	create["autoMode"] = true
	return json.Marshal(map[string]interface{}{
		"create":    create,
		"contextId": contextID,
	})
}

func buildInworldTTSSendTextMessage(contextID string, text string) ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"send_text": map[string]interface{}{"text": text},
		"contextId": contextID,
	})
}

func buildInworldTTSFlushMessage(contextID string) ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"flush_context": map[string]interface{}{},
		"contextId":     contextID,
	})
}

func buildInworldTTSCloseMessage(contextID string) ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"close_context": map[string]interface{}{},
		"contextId":     contextID,
	})
}

func inworldTTSBasePayload(t *InworldTTS) map[string]interface{} {
	payload := map[string]interface{}{
		"voiceId": t.voice,
		"modelId": t.model,
		"audioConfig": map[string]interface{}{
			"audioEncoding":   t.encoding,
			"sampleRateHertz": t.sampleRate,
			"bitrate":         t.bitRate,
			"speakingRate":    t.speakingRate,
			"temperature":     t.temperature,
		},
		"timestampTransportStrategy": t.timestampTransportStrategy,
	}
	if t.language != "" {
		payload["language"] = t.language
	}
	if t.timestampType != "" {
		payload["timestampType"] = t.timestampType
	}
	if t.textNormalization != "" {
		payload["applyTextNormalization"] = t.textNormalization
	}
	if t.deliveryMode != "" {
		payload["deliveryMode"] = t.deliveryMode
	}
	return payload
}

type inworldTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
	scanner    *bufio.Scanner
}

func (s *inworldTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.resp == nil || s.resp.Body == nil {
		return nil, io.EOF
	}
	if s.scanner == nil {
		s.scanner = bufio.NewScanner(s.resp.Body)
		s.scanner.Buffer(make([]byte, 0, 64*1024), inworldTTSMaxResponseLineBytes)
	}
	for s.scanner.Scan() {
		line := bytes.TrimSpace(s.scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		audio, done, err := inworldTTSAudioFromResponseLine(line, s.sampleRate)
		if err != nil {
			var syntaxErr *json.SyntaxError
			if errors.As(err, &syntaxErr) {
				continue
			}
			return nil, err
		}
		if done {
			return nil, io.EOF
		}
		if audio != nil {
			return audio, nil
		}
	}
	if err := s.scanner.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func (s *inworldTTSChunkedStream) Close() error {
	if s.resp == nil || s.resp.Body == nil {
		return nil
	}
	body := s.resp.Body
	s.resp = nil
	return body.Close()
}

type inworldTTSSynthesizeStream struct {
	conn              *websocket.Conn
	ctx               context.Context
	cancel            context.CancelFunc
	provider          *InworldTTS
	contextID         string
	events            chan *tts.SynthesizedAudio
	errCh             chan error
	sampleRate        int
	pendingText       bytes.Buffer
	mu                sync.Mutex
	closed            bool
	cumulativeTime    float64
	generationEndTime float64

	writeMessage func(int, []byte) error
	closeConn    func() error
}

func (s *inworldTTSSynthesizeStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if text == "" {
		return nil
	}
	if s.closed {
		return fmt.Errorf("inworld tts stream is closed")
	}
	_, err := s.pendingText.WriteString(text)
	return err
}

func (s *inworldTTSSynthesizeStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("inworld tts stream is closed")
	}
	text := s.pendingText.String()
	s.pendingText.Reset()
	if s.conn == nil && s.writeMessage == nil {
		return nil
	}
	if text != "" {
		for _, chunk := range inworldTTSChunkText(text, inworldTTSSendTextChunkLimit) {
			message, err := buildInworldTTSSendTextMessage(s.contextID, chunk)
			if err != nil {
				return err
			}
			if err := s.writeMessageData(websocket.TextMessage, message); err != nil {
				s.closeAfterWriteFailureLocked()
				return err
			}
		}
	}
	message, err := buildInworldTTSFlushMessage(s.contextID)
	if err != nil {
		return err
	}
	if err := s.writeMessageData(websocket.TextMessage, message); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

func inworldTTSChunkText(text string, limit int) []string {
	if text == "" {
		return nil
	}
	if limit <= 0 {
		return []string{text}
	}
	chunks := make([]string, 0, (len([]rune(text))+limit-1)/limit)
	start := 0
	count := 0
	for i := range text {
		if count == limit {
			chunks = append(chunks, text[start:i])
			start = i
			count = 0
		}
		count++
	}
	chunks = append(chunks, text[start:])
	return chunks
}

func (s *inworldTTSSynthesizeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	if s.provider != nil {
		defer s.provider.unregisterStream(s)
	}
	if message, err := buildInworldTTSCloseMessage(s.contextID); err == nil {
		_ = s.writeMessageData(websocket.TextMessage, message)
	}
	if s.conn != nil {
		_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	}
	return s.closeConnection()
}

func (s *inworldTTSSynthesizeStream) writeMessageData(messageType int, data []byte) error {
	if s.writeMessage != nil {
		return s.writeMessage(messageType, data)
	}
	return s.writeWebsocketMessage(messageType, data)
}

func (s *inworldTTSSynthesizeStream) writeWebsocketMessage(messageType int, data []byte) error {
	return s.conn.WriteMessage(messageType, data)
}

func (s *inworldTTSSynthesizeStream) closeConnection() error {
	if s.closeConn != nil {
		return s.closeConn()
	}
	return s.closeWebsocketConn()
}

func (s *inworldTTSSynthesizeStream) closeWebsocketConn() error {
	if s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

func (s *inworldTTSSynthesizeStream) closeAfterWriteFailureLocked() {
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

func (s *inworldTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
	if s.isClosed() {
		return nil, io.EOF
	}
	select {
	case event, ok := <-s.events:
		if !ok {
			select {
			case err := <-s.errCh:
				return nil, err
			default:
				return nil, io.EOF
			}
		}
		return event, nil
	case err := <-s.errCh:
		return nil, err
	case <-s.ctx.Done():
		if s.isClosed() {
			return nil, io.EOF
		}
		return nil, s.ctx.Err()
	}
}

func (s *inworldTTSSynthesizeStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *inworldTTSSynthesizeStream) readLoop() {
	defer close(s.events)
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) && err != io.EOF {
				s.errCh <- err
			}
			return
		}
		if msgType != websocket.TextMessage {
			continue
		}
		audio, done, err := s.handleWebsocketMessage(payload)
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

func (s *inworldTTSSynthesizeStream) handleWebsocketMessage(payload []byte) (*tts.SynthesizedAudio, bool, error) {
	audio, done, flushCompleted, generationEndTime, err := inworldTTSAudioFromWebsocketMessageWithOffset(payload, s.contextID, s.sampleRate, s.cumulativeTime)
	if err != nil {
		return nil, false, err
	}
	if generationEndTime > s.generationEndTime {
		s.generationEndTime = generationEndTime
	}
	if flushCompleted && s.generationEndTime > 0 {
		s.cumulativeTime = s.generationEndTime
	}
	return audio, done, nil
}

func inworldTTSAudioFromResponseLine(payload []byte, sampleRate int) (*tts.SynthesizedAudio, bool, error) {
	var message struct {
		Result struct {
			AudioContent  string `json:"audioContent"`
			TimestampInfo struct {
				WordAlignment struct {
					Words                []string  `json:"words"`
					WordStartTimeSeconds []float64 `json:"wordStartTimeSeconds"`
					WordEndTimeSeconds   []float64 `json:"wordEndTimeSeconds"`
				} `json:"wordAlignment"`
			} `json:"timestampInfo"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, err
	}
	if message.Error != nil {
		return nil, false, llm.NewAPIStatusError(message.Error.Message, message.Error.Code, "", nil)
	}
	if message.Result.AudioContent == "" {
		return nil, false, nil
	}
	audio, err := base64.StdEncoding.DecodeString(message.Result.AudioContent)
	if err != nil {
		return nil, false, err
	}
	frame := inworldTTSAudioFrame(audio, sampleRate)
	frame.TimedTranscript = inworldTTSTimedTranscript(message.Result.TimestampInfo.WordAlignment.Words, message.Result.TimestampInfo.WordAlignment.WordStartTimeSeconds, message.Result.TimestampInfo.WordAlignment.WordEndTimeSeconds, 0)
	return frame, false, nil
}

func inworldTTSAudioFromWebsocketMessage(payload []byte, contextID string, sampleRate int) (*tts.SynthesizedAudio, bool, error) {
	audio, done, _, _, err := inworldTTSAudioFromWebsocketMessageWithOffset(payload, contextID, sampleRate, 0)
	return audio, done, err
}

func inworldTTSAudioFromWebsocketMessageWithOffset(payload []byte, contextID string, sampleRate int, cumulativeTime float64) (*tts.SynthesizedAudio, bool, bool, float64, error) {
	var message struct {
		Result struct {
			ContextID  string `json:"contextId"`
			AudioChunk *struct {
				AudioContent  string `json:"audioContent"`
				TimestampInfo struct {
					WordAlignment struct {
						Words                []string  `json:"words"`
						WordStartTimeSeconds []float64 `json:"wordStartTimeSeconds"`
						WordEndTimeSeconds   []float64 `json:"wordEndTimeSeconds"`
					} `json:"wordAlignment"`
				} `json:"timestampInfo"`
			} `json:"audioChunk"`
			ContextClosed map[string]interface{} `json:"contextClosed"`
			Status        *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"status"`
			FlushCompleted map[string]interface{} `json:"flushCompleted"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, false, 0, err
	}
	if message.Error != nil {
		return nil, false, false, 0, llm.NewAPIStatusError(message.Error.Message, message.Error.Code, "", nil)
	}
	if message.Result.Status != nil && message.Result.Status.Code != 0 {
		return nil, false, false, 0, llm.NewAPIStatusError(message.Result.Status.Message, message.Result.Status.Code, "", nil)
	}
	if message.Result.ContextID != "" && message.Result.ContextID != contextID {
		return nil, false, false, 0, nil
	}
	if message.Result.FlushCompleted != nil {
		return nil, false, true, 0, nil
	}
	if message.Result.ContextClosed != nil {
		return nil, true, false, 0, nil
	}
	if message.Result.AudioChunk == nil || message.Result.AudioChunk.AudioContent == "" {
		return nil, false, false, 0, nil
	}
	audio, err := base64.StdEncoding.DecodeString(message.Result.AudioChunk.AudioContent)
	if err != nil {
		return nil, false, false, 0, err
	}
	frame := inworldTTSAudioFrame(audio, sampleRate)
	frame.SegmentID = message.Result.ContextID
	frame.TimedTranscript = inworldTTSTimedTranscript(message.Result.AudioChunk.TimestampInfo.WordAlignment.Words, message.Result.AudioChunk.TimestampInfo.WordAlignment.WordStartTimeSeconds, message.Result.AudioChunk.TimestampInfo.WordAlignment.WordEndTimeSeconds, cumulativeTime)
	return frame, false, false, inworldTTSGenerationEndTime(frame.TimedTranscript), nil
}

func inworldTTSAudioFrame(audio []byte, sampleRate int) *tts.SynthesizedAudio {
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              bytes.Clone(audio),
			SampleRate:        uint32(sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(len(audio) / 2),
		},
	}
}

func inworldTTSTimedTranscript(words []string, starts []float64, ends []float64, cumulativeTime float64) []tts.TimedString {
	limit := len(words)
	if len(starts) < limit {
		limit = len(starts)
	}
	if len(ends) < limit {
		limit = len(ends)
	}
	if limit == 0 {
		return nil
	}
	timed := make([]tts.TimedString, 0, limit)
	for i := 0; i < limit; i++ {
		if words[i] == "" {
			continue
		}
		timed = append(timed, tts.TimedString{
			Text:      words[i],
			StartTime: starts[i] + cumulativeTime,
			EndTime:   ends[i] + cumulativeTime,
		})
	}
	return timed
}

func inworldTTSGenerationEndTime(timed []tts.TimedString) float64 {
	var endTime float64
	for _, word := range timed {
		if word.EndTime > endTime {
			endTime = word.EndTime
		}
	}
	return endTime
}

func ensureTrailingSlash(value string) string {
	return strings.TrimRight(value, "/") + "/"
}
