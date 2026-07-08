package speechmatics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/adapter/silero"
	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	corevad "github.com/cavos-io/rtp-agent/core/vad"
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
	vad                  corevad.VAD
	vadSet               bool
	closed               bool
	closeDone            chan struct{}
}

const (
	speechmaticsAPIKeyEnv       = "SPEECHMATICS_API_KEY"
	speechmaticsRTURLEnv        = "SPEECHMATICS_RT_URL"
	speechmaticsSTTAppParam     = "livekit/1.5.19.rc1"
	speechmaticsVoiceSDKVersion = "0.2.8"
)

var speechmaticsSpeakerResultTimeout = 5 * time.Second
var speechmaticsForcedEOUTimeout = time.Second
var speechmaticsSTTRetryInterval = func(retryAttempt int) time.Duration {
	return llm.DefaultAPIConnectOptions().IntervalForRetry(retryAttempt)
}

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
		s.sampleRate = sampleRate
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

func WithSpeechmaticsSTTVAD(detector corevad.VAD) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.vad = detector
		s.vadSet = true
		if detector != nil {
			s.turnDetectionMode = "external"
		}
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
		closeDone:         make(chan struct{}),
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.turnDetectionMode == "external" && !provider.vadSet {
		provider.vad = silero.NewSileroVAD()
	}
	if provider.vad != nil {
		provider.turnDetectionMode = "external"
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
		return 0
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
	header := make(http.Header)
	header["Authorization"] = []string{"Bearer " + s.apiKey}

	dialCtx := ctx
	var cancelDial context.CancelFunc
	var stopCloseWatch chan struct{}
	if done := s.closeDoneChannel(); done != nil {
		dialCtx, cancelDial = context.WithCancel(ctx)
		stopCloseWatch = make(chan struct{})
		go func() {
			select {
			case <-done:
				cancelDial()
			case <-dialCtx.Done():
			case <-stopCloseWatch:
			}
		}()
	}
	if cancelDial != nil {
		defer cancelDial()
	}
	if stopCloseWatch != nil {
		defer close(stopCloseWatch)
	}

	streamLanguage := speechmaticsSTTStreamLanguage(s, language)
	startupAttempt := 0

	for {
		conn, err := s.dialStream(dialCtx, buildSpeechmaticsSTTStreamURL(s), header, &startupAttempt)
		if err != nil {
			return nil, err
		}
		if s.isClosed() {
			conn.Close()
			return nil, io.ErrClosedPipe
		}

		streamStartTime := time.Now()
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
				bufferRawFinals:      true,
				startTime:            float64(streamStartTime.UnixNano()) / 1e9,
			},
			owner:                     s,
			waitForRecognitionStarted: true,
		}
		stream.writeBinary = stream.writeBinaryMessage
		stream.writeJSON = stream.writeJSONMessage
		if err := stream.startVAD(ctx); err != nil {
			conn.Close()
			return nil, err
		}

		initMsg := buildSpeechmaticsSTTStartMessage(s, streamLanguage)

		if err := conn.WriteJSON(initMsg); err != nil {
			_ = stream.Close()
			if retryErr := s.waitBeforeStartupRetry(dialCtx, err, &startupAttempt); retryErr != nil {
				return nil, retryErr
			}
			continue
		}

		if !s.registerStream(stream) {
			_ = stream.Close()
			return nil, io.ErrClosedPipe
		}
		go stream.readLoop()

		return stream, nil
	}
}

func (s *SpeechmaticsSTT) dialStream(ctx context.Context, streamURL string, header http.Header, startupAttempt *int) (*websocket.Conn, error) {
	for {
		conn, _, err := websocket.DefaultDialer.DialContext(ctx, streamURL, header)
		if err == nil {
			return conn, nil
		}
		if retryErr := s.waitBeforeStartupRetry(ctx, err, startupAttempt); retryErr != nil {
			return nil, retryErr
		}
	}
}

