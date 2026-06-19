package cartesia

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

const (
	defaultCartesiaSTTBaseURL              = "https://api.cartesia.ai"
	defaultCartesiaSTTModel                = "ink-2"
	defaultCartesiaSTTWhisperModel         = "ink-whisper"
	defaultCartesiaSTTSampleRate           = 16000
	defaultCartesiaSTTAudioChunkDurationMS = 160
	defaultCartesiaSTTEncoding             = "pcm_s16le"
	cartesiaSTTAPIVersion                  = "2025-04-16"
)

type CartesiaSTT struct {
	apiKey               string
	wsBaseURL            string
	model                string
	language             string
	encoding             string
	sampleRate           int
	audioChunkDurationMS int
	finalTranscriptMode  string
	mu                   sync.Mutex
	streams              map[*cartesiaSTTStream]struct{}
}

type CartesiaSTTOption func(*CartesiaSTT)

func WithCartesiaSTTBaseURL(baseURL string) CartesiaSTTOption {
	return func(s *CartesiaSTT) {
		if baseURL != "" {
			s.wsBaseURL = cartesiaSTTBaseURLToWSBaseURL(baseURL)
		}
	}
}

func WithCartesiaSTTModel(model string) CartesiaSTTOption {
	return func(s *CartesiaSTT) {
		if model != "" {
			s.model = model
			s.finalTranscriptMode = cartesiaSTTFinalTranscriptMode(model)
		}
	}
}

func WithCartesiaSTTLanguage(language string) CartesiaSTTOption {
	return func(s *CartesiaSTT) {
		s.language = language
	}
}

