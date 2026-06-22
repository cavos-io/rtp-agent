package mistralai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/gorilla/websocket"
)

const (
	defaultMistralAISTTBaseURL    = "https://api.mistral.ai/v1"
	defaultMistralAISTTModel      = "voxtral-mini-latest"
	defaultMistralAISTTSampleRate = 16000
	mistralAISTTRealtimeChunkSize = defaultMistralAISTTSampleRate / 20 * 2
)

type MistralAISTT struct {
	apiKey                 string
	baseURL                string
	model                  string
	language               string
	contextBias            []string
	sampleRate             int
	targetStreamingDelayMS *int
	vad                    vad.VAD

	dialRealtime  func(context.Context, string, http.Header) (mistralAISTTRealtimeConn, error)
	streamsMu     sync.Mutex
	activeStreams map[*mistralAISTTRealtimeStream]struct{}
}

type MistralAISTTOption func(*MistralAISTT)

func WithMistralAISTTBaseURL(baseURL string) MistralAISTTOption {
	return func(s *MistralAISTT) {
		if baseURL != "" {
			s.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithMistralAISTTModel(model string) MistralAISTTOption {
	return func(s *MistralAISTT) {
		if model != "" {
			s.model = model
		}
	}
}

func WithMistralAISTTLanguage(language string) MistralAISTTOption {
	return func(s *MistralAISTT) {
		s.language = language
	}
}

func WithMistralAISTTContextBias(contextBias []string) MistralAISTTOption {
	return func(s *MistralAISTT) {
		s.contextBias = contextBias
	}
}

func WithMistralAISTTTargetStreamingDelay(delayMS int) MistralAISTTOption {
	return func(s *MistralAISTT) {
		s.targetStreamingDelayMS = &delayMS
	}
}

func WithMistralAISTTVAD(v vad.VAD) MistralAISTTOption {
	return func(s *MistralAISTT) {
		s.vad = v
	}
}

func NewMistralAISTT(apiKey string, opts ...MistralAISTTOption) *MistralAISTT {
	if apiKey == "" {
		apiKey = os.Getenv("MISTRAL_API_KEY")
	}
	provider := &MistralAISTT{
		apiKey:     apiKey,
		baseURL:    defaultMistralAISTTBaseURL,
		model:      defaultMistralAISTTModel,
		sampleRate: defaultMistralAISTTSampleRate,
	}
	provider.dialRealtime = defaultMistralAISTTRealtimeDialer
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *MistralAISTT) Label() string { return "mistralai.STT" }
func (s *MistralAISTT) Model() string { return s.model }
func (s *MistralAISTT) Provider() string {
	return "MistralAI"
}
func (s *MistralAISTT) InputSampleRate() uint32 {
	if s == nil || s.sampleRate <= 0 {
		return defaultMistralAISTTSampleRate
	}
	return uint32(s.sampleRate)
}
func (s *MistralAISTT) Capabilities() stt.STTCapabilities {
	realtime := mistralAISTTIsRealtime(s.model)
	return stt.STTCapabilities{
		Streaming:         realtime,
		InterimResults:    realtime,
		Diarization:       false,
		AlignedTranscript: "",
		OfflineRecognize:  !realtime,
	}
}

func (s *MistralAISTT) UpdateOptions(opts ...MistralAISTTOption) {
	beforeDelay := cloneIntPtr(s.targetStreamingDelayMS)
	for _, opt := range opts {
		opt(s)
	}
	if intPtrsEqual(beforeDelay, s.targetStreamingDelayMS) || s.targetStreamingDelayMS == nil {
		return
	}
	for _, stream := range s.snapshotRealtimeStreams() {
		if err := stream.updateTargetStreamingDelay(*s.targetStreamingDelayMS); err != nil {
			stream.sendErr(err)
		}
	}
}

func (s *MistralAISTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if err := validateMistralAISTTAPIKey(s.apiKey); err != nil {
		return nil, err
	}
	if !mistralAISTTIsRealtime(s.model) {
		return nil, fmt.Errorf("mistralai stt streaming is only available for realtime models")
	}
	conn, err := s.dialRealtime(ctx, buildMistralAISTTRealtimeURL(s), buildMistralAISTTRealtimeHeaders(s))
	if err != nil {
		return nil, llm.NewAPIConnectionError(err.Error())
	}
	var vadStream vad.VADStream
	if s.vad != nil {
		vadStream, err = s.vad.Stream(ctx)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &mistralAISTTRealtimeStream{
		conn:      conn,
		events:    make(chan *stt.SpeechEvent, 100),
		errCh:     make(chan error, 1),
		ctx:       streamCtx,
		cancel:    cancel,
		state:     &mistralAISTTRealtimeState{detectedLanguage: language},
		vadStream: vadStream,
	}
	s.registerRealtimeStream(stream)
	if s.targetStreamingDelayMS != nil {
		if err := stream.updateTargetStreamingDelay(*s.targetStreamingDelayMS); err != nil {
			cancel()
			s.unregisterRealtimeStream(stream)
			_ = conn.Close()
			return nil, err
		}
	}
	stream.wg.Add(1)
	go func() {
		defer stream.wg.Done()
		stream.readLoop()
	}()
	if vadStream != nil {
		stream.wg.Add(1)
		go func() {
			defer stream.wg.Done()
			stream.vadLoop()
		}()
	}
	go func() {
		stream.wg.Wait()
		s.unregisterRealtimeStream(stream)
		close(stream.events)
	}()
	return stream, nil
}

func cloneIntPtr(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func intPtrsEqual(left *int, right *int) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func (s *MistralAISTT) registerRealtimeStream(stream *mistralAISTTRealtimeStream) {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	if s.activeStreams == nil {
		s.activeStreams = map[*mistralAISTTRealtimeStream]struct{}{}
	}
	s.activeStreams[stream] = struct{}{}
}

func (s *MistralAISTT) unregisterRealtimeStream(stream *mistralAISTTRealtimeStream) {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	delete(s.activeStreams, stream)
}

func (s *MistralAISTT) snapshotRealtimeStreams() []*mistralAISTTRealtimeStream {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	streams := make([]*mistralAISTTRealtimeStream, 0, len(s.activeStreams))
	for stream := range s.activeStreams {
		streams = append(streams, stream)
	}
	return streams
}

func (s *MistralAISTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	if err := validateMistralAISTTAPIKey(s.apiKey); err != nil {
		return nil, err
	}

	if mistralAISTTIsRealtime(s.model) {
		return nil, fmt.Errorf("mistralai realtime models do not support offline recognize")
	}
	audio := mistralAISTTWAVBytes(frames, uint32(s.sampleRate), 1)
	req, err := buildMistralAISTTRecognizeRequest(ctx, s, audio, language)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, llm.NewAPIConnectionError(err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusGatewayTimeout {
			return nil, llm.NewAPITimeoutError(string(respBody))
		}
		return nil, llm.NewAPIStatusError("MistralAI STT request failed", resp.StatusCode, "", string(respBody))
	}
	var result mistralAISTTResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, llm.NewAPIConnectionError(err.Error())
	}
	return mistralAISTTSpeechEvent(resolveMistralAISTTLanguage(s, language), result), nil
}

func mistralAISTTWAVBytes(frames []*model.AudioFrame, defaultSampleRate uint32, defaultNumChannels uint32) []byte {
	sampleRate := defaultSampleRate
	numChannels := defaultNumChannels
	var pcm bytes.Buffer
	for _, frame := range frames {
		if frame == nil {
			continue
		}
		if frame.SampleRate != 0 {
			sampleRate = frame.SampleRate
		}
		if frame.NumChannels != 0 {
			numChannels = frame.NumChannels
		}
		pcm.Write(frame.Data)
	}
	if sampleRate == 0 {
		sampleRate = defaultMistralAISTTSampleRate
	}
	if numChannels == 0 {
		numChannels = 1
	}
	data := pcm.Bytes()
	dataSize := uint32(len(data))
	byteRate := sampleRate * numChannels * 2
	blockAlign := numChannels * 2
	var wav bytes.Buffer
	wav.WriteString("RIFF")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(36)+dataSize)
	wav.WriteString("WAVE")
	wav.WriteString("fmt ")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(16))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(1))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(numChannels))
	_ = binary.Write(&wav, binary.LittleEndian, sampleRate)
	_ = binary.Write(&wav, binary.LittleEndian, byteRate)
	_ = binary.Write(&wav, binary.LittleEndian, uint16(blockAlign))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(16))
	wav.WriteString("data")
	_ = binary.Write(&wav, binary.LittleEndian, dataSize)
	wav.Write(data)
	return wav.Bytes()
}