func (s *SpeechmaticsSTT) waitBeforeStartupRetry(ctx context.Context, err error, startupAttempt *int) error {
	if s.isClosed() {
		return io.ErrClosedPipe
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	connectionErr := llm.NewAPIConnectionError(err.Error())
	if startupAttempt == nil {
		return connectionErr
	}
	maxRetry := llm.DefaultAPIConnectOptions().MaxRetry
	if *startupAttempt >= maxRetry {
		return llm.NewAPIConnectionError(fmt.Sprintf("failed to recognize speech after %d attempts", *startupAttempt))
	}
	interval := speechmaticsSTTRetryInterval(*startupAttempt)
	*startupAttempt = *startupAttempt + 1
	if interval <= 0 {
		return nil
	}
	timer := time.NewTimer(interval)
	select {
	case <-timer.C:
	case <-ctx.Done():
		timer.Stop()
		if s.isClosed() {
			return io.ErrClosedPipe
		}
		return ctx.Err()
	}
	return nil
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
	if s.closeDone != nil {
		close(s.closeDone)
		s.closeDone = nil
	}
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
	if s == nil {
		return io.ErrClosedPipe
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil
	}
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
	if s.enableDiarization != nil && !*s.enableDiarization {
		s.mu.Unlock()
		return fmt.Errorf("diarization is not enabled")
	}
	if focusSpeakers != nil {
		s.focusSpeakers = cloneSpeechmaticsStringSlice(focusSpeakers)
	}
	if ignoreSpeakers != nil {
		s.ignoreSpeakers = cloneSpeechmaticsStringSlice(ignoreSpeakers)
	}
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
	groups, err := s.GetSpeakerIDGroups(ctx)
	speakers := make([]SpeechmaticsSpeakerIdentifier, 0)
	for _, group := range groups {
		speakers = append(speakers, group...)
	}
	return speakers, err
}

func (s *SpeechmaticsSTT) GetSpeakerIDGroups(ctx context.Context) ([][]SpeechmaticsSpeakerIdentifier, error) {
	if s == nil {
		return nil, io.ErrClosedPipe
	}
	streams := s.activeStreams()
	groups := make([][]SpeechmaticsSpeakerIdentifier, 0, len(streams))
	if s.enableDiarization != nil && !*s.enableDiarization {
		for range streams {
			groups = append(groups, nil)
		}
		return groups, nil
	}
	var requestErr error
	for _, stream := range streams {
		streamCtx, cancel := speechmaticsSpeakerResultContext(ctx)
		streamSpeakers, err := stream.GetSpeakerIDs(streamCtx)
		cancel()
		if err != nil {
			if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				groups = append(groups, nil)
				continue
			}
			if requestErr == nil {
				requestErr = err
			}
			groups = append(groups, nil)
			continue
		}
		groups = append(groups, streamSpeakers)
	}
	return groups, requestErr
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

func (s *SpeechmaticsSTT) closeDoneChannel() <-chan struct{} {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeDone
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
	stream.providerManagedEndpointing = speechmaticsProviderManagedEndpointing(s)
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
	if s.eouMaxDelay != nil && s.eouSilenceTrigger != nil {
		if *s.eouMaxDelay <= *s.eouSilenceTrigger {
			problems = append(problems, "end_of_utterance_max_delay must be greater than end_of_utterance_silence_trigger")
		}
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
	for key, value := range speechmaticsEndpointingConfig(s) {
		config[key] = value
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
		},
		"transcription_config": config,
	}
}

func speechmaticsEndpointingConfig(s *SpeechmaticsSTT) map[string]interface{} {
	config := make(map[string]interface{})
	if s == nil {
		return config
	}
	mode := s.turnDetectionMode
	trigger := 0.5
	switch mode {
	case "external":
		return config
	case "adaptive":
		return config
	case "smart_turn":
		return config
	case "fixed":
		if s.eouSilenceTrigger != nil {
			trigger = *s.eouSilenceTrigger
		}
		config["conversation_config"] = map[string]interface{}{
			"end_of_utterance_silence_trigger": trigger,
		}
		return config
	default:
		return config
	}
	if s.eouSilenceTrigger != nil {
		trigger = *s.eouSilenceTrigger
	}
	maxDelay := 10.0
	if s.eouMaxDelay != nil {
		maxDelay = *s.eouMaxDelay
	}
	config["end_of_utterance_mode"] = mode
	config["end_of_utterance_silence_trigger"] = trigger
	config["end_of_utterance_max_delay"] = maxDelay
	return config
}

func speechmaticsProviderManagedEndpointing(s *SpeechmaticsSTT) bool {
	return s != nil && (s.turnDetectionMode == "fixed" || s.turnDetectionMode == "adaptive" || s.turnDetectionMode == "smart_turn")
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
	config["speaker_sensitivity"] = 0.5
	config["prefer_current_speaker"] = false
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
	transportOnce   sync.Once
	mu              sync.Mutex
	closed          bool
	inputEnded      bool
	pendingEndInput bool
	pendingFinalize bool
	pendingErr      error

	writeBinary func([]byte) error
	writeJSON   func(interface{}) error
	closeConn   func() error
	owner       *SpeechmaticsSTT
	state       *speechmaticsStreamState
	audioBuf    *audio.AudioByteStream
	inputAudio  speechmaticsSTTInputAudioNormalizer
	vadStream   corevad.VADStream
	audioReady  bool

	waitForRecognitionStarted  bool
	pendingAudioChunks         [][]byte
	pendingVADFrames           []*model.AudioFrame
	pendingVADEndInput         bool
	drainingStartup            bool
	speakerResultCh            chan []SpeechmaticsSpeakerIdentifier
	pushedSampleRate           uint32
	providerManagedEndpointing bool
	drainEventsAfterClose      bool
	forcedEOUPending           bool
	forcedEOUSeq               uint64
	forcedEOUCompleted         bool
	fixedEOUCompleted          bool
}

type speechmaticsStreamState struct {
	language             string
	speechDuration       float64
	audioSecondsSent     float64
	startTimeOffset      float64
	startTime            float64
	speakerActiveFormat  string
	speakerPassiveFormat string
	focusSpeakers        []string
	ignoreSpeakers       []string
	focusMode            string
	includePartials      bool
	wordDelimiter        string
	wordDelimiterSet     bool
	bufferRawFinals      bool
	pendingRawFinals     []*stt.SpeechEvent
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
			Content    string   `json:"content"`
			Confidence *float64 `json:"confidence"`
			SpeakerID  string   `json:"speaker"`
			Language   string   `json:"language"`
		} `json:"alternatives"`
		Type      string  `json:"type"`
		Attaches  string  `json:"attaches_to"`
		IsEOS     bool    `json:"is_eos"`
		StartTime float64 `json:"start_time"`
		EndTime   float64 `json:"end_time"`
	} `json:"results"`
	Segments []struct {
		Text       string   `json:"text"`
		Language   string   `json:"language"`
		SpeakerID  string   `json:"speaker_id"`
		IsActive   *bool    `json:"is_active"`
		Annotation []string `json:"annotation"`
		Metadata   struct {
			StartTime float64 `json:"start_time"`
			EndTime   float64 `json:"end_time"`
		} `json:"metadata"`
	} `json:"segments"`
	LanguagePackInfo struct {
		WordDelimiter *string `json:"word_delimiter"`
	} `json:"language_pack_info"`
	Speakers []SpeechmaticsSpeakerIdentifier `json:"speakers"`
}