func WithCartesiaSTTSampleRate(sampleRate int) CartesiaSTTOption {
	return func(s *CartesiaSTT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithCartesiaSTTEncoding(encoding string) CartesiaSTTOption {
	return func(s *CartesiaSTT) {
		if encoding != "" {
			s.encoding = encoding
		}
	}
}

func WithCartesiaSTTAudioChunkDurationMS(durationMS int) CartesiaSTTOption {
	return func(s *CartesiaSTT) {
		if durationMS > 0 {
			s.audioChunkDurationMS = durationMS
		}
	}
}

func NewCartesiaSTT(apiKey string, opts ...CartesiaSTTOption) *CartesiaSTT {
	if apiKey == "" {
		apiKey = os.Getenv("CARTESIA_API_KEY")
	}
	provider := &CartesiaSTT{
		apiKey:               apiKey,
		wsBaseURL:            cartesiaSTTBaseURLToWSBaseURL(defaultCartesiaSTTBaseURL),
		model:                defaultCartesiaSTTModel,
		encoding:             defaultCartesiaSTTEncoding,
		sampleRate:           defaultCartesiaSTTSampleRate,
		audioChunkDurationMS: defaultCartesiaSTTAudioChunkDurationMS,
		finalTranscriptMode:  "auto",
		streams:              make(map[*cartesiaSTTStream]struct{}),
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.model == defaultCartesiaSTTModel && provider.language != "" && cartesiaLanguageBase(provider.language) != "en" {
		provider.model = defaultCartesiaSTTWhisperModel
		provider.finalTranscriptMode = "legacy"
	}
	return provider
}

func (s *CartesiaSTT) Label() string { return "cartesia.STT" }
func (s *CartesiaSTT) Model() string { return s.model }
func (s *CartesiaSTT) Provider() string {
	return "Cartesia"
}
func (s *CartesiaSTT) InputSampleRate() uint32 {
	if s == nil || s.sampleRate <= 0 {
		return defaultCartesiaSTTSampleRate
	}
	return uint32(s.sampleRate)
}
func (s *CartesiaSTT) UpdateOptions(language string) {
	if s == nil || language == "" {
		return
	}
	var streams []*cartesiaSTTStream
	s.mu.Lock()
	s.language = language
	for stream := range s.streams {
		streams = append(streams, stream)
	}
	s.mu.Unlock()
	for _, stream := range streams {
		stream.updateOptions(language)
	}
}
func (s *CartesiaSTT) Capabilities() stt.STTCapabilities {
	legacy := s.finalTranscriptMode == "legacy"
	aligned := ""
	if legacy {
		aligned = "word"
	}
	return stt.STTCapabilities{
		Streaming:         true,
		InterimResults:    !legacy,
		Diarization:       false,
		AlignedTranscript: aligned,
		OfflineRecognize:  false,
	}
}

func (s *CartesiaSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if err := validateCartesiaSTTAPIKey(s.apiKey); err != nil {
		return nil, err
	}

	streamLanguage := s.language
	if language != "" {
		streamLanguage = language
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildCartesiaSTTStreamURLForLanguage(s, streamLanguage), buildCartesiaSTTHeaders(s))
	if err != nil {
		return nil, fmt.Errorf("failed to dial cartesia stt websocket: %w", err)
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &cartesiaSTTStream{
		provider:     s,
		conn:         conn,
		events:       make(chan *stt.SpeechEvent, 100),
		errCh:        make(chan error, 1),
		ctx:          streamCtx,
		cancel:       cancel,
		audioBStream: newCartesiaSTTAudioByteStream(s.sampleRate, s.audioChunkDurationMS),
		state: &cartesiaSTTStreamState{
			language: cartesiaLanguageOrDefault(streamLanguage),
			mode:     s.finalTranscriptMode,
		},
	}
	stream.writeBinary = stream.writeBinaryMessage
	stream.writeText = stream.writeTextMessage
	stream.closeConn = stream.closeWebsocketConn
	s.registerStream(stream)
	go stream.readLoop()
	return stream, nil
}

func (s *CartesiaSTT) registerStream(stream *cartesiaSTTStream) {
	if s == nil || stream == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streams == nil {
		s.streams = make(map[*cartesiaSTTStream]struct{})
	}
	s.streams[stream] = struct{}{}
}

func (s *CartesiaSTT) unregisterStream(stream *cartesiaSTTStream) {
	if s == nil || stream == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.streams, stream)
}

func (s *CartesiaSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	return nil, fmt.Errorf("cartesia stt does not support batch recognition")
}

func validateCartesiaSTTAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("cartesia API key is required, either as argument or set CARTESIA_API_KEY environment variable")
	}
	return nil
}

type cartesiaSTTStream struct {
	provider *CartesiaSTT
	conn     *websocket.Conn
	events   chan *stt.SpeechEvent
	errCh    chan error
	mu       sync.Mutex
	closed   bool
	ctx      context.Context
	cancel   context.CancelFunc
	state    *cartesiaSTTStreamState

	audioBStream *audio.AudioByteStream
	writeBinary  func([]byte) error
	writeText    func([]byte) error
	closeConn    func() error
}

func (s *cartesiaSTTStream) PushFrame(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	if s.audioBStream == nil {
		s.audioBStream = newCartesiaSTTAudioByteStream(int(frame.SampleRate), defaultCartesiaSTTAudioChunkDurationMS)
	}
	for _, chunk := range s.audioBStream.Write(frame.Data) {
		if err := s.writeBinaryData(chunk.Data); err != nil {
			return err
		}
	}
	return nil
}

func (s *cartesiaSTTStream) Flush() error {
	if s.state.mode == "legacy" {
		if s.audioBStream != nil {
			for _, chunk := range s.audioBStream.Flush() {
				if err := s.writeBinaryData(chunk.Data); err != nil {
					return err
				}
			}
		}
		return s.writeTextData([]byte("finalize"))
	}
	return nil
}

func (s *cartesiaSTTStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	if s.audioBStream != nil {
		for _, chunk := range s.audioBStream.Flush() {
			if err := s.writeBinaryData(chunk.Data); err != nil {
				return err
			}
		}
	}
	closeMessage := `{"type":"close"}`
	if s.state.mode == "legacy" {
		closeMessage = "close"
	}
	_ = s.writeTextData([]byte(closeMessage))
	if s.provider != nil {
		s.provider.unregisterStream(s)
	}
	return s.closeConnection()
}

func (s *cartesiaSTTStream) writeBinaryData(data []byte) error {
	if s.writeBinary != nil {
		return s.writeBinary(data)
	}
	return s.writeBinaryMessage(data)
}

func (s *cartesiaSTTStream) writeBinaryMessage(data []byte) error {
	return s.conn.WriteMessage(websocket.BinaryMessage, data)
}

func (s *cartesiaSTTStream) writeTextData(data []byte) error {
	if s.writeText != nil {
		return s.writeText(data)
	}
	return s.writeTextMessage(data)
}

func (s *cartesiaSTTStream) writeTextMessage(data []byte) error {
	return s.conn.WriteMessage(websocket.TextMessage, data)
}

func (s *cartesiaSTTStream) closeConnection() error {
	if s.closeConn != nil {
		return s.closeConn()
	}
	return s.closeWebsocketConn()
}

func (s *cartesiaSTTStream) closeWebsocketConn() error {
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *cartesiaSTTStream) Next() (*stt.SpeechEvent, error) {
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

func (s *cartesiaSTTStream) readLoop() {
	defer close(s.events)
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			if !s.isClosed() {
				for _, event := range cartesiaSTTUnexpectedCloseEvents(s.state) {
					s.events <- event
				}
				s.errCh <- cartesiaSTTUnexpectedCloseError(err)
			}
			return
		}
		if msgType != websocket.TextMessage {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal(payload, &data); err != nil {
			continue
		}
		events, err := processCartesiaSTTEvent(s.state, data)
		if err != nil {
			s.errCh <- err
			return
		}
		for _, event := range events {
			s.events <- event
		}
	}
}

func (s *cartesiaSTTStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *cartesiaSTTStream) updateOptions(language string) {
	if s == nil || language == "" || s.state == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.language = cartesiaLanguageBase(language)
}

func cartesiaSTTUnexpectedCloseError(err error) error {
	message := "Cartesia STT connection closed unexpectedly"
	if err != nil && err != io.EOF {
		message += ": " + err.Error()
	}
	return llm.NewAPIConnectionError(message)
}

func cartesiaSTTUnexpectedCloseEvents(state *cartesiaSTTStreamState) []*stt.SpeechEvent {
	if state == nil || state.mode != "auto" || !state.speaking {
		return nil
	}
	events := []*stt.SpeechEvent{}
	if state.speechDuration > 0 {
		events = append(events, cartesiaUsageEvent(state))
		state.speechDuration = 0
	}
	if state.currentTranscript != "" {
		events = append(events, cartesiaTranscriptEvent(stt.SpeechEventFinalTranscript, state, state.currentTranscript))
	}
	events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech, RequestID: state.requestID})
	state.speaking = false
	state.currentTranscript = ""
	return events
}

