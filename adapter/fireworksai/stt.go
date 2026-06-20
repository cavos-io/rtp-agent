package fireworksai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

const (
	defaultBaseURL             = "wss://audio-streaming.us-virginia-1.direct.fireworks.ai/v1"
	defaultSampleRate          = 16000
	defaultTextTimeoutSeconds  = 1.0
	minTextTimeoutSeconds      = 1.0
	maxTextTimeoutSeconds      = 29.0
	defaultResponseFormat      = "verbose_json"
	streamingPath              = "/audio/transcriptions/streaming"
	closeMessage               = `{"checkpoint_id":"final"}`
	fireworksPCMBytesPerSample = 2
)

type FireworksSTT struct {
	mu                     sync.Mutex
	apiKey                 string
	baseURL                string
	model                  string
	sampleRate             int
	language               string
	prompt                 string
	temperature            *float64
	skipVAD                *bool
	vadKwargs              map[string]any
	textTimeoutSeconds     float64
	responseFormat         string
	timestampGranularities []string
	dialWebsocket          fireworksSTTWebsocketDialer
	streams                map[*fireworksStream]struct{}
}

type FireworksSTTOption func(*FireworksSTT)
type fireworksSTTWebsocketDialer func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error)

func WithFireworksBaseURL(baseURL string) FireworksSTTOption {
	return func(s *FireworksSTT) {
		if baseURL != "" {
			s.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithFireworksModel(model string) FireworksSTTOption {
	return func(s *FireworksSTT) {
		s.model = model
	}
}

func WithFireworksLanguage(language string) FireworksSTTOption {
	return func(s *FireworksSTT) {
		s.language = language
	}
}

func WithFireworksPrompt(prompt string) FireworksSTTOption {
	return func(s *FireworksSTT) {
		s.prompt = prompt
	}
}

func WithFireworksTemperature(temperature float64) FireworksSTTOption {
	return func(s *FireworksSTT) {
		s.temperature = &temperature
	}
}

func WithFireworksSkipVAD(skip bool) FireworksSTTOption {
	return func(s *FireworksSTT) {
		s.skipVAD = &skip
	}
}

func WithFireworksVADKwargs(vadKwargs map[string]any) FireworksSTTOption {
	return func(s *FireworksSTT) {
		s.vadKwargs = vadKwargs
	}
}

func WithFireworksTextTimeoutSeconds(seconds float64) FireworksSTTOption {
	return func(s *FireworksSTT) {
		if validFireworksTextTimeoutSeconds(seconds) {
			s.textTimeoutSeconds = seconds
		}
	}
}

func validFireworksTextTimeoutSeconds(seconds float64) bool {
	return seconds >= minTextTimeoutSeconds && seconds <= maxTextTimeoutSeconds
}

func WithFireworksTimestampGranularities(granularities []string) FireworksSTTOption {
	return func(s *FireworksSTT) {
		s.timestampGranularities = granularities
	}
}

func withFireworksSTTWebsocketDialer(dialer fireworksSTTWebsocketDialer) FireworksSTTOption {
	return func(s *FireworksSTT) {
		if dialer != nil {
			s.dialWebsocket = dialer
		}
	}
}

func NewFireworksSTT(apiKey string, opts ...FireworksSTTOption) *FireworksSTT {
	if apiKey == "" {
		apiKey = os.Getenv("FIREWORKS_API_KEY")
	}
	provider := &FireworksSTT{
		apiKey:             apiKey,
		baseURL:            defaultBaseURL,
		sampleRate:         defaultSampleRate,
		textTimeoutSeconds: defaultTextTimeoutSeconds,
		responseFormat:     defaultResponseFormat,
		dialWebsocket:      defaultFireworksSTTWebsocketDialer,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *FireworksSTT) Label() string { return "fireworks.STT" }
func (s *FireworksSTT) Model() string {
	if s.model == "" {
		return "unknown"
	}
	return s.model
}
func (s *FireworksSTT) Provider() string { return "FireworksAI" }
func (s *FireworksSTT) InputSampleRate() uint32 {
	return uint32(s.sampleRate)
}

func (s *FireworksSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: false, OfflineRecognize: false}
}

func (s *FireworksSTT) UpdateOptions(opts ...FireworksSTTOption) {
	if s == nil {
		return
	}
	s.mu.Lock()
	for _, opt := range opts {
		opt(s)
	}
	streams := make([]*fireworksStream, 0, len(s.streams))
	for stream := range s.streams {
		streams = append(streams, stream)
	}
	endpoint := buildFireworksStreamURL(s)
	headers := buildFireworksStreamHeaders(s)
	dialer := s.dialWebsocket
	language := s.language
	s.mu.Unlock()

	for _, stream := range streams {
		stream.updateOptions(endpoint, headers, dialer, language)
	}
}

func (s *FireworksSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if err := validateFireworksAPIKey(s.apiKey); err != nil {
		return nil, err
	}

	s.mu.Lock()
	if language != "" {
		s.language = language
	}
	endpoint := buildFireworksStreamURL(s)
	headers := buildFireworksStreamHeaders(s)
	dialer := s.dialWebsocket
	streamLanguage := s.language
	s.mu.Unlock()

	conn, _, err := dialer(ctx, endpoint, headers)
	if err != nil {
		return nil, fmt.Errorf("failed to dial fireworks stt websocket: %w", err)
	}

	streamCtx, cancel := context.WithCancel(ctx)
	stream := &fireworksStream{
		owner:  s,
		conn:   conn,
		events: make(chan *stt.SpeechEvent, 100),
		errCh:  make(chan error, 1),
		ctx:    streamCtx,
		cancel: cancel,
		state: &fireworksStreamState{
			language:            streamLanguage,
			transcriptState:     map[int]string{},
			finalSegmentsLength: map[int]int{},
			lastFinalSegmentID:  -1,
		},
	}
	s.registerStream(stream)
	go stream.readLoop(conn)
	return stream, nil
}

func (s *FireworksSTT) registerStream(stream *fireworksStream) {
	if s == nil || stream == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streams == nil {
		s.streams = make(map[*fireworksStream]struct{})
	}
	s.streams[stream] = struct{}{}
}

func (s *FireworksSTT) unregisterStream(stream *fireworksStream) {
	if s == nil || stream == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.streams, stream)
}

func defaultFireworksSTTWebsocketDialer(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
	return websocket.DefaultDialer.DialContext(ctx, endpoint, headers)
}

func (s *FireworksSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	return nil, fmt.Errorf("fireworksai stt does not support batch recognition, use stream instead")
}

func validateFireworksAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("fireworks API key is required, either as argument or set FIREWORKS_API_KEY environment variable")
	}
	return nil
}

