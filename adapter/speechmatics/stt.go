package speechmatics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

type SpeechmaticsSTT struct {
	apiKey               string
	baseURL              string
	language             string
	sampleRate           int
	audioEncoding        string
	domain               string
	outputLocale         string
	includePartials      *bool
	enableDiarization    *bool
	additionalVocab      []SpeechmaticsAdditionalVocabEntry
	focusSpeakers        []string
	ignoreSpeakers       []string
	focusMode            string
	knownSpeakers        []SpeechmaticsSpeakerIdentifier
	operatingPoint       string
	maxDelay             *float64
	eouSilenceTrigger    *float64
	eouMaxDelay          *float64
	punctuation          map[string]interface{}
	speakerSensitivity   *float64
	maxSpeakers          *int
	preferCurrentSpeaker *bool
}

const (
	speechmaticsAPIKeyEnv = "SPEECHMATICS_API_KEY"
	speechmaticsRTURLEnv  = "SPEECHMATICS_RT_URL"
)

type SpeechmaticsSTTOption func(*SpeechmaticsSTT)

type SpeechmaticsAdditionalVocabEntry struct {
	Content    string   `json:"content"`
	SoundsLike []string `json:"sounds_like,omitempty"`
}

type SpeechmaticsSpeakerIdentifier struct {
	Label     string `json:"label"`
	SpeakerID string `json:"speaker_id"`
}

func WithSpeechmaticsSTTLanguage(language string) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		if language != "" {
			s.language = language
		}
	}
}

func WithSpeechmaticsSTTBaseURL(baseURL string) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		if baseURL != "" {
			s.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithSpeechmaticsSTTSampleRate(sampleRate int) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithSpeechmaticsSTTAudioEncoding(encoding string) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		if encoding != "" {
			s.audioEncoding = encoding
		}
	}
}

func WithSpeechmaticsSTTDomain(domain string) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.domain = domain
	}
}

func WithSpeechmaticsSTTOutputLocale(outputLocale string) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.outputLocale = outputLocale
	}
}

func WithSpeechmaticsSTTIncludePartials(enabled bool) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.includePartials = &enabled
	}
}

func WithSpeechmaticsSTTEnableDiarization(enabled bool) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.enableDiarization = &enabled
	}
}

func WithSpeechmaticsSTTAdditionalVocab(vocab []SpeechmaticsAdditionalVocabEntry) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.additionalVocab = vocab
	}
}

func WithSpeechmaticsSTTSpeakerFocus(focusSpeakers []string, ignoreSpeakers []string, focusMode string) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.focusSpeakers = focusSpeakers
		s.ignoreSpeakers = ignoreSpeakers
		if focusMode != "" {
			s.focusMode = focusMode
		}
	}
}

func WithSpeechmaticsSTTKnownSpeakers(speakers []SpeechmaticsSpeakerIdentifier) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.knownSpeakers = speakers
	}
}

func WithSpeechmaticsSTTOperatingPoint(operatingPoint string) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.operatingPoint = operatingPoint
	}
}

func WithSpeechmaticsSTTMaxDelay(maxDelay float64) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.maxDelay = &maxDelay
	}
}

func WithSpeechmaticsSTTEndOfUtteranceSilenceTrigger(trigger float64) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.eouSilenceTrigger = &trigger
	}
}

func WithSpeechmaticsSTTEndOfUtteranceMaxDelay(maxDelay float64) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.eouMaxDelay = &maxDelay
	}
}

func WithSpeechmaticsSTTPunctuationOverrides(overrides map[string]interface{}) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.punctuation = overrides
	}
}

func WithSpeechmaticsSTTSpeakerSensitivity(sensitivity float64) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.speakerSensitivity = &sensitivity
	}
}

func WithSpeechmaticsSTTMaxSpeakers(maxSpeakers int) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.maxSpeakers = &maxSpeakers
	}
}

func WithSpeechmaticsSTTPreferCurrentSpeaker(prefer bool) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.preferCurrentSpeaker = &prefer
	}
}

func NewSpeechmaticsSTT(apiKey string, opts ...SpeechmaticsSTTOption) *SpeechmaticsSTT {
	if apiKey == "" {
		apiKey = os.Getenv(speechmaticsAPIKeyEnv)
	}
	baseURL := os.Getenv(speechmaticsRTURLEnv)
	if baseURL == "" {
		baseURL = "wss://eu2.rt.speechmatics.com/v2"
	}
	provider := &SpeechmaticsSTT{
		apiKey:        apiKey,
		baseURL:       strings.TrimRight(baseURL, "/"),
		language:      "en",
		sampleRate:    16000,
		audioEncoding: "pcm_s16le",
		focusMode:     "retain",
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *SpeechmaticsSTT) Label() string { return "speechmatics.STT" }
func (s *SpeechmaticsSTT) Model() string {
	if s.operatingPoint != "" {
		return s.operatingPoint
	}
	return "enhanced"
}
func (s *SpeechmaticsSTT) Provider() string {
	return "Speechmatics"
}
func (s *SpeechmaticsSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: true, AlignedTranscript: "chunk", OfflineRecognize: false}
}