type cartesiaSTTStreamState struct {
	language          string
	requestID         string
	mode              string
	speaking          bool
	currentTranscript string
	speechDuration    float64
	lastSpeechEndTime float64
	startTimeOffset   float64
}

func buildCartesiaSTTStreamURL(s *CartesiaSTT) string {
	return buildCartesiaSTTStreamURLForLanguage(s, s.language)
}

func buildCartesiaSTTStreamURLForLanguage(s *CartesiaSTT, language string) string {
	path := "/stt/turns/websocket"
	if s.finalTranscriptMode == "legacy" {
		path = "/stt/websocket"
	}
	u, _ := url.Parse(strings.TrimRight(s.wsBaseURL, "/") + path)
	q := u.Query()
	q.Set("model", s.model)
	q.Set("sample_rate", fmt.Sprintf("%d", s.sampleRate))
	q.Set("encoding", s.encoding)
	if s.finalTranscriptMode == "legacy" && language != "" {
		q.Set("language", cartesiaLanguageBase(language))
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func buildCartesiaSTTHeaders(s *CartesiaSTT) http.Header {
	headers := make(http.Header)
	headers.Set("Cartesia-Version", cartesiaSTTAPIVersion)
	headers.Set("X-API-Key", s.apiKey)
	headers.Set("User-Agent", "LiveKit Agents Cartesia Plugin/Go")
	return headers
}

func processCartesiaSTTEvent(state *cartesiaSTTStreamState, data map[string]any) ([]*stt.SpeechEvent, error) {
	if requestID, _ := data["request_id"].(string); requestID != "" {
		state.requestID = requestID
	}
	if state.mode == "legacy" {
		return processCartesiaLegacySTTEvent(state, data)
	}
	return processCartesiaAutoSTTEvent(state, data)
}

func processCartesiaAutoSTTEvent(state *cartesiaSTTStreamState, data map[string]any) ([]*stt.SpeechEvent, error) {
	eventType, _ := data["type"].(string)
	switch eventType {
	case "connected":
		return nil, nil
	case "turn.start":
		if state.speaking {
			return nil, nil
		}
		state.speaking = true
		state.currentTranscript = ""
		return []*stt.SpeechEvent{{Type: stt.SpeechEventStartOfSpeech, RequestID: state.requestID}}, nil
	case "turn.update":
		if !state.speaking {
			return nil, nil
		}
		transcript, _ := data["transcript"].(string)
		if transcript == "" || transcript == state.currentTranscript {
			return nil, nil
		}
		state.currentTranscript = transcript
		return []*stt.SpeechEvent{cartesiaTranscriptEvent(stt.SpeechEventInterimTranscript, state, transcript)}, nil
	case "turn.eager_end":
		if !state.speaking {
			return nil, nil
		}
		transcript, _ := data["transcript"].(string)
		if transcript == "" {
			transcript = state.currentTranscript
		}
		if transcript == "" {
			return nil, nil
		}
		state.currentTranscript = transcript
		return []*stt.SpeechEvent{cartesiaTranscriptEvent(stt.SpeechEventPreflightTranscript, state, transcript)}, nil
	case "turn.resume":
		if !state.speaking || state.currentTranscript == "" {
			return nil, nil
		}
		return []*stt.SpeechEvent{cartesiaTranscriptEvent(stt.SpeechEventInterimTranscript, state, state.currentTranscript)}, nil
	case "turn.end":
		if !state.speaking {
			return nil, nil
		}
		transcript, _ := data["transcript"].(string)
		if transcript == "" {
			transcript = state.currentTranscript
		}
		events := []*stt.SpeechEvent{}
		if state.speechDuration > 0 {
			events = append(events, cartesiaUsageEvent(state))
			state.speechDuration = 0
		}
		events = append(events, cartesiaTranscriptEvent(stt.SpeechEventFinalTranscript, state, transcript))
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech, RequestID: state.requestID})
		state.speaking = false
		state.currentTranscript = ""
		return events, nil
	case "error":
		return nil, cartesiaSTTError(data, "status_code")
	default:
		return nil, nil
	}
}

