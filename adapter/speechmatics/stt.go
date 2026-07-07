package speechmatics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

type SpeechmaticsSTT struct {
	mu                   sync.Mutex
	streams              map[*speechmaticsSTTStream]struct{}
	apiKey               string
	baseURL              string
	language             string
	turnDetectionMode    string
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
	speakerActiveFormat  string
	speakerPassiveFormat string
	knownSpeakers        []SpeechmaticsSpeakerIdentifier
	operatingPoint       string
	maxDelay             *float64
	eouSilenceTrigger    *float64
	eouMaxDelay          *float64
	punctuation          map[string]interface{}
	speakerSensitivity   *float64
	maxSpeakers          *int
	preferCurrentSpeaker *bool
	closed               bool
}

const (
	speechmaticsAPIKeyEnv       = "SPEECHMATICS_API_KEY"
	speechmaticsRTURLEnv        = "SPEECHMATICS_RT_URL"
	speechmaticsSTTAppParam     = "livekit/1.5.19.rc1"
	speechmaticsVoiceSDKVersion = "0.2.8"
)

var speechmaticsSpeakerResultTimeout = 5 * time.Second

type SpeechmaticsSTTOption func(*SpeechmaticsSTT)

type SpeechmaticsAdditionalVocabEntry struct {
	Content    string   `json:"content"`
	SoundsLike []string `json:"sounds_like,omitempty"`
}

type SpeechmaticsSpeakerIdentifier struct {
	Label              string   `json:"label"`
	SpeakerID          string   `json:"speaker_id,omitempty"`
	SpeakerIdentifiers []string `json:"speaker_identifiers,omitempty"`
}

func WithSpeechmaticsSTTLanguage(language string) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.language = language
	}
}

func WithSpeechmaticsSTTBaseURL(baseURL string) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.baseURL = baseURL
	}
}

func WithSpeechmaticsSTTSampleRate(sampleRate int) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		if sampleRate >= 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithSpeechmaticsSTTAudioEncoding(encoding string) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.audioEncoding = encoding
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

func WithSpeechmaticsSTTFixedTurnDetection() SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.turnDetectionMode = "fixed"
	}
}

func WithSpeechmaticsSTTAdaptiveTurnDetection() SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.turnDetectionMode = "adaptive"
	}
}

func WithSpeechmaticsSTTSmartTurnDetection() SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.turnDetectionMode = "smart_turn"
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

func WithSpeechmaticsSTTSpeakerFormats(activeFormat string, passiveFormat string) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.speakerActiveFormat = activeFormat
		s.speakerPassiveFormat = passiveFormat
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
	maxDelay := 2.0
	provider := &SpeechmaticsSTT{
		apiKey:            apiKey,
		baseURL:           baseURL,
		language:          "en",
		turnDetectionMode: "external",
		sampleRate:        16000,
		audioEncoding:     "pcm_s16le",
		focusMode:         "retain",
		operatingPoint:    "enhanced",
		maxDelay:          &maxDelay,
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
func (s *SpeechmaticsSTT) InputSampleRate() uint32 {
	if s == nil || s.sampleRate < 0 {
		return 16000
	}
	return uint32(s.sampleRate)
}
func (s *SpeechmaticsSTT) Capabilities() stt.STTCapabilities {
	diarization := true
	if s != nil && s.enableDiarization != nil {
		diarization = *s.enableDiarization
	}
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: diarization, AlignedTranscript: "chunk", OfflineRecognize: false}
}

