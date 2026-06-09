package soniox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

const (
	defaultBaseURL            = "wss://stt-rt.soniox.com/transcribe-websocket"
	defaultModel              = "stt-rt-v4"
	defaultSampleRate         = 16000
	defaultNumChannels        = 1
	defaultMaxEndpointDelayMS = 500
	sonioxAPIKeyEnv           = "SONIOX_API_KEY"

	sonioxKeepaliveMessage = `{"type": "keepalive"}`
	sonioxEndToken         = "<end>"
	sonioxFinalizedToken   = "<fin>"
)

type SonioxSTT struct {
	apiKey                       string
	baseURL                      string
	model                        string
	languageHints                []string
	languageHintsStrict          bool
	context                      any
	numChannels                  int
	sampleRate                   int
	enableSpeakerDiarization     bool
	enableLanguageIdentification bool
	maxEndpointDelayMS           int
	clientReferenceID            string
	translation                  map[string]string
}

type SonioxContextGeneralItem struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type SonioxContextTranslationTerm struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type SonioxContextObject struct {
	General          []SonioxContextGeneralItem     `json:"general,omitempty"`
	Text             string                         `json:"text,omitempty"`
	Terms            []string                       `json:"terms,omitempty"`
	TranslationTerms []SonioxContextTranslationTerm `json:"translation_terms,omitempty"`
}

type SonioxSTTOption func(*SonioxSTT)