func processCartesiaLegacySTTEvent(state *cartesiaSTTStreamState, data map[string]any) ([]*stt.SpeechEvent, error) {
	eventType, _ := data["type"].(string)
	switch eventType {
	case "transcript":
		text, _ := data["text"].(string)
		isFinal, _ := data["is_final"].(bool)
		if text == "" && !isFinal {
			return nil, nil
		}
		events := []*stt.SpeechEvent{}
		if !state.speaking {
			state.speaking = true
			events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech, RequestID: state.requestID})
		}
		speechData := cartesiaLegacySpeechData(state, data, text)
		if isFinal {
			if state.speechDuration > 0 {
				events = append(events, cartesiaUsageEvent(state))
				state.speechDuration = 0
			}
			events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventFinalTranscript, RequestID: state.requestID, Alternatives: []stt.SpeechData{speechData}})
			if state.speaking {
				state.speaking = false
				events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech, RequestID: state.requestID})
			}
		} else {
			events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventInterimTranscript, RequestID: state.requestID, Alternatives: []stt.SpeechData{speechData}})
		}
		return events, nil
	case "flush_done", "done":
		return nil, nil
	case "error":
		return nil, cartesiaSTTError(data, "code")
	default:
		return nil, nil
	}
}

func cartesiaLegacySpeechData(state *cartesiaSTTStreamState, data map[string]any, text string) stt.SpeechData {
	if state.lastSpeechEndTime == 0 {
		state.lastSpeechEndTime = state.startTimeOffset
	}
	start := state.lastSpeechEndTime
	duration := cartesiaAnyFloat(data["duration"])
	end := start + duration
	state.lastSpeechEndTime = end
	words := cartesiaWordsFromAny(data["words"], state.startTimeOffset)
	return stt.SpeechData{
		Language:   state.languageOrDefault(),
		Text:       text,
		Confidence: stt.DefaultTranscriptConfidence(text),
		StartTime:  start,
		EndTime:    end,
		Words:      words,
	}
}