func (s *speechmaticsSTTStream) readLoop() {
	defer func() {
		s.closeVADStream()
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
					s.enqueueError(llm.NewAPIConnectionError("Speechmatics STT WebSocket closed unexpectedly"))
				}
			} else {
				s.enqueueError(llm.NewAPIConnectionError(err.Error()))
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
		for _, event := range s.flushPendingRawFinalEvents() {
			if !s.enqueueEvent(event) {
				return false
			}
		}
		s.markClosedDrainingEvents()
		_ = s.closeTransportOnce()
		return false
	}
	if resp.Message == "RecognitionStarted" {
		s.recordRecognitionStarted(resp)
		if err := s.markReadyForAudio(); err != nil {
			s.enqueueError(err)
			return false
		}
		return true
	}
	if resp.Message == "EndOfUtterance" {
		if s.owner != nil && s.owner.turnDetectionMode == "fixed" {
			if s.consumeCompletedFixedEOU() {
				return true
			}
			s.clearForcedEOU()
			for _, event := range s.fixedEOUEndEvents() {
				if !s.enqueueEvent(event) {
					return false
				}
			}
		} else if s.consumeCurrentForcedEOU() {
			for _, event := range s.forcedEOUEndEvents() {
				if !s.enqueueEvent(event) {
					return false
				}
			}
		}
		return true
	}
	if resp.Message == "SpeakersResult" {
		s.recordSpeakerResult(resp.Speakers)
		return true
	}
	if s.forcedEOUActive() && speechmaticsPartialMessage(resp.Message) {
		for _, event := range s.forcedEOUPartialEvents(resp) {
			if !s.enqueueEvent(event) {
				return false
			}
		}
		return true
	}
	if resp.Message == "StartOfTurn" {
		s.resetCompletedEOU()
	}
	if resp.Message == "EndOfTurn" {
		if s.consumeCompletedForcedEOU() || s.consumeCompletedFixedEOU() {
			return true
		}
		s.clearForcedEOU()
	}
	for _, event := range s.responseEvents(resp) {
		if !s.enqueueEvent(event) {
			return false
		}
	}
	return true
}

func speechmaticsPartialMessage(message string) bool {
	return message == "AddPartialSegment" || message == "AddPartialTranscript"
}

func speechmaticsForcedEOUPartialEvents(resp smResponse, state *speechmaticsStreamState) []*stt.SpeechEvent {
	if resp.Message == "AddPartialTranscript" {
		return speechmaticsFlushPendingRawFinals(state)
	}
	return nil
}

func (s *speechmaticsSTTStream) flushPendingRawFinalEvents() []*stt.SpeechEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return speechmaticsFlushPendingRawFinals(s.state)
}

func (s *speechmaticsSTTStream) forcedEOUPartialEvents(resp smResponse) []*stt.SpeechEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return speechmaticsForcedEOUPartialEvents(resp, s.state)
}

