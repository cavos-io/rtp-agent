package speechmatics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
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

const speechmaticsMinEndOfTurnDelay = 10 * time.Millisecond
const (
	speechmaticsAdaptiveVADStoppedPenalty         = 0.2
	speechmaticsAdaptiveNoEOSPenalty              = 2.0
	speechmaticsAdaptiveFinalEOSPenalty           = 0.5
	speechmaticsAnnotationEndsWithFinal           = "ends_with_final"
	speechmaticsAnnotationEndsWithEOS             = "ends_with_eos"
	speechmaticsAnnotationEndsWithDisfluency      = "ends_with_disfluency"
	speechmaticsAnnotationHasDisfluency           = "has_disfluency"
	speechmaticsAnnotationVerySlowSpeaker         = "very_slow_speaker"
	speechmaticsAnnotationSlowSpeaker             = "slow_speaker"
	speechmaticsAnnotationSmartTurnTrue           = "smart_turn_true"
	speechmaticsAnnotationSmartTurnFalse          = "smart_turn_false"
	speechmaticsAnnotationSmartTurnInactive       = "smart_turn_inactive"
	speechmaticsAnnotationVADStopped              = "vad_stopped"
	speechmaticsAdaptiveEndsWithDisfluencyPenalty = 2.5
	speechmaticsAdaptiveHasDisfluencyPenalty      = 1.1
	speechmaticsAdaptiveVerySlowSpeakerPenalty    = 3.0
	speechmaticsAdaptiveSlowSpeakerPenalty        = 2.0
	speechmaticsAdaptiveSmartTurnTruePenalty      = 0.2
)

var speechmaticsLocalEndpointingDelay = func(s *SpeechmaticsSTT) time.Duration {
	return speechmaticsLocalEndpointingDelayWithAnnotations(s, nil)
}

func speechmaticsLocalEndpointingDelayWithAnnotations(s *SpeechmaticsSTT, annotations []string) time.Duration {
	if s == nil {
		return 0
	}
	switch s.turnDetectionMode {
	case "adaptive":
		delay := 0.7
		if s.eouSilenceTrigger != nil {
			delay = *s.eouSilenceTrigger
		}
		delay *= speechmaticsAdaptiveVADStoppedPenalty
		delay *= speechmaticsAdaptiveAnnotationPenalty(annotations)
		if s.eouMaxDelay != nil && *s.eouMaxDelay < delay {
			delay = *s.eouMaxDelay
		}
		return speechmaticsClampLocalEndpointingDelay(delay)
	case "smart_turn":
		delay := 0.8
		if s.eouSilenceTrigger != nil {
			delay = *s.eouSilenceTrigger
		}
		delay *= speechmaticsAdaptiveAnnotationPenalty(speechmaticsSmartTurnLocalVADAnnotations(annotations))
		if s.eouMaxDelay != nil && *s.eouMaxDelay < delay {
			delay = *s.eouMaxDelay
		}
		return speechmaticsClampLocalEndpointingDelay(delay)
	default:
		return 0
	}
}

func speechmaticsAdaptiveAnnotationPenalty(annotations []string) float64 {
	if len(annotations) == 0 {
		return 1.0
	}
	penalty := 1.0
	if speechmaticsStringInSlice(speechmaticsAnnotationVerySlowSpeaker, annotations) {
		penalty *= speechmaticsAdaptiveVerySlowSpeakerPenalty
	}
	if speechmaticsStringInSlice(speechmaticsAnnotationSlowSpeaker, annotations) {
		penalty *= speechmaticsAdaptiveSlowSpeakerPenalty
	}
	if speechmaticsStringInSlice(speechmaticsAnnotationEndsWithDisfluency, annotations) {
		penalty *= speechmaticsAdaptiveEndsWithDisfluencyPenalty
	}
	if speechmaticsStringInSlice(speechmaticsAnnotationHasDisfluency, annotations) {
		penalty *= speechmaticsAdaptiveHasDisfluencyPenalty
	}
	if !speechmaticsStringInSlice(speechmaticsAnnotationEndsWithEOS, annotations) {
		penalty *= speechmaticsAdaptiveNoEOSPenalty
	}
	if speechmaticsStringInSlice(speechmaticsAnnotationEndsWithFinal, annotations) &&
		speechmaticsStringInSlice(speechmaticsAnnotationEndsWithEOS, annotations) {
		penalty *= speechmaticsAdaptiveFinalEOSPenalty
	}
	if speechmaticsStringInSlice(speechmaticsAnnotationSmartTurnTrue, annotations) {
		penalty *= speechmaticsAdaptiveSmartTurnTruePenalty
	}
	if speechmaticsStringInSlice(speechmaticsAnnotationVADStopped, annotations) &&
		speechmaticsStringInSlice(speechmaticsAnnotationSmartTurnInactive, annotations) {
		penalty *= speechmaticsAdaptiveVADStoppedPenalty
	}
	return penalty
}

func speechmaticsSmartTurnLocalVADAnnotations(annotations []string) []string {
	if speechmaticsStringInSlice(speechmaticsAnnotationSmartTurnTrue, annotations) ||
		speechmaticsStringInSlice(speechmaticsAnnotationSmartTurnFalse, annotations) {
		return annotations
	}
	withVAD := cloneSpeechmaticsStringSlice(annotations)
	withVAD = append(withVAD, speechmaticsAnnotationVADStopped, speechmaticsAnnotationSmartTurnInactive)
	return withVAD
}

func speechmaticsRoundEndOfTurnDelay(seconds float64) float64 {
	return math.Round(seconds*1000+1e-9) / 1000
}

func speechmaticsClampLocalEndpointingDelay(seconds float64) time.Duration {
	seconds = speechmaticsRoundEndOfTurnDelay(seconds)
	delay := time.Duration(seconds * float64(time.Second))
	if delay < speechmaticsMinEndOfTurnDelay {
		return speechmaticsMinEndOfTurnDelay
	}
	return delay
}

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
	if provider.vad != nil {
		provider.turnDetectionMode = "external"
	}
	switch provider.turnDetectionMode {
	case "external":
		if !provider.vadSet {
			provider.vad = silero.NewSileroVAD()
		}
	case "adaptive", "smart_turn":
		provider.vad = speechmaticsLocalEndpointingVAD()
	}
	return provider
}

