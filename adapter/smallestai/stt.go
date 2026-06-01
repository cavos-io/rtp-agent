package smallestai

import (
	"bytes"
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

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

const (
	defaultSmallestAISTTBaseURL      = "https://api.smallest.ai/waves/v1"
	defaultSmallestAISTTModel        = "pulse"
	defaultSmallestAISTTLanguage     = "en"
	defaultSmallestAISTTSampleRate   = 16000
	defaultSmallestAISTTEncoding     = "linear16"
	defaultSmallestAISTTEOUTimeoutMS = 0
)

type SmallestAISTT struct {
	apiKey         string
	baseURL        string
	model          string
	language       string
	sampleRate     int
	encoding       string
	wordTimestamps bool
	diarize        bool
	eouTimeoutMS   int
}

type SmallestAISTTOption func(*SmallestAISTT)

func WithSmallestAISTTBaseURL(baseURL string) SmallestAISTTOption {
	return func(s *SmallestAISTT) {
		if baseURL != "" {
			s.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithSmallestAISTTModel(model string) SmallestAISTTOption {
	return func(s *SmallestAISTT) {
		if model != "" {
			s.model = model
		}
	}
}

func WithSmallestAISTTLanguage(language string) SmallestAISTTOption {
	return func(s *SmallestAISTT) {
		if language != "" {
			s.language = language
		}
	}
}

func WithSmallestAISTTSampleRate(sampleRate int) SmallestAISTTOption {
	return func(s *SmallestAISTT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithSmallestAISTTEncoding(encoding string) SmallestAISTTOption {
	return func(s *SmallestAISTT) {
		if encoding != "" {
			s.encoding = encoding
		}
	}
}

func WithSmallestAISTTWordTimestamps(enabled bool) SmallestAISTTOption {
	return func(s *SmallestAISTT) {
		s.wordTimestamps = enabled
	}
}

func WithSmallestAISTTDiarize(enabled bool) SmallestAISTTOption {
	return func(s *SmallestAISTT) {
		s.diarize = enabled
	}
}

func WithSmallestAISTTEOUTimeoutMS(timeoutMS int) SmallestAISTTOption {
	return func(s *SmallestAISTT) {
		if timeoutMS >= 0 {
			s.eouTimeoutMS = timeoutMS
		}
	}
}

func NewSmallestAISTT(apiKey string, opts ...SmallestAISTTOption) *SmallestAISTT {
	provider := &SmallestAISTT{
		apiKey:         apiKey,
		baseURL:        defaultSmallestAISTTBaseURL,
		model:          defaultSmallestAISTTModel,
		language:       defaultSmallestAISTTLanguage,
		sampleRate:     defaultSmallestAISTTSampleRate,
		encoding:       defaultSmallestAISTTEncoding,
		wordTimestamps: true,
		diarize:        false,
		eouTimeoutMS:   defaultSmallestAISTTEOUTimeoutMS,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *SmallestAISTT) Label() string { return "smallestai.STT" }
func (s *SmallestAISTT) Capabilities() stt.STTCapabilities {
	aligned := ""
	if s.wordTimestamps {
		aligned = "word"
	}
	return stt.STTCapabilities{
		Streaming:         true,
		InterimResults:    true,
		Diarization:       s.diarize,
		AlignedTranscript: aligned,
		OfflineRecognize:  true,
	}
}

func (s *SmallestAISTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildSmallestAISTTStreamURL(s, language), buildSmallestAISTTHeaders(s))
	if err != nil {
		return nil, fmt.Errorf("failed to dial smallestai stt websocket: %w", err)
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &smallestAISTTStream{
		conn:   conn,
		events: make(chan *stt.SpeechEvent, 100),
		errCh:  make(chan error, 1),
		ctx:    streamCtx,
		cancel: cancel,
		state:  &smallestAISTTStreamState{language: resolveSmallestAISTTLanguage(s, language), diarize: s.diarize},
	}
	go stream.readLoop()
	return stream, nil
}

func (s *SmallestAISTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	var audio bytes.Buffer
	for _, frame := range frames {
		audio.Write(frame.Data)
	}
	req, err := buildSmallestAISTTRecognizeRequest(ctx, s, audio.Bytes(), language)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("smallestai stt error: %s", string(respBody))
	}
	var result smallestAIBatchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return smallestAIBatchSpeechEvent(resolveSmallestAISTTLanguage(s, language), result), nil
}

func buildSmallestAISTTRecognizeRequest(ctx context.Context, s *SmallestAISTT, audio []byte, language string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, buildSmallestAISTTHTTPURL(s, language), bytes.NewReader(audio))
	if err != nil {
		return nil, err
	}
	for key, values := range buildSmallestAISTTHeaders(s) {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	return req, nil
}

func buildSmallestAISTTHTTPURL(s *SmallestAISTT, language string) string {
	u, _ := url.Parse(strings.TrimRight(s.baseURL, "/") + "/" + s.model + "/get_text")
	q := smallestAISTTQuery(s, language, false)
	u.RawQuery = q.Encode()
	return u.String()
}

func buildSmallestAISTTStreamURL(s *SmallestAISTT, language string) string {
	streamBase := strings.TrimRight(s.baseURL, "/")
	streamBase = strings.Replace(streamBase, "https://", "wss://", 1)
	streamBase = strings.Replace(streamBase, "http://", "ws://", 1)
	u, _ := url.Parse(streamBase + "/" + s.model + "/get_text")
	q := smallestAISTTQuery(s, language, true)
	u.RawQuery = q.Encode()
	return u.String()
}

func smallestAISTTQuery(s *SmallestAISTT, language string, includeEOU bool) url.Values {
	q := url.Values{}
	q.Set("language", resolveSmallestAISTTLanguage(s, language))
	q.Set("encoding", s.encoding)
	q.Set("sample_rate", strconv.Itoa(s.sampleRate))
	q.Set("word_timestamps", strconv.FormatBool(s.wordTimestamps))
	q.Set("diarize", strconv.FormatBool(s.diarize))
	if includeEOU && s.eouTimeoutMS > 0 {
		q.Set("eou_timeout_ms", strconv.Itoa(s.eouTimeoutMS))
	}
	return q
}

func buildSmallestAISTTHeaders(s *SmallestAISTT) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+s.apiKey)
	headers.Set("X-Source", "livekit")
	return headers
}

func resolveSmallestAISTTLanguage(s *SmallestAISTT, language string) string {
	if language != "" {
		return language
	}
	return s.language
}

type smallestAISTTStream struct {
	conn   *websocket.Conn
	events chan *stt.SpeechEvent
	errCh  chan error
	mu     sync.Mutex
	closed bool

	ctx    context.Context
	cancel context.CancelFunc
	state  *smallestAISTTStreamState
}

func (s *smallestAISTTStream) readLoop() {
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
		var resp smallestAIStreamResponse
		if err := json.Unmarshal(message, &resp); err != nil {
			continue
		}
		for _, event := range processSmallestAISTTStreamEvent(s.state, resp, 0) {
			s.events <- event
		}
		if resp.IsLast {
			return
		}
	}
}

func (s *smallestAISTTStream) PushFrame(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, frame.Data)
}

func (s *smallestAISTTStream) Flush() error {
	return nil
}

func (s *smallestAISTTStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	_ = s.conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"close_stream"}`))
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *smallestAISTTStream) Next() (*stt.SpeechEvent, error) {
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

type smallestAISTTStreamState struct {
	language  string
	diarize   bool
	speaking  bool
	sessionID string
}

type smallestAIStreamResponse struct {
	SessionID  string           `json:"session_id"`
	Transcript string           `json:"transcript"`
	IsFinal    bool             `json:"is_final"`
	IsLast     bool             `json:"is_last"`
	Language   string           `json:"language"`
	Words      []smallestAIWord `json:"words"`
}

type smallestAIBatchResponse struct {
	Transcription string           `json:"transcription"`
	Language      string           `json:"language"`
	Words         []smallestAIWord `json:"words"`
}

type smallestAIWord struct {
	Word       string  `json:"word"`
	Start      float64 `json:"start"`
	End        float64 `json:"end"`
	Confidence float64 `json:"confidence"`
	Speaker    *int    `json:"speaker,omitempty"`
}

func processSmallestAISTTStreamEvent(state *smallestAISTTStreamState, resp smallestAIStreamResponse, startTimeOffset float64) []*stt.SpeechEvent {
	if resp.SessionID != "" {
		state.sessionID = resp.SessionID
	}
	if resp.Transcript == "" {
		return nil
	}
	var events []*stt.SpeechEvent
	if !state.speaking {
		state.speaking = true
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech})
	}
	eventType := stt.SpeechEventInterimTranscript
	if resp.IsFinal {
		eventType = stt.SpeechEventFinalTranscript
	}
	events = append(events, &stt.SpeechEvent{
		Type:      eventType,
		RequestID: state.sessionID,
		Alternatives: []stt.SpeechData{
			smallestAISpeechData(state.language, resp.Language, resp.Transcript, resp.Words, startTimeOffset, state.diarize),
		},
	})
	if resp.IsFinal {
		state.speaking = false
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech})
	}
	return events
}

func smallestAIBatchSpeechEvent(language string, resp smallestAIBatchResponse) *stt.SpeechEvent {
	return &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			smallestAISpeechData(language, resp.Language, resp.Transcription, resp.Words, 0, false),
		},
	}
}

func smallestAISpeechData(defaultLanguage, detectedLanguage, transcript string, words []smallestAIWord, startTimeOffset float64, diarize bool) stt.SpeechData {
	language := detectedLanguage
	if language == "" {
		language = defaultLanguage
	}
	data := stt.SpeechData{
		Language: language,
		Text:     transcript,
	}
	if len(words) == 0 {
		return data
	}
	data.StartTime = words[0].Start + startTimeOffset
	data.EndTime = words[len(words)-1].End + startTimeOffset
	data.Confidence = words[0].Confidence
	data.Words = smallestAITimedStrings(words, startTimeOffset)
	if diarize {
		data.SpeakerID = smallestAIMajoritySpeaker(words)
	}
	return data
}

func smallestAITimedStrings(words []smallestAIWord, startTimeOffset float64) []stt.TimedString {
	timed := make([]stt.TimedString, 0, len(words))
	for _, word := range words {
		speakerID := ""
		if word.Speaker != nil {
			speakerID = "S" + strconv.Itoa(*word.Speaker)
		}
		timed = append(timed, stt.TimedString{
			Text:       word.Word,
			StartTime:  word.Start + startTimeOffset,
			EndTime:    word.End + startTimeOffset,
			Confidence: word.Confidence,
			SpeakerID:  speakerID,
		})
	}
	return timed
}

func smallestAIMajoritySpeaker(words []smallestAIWord) string {
	counts := map[int]int{}
	for _, word := range words {
		if word.Speaker != nil {
			counts[*word.Speaker]++
		}
	}
	if len(counts) == 0 {
		return ""
	}
	speakers := make([]int, 0, len(counts))
	for speaker := range counts {
		speakers = append(speakers, speaker)
	}
	sort.Slice(speakers, func(i, j int) bool {
		if counts[speakers[i]] == counts[speakers[j]] {
			return speakers[i] < speakers[j]
		}
		return counts[speakers[i]] > counts[speakers[j]]
	})
	return "S" + strconv.Itoa(speakers[0])
}