func (s *speechmaticsSTTStream) responseEvents(resp smResponse) []*stt.SpeechEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return speechmaticsEvents(resp, s.state)
}

func (s *speechmaticsSTTStream) recordRecognitionStarted(resp smResponse) {
	if s == nil || resp.LanguagePackInfo.WordDelimiter == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		s.state = &speechmaticsStreamState{}
	}
	s.state.wordDelimiter = *resp.LanguagePackInfo.WordDelimiter
	s.state.wordDelimiterSet = true
}

func (s *speechmaticsSTTStream) enqueueError(err error) {
	if s == nil || s.errCh == nil || err == nil {
		return
	}
	select {
	case s.errCh <- err:
	default:
	}
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
	case "AddPartialTranscript", "AddTranscript":
		return speechmaticsTranscriptEvents(resp, state)
	case "AddPartialSegment", "AddSegment":
		return speechmaticsSegmentEvents(resp, state)
	case "StartOfTurn":
		return []*stt.SpeechEvent{{Type: stt.SpeechEventStartOfSpeech}}
	case "EndOfTurn":
		return speechmaticsEndOfTurnEvents(state)
	}
	return nil
}

func speechmaticsEndOfTurnEvents(state *speechmaticsStreamState) []*stt.SpeechEvent {
	events := speechmaticsFlushPendingRawFinals(state)
	events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech})
	if usage := speechmaticsRecognitionUsageEvent(state); usage != nil {
		events = append(events, usage)
	}
	return events
}

func speechmaticsFlushPendingRawFinals(state *speechmaticsStreamState) []*stt.SpeechEvent {
	if state == nil || len(state.pendingRawFinals) == 0 {
		return nil
	}
	events := state.pendingRawFinals
	state.pendingRawFinals = nil
	return events
}

type speechmaticsRawTranscriptFragment struct {
	text       string
	kind       string
	speakerID  string
	language   string
	attaches   string
	isEOS      bool
	startTime  float64
	endTime    float64
	confidence float64
}

func speechmaticsTranscriptEvents(resp smResponse, state *speechmaticsStreamState) []*stt.SpeechEvent {
	return speechmaticsTranscriptGroupedEvents(resp, state)
}

func speechmaticsTranscriptEvent(resp smResponse, state *speechmaticsStreamState) *stt.SpeechEvent {
	events := speechmaticsTranscriptGroupedEvents(resp, state)
	if len(events) == 0 {
		return nil
	}
	return events[0]
}

func speechmaticsTranscriptGroupedEvents(resp smResponse, state *speechmaticsStreamState) []*stt.SpeechEvent {
	if resp.Message == "AddPartialTranscript" {
		return speechmaticsRawPartialTranscriptEvents(resp, state)
	}
	eventType := stt.SpeechEventInterimTranscript
	if resp.Message == "AddTranscript" {
		eventType = stt.SpeechEventFinalTranscript
	}
	events := speechmaticsRawTranscriptEvents(resp, state, eventType)
	if resp.Message == "AddTranscript" && state != nil && state.bufferRawFinals {
		state.pendingRawFinals = append(state.pendingRawFinals, events...)
		return nil
	}
	return events
}

func speechmaticsRawPartialTranscriptEvents(resp smResponse, state *speechmaticsStreamState) []*stt.SpeechEvent {
	var events []*stt.SpeechEvent
	if state != nil && len(state.pendingRawFinals) > 0 {
		events = append(events, state.pendingRawFinals...)
		state.pendingRawFinals = nil
	}
	if state != nil && !state.includePartials {
		return events
	}
	events = append(events, speechmaticsRawTranscriptEvents(resp, state, stt.SpeechEventInterimTranscript)...)
	return events
}

func speechmaticsRawTranscriptEvents(resp smResponse, state *speechmaticsStreamState, eventType stt.SpeechEventType) []*stt.SpeechEvent {
	startTimeOffset := speechmaticsStartTimeOffset(state)
	var fragments []speechmaticsRawTranscriptFragment

	for _, result := range resp.Results {
		if len(result.Alternatives) == 0 {
			continue
		}
		alt := result.Alternatives[0]
		if alt.Content == "" {
			continue
		}
		resultSpeakerID := speechmaticsSegmentSpeakerID(alt.SpeakerID)
		if speechmaticsSpeakerFiltered(resultSpeakerID, state) {
			continue
		}
		language := speechmaticsSegmentLanguage(alt.Language, state)
		startTime := result.StartTime + startTimeOffset
		endTime := result.EndTime + startTimeOffset
		kind := result.Type
		if kind == "" {
			kind = "word"
		}
		fragments = append(fragments, speechmaticsRawTranscriptFragment{
			text:       alt.Content,
			kind:       kind,
			speakerID:  resultSpeakerID,
			language:   language,
			attaches:   result.Attaches,
			isEOS:      result.IsEOS,
			startTime:  startTime,
			endTime:    endTime,
			confidence: speechmaticsAlternativeConfidence(alt.Confidence),
		})
	}

	if len(fragments) > 0 {
		return speechmaticsRawTranscriptEventsFromFragments(eventType, fragments, state)
	}
	if len(resp.Results) > 0 || resp.Metadata.Transcript == "" {
		return nil
	}
	return []*stt.SpeechEvent{
		{
			Type: eventType,
			Alternatives: []stt.SpeechData{
				{
					Text:       resp.Metadata.Transcript,
					Language:   speechmaticsSegmentLanguage("", state),
					Confidence: 1.0,
					StartTime:  resp.Metadata.StartTime + startTimeOffset,
					EndTime:    resp.Metadata.EndTime + startTimeOffset,
				},
			},
		},
	}
}

