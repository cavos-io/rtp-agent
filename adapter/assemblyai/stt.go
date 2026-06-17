package assemblyai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

const (
	defaultAssemblyAIBaseURL        = "wss://streaming.assemblyai.com"
	defaultAssemblyAIEncoding       = "pcm_s16le"
	defaultAssemblyAISpeechModel    = "universal-streaming-english"
	defaultAssemblyAISampleRate     = 16000
	defaultAssemblyAIMinTurnSilence = 100
)

type AssemblyAISTT struct {
	apiKey             string
	baseURL            string
	sampleRate         int
	encoding           string
	speechModel        string
	languageDetection  *bool
	endTurnConfidence  *float64
	minTurnSilence     *int
	maxTurnSilence     *int
	formatTurns        *bool
	continuousPartials *bool
	interruptionDelay  *int
	keytermsPrompt     []string
	prompt             string
	vadThreshold       *float64
	speakerLabels      *bool
	maxSpeakers        *int
	domain             string
}

type AssemblyAISTTOption func(*AssemblyAISTT)

func WithAssemblyAISTTBaseURL(baseURL string) AssemblyAISTTOption {
	return func(s *AssemblyAISTT) {
		if baseURL != "" {
			s.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithAssemblyAISTTSampleRate(sampleRate int) AssemblyAISTTOption {
	return func(s *AssemblyAISTT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithAssemblyAISTTModel(model string) AssemblyAISTTOption {
	return func(s *AssemblyAISTT) {
		if model == "u3-pro" {
			model = "u3-rt-pro"
		}
		if model != "" {
			s.speechModel = model
		}
	}
}

func WithAssemblyAISTTMinTurnSilence(ms int) AssemblyAISTTOption {
	return func(s *AssemblyAISTT) {
		if ms > 0 {
			s.minTurnSilence = intPtr(ms)
		}
	}
}

func WithAssemblyAISTTMaxTurnSilence(ms int) AssemblyAISTTOption {
	return func(s *AssemblyAISTT) {
		if ms > 0 {
			s.maxTurnSilence = intPtr(ms)
		}
	}
}

func WithAssemblyAISTTEndOfTurnConfidenceThreshold(threshold float64) AssemblyAISTTOption {
	return func(s *AssemblyAISTT) {
		s.endTurnConfidence = &threshold
	}
}

func WithAssemblyAISTTFormatTurns(enabled bool) AssemblyAISTTOption {
	return func(s *AssemblyAISTT) {
		s.formatTurns = boolPtr(enabled)
	}
}

func WithAssemblyAISTTLanguageDetection(enabled bool) AssemblyAISTTOption {
	return func(s *AssemblyAISTT) {
		s.languageDetection = boolPtr(enabled)
	}
}

func WithAssemblyAISTTContinuousPartials(enabled bool) AssemblyAISTTOption {
	return func(s *AssemblyAISTT) {
		s.continuousPartials = boolPtr(enabled)
	}
}

func WithAssemblyAISTTInterruptionDelay(ms int) AssemblyAISTTOption {
	return func(s *AssemblyAISTT) {
		if ms >= 0 {
			s.interruptionDelay = intPtr(ms)
		}
	}
}

func WithAssemblyAISTTKeytermsPrompt(keyterms []string) AssemblyAISTTOption {
	return func(s *AssemblyAISTT) {
		s.keytermsPrompt = append([]string(nil), keyterms...)
	}
}

func WithAssemblyAISTTPrompt(prompt string) AssemblyAISTTOption {
	return func(s *AssemblyAISTT) {
		s.prompt = prompt
	}
}

func WithAssemblyAISTTVADThreshold(threshold float64) AssemblyAISTTOption {
	return func(s *AssemblyAISTT) {
		s.vadThreshold = &threshold
	}
}

func WithAssemblyAISTTSpeakerLabels(enabled bool) AssemblyAISTTOption {
	return func(s *AssemblyAISTT) {
		s.speakerLabels = boolPtr(enabled)
	}
}

func WithAssemblyAISTTMaxSpeakers(maxSpeakers int) AssemblyAISTTOption {
	return func(s *AssemblyAISTT) {
		if maxSpeakers > 0 {
			s.maxSpeakers = intPtr(maxSpeakers)
		}
	}
}

func WithAssemblyAISTTDomain(domain string) AssemblyAISTTOption {
	return func(s *AssemblyAISTT) {
		s.domain = domain
	}
}

func NewAssemblyAISTT(apiKey string, opts ...AssemblyAISTTOption) *AssemblyAISTT {
	if apiKey == "" {
		apiKey = os.Getenv("ASSEMBLYAI_API_KEY")
	}
	provider := &AssemblyAISTT{
		apiKey:         apiKey,
		baseURL:        defaultAssemblyAIBaseURL,
		sampleRate:     defaultAssemblyAISampleRate,
		encoding:       defaultAssemblyAIEncoding,
		speechModel:    defaultAssemblyAISpeechModel,
		minTurnSilence: intPtr(defaultAssemblyAIMinTurnSilence),
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.speechModel == "u3-rt-pro" && provider.continuousPartials == nil {
		provider.continuousPartials = boolPtr(true)
	}
	return provider
}

func (s *AssemblyAISTT) Label() string { return "assemblyai.STT" }
func (s *AssemblyAISTT) Model() string { return s.speechModel }
func (s *AssemblyAISTT) Provider() string {
	return "AssemblyAI"
}
func (s *AssemblyAISTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: s.speakerLabels != nil && *s.speakerLabels, AlignedTranscript: "word", OfflineRecognize: false}
}

func (s *AssemblyAISTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if err := s.validateStreamConfig(); err != nil {
		return nil, err
	}

	header := make(http.Header)
	header.Set("Authorization", s.apiKey)
	header.Set("Content-Type", "application/json")
	header.Set("User-Agent", "AssemblyAI/1.0 (integration=Livekit)")

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildAssemblyAIStreamURL(s), header)
	if err != nil {
		return nil, err
	}

	stream := &assemblyAISTTStream{
		conn:       conn,
		events:     make(chan *stt.SpeechEvent, 10),
		errCh:      make(chan error, 1),
		state:      &assemblyAIStreamState{},
		sampleRate: s.sampleRate,
	}
	stream.writeBinary = stream.writeBinaryMessage
	stream.writeJSON = stream.writeJSONMessage
	stream.closeConn = stream.closeWebsocketConn

	go stream.readLoop()

	return stream, nil
}

func (s *AssemblyAISTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	return nil, fmt.Errorf("assemblyai offline recognize is not implemented")
}

func (s *AssemblyAISTT) validateStreamConfig() error {
	if s.apiKey == "" {
		return fmt.Errorf("AssemblyAI API key is required. Pass one in via the apiKey parameter, or set it as the ASSEMBLYAI_API_KEY environment variable")
	}
	if s.speechModel != "u3-rt-pro" {
		if s.prompt != "" {
			return fmt.Errorf("the prompt parameter is only supported with the u3-rt-pro model")
		}
		if s.continuousPartials != nil {
			return fmt.Errorf("the continuous_partials parameter is only supported with the u3-rt-pro model")
		}
		if s.interruptionDelay != nil {
			return fmt.Errorf("the interruption_delay parameter is only supported with the u3-rt-pro model")
		}
	}
	return nil
}

func buildAssemblyAIStreamURL(s *AssemblyAISTT) string {
	u, err := url.Parse(strings.TrimRight(s.baseURL, "/") + "/v3/ws")
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set("sample_rate", strconv.Itoa(s.sampleRate))
	q.Set("encoding", s.encoding)
	q.Set("speech_model", s.speechModel)
	if s.formatTurns != nil {
		q.Set("format_turns", strconv.FormatBool(*s.formatTurns))
	}
	if s.continuousPartials != nil {
		q.Set("continuous_partials", strconv.FormatBool(*s.continuousPartials))
	}
	if s.interruptionDelay != nil {
		q.Set("interruption_delay", strconv.Itoa(*s.interruptionDelay))
	}
	if s.endTurnConfidence != nil {
		q.Set("end_of_turn_confidence_threshold", strconv.FormatFloat(*s.endTurnConfidence, 'f', -1, 64))
	}
	if s.minTurnSilence != nil {
		q.Set("min_turn_silence", strconv.Itoa(*s.minTurnSilence))
	}
	if s.maxTurnSilence != nil {
		q.Set("max_turn_silence", strconv.Itoa(*s.maxTurnSilence))
	} else if s.speechModel == "u3-rt-pro" && s.minTurnSilence != nil {
		q.Set("max_turn_silence", strconv.Itoa(*s.minTurnSilence))
	}
	if len(s.keytermsPrompt) > 0 {
		if encoded, err := json.Marshal(s.keytermsPrompt); err == nil {
			q.Set("keyterms_prompt", string(encoded))
		}
	}
	if s.languageDetection != nil {
		q.Set("language_detection", strconv.FormatBool(*s.languageDetection))
	} else {
		q.Set("language_detection", strconv.FormatBool(strings.Contains(s.speechModel, "multilingual") || s.speechModel == "u3-rt-pro"))
	}
	if s.prompt != "" {
		q.Set("prompt", s.prompt)
	}
	if s.vadThreshold != nil {
		q.Set("vad_threshold", strconv.FormatFloat(*s.vadThreshold, 'f', -1, 64))
	}
	if s.speakerLabels != nil {
		q.Set("speaker_labels", strconv.FormatBool(*s.speakerLabels))
	}
	if s.maxSpeakers != nil {
		q.Set("max_speakers", strconv.Itoa(*s.maxSpeakers))
	}
	if s.domain != "" {
		q.Set("domain", s.domain)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

type assemblyAIWord struct {
	Text       string  `json:"text"`
	Start      int     `json:"start"`
	End        int     `json:"end"`
	Confidence float64 `json:"confidence"`
}

func assemblyAITimedStrings(words []assemblyAIWord) []stt.TimedString {
	if len(words) == 0 {
		return nil
	}
	timed := make([]stt.TimedString, 0, len(words))
	for _, word := range words {
		timed = append(timed, stt.TimedString{
			Text:       word.Text,
			StartTime:  float64(word.Start) / 1000,
			EndTime:    float64(word.End) / 1000,
			Confidence: word.Confidence,
		})
	}
	return timed
}

type assemblyAISTTStream struct {
	conn   *websocket.Conn
	events chan *stt.SpeechEvent
	errCh  chan error
	mu     sync.Mutex
	closed bool

	writeBinary func([]byte) error
	writeJSON   func(any) error
	closeConn   func() error
	state       *assemblyAIStreamState
	sampleRate  int
	audioBuf    *audio.AudioByteStream
}

type assemblyAIStreamState struct {
	lastPreflightStartTime float64
}

type aaiResponse struct {
	Type            string           `json:"type"`
	MessageType     string           `json:"message_type"`
	Text            string           `json:"text"`
	Transcript      string           `json:"transcript"`
	Utterance       string           `json:"utterance"`
	Confidence      float64          `json:"confidence"`
	EndOfTurn       bool             `json:"end_of_turn"`
	TurnIsFormatted bool             `json:"turn_is_formatted"`
	Language        string           `json:"language_code"`
	SpeakerID       string           `json:"speaker_label"`
	Words           []assemblyAIWord `json:"words"`
	Error           string           `json:"error"`
}

func (s *assemblyAISTTStream) readLoop() {
	defer close(s.events)
	for {
		_, message, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				s.errCh <- err
			}
			return
		}

		var resp aaiResponse
		if err := json.Unmarshal(message, &resp); err != nil {
			continue
		}

		if resp.Type == "Begin" || resp.MessageType == "SessionBegins" {
			continue
		}

		if resp.Type == "Termination" || resp.MessageType == "SessionTerminated" {
			return
		}

		if resp.Error != "" {
			s.errCh <- fmt.Errorf("assemblyai error: %s", resp.Error)
			return
		}

		for _, event := range assemblyAIRealtimeEvents(resp, s.state) {
			s.events <- event
		}
	}
}

func assemblyAIRealtimeEvents(resp aaiResponse, state *assemblyAIStreamState) []*stt.SpeechEvent {
	if resp.Type == "SpeechStarted" {
		return []*stt.SpeechEvent{{Type: stt.SpeechEventStartOfSpeech}}
	}
	if resp.Type == "Turn" || resp.MessageType == "PartialTranscript" || resp.MessageType == "FinalTranscript" {
		return assemblyAIRealtimeTranscriptEvents(resp, state)
	}
	return nil
}

func assemblyAIRealtimeTranscriptEvent(resp aaiResponse) *stt.SpeechEvent {
	events := assemblyAIRealtimeTranscriptEvents(resp, &assemblyAIStreamState{})
	for _, event := range events {
		if event.Type == stt.SpeechEventFinalTranscript {
			return event
		}
	}
	if len(events) > 0 {
		return events[0]
	}
	return nil
}

func assemblyAIRealtimeTranscriptEvents(resp aaiResponse, state *assemblyAIStreamState) []*stt.SpeechEvent {
	if state == nil {
		state = &assemblyAIStreamState{}
	}
	if resp.MessageType == "PartialTranscript" {
		if text := assemblyAIResponseText(resp); text != "" {
			return []*stt.SpeechEvent{assemblyAITranscriptEvent(stt.SpeechEventInterimTranscript, resp, text, assemblyAITimedStrings(resp.Words), 0, 0)}
		}
		return nil
	}
	if resp.MessageType == "FinalTranscript" {
		if text := assemblyAIResponseText(resp); text != "" {
			return []*stt.SpeechEvent{assemblyAITranscriptEvent(stt.SpeechEventFinalTranscript, resp, text, assemblyAITimedStrings(resp.Words), 0, 0)}
		}
		return nil
	}

	words := assemblyAITimedStrings(resp.Words)
	startTime, endTime := assemblyAIWordTimeRange(words)
	events := make([]*stt.SpeechEvent, 0, 4)
	if len(words) > 0 {
		events = append(events, assemblyAITranscriptEvent(stt.SpeechEventInterimTranscript, resp, assemblyAITextFromTimedWords(words), words, startTime, endTime))
	}
	if resp.Utterance != "" {
		if state.lastPreflightStartTime == 0 {
			state.lastPreflightStartTime = startTime
		}
		utteranceWords := assemblyAIWordsFromStart(words, state.lastPreflightStartTime)
		events = append(events, assemblyAITranscriptEvent(stt.SpeechEventPreflightTranscript, resp, resp.Utterance, utteranceWords, state.lastPreflightStartTime, endTime))
		state.lastPreflightStartTime = endTime
	}
	if resp.EndOfTurn {
		text := resp.Transcript
		if text == "" {
			text = resp.Text
		}
		if text != "" {
			events = append(events, assemblyAITranscriptEvent(stt.SpeechEventFinalTranscript, resp, text, words, startTime, endTime))
		}
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech})
		state.lastPreflightStartTime = 0
	}
	return events
}

func assemblyAIResponseText(resp aaiResponse) string {
	text := resp.Text
	if text == "" {
		text = resp.Transcript
	}
	return text
}

func assemblyAITranscriptEvent(eventType stt.SpeechEventType, resp aaiResponse, text string, words []stt.TimedString, startTime float64, endTime float64) *stt.SpeechEvent {
	confidence := assemblyAIConfidence(resp.Confidence, words)

	return &stt.SpeechEvent{
		Type: eventType,
		Alternatives: []stt.SpeechData{
			{
				Language:   assemblyAILanguage(resp.Language),
				Text:       text,
				StartTime:  startTime,
				EndTime:    endTime,
				Confidence: confidence,
				Words:      words,
				SpeakerID:  assemblyAISpeakerID(resp.SpeakerID),
			},
		},
	}
}

func assemblyAIConfidence(respConfidence float64, words []stt.TimedString) float64 {
	if respConfidence != 0 || len(words) == 0 {
		return respConfidence
	}
	var confidence float64
	for _, word := range words {
		confidence += word.Confidence
	}
	return confidence / float64(len(words))
}

func assemblyAIWordTimeRange(words []stt.TimedString) (float64, float64) {
	if len(words) == 0 {
		return 0, 0
	}
	return words[0].StartTime, words[len(words)-1].EndTime
}

func assemblyAITextFromTimedWords(words []stt.TimedString) string {
	parts := make([]string, 0, len(words))
	for _, word := range words {
		parts = append(parts, word.Text)
	}
	return strings.Join(parts, " ")
}

func assemblyAIWordsFromStart(words []stt.TimedString, startTime float64) []stt.TimedString {
	if len(words) == 0 {
		return nil
	}
	filtered := make([]stt.TimedString, 0, len(words))
	for _, word := range words {
		if word.StartTime >= startTime {
			filtered = append(filtered, word)
		}
	}
	return filtered
}

func assemblyAILanguage(language string) string {
	if language == "" {
		return "en"
	}
	return language
}

func assemblyAISpeakerID(speakerID string) string {
	if speakerID == "" || speakerID == "UNKNOWN" {
		return ""
	}
	return speakerID
}

func intPtr(value int) *int {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}

func (s *assemblyAISTTStream) PushFrame(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}

	if s.audioBuf == nil {
		s.audioBuf = newAssemblyAISTTAudioByteStream(s, frame)
	}
	for _, chunk := range s.audioBuf.Push(frame.Data) {
		if err := s.writeBinaryData(chunk.Data); err != nil {
			s.closed = true
			_ = s.closeConnection()
			return err
		}
	}
	return nil
}

func (s *assemblyAISTTStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.audioBuf == nil {
		return nil
	}
	for _, chunk := range s.audioBuf.Flush() {
		if err := s.writeBinaryData(chunk.Data); err != nil {
			s.closed = true
			_ = s.closeConnection()
			return err
		}
	}
	return nil
}

func newAssemblyAISTTAudioByteStream(s *assemblyAISTTStream, frame *model.AudioFrame) *audio.AudioByteStream {
	sampleRate := uint32(s.sampleRate)
	if frame.SampleRate > 0 {
		sampleRate = frame.SampleRate
	}
	if sampleRate == 0 {
		sampleRate = defaultAssemblyAISampleRate
	}
	numChannels := frame.NumChannels
	if numChannels == 0 {
		numChannels = 1
	}
	return audio.NewAudioByteStream(sampleRate, numChannels, sampleRate/20)
}

func (s *assemblyAISTTStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	_ = s.writeJSONData(map[string]string{"type": "Terminate"})
	return s.closeConnection()
}

func (s *assemblyAISTTStream) writeBinaryData(data []byte) error {
	if s.writeBinary != nil {
		return s.writeBinary(data)
	}
	return s.writeBinaryMessage(data)
}

func (s *assemblyAISTTStream) writeBinaryMessage(data []byte) error {
	if s.conn == nil {
		return io.ErrClosedPipe
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, data)
}

func (s *assemblyAISTTStream) writeJSONData(message any) error {
	if s.writeJSON != nil {
		return s.writeJSON(message)
	}
	return s.writeJSONMessage(message)
}

func (s *assemblyAISTTStream) writeJSONMessage(message any) error {
	if s.conn == nil {
		return io.ErrClosedPipe
	}
	return s.conn.WriteJSON(message)
}

func (s *assemblyAISTTStream) closeConnection() error {
	if s.closeConn != nil {
		return s.closeConn()
	}
	return s.closeWebsocketConn()
}

func (s *assemblyAISTTStream) closeWebsocketConn() error {
	if s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

func (s *assemblyAISTTStream) Next() (*stt.SpeechEvent, error) {
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
	}
}