func (s *SpeechmaticsSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if s.apiKey == "" {
		return nil, fmt.Errorf("speechmatics API key is required. Pass one in via the apiKey parameter, or set SPEECHMATICS_API_KEY")
	}
	header := make(map[string][]string)
	header["Authorization"] = []string{"Bearer " + s.apiKey}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildSpeechmaticsSTTStreamURL(s), header)
	if err != nil {
		return nil, err
	}

	stream := &speechmaticsSTTStream{
		conn:   conn,
		events: make(chan *stt.SpeechEvent, 10),
		errCh:  make(chan error, 1),
		state:  &speechmaticsStreamState{},
	}
	stream.writeBinary = stream.writeBinaryMessage

	initMsg := buildSpeechmaticsSTTStartMessage(s, language)

	if err := conn.WriteJSON(initMsg); err != nil {
		conn.Close()
		return nil, err
	}

	go stream.readLoop()

	return stream, nil
}

func (s *SpeechmaticsSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	return nil, fmt.Errorf("speechmatics offline recognize is not implemented")
}

func buildSpeechmaticsSTTStreamURL(s *SpeechmaticsSTT) string {
	return strings.TrimRight(s.baseURL, "/")
}

func buildSpeechmaticsSTTStartMessage(s *SpeechmaticsSTT, language string) map[string]interface{} {
	if language == "" {
		language = s.language
	}
	config := map[string]interface{}{
		"language": language,
	}
	if s.includePartials != nil {
		config["enable_partials"] = *s.includePartials
	} else {
		config["enable_partials"] = true
	}
	if s.domain != "" {
		config["domain"] = s.domain
	}
	if s.outputLocale != "" {
		config["output_locale"] = s.outputLocale
	}
	if s.enableDiarization != nil {
		if *s.enableDiarization {
			config["diarization"] = "speaker"
		} else {
			config["diarization"] = "none"
		}
	}
	if len(s.additionalVocab) > 0 {
		config["additional_vocab"] = s.additionalVocab
	}
	if len(s.focusSpeakers) > 0 || len(s.ignoreSpeakers) > 0 || s.focusMode != "" {
		config["speaker_config"] = map[string]interface{}{
			"focus_speakers":  s.focusSpeakers,
			"ignore_speakers": s.ignoreSpeakers,
			"focus_mode":      s.focusMode,
		}
	}
	if len(s.knownSpeakers) > 0 {
		config["known_speakers"] = s.knownSpeakers
	}
	if s.operatingPoint != "" {
		config["operating_point"] = s.operatingPoint
	}
	if s.maxDelay != nil {
		config["max_delay"] = *s.maxDelay
	}
	if s.eouSilenceTrigger != nil {
		config["end_of_utterance_silence_trigger"] = *s.eouSilenceTrigger
	}
	if s.eouMaxDelay != nil {
		config["end_of_utterance_max_delay"] = *s.eouMaxDelay
	}
	if s.punctuation != nil {
		config["punctuation_overrides"] = s.punctuation
	}
	if s.speakerSensitivity != nil {
		config["speaker_sensitivity"] = *s.speakerSensitivity
	}
	if s.maxSpeakers != nil {
		config["max_speakers"] = *s.maxSpeakers
	}
	if s.preferCurrentSpeaker != nil {
		config["prefer_current_speaker"] = *s.preferCurrentSpeaker
	}
	return map[string]interface{}{
		"message": "StartRecognition",
		"audio_format": map[string]interface{}{
			"type":        "raw",
			"encoding":    s.audioEncoding,
			"sample_rate": s.sampleRate,
		},
		"transcription_config": config,
	}
}

type speechmaticsSTTStream struct {
	conn   *websocket.Conn
	events chan *stt.SpeechEvent
	errCh  chan error
	mu     sync.Mutex
	closed bool

	writeBinary func([]byte) error
	state       *speechmaticsStreamState
	audioBuf    *audio.AudioByteStream
}

type speechmaticsStreamState struct {
	speechDuration float64
}

type smResponse struct {
	Message  string `json:"message"`
	Metadata struct {
		Transcript string  `json:"transcript"`
		StartTime  float64 `json:"start_time"`
		EndTime    float64 `json:"end_time"`
	} `json:"metadata"`
	Results []struct {
		Alternatives []struct {
			Content    string  `json:"content"`
			Confidence float64 `json:"confidence"`
		} `json:"alternatives"`
		Type      string  `json:"type"`
		StartTime float64 `json:"start_time"`
		EndTime   float64 `json:"end_time"`
	} `json:"results"`
}

func (s *speechmaticsSTTStream) readLoop() {
	defer close(s.events)
	for {
		_, message, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				s.errCh <- err
			}
			return
		}

		var resp smResponse
		if err := json.Unmarshal(message, &resp); err != nil {
			continue
		}

		if resp.Message == "EndOfTranscript" {
			return
		}
		if resp.Message == "Error" {
			s.errCh <- fmt.Errorf("speechmatics error: %s", string(message))
			return
		}
		for _, event := range speechmaticsEvents(resp, s.state) {
			s.events <- event
		}
	}
}