func speechmaticsLocalEndpointingVAD() corevad.VAD {
	return silero.NewSileroVAD(
		silero.WithMinSilenceDuration(0.18),
		silero.WithActivationThreshold(0.35),
	)
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
	startupStarted := time.Now()

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
		startTimeOffset := streamStartTime.Sub(startupStarted).Seconds()
		if startTimeOffset < 0 {
			startTimeOffset = 0
		}
		stream := &speechmaticsSTTStream{
			conn:   conn,
			events: make(chan *stt.SpeechEvent, 10),
			errCh:  make(chan error, 1),
			done:   make(chan struct{}),
			state: &speechmaticsStreamState{
				language:                  streamLanguage,
				speakerActiveFormat:       s.speakerActiveFormat,
				speakerPassiveFormat:      s.speakerPassiveFormat,
				focusSpeakers:             cloneSpeechmaticsStringSlice(s.focusSpeakers),
				ignoreSpeakers:            cloneSpeechmaticsStringSlice(s.ignoreSpeakers),
				focusMode:                 s.focusMode,
				includePartials:           speechmaticsIncludePartials(s),
				bufferRawFinals:           true,
				startTimeOffset:           startTimeOffset,
				startTime:                 float64(streamStartTime.UnixNano()) / 1e9,
				splitRawFinalEOSSentences: false,
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
	drainEvents                []*stt.SpeechEvent
	overflowEvents             []*stt.SpeechEvent
	forcedEOUPending           bool
	pendingLocalEndpointingEOU bool
	forcedEOUSeq               uint64
	forcedEOUCompleted         bool
	fixedEOUCompleted          bool
	completedEOUNewTurnStarted bool
	localEndpointingEOUSeq     uint64
	localEndpointingTurnClosed bool
}

type speechmaticsStreamState struct {
	language                   string
	speechDuration             float64
	audioSecondsSent           float64
	startTimeOffset            float64
	startTime                  float64
	speakerActiveFormat        string
	speakerPassiveFormat       string
	focusSpeakers              []string
	ignoreSpeakers             []string
	focusMode                  string
	includePartials            bool
	wordDelimiter              string
	wordDelimiterSet           bool
	bufferRawFinals            bool
	pendingRawFinals           []*stt.SpeechEvent
	rawTrimBeforeTimeSet       bool
	rawTrimBeforeTime          float64
	latestRawPartialEvents     []*stt.SpeechEvent
	splitRawFinalEOSSentences  bool
	turnHasTranscript          bool
	latestSegmentAnnotationSet bool
	latestSegmentAnnotation    []string
}

type smAlternative struct {
	Content    string   `json:"content"`
	Confidence *float64 `json:"confidence"`
	SpeakerID  string   `json:"speaker"`
	Language   string   `json:"language"`
	Tags       []string `json:"tags,omitempty"`
}

func (a *smAlternative) UnmarshalJSON(data []byte) error {
	var raw struct {
		Content    json.RawMessage `json:"content"`
		Confidence json.RawMessage `json:"confidence"`
		SpeakerID  string          `json:"speaker"`
		Language   string          `json:"language"`
		Tags       json.RawMessage `json:"tags"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*a = smAlternative{
		SpeakerID: raw.SpeakerID,
		Language:  raw.Language,
	}
	if len(raw.Content) > 0 {
		content, err := speechmaticsUnmarshalReferenceContent(raw.Content)
		if err != nil {
			return fmt.Errorf("content: %w", err)
		}
		a.Content = content
	}
	if len(raw.Confidence) > 0 {
		confidence, err := speechmaticsUnmarshalReferenceFloat(raw.Confidence)
		if err != nil {
			return fmt.Errorf("confidence: %w", err)
		}
		a.Confidence = &confidence
	}
	if len(raw.Tags) == 0 {
		return nil
	}
	var tagItems []json.RawMessage
	if err := json.Unmarshal(raw.Tags, &tagItems); err == nil {
		a.Tags = make([]string, 0, len(tagItems))
		for _, item := range tagItems {
			var tag string
			if err := json.Unmarshal(item, &tag); err == nil {
				a.Tags = append(a.Tags, tag)
			}
		}
		return nil
	}
	var tag string
	if err := json.Unmarshal(raw.Tags, &tag); err != nil {
		var tagMap map[string]json.RawMessage
		if mapErr := json.Unmarshal(raw.Tags, &tagMap); mapErr != nil {
			return err
		}
		a.Tags = make([]string, 0, len(tagMap))
		for key := range tagMap {
			a.Tags = append(a.Tags, key)
		}
		sort.Strings(a.Tags)
		return nil
	}
	a.Tags = []string{tag}
	if strings.Contains(tag, "disfluency") && tag != "disfluency" {
		a.Tags = append(a.Tags, "disfluency")
	}
	return nil
}

func speechmaticsUnmarshalReferenceContent(data []byte) (string, error) {
	if string(data) == "null" {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		return text, nil
	}
	var boolValue bool
	if err := json.Unmarshal(data, &boolValue); err == nil {
		if !boolValue {
			return "", nil
		}
		return "", fmt.Errorf("true is not reference-falsey")
	}
	var number float64
	if err := json.Unmarshal(data, &number); err == nil {
		if number == 0 {
			return "", nil
		}
		return "", fmt.Errorf("number %v is not reference-falsey", number)
	}
	var array []json.RawMessage
	if err := json.Unmarshal(data, &array); err == nil {
		if len(array) == 0 {
			return "", nil
		}
		return "", fmt.Errorf("array is not reference-falsey")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err == nil {
		if len(object) == 0 {
			return "", nil
		}
		return "", fmt.Errorf("object is not reference-falsey")
	}
	return "", fmt.Errorf("must be string or reference-falsey")
}

type smResult struct {
	Alternatives []smAlternative `json:"alternatives"`
	Type         string          `json:"type"`
	Attaches     string          `json:"attaches_to"`
	IsEOS        bool            `json:"is_eos"`
	StartTime    float64         `json:"start_time"`
	EndTime      float64         `json:"end_time"`
}

func (r *smResult) UnmarshalJSON(data []byte) error {
	type result smResult
	var raw struct {
		result
		StartTime json.RawMessage `json:"start_time"`
		EndTime   json.RawMessage `json:"end_time"`
		IsEOS     json.RawMessage `json:"is_eos"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*r = smResult(raw.result)
	var err error
	if len(raw.StartTime) > 0 {
		r.StartTime, err = speechmaticsUnmarshalReferenceFloat(raw.StartTime)
		if err != nil {
			return fmt.Errorf("start_time: %w", err)
		}
	}
	if len(raw.EndTime) > 0 {
		r.EndTime, err = speechmaticsUnmarshalReferenceFloat(raw.EndTime)
		if err != nil {
			return fmt.Errorf("end_time: %w", err)
		}
	}
	if len(raw.IsEOS) > 0 {
		r.IsEOS, err = speechmaticsUnmarshalReferenceBool(raw.IsEOS)
		if err != nil {
			return fmt.Errorf("is_eos: %w", err)
		}
	}
	return nil
}

func speechmaticsUnmarshalReferenceFloat(data []byte) (float64, error) {
	var value float64
	if err := json.Unmarshal(data, &value); err == nil {
		return value, nil
	}
	var boolValue bool
	if err := json.Unmarshal(data, &boolValue); err == nil {
		if boolValue {
			return 1, nil
		}
		return 0, nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return 0, err
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func speechmaticsUnmarshalReferenceBool(data []byte) (bool, error) {
	var value bool
	if err := json.Unmarshal(data, &value); err == nil {
		return value, nil
	}
	var number float64
	if err := json.Unmarshal(data, &number); err == nil {
		switch number {
		case 0:
			return false, nil
		case 1:
			return true, nil
		default:
			return false, fmt.Errorf("invalid bool number %v", number)
		}
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return false, err
	}
	switch strings.ToLower(text) {
	case "yes", "on", "y":
		return true, nil
	case "no", "off", "n":
		return false, nil
	}
	value, err := strconv.ParseBool(text)
	if err != nil {
		return false, err
	}
	return value, nil
}

func speechmaticsUnmarshalReferenceTruthyBool(data []byte) (bool, error) {
	var value bool
	if err := json.Unmarshal(data, &value); err == nil {
		return value, nil
	}
	var number float64
	if err := json.Unmarshal(data, &number); err == nil {
		return number != 0, nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		if value, ok := speechmaticsParseReferenceBoolString(text); ok {
			return value, nil
		}
		return text != "", nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(data, &list); err == nil {
		return len(list) > 0, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err == nil {
		return len(object) > 0, nil
	}
	return false, fmt.Errorf("unsupported truthy bool")
}

func speechmaticsParseReferenceBoolString(text string) (bool, bool) {
	switch strings.ToLower(text) {
	case "yes", "on", "y", "true", "t", "1":
		return true, true
	case "no", "off", "n", "false", "f", "0":
		return false, true
	default:
		return false, false
	}
}

func speechmaticsUnmarshalReferenceSegmentTiming(data []byte) (float64, bool) {
	var value bool
	if err := json.Unmarshal(data, &value); err == nil {
		if value {
			return 1, true
		}
		return 0, true
	}
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		number, err := strconv.ParseFloat(text, 64)
		if err == nil {
			return number, true
		}
	}
	return 0, false
}

func speechmaticsUnmarshalReferenceSegmentText(data []byte) (string, error) {
	if string(data) == "null" {
		return "None", nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		return text, nil
	}
	var value bool
	if err := json.Unmarshal(data, &value); err == nil {
		if value {
			return "True", nil
		}
		return "False", nil
	}
	var number json.Number
	if err := json.Unmarshal(data, &number); err == nil {
		return number.String(), nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(data, &list); err == nil {
		return speechmaticsReferenceListText(list)
	}
	if text, err := speechmaticsReferenceObjectTextRaw(data); err == nil {
		return text, nil
	}
	return "", fmt.Errorf("unsupported segment text")
}

func speechmaticsUnmarshalReferenceSegmentSpeakerID(data []byte) (string, error) {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		return text, nil
	}
	var number json.Number
	if err := json.Unmarshal(data, &number); err == nil {
		return number.String(), nil
	}
	var value bool
	if err := json.Unmarshal(data, &value); err == nil {
		if value {
			return "True", nil
		}
		return "False", nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(data, &list); err == nil {
		return speechmaticsReferenceListText(list)
	}
	if text, err := speechmaticsReferenceObjectTextRaw(data); err == nil {
		return text, nil
	}
	return "", fmt.Errorf("unsupported segment speaker id")
}

func speechmaticsUnmarshalReferenceSegmentLanguage(data []byte) (string, error) {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		return text, nil
	}
	var number json.Number
	if err := json.Unmarshal(data, &number); err == nil {
		return number.String(), nil
	}
	var value bool
	if err := json.Unmarshal(data, &value); err == nil {
		if value {
			return "True", nil
		}
		return "False", nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(data, &list); err == nil {
		return speechmaticsReferenceListText(list)
	}
	if text, err := speechmaticsReferenceObjectTextRaw(data); err == nil {
		return text, nil
	}
	return "", fmt.Errorf("unsupported segment language")
}

func speechmaticsReferenceListText(items []json.RawMessage) (string, error) {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		text, err := speechmaticsReferenceTextValue(item)
		if err != nil {
			return "", err
		}
		parts = append(parts, text)
	}
	return "[" + strings.Join(parts, ", ") + "]", nil
}

func speechmaticsReferenceObjectTextRaw(data []byte) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return "", err
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != '{' {
		return "", fmt.Errorf("not an object")
	}
	parts := make([]string, 0)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return "", err
		}
		key, ok := keyToken.(string)
		if !ok {
			return "", fmt.Errorf("object key must be a string")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return "", err
		}
		text, err := speechmaticsReferenceTextValue(value)
		if err != nil {
			return "", err
		}
		parts = append(parts, speechmaticsReferenceStringText(key)+": "+text)
	}
	token, err = decoder.Token()
	if err != nil {
		return "", err
	}
	delim, ok = token.(json.Delim)
	if !ok || delim != '}' {
		return "", fmt.Errorf("object is not closed")
	}
	return "{" + strings.Join(parts, ", ") + "}", nil
}

func speechmaticsReferenceTextValue(data []byte) (string, error) {
	if string(data) == "null" {
		return "None", nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		return speechmaticsReferenceStringText(text), nil
	}
	var value bool
	if err := json.Unmarshal(data, &value); err == nil {
		if value {
			return "True", nil
		}
		return "False", nil
	}
	var number json.Number
	if err := json.Unmarshal(data, &number); err == nil {
		return number.String(), nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(data, &list); err == nil {
		return speechmaticsReferenceListText(list)
	}
	if text, err := speechmaticsReferenceObjectTextRaw(data); err == nil {
		return text, nil
	}
	return "", fmt.Errorf("unsupported list text value")
}

func speechmaticsReferenceStringText(text string) string {
	if strings.Contains(text, "'") && !strings.Contains(text, `"`) {
		return strconv.Quote(text)
	}
	replacer := strings.NewReplacer(
		"\\", `\\`,
		"\n", `\n`,
		"\r", `\r`,
		"\t", `\t`,
		"'", `\'`,
	)
	return "'" + replacer.Replace(text) + "'"
}

func speechmaticsNormalizeSegments(data []byte) ([]byte, error) {
	var raw struct {
		Segments []map[string]json.RawMessage `json:"segments"`
	}
	if err := json.Unmarshal(data, &raw); err != nil || len(raw.Segments) == 0 {
		return data, err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, err
	}
	changed := false
	for i, segment := range raw.Segments {
		if text, ok := segment["text"]; ok {
			value, err := speechmaticsUnmarshalReferenceSegmentText(text)
			if err != nil {
				return nil, fmt.Errorf("segments[%d].text: %w", i, err)
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			segment["text"] = encoded
			changed = true
		}
		if speakerID, ok := segment["speaker_id"]; ok && string(speakerID) != "null" {
			value, err := speechmaticsUnmarshalReferenceSegmentSpeakerID(speakerID)
			if err != nil {
				return nil, fmt.Errorf("segments[%d].speaker_id: %w", i, err)
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			segment["speaker_id"] = encoded
			changed = true
		}
		if language, ok := segment["language"]; ok && string(language) != "null" {
			value, err := speechmaticsUnmarshalReferenceSegmentLanguage(language)
			if err != nil {
				return nil, fmt.Errorf("segments[%d].language: %w", i, err)
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			segment["language"] = encoded
			changed = true
		}
		if metadataRaw, ok := segment["metadata"]; ok && string(metadataRaw) != "null" {
			var metadata map[string]json.RawMessage
			if err := json.Unmarshal(metadataRaw, &metadata); err == nil {
				metadataChanged := false
				for _, field := range []string{"start_time", "end_time"} {
					rawTiming, ok := metadata[field]
					if !ok || string(rawTiming) == "null" {
						continue
					}
					value, converted := speechmaticsUnmarshalReferenceSegmentTiming(rawTiming)
					if !converted {
						continue
					}
					encoded, err := json.Marshal(value)
					if err != nil {
						return nil, err
					}
					metadata[field] = encoded
					metadataChanged = true
					changed = true
				}
				if metadataChanged {
					encodedMetadata, err := json.Marshal(metadata)
					if err != nil {
						return nil, err
					}
					segment["metadata"] = encodedMetadata
				}
			}
		}
		if annotation, ok := segment["annotation"]; ok && string(annotation) != "null" {
			var values []string
			if err := json.Unmarshal(annotation, &values); err != nil {
				segment["annotation"] = []byte("[]")
				changed = true
			}
		}
		isActive, ok := segment["is_active"]
		if !ok || string(isActive) == "null" {
			continue
		}
		value, err := speechmaticsUnmarshalReferenceTruthyBool(isActive)
		if err != nil {
			return nil, fmt.Errorf("segments[%d].is_active: %w", i, err)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		segment["is_active"] = encoded
		changed = true
	}
	if !changed {
		return data, nil
	}
	segments, err := json.Marshal(raw.Segments)
	if err != nil {
		return nil, err
	}
	top["segments"] = segments
	return json.Marshal(top)
}

func speechmaticsNormalizeFalseyRawResults(data []byte) ([]byte, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return data, err
	}
	var message string
	if err := json.Unmarshal(top["message"], &message); err != nil {
		return data, nil
	}
	if message != "AddTranscript" && message != "AddPartialTranscript" {
		return data, nil
	}
	resultsData, ok := top["results"]
	if !ok || string(resultsData) == "null" {
		return data, nil
	}
	var results []json.RawMessage
	if err := json.Unmarshal(resultsData, &results); err != nil {
		return data, nil
	}
	changed := false
	for i, resultData := range results {
		keep, ok := speechmaticsRawResultHasReferenceContent(resultData)
		if !ok || keep {
			continue
		}
		results[i] = []byte(`{"alternatives":[{}]}`)
		changed = true
	}
	if !changed {
		return data, nil
	}
	resultsData, err := json.Marshal(results)
	if err != nil {
		return nil, err
	}
	top["results"] = resultsData
	return json.Marshal(top)
}

func speechmaticsNormalizeFinalRawMetadataWithResults(data []byte) ([]byte, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return data, err
	}
	var message string
	if err := json.Unmarshal(top["message"], &message); err != nil || message != "AddTranscript" {
		return data, nil
	}
	resultsData, ok := top["results"]
	if !ok || string(resultsData) == "null" {
		return data, nil
	}
	var results []json.RawMessage
	if err := json.Unmarshal(resultsData, &results); err != nil {
		return data, nil
	}
	metadata, ok := top["metadata"]
	if !ok || string(metadata) == "null" {
		return data, nil
	}
	var metadataObject map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &metadataObject); err != nil {
		return data, nil
	}
	top["metadata"] = []byte(`{}`)
	return json.Marshal(top)
}

func speechmaticsNormalizePartialRawMetadataWithResults(data []byte) ([]byte, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return data, err
	}
	var message string
	if err := json.Unmarshal(top["message"], &message); err != nil || message != "AddPartialTranscript" {
		return data, nil
	}
	resultsData, ok := top["results"]
	if !ok || string(resultsData) == "null" {
		return data, nil
	}
	var results []json.RawMessage
	if err := json.Unmarshal(resultsData, &results); err != nil {
		return data, nil
	}
	metadata, ok := top["metadata"]
	if !ok || string(metadata) == "null" {
		return data, nil
	}
	var metadataObject map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &metadataObject); err != nil {
		return data, nil
	}
	normalized := map[string]json.RawMessage{}
	if endTime, ok := metadataObject["end_time"]; ok {
		var boolValue bool
		if err := json.Unmarshal(endTime, &boolValue); err == nil {
			if boolValue {
				normalized["end_time"] = []byte(`1`)
			} else {
				normalized["end_time"] = []byte(`0`)
			}
		} else {
			normalized["end_time"] = endTime
		}
	}
	metadataData, err := json.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	top["metadata"] = metadataData
	return json.Marshal(top)
}

func speechmaticsRawResultHasReferenceContent(data []byte) (bool, bool) {
	var result map[string]json.RawMessage
	if err := json.Unmarshal(data, &result); err != nil {
		return false, false
	}
	if result == nil {
		return false, false
	}
	alternativesData, ok := result["alternatives"]
	if !ok {
		return false, true
	}
	var alternatives []json.RawMessage
	if err := json.Unmarshal(alternativesData, &alternatives); err != nil || len(alternatives) == 0 {
		return false, false
	}
	var alternative map[string]json.RawMessage
	if err := json.Unmarshal(alternatives[0], &alternative); err != nil {
		return false, false
	}
	if alternative == nil {
		return false, false
	}
	contentData, ok := alternative["content"]
	if !ok {
		return false, true
	}
	content, err := speechmaticsUnmarshalReferenceContent(contentData)
	if err != nil {
		return true, true
	}
	return content != "", true
}

type smResponse struct {
	Message  string `json:"message"`
	Metadata struct {
		Transcript string  `json:"transcript"`
		StartTime  float64 `json:"start_time"`
		EndTime    float64 `json:"end_time"`
	} `json:"metadata"`
	Results  []smResult `json:"results"`
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
	Speakers                []SpeechmaticsSpeakerIdentifier `json:"speakers"`
	rawLanguagePresent      []bool
	rawSpeakerPresent       []bool
	rawSpeakerNull          []bool
	segmentLanguagePresent  []bool
	segmentIsActivePresent  []bool
	segmentSpeakerIDPresent []bool
	segmentSpeakerIDNull    []bool
}

func (r *smResponse) UnmarshalJSON(data []byte) error {
	var err error
	data, err = speechmaticsNormalizeSegments(data)
	if err != nil {
		return err
	}
	data, err = speechmaticsNormalizeFalseyRawResults(data)
	if err != nil {
		return err
	}
	data, err = speechmaticsNormalizeFinalRawMetadataWithResults(data)
	if err != nil {
		return err
	}
	data, err = speechmaticsNormalizePartialRawMetadataWithResults(data)
	if err != nil {
		return err
	}
	type response smResponse
	var decoded response
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw struct {
		Message  string                       `json:"message"`
		Metadata json.RawMessage              `json:"metadata"`
		Results  json.RawMessage              `json:"results"`
		Segments []map[string]json.RawMessage `json:"segments"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if (raw.Message == "AddTranscript" || raw.Message == "AddPartialTranscript") && string(raw.Metadata) == "null" {
		return fmt.Errorf("metadata must be an object")
	}
	type rawResult struct {
		Alternatives json.RawMessage `json:"alternatives"`
		IsEOS        json.RawMessage `json:"is_eos"`
		Attaches     json.RawMessage `json:"attaches_to"`
		Type         json.RawMessage `json:"type"`
		StartTime    json.RawMessage `json:"start_time"`
		EndTime      json.RawMessage `json:"end_time"`
		Volume       json.RawMessage `json:"volume"`
	}
	var rawResultItems []json.RawMessage
	if len(raw.Results) > 0 {
		if (raw.Message == "AddTranscript" || raw.Message == "AddPartialTranscript") && string(raw.Results) == "null" {
			return fmt.Errorf("results must be an array")
		}
		if err := json.Unmarshal(raw.Results, &rawResultItems); err != nil {
			return fmt.Errorf("results must be an array: %w", err)
		}
	}
	if len(rawResultItems) > 0 {
		decoded.rawLanguagePresent = make([]bool, len(rawResultItems))
		decoded.rawSpeakerPresent = make([]bool, len(rawResultItems))
		decoded.rawSpeakerNull = make([]bool, len(rawResultItems))
		for i, resultData := range rawResultItems {
			if (raw.Message == "AddTranscript" || raw.Message == "AddPartialTranscript") && string(resultData) == "null" {
				return fmt.Errorf("results[%d] must be an object", i)
			}
			var result rawResult
			if err := json.Unmarshal(resultData, &result); err != nil {
				return fmt.Errorf("results[%d] must be an object: %w", i, err)
			}
			if (raw.Message == "AddTranscript" || raw.Message == "AddPartialTranscript") &&
				string(result.IsEOS) == "null" {
				return fmt.Errorf("results[%d].is_eos must be a bool", i)
			}
			if (raw.Message == "AddTranscript" || raw.Message == "AddPartialTranscript") &&
				string(result.Attaches) == "null" {
				return fmt.Errorf("results[%d].attaches_to must be a string", i)
			}
			if (raw.Message == "AddTranscript" || raw.Message == "AddPartialTranscript") &&
				string(result.Type) == "null" {
				return fmt.Errorf("results[%d].type must be a string", i)
			}
			if (raw.Message == "AddTranscript" || raw.Message == "AddPartialTranscript") &&
				string(result.StartTime) == "null" {
				return fmt.Errorf("results[%d].start_time must be a number", i)
			}
			if (raw.Message == "AddTranscript" || raw.Message == "AddPartialTranscript") &&
				string(result.EndTime) == "null" {
				return fmt.Errorf("results[%d].end_time must be a number", i)
			}
			if raw.Message == "AddTranscript" || raw.Message == "AddPartialTranscript" {
				if len(result.Volume) > 0 && string(result.Volume) != "null" {
					if _, err := speechmaticsUnmarshalReferenceFloat(result.Volume); err != nil {
						return fmt.Errorf("results[%d].volume must be a number", i)
					}
				}
			}
			if len(result.Alternatives) == 0 {
				continue
			}
			var alternativeItems []json.RawMessage
			if err := json.Unmarshal(result.Alternatives, &alternativeItems); err != nil {
				return fmt.Errorf("results[%d].alternatives must be an array", i)
			}
			if (raw.Message == "AddTranscript" || raw.Message == "AddPartialTranscript") &&
				len(alternativeItems) == 0 {
				return fmt.Errorf("results[%d].alternatives must contain at least one alternative", i)
			}
			if len(alternativeItems) == 0 {
				continue
			}
			if (raw.Message == "AddTranscript" || raw.Message == "AddPartialTranscript") && string(alternativeItems[0]) == "null" {
				return fmt.Errorf("results[%d].alternatives[0] must be an object", i)
			}
			var alternative map[string]json.RawMessage
			if err := json.Unmarshal(alternativeItems[0], &alternative); err != nil {
				return fmt.Errorf("results[%d].alternatives[0] must be an object: %w", i, err)
			}
			if (raw.Message == "AddTranscript" || raw.Message == "AddPartialTranscript") &&
				string(alternative["tags"]) == "null" {
				return fmt.Errorf("results[%d].alternatives[0].tags must be an array", i)
			}
			if (raw.Message == "AddTranscript" || raw.Message == "AddPartialTranscript") &&
				string(alternative["language"]) == "null" {
				return fmt.Errorf("results[%d].alternatives[0].language must be a string", i)
			}
			if (raw.Message == "AddTranscript" || raw.Message == "AddPartialTranscript") &&
				string(alternative["confidence"]) == "null" {
				return fmt.Errorf("results[%d].alternatives[0].confidence must be a number", i)
			}
			if (raw.Message == "AddTranscript" || raw.Message == "AddPartialTranscript") &&
				string(alternative["direction"]) == "null" {
				return fmt.Errorf("results[%d].alternatives[0].direction must be a string", i)
			}
			_, decoded.rawLanguagePresent[i] = alternative["language"]
			speaker, speakerPresent := alternative["speaker"]
			decoded.rawSpeakerPresent[i] = speakerPresent
			decoded.rawSpeakerNull[i] = speakerPresent && string(speaker) == "null"
		}
	}
	if len(raw.Segments) > 0 {
		decoded.segmentLanguagePresent = make([]bool, len(raw.Segments))
		decoded.segmentIsActivePresent = make([]bool, len(raw.Segments))
		decoded.segmentSpeakerIDPresent = make([]bool, len(raw.Segments))
		decoded.segmentSpeakerIDNull = make([]bool, len(raw.Segments))
		for i, segment := range raw.Segments {
			if segment == nil {
				return fmt.Errorf("segments[%d] must be an object", i)
			}
			if text, ok := segment["text"]; ok && string(text) == "null" {
				decoded.Segments[i].Text = "None"
			}
			if metadata, ok := segment["metadata"]; ok && string(metadata) == "null" {
				return fmt.Errorf("segments[%d].metadata must be an object", i)
			}
			if language, ok := segment["language"]; ok && string(language) == "null" {
				return fmt.Errorf("segments[%d].language must be a string", i)
			}
			_, decoded.segmentLanguagePresent[i] = segment["language"]
			_, decoded.segmentIsActivePresent[i] = segment["is_active"]
			_, decoded.segmentSpeakerIDPresent[i] = segment["speaker_id"]
			if speakerID, ok := segment["speaker_id"]; ok && string(speakerID) == "null" {
				decoded.segmentSpeakerIDNull[i] = true
			}
		}
	}
	*r = smResponse(decoded)
	return nil
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
					s.prepareDrainPendingRawFinals()
					s.enqueueError(llm.NewAPIConnectionError("Speechmatics STT WebSocket closed unexpectedly"))
					s.markClosedDrainingEvents()
				}
			} else {
				s.prepareDrainPendingRawFinals()
				s.enqueueError(llm.NewAPIConnectionError(err.Error()))
				s.markClosedDrainingEvents()
			}
			return
		}

		var resp smResponse
		if err := json.Unmarshal(message, &resp); err != nil {
			if json.Valid(message) {
				messageName := speechmaticsMessageName(message)
				if speechmaticsControlMessage(messageName) {
					if !s.handleResponse(smResponse{Message: messageName}) {
						return
					}
					continue
				}
				if messageName == "SpeakersResult" {
					s.recordSpeakerResult(nil)
					continue
				}
				if speechmaticsDataMessageName(messageName) {
					_ = s.closeTransportOnce()
					s.prepareDrainPendingRawFinals()
					s.enqueueError(llm.NewAPIConnectionError(fmt.Sprintf("Invalid Speechmatics message: %v", err)))
					s.markClosedDrainingEvents()
					return
				}
				continue
			}
			_ = s.closeTransportOnce()
			s.prepareDrainPendingRawFinals()
			s.enqueueError(llm.NewAPIConnectionError(fmt.Sprintf("Invalid JSON received: %v", err)))
			s.markClosedDrainingEvents()
			return
		}

		if !s.handleResponse(resp) {
			return
		}
	}
}

func speechmaticsControlMessage(message string) bool {
	switch message {
	case "RecognitionStarted", "StartOfTurn", "EndOfTurn", "EndOfUtterance", "EndOfTranscript":
		return true
	default:
		return false
	}
}

func speechmaticsMessageName(data []byte) string {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return ""
	}
	var message string
	if err := json.Unmarshal(raw["message"], &message); err != nil {
		return ""
	}
	return message
}

func speechmaticsDataMessageName(message string) bool {
	switch message {
	case "AddPartialSegment", "AddSegment", "AddPartialTranscript", "AddTranscript", "SpeakersResult":
		return true
	default:
		return false
	}
}

func (s *speechmaticsSTTStream) handleResponse(resp smResponse) bool {
	if resp.Message == "EndOfTranscript" {
		_ = s.closeTransportOnce()
		s.closeLocalEndpointingTurn()
		s.prepareDrainPendingRawFinals()
		s.markClosedDrainingEvents()
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
		s.reopenLocalEndpointingTurn()
		s.clearForcedEOU()
		s.markCompletedEOUNewTurnStarted()
	}
	if speechmaticsTranscriptMessage(resp.Message) {
		s.resetCompletedEOUAfterNewTurnContent()
	}
	if resp.Message == "EndOfTurn" {
		s.closeLocalEndpointingTurn()
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

func speechmaticsTranscriptMessage(message string) bool {
	return message == "AddPartialSegment" || message == "AddSegment" || message == "AddPartialTranscript" || message == "AddTranscript"
}

func speechmaticsForcedEOUPartialEvents(resp smResponse, state *speechmaticsStreamState) []*stt.SpeechEvent {
	if resp.Message == "AddPartialTranscript" {
		return speechmaticsFlushPendingRawFinals(state)
	}
	return nil
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
	if s.done != nil {
		select {
		case <-s.done:
			return false
		default:
		}
	}
	if s.hasOverflowEvents() {
		s.queueOverflowEvent(event)
		return true
	}
	if s.done == nil {
		select {
		case s.events <- event:
			return true
		default:
			s.queueOverflowEvent(event)
			return true
		}
	}
	select {
	case s.events <- event:
		return true
	case <-s.done:
		return false
	default:
		s.queueOverflowEvent(event)
		return true
	}
}

func (s *speechmaticsSTTStream) hasOverflowEvents() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.overflowEvents) > 0
}

func (s *speechmaticsSTTStream) queueOverflowEvent(event *stt.SpeechEvent) {
	if s == nil || event == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.overflowEvents = append(s.overflowEvents, event)
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
		speechmaticsClearLatestRawPartialEvents(state)
		speechmaticsClearTurnTranscriptEvidence(state)
		events := speechmaticsFlushPendingRawFinals(state)
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech})
		return events
	case "EndOfTurn":
		defer speechmaticsClearLatestRawPartialEvents(state)
		defer speechmaticsClearTurnTranscriptEvidence(state)
		return speechmaticsEndOfTurnEvents(state)
	}
	return nil
}

func speechmaticsClearLatestRawPartialEvents(state *speechmaticsStreamState) {
	if state != nil {
		state.latestRawPartialEvents = nil
	}
}

func speechmaticsClearTurnTranscriptEvidence(state *speechmaticsStreamState) {
	if state != nil {
		state.turnHasTranscript = false
	}
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
	speechmaticsRecordRawFinalTrimBeforeTime(state, events)
	return events
}

type speechmaticsRawTranscriptFragment struct {
	text            string
	kind            string
	speakerID       string
	speakerGroupID  string
	formatSpeakerID string
	language        string
	attaches        string
	isEOS           bool
	startTime       float64
	endTime         float64
	confidence      float64
	disfluency      bool
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
		if len(events) > 0 {
			state.turnHasTranscript = true
		}
		state.pendingRawFinals = append(state.pendingRawFinals, events...)
		return nil
	}
	if resp.Message == "AddTranscript" {
		speechmaticsRecordRawFinalTrimBeforeTime(state, events)
	}
	return events
}

func speechmaticsRawPartialTranscriptEvents(resp smResponse, state *speechmaticsStreamState) []*stt.SpeechEvent {
	var events []*stt.SpeechEvent
	var flushedFinals []*stt.SpeechEvent
	if state != nil && len(state.pendingRawFinals) > 0 {
		flushedFinals = append(flushedFinals, state.pendingRawFinals...)
		events = append(events, flushedFinals...)
		state.pendingRawFinals = nil
		speechmaticsRecordRawFinalTrimBeforeTime(state, events)
	}
	if state != nil && !state.includePartials {
		return events
	}
	partials := speechmaticsRawTranscriptEvents(resp, state, stt.SpeechEventInterimTranscript)
	partials = speechmaticsDropDuplicateTranscriptPartials(partials, flushedFinals)
	if state != nil && speechmaticsTranscriptEventSlicesSame(partials, state.latestRawPartialEvents) {
		return events
	}
	if state != nil && len(partials) > 0 {
		state.latestRawPartialEvents = partials
	}
	events = append(events, partials...)
	return events
}

func speechmaticsDropDuplicateTranscriptPartials(partials, finals []*stt.SpeechEvent) []*stt.SpeechEvent {
	if len(partials) == 0 || len(finals) == 0 {
		return partials
	}
	kept := partials[:0]
	for _, partial := range partials {
		if speechmaticsTranscriptDuplicatesAny(partial, finals) {
			continue
		}
		kept = append(kept, partial)
	}
	return kept
}

func speechmaticsTranscriptDuplicatesAny(event *stt.SpeechEvent, candidates []*stt.SpeechEvent) bool {
	if event == nil {
		return false
	}
	for _, candidate := range candidates {
		if speechmaticsTranscriptEventsSameTextTimingAndIdentity(event, candidate) {
			return true
		}
	}
	return false
}

func speechmaticsRawTranscriptEvents(resp smResponse, state *speechmaticsStreamState, eventType stt.SpeechEventType) []*stt.SpeechEvent {
	startTimeOffset := speechmaticsStartTimeOffset(state)
	var fragments []speechmaticsRawTranscriptFragment

	for i, result := range resp.Results {
		if len(result.Alternatives) == 0 {
			continue
		}
		alt := result.Alternatives[0]
		if alt.Content == "" {
			continue
		}
		resultSpeakerID := speechmaticsRawSpeakerID(alt.SpeakerID, speechmaticsRawSpeakerPresent(resp, i))
		if alt.SpeakerID != "" && speechmaticsSpeakerFiltered(resultSpeakerID, state) {
			continue
		}
		formatSpeakerID := resultSpeakerID
		if speechmaticsRawSpeakerNull(resp, i) {
			formatSpeakerID = "None"
		}
		speakerGroupID := resultSpeakerID
		if speechmaticsRawSpeakerNull(resp, i) {
			speakerGroupID = "\x00null"
		}
		language := speechmaticsRawFragmentLanguage(alt.Language, speechmaticsRawLanguagePresent(resp, i))
		startTime := result.StartTime + startTimeOffset
		endTime := result.EndTime + startTimeOffset
		if speechmaticsRawFragmentTrimmed(state, startTime) {
			continue
		}
		kind := result.Type
		if kind == "" {
			kind = "word"
		}
		fragments = append(fragments, speechmaticsRawTranscriptFragment{
			text:            alt.Content,
			kind:            kind,
			speakerID:       resultSpeakerID,
			speakerGroupID:  speakerGroupID,
			formatSpeakerID: formatSpeakerID,
			language:        language,
			attaches:        result.Attaches,
			isEOS:           result.IsEOS,
			startTime:       startTime,
			endTime:         endTime,
			confidence:      speechmaticsAlternativeConfidence(alt.Confidence),
			disfluency:      speechmaticsStringInSlice("disfluency", alt.Tags),
		})
	}

	if len(fragments) > 0 {
		speechmaticsRecordLatestRawTranscriptAnnotation(state, eventType, fragments)
		events := speechmaticsRawTranscriptEventsFromFragments(eventType, fragments, state)
		if len(events) > 0 && state != nil {
			state.turnHasTranscript = true
		}
		return events
	}
	if len(resp.Results) > 0 || resp.Metadata.Transcript == "" {
		return nil
	}
	if speechmaticsRawFragmentTrimmed(state, resp.Metadata.StartTime+startTimeOffset) {
		return nil
	}
	if state != nil {
		state.turnHasTranscript = true
	}
	return []*stt.SpeechEvent{
		{
			Type: eventType,
			Alternatives: []stt.SpeechData{
				{
					Text:       resp.Metadata.Transcript,
					Language:   speechmaticsSegmentLanguage("", false, state),
					Confidence: 1.0,
					StartTime:  resp.Metadata.StartTime + startTimeOffset,
					EndTime:    resp.Metadata.EndTime + startTimeOffset,
				},
			},
		},
	}
}

func speechmaticsRawFragmentTrimmed(state *speechmaticsStreamState, startTime float64) bool {
	return state != nil && state.rawTrimBeforeTimeSet && startTime < state.rawTrimBeforeTime
}

func speechmaticsRecordRawFinalTrimBeforeTime(state *speechmaticsStreamState, events []*stt.SpeechEvent) {
	if state == nil || len(events) == 0 {
		return
	}
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event == nil || event.Type != stt.SpeechEventFinalTranscript || len(event.Alternatives) == 0 {
			continue
		}
		state.rawTrimBeforeTimeSet = true
		state.rawTrimBeforeTime = event.Alternatives[len(event.Alternatives)-1].EndTime
		return
	}
}

func speechmaticsAlternativeConfidence(confidence *float64) float64 {
	if confidence == nil {
		return 1.0
	}
	return *confidence
}

func speechmaticsRecordLatestRawTranscriptAnnotation(state *speechmaticsStreamState, eventType stt.SpeechEventType, fragments []speechmaticsRawTranscriptFragment) {
	if state == nil || len(fragments) == 0 {
		return
	}
	var annotations []string
	if eventType == stt.SpeechEventFinalTranscript {
		annotations = append(annotations, speechmaticsAnnotationEndsWithFinal)
	}
	if fragments[len(fragments)-1].isEOS {
		annotations = append(annotations, speechmaticsAnnotationEndsWithEOS)
	}
	annotations = speechmaticsAppendRawDisfluencyAnnotation(annotations, fragments)
	annotations = speechmaticsAppendRawSpeechRateAnnotation(annotations, fragments)
	state.latestSegmentAnnotationSet = true
	state.latestSegmentAnnotation = annotations
}

func speechmaticsAppendRawDisfluencyAnnotation(annotations []string, fragments []speechmaticsRawTranscriptFragment) []string {
	if len(fragments) == 0 {
		return annotations
	}
	for _, fragment := range fragments {
		if fragment.disfluency {
			annotations = append(annotations, speechmaticsAnnotationHasDisfluency)
			break
		}
	}
	if fragments[0].disfluency {
		annotations = append(annotations, "starts_with_disfluency")
	}
	last := fragments[len(fragments)-1]
	if last.disfluency || (len(fragments) > 1 && (last.isEOS || last.kind == "punctuation") && fragments[len(fragments)-2].disfluency) {
		annotations = append(annotations, speechmaticsAnnotationEndsWithDisfluency)
	}
	return annotations
}

func speechmaticsAppendRawSpeechRateAnnotation(annotations []string, fragments []speechmaticsRawTranscriptFragment) []string {
	words := make([]speechmaticsRawTranscriptFragment, 0, len(fragments))
	for _, fragment := range fragments {
		if fragment.kind == "word" {
			words = append(words, fragment)
		}
	}
	if len(words) <= 1 {
		return annotations
	}
	recentWords := words
	if len(recentWords) > 10 {
		recentWords = recentWords[len(recentWords)-10:]
	}
	span := recentWords[len(recentWords)-1].endTime - recentWords[0].startTime
	if span <= 0 {
		return annotations
	}
	wpm := (float64(len(recentWords)) / span) * 60
	if wpm < 80 {
		return append(annotations, speechmaticsAnnotationVerySlowSpeaker)
	}
	if wpm < 110 {
		return append(annotations, speechmaticsAnnotationSlowSpeaker)
	}
	return annotations
}

func speechmaticsRawTranscriptEventsFromFragments(eventType stt.SpeechEventType, fragments []speechmaticsRawTranscriptFragment, state *speechmaticsStreamState) []*stt.SpeechEvent {
	var events []*stt.SpeechEvent
	groupStart := 0
	for i := 1; i <= len(fragments); i++ {
		if i < len(fragments) && fragments[i].speakerGroupID == fragments[groupStart].speakerGroupID && !speechmaticsSplitRawTranscriptAtEOS(eventType, fragments[i-1], state) {
			continue
		}
		if event := speechmaticsRawTranscriptEventFromGroup(eventType, fragments[groupStart:i], state); event != nil {
			events = append(events, event)
		}
		groupStart = i
	}
	return events
}

func speechmaticsSplitRawTranscriptAtEOS(eventType stt.SpeechEventType, fragment speechmaticsRawTranscriptFragment, state *speechmaticsStreamState) bool {
	return (state == nil || state.splitRawFinalEOSSentences) && eventType == stt.SpeechEventFinalTranscript && fragment.isEOS
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
	active := true
	if rawActive := speechmaticsRawTranscriptSpeakerActive(speakerID, state); rawActive != nil {
		active = *rawActive
	}
	formatSpeakerID := speakerID
	if fragments[0].formatSpeakerID != "" || speakerID == "" {
		formatSpeakerID = fragments[0].formatSpeakerID
	}
	text = speechmaticsFormattedSegmentText(text, formatSpeakerID, active, state)
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
	for i, segment := range resp.Segments {
		speakerPresent := speechmaticsSegmentSpeakerIDPresent(resp, i)
		speakerID := speechmaticsSegmentSpeakerID(segment.SpeakerID, speakerPresent)
		if speechmaticsSystemSpeakerID(speakerID) ||
			(speakerPresent && segment.SpeakerID != "" && speechmaticsConfiguredSpeakerFiltered(speakerID, state)) {
			continue
		}
		active := speechmaticsSegmentIsActive(segment.IsActive, speechmaticsSegmentIsActivePresent(resp, i))
		speechmaticsRecordLatestSegmentAnnotation(state, segment.Annotation, active)
		formatSpeakerID := speakerID
		if speechmaticsSegmentSpeakerIDNull(resp, i) {
			formatSpeakerID = "None"
		}
		text := speechmaticsFormattedSegmentText(segment.Text, formatSpeakerID, active, state)
		events = append(events, &stt.SpeechEvent{
			Type: eventType,
			Alternatives: []stt.SpeechData{
				{
					Text:      text,
					Language:  speechmaticsSegmentLanguage(segment.Language, speechmaticsSegmentLanguagePresent(resp, i), state),
					SpeakerID: speakerID,
					StartTime: segment.Metadata.StartTime + startTimeOffset,
					EndTime:   segment.Metadata.EndTime + startTimeOffset,
				},
			},
		})
	}
	if eventType == stt.SpeechEventFinalTranscript {
		speechmaticsDropOverlappedPendingRawFinals(state, events)
		speechmaticsRecordRawFinalTrimBeforeTime(state, events)
	}
	if len(events) > 0 && state != nil {
		state.turnHasTranscript = true
	}
	return events
}

func speechmaticsDropOverlappedPendingRawFinals(state *speechmaticsStreamState, segmentEvents []*stt.SpeechEvent) {
	if state == nil || len(state.pendingRawFinals) == 0 || len(segmentEvents) == 0 {
		return
	}
	kept := state.pendingRawFinals[:0]
	for _, rawEvent := range state.pendingRawFinals {
		if speechmaticsTranscriptOverlapsAny(rawEvent, segmentEvents) {
			continue
		}
		kept = append(kept, rawEvent)
	}
	state.pendingRawFinals = kept
}

func speechmaticsTranscriptOverlapsAny(event *stt.SpeechEvent, candidates []*stt.SpeechEvent) bool {
	if event == nil || event.Type != stt.SpeechEventFinalTranscript {
		return false
	}
	for _, candidate := range candidates {
		if speechmaticsTranscriptEventsOverlap(event, candidate) {
			return true
		}
	}
	return false
}

func speechmaticsTranscriptEventsOverlap(left, right *stt.SpeechEvent) bool {
	if left == nil || right == nil || left.Type != stt.SpeechEventFinalTranscript || right.Type != stt.SpeechEventFinalTranscript {
		return false
	}
	for _, leftAlt := range left.Alternatives {
		for _, rightAlt := range right.Alternatives {
			if leftAlt.StartTime < rightAlt.EndTime && rightAlt.StartTime < leftAlt.EndTime {
				return true
			}
			if speechmaticsTranscriptAlternativesSameTimingAndIdentity(leftAlt, rightAlt) {
				return true
			}
		}
	}
	return false
}

func speechmaticsTranscriptEventSlicesSame(left, right []*stt.SpeechEvent) bool {
	if len(left) == 0 || len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] == nil || right[i] == nil || left[i].Type != right[i].Type ||
			!speechmaticsTranscriptEventsSameTextTimingAndIdentity(left[i], right[i]) {
			return false
		}
	}
	return true
}

func speechmaticsTranscriptEventsSameTextTimingAndIdentity(left, right *stt.SpeechEvent) bool {
	if left == nil || right == nil {
		return false
	}
	for _, leftAlt := range left.Alternatives {
		for _, rightAlt := range right.Alternatives {
			if speechmaticsTranscriptAlternativesSameTimingAndIdentity(leftAlt, rightAlt) {
				return true
			}
		}
	}
	return false
}

func speechmaticsTranscriptAlternativesSameTimingAndIdentity(left, right stt.SpeechData) bool {
	return left.StartTime == right.StartTime &&
		left.EndTime == right.EndTime &&
		left.Text == right.Text &&
		left.SpeakerID == right.SpeakerID &&
		left.Language == right.Language
}

func speechmaticsRecordLatestSegmentAnnotation(state *speechmaticsStreamState, annotations []string, active bool) {
	if state == nil || !active {
		return
	}
	state.latestSegmentAnnotationSet = true
	state.latestSegmentAnnotation = cloneSpeechmaticsStringSlice(annotations)
}

func speechmaticsSpeakerFiltered(speakerID string, state *speechmaticsStreamState) bool {
	if speechmaticsSystemSpeakerID(speakerID) {
		return true
	}
	return speechmaticsConfiguredSpeakerFiltered(speakerID, state)
}

func speechmaticsConfiguredSpeakerFiltered(speakerID string, state *speechmaticsStreamState) bool {
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
	for _, r := range speakerID[2 : len(speakerID)-2] {
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

func speechmaticsSegmentLanguagePresent(resp smResponse, index int) bool {
	return index >= 0 && index < len(resp.segmentLanguagePresent) && resp.segmentLanguagePresent[index]
}

func speechmaticsSegmentLanguage(language string, present bool, state *speechmaticsStreamState) string {
	if present || language != "" {
		return language
	}
	if state != nil {
		return state.language
	}
	return "en"
}

func speechmaticsRawLanguagePresent(resp smResponse, index int) bool {
	return index >= 0 && index < len(resp.rawLanguagePresent) && resp.rawLanguagePresent[index]
}

func speechmaticsRawFragmentLanguage(language string, present bool) string {
	if present || language != "" {
		return language
	}
	return "en"
}

func speechmaticsRawSpeakerPresent(resp smResponse, index int) bool {
	return index >= 0 && index < len(resp.rawSpeakerPresent) && resp.rawSpeakerPresent[index]
}

func speechmaticsRawSpeakerNull(resp smResponse, index int) bool {
	return index >= 0 && index < len(resp.rawSpeakerNull) && resp.rawSpeakerNull[index]
}

func speechmaticsRawSpeakerID(speakerID string, present bool) string {
	if present || speakerID != "" {
		return speakerID
	}
	return "UU"
}

func speechmaticsSegmentSpeakerIDPresent(resp smResponse, index int) bool {
	return index >= 0 && index < len(resp.segmentSpeakerIDPresent) && resp.segmentSpeakerIDPresent[index]
}

func speechmaticsSegmentSpeakerIDNull(resp smResponse, index int) bool {
	return index >= 0 && index < len(resp.segmentSpeakerIDNull) && resp.segmentSpeakerIDNull[index]
}

func speechmaticsSegmentIsActivePresent(resp smResponse, index int) bool {
	return index >= 0 && index < len(resp.segmentIsActivePresent) && resp.segmentIsActivePresent[index]
}

func speechmaticsSegmentIsActive(isActive *bool, present bool) bool {
	if isActive != nil {
		return *isActive
	}
	return !present
}

func speechmaticsSegmentSpeakerID(speakerID string, present bool) string {
	if present || speakerID != "" {
		return speakerID
	}
	return "UU"
}

func speechmaticsFormattedSegmentText(text, speakerID string, active bool, state *speechmaticsStreamState) string {
	format := ""
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
	if len(frame.Data) == 0 {
		return nil
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
		pendingLocalEndpointingEOU := s.pendingLocalEndpointingEOU
		pendingEndInput := s.pendingEndInput
		vadStream := s.vadStream
		s.pendingVADFrames = nil
		s.pendingVADEndInput = false
		s.pendingAudioChunks = nil
		s.pendingFinalize = false
		s.pendingLocalEndpointingEOU = false
		s.pendingEndInput = false
		if len(pendingVADFrames) == 0 && !pendingVADEndInput && len(pendingAudio) == 0 && !pendingFinalize && !pendingLocalEndpointingEOU && !pendingEndInput {
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
		if pendingLocalEndpointingEOU {
			if err := s.sendForceEndOfUtteranceWithProviderManaged(true); err != nil {
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
	return s.sendForceEndOfUtteranceWithProviderManaged(false)
}

func (s *speechmaticsSTTStream) sendLocalEndpointingForceEndOfUtterance() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	if s.startupGateActiveLocked() {
		s.pendingLocalEndpointingEOU = true
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	return s.sendForceEndOfUtteranceWithProviderManaged(true)
}

func (s *speechmaticsSTTStream) sendForceEndOfUtteranceWithProviderManaged(allowProviderManaged bool) error {
	seq, timestamp, ok := s.beginForcedEOU(allowProviderManaged)
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

func (s *speechmaticsSTTStream) beginForcedEOU(allowProviderManaged bool) (uint64, float64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || (s.providerManagedEndpointing && !allowProviderManaged) {
		return 0, 0, false
	}
	if s.forcedEOUPending || s.forcedEOUCompleted {
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
	if !speechmaticsHasReferenceTurnEndEvidence(s.state) {
		return nil
	}
	events := speechmaticsEndOfTurnEvents(s.state)
	speechmaticsClearTurnTranscriptEvidence(s.state)
	return events
}

func (s *speechmaticsSTTStream) fixedEOUEndEvents() []*stt.SpeechEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fixedEOUCompleted || s.localEndpointingTurnClosed {
		return nil
	}
	if !s.closed {
		s.fixedEOUCompleted = true
		s.localEndpointingTurnClosed = true
	}
	if !speechmaticsHasReferenceTurnEndEvidence(s.state) {
		return nil
	}
	events := speechmaticsEndOfTurnEvents(s.state)
	speechmaticsClearTurnTranscriptEvidence(s.state)
	return events
}

func speechmaticsHasReferenceTurnEndEvidence(state *speechmaticsStreamState) bool {
	return state != nil && (state.turnHasTranscript || len(state.pendingRawFinals) > 0)
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
	s.completedEOUNewTurnStarted = false
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
	s.completedEOUNewTurnStarted = false
	return true
}

func (s *speechmaticsSTTStream) markCompletedEOUNewTurnStarted() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.completedEOUNewTurnStarted = s.forcedEOUCompleted || s.fixedEOUCompleted
	s.mu.Unlock()
}

func (s *speechmaticsSTTStream) resetCompletedEOUAfterNewTurnContent() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if !s.completedEOUNewTurnStarted {
		s.mu.Unlock()
		return
	}
	s.forcedEOUCompleted = false
	s.fixedEOUCompleted = false
	s.completedEOUNewTurnStarted = false
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
		if event == nil {
			continue
		}
		switch event.Type {
		case corevad.VADEventStartOfSpeech:
			s.handleVADStartOfSpeech()
		case corevad.VADEventEndOfSpeech:
			s.scheduleLocalEndpointingForceEndOfUtterance()
		}
	}
}

func (s *speechmaticsSTTStream) handleVADStartOfSpeech() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.localEndpointingTurnClosed = false
	s.forcedEOUCompleted = false
	s.fixedEOUCompleted = false
	s.completedEOUNewTurnStarted = false
	s.pendingLocalEndpointingEOU = false
	s.localEndpointingEOUSeq++
	speechmaticsClearLatestEndpointingAnnotation(s.state)
	s.mu.Unlock()
}

func (s *speechmaticsSTTStream) scheduleLocalEndpointingForceEndOfUtterance() {
	if s == nil {
		return
	}
	delay := time.Duration(0)
	if s.providerManagedEndpointing {
		delay = s.localEndpointingForceEOUDelay()
	}
	s.mu.Lock()
	if s.closed || s.localEndpointingTurnClosed {
		s.mu.Unlock()
		return
	}
	s.localEndpointingEOUSeq++
	seq := s.localEndpointingEOUSeq
	s.mu.Unlock()
	if delay <= 0 {
		s.sendScheduledLocalEndpointingForceEndOfUtterance(seq)
		return
	}
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-s.done:
			return
		}
		s.sendScheduledLocalEndpointingForceEndOfUtterance(seq)
	}()
}

func (s *speechmaticsSTTStream) localEndpointingForceEOUDelay() time.Duration {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	hasTranscript := s.state != nil && s.state.turnHasTranscript
	s.mu.Unlock()
	if !hasTranscript {
		return speechmaticsMinEndOfTurnDelay
	}
	return s.localEndpointingDelay()
}

func (s *speechmaticsSTTStream) localEndpointingDelay() time.Duration {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	annotationsSet := s.state != nil && s.state.latestSegmentAnnotationSet
	hasTranscript := s.state != nil && s.state.turnHasTranscript
	var annotations []string
	if annotationsSet {
		annotations = cloneSpeechmaticsStringSlice(s.state.latestSegmentAnnotation)
	}
	s.mu.Unlock()
	if annotationsSet {
		return speechmaticsLocalEndpointingDelayWithAnnotations(s.owner, annotations)
	}
	if hasTranscript {
		return speechmaticsLocalEndpointingDelayWithAnnotations(s.owner, []string{speechmaticsAnnotationVADStopped})
	}
	return speechmaticsLocalEndpointingDelay(s.owner)
}

func (s *speechmaticsSTTStream) sendScheduledLocalEndpointingForceEndOfUtterance(seq uint64) {
	if !s.consumeLocalEndpointingForceEndOfUtterance(seq) {
		return
	}
	if err := s.sendLocalEndpointingForceEndOfUtterance(); err != nil {
		s.enqueueError(err)
		_ = s.Close()
	}
}

func (s *speechmaticsSTTStream) consumeLocalEndpointingForceEndOfUtterance(seq uint64) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.localEndpointingEOUSeq != seq {
		return false
	}
	s.localEndpointingEOUSeq++
	return true
}

func (s *speechmaticsSTTStream) closeLocalEndpointingTurn() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.localEndpointingTurnClosed = true
	s.pendingLocalEndpointingEOU = false
	s.localEndpointingEOUSeq++
	s.mu.Unlock()
}

func (s *speechmaticsSTTStream) reopenLocalEndpointingTurn() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.localEndpointingTurnClosed = false
	s.pendingLocalEndpointingEOU = false
	s.localEndpointingEOUSeq++
	speechmaticsClearLatestEndpointingAnnotation(s.state)
	s.mu.Unlock()
}

func speechmaticsClearLatestEndpointingAnnotation(state *speechmaticsStreamState) {
	if state == nil {
		return
	}
	state.latestSegmentAnnotationSet = false
	state.latestSegmentAnnotation = nil
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
	return audio.NewAudioByteStream(sampleRate, 1, 0)
}

type speechmaticsSTTInputAudioNormalizer struct {
	sampleRate    uint32
	numChannels   uint32
	targetRate    uint32
	inputSamples  uint64
	outputSamples uint64
	historyStart  uint64
	history       []byte
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
		n.inputSamples = 0
		n.outputSamples = 0
		n.historyStart = 0
		n.history = nil
	}
	if samplesPerChannel == 0 {
		return &model.AudioFrame{
			SampleRate:        targetRate,
			NumChannels:       frame.NumChannels,
			SamplesPerChannel: 0,
			ParticipantID:     frame.ParticipantID,
		}, nil
	}

	baseInputSamples := n.inputSamples
	totalInputSamples := baseInputSamples + uint64(samplesPerChannel)
	if len(n.history) == 0 {
		n.historyStart = baseInputSamples
	}
	n.history = append(n.history, frame.Data[:expectedBytes]...)
	totalOutputSamples := totalInputSamples * uint64(targetRate) / uint64(frame.SampleRate)
	channelCount := int(frame.NumChannels)
	sampleBytes := channelCount * 2
	historySamples := uint64(len(n.history) / sampleBytes)
	outSamples := uint32(totalOutputSamples - n.outputSamples)
	out := make([]byte, int(outSamples)*sampleBytes)
	for outIdx := uint32(0); outIdx < outSamples; outIdx++ {
		globalOutputIndex := n.outputSamples + uint64(outIdx)
		globalInputIndex := globalOutputIndex * uint64(frame.SampleRate) / uint64(targetRate)
		if globalInputIndex < n.historyStart {
			globalInputIndex = n.historyStart
		}
		srcIdx := globalInputIndex - n.historyStart
		if srcIdx >= historySamples {
			srcIdx = historySamples - 1
		}
		for ch := 0; ch < channelCount; ch++ {
			inOffset := (int(srcIdx)*channelCount + ch) * 2
			outOffset := (int(outIdx)*channelCount + ch) * 2
			copy(out[outOffset:outOffset+2], n.history[inOffset:inOffset+2])
		}
	}
	n.inputSamples = totalInputSamples
	n.outputSamples = totalOutputSamples
	n.trimHistory()

	return &model.AudioFrame{
		Data:              out,
		SampleRate:        targetRate,
		NumChannels:       frame.NumChannels,
		SamplesPerChannel: outSamples,
		ParticipantID:     frame.ParticipantID,
	}, nil
}

func (n *speechmaticsSTTInputAudioNormalizer) flush() *model.AudioFrame {
	if n == nil || len(n.history) == 0 || n.targetRate == 0 || n.numChannels == 0 {
		return nil
	}
	if n.outputSamples*uint64(n.sampleRate) >= n.inputSamples*uint64(n.targetRate) {
		n.inputSamples = 0
		n.outputSamples = 0
		n.historyStart = 0
		n.history = nil
		return nil
	}
	channelCount := int(n.numChannels)
	sampleBytes := channelCount * 2
	historySamples := uint64(len(n.history) / sampleBytes)
	globalInputIndex := n.outputSamples * uint64(n.sampleRate) / uint64(n.targetRate)
	if globalInputIndex >= n.inputSamples {
		globalInputIndex = n.inputSamples - 1
	}
	if globalInputIndex < n.historyStart {
		globalInputIndex = n.historyStart
	}
	srcIdx := globalInputIndex - n.historyStart
	if srcIdx >= historySamples {
		srcIdx = historySamples - 1
	}
	offset := int(srcIdx) * sampleBytes
	data := append([]byte(nil), n.history[offset:offset+sampleBytes]...)
	frame := &model.AudioFrame{
		Data:              data,
		SampleRate:        n.targetRate,
		NumChannels:       n.numChannels,
		SamplesPerChannel: 1,
	}
	n.inputSamples = 0
	n.outputSamples = 0
	n.historyStart = 0
	n.history = nil
	return frame
}

func (n *speechmaticsSTTInputAudioNormalizer) reset() {
	*n = speechmaticsSTTInputAudioNormalizer{}
}

func (n *speechmaticsSTTInputAudioNormalizer) trimHistory() {
	if n == nil || len(n.history) == 0 || n.sampleRate == 0 || n.targetRate == 0 || n.numChannels == 0 {
		return
	}
	nextInputIndex := n.outputSamples * uint64(n.sampleRate) / uint64(n.targetRate)
	if nextInputIndex <= n.historyStart {
		return
	}
	channelCount := int(n.numChannels)
	sampleBytes := channelCount * 2
	historySamples := uint64(len(n.history) / sampleBytes)
	dropSamples := nextInputIndex - n.historyStart
	if dropSamples >= historySamples {
		n.history = nil
		n.historyStart = nextInputIndex
		return
	}
	dropBytes := int(dropSamples) * sampleBytes
	copy(n.history, n.history[dropBytes:])
	n.history = n.history[:len(n.history)-dropBytes]
	n.historyStart = nextInputIndex
}

func (s *speechmaticsSTTStream) Close() error {
	_ = s.closeTransportOnce()
	s.prepareDrainPendingRawFinals()
	s.closeDone()
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
	s.localEndpointingEOUSeq++
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

func (s *speechmaticsSTTStream) prepareDrainPendingRawFinals() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	events := speechmaticsFlushPendingRawFinals(s.state)
	if len(events) == 0 {
		return
	}
	s.drainEvents = append(s.drainEvents, events...)
	s.drainEventsAfterClose = true
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
		if event, ok := s.nextDrainedEvent(); ok {
			return event, nil
		}
		if s.pendingErr != nil {
			err := s.pendingErr
			s.pendingErr = nil
			return nil, err
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
		if event, ok := s.nextOverflowEvent(); ok {
			return event, nil
		}
		if event, ok := s.nextDrainedEvent(); ok {
			return event, nil
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
	default:
	}
	if event, ok := s.nextOverflowEvent(); ok {
		return event, nil
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
		if event, ok := s.nextOverflowEvent(); ok {
			return event, nil
		}
		if event, ok := s.nextDrainedEvent(); ok {
			return event, nil
		}
		s.pendingErr = nil
		s.markClosed()
		return nil, err
	case <-s.done:
		if event, ok := s.nextDrainedEvent(); ok {
			return event, nil
		}
		return nil, io.EOF
	}
}

func (s *speechmaticsSTTStream) nextDrainedEvent() (*stt.SpeechEvent, bool) {
	if s == nil || !s.shouldDrainEventsAfterClose() {
		return nil, false
	}
	select {
	case event, ok := <-s.events:
		if ok {
			return event, true
		}
	default:
	}
	if event, ok := s.nextOverflowEvent(); ok {
		return event, true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.drainEvents) == 0 {
		return nil, false
	}
	event := s.drainEvents[0]
	copy(s.drainEvents, s.drainEvents[1:])
	s.drainEvents = s.drainEvents[:len(s.drainEvents)-1]
	return event, true
}

func (s *speechmaticsSTTStream) nextOverflowEvent() (*stt.SpeechEvent, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.overflowEvents) == 0 {
		return nil, false
	}
	event := s.overflowEvents[0]
	copy(s.overflowEvents, s.overflowEvents[1:])
	s.overflowEvents = s.overflowEvents[:len(s.overflowEvents)-1]
	return event, true
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