func (s *SpeechmaticsSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if s.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if s.apiKey == "" {
		return nil, fmt.Errorf("speechmatics API key is required. Pass one in via the apiKey parameter, or set SPEECHMATICS_API_KEY")
	}
	if err := validateSpeechmaticsSTTOptions(s); err != nil {
		return nil, err
	}
	header := make(map[string][]string)
	header["Authorization"] = []string{"Bearer " + s.apiKey}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildSpeechmaticsSTTStreamURL(s), header)
	if err != nil {
		return nil, err
	}
	if s.isClosed() {
		conn.Close()
		return nil, io.ErrClosedPipe
	}
	streamLanguage := speechmaticsSTTStreamLanguage(s, language)

	stream := &speechmaticsSTTStream{
		conn:   conn,
		events: make(chan *stt.SpeechEvent, 10),
		errCh:  make(chan error, 1),
		done:   make(chan struct{}),
		state: &speechmaticsStreamState{
			language:             streamLanguage,
			speakerActiveFormat:  s.speakerActiveFormat,
			speakerPassiveFormat: s.speakerPassiveFormat,
			focusSpeakers:        cloneSpeechmaticsStringSlice(s.focusSpeakers),
			ignoreSpeakers:       cloneSpeechmaticsStringSlice(s.ignoreSpeakers),
			focusMode:            s.focusMode,
			includePartials:      speechmaticsIncludePartials(s),
		},
		owner:                     s,
		waitForRecognitionStarted: true,
	}
	stream.writeBinary = stream.writeBinaryMessage
	stream.writeJSON = stream.writeJSONMessage
	stream.closeConn = stream.closeWebsocketConn

	initMsg := buildSpeechmaticsSTTStartMessage(s, streamLanguage)

	if err := conn.WriteJSON(initMsg); err != nil {
		conn.Close()
		return nil, err
	}

	if !s.registerStream(stream) {
		conn.Close()
		return nil, io.ErrClosedPipe
	}
	go stream.readLoop()

	return stream, nil
}

func (s *SpeechmaticsSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	return nil, fmt.Errorf("speechmatics offline recognize is not implemented")
}

func (s *SpeechmaticsSTT) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	streams := make([]*speechmaticsSTTStream, 0, len(s.streams))
	for stream := range s.streams {
		streams = append(streams, stream)
	}
	s.mu.Unlock()

	var closeErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (s *SpeechmaticsSTT) Finalize() error {
	streams := s.activeStreams()
	var finalizeErr error
	for _, stream := range streams {
		if err := stream.Finalize(); err != nil && finalizeErr == nil && !errors.Is(err, io.ErrClosedPipe) {
			finalizeErr = err
		}
	}
	return finalizeErr
}

func (s *SpeechmaticsSTT) UpdateSpeakers(focusSpeakers []string, ignoreSpeakers []string, focusMode string) error {
	if s == nil {
		return io.ErrClosedPipe
	}
	if s.enableDiarization != nil && !*s.enableDiarization {
		return fmt.Errorf("diarization is not enabled")
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return io.ErrClosedPipe
	}
	streams := make([]*speechmaticsSTTStream, 0, len(s.streams))
	for stream := range s.streams {
		streams = append(streams, stream)
	}
	if len(streams) == 0 {
		s.mu.Unlock()
		return nil
	}
	s.focusSpeakers = cloneSpeechmaticsStringSlice(focusSpeakers)
	s.ignoreSpeakers = cloneSpeechmaticsStringSlice(ignoreSpeakers)
	if focusMode != "" {
		s.focusMode = focusMode
	}
	focusSpeakers = cloneSpeechmaticsStringSlice(s.focusSpeakers)
	ignoreSpeakers = cloneSpeechmaticsStringSlice(s.ignoreSpeakers)
	focusMode = s.focusMode
	s.mu.Unlock()

	var updateErr error
	for _, stream := range streams {
		if err := stream.UpdateSpeakers(focusSpeakers, ignoreSpeakers, focusMode); err != nil && updateErr == nil && !errors.Is(err, io.ErrClosedPipe) {
			updateErr = err
		}
	}
	return updateErr
}

func (s *SpeechmaticsSTT) GetSpeakerIDs(ctx context.Context) ([]SpeechmaticsSpeakerIdentifier, error) {
	if s == nil {
		return nil, io.ErrClosedPipe
	}
	if s.enableDiarization != nil && !*s.enableDiarization {
		return nil, nil
	}
	streams := s.activeStreams()
	speakers := make([]SpeechmaticsSpeakerIdentifier, 0, len(streams))
	var requestErr error
	for _, stream := range streams {
		streamCtx, cancel := speechmaticsSpeakerResultContext(ctx)
		streamSpeakers, err := stream.GetSpeakerIDs(streamCtx)
		cancel()
		if err != nil {
			if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				continue
			}
			if requestErr == nil {
				requestErr = err
			}
			continue
		}
		speakers = append(speakers, streamSpeakers...)
	}
	return speakers, requestErr
}

func speechmaticsSpeakerResultContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := speechmaticsSpeakerResultTimeout
	if timeout <= 0 {
		return ctx, func() {}
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= timeout {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func (s *SpeechmaticsSTT) isClosed() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *SpeechmaticsSTT) registerStream(stream *speechmaticsSTTStream) bool {
	if s == nil || stream == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	if s.streams == nil {
		s.streams = make(map[*speechmaticsSTTStream]struct{})
	}
	s.streams[stream] = struct{}{}
	stream.owner = s
	return true
}

func (s *SpeechmaticsSTT) unregisterStream(stream *speechmaticsSTTStream) {
	if s == nil || stream == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.streams, stream)
}

func (s *SpeechmaticsSTT) activeStreams() []*speechmaticsSTTStream {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	streams := make([]*speechmaticsSTTStream, 0, len(s.streams))
	for stream := range s.streams {
		streams = append(streams, stream)
	}
	return streams
}

func buildSpeechmaticsSTTStreamURL(s *SpeechmaticsSTT) string {
	rawURL := s.baseURL
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	query := parsed.Query()
	query.Set("sm-app", speechmaticsSTTAppParam)
	query.Set("sm-voice-sdk", speechmaticsVoiceSDKVersion)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func speechmaticsSTTStreamLanguage(s *SpeechmaticsSTT, language string) string {
	if language != "" {
		return language
	}
	if s != nil {
		return s.language
	}
	return "en"
}

func validateSpeechmaticsSTTOptions(s *SpeechmaticsSTT) error {
	if s == nil {
		return io.ErrClosedPipe
	}
	var problems []string
	if s.baseURL == "" {
		problems = append(problems, "missing Speechmatics base URL")
	}
	if s.eouSilenceTrigger != nil && (*s.eouSilenceTrigger <= 0 || *s.eouSilenceTrigger >= 2) {
		problems = append(problems, "end_of_utterance_silence_trigger must be between 0 and 2")
	}
	if s.eouMaxDelay != nil && s.eouSilenceTrigger != nil && *s.eouMaxDelay <= *s.eouSilenceTrigger {
		problems = append(problems, "end_of_utterance_max_delay must be greater than end_of_utterance_silence_trigger")
	}
	if s.maxSpeakers != nil && (*s.maxSpeakers <= 1 || *s.maxSpeakers > 100) {
		problems = append(problems, "max_speakers must be between 2 and 100")
	}
	if s.maxDelay != nil && (*s.maxDelay < 0.7 || *s.maxDelay > 4.0) {
		problems = append(problems, "max_delay must be between 0.7 and 4.0")
	}
	if s.speakerSensitivity != nil && (*s.speakerSensitivity <= 0 || *s.speakerSensitivity >= 1.0) {
		problems = append(problems, "speaker_sensitivity must be between 0.0 and 1.0")
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid Speechmatics STT options: %s", strings.Join(problems, ", "))
	}
	return nil
}

func buildSpeechmaticsSTTStartMessage(s *SpeechmaticsSTT, language string) map[string]interface{} {
	language = speechmaticsSTTStreamLanguage(s, language)
	config := map[string]interface{}{
		"language": language,
	}
	config["enable_partials"] = true
	if s.domain != "" {
		config["domain"] = s.domain
	}
	if s.outputLocale != "" {
		config["output_locale"] = s.outputLocale
	}
	if s.enableDiarization != nil {
		if *s.enableDiarization {
			config["diarization"] = "speaker"
		}
	} else {
		config["diarization"] = "speaker"
	}
	if len(s.additionalVocab) > 0 {
		config["additional_vocab"] = s.additionalVocab
	}
	if s.operatingPoint != "" {
		config["operating_point"] = s.operatingPoint
	}
	if s.maxDelay != nil {
		config["max_delay"] = *s.maxDelay
	}
	config["enable_entities"] = false
	config["max_delay_mode"] = "flexible"
	config["audio_filtering_config"] = map[string]interface{}{
		"volume_threshold": 0.0,
	}
	if conversationConfig := speechmaticsConversationConfig(s); len(conversationConfig) > 0 {
		config["conversation_config"] = conversationConfig
	}
	if s.punctuation != nil {
		config["punctuation_overrides"] = s.punctuation
	}
	if speakerConfig := speechmaticsSTTDiarizationConfig(s); len(speakerConfig) > 0 {
		config["speaker_diarization_config"] = speakerConfig
	}
	return map[string]interface{}{
		"message": "StartRecognition",
		"audio_format": map[string]interface{}{
			"type":        "raw",
			"encoding":    s.audioEncoding,
			"sample_rate": s.sampleRate,
			"chunk_size":  160,
		},
		"transcription_config": config,
	}
}

func speechmaticsConversationSilenceTrigger(s *SpeechmaticsSTT) (float64, bool) {
	if s == nil {
		return 0, false
	}
	if s.eouSilenceTrigger != nil {
		return *s.eouSilenceTrigger, true
	}
	if s.turnDetectionMode == "fixed" {
		return 0.5, true
	}
	return 0, false
}

func speechmaticsConversationConfig(s *SpeechmaticsSTT) map[string]interface{} {
	config := make(map[string]interface{})
	if s == nil || s.turnDetectionMode != "fixed" {
		return config
	}
	if trigger, ok := speechmaticsConversationSilenceTrigger(s); ok {
		config["end_of_utterance_silence_trigger"] = trigger
	}
	return config
}

func speechmaticsIncludePartials(s *SpeechmaticsSTT) bool {
	if s == nil || s.includePartials == nil {
		return true
	}
	return *s.includePartials
}

func speechmaticsSTTDiarizationConfig(s *SpeechmaticsSTT) map[string]interface{} {
	config := make(map[string]interface{})
	if s.enableDiarization != nil && !*s.enableDiarization {
		return config
	}
	if len(s.knownSpeakers) > 0 {
		config["speakers"] = speechmaticsKnownSpeakerConfig(s.knownSpeakers)
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
	return config
}

func speechmaticsKnownSpeakerConfig(speakers []SpeechmaticsSpeakerIdentifier) []map[string]interface{} {
	config := make([]map[string]interface{}, 0, len(speakers))
	for _, speaker := range speakers {
		identifiers := cloneSpeechmaticsStringSlice(speaker.SpeakerIdentifiers)
		if len(identifiers) == 0 && speaker.SpeakerID != "" {
			identifiers = []string{speaker.SpeakerID}
		}
		entry := map[string]interface{}{
			"label":               speaker.Label,
			"speaker_identifiers": identifiers,
		}
		config = append(config, entry)
	}
	return config
}

func cloneSpeechmaticsStringSlice(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

func cloneSpeechmaticsSpeakerIDs(values []SpeechmaticsSpeakerIdentifier) []SpeechmaticsSpeakerIdentifier {
	if values == nil {
		return nil
	}
	return append([]SpeechmaticsSpeakerIdentifier(nil), values...)
}

type speechmaticsSTTStream struct {
	conn            *websocket.Conn
	events          chan *stt.SpeechEvent
	errCh           chan error
	done            chan struct{}
	doneOnce        sync.Once
	mu              sync.Mutex
	closed          bool
	inputEnded      bool
	pendingEndInput bool

	writeBinary func([]byte) error
	writeJSON   func(interface{}) error
	closeConn   func() error
	owner       *SpeechmaticsSTT
	state       *speechmaticsStreamState
	audioBuf    *audio.AudioByteStream
	inputAudio  speechmaticsSTTInputAudioNormalizer
	audioReady  bool

	waitForRecognitionStarted bool
	pendingAudioChunks        [][]byte
	speakerResultCh           chan []SpeechmaticsSpeakerIdentifier
	pushedSampleRate          uint32
}

type speechmaticsStreamState struct {
	language             string
	speechDuration       float64
	startTimeOffset      float64
	startTime            float64
	speakerActiveFormat  string
	speakerPassiveFormat string
	focusSpeakers        []string
	ignoreSpeakers       []string
	focusMode            string
	includePartials      bool
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
	Segments []struct {
		Text      string `json:"text"`
		Language  string `json:"language"`
		SpeakerID string `json:"speaker_id"`
		IsActive  *bool  `json:"is_active"`
		Metadata  struct {
			StartTime float64 `json:"start_time"`
			EndTime   float64 `json:"end_time"`
		} `json:"metadata"`
	} `json:"segments"`
	Speakers []SpeechmaticsSpeakerIdentifier `json:"speakers"`
}

func (s *speechmaticsSTTStream) readLoop() {
	defer func() {
		if s.owner != nil {
			s.owner.unregisterStream(s)
		}
		close(s.events)
	}()
	for {
		_, message, err := s.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) || err == io.EOF {
				if !s.isClosed() {
					s.errCh <- llm.NewAPIConnectionError("Speechmatics STT WebSocket closed unexpectedly")
				}
			} else {
				s.errCh <- err
			}
			return
		}

		var resp smResponse
		if err := json.Unmarshal(message, &resp); err != nil {
			continue
		}

		if !s.handleResponse(resp) {
			return
		}
	}
}

func (s *speechmaticsSTTStream) handleResponse(resp smResponse) bool {
	if resp.Message == "EndOfTranscript" {
		s.markClosed()
		_ = s.closeTransport()
		return false
	}
	if resp.Message == "RecognitionStarted" {
		if err := s.markReadyForAudio(); err != nil {
			if s.errCh != nil {
				select {
				case s.errCh <- err:
				default:
				}
			}
			return false
		}
		return true
	}
	if resp.Message == "SpeakersResult" {
		s.recordSpeakerResult(resp.Speakers)
		return true
	}
	for _, event := range speechmaticsEvents(resp, s.state) {
		if !s.enqueueEvent(event) {
			return false
		}
	}
	return true
}

func (s *speechmaticsSTTStream) enqueueEvent(event *stt.SpeechEvent) bool {
	if event == nil {
		return true
	}
	if s.done == nil {
		s.events <- event
		return true
	}
	select {
	case s.events <- event:
		return true
	case <-s.done:
		return false
	}
}

func (s *speechmaticsSTTStream) isClosed() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func speechmaticsEvents(resp smResponse, state *speechmaticsStreamState) []*stt.SpeechEvent {
	switch resp.Message {
	case "AddPartialSegment", "AddSegment":
		return speechmaticsSegmentEvents(resp, state)
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

func speechmaticsSegmentEvents(resp smResponse, state *speechmaticsStreamState) []*stt.SpeechEvent {
	if len(resp.Segments) == 0 {
		return nil
	}
	eventType := stt.SpeechEventInterimTranscript
	if resp.Message == "AddSegment" {
		eventType = stt.SpeechEventFinalTranscript
	}
	if eventType == stt.SpeechEventInterimTranscript && state != nil && !state.includePartials {
		return nil
	}
	startTimeOffset := speechmaticsStartTimeOffset(state)

	events := make([]*stt.SpeechEvent, 0, len(resp.Segments))
	for _, segment := range resp.Segments {
		speakerID := speechmaticsSegmentSpeakerID(segment.SpeakerID)
		if speechmaticsSpeakerFiltered(speakerID, state) {
			continue
		}
		text := speechmaticsFormattedSegmentText(segment.Text, speakerID, segment.IsActive, state)
		events = append(events, &stt.SpeechEvent{
			Type: eventType,
			Alternatives: []stt.SpeechData{
				{
					Text:      text,
					Language:  speechmaticsSegmentLanguage(segment.Language, state),
					SpeakerID: speakerID,
					StartTime: segment.Metadata.StartTime + startTimeOffset,
					EndTime:   segment.Metadata.EndTime + startTimeOffset,
				},
			},
		})
	}
	return events
}

func speechmaticsSpeakerFiltered(speakerID string, state *speechmaticsStreamState) bool {
	if speechmaticsSystemSpeakerID(speakerID) {
		return true
	}
	if state == nil {
		return false
	}
	if speechmaticsStringInSlice(speakerID, state.ignoreSpeakers) {
		return true
	}
	return state.focusMode == "ignore" && len(state.focusSpeakers) > 0 && !speechmaticsStringInSlice(speakerID, state.focusSpeakers)
}

func speechmaticsSystemSpeakerID(speakerID string) bool {
	if len(speakerID) < 6 || !strings.HasPrefix(speakerID, "__") || !strings.HasSuffix(speakerID, "__") {
		return false
	}
	inner := speakerID[2 : len(speakerID)-2]
	if len(inner) < 2 {
		return false
	}
	for _, r := range inner {
		if r == '_' || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func speechmaticsStringInSlice(value string, values []string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func speechmaticsSegmentLanguage(language string, state *speechmaticsStreamState) string {
	if language != "" {
		return language
	}
	if state != nil {
		return state.language
	}
	return "en"
}

func speechmaticsSegmentSpeakerID(speakerID string) string {
	if speakerID != "" {
		return speakerID
	}
	return "UU"
}

func speechmaticsFormattedSegmentText(text, speakerID string, isActive *bool, state *speechmaticsStreamState) string {
	format := ""
	active := true
	if isActive != nil {
		active = *isActive
	}
	if state != nil {
		if active {
			format = state.speakerActiveFormat
		} else {
			format = state.speakerPassiveFormat
		}
	}
	if format == "" {
		format = "{text}"
	}
	format = strings.ReplaceAll(format, "{speaker_id}", speakerID)
	return strings.ReplaceAll(format, "{text}", text)
}

func speechmaticsStartTimeOffset(state *speechmaticsStreamState) float64 {
	if state == nil {
		return 0
	}
	return state.startTimeOffset
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

func (s *speechmaticsSTTStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	if s.pushedSampleRate != 0 && s.pushedSampleRate != frame.SampleRate {
		return fmt.Errorf("the sample rate of the input frames must be consistent")
	}
	s.pushedSampleRate = frame.SampleRate
	normalizedFrame, err := s.inputAudio.normalize(frame, s.targetSampleRate())
	if err != nil {
		return err
	}
	return s.writeAudioFrameLocked(normalizedFrame)
}

func (s *speechmaticsSTTStream) writeAudioFrameLocked(frame *model.AudioFrame) error {
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
		if err := s.writeAudioChunkLocked(chunk.Data); err != nil {
			_ = s.closeLocked()
			return err
		}
		s.state.speechDuration += audio.CalculateFrameDuration(chunk)
	}
	return nil
}

func (s *speechmaticsSTTStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}
	if tail := s.inputAudio.flush(); tail != nil {
		if err := s.writeAudioFrameLocked(tail); err != nil {
			_ = s.closeLocked()
			return err
		}
	}
	if s.audioBuf != nil {
		if s.state == nil {
			s.state = &speechmaticsStreamState{}
		}
		for _, chunk := range s.audioBuf.Flush() {
			if err := s.writeAudioChunkLocked(chunk.Data); err != nil {
				_ = s.closeLocked()
				return err
			}
			s.state.speechDuration += audio.CalculateFrameDuration(chunk)
		}
	}
	s.inputEnded = true
	if s.waitForRecognitionStarted && !s.audioReady {
		s.pendingEndInput = true
		return nil
	}
	if err := s.writeJSONData(map[string]interface{}{"message": "EndOfStream"}); err != nil {
		_ = s.closeLocked()
		return err
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

func (s *speechmaticsSTTStream) writeJSONData(message interface{}) error {
	if s.writeJSON != nil {
		return s.writeJSON(message)
	}
	return s.writeJSONMessage(message)
}

func (s *speechmaticsSTTStream) writeJSONMessage(message interface{}) error {
	if s.conn == nil {
		return io.ErrClosedPipe
	}
	return s.conn.WriteJSON(message)
}

func (s *speechmaticsSTTStream) writeAudioChunkLocked(data []byte) error {
	if s.waitForRecognitionStarted && !s.audioReady {
		s.pendingAudioChunks = append(s.pendingAudioChunks, append([]byte(nil), data...))
		return nil
	}
	return s.writeBinaryData(data)
}

func (s *speechmaticsSTTStream) markReadyForAudio() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	s.audioReady = true
	pending := s.pendingAudioChunks
	s.pendingAudioChunks = nil
	for _, chunk := range pending {
		if err := s.writeBinaryData(chunk); err != nil {
			_ = s.closeLocked()
			return err
		}
	}
	if s.pendingEndInput {
		s.pendingEndInput = false
		if err := s.writeJSONData(map[string]interface{}{"message": "EndOfStream"}); err != nil {
			_ = s.closeLocked()
			return err
		}
	}
	return nil
}

func (s *speechmaticsSTTStream) Finalize() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	return s.writeJSONData(map[string]interface{}{"message": "ForceEndOfUtterance"})
}

func (s *speechmaticsSTTStream) UpdateSpeakers(focusSpeakers []string, ignoreSpeakers []string, focusMode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.state == nil {
		s.state = &speechmaticsStreamState{}
	}
	s.state.focusSpeakers = cloneSpeechmaticsStringSlice(focusSpeakers)
	s.state.ignoreSpeakers = cloneSpeechmaticsStringSlice(ignoreSpeakers)
	s.state.focusMode = focusMode
	return nil
}

func (s *speechmaticsSTTStream) GetSpeakerIDs(ctx context.Context) ([]SpeechmaticsSpeakerIdentifier, error) {
	resultCh, err := s.prepareSpeakerResult()
	if err != nil {
		return nil, err
	}
	if err := s.writeJSONData(map[string]interface{}{"message": "GetSpeakers"}); err != nil {
		return nil, err
	}
	select {
	case speakers := <-resultCh:
		return cloneSpeechmaticsSpeakerIDs(speakers), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *speechmaticsSTTStream) prepareSpeakerResult() (chan []SpeechmaticsSpeakerIdentifier, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, io.ErrClosedPipe
	}
	if s.speakerResultCh == nil {
		s.speakerResultCh = make(chan []SpeechmaticsSpeakerIdentifier, 1)
	}
	for {
		select {
		case <-s.speakerResultCh:
		default:
			return s.speakerResultCh, nil
		}
	}
}

func (s *speechmaticsSTTStream) recordSpeakerResult(speakers []SpeechmaticsSpeakerIdentifier) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.speakerResultCh == nil {
		s.speakerResultCh = make(chan []SpeechmaticsSpeakerIdentifier, 1)
	}
	resultCh := s.speakerResultCh
	result := cloneSpeechmaticsSpeakerIDs(speakers)
	s.mu.Unlock()

	select {
	case resultCh <- result:
	default:
		<-resultCh
		resultCh <- result
	}
}

func (s *speechmaticsSTTStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}
	if tail := s.inputAudio.flush(); tail != nil {
		if err := s.writeAudioFrameLocked(tail); err != nil {
			_ = s.closeLocked()
			return err
		}
	}
	if s.audioBuf == nil {
		return nil
	}
	if s.state == nil {
		s.state = &speechmaticsStreamState{}
	}
	for _, chunk := range s.audioBuf.Flush() {
		if err := s.writeAudioChunkLocked(chunk.Data); err != nil {
			_ = s.closeLocked()
			return err
		}
		s.state.speechDuration += audio.CalculateFrameDuration(chunk)
	}
	return nil
}

func (s *speechmaticsSTTStream) targetSampleRate() uint32 {
	if s == nil || s.owner == nil || s.owner.sampleRate <= 0 {
		return 0
	}
	return uint32(s.owner.sampleRate)
}

func (s *speechmaticsSTTStream) StartTimeOffset() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		return 0
	}
	return s.state.startTimeOffset
}

func (s *speechmaticsSTTStream) SetStartTimeOffset(offset float64) {
	if offset < 0 {
		panic("start_time_offset must be non-negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		s.state = &speechmaticsStreamState{}
	}
	s.state.startTimeOffset = offset
}

func (s *speechmaticsSTTStream) StartTime() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		return 0
	}
	return s.state.startTime
}