func buildFireworksStreamHeaders(s *FireworksSTT) http.Header {
	headers := make(http.Header)
	headers.Set("User-Agent", "LiveKit Agents")
	headers.Set("Authorization", s.apiKey)
	return headers
}

func buildFireworksStreamURL(s *FireworksSTT) string {
	base := strings.TrimRight(s.baseURL, "/") + streamingPath
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	query := u.Query()
	setOptionalString(query, "model", s.model)
	setOptionalString(query, "language", s.language)
	setOptionalString(query, "prompt", s.prompt)
	if s.temperature != nil {
		query.Set("temperature", strconv.FormatFloat(*s.temperature, 'f', -1, 64))
	}
	if s.skipVAD != nil {
		query.Set("skip_vad", strconv.FormatBool(*s.skipVAD))
	}
	if len(s.vadKwargs) > 0 {
		if payload, err := json.Marshal(s.vadKwargs); err == nil {
			query.Set("vad_kwargs", string(payload))
		}
	}
	query.Set("text_timeout_seconds", strconv.FormatFloat(s.textTimeoutSeconds, 'f', -1, 64))
	query.Set("response_format", s.responseFormat)
	for _, granularity := range s.timestampGranularities {
		query.Add("timestamp_granularities", granularity)
	}
	u.RawQuery = query.Encode()
	return u.String()
}

