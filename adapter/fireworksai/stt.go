package fireworksai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/gorilla/websocket"
)

const (
	defaultBaseURL            = "wss://audio-streaming.us-virginia-1.direct.fireworks.ai/v1"
	defaultSampleRate         = 16000
	defaultTextTimeoutSeconds = 1.0
	defaultResponseFormat     = "verbose_json"
	streamingPath             = "/audio_streaming"
	closeMessage              = `{"checkpoint_id":"final"}`
)

type FireworksSTT struct {
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
}

type FireworksSTTOption func(*FireworksSTT)

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
		if seconds > 0 {
			s.textTimeoutSeconds = seconds
		}
	}
}

func WithFireworksTimestampGranularities(granularities []string) FireworksSTTOption {
	return func(s *FireworksSTT) {
		s.timestampGranularities = granularities
	}
}

func NewFireworksSTT(apiKey string, opts ...FireworksSTTOption) *FireworksSTT {
	provider := &FireworksSTT{
		apiKey:             apiKey,
		baseURL:            defaultBaseURL,
		sampleRate:         defaultSampleRate,
		textTimeoutSeconds: defaultTextTimeoutSeconds,
		responseFormat:     defaultResponseFormat,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *FireworksSTT) Label() string { return "fireworks.STT" }
func (s *FireworksSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: false, OfflineRecognize: false}
}

func (s *FireworksSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if language != "" {
		s.language = language
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildFireworksStreamURL(s), buildFireworksStreamHeaders(s))
	if err != nil {
		return nil, fmt.Errorf("failed to dial fireworks stt websocket: %w", err)
	}

	streamCtx, cancel := context.WithCancel(ctx)
	stream := &fireworksStream{
		conn:   conn,
		events: make(chan *stt.SpeechEvent, 100),
		errCh:  make(chan error, 1),
		ctx:    streamCtx,
		cancel: cancel,
		state: &fireworksStreamState{
			language:            s.language,
			transcriptState:     map[int]string{},
			finalSegmentsLength: map[int]int{},
			lastFinalSegmentID:  -1,
		},
	}
	go stream.readLoop()
	return stream, nil
}

func (s *FireworksSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	return nil, fmt.Errorf("fireworksai stt does not support batch recognition, use stream instead")
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
	conn   *websocket.Conn
	events chan *stt.SpeechEvent
	errCh  chan error
	mu     sync.Mutex
	closed bool

	ctx    context.Context
	cancel context.CancelFunc
	state  *fireworksStreamState
}

func (s *fireworksStream) readLoop() {
	defer close(s.events)
	for {
		msgType, message, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) && err != io.EOF {
				s.errCh <- err
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

func (s *fireworksStream) PushFrame(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, frame.Data)
}

func (s *fireworksStream) Flush() error {
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
			{Language: state.language, Text: fullTranscript},
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