func cartesiaTranscriptEvent(eventType stt.SpeechEventType, state *cartesiaSTTStreamState, transcript string) *stt.SpeechEvent {
	return &stt.SpeechEvent{
		Type:      eventType,
		RequestID: state.requestID,
		Alternatives: []stt.SpeechData{{
			Text:       transcript,
			Language:   state.languageOrDefault(),
			Confidence: stt.DefaultTranscriptConfidence(transcript),
		}},
	}
}

func cartesiaUsageEvent(state *cartesiaSTTStreamState) *stt.SpeechEvent {
	return &stt.SpeechEvent{
		Type:      stt.SpeechEventRecognitionUsage,
		RequestID: state.requestID,
		RecognitionUsage: &stt.RecognitionUsage{
			AudioDuration: state.speechDuration,
		},
	}
}

func cartesiaSTTError(data map[string]any, codeKey string) error {
	status := int(cartesiaAnyFloat(data[codeKey]))
	message, _ := data["message"].(string)
	if message == "" {
		message, _ = data["title"].(string)
	}
	if message == "" {
		message = "unknown error from cartesia"
	}
	if status >= 500 || status == 0 {
		return fmt.Errorf("cartesia stt error %d: %s", status, message)
	}
	return nil
}

func cartesiaWordsFromAny(raw any, startTimeOffset float64) []stt.TimedString {
	rawWords, ok := raw.([]any)
	if !ok {
		return nil
	}
	words := make([]stt.TimedString, 0, len(rawWords))
	for _, rawWord := range rawWords {
		wordMap, ok := rawWord.(map[string]any)
		if !ok {
			continue
		}
		words = append(words, stt.TimedString{
			Text:            cartesiaAnyString(wordMap["word"]),
			StartTime:       cartesiaAnyFloat(wordMap["start"]) + startTimeOffset,
			EndTime:         cartesiaAnyFloat(wordMap["end"]) + startTimeOffset,
			StartTimeOffset: startTimeOffset,
		})
	}
	return words
}

func cartesiaSTTBaseURLToWSBaseURL(baseURL string) string {
	if strings.HasPrefix(baseURL, "http://") || strings.HasPrefix(baseURL, "https://") {
		return strings.Replace(baseURL, "http", "ws", 1)
	}
	return "wss://" + baseURL
}

func cartesiaSTTFinalTranscriptMode(model string) string {
	if strings.HasPrefix(model, "ink-whisper") {
		return "legacy"
	}
	return "auto"
}

func cartesiaLanguageBase(language string) string {
	if idx := strings.Index(language, "-"); idx >= 0 {
		return language[:idx]
	}
	return language
}

func (s *CartesiaSTT) languageOrDefault() string {
	return cartesiaLanguageOrDefault(s.language)
}

func cartesiaLanguageOrDefault(language string) string {
	if language != "" {
		return cartesiaLanguageBase(language)
	}
	return "en"
}

func newCartesiaSTTAudioByteStream(sampleRate int, durationMS int) *audio.AudioByteStream {
	if sampleRate <= 0 {
		sampleRate = defaultCartesiaSTTSampleRate
	}
	if durationMS <= 0 {
		durationMS = defaultCartesiaSTTAudioChunkDurationMS
	}
	samplesPerChannel := uint32(sampleRate * durationMS / 1000)
	return audio.NewAudioByteStream(uint32(sampleRate), 1, samplesPerChannel)
}

func (s *cartesiaSTTStreamState) languageOrDefault() string {
	if s.language != "" {
		return cartesiaLanguageBase(s.language)
	}
	return "en"
}

func cartesiaAnyString(value any) string {
	str, _ := value.(string)
	return str
}

func cartesiaAnyFloat(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	default:
		return 0
	}
}