func speechmaticsEvents(resp smResponse, state *speechmaticsStreamState) []*stt.SpeechEvent {
	switch resp.Message {
	case "AddPartialTranscript", "AddTranscript":
		if event := speechmaticsTranscriptEvent(resp); event != nil {
			return []*stt.SpeechEvent{event}
		}
	case "StartOfTurn":
		return []*stt.SpeechEvent{{Type: stt.SpeechEventStartOfSpeech}}
	case "EndOfTurn":
		events := []*stt.SpeechEvent{{Type: stt.SpeechEventEndOfSpeech}}
		if usage := speechmaticsRecognitionUsageEvent(state); usage != nil {
			events = append(events, usage)
		}
		return events
	}
	return nil
}

func speechmaticsRecognitionUsageEvent(state *speechmaticsStreamState) *stt.SpeechEvent {
	if state == nil || state.speechDuration <= 0 {
		return nil
	}
	duration := state.speechDuration
	state.speechDuration = 0
	return &stt.SpeechEvent{
		Type:             stt.SpeechEventRecognitionUsage,
		RecognitionUsage: &stt.RecognitionUsage{AudioDuration: duration},
	}
}

func speechmaticsTranscriptEvent(resp smResponse) *stt.SpeechEvent {
	eventType := stt.SpeechEventInterimTranscript
	if resp.Message == "AddTranscript" {
		eventType = stt.SpeechEventFinalTranscript
	}

	transcript := ""
	var totalConfidence float64
	var minStart, maxEnd float64
	hasTiming := false
	var words []stt.TimedString

	for _, result := range resp.Results {
		if len(result.Alternatives) == 0 {
			continue
		}
		alt := result.Alternatives[0]
		switch result.Type {
		case "word":
			transcript += alt.Content + " "
			words = append(words, stt.TimedString{
				Text:       alt.Content,
				StartTime:  result.StartTime,
				EndTime:    result.EndTime,
				Confidence: alt.Confidence,
			})
		case "punctuation":
			if transcript != "" {
				transcript = transcript[:len(transcript)-1] + alt.Content + " "
			} else {
				transcript = alt.Content + " "
			}
		}

		totalConfidence += alt.Confidence
		if !hasTiming {
			minStart = result.StartTime
			hasTiming = true
		}
		maxEnd = result.EndTime
	}

	if hasTiming {
		if transcript != "" {
			transcript = transcript[:len(transcript)-1]
		}
		return &stt.SpeechEvent{
			Type: eventType,
			Alternatives: []stt.SpeechData{
				{
					Text:       transcript,
					Confidence: totalConfidence / float64(len(resp.Results)),
					StartTime:  minStart,
					EndTime:    maxEnd,
					Words:      words,
				},
			},
		}
	}

	if resp.Metadata.Transcript == "" {
		return nil
	}
	return &stt.SpeechEvent{
		Type: eventType,
		Alternatives: []stt.SpeechData{
			{
				Text:       resp.Metadata.Transcript,
				Confidence: 1.0,
				StartTime:  resp.Metadata.StartTime,
				EndTime:    resp.Metadata.EndTime,
			},
		},
	}
}

func (s *speechmaticsSTTStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	if s.state == nil {
		s.state = &speechmaticsStreamState{}
	}
	if s.audioBuf == nil {
		s.audioBuf = newSpeechmaticsAudioByteStream(frame)
	}
	for _, chunk := range s.audioBuf.Push(frame.Data) {
		if err := s.writeBinaryData(chunk.Data); err != nil {
			return err
		}
		s.state.speechDuration += audio.CalculateFrameDuration(chunk)
	}
	return nil
}

func (s *speechmaticsSTTStream) writeBinaryData(data []byte) error {
	if s.writeBinary != nil {
		return s.writeBinary(data)
	}
	return s.writeBinaryMessage(data)
}

func (s *speechmaticsSTTStream) writeBinaryMessage(data []byte) error {
	if s.conn == nil {
		return io.ErrClosedPipe
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, data)
}

func (s *speechmaticsSTTStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.audioBuf == nil {
		return nil
	}
	if s.state == nil {
		s.state = &speechmaticsStreamState{}
	}
	for _, chunk := range s.audioBuf.Flush() {
		if err := s.writeBinaryData(chunk.Data); err != nil {
			return err
		}
		s.state.speechDuration += audio.CalculateFrameDuration(chunk)
	}
	return nil
}

func newSpeechmaticsAudioByteStream(frame *model.AudioFrame) *audio.AudioByteStream {
	sampleRate := frame.SampleRate
	if sampleRate == 0 {
		sampleRate = 16000
	}
	numChannels := frame.NumChannels
	if numChannels == 0 {
		numChannels = 1
	}
	return audio.NewAudioByteStream(sampleRate, numChannels, 0)
}

func (s *speechmaticsSTTStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.conn.WriteJSON(map[string]interface{}{"message": "EndOfStream"})
	return s.conn.Close()
}

func (s *speechmaticsSTTStream) Next() (*stt.SpeechEvent, error) {
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