func (s *speechmaticsSTTStream) SetStartTime(startTime float64) {
	if startTime < 0 {
		panic("start_time must be non-negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		s.state = &speechmaticsStreamState{}
	}
	s.state.startTime = startTime
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

type speechmaticsSTTInputAudioNormalizer struct {
	sampleRate  uint32
	numChannels uint32
	targetRate  uint32
	remainder   uint64
	lastSample  []byte
}

func (n *speechmaticsSTTInputAudioNormalizer) normalize(frame *model.AudioFrame, targetRate uint32) (*model.AudioFrame, error) {
	if frame == nil || targetRate == 0 || frame.SampleRate == targetRate {
		n.reset()
		return frame, nil
	}
	if frame.SampleRate == 0 {
		return nil, fmt.Errorf("cannot resample audio with zero sample rate")
	}
	if frame.NumChannels == 0 {
		return nil, fmt.Errorf("cannot resample audio with zero channels")
	}
	if len(frame.Data)%2 != 0 {
		return nil, fmt.Errorf("cannot resample non-16-bit PCM audio")
	}
	samplesPerChannel := frame.SamplesPerChannel
	if samplesPerChannel == 0 {
		samplesPerChannel = uint32(len(frame.Data)) / frame.NumChannels / 2
	}
	expectedBytes := int(samplesPerChannel * frame.NumChannels * 2)
	if len(frame.Data) < expectedBytes {
		return nil, fmt.Errorf("audio frame data is shorter than declared sample count")
	}
	if n.sampleRate != frame.SampleRate || n.numChannels != frame.NumChannels || n.targetRate != targetRate {
		n.sampleRate = frame.SampleRate
		n.numChannels = frame.NumChannels
		n.targetRate = targetRate
		n.remainder = 0
		n.lastSample = nil
	}
	if samplesPerChannel == 0 {
		return &model.AudioFrame{
			SampleRate:        targetRate,
			NumChannels:       frame.NumChannels,
			SamplesPerChannel: 0,
			ParticipantID:     frame.ParticipantID,
		}, nil
	}

	scaledSamples := uint64(samplesPerChannel)*uint64(targetRate) + n.remainder
	outSamples := uint32(scaledSamples / uint64(frame.SampleRate))
	n.remainder = scaledSamples % uint64(frame.SampleRate)
	out := make([]byte, int(outSamples*frame.NumChannels*2))
	channelCount := int(frame.NumChannels)
	for outIdx := uint32(0); outIdx < outSamples; outIdx++ {
		srcIdx := uint32(uint64(outIdx) * uint64(frame.SampleRate) / uint64(targetRate))
		if srcIdx >= samplesPerChannel {
			srcIdx = samplesPerChannel - 1
		}
		for ch := 0; ch < channelCount; ch++ {
			inOffset := (int(srcIdx)*channelCount + ch) * 2
			outOffset := (int(outIdx)*channelCount + ch) * 2
			copy(out[outOffset:outOffset+2], frame.Data[inOffset:inOffset+2])
		}
	}
	if n.remainder > 0 {
		offset := int((samplesPerChannel - 1) * frame.NumChannels * 2)
		n.lastSample = append(n.lastSample[:0], frame.Data[offset:offset+int(frame.NumChannels*2)]...)
	} else {
		n.lastSample = nil
	}

	return &model.AudioFrame{
		Data:              out,
		SampleRate:        targetRate,
		NumChannels:       frame.NumChannels,
		SamplesPerChannel: outSamples,
		ParticipantID:     frame.ParticipantID,
	}, nil
}

func (n *speechmaticsSTTInputAudioNormalizer) flush() *model.AudioFrame {
	if n == nil || n.remainder == 0 || len(n.lastSample) == 0 || n.targetRate == 0 || n.numChannels == 0 {
		return nil
	}
	data := append([]byte(nil), n.lastSample...)
	frame := &model.AudioFrame{
		Data:              data,
		SampleRate:        n.targetRate,
		NumChannels:       n.numChannels,
		SamplesPerChannel: 1,
	}
	n.remainder = 0
	n.lastSample = nil
	return frame
}

func (n *speechmaticsSTTInputAudioNormalizer) reset() {
	n.sampleRate = 0
	n.numChannels = 0
	n.targetRate = 0
	n.remainder = 0
	n.lastSample = nil
}

func (s *speechmaticsSTTStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeLocked()
}

func (s *speechmaticsSTTStream) closeLocked() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.inputEnded = true
	s.pendingEndInput = false
	s.pendingAudioChunks = nil
	s.closeDone()
	defer func() {
		if s.owner != nil {
			s.owner.unregisterStream(s)
		}
	}()
	if s.closeConn != nil {
		return s.closeConn()
	}
	return s.closeWebsocketConn()
}

func (s *speechmaticsSTTStream) closeWebsocketConn() error {
	if s.conn == nil {
		return nil
	}
	writeErr := s.writeJSONMessage(map[string]interface{}{"message": "EndOfStream"})
	closeErr := s.conn.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func (s *speechmaticsSTTStream) closeTransport() error {
	if s.conn != nil {
		return s.conn.Close()
	}
	if s.closeConn != nil {
		return s.closeConn()
	}
	return nil
}

func (s *speechmaticsSTTStream) Next() (*stt.SpeechEvent, error) {
	if s.isClosed() {
		return nil, io.EOF
	}
	select {
	case event, ok := <-s.events:
		if !ok {
			select {
			case err := <-s.errCh:
				s.markClosed()
				return nil, err
			default:
				s.markClosed()
				return nil, io.EOF
			}
		}
		return event, nil
	case err := <-s.errCh:
		select {
		case event, ok := <-s.events:
			if ok {
				return event, nil
			}
		default:
		}
		s.markClosed()
		return nil, err
	}
}

func (s *speechmaticsSTTStream) markClosed() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	s.closeDone()
	if s.owner != nil {
		s.owner.unregisterStream(s)
	}
}

func (s *speechmaticsSTTStream) closeDone() {
	if s == nil || s.done == nil {
		return
	}
	s.doneOnce.Do(func() {
		close(s.done)
	})
}