func speechmaticsAlternativeConfidence(confidence *float64) float64 {
	if confidence == nil {
		return 1.0
	}
	return *confidence
}

func speechmaticsRawTranscriptEventsFromFragments(eventType stt.SpeechEventType, fragments []speechmaticsRawTranscriptFragment, state *speechmaticsStreamState) []*stt.SpeechEvent {
	var events []*stt.SpeechEvent
	groupStart := 0
	for i := 1; i <= len(fragments); i++ {
		if i < len(fragments) && fragments[i].speakerID == fragments[groupStart].speakerID && !speechmaticsSplitRawTranscriptAtEOS(eventType, fragments[i-1]) {
			continue
		}
		if event := speechmaticsRawTranscriptEventFromGroup(eventType, fragments[groupStart:i], state); event != nil {
			events = append(events, event)
		}
		groupStart = i
	}
	return events
}

func speechmaticsSplitRawTranscriptAtEOS(eventType stt.SpeechEventType, fragment speechmaticsRawTranscriptFragment) bool {
	return eventType == stt.SpeechEventFinalTranscript && fragment.isEOS
}

func speechmaticsRawTranscriptEventFromGroup(eventType stt.SpeechEventType, fragments []speechmaticsRawTranscriptFragment, state *speechmaticsStreamState) *stt.SpeechEvent {
	if len(fragments) == 0 {
		return nil
	}
	fragments = speechmaticsTrimRawTranscriptEdgePunctuation(fragments)
	if len(fragments) == 0 {
		return nil
	}
	text := ""
	var words []stt.TimedString
	var totalConfidence float64
	for i, fragment := range fragments {
		if i == 0 {
			text = fragment.text
		} else if fragment.attaches == "previous" || fragments[i-1].attaches == "next" {
			text += fragment.text
		} else {
			text += speechmaticsRawWordDelimiter(state) + fragment.text
		}
		totalConfidence += fragment.confidence
		if fragment.kind == "word" {
			words = append(words, stt.TimedString{
				Text:       fragment.text,
				StartTime:  fragment.startTime,
				EndTime:    fragment.endTime,
				Confidence: fragment.confidence,
				SpeakerID:  fragment.speakerID,
			})
		}
	}
	speakerID := fragments[0].speakerID
	text = speechmaticsFormattedSegmentText(text, speakerID, speechmaticsRawTranscriptSpeakerActive(speakerID, state), state)
	return &stt.SpeechEvent{
		Type: eventType,
		Alternatives: []stt.SpeechData{
			{
				Text:       text,
				Language:   fragments[0].language,
				Confidence: totalConfidence / float64(len(fragments)),
				SpeakerID:  speakerID,
				StartTime:  fragments[0].startTime,
				EndTime:    fragments[len(fragments)-1].endTime,
				Words:      words,
			},
		},
	}
}

func speechmaticsRawWordDelimiter(state *speechmaticsStreamState) string {
	if state != nil && state.wordDelimiterSet {
		return state.wordDelimiter
	}
	return " "
}

func speechmaticsRawTranscriptSpeakerActive(speakerID string, state *speechmaticsStreamState) *bool {
	if state == nil || len(state.focusSpeakers) == 0 {
		return nil
	}
	active := speechmaticsStringInSlice(speakerID, state.focusSpeakers)
	return &active
}

func speechmaticsTrimRawTranscriptEdgePunctuation(fragments []speechmaticsRawTranscriptFragment) []speechmaticsRawTranscriptFragment {
	if len(fragments) > 0 && fragments[0].attaches == "previous" {
		fragments = fragments[1:]
	}
	if len(fragments) > 0 && fragments[len(fragments)-1].attaches == "next" {
		fragments = fragments[:len(fragments)-1]
	}
	return fragments
}