func buildMistralAISTTRecognizeRequest(ctx context.Context, s *MistralAISTT, audio []byte, language string) (*http.Request, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="audio.wav"`)
	header.Set("Content-Type", "audio/wav")
	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(audio); err != nil {
		return nil, err
	}
	if err := writer.WriteField("model", s.model); err != nil {
		return nil, err
	}
	if len(s.contextBias) > 0 {
		if err := writer.WriteField("context_bias", strings.Join(s.contextBias, ",")); err != nil {
			return nil, err
		}
	}
	if requestLanguage := resolveMistralAISTTLanguage(s, language); requestLanguage != "" {
		if err := writer.WriteField("language", requestLanguage); err != nil {
			return nil, err
		}
	} else if err := writer.WriteField("timestamp_granularities", "segment"); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.baseURL, "/")+"/audio/transcriptions", &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", s.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req, nil
}

func buildMistralAISTTRealtimeURL(s *MistralAISTT) string {
	base := strings.TrimRight(s.baseURL, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	endpoint := base + "/audio/transcriptions/realtime"
	u, _ := url.Parse(endpoint)
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}
	q := u.Query()
	q.Set("model", s.model)
	u.RawQuery = q.Encode()
	return u.String()
}

func buildMistralAISTTRealtimeHeaders(s *MistralAISTT) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+s.apiKey)
	return headers
}

type mistralAISTTRealtimeConn interface {
	WriteMessage(messageType int, data []byte) error
	ReadMessage() (int, []byte, error)
	Close() error
}

func defaultMistralAISTTRealtimeDialer(ctx context.Context, endpoint string, headers http.Header) (mistralAISTTRealtimeConn, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, endpoint, headers)
	return conn, err
}

type mistralAISTTRealtimeStream struct {
	conn            mistralAISTTRealtimeConn
	events          chan *stt.SpeechEvent
	errCh           chan error
	mu              sync.Mutex
	writeMu         sync.Mutex
	wg              sync.WaitGroup
	closed          bool
	speaking        bool
	startTimeOffset float64
	startTime       float64
	ctx             context.Context
	cancel          context.CancelFunc
	state           *mistralAISTTRealtimeState
	vadStream       vad.VADStream
}

type mistralAISTTRealtimeState struct {
	requestID        string
	detectedLanguage string
	currentText      string
}

func (s *mistralAISTTRealtimeStream) PushFrame(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	for offset := 0; offset < len(frame.Data); offset += mistralAISTTRealtimeChunkSize {
		end := min(offset+mistralAISTTRealtimeChunkSize, len(frame.Data))
		msg := map[string]any{
			"type":  "input_audio.append",
			"audio": base64.StdEncoding.EncodeToString(frame.Data[offset:end]),
		}
		if err := s.writeJSON(msg); err != nil {
			return err
		}
	}
	if s.vadStream != nil {
		if err := s.vadStream.PushFrame(frame); err != nil {
			return err
		}
	}
	return nil
}

func (s *mistralAISTTRealtimeStream) Flush() error {
	return s.writeJSON(map[string]any{"type": "input_audio.flush"})
}

func (s *mistralAISTTRealtimeStream) updateTargetStreamingDelay(delayMS int) error {
	return s.writeJSON(map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"target_streaming_delay_ms": delayMS,
		},
	})
}

func (s *mistralAISTTRealtimeStream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	if s.vadStream != nil {
		_ = s.vadStream.EndInput()
		_ = s.vadStream.Close()
	}
	_ = s.writeJSON(map[string]any{"type": "input_audio.end"})
	return s.conn.Close()
}

func (s *mistralAISTTRealtimeStream) Next() (*stt.SpeechEvent, error) {
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
		return nil, s.ctx.Err()
	}
}

func (s *mistralAISTTRealtimeStream) StartTimeOffset() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startTimeOffset
}

func (s *mistralAISTTRealtimeStream) SetStartTimeOffset(offset float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startTimeOffset = nonNegativeMistralAISTTStreamTime(offset)
}

func (s *mistralAISTTRealtimeStream) StartTime() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startTime
}

func (s *mistralAISTTRealtimeStream) SetStartTime(startTime float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startTime = nonNegativeMistralAISTTStreamTime(startTime)
}

func (s *mistralAISTTRealtimeStream) currentStartTimeOffset() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startTimeOffset
}

func (s *mistralAISTTRealtimeStream) readLoop() {
	for {
		msgType, message, err := s.conn.ReadMessage()
		if err != nil {
			if err != io.EOF && !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				s.errCh <- llm.NewAPIConnectionError(err.Error())
			}
			return
		}
		if msgType != websocket.TextMessage {
			continue
		}
		events, err := processMistralAISTTRealtimeMessage(s.state, message, s.currentStartTimeOffset())
		if err != nil {
			s.errCh <- err
			return
		}
		for _, event := range events {
			s.events <- event
		}
	}
}

func (s *mistralAISTTRealtimeStream) vadLoop() {
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}
		event, err := s.vadStream.Next()
		if err != nil {
			if err != io.EOF {
				s.sendErr(err)
			}
			return
		}
		switch event.Type {
		case vad.VADEventStartOfSpeech:
			if !s.speaking {
				s.speaking = true
				s.sendEvent(&stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech})
			}
		case vad.VADEventEndOfSpeech:
			if err := s.Flush(); err != nil {
				s.sendErr(err)
				return
			}
			if s.speaking {
				s.speaking = false
				s.sendEvent(&stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech})
			}
		}
	}
}

func (s *mistralAISTTRealtimeStream) sendEvent(event *stt.SpeechEvent) {
	select {
	case s.events <- event:
	case <-s.ctx.Done():
	}
}

func (s *mistralAISTTRealtimeStream) sendErr(err error) {
	select {
	case s.errCh <- err:
	default:
	}
}

func (s *mistralAISTTRealtimeStream) writeJSON(msg map[string]any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.WriteMessage(websocket.TextMessage, data)
}

func processMistralAISTTRealtimeMessage(state *mistralAISTTRealtimeState, message []byte, startTimeOffset float64) ([]*stt.SpeechEvent, error) {
	var payload map[string]any
	if err := json.Unmarshal(message, &payload); err != nil {
		return nil, llm.NewAPIConnectionError(err.Error())
	}
	switch payloadString(payload, "type") {
	case "session.created":
		if session, ok := payload["session"].(map[string]any); ok {
			state.requestID = payloadString(session, "request_id")
		}
	case "transcription.language":
		state.detectedLanguage = payloadString(payload, "audio_language")
	case "transcription.text.delta":
		state.currentText += payloadString(payload, "text")
		if state.currentText == "" {
			return nil, nil
		}
		return []*stt.SpeechEvent{{
			Type:      stt.SpeechEventInterimTranscript,
			RequestID: state.requestID,
			Alternatives: []stt.SpeechData{{
				Text:     state.currentText,
				Language: state.detectedLanguage,
			}},
		}}, nil
	case "transcription.done":
		text := payloadString(payload, "text")
		language := payloadString(payload, "language")
		if language == "" {
			language = state.detectedLanguage
		}
		state.currentText = ""
		events := []*stt.SpeechEvent{{
			Type:      stt.SpeechEventFinalTranscript,
			RequestID: state.requestID,
			Alternatives: []stt.SpeechData{{
				Text:     text,
				Language: language,
				Words:    mistralAISTTRealtimeSegments(payload["segments"], startTimeOffset),
			}},
		}}
		events = append(events, mistralAISTTRealtimeUsageEvent(state.requestID, payload["usage"]))
		return events, nil
	case "error":
		errPayload, _ := payload["error"].(map[string]any)
		return nil, llm.NewAPIStatusErrorWithRetryable(
			payloadString(errPayload, "message"),
			int(payloadFloat(errPayload, "code")),
			state.requestID,
			errPayload,
			false,
		)
	}
	return nil, nil
}

func mistralAISTTRealtimeSegments(value any, startTimeOffset float64) []stt.TimedString {
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return nil
	}
	segments := make([]stt.TimedString, 0, len(items))
	for _, item := range items {
		data, ok := item.(map[string]any)
		if !ok {
			continue
		}
		segments = append(segments, stt.TimedString{
			Text:            payloadString(data, "text"),
			StartTime:       startTimeOffset + payloadFloat(data, "start"),
			EndTime:         startTimeOffset + payloadFloat(data, "end"),
			StartTimeOffset: startTimeOffset,
		})
	}
	return segments
}

func nonNegativeMistralAISTTStreamTime(value float64) float64 {
	if value < 0 {
		return 0
	}
	return value
}

func mistralAISTTRealtimeUsageEvent(requestID string, value any) *stt.SpeechEvent {
	usage, _ := value.(map[string]any)
	return &stt.SpeechEvent{
		Type:      stt.SpeechEventRecognitionUsage,
		RequestID: requestID,
		RecognitionUsage: &stt.RecognitionUsage{
			AudioDuration: payloadFloat(usage, "prompt_audio_seconds"),
			InputTokens:   int(payloadFloat(usage, "prompt_tokens")),
			OutputTokens:  int(payloadFloat(usage, "completion_tokens")),
		},
	}
}

func payloadString(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return value
}

func payloadFloat(payload map[string]any, key string) float64 {
	switch value := payload[key].(type) {
	case float64:
		return value
	case int:
		return float64(value)
	default:
		return 0
	}
}

func validateMistralAISTTAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("mistral AI API key is required. Set MISTRAL_API_KEY or pass api_key")
	}
	return nil
}

func resolveMistralAISTTLanguage(s *MistralAISTT, language string) string {
	if language != "" {
		return language
	}
	return s.language
}

func mistralAISTTIsRealtime(model string) bool {
	return strings.Contains(model, "realtime")
}

type mistralAISTTResponse struct {
	Text     string                `json:"text"`
	Language string                `json:"language"`
	Segments []mistralAISTTSegment `json:"segments"`
}

type mistralAISTTSegment struct {
	Text  string  `json:"text"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

func mistralAISTTSpeechEvent(defaultLanguage string, resp mistralAISTTResponse) *stt.SpeechEvent {
	language := resp.Language
	if language == "" {
		language = defaultLanguage
	}
	return &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			{
				Text:       resp.Text,
				Language:   language,
				Confidence: stt.DefaultTranscriptConfidence(resp.Text),
				StartTime:  mistralAISTTStartTime(resp.Segments),
				EndTime:    mistralAISTTEndTime(resp.Segments),
				Words:      mistralAISTTTimedStrings(resp.Segments),
			},
		},
	}
}

func mistralAISTTStartTime(segments []mistralAISTTSegment) float64 {
	if len(segments) == 0 {
		return 0
	}
	return segments[0].Start
}

func mistralAISTTEndTime(segments []mistralAISTTSegment) float64 {
	if len(segments) == 0 {
		return 0
	}
	return segments[len(segments)-1].End
}

func mistralAISTTTimedStrings(segments []mistralAISTTSegment) []stt.TimedString {
	if len(segments) == 0 {
		return nil
	}
	timed := make([]stt.TimedString, 0, len(segments))
	for _, segment := range segments {
		timed = append(timed, stt.TimedString{
			Text:      segment.Text,
			StartTime: segment.Start,
			EndTime:   segment.End,
		})
	}
	return timed
}