func WithSonioxBaseURL(baseURL string) SonioxSTTOption {
	return func(s *SonioxSTT) {
		if baseURL != "" {
			s.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithSonioxModel(model string) SonioxSTTOption {
	return func(s *SonioxSTT) {
		if model != "" {
			s.model = model
		}
	}
}

func WithSonioxLanguageHints(languageHints []string) SonioxSTTOption {
	return func(s *SonioxSTT) {
		s.languageHints = languageHints
	}
}

func WithSonioxLanguageHintsStrict(strict bool) SonioxSTTOption {
	return func(s *SonioxSTT) {
		s.languageHintsStrict = strict
	}
}

func WithSonioxContextText(context string) SonioxSTTOption {
	return func(s *SonioxSTT) {
		if context != "" {
			s.context = context
		}
	}
}

func WithSonioxContextObject(context SonioxContextObject) SonioxSTTOption {
	return func(s *SonioxSTT) {
		s.context = context
	}
}

func WithSonioxNumChannels(numChannels int) SonioxSTTOption {
	return func(s *SonioxSTT) {
		if numChannels > 0 {
			s.numChannels = numChannels
		}
	}
}

func WithSonioxSampleRate(sampleRate int) SonioxSTTOption {
	return func(s *SonioxSTT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithSonioxSpeakerDiarization(enabled bool) SonioxSTTOption {
	return func(s *SonioxSTT) {
		s.enableSpeakerDiarization = enabled
	}
}

func WithSonioxLanguageIdentification(enabled bool) SonioxSTTOption {
	return func(s *SonioxSTT) {
		s.enableLanguageIdentification = enabled
	}
}

func WithSonioxMaxEndpointDelayMS(ms int) SonioxSTTOption {
	return func(s *SonioxSTT) {
		if ms > 0 {
			s.maxEndpointDelayMS = ms
		}
	}
}

func WithSonioxClientReferenceID(clientReferenceID string) SonioxSTTOption {
	return func(s *SonioxSTT) {
		s.clientReferenceID = clientReferenceID
	}
}

func WithSonioxOneWayTranslation(targetLanguage string) SonioxSTTOption {
	return func(s *SonioxSTT) {
		if targetLanguage != "" {
			s.translation = map[string]string{"type": "one_way", "target_language": targetLanguage}
		}
	}
}

func WithSonioxTwoWayTranslation(languageA string, languageB string) SonioxSTTOption {
	return func(s *SonioxSTT) {
		if languageA != "" && languageB != "" {
			s.translation = map[string]string{"type": "two_way", "language_a": languageA, "language_b": languageB}
		}
	}
}

func NewSonioxSTT(apiKey string, opts ...SonioxSTTOption) *SonioxSTT {
	if apiKey == "" {
		apiKey = os.Getenv(sonioxAPIKeyEnv)
	}
	provider := &SonioxSTT{
		apiKey:                       apiKey,
		baseURL:                      defaultBaseURL,
		model:                        defaultModel,
		numChannels:                  defaultNumChannels,
		sampleRate:                   defaultSampleRate,
		enableLanguageIdentification: true,
		maxEndpointDelayMS:           defaultMaxEndpointDelayMS,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *SonioxSTT) Label() string { return "soniox.STT" }
func (s *SonioxSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{
		Streaming:         true,
		InterimResults:    true,
		Diarization:       s.enableSpeakerDiarization,
		AlignedTranscript: "chunk",
		OfflineRecognize:  false,
	}
}

func (s *SonioxSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	payload, err := buildSonioxConfigJSON(s)
	if err != nil {
		return nil, err
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, s.baseURL, http.Header{})
	if err != nil {
		return nil, fmt.Errorf("failed to dial soniox websocket: %w", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		conn.Close()
		return nil, err
	}

	streamCtx, cancel := context.WithCancel(ctx)
	stream := &sonioxStream{
		conn:   conn,
		events: make(chan *stt.SpeechEvent, 100),
		errCh:  make(chan error, 1),
		ctx:    streamCtx,
		cancel: cancel,
		state:  &sonioxMessageState{},
	}
	go stream.readLoop()
	go stream.keepAliveLoop()
	return stream, nil
}

func (s *SonioxSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	return nil, fmt.Errorf("soniox speech-to-text api does not support single frame recognition")
}

func buildSonioxConfig(s *SonioxSTT) map[string]any {
	config := map[string]any{
		"api_key":                        s.apiKey,
		"model":                          s.model,
		"audio_format":                   "pcm_s16le",
		"num_channels":                   s.numChannels,
		"enable_endpoint_detection":      true,
		"sample_rate":                    s.sampleRate,
		"language_hints":                 s.languageHints,
		"language_hints_strict":          s.languageHintsStrict,
		"context":                        s.context,
		"enable_speaker_diarization":     s.enableSpeakerDiarization,
		"enable_language_identification": s.enableLanguageIdentification,
		"client_reference_id":            s.clientReferenceID,
		"max_endpoint_delay_ms":          s.maxEndpointDelayMS,
	}
	if len(s.translation) > 0 {
		config["translation"] = s.translation
	}
	return config
}

func buildSonioxConfigJSON(s *SonioxSTT) ([]byte, error) {
	config := buildSonioxConfig(s)
	if s.languageHints == nil {
		delete(config, "language_hints")
	}
	if s.context == nil {
		delete(config, "context")
	}
	if s.clientReferenceID == "" {
		delete(config, "client_reference_id")
	}
	return json.Marshal(config)
}

type sonioxStream struct {
	conn   *websocket.Conn
	events chan *stt.SpeechEvent
	errCh  chan error
	mu     sync.Mutex
	closed bool

	ctx    context.Context
	cancel context.CancelFunc
	state  *sonioxMessageState
}

func (s *sonioxStream) readLoop() {
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
		events, err := processSonioxMessage(s.state, message)
		if err != nil {
			s.errCh <- err
			return
		}
		for _, event := range events {
			s.events <- event
		}
	}
}

func (s *sonioxStream) keepAliveLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			_ = s.conn.WriteMessage(websocket.TextMessage, []byte(sonioxKeepaliveMessage))
		}
	}
}

func (s *sonioxStream) PushFrame(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, frame.Data)
}

func (s *sonioxStream) Flush() error {
	return nil
}

func (s *sonioxStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *sonioxStream) Next() (*stt.SpeechEvent, error) {
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

type sonioxMessageState struct {
	final              sonioxTokenAccumulator
	speaking           bool
	reportedDurationMS int
}

type sonioxMessage struct {
	Tokens           []sonioxToken `json:"tokens"`
	TotalAudioProcMS float64       `json:"total_audio_proc_ms"`
	Finished         bool          `json:"finished"`
	ErrorCode        any           `json:"error_code"`
	ErrorMessage     string        `json:"error_message"`
}

type sonioxToken struct {
	Text              string   `json:"text"`
	Language          string   `json:"language"`
	IsFinal           bool     `json:"is_final"`
	TranslationStatus string   `json:"translation_status"`
	Speaker           *float64 `json:"speaker"`
	StartMS           *float64 `json:"start_ms"`
	EndMS             *float64 `json:"end_ms"`
	Confidence        *float64 `json:"confidence"`
}

func processSonioxMessage(state *sonioxMessageState, payload []byte) ([]*stt.SpeechEvent, error) {
	var message sonioxMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, err
	}
	if message.ErrorCode != nil || message.ErrorMessage != "" {
		return nil, fmt.Errorf("soniox stt error: %v - %s", message.ErrorCode, message.ErrorMessage)
	}

	var events []*stt.SpeechEvent
	nonFinal := &sonioxTokenAccumulator{}

	flushFinal := func() {
		if state.final.text == "" {
			state.final.reset()
			return
		}
		events = append(events,
			&stt.SpeechEvent{
				Type: stt.SpeechEventFinalTranscript,
				Alternatives: []stt.SpeechData{
					state.final.toSpeechData(0, nil, nil, nil, nil),
				},
			},
			&stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech},
		)
		state.final.reset()
		state.speaking = false
	}

	for _, token := range message.Tokens {
		if isSonioxEndToken(token) {
			flushFinal()
			events = append(events, sonioxUsageEvents(state, message.TotalAudioProcMS)...)
			continue
		}
		if token.IsFinal {
			state.final.update(token)
		} else {
			nonFinal.update(token)
		}
	}

	if state.final.text != "" || nonFinal.text != "" {
		if !state.speaking {
			state.speaking = true
			events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech})
		}
		eventType := stt.SpeechEventInterimTranscript
		if state.final.text != "" && nonFinal.text == "" {
			eventType = stt.SpeechEventPreflightTranscript
		}
		events = append(events, &stt.SpeechEvent{
			Type: eventType,
			Alternatives: []stt.SpeechData{
				state.final.mergedSpeechData(nonFinal, 0, nil, nil, nil, nil),
			},
		})
	}

	if message.Finished {
		flushFinal()
		events = append(events, sonioxUsageEvents(state, message.TotalAudioProcMS)...)
	}

	return events, nil
}