func speechmaticsSegmentEvents(resp smResponse, state *speechmaticsStreamState) []*stt.SpeechEvent {
	if len(resp.Segments) == 0 {
		return nil
	}
	eventType := stt.SpeechEventInterimTranscript
	if resp.Message == "AddSegment" {
		eventType = stt.SpeechEventFinalTranscript
	}
	startTimeOffset := speechmaticsStartTimeOffset(state)

	events := make([]*stt.SpeechEvent, 0, len(resp.Segments))
	for _, segment := range resp.Segments {
		if eventType == stt.SpeechEventInterimTranscript && state != nil && !state.includePartials && !speechmaticsSegmentHasFinal(segment.Annotation) {
			continue
		}
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

func speechmaticsSegmentHasFinal(annotation []string) bool {
	for _, value := range annotation {
		if value == "has_final" {
			return true
		}
	}
	return false
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
	if s.inputEnded {
		s.mu.Unlock()
		return fmt.Errorf("stream input ended")
	}
	if s.closed {
		s.mu.Unlock()
		return io.ErrClosedPipe
	}
	if frame == nil {
		s.mu.Unlock()
		return nil
	}
	if s.pushedSampleRate != 0 && s.pushedSampleRate != frame.SampleRate {
		s.mu.Unlock()
		return fmt.Errorf("the sample rate of the input frames must be consistent")
	}
	s.pushedSampleRate = frame.SampleRate
	if len(frame.Data) == 0 {
		s.mu.Unlock()
		return nil
	}
	vadStream := s.vadStream
	bufferVAD := vadStream != nil && s.startupGateActiveLocked()
	if bufferVAD {
		s.pendingVADFrames = append(s.pendingVADFrames, frame)
	}
	s.mu.Unlock()
	if vadStream != nil && !bufferVAD {
		if err := vadStream.PushFrame(frame); err != nil {
			s.enqueueError(err)
			_ = s.Close()
			return err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.pushedSampleRate != 0 && s.pushedSampleRate != frame.SampleRate {
		return fmt.Errorf("the sample rate of the input frames must be consistent")
	}
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
			s.enqueueError(err)
			_ = s.closeLocked()
			return err
		}
		s.recordSentAudioDurationLocked(audio.CalculateFrameDuration(chunk))
	}
	return nil
}

func (s *speechmaticsSTTStream) EndInput() error {
	s.mu.Lock()
	if s.inputEnded {
		s.mu.Unlock()
		return fmt.Errorf("stream input ended")
	}
	if s.closed {
		s.mu.Unlock()
		return io.ErrClosedPipe
	}
	if tail := s.inputAudio.flush(); tail != nil {
		if err := s.writeAudioFrameLocked(tail); err != nil {
			_ = s.closeLocked()
			s.mu.Unlock()
			return err
		}
	}
	if s.audioBuf != nil {
		if s.state == nil {
			s.state = &speechmaticsStreamState{}
		}
		for _, chunk := range s.audioBuf.Flush() {
			if err := s.writeAudioChunkLocked(chunk.Data); err != nil {
				s.enqueueError(err)
				_ = s.closeLocked()
				s.mu.Unlock()
				return err
			}
			s.recordSentAudioDurationLocked(audio.CalculateFrameDuration(chunk))
		}
	}
	vadStream := s.vadStream
	bufferVADEndInput := vadStream != nil && s.startupGateActiveLocked()
	if bufferVADEndInput {
		s.pendingVADEndInput = true
	}
	s.mu.Unlock()

	if vadStream != nil && !bufferVADEndInput {
		if err := vadStream.EndInput(); err != nil {
			s.enqueueError(err)
			_ = s.Close()
			return err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}
	if s.closed {
		return io.ErrClosedPipe
	}
	s.inputEnded = true
	if s.startupGateActiveLocked() {
		s.pendingEndInput = true
		return nil
	}
	if err := s.writeJSONData(map[string]interface{}{"message": "EndOfStream"}); err != nil {
		s.enqueueError(err)
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
	if s.startupGateActiveLocked() {
		s.pendingAudioChunks = append(s.pendingAudioChunks, append([]byte(nil), data...))
		return nil
	}
	return s.writeBinaryData(data)
}

func (s *speechmaticsSTTStream) startupGateActiveLocked() bool {
	return s.waitForRecognitionStarted && (!s.audioReady || s.drainingStartup)
}

func (s *speechmaticsSTTStream) markReadyForAudio() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return io.ErrClosedPipe
	}
	s.audioReady = true
	s.drainingStartup = true
	s.mu.Unlock()

	for {
		s.mu.Lock()
		if s.closed {
			s.drainingStartup = false
			s.mu.Unlock()
			return io.ErrClosedPipe
		}
		pendingVADFrames := s.pendingVADFrames
		pendingVADEndInput := s.pendingVADEndInput
		pendingAudio := s.pendingAudioChunks
		pendingFinalize := s.pendingFinalize
		pendingEndInput := s.pendingEndInput
		vadStream := s.vadStream
		s.pendingVADFrames = nil
		s.pendingVADEndInput = false
		s.pendingAudioChunks = nil
		s.pendingFinalize = false
		s.pendingEndInput = false
		if len(pendingVADFrames) == 0 && !pendingVADEndInput && len(pendingAudio) == 0 && !pendingFinalize && !pendingEndInput {
			s.drainingStartup = false
			s.mu.Unlock()
			return nil
		}
		s.mu.Unlock()

		if vadStream != nil {
			for _, frame := range pendingVADFrames {
				if err := vadStream.PushFrame(frame); err != nil {
					_ = s.Close()
					return err
				}
			}
		}
		for _, chunk := range pendingAudio {
			if err := s.writeBinaryData(chunk); err != nil {
				_ = s.Close()
				return err
			}
		}
		if vadStream != nil && pendingVADEndInput {
			if err := vadStream.EndInput(); err != nil {
				s.enqueueError(err)
				_ = s.Close()
				return err
			}
		}
		if pendingFinalize {
			if err := s.sendForceEndOfUtterance(); err != nil {
				_ = s.Close()
				return err
			}
		}
		if pendingEndInput {
			if err := s.writeJSONData(map[string]interface{}{"message": "EndOfStream"}); err != nil {
				_ = s.Close()
				return err
			}
		}
	}
}

func (s *speechmaticsSTTStream) Finalize() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return io.ErrClosedPipe
	}
	if s.providerManagedEndpointing {
		fixedMode := s.owner != nil && s.owner.turnDetectionMode == "fixed"
		s.mu.Unlock()
		if fixedMode {
			for _, event := range s.fixedEOUEndEvents() {
				if !s.enqueueEvent(event) {
					return io.EOF
				}
			}
		}
		return nil
	}
	if s.startupGateActiveLocked() {
		s.pendingFinalize = true
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	if err := s.sendForceEndOfUtterance(); err != nil {
		s.enqueueError(err)
		_ = s.Close()
		return err
	}
	return nil
}

func (s *speechmaticsSTTStream) sendForceEndOfUtterance() error {
	seq, timestamp, ok := s.beginForcedEOU()
	if !ok {
		return nil
	}
	if err := s.writeJSONData(map[string]interface{}{
		"message":   "ForceEndOfUtterance",
		"timestamp": timestamp,
	}); err != nil {
		s.clearForcedEOU()
		return err
	}
	s.scheduleForcedEOUTimeout(seq)
	return nil
}

func (s *speechmaticsSTTStream) beginForcedEOU() (uint64, float64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.providerManagedEndpointing {
		return 0, 0, false
	}
	if s.forcedEOUPending {
		return 0, 0, false
	}
	s.forcedEOUPending = true
	s.forcedEOUSeq++
	timestamp := 0.0
	if s.state != nil {
		timestamp = s.state.audioSecondsSent
	}
	return s.forcedEOUSeq, timestamp, true
}

func (s *speechmaticsSTTStream) scheduleForcedEOUTimeout(seq uint64) {
	if s == nil || speechmaticsForcedEOUTimeout <= 0 {
		return
	}
	s.mu.Lock()
	timeout := speechmaticsForcedEOUTimeout
	s.mu.Unlock()

	go func() {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-s.done:
			return
		}
		if !s.consumeForcedEOU(seq) {
			return
		}
		for _, event := range s.forcedEOUEndEvents() {
			if !s.enqueueEvent(event) {
				return
			}
		}
	}()
}