func setOptionalString(values url.Values, key string, value string) {
	if value != "" {
		values.Set(key, value)
	}
}

type fireworksStream struct {
	owner        *FireworksSTT
	conn         *websocket.Conn
	events       chan *stt.SpeechEvent
	errCh        chan error
	mu           sync.Mutex
	closed       bool
	reconnecting bool

	ctx    context.Context
	cancel context.CancelFunc
	state  *fireworksStreamState
	audio  bytes.Buffer
}

func (s *fireworksStream) readLoop(conn *websocket.Conn) {
	closeEvents := false
	defer func() {
		if closeEvents {
			if s.owner != nil {
				s.owner.unregisterStream(s)
			}
			close(s.events)
		}
	}()
	for {
		msgType, message, err := conn.ReadMessage()
		if err != nil {
			s.mu.Lock()
			current := s.conn == conn
			closed := s.closed
			reconnecting := s.reconnecting
			if current && !reconnecting {
				closeEvents = true
			}
			s.mu.Unlock()
			if current && !closed && !reconnecting {
				if closeErr, ok := err.(*websocket.CloseError); ok {
					s.errCh <- llm.NewAPIStatusError("Fireworks connection closed unexpectedly", closeErr.Code, "", err.Error())
				} else if err == io.EOF {
					s.errCh <- llm.NewAPIStatusError("Fireworks connection closed unexpectedly", -1, "", err.Error())
				} else {
					s.errCh <- err
				}
			}
			return
		}
		if msgType != websocket.TextMessage {
			continue
		}
		var event fireworksStreamEvent
		if err := json.Unmarshal(message, &event); err != nil {
			continue
		}
		for _, speechEvent := range processFireworksStreamEvent(s.state, event, false) {
			s.events <- speechEvent
		}
	}
}

func (s *fireworksStream) updateOptions(endpoint string, headers http.Header, dialer fireworksSTTWebsocketDialer, language string) {
	if dialer == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	oldConn := s.conn
	s.reconnecting = true
	s.mu.Unlock()

	if oldConn != nil {
		_ = oldConn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		_ = oldConn.Close()
	}
	newConn, _, err := dialer(s.ctx, endpoint, headers)
	if err != nil {
		s.mu.Lock()
		if !s.closed {
			s.reconnecting = false
			select {
			case s.errCh <- fmt.Errorf("failed to reconnect fireworks stt websocket: %w", err):
			default:
			}
		}
		s.mu.Unlock()
		return
	}

	s.mu.Lock()
	if s.closed {
		s.reconnecting = false
		s.mu.Unlock()
		_ = newConn.Close()
		return
	}
	s.conn = newConn
	s.state.language = language
	s.reconnecting = false
	if err := s.writeBufferedAudioLocked(false); err != nil {
		select {
		case s.errCh <- err:
		default:
		}
	}
	s.mu.Unlock()
	go s.readLoop(newConn)
}

func (s *fireworksStream) PushFrame(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("fireworks stt stream is closed")
	}
	if _, err := s.audio.Write(frame.Data); err != nil {
		return err
	}
	if s.reconnecting || s.conn == nil {
		return nil
	}
	return s.writeBufferedAudioLocked(false)
}

func (s *fireworksStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("fireworks stt stream is closed")
	}
	if s.audio.Len() == 0 {
		return nil
	}
	if s.reconnecting || s.conn == nil {
		return nil
	}
	return s.writeBufferedAudioLocked(true)
}

func (s *fireworksStream) writeBufferedAudioLocked(flush bool) error {
	if s.conn == nil {
		return nil
	}
	chunkBytes := defaultSampleRate / 20 * fireworksPCMBytesPerSample
	for s.audio.Len() >= chunkBytes {
		chunk := make([]byte, chunkBytes)
		if _, err := s.audio.Read(chunk); err != nil {
			return err
		}
		if err := s.conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
			return err
		}
	}
	if !flush || s.audio.Len() == 0 {
		return nil
	}
	chunk := bytes.Clone(s.audio.Bytes())
	s.audio.Reset()
	if err := s.conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
		return err
	}
	return nil
}