func sonioxUsageEvents(state *sonioxMessageState, totalAudioProcMS float64) []*stt.SpeechEvent {
	toReport := totalAudioProcMS - float64(state.reportedDurationMS)
	if toReport <= 0 {
		return nil
	}
	state.reportedDurationMS = int(totalAudioProcMS)
	return []*stt.SpeechEvent{
		{
			Type: stt.SpeechEventRecognitionUsage,
			RecognitionUsage: &stt.RecognitionUsage{
				AudioDuration: toReport / 1000,
			},
		},
	}
}

func isSonioxEndToken(token sonioxToken) bool {
	return token.Text == sonioxEndToken || token.Text == sonioxFinalizedToken
}

type sonioxTokenAccumulator struct {
	text            string
	language        string
	speakerID       string
	startMS         float64
	endMS           float64
	confidenceSum   float64
	confidenceCount int
	hasStartMS      bool
}

func (a *sonioxTokenAccumulator) update(token sonioxToken) {
	a.text += token.Text
	if token.Language != "" {
		a.language = token.Language
	}
	if token.Speaker != nil && a.speakerID == "" {
		a.speakerID = fmt.Sprintf("%.0f", *token.Speaker)
	}
	if token.StartMS != nil && !a.hasStartMS {
		a.startMS = *token.StartMS
		a.hasStartMS = true
	}
	if token.EndMS != nil {
		a.endMS = *token.EndMS
	}
	if token.Confidence != nil {
		a.confidenceSum += *token.Confidence
		a.confidenceCount++
	}
}

func (a *sonioxTokenAccumulator) reset() {
	*a = sonioxTokenAccumulator{}
}

func (a *sonioxTokenAccumulator) confidence() float64 {
	if a.confidenceCount == 0 {
		return 0
	}
	return a.confidenceSum / float64(a.confidenceCount)
}

func (a *sonioxTokenAccumulator) toSpeechData(startTimeOffset float64, sourceLanguages []string, sourceTexts []string, targetLanguages []string, targetTexts []string) stt.SpeechData {
	return stt.SpeechData{
		Text:            a.text,
		Language:        a.language,
		SpeakerID:       a.speakerID,
		StartTime:       a.startMS/1000 + startTimeOffset,
		EndTime:         a.endMS/1000 + startTimeOffset,
		Confidence:      a.confidence(),
		SourceLanguages: sourceLanguages,
		SourceTexts:     sourceTexts,
		TargetLanguages: targetLanguages,
		TargetTexts:     targetTexts,
	}
}

func (a *sonioxTokenAccumulator) mergedSpeechData(other *sonioxTokenAccumulator, startTimeOffset float64, sourceLanguages []string, sourceTexts []string, targetLanguages []string, targetTexts []string) stt.SpeechData {
	startMS := a.startMS
	if !a.hasStartMS || (other.hasStartMS && other.startMS < startMS) {
		startMS = other.startMS
	}
	endMS := a.endMS
	if other.endMS > endMS {
		endMS = other.endMS
	}
	confidenceSum := a.confidenceSum + other.confidenceSum
	confidenceCount := a.confidenceCount + other.confidenceCount
	confidence := 0.0
	if confidenceCount > 0 {
		confidence = confidenceSum / float64(confidenceCount)
	}
	language := a.language
	if language == "" {
		language = other.language
	}
	speakerID := a.speakerID
	if speakerID == "" {
		speakerID = other.speakerID
	}
	return stt.SpeechData{
		Text:            a.text + other.text,
		Language:        language,
		SpeakerID:       speakerID,
		StartTime:       startMS/1000 + startTimeOffset,
		EndTime:         endMS/1000 + startTimeOffset,
		Confidence:      confidence,
		SourceLanguages: sourceLanguages,
		SourceTexts:     sourceTexts,
		TargetLanguages: targetLanguages,
		TargetTexts:     targetTexts,
	}
}