func (s *speechmaticsSTTStream) consumeForcedEOU(seq uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !s.forcedEOUPending || s.forcedEOUSeq != seq {
		return false
	}
	s.forcedEOUPending = false
	return true
}

func (s *speechmaticsSTTStream) consumeCurrentForcedEOU() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !s.forcedEOUPending {
		return false
	}
	s.forcedEOUPending = false
	s.forcedEOUSeq++
	return true
}

func (s *speechmaticsSTTStream) forcedEOUActive() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed && s.forcedEOUPending
}

func (s *speechmaticsSTTStream) forcedEOUEndEvents() []*stt.SpeechEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.forcedEOUCompleted = true
	}
	return speechmaticsEndOfTurnEvents(s.state)
}

func (s *speechmaticsSTTStream) fixedEOUEndEvents() []*stt.SpeechEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.fixedEOUCompleted = true
	}
	return speechmaticsEndOfTurnEvents(s.state)
}

func (s *speechmaticsSTTStream) consumeCompletedForcedEOU() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.forcedEOUCompleted {
		return false
	}
	s.forcedEOUCompleted = false
	return true
}

func (s *speechmaticsSTTStream) consumeCompletedFixedEOU() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.fixedEOUCompleted {
		return false
	}
	s.fixedEOUCompleted = false
	return true
}

