package xai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

const (
	defaultXaiSTTRestURL      = "https://api.x.ai/v1/stt"
	defaultXaiSTTWebsocketURL = "wss://api.x.ai/v1/stt"
	defaultXaiSTTSampleRate   = 16000
	defaultXaiSTTLanguage     = "en"
	defaultXaiSTTEndpointing  = 100
	xaiAPIKeyEnv              = "XAI_API_KEY"
)

type XaiSTT struct {
	mu                   sync.Mutex
	apiKey               string
	restURL              string
	websocketURL         string
	sampleRate           int
	language             string
	enableInterimResults bool
	enableDiarization    bool
	endpointing          int
	streams              map[*xaiSTTStream]struct{}
}

type XaiSTTOption func(*XaiSTT)

func WithXaiSTTRestURL(restURL string) XaiSTTOption {
	return func(s *XaiSTT) {
		if restURL != "" {
			s.restURL = restURL
		}
	}
}

func WithXaiSTTWebsocketURL(websocketURL string) XaiSTTOption {
	return func(s *XaiSTT) {
		if websocketURL != "" {
			s.websocketURL = websocketURL
		}
	}
}

func WithXaiSTTSampleRate(sampleRate int) XaiSTTOption {
	return func(s *XaiSTT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithXaiSTTLanguage(language string) XaiSTTOption {
	return func(s *XaiSTT) {
		if language != "" {
			s.language = language
		}
	}
}

func WithXaiSTTInterimResults(enabled bool) XaiSTTOption {
	return func(s *XaiSTT) {
		s.enableInterimResults = enabled
	}
}

func WithXaiSTTDiarization(enabled bool) XaiSTTOption {
	return func(s *XaiSTT) {
		s.enableDiarization = enabled
	}
}

func WithXaiSTTEndpointing(endpointing int) XaiSTTOption {
	return func(s *XaiSTT) {
		if endpointing >= 0 {
			s.endpointing = endpointing
		}
	}
}

func NewXaiSTT(apiKey string, opts ...XaiSTTOption) *XaiSTT {
	if apiKey == "" {
		apiKey = os.Getenv(xaiAPIKeyEnv)
	}
	provider := &XaiSTT{
		apiKey:               apiKey,
		restURL:              defaultXaiSTTRestURL,
		websocketURL:         defaultXaiSTTWebsocketURL,
		sampleRate:           defaultXaiSTTSampleRate,
		language:             defaultXaiSTTLanguage,
		enableInterimResults: true,
		enableDiarization:    false,
		endpointing:          defaultXaiSTTEndpointing,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *XaiSTT) Label() string { return "xai.STT" }
func (s *XaiSTT) InputSampleRate() uint32 {
	return uint32(s.sampleRate)
}
func (s *XaiSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{
		Streaming:         true,
		InterimResults:    s.enableInterimResults,
		Diarization:       s.enableDiarization,
		AlignedTranscript: "word",
		OfflineRecognize:  true,
	}
}

func (s *XaiSTT) UpdateOptions(opts ...XaiSTTOption) {
	s.mu.Lock()
	for _, opt := range opts {
		opt(s)
	}
	streams := make([]*xaiSTTStream, 0, len(s.streams))
	for stream := range s.streams {
		streams = append(streams, stream)
	}
	interimResults := s.enableInterimResults
	diarization := s.enableDiarization
	s.mu.Unlock()

	for _, stream := range streams {
		stream.updateOptions(interimResults, diarization)
	}
}

func (s *XaiSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if err := validateXaiAPIKey(s.apiKey); err != nil {
		return nil, err
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildXaiSTTStreamURL(s, language), buildXaiSTTHeaders(s))
	if err != nil {
		return nil, llm.NewAPIConnectionError("failed to connect to xAI")
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &xaiSTTStream{
		owner:  s,
		conn:   conn,
		events: make(chan *stt.SpeechEvent, 100),
		errCh:  make(chan error, 1),
		ctx:    streamCtx,
		cancel: cancel,
		state: &xaiSTTStreamState{
			interimResults: s.enableInterimResults,
			diarization:    s.enableDiarization,
		},
	}
	s.registerStream(stream)
	go stream.readLoop()
	return stream, nil
}

func (s *XaiSTT) registerStream(stream *xaiSTTStream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streams == nil {
		s.streams = make(map[*xaiSTTStream]struct{})
	}
	s.streams[stream] = struct{}{}
}

func (s *XaiSTT) unregisterStream(stream *xaiSTTStream) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.streams, stream)
}

func (s *XaiSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	if err := validateXaiAPIKey(s.apiKey); err != nil {
		return nil, err
	}

	var audio bytes.Buffer
	for _, frame := range frames {
		audio.Write(frame.Data)
	}
	req, err := buildXaiSTTRecognizeRequest(ctx, s, audio.Bytes(), language)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, llm.NewAPITimeoutError(err.Error())
		}
		return nil, llm.NewAPIConnectionError(err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, llm.NewAPIStatusError("xAI STT request failed", resp.StatusCode, "", string(respBody))
	}
	var result xaiSTTResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, llm.NewAPIConnectionError(err.Error())
	}
	return xaiSTTBatchSpeechEvent(s.enableDiarization, result), nil
}