func (s *fireworksStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	_ = s.conn.WriteMessage(websocket.TextMessage, []byte(closeMessage))
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	if s.owner != nil {
		s.owner.unregisterStream(s)
	}
	return s.conn.Close()
}

func (s *fireworksStream) Next() (*stt.SpeechEvent, error) {
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

type fireworksStreamState struct {
	language            string
	transcriptState     map[int]string
	speaking            bool
	finalSegmentsLength map[int]int
	lastFinalSegmentID  int
}

type fireworksStreamEvent struct {
	Segments []fireworksSegment `json:"segments"`
}

type fireworksSegment struct {
	ID    int             `json:"id"`
	Text  string          `json:"text"`
	Words []fireworksWord `json:"words"`
}

type fireworksWord struct {
	Word    string `json:"word"`
	IsFinal bool   `json:"is_final"`
}

func processFireworksStreamEvent(state *fireworksStreamState, event fireworksStreamEvent, endOnFinal bool) []*stt.SpeechEvent {
	if state.transcriptState == nil {
		state.transcriptState = map[int]string{}
	}
	if state.finalSegmentsLength == nil {
		state.finalSegmentsLength = map[int]int{}
	}
	if len(event.Segments) == 0 {
		return nil
	}

	latest := event.Segments[0]
	for _, segment := range event.Segments {
		if segment.ID > latest.ID {
			latest = segment
		}
		if segment.ID < state.lastFinalSegmentID {
			continue
		}
		if segment.ID == state.lastFinalSegmentID {
			finalizedWordCount := state.finalSegmentsLength[segment.ID]
			if finalizedWordCount < len(segment.Words) {
				newWords := segment.Words[finalizedWordCount:]
				words := make([]string, 0, len(newWords))
				for _, word := range newWords {
					if word.Word != "" {
						words = append(words, word.Word)
					}
				}
				state.transcriptState[segment.ID] = strings.TrimSpace(strings.Join(words, " "))
			} else {
				delete(state.transcriptState, segment.ID)
			}
			continue
		}
		state.transcriptState[segment.ID] = segment.Text
	}
	for segmentID := range state.transcriptState {
		if segmentID > latest.ID {
			delete(state.transcriptState, segmentID)
		}
	}

	fullTranscript := fullFireworksTranscript(state.transcriptState)
	if fullTranscript == "" {
		return nil
	}

	var events []*stt.SpeechEvent
	if !state.speaking {
		state.speaking = true
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech})
	}

	eventType := stt.SpeechEventInterimTranscript
	if latestFinal(latest) {
		eventType = stt.SpeechEventFinalTranscript
	}
	events = append(events, &stt.SpeechEvent{
		Type: eventType,
		Alternatives: []stt.SpeechData{
			{Language: state.language, Text: fullTranscript, Confidence: stt.DefaultTranscriptConfidence(fullTranscript)},
		},
	})
	if eventType == stt.SpeechEventFinalTranscript {
		state.transcriptState = map[int]string{}
		state.lastFinalSegmentID = latest.ID
		state.finalSegmentsLength[latest.ID] = len(latest.Words)
		if endOnFinal {
			state.speaking = false
			events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech})
		}
	}
	return events
}

func fullFireworksTranscript(segments map[int]string) string {
	if len(segments) == 0 {
		return ""
	}
	ids := make([]int, 0, len(segments))
	for id := range segments {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	texts := make([]string, 0, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(segments[id]) != "" {
			texts = append(texts, strings.TrimSpace(segments[id]))
		}
	}
	return strings.TrimSpace(strings.Join(texts, " "))
}

func latestFinal(segment fireworksSegment) bool {
	if len(segment.Words) == 0 {
		return false
	}
	return segment.Words[len(segment.Words)-1].IsFinal
}