func (s *speechmaticsSTTStream) resetCompletedEOU() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.forcedEOUCompleted = false
	s.fixedEOUCompleted = false
	s.mu.Unlock()
}

func (s *speechmaticsSTTStream) clearForcedEOU() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.forcedEOUPending = false
	s.forcedEOUSeq++
	s.mu.Unlock()
}

func (s *speechmaticsSTTStream) startVAD(ctx context.Context) error {
	if s == nil || s.owner == nil || s.owner.vad == nil {
		return nil
	}
	vadStream, err := s.owner.vad.Stream(ctx)
	if err != nil {
		return err
	}
	s.vadStream = vadStream
	go s.runVAD(vadStream)
	return nil
}

func (s *speechmaticsSTTStream) runVAD(vadStream corevad.VADStream) {
	for {
		event, err := vadStream.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			s.enqueueError(err)
			_ = s.Close()
			return
		}
		if event != nil && event.Type == corevad.VADEventEndOfSpeech {
			if err := s.Finalize(); err != nil {
				s.enqueueError(err)
				_ = s.Close()
				return
			}
		}
	}
}

func (s *speechmaticsSTTStream) closeVADStream() {
	if s == nil {
		return
	}
	s.mu.Lock()
	vadStream := s.vadStream
	s.vadStream = nil
	s.mu.Unlock()
	if vadStream != nil {
		_ = vadStream.Close()
	}
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
	if focusSpeakers != nil {
		s.state.focusSpeakers = cloneSpeechmaticsStringSlice(focusSpeakers)
	}
	if ignoreSpeakers != nil {
		s.state.ignoreSpeakers = cloneSpeechmaticsStringSlice(ignoreSpeakers)
	}
	if focusMode != "" {
		s.state.focusMode = focusMode
	}
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
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}
	if s.closed {
		return io.ErrClosedPipe
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
			s.enqueueError(err)
			_ = s.closeLocked()
			return err
		}
		s.recordSentAudioDurationLocked(audio.CalculateFrameDuration(chunk))
	}
	return nil
}

func (s *speechmaticsSTTStream) recordSentAudioDurationLocked(duration float64) {
	if s.state == nil {
		s.state = &speechmaticsStreamState{}
	}
	s.state.speechDuration += duration
	s.state.audioSecondsSent += duration
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
	s.closeDone()
	_ = s.closeTransportOnce()
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
	s.pendingFinalize = false
	s.pendingAudioChunks = nil
	s.pendingVADFrames = nil
	s.pendingVADEndInput = false
	s.drainingStartup = false
	vadStream := s.vadStream
	s.vadStream = nil
	s.closeDone()
	defer func() {
		if s.owner != nil {
			s.owner.unregisterStream(s)
		}
	}()
	if vadStream != nil {
		_ = vadStream.Close()
	}
	_ = s.closeTransportOnce()
	return nil
}

func (s *speechmaticsSTTStream) closeTransportOnce() error {
	var closeErr error
	s.transportOnce.Do(func() {
		if s.conn != nil {
			closeErr = s.conn.Close()
			return
		}
		if s.closeConn != nil {
			closeErr = s.closeConn()
		}
	})
	return closeErr
}

func (s *speechmaticsSTTStream) Next() (*stt.SpeechEvent, error) {
	if s.isClosed() {
		if s.shouldDrainEventsAfterClose() {
			select {
			case event, ok := <-s.events:
				if ok {
					return event, nil
				}
			default:
			}
		}
		select {
		case err := <-s.errCh:
			return nil, err
		default:
		}
		return nil, io.EOF
	}
	if s.pendingErr != nil {
		select {
		case event, ok := <-s.events:
			if ok {
				return event, nil
			}
		default:
		}
		err := s.pendingErr
		s.pendingErr = nil
		s.markClosed()
		return nil, err
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
		s.pendingErr = err
		select {
		case event, ok := <-s.events:
			if ok {
				return event, nil
			}
		default:
		}
		s.pendingErr = nil
		s.markClosed()
		return nil, err
	case <-s.done:
		if s.shouldDrainEventsAfterClose() {
			select {
			case event, ok := <-s.events:
				if ok {
					return event, nil
				}
			default:
			}
		}
		return nil, io.EOF
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

func (s *speechmaticsSTTStream) markClosedDrainingEvents() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.drainEventsAfterClose = true
	s.mu.Unlock()
	s.closeDone()
	if s.owner != nil {
		s.owner.unregisterStream(s)
	}
}

func (s *speechmaticsSTTStream) shouldDrainEventsAfterClose() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.drainEventsAfterClose
}

func (s *speechmaticsSTTStream) closeDone() {
	if s == nil || s.done == nil {
		return
	}
	s.doneOnce.Do(func() {
		close(s.done)
	})
}