func buildXaiSTTRecognizeRequest(ctx context.Context, s *XaiSTT, audio []byte, language string) (*http.Request, error) {
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
	if err := writer.WriteField("language", resolveXaiSTTLanguage(s, language)); err != nil {
		return nil, err
	}
	if err := writer.WriteField("format", "true"); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.restURL, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req, nil
}

func buildXaiSTTStreamURL(s *XaiSTT, language string) string {
	u, _ := url.Parse(s.websocketURL)
	q := u.Query()
	q.Set("encoding", "pcm")
	q.Set("sample_rate", strconv.Itoa(s.sampleRate))
	q.Set("interim_results", strconv.FormatBool(s.enableInterimResults))
	q.Set("diarize", strconv.FormatBool(s.enableDiarization))
	q.Set("language", resolveXaiSTTLanguage(s, language))
	q.Set("endpointing", strconv.Itoa(s.endpointing))
	u.RawQuery = q.Encode()
	return u.String()
}

func buildXaiSTTHeaders(s *XaiSTT) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+s.apiKey)
	return headers
}

func resolveXaiSTTLanguage(s *XaiSTT, language string) string {
	if language != "" {
		return language
	}
	return s.language
}

type xaiSTTStream struct {
	owner  *XaiSTT
	conn   *websocket.Conn
	events chan *stt.SpeechEvent
	errCh  chan error
	mu     sync.Mutex
	closed bool

	ctx    context.Context
	cancel context.CancelFunc
	state  *xaiSTTStreamState
}

func (s *xaiSTTStream) readLoop() {
	defer close(s.events)
	defer s.owner.unregisterStream(s)
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
		var payload map[string]any
		if err := json.Unmarshal(message, &payload); err != nil {
			continue
		}
		s.mu.Lock()
		events := processXaiSTTStreamEvent(s.state, payload)
		s.mu.Unlock()
		for _, event := range events {
			s.events <- event
		}
	}
}

func (s *xaiSTTStream) updateOptions(interimResults bool, diarization bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.interimResults = interimResults
	s.state.diarization = diarization
}

func (s *xaiSTTStream) PushFrame(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, frame.Data)
}

func (s *xaiSTTStream) Flush() error { return nil }

func (s *xaiSTTStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	s.owner.unregisterStream(s)
	_ = s.conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"audio.done"}`))
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *xaiSTTStream) Next() (*stt.SpeechEvent, error) {
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

type xaiSTTStreamState struct {
	interimResults    bool
	diarization       bool
	speaking          bool
	emittedChunkFinal bool
}

type xaiSTTResponse struct {
	Text     string       `json:"text"`
	Language string       `json:"language"`
	Words    []xaiSTTWord `json:"words"`
}

type xaiSTTWord struct {
	Text    string  `json:"text"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Speaker *int    `json:"speaker,omitempty"`
}

func processXaiSTTStreamEvent(state *xaiSTTStreamState, payload map[string]any) []*stt.SpeechEvent {
	switch payloadString(payload, "type") {
	case "transcript.created":
		return nil
	case "transcript.partial":
		return processXaiSTTPartial(state, payload)
	case "transcript.done":
		return processXaiSTTDone(state, payload)
	default:
		return nil
	}
}

func processXaiSTTPartial(state *xaiSTTStreamState, payload map[string]any) []*stt.SpeechEvent {
	text := payloadString(payload, "text")
	if text == "" {
		return nil
	}
	language := payloadString(payload, "language")
	words := xaiSTTWords(payload["words"])
	isFinal := payloadBool(payload, "is_final")
	speechFinal := payloadBool(payload, "speech_final")

	var events []*stt.SpeechEvent
	if !state.speaking {
		state.speaking = true
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech})
	}

	if !isFinal {
		if state.interimResults {
			events = append(events, &stt.SpeechEvent{
				Type: stt.SpeechEventInterimTranscript,
				Alternatives: []stt.SpeechData{{
					Language: language,
					Text:     text,
				}},
			})
		}
		return events
	}

	if !speechFinal {
		state.emittedChunkFinal = true
		events = append(events, &stt.SpeechEvent{
			Type:         stt.SpeechEventFinalTranscript,
			Alternatives: []stt.SpeechData{xaiSTTSpeechData(words, text, language, state.diarization)},
		})
		return events
	}

	if !state.emittedChunkFinal {
		events = append(events, &stt.SpeechEvent{
			Type:         stt.SpeechEventFinalTranscript,
			Alternatives: []stt.SpeechData{xaiSTTSpeechData(words, text, language, state.diarization)},
		})
	}
	state.emittedChunkFinal = false
	state.speaking = false
	events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech})
	return events
}

func processXaiSTTDone(state *xaiSTTStreamState, payload map[string]any) []*stt.SpeechEvent {
	text := payloadString(payload, "text")
	language := payloadString(payload, "language")
	words := xaiSTTWords(payload["words"])
	var events []*stt.SpeechEvent
	if text != "" {
		events = append(events, &stt.SpeechEvent{
			Type:         stt.SpeechEventFinalTranscript,
			Alternatives: []stt.SpeechData{xaiSTTSpeechData(words, text, language, state.diarization)},
		})
	}
	if state.speaking {
		state.speaking = false
		state.emittedChunkFinal = false
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech})
	}
	return events
}

func xaiSTTBatchSpeechEvent(enableDiarization bool, resp xaiSTTResponse) *stt.SpeechEvent {
	return &stt.SpeechEvent{
		Type:         stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{xaiSTTSpeechData(resp.Words, resp.Text, resp.Language, enableDiarization)},
	}
}

func xaiSTTSpeechData(words []xaiSTTWord, text, language string, enableDiarization bool) stt.SpeechData {
	data := stt.SpeechData{
		Language:   language,
		Text:       text,
		Confidence: stt.DefaultTranscriptConfidence(text),
	}
	if len(words) == 0 {
		return data
	}
	data.StartTime = words[0].Start
	data.EndTime = words[len(words)-1].End
	if enableDiarization && words[0].Speaker != nil {
		data.SpeakerID = "S" + strconv.Itoa(*words[0].Speaker)
	}
	data.Words = make([]stt.TimedString, 0, len(words))
	for _, word := range words {
		timed := stt.TimedString{
			Text:      word.Text,
			StartTime: word.Start,
			EndTime:   word.End,
		}
		if word.Speaker != nil {
			timed.SpeakerID = "S" + strconv.Itoa(*word.Speaker)
		}
		data.Words = append(data.Words, timed)
	}
	return data
}

func xaiSTTWords(raw any) []xaiSTTWord {
	rawWords, ok := raw.([]any)
	if !ok {
		return nil
	}
	words := make([]xaiSTTWord, 0, len(rawWords))
	for _, rawWord := range rawWords {
		wordMap, ok := rawWord.(map[string]any)
		if !ok {
			continue
		}
		word := xaiSTTWord{
			Text:  payloadString(wordMap, "text"),
			Start: payloadFloat(wordMap, "start"),
			End:   payloadFloat(wordMap, "end"),
		}
		if speaker, ok := payloadInt(wordMap, "speaker"); ok {
			word.Speaker = &speaker
		}
		words = append(words, word)
	}
	return words
}

func payloadString(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return value
}

func payloadBool(payload map[string]any, key string) bool {
	value, _ := payload[key].(bool)
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

func payloadInt(payload map[string]any, key string) (int, bool) {
	switch value := payload[key].(type) {
	case float64:
		return int(value), true
	case int:
		return value, true
	default:
		return 0, false
	}
}
