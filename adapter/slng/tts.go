package slng

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/library/tokenize"
	"github.com/gorilla/websocket"
)

var slngElevenLabsTTSModelOptionKeys = []string{
	"inactivity_timeout",
	"apply_text_normalization",
	"auto_mode",
	"enable_logging",
	"enable_ssml_parsing",
	"sync_alignment",
	"language_code",
	"stability",
	"similarity_boost",
	"style",
	"speed",
	"use_speaker_boost",
	"chunk_length_schedule",
	"preferred_alignment",
}

var slngTTSNow = time.Now
var slngTTSDialContext = func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
	return websocket.DefaultDialer.DialContext(ctx, endpoint, headers)
}

const defaultSLNGTTSFallbackRecoveryCooldown = time.Minute

type TTSChunkingMode string

const (
	TTSChunkingAuto   TTSChunkingMode = "auto"
	TTSChunkingWord   TTSChunkingMode = "word"
	TTSChunkingPhrase TTSChunkingMode = "phrase"
)

type TTS struct {
	mu                sync.Mutex
	optionError       error
	apiKey            string
	providerAPIKey    string
	model             string
	endpoint          string
	connections       []TTSConnectionConfig
	candidateState    *candidateState
	fallbackCooldown  time.Duration
	regionOverride    string
	worldPartOverride string
	externalAgentID   string
	externalSessionID string
	extraHeaders      http.Header
	voice             string
	language          string
	sampleRate        int
	speed             float64
	encoding          string
	modelOptions      map[string]any
	runtimeInit       map[string]any
	textChunking      TTSChunkingMode
	phraseMaxChars    int
	firstAudioTimeout time.Duration
	warmStandby       bool
	standby           *ttsStandbyConnection
	standbyEpoch      uint64
	standbyCancel     context.CancelFunc
	standbyDone       chan struct{}
	standbyTaskID     uint64
	streams           map[*ttsStream]struct{}
	closed            bool
}

type ttsStandbyConnection struct {
	conn           *websocket.Conn
	candidate      ttsConnectionCandidate
	candidateIndex int
	attempt        *TTS
	epoch          uint64
}

type ttsStandbySettings struct {
	apiKey            string
	providerAPIKey    string
	model             string
	endpoint          string
	connections       []TTSConnectionConfig
	regionOverride    string
	worldPartOverride string
	externalAgentID   string
	externalSessionID string
	extraHeaders      http.Header
	voice             string
	language          string
	sampleRate        int
	speed             float64
	encoding          string
	modelOptions      map[string]any
	runtimeInit       map[string]any
	textChunking      TTSChunkingMode
	phraseMaxChars    int
	firstAudioTimeout time.Duration
	warmStandby       bool
}

type TTSOption func(*TTS)

func WithTTSBaseURL(baseURL string) TTSOption {
	return func(t *TTS) {
		if baseURL != "" {
			t.endpoint = defaultTTSEndpoint(strings.TrimRight(baseURL, "/"), t.model)
			t.connections = nil
		}
	}
}

func WithTTSModel(modelName string) TTSOption {
	return func(t *TTS) {
		if modelName != "" {
			t.model = modelName
			t.endpoint = defaultTTSEndpoint(defaultSLNGBaseURL, modelName)
			t.voice = normalizeTTSVoice(modelName, t.voice)
			t.language = normalizeLanguageForModel(modelName, t.language, t.modelOptions)
			t.connections = nil
		}
	}
}

func WithTTSEndpoint(endpoint string) TTSOption {
	return func(t *TTS) {
		if endpoint != "" {
			t.endpoint = endpoint
			t.connections = nil
			if model, err := bridgeModel(endpoint, "tts"); err == nil {
				t.model = model
			}
		}
	}
}

func WithTTSConnections(connections ...TTSConnectionConfig) TTSOption {
	return func(t *TTS) {
		if len(connections) == 0 {
			return
		}
		t.connections = cloneTTSConnectionConfigs(connections)
		t.endpoint = connections[0].Endpoint
		if model, err := bridgeModel(connections[0].Endpoint, "tts"); err == nil {
			t.model = model
		}
	}
}

func WithTTSFallbackRecoveryCooldown(cooldown time.Duration) TTSOption {
	return func(t *TTS) {
		t.fallbackCooldown = max(cooldown, 0)
	}
}

func WithTTSRegionOverride(region any) TTSOption {
	return func(t *TTS) {
		t.regionOverride = normalizeRegionOverride(region)
	}
}

func WithTTSProviderAPIKey(apiKey string) TTSOption {
	return func(t *TTS) {
		t.providerAPIKey = apiKey
	}
}

func WithTTSWorldPartOverride(worldPart string) TTSOption {
	return func(t *TTS) {
		t.worldPartOverride = worldPart
	}
}

func WithTTSExternalTracking(agentID, sessionID string) TTSOption {
	return func(t *TTS) {
		t.externalAgentID = agentID
		t.externalSessionID = sessionID
	}
}

func WithTTSExtraHeaders(headers http.Header) TTSOption {
	return func(t *TTS) {
		t.extraHeaders = headers.Clone()
	}
}

func WithTTSVoice(voice string) TTSOption {
	return func(t *TTS) {
		t.voice = normalizeTTSVoice(t.model, voice)
	}
}

func WithTTSLanguage(language string) TTSOption {
	return func(t *TTS) {
		t.language = normalizeLanguageForModel(t.model, language, t.modelOptions)
	}
}

func WithTTSSampleRate(sampleRate int) TTSOption {
	return func(t *TTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithTTSSpeed(speed float64) TTSOption {
	return func(t *TTS) {
		t.speed = speed
	}
}

func WithTTSModelOptions(options map[string]any) TTSOption {
	return func(t *TTS) {
		t.modelOptions = cloneSLNGMap(options)
		t.language = normalizeLanguageForModel(t.model, t.language, t.modelOptions)
	}
}

func WithTTSRuntimeInit(init map[string]any) TTSOption {
	return func(t *TTS) {
		t.runtimeInit = cloneSLNGMap(init)
	}
}

func WithTTSTextChunking(mode TTSChunkingMode, phraseMaxChars int) TTSOption {
	return func(t *TTS) {
		if mode != TTSChunkingAuto && mode != TTSChunkingWord && mode != TTSChunkingPhrase {
			t.optionError = errors.New("text chunking must be auto, word, or phrase")
			return
		}
		if phraseMaxChars <= 0 {
			t.optionError = errors.New("phrase max chars must be positive")
			return
		}
		t.textChunking = mode
		t.phraseMaxChars = phraseMaxChars
	}
}

func WithTTSFirstAudioTimeout(timeout time.Duration) TTSOption {
	return func(t *TTS) {
		t.firstAudioTimeout = max(timeout, 0)
	}
}

func WithTTSWarmStandby(enabled bool) TTSOption {
	return func(t *TTS) {
		t.warmStandby = enabled
	}
}

func NewTTS(apiKey string, opts ...TTSOption) *TTS {
	if apiKey == "" {
		apiKey = os.Getenv(slngAPIKeyEnv)
	}
	provider := &TTS{
		apiKey:           apiKey,
		model:            defaultSLNGTTSModel,
		endpoint:         defaultTTSEndpoint(defaultSLNGBaseURL, defaultSLNGTTSModel),
		voice:            normalizeTTSVoice(defaultSLNGTTSModel, defaultSLNGTTSVoice),
		language:         defaultSLNGLanguage,
		sampleRate:       defaultSLNGTTSSampleRate,
		speed:            defaultSLNGSpeed,
		encoding:         defaultSLNGTTSEncoding,
		fallbackCooldown: defaultSLNGTTSFallbackRecoveryCooldown,
		textChunking:     TTSChunkingWord,
		phraseMaxChars:   60,
	}
	for _, opt := range opts {
		opt(provider)
	}
	provider.candidateState = newCandidateState(provider.ttsCandidateCount(), provider.fallbackCooldown)
	return provider
}

func slngPhraseChunks(text string, maxChars int) []string {
	chunks, _ := slngPhraseChunkParts(text, maxChars, true)
	return chunks
}

func slngPhraseChunkParts(text string, maxChars int, flush bool) ([]string, string) {
	tokens := tokenize.SplitWords(text, false, false, false)
	var chunks []string
	var buffer []string
	hasLetter := false
	consumed := 0
	for _, token := range tokens {
		buffer = append(buffer, token.Token)
		hasLetter = hasLetter || strings.ContainsFunc(token.Token, unicode.IsLetter)
		phrase := strings.Join(buffer, " ")
		runes := []rune(strings.TrimSpace(phrase))
		atBoundary := len(runes) > 0 && strings.ContainsRune(".!?,;:", runes[len(runes)-1])
		if hasLetter && (atBoundary || len(runes) >= maxChars) {
			chunks = append(chunks, phrase)
			consumed = token.End
			buffer = nil
			hasLetter = false
		}
	}
	if flush && hasLetter {
		chunks = append(chunks, strings.Join(buffer, " "))
		return chunks, ""
	}
	if flush {
		return chunks, ""
	}
	runes := []rune(text)
	if consumed >= len(runes) {
		return chunks, ""
	}
	return chunks, strings.TrimLeftFunc(string(runes[consumed:]), unicode.IsSpace)
}

func (t *TTS) Label() string { return "slng.TTS" }
func (t *TTS) Model() string { return "slng" }
func (t *TTS) Provider() string {
	return "SLNG"
}
func (t *TTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *TTS) SampleRate() int  { return t.sampleRate }
func (t *TTS) NumChannels() int { return slngNumChannels }

func (t *TTS) UpdateOptions(opts ...TTSOption) {
	t.mu.Lock()
	before := t.standbySettingsLocked()
	for _, opt := range opts {
		if opt != nil {
			opt(t)
		}
	}
	invalidateStandby := !reflect.DeepEqual(before, t.standbySettingsLocked())
	if t.candidateState == nil ||
		t.candidateState.count != t.ttsCandidateCount() ||
		t.candidateState.cooldown != t.fallbackCooldown {
		t.candidateState = newCandidateState(t.ttsCandidateCount(), t.fallbackCooldown)
	}
	var standby *ttsStandbyConnection
	var cancel context.CancelFunc
	var standbyDone chan struct{}
	if invalidateStandby {
		t.standbyEpoch++
		standby = t.standby
		t.standby = nil
		cancel = t.standbyCancel
		t.standbyCancel = nil
		standbyDone = t.standbyDone
		t.standbyTaskID++
	}
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if standbyDone != nil {
		<-standbyDone
	}
	if standby != nil {
		_ = standby.conn.Close()
	}
}

func (t *TTS) standbySettingsLocked() ttsStandbySettings {
	return ttsStandbySettings{
		apiKey:            t.apiKey,
		providerAPIKey:    t.providerAPIKey,
		model:             t.model,
		endpoint:          t.endpoint,
		connections:       cloneTTSConnectionConfigs(t.connections),
		regionOverride:    t.regionOverride,
		worldPartOverride: t.worldPartOverride,
		externalAgentID:   t.externalAgentID,
		externalSessionID: t.externalSessionID,
		extraHeaders:      t.extraHeaders.Clone(),
		voice:             t.voice,
		language:          t.language,
		sampleRate:        t.sampleRate,
		speed:             t.speed,
		encoding:          t.encoding,
		modelOptions:      cloneSLNGMap(t.modelOptions),
		runtimeInit:       cloneSLNGMap(t.runtimeInit),
		textChunking:      t.textChunking,
		phraseMaxChars:    t.phraseMaxChars,
		firstAudioTimeout: t.firstAudioTimeout,
		warmStandby:       t.warmStandby,
	}
}

func (t *TTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	stream, err := t.stream(ctx, false)
	if err != nil {
		return nil, err
	}
	if text != "" {
		if err := stream.PushText(text); err != nil {
			stream.Close()
			return nil, err
		}
	}
	if err := stream.Flush(); err != nil {
		stream.Close()
		return nil, err
	}
	return &ttsChunkedStream{stream: stream}, nil
}

func (t *TTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return t.stream(ctx, true)
}

func (t *TTS) Prewarm() {
	t.startTTSStandby()
}

func (t *TTS) startTTSStandby() {
	t.mu.Lock()
	if !t.warmStandby || t.closed || t.standby != nil || t.standbyCancel != nil {
		t.mu.Unlock()
		return
	}
	candidateIndex := -1
	if t.candidateState != nil {
		candidateIndex = t.candidateState.start(slngTTSNow())
	}
	if candidateIndex < 0 {
		t.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.standbyCancel = cancel
	t.standbyDone = done
	t.standbyTaskID++
	taskID := t.standbyTaskID
	epoch := t.standbyEpoch
	t.mu.Unlock()

	go func() {
		defer close(done)
		conn, candidate, attempt, err := t.dialTTSStandby(ctx, candidateIndex)
		t.mu.Lock()
		if taskID == t.standbyTaskID {
			t.standbyCancel = nil
		}
		activeIndex := -1
		if t.candidateState != nil {
			activeIndex = t.candidateState.start(slngTTSNow())
		}
		store := err == nil &&
			taskID == t.standbyTaskID &&
			epoch == t.standbyEpoch &&
			candidateIndex == activeIndex &&
			t.warmStandby &&
			!t.closed &&
			t.standby == nil
		if store {
			t.standby = &ttsStandbyConnection{
				conn:           conn,
				candidate:      candidate,
				candidateIndex: candidateIndex,
				attempt:        attempt,
				epoch:          epoch,
			}
		}
		t.mu.Unlock()
		if err == nil && !store {
			_ = conn.Close()
		}
	}()
}

func (t *TTS) stream(ctx context.Context, appendTextSpace bool) (tts.SynthesizeStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if t.optionError != nil {
		return nil, t.optionError
	}
	if err := t.requireAPIKey(); err != nil {
		return nil, err
	}
	conn, candidate, candidateIndex, attempt, err := t.acquireTTSConnection(ctx)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &ttsStream{
		provider:          t,
		ctx:               streamCtx,
		cancel:            cancel,
		conn:              conn,
		candidateIndex:    candidateIndex,
		sampleRate:        attempt.sampleRate,
		model:             candidate.model,
		appendTextSpace:   appendTextSpace,
		firstAudioTimeout: attempt.firstAudioTimeout,
		textChunking:      attempt.textChunking,
		phraseMaxChars:    attempt.phraseMaxChars,
	}
	if !t.registerStream(stream) {
		return nil, io.ErrClosedPipe
	}
	return stream, nil
}

func (t *TTS) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	streams := make([]*ttsStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	standby := t.standby
	t.standby = nil
	cancelStandby := t.standbyCancel
	t.standbyCancel = nil
	standbyDone := t.standbyDone
	t.standbyTaskID++
	t.mu.Unlock()

	var firstErr error
	if cancelStandby != nil {
		cancelStandby()
	}
	if standbyDone != nil {
		<-standbyDone
	}
	if standby != nil {
		firstErr = standby.conn.Close()
	}
	for _, stream := range streams {
		if err := stream.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (t *TTS) isClosed() bool {
	if t == nil {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

func (t *TTS) registerStream(stream *ttsStream) bool {
	if stream == nil {
		return false
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		_ = stream.Close()
		return false
	}
	if t.streams == nil {
		t.streams = make(map[*ttsStream]struct{})
	}
	stream.provider = t
	t.streams[stream] = struct{}{}
	t.mu.Unlock()
	return true
}

func (t *TTS) unregisterStream(stream *ttsStream) {
	if stream == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.streams, stream)
}

func (t *TTS) requireAPIKey() error {
	if t.apiKey == "" {
		return fmt.Errorf("api key is required, or set %s environment variable", slngAPIKeyEnv)
	}
	return nil
}

func (t *TTS) ttsCandidateCount() int {
	if len(t.connections) > 0 {
		return len(t.connections)
	}
	if t.endpoint == "" {
		return 0
	}
	return 1
}

func (t *TTS) resolvedTTSCandidates() ([]ttsConnectionCandidate, error) {
	t.mu.Lock()
	connections := cloneTTSConnectionConfigs(t.connections)
	endpoint := t.endpoint
	model := t.model
	voice := t.voice
	runtimeInit := cloneSLNGMap(t.runtimeInit)
	t.mu.Unlock()

	if len(connections) == 0 {
		if endpoint == "" {
			return nil, errors.New("slng tts websocket endpoint is empty")
		}
		return []ttsConnectionCandidate{{
			endpoint: endpoint,
			model:    model,
			voice:    voice,
			init:     runtimeInit,
		}}, nil
	}

	candidates := make([]ttsConnectionCandidate, 0, len(connections))
	for _, config := range connections {
		candidateModel, err := bridgeModel(config.Endpoint, "tts")
		if err != nil {
			return nil, err
		}
		if config.Model != "" && config.Model != candidateModel {
			return nil, errors.New("TTS connection model must match its endpoint")
		}
		candidateVoice := config.Voice
		if candidateVoice == "" {
			candidateVoice = voice
		}
		init := runtimeInit
		if config.Init != nil {
			init = cloneSLNGMap(config.Init)
		}
		candidates = append(candidates, ttsConnectionCandidate{
			endpoint: config.Endpoint,
			model:    candidateModel,
			voice:    candidateVoice,
			headers:  config.Headers.Clone(),
			init:     init,
		})
	}
	return candidates, nil
}

func (t *TTS) ttsAttempt(candidate ttsConnectionCandidate) *TTS {
	t.mu.Lock()
	defer t.mu.Unlock()
	return &TTS{
		apiKey:            t.apiKey,
		providerAPIKey:    t.providerAPIKey,
		model:             candidate.model,
		endpoint:          candidate.endpoint,
		regionOverride:    t.regionOverride,
		worldPartOverride: t.worldPartOverride,
		externalAgentID:   t.externalAgentID,
		externalSessionID: t.externalSessionID,
		extraHeaders:      t.extraHeaders.Clone(),
		voice:             normalizeTTSVoice(candidate.model, candidate.voice),
		language:          t.language,
		sampleRate:        t.sampleRate,
		speed:             t.speed,
		encoding:          t.encoding,
		modelOptions:      cloneSLNGMap(t.modelOptions),
		textChunking:      t.textChunking,
		phraseMaxChars:    t.phraseMaxChars,
		firstAudioTimeout: t.firstAudioTimeout,
	}
}

func (t *TTS) acquireTTSConnection(ctx context.Context) (*websocket.Conn, ttsConnectionCandidate, int, *TTS, error) {
	t.mu.Lock()
	candidateIndex := -1
	if t.candidateState != nil {
		candidateIndex = t.candidateState.start(slngTTSNow())
	}
	standby := t.standby
	if standby != nil {
		t.standby = nil
	}
	epoch := t.standbyEpoch
	t.mu.Unlock()

	if standby != nil {
		if standby.epoch == epoch && standby.candidateIndex == candidateIndex {
			return standby.conn, standby.candidate, standby.candidateIndex, standby.attempt, nil
		}
		_ = standby.conn.Close()
	}
	return t.dialTTSCandidates(ctx, candidateIndex)
}

func (t *TTS) dialTTSStandby(ctx context.Context, candidateIndex int) (*websocket.Conn, ttsConnectionCandidate, *TTS, error) {
	candidates, err := t.resolvedTTSCandidates()
	if err != nil {
		return nil, ttsConnectionCandidate{}, nil, err
	}
	if candidateIndex < 0 || candidateIndex >= len(candidates) {
		return nil, ttsConnectionCandidate{}, nil, errors.New("slng tts active connection candidate is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	candidate := candidates[candidateIndex]
	attempt := t.ttsAttempt(candidate)
	headers, err := buildTTSWebsocketHeadersForCandidate(attempt, candidate.headers)
	if err != nil {
		return nil, ttsConnectionCandidate{}, nil, err
	}
	conn, response, err := slngTTSDialContext(ctx, candidate.endpoint, headers)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ttsConnectionCandidate{}, nil, ctxErr
		}
		return nil, ttsConnectionCandidate{}, nil, slngTTSDialError(response, err)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		_ = conn.Close()
		return nil, ttsConnectionCandidate{}, nil, ctxErr
	}
	initPayload := buildTTSInitPayload(attempt)
	if candidate.init != nil {
		initPayload, err = json.Marshal(candidate.init)
	}
	if err == nil {
		err = conn.WriteMessage(websocket.TextMessage, initPayload)
	}
	if err != nil {
		_ = conn.Close()
		return nil, ttsConnectionCandidate{}, nil, llm.NewAPIConnectionError(fmt.Sprintf("failed to initialize slng tts websocket: %v", err))
	}
	return conn, candidate, attempt, nil
}

func (t *TTS) dialTTSCandidates(ctx context.Context, candidateIndex int) (*websocket.Conn, ttsConnectionCandidate, int, *TTS, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	candidates, err := t.resolvedTTSCandidates()
	if err != nil {
		return nil, ttsConnectionCandidate{}, -1, nil, err
	}

	t.mu.Lock()
	if t.candidateState == nil || t.candidateState.count != len(candidates) {
		t.candidateState = newCandidateState(len(candidates), t.fallbackCooldown)
	}
	if candidateIndex < 0 {
		candidateIndex = t.candidateState.start(slngTTSNow())
	}
	t.mu.Unlock()

	var lastErr error
	for candidateIndex >= 0 && candidateIndex < len(candidates) {
		if err := ctx.Err(); err != nil {
			return nil, ttsConnectionCandidate{}, -1, nil, err
		}
		candidate := candidates[candidateIndex]
		attempt := t.ttsAttempt(candidate)
		headers, err := buildTTSWebsocketHeadersForCandidate(attempt, candidate.headers)
		if err != nil {
			return nil, ttsConnectionCandidate{}, -1, nil, err
		}
		conn, response, dialErr := slngTTSDialContext(ctx, candidate.endpoint, headers)
		var candidateErr error
		if dialErr != nil {
			if err := ctx.Err(); err != nil {
				return nil, ttsConnectionCandidate{}, -1, nil, err
			}
			candidateErr = slngTTSDialError(response, dialErr)
		} else if err := ctx.Err(); err != nil {
			_ = conn.Close()
			return nil, ttsConnectionCandidate{}, -1, nil, err
		} else {
			initPayload := buildTTSInitPayload(attempt)
			if candidate.init != nil {
				initPayload, err = json.Marshal(candidate.init)
			}
			if err == nil {
				err = conn.WriteMessage(websocket.TextMessage, initPayload)
			}
			if err != nil {
				_ = conn.Close()
				candidateErr = llm.NewAPIConnectionError(fmt.Sprintf("failed to initialize slng tts websocket: %v", err))
			}
		}
		if candidateErr != nil {
			lastErr = candidateErr
			if !isSLNGFallbackEligible(candidateErr) {
				return nil, ttsConnectionCandidate{}, -1, nil, candidateErr
			}
			t.mu.Lock()
			nextIndex, ok := t.candidateState.advance(candidateIndex, slngTTSNow())
			t.mu.Unlock()
			if !ok {
				return nil, ttsConnectionCandidate{}, -1, nil, lastErr
			}
			candidateIndex = nextIndex
			continue
		}
		if t.isClosed() {
			_ = conn.Close()
			return nil, ttsConnectionCandidate{}, -1, nil, io.ErrClosedPipe
		}
		t.mu.Lock()
		t.candidateState.selectCandidate(candidateIndex)
		t.mu.Unlock()
		return conn, candidate, candidateIndex, attempt, nil
	}
	return nil, ttsConnectionCandidate{}, -1, nil, lastErr
}

func (t *TTS) advanceTTSCandidate(ctx context.Context, candidateIndex int) (*websocket.Conn, ttsConnectionCandidate, int, *TTS, error) {
	t.mu.Lock()
	nextIndex, ok := t.candidateState.advance(candidateIndex, slngTTSNow())
	t.mu.Unlock()
	if !ok {
		return nil, ttsConnectionCandidate{}, -1, nil, errors.New("slng tts fallback candidates exhausted")
	}
	return t.dialTTSCandidates(ctx, nextIndex)
}

func slngTTSDialError(response *http.Response, err error) error {
	if response == nil {
		return llm.NewAPIConnectionError(fmt.Sprintf("failed to dial slng tts websocket: %v", err))
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = http.StatusText(response.StatusCode)
	}
	return llm.NewAPIStatusError(message, response.StatusCode, response.Header.Get("X-Request-ID"), string(body))
}

func buildTTSWebsocketHeaders(t *TTS) (http.Header, error) {
	return buildTTSWebsocketHeadersForCandidate(t, nil)
}

func buildTTSWebsocketHeadersForCandidate(t *TTS, candidate http.Header) (http.Header, error) {
	return (gatewayHeaders{
		APIKey:            t.apiKey,
		ProviderAPIKey:    t.providerAPIKey,
		RegionOverride:    t.regionOverride,
		WorldPartOverride: t.worldPartOverride,
		ExternalAgentID:   t.externalAgentID,
		ExternalSessionID: t.externalSessionID,
		Extra:             t.extraHeaders,
	}).build(candidate)
}
func buildTTSInitPayload(t *TTS) []byte {
	language := normalizeLanguageForModel(t.model, t.language, t.modelOptions)
	config := map[string]any{
		"language":    language,
		"encoding":    t.encoding,
		"sample_rate": t.sampleRate,
		"speed":       t.speed,
	}
	payload := map[string]any{
		"type":     "init",
		"model":    t.model,
		"voice":    t.voice,
		"language": language,
		"config":   config,
	}
	ref, err := parseModelRef(t.model)
	if err == nil {
		switch {
		case ref.routeProvider == "deepgram" && ref.routeModel == "aura":
			payload["model"] = t.voice
		case ref.routeProvider == "rime" && ref.routeModel == "arcana":
			config["modelId"] = slngOptionDefault(t.modelOptions, "modelId", "arcana")
			config["segment"] = slngOptionDefault(t.modelOptions, "segment", "bySentence")
			for _, key := range []string{"speakingStyle", "addBreathing", "addDisfluencies", "phonemizeBetweenBrackets", "translateTo"} {
				if value, ok := t.modelOptions[key]; ok {
					config[key] = value
				}
			}
			payload["speaker"] = t.voice
		case ref.routeProvider == "rime" && ref.routeModel == "coda":
			config["modelId"] = slngOptionDefault(t.modelOptions, "modelId", "coda")
			if value, ok := t.modelOptions["segment"]; ok {
				config["segment"] = value
			}
			payload["speaker"] = t.voice
		case ref.routeProvider == "elevenlabs":
			for _, key := range slngElevenLabsTTSModelOptionKeys {
				if value, ok := t.modelOptions[key]; ok {
					config[key] = value
				}
			}
		case ref.routeProvider == "sarvam" && ref.routeModel == "bulbul":
			config["speech_sample_rate"] = fmt.Sprint(t.sampleRate)
			config["pace"] = slngOptionDefault(t.modelOptions, "pace", t.speed)
			for _, key := range []string{"temperature", "output_audio_bitrate", "min_buffer_size", "max_chunk_length"} {
				if value, ok := t.modelOptions[key]; ok {
					config[key] = value
				}
			}
		}
	}
	data, _ := json.Marshal(payload)
	return data
}
func isRimeArcanaModel(modelName string) bool {
	ref, err := parseModelRef(modelName)
	return err == nil && ref.routeProvider == "rime" && ref.routeModel == "arcana"
}

func isRimeCodaModel(modelName string) bool {
	ref, err := parseModelRef(modelName)
	return err == nil && ref.routeProvider == "rime" && ref.routeModel == "coda"
}
func normalizeTTSVoice(modelName, voice string) string {
	cleaned := strings.TrimSpace(voice)
	ref, err := parseModelRef(modelName)
	if err != nil {
		return cleaned
	}
	if ref.routeProvider == "deepgram" && ref.routeModel == "aura" {
		if cleaned != "" && cleaned != "default" {
			return cleaned
		}
		if mapped := auraDefaultVoiceByVariant[ref.variant]; mapped != "" {
			return mapped
		}
		return auraDefaultVoiceByVariant["2"]
	}
	if ref.routeProvider == "rime" && ref.routeModel == "arcana" {
		if cleaned != "" && cleaned != "default" {
			return cleaned
		}
		lang := rimeLangFromVariant(ref.variant)
		if lang == "" {
			lang = "en"
		}
		return rimeDefaultSpeakerByLang[lang]
	}
	return cleaned
}

func rimeLangFromVariant(variant string) string {
	if variant == "" {
		return ""
	}
	if _, ok := rimeDefaultSpeakerByLang[variant]; ok {
		return variant
	}
	if _, lang, ok := strings.Cut(variant, "-"); ok {
		if _, exists := rimeDefaultSpeakerByLang[lang]; exists {
			return lang
		}
	}
	return ""
}

var auraDefaultVoiceByVariant = map[string]string{
	"":     "aura-2-thalia-en",
	"2":    "aura-2-thalia-en",
	"2-en": "aura-2-thalia-en",
	"2-es": "aura-2-celeste-es",
}

var rimeDefaultSpeakerByLang = map[string]string{
	"ar": "sakina",
	"de": "lorelei",
	"en": "astra",
	"es": "seraphina",
	"fr": "destin",
}

func ttsAudioFromMessage(payload []byte, sampleRate int) (*tts.SynthesizedAudio, bool, error) {
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, nil
	}
	if isSLNGTTSEndEvent(message) {
		return slngTTSFinalMarker(), true, nil
	}
	messageType := slngString(message["type"])
	switch messageType {
	case "Audio", "audio", "audio_chunk", "chunk":
		encoded := extractSLNGAudio(message)
		if encoded == "" {
			return nil, false, nil
		}
		data, err := slngDecodeBase64Audio(encoded)
		if err != nil {
			return nil, false, nil
		}
		return &tts.SynthesizedAudio{
			Frame: &model.AudioFrame{
				Data:              data,
				SampleRate:        uint32(sampleRate),
				NumChannels:       slngNumChannels,
				SamplesPerChannel: uint32(len(data) / 2),
			},
		}, false, nil
	case "Flushed", "audio_end", "end", "flushed", "complete", "completed", "done", "final":
		return slngTTSFinalMarker(), true, nil
	case "Error", "error":
		return nil, false, slngStatusError(message)
	case "":
		if encoded := slngString(message["audio"]); encoded != "" {
			data, err := slngDecodeBase64Audio(encoded)
			if err != nil {
				if slngBool(message["isFinal"]) {
					return slngTTSFinalMarker(), true, nil
				}
				return nil, false, nil
			}
			audio := &tts.SynthesizedAudio{
				Frame: &model.AudioFrame{
					Data:              data,
					SampleRate:        uint32(sampleRate),
					NumChannels:       slngNumChannels,
					SamplesPerChannel: uint32(len(data) / 2),
				},
			}
			return audio, slngBool(message["isFinal"]), nil
		}
		if slngBool(message["isFinal"]) {
			return slngTTSFinalMarker(), true, nil
		}
		if message["error"] != nil {
			return nil, false, slngStatusError(message)
		}
	}
	return nil, false, nil
}

func slngDecodeBase64Audio(data string) ([]byte, error) {
	clean := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		b := data[i]
		switch {
		case b >= 'A' && b <= 'Z',
			b >= 'a' && b <= 'z',
			b >= '0' && b <= '9',
			b == '+',
			b == '/',
			b == '=':
			clean = append(clean, b)
		}
	}
	return base64.StdEncoding.DecodeString(string(clean))
}

func slngTTSFinalMarker() *tts.SynthesizedAudio {
	return &tts.SynthesizedAudio{IsFinal: true}
}

func isSLNGTTSEndEvent(message map[string]any) bool {
	if slngString(message["type"]) != "event" {
		return false
	}
	data, _ := message["data"].(map[string]any)
	raw := strings.ToLower(slngString(data["event_type"]))
	if raw == "" {
		raw = strings.ToLower(slngString(data["event"]))
	}
	switch raw {
	case "complete", "completed", "done", "end", "final":
		return true
	default:
		return false
	}
}

type ttsStream struct {
	mu                  sync.Mutex
	provider            *TTS
	ctx                 context.Context
	cancel              context.CancelFunc
	conn                *websocket.Conn
	candidateIndex      int
	sampleRate          int
	model               string
	audioFrames         int
	audioBytes          int
	textMessages        int
	pendingText         string
	wordBuffer          string
	wordBufferHasLetter bool
	lastMessageType     string
	appendTextSpace     bool
	firstAudioTimeout   time.Duration
	firstAudioDeadline  time.Time
	textChunking        TTSChunkingMode
	phraseMaxChars      int
	replay              []ttsInputAction
	closed              bool
}

type ttsInputAction struct {
	text  string
	flush bool
}

func (s *ttsStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if text == "" {
		return nil
	}
	if s.conn == nil {
		return io.ErrClosedPipe
	}
	if s.audioFrames == 0 {
		s.replay = append(s.replay, ttsInputAction{text: text})
	}
	return s.pushTextLocked(text)
}

func (s *ttsStream) pushTextLocked(text string) error {
	s.pendingText += text
	if s.textChunking == TTSChunkingAuto || s.textChunking == TTSChunkingPhrase {
		return s.sendPhraseChunksLocked(false)
	}
	return s.sendCompleteWordsLocked()
}

func (s *ttsStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.conn == nil {
		return io.ErrClosedPipe
	}
	if s.audioFrames == 0 {
		s.replay = append(s.replay, ttsInputAction{flush: true})
	}
	return s.flushLocked()
}

func (s *ttsStream) flushLocked() error {
	if s.pendingText != "" && (s.textChunking == TTSChunkingAuto || s.textChunking == TTSChunkingPhrase) {
		if err := s.sendPhraseChunksLocked(true); err != nil {
			return err
		}
	} else {
		if err := s.flushWordChunksLocked(); err != nil {
			return err
		}
	}
	if isRimeArcanaModel(s.model) {
		if err := s.conn.WriteMessage(websocket.TextMessage, []byte(slngCancelMessage)); err != nil {
			s.closed = true
			_ = s.conn.Close()
			if s.provider != nil {
				s.provider.unregisterStream(s)
			}
			return err
		}
		return nil
	}
	if err := s.conn.WriteMessage(websocket.TextMessage, []byte(slngFlushMessage)); err != nil {
		s.closed = true
		_ = s.conn.Close()
		if s.provider != nil {
			s.provider.unregisterStream(s)
		}
		return err
	}
	return nil
}

func (s *ttsStream) sendPhraseChunksLocked(flush bool) error {
	chunks, remainder := slngPhraseChunkParts(s.pendingText, s.phraseMaxChars, flush)
	for _, chunk := range chunks {
		if err := s.sendTextLocked(chunk); err != nil {
			return err
		}
	}
	s.pendingText = remainder
	return nil
}

func (s *ttsStream) sendCompleteWordsLocked() error {
	tokens := tokenize.SplitWords(s.pendingText, false, false, false)
	if len(tokens) == 0 {
		return nil
	}
	for _, token := range tokens[:len(tokens)-1] {
		if err := s.bufferWordTokenLocked(token.Token); err != nil {
			return err
		}
	}
	last := tokens[len(tokens)-1]
	if strings.ContainsFunc(last.Token, unicode.IsLetter) && s.wordBufferHasLetter {
		if err := s.sendTextLocked(s.wordBuffer); err != nil {
			return err
		}
		s.wordBuffer = ""
		s.wordBufferHasLetter = false
	}
	runes := []rune(s.pendingText)
	s.pendingText = string(runes[last.Start:])
	return nil
}

func (s *ttsStream) flushWordChunksLocked() error {
	for _, token := range tokenize.SplitWords(s.pendingText, false, false, false) {
		if err := s.bufferWordTokenLocked(token.Token); err != nil {
			return err
		}
	}
	s.pendingText = ""
	if s.wordBufferHasLetter {
		if err := s.sendTextLocked(s.wordBuffer); err != nil {
			return err
		}
	}
	s.wordBuffer = ""
	s.wordBufferHasLetter = false
	return nil
}

func (s *ttsStream) bufferWordTokenLocked(token string) error {
	piece := token
	hasLetter := strings.ContainsFunc(token, unicode.IsLetter)
	if s.wordBuffer == "" {
		s.wordBuffer = piece
		s.wordBufferHasLetter = hasLetter
		return nil
	}
	if hasLetter && s.wordBufferHasLetter {
		if err := s.sendTextLocked(s.wordBuffer); err != nil {
			return err
		}
		s.wordBuffer = piece
		s.wordBufferHasLetter = true
		return nil
	}
	s.wordBuffer += " " + piece
	s.wordBufferHasLetter = s.wordBufferHasLetter || hasLetter
	return nil
}

func (s *ttsStream) sendTextLocked(text string) error {
	if s.appendTextSpace && !strings.HasSuffix(text, " ") {
		text += " "
	}
	data, err := json.Marshal(map[string]any{"type": "text", "text": text})
	if err != nil {
		return err
	}
	if err := s.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		s.closed = true
		_ = s.conn.Close()
		if s.provider != nil {
			s.provider.unregisterStream(s)
		}
		return err
	}
	if s.audioFrames == 0 && s.firstAudioTimeout > 0 && s.firstAudioDeadline.IsZero() {
		s.firstAudioDeadline = time.Now().Add(s.firstAudioTimeout)
		_ = s.conn.SetReadDeadline(s.firstAudioDeadline)
	}
	if s.provider != nil {
		s.provider.startTTSStandby()
	}
	return nil
}

func (s *ttsStream) EndInput() error { return s.Flush() }

func (s *ttsStream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.firstAudioDeadline = time.Time{}
	cancel := s.cancel
	s.cancel = nil
	provider := s.provider
	conn := s.conn
	s.conn = nil
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	var err error
	if conn != nil {
		_ = conn.SetReadDeadline(time.Now())
		err = conn.Close()
	}
	if provider != nil {
		provider.unregisterStream(s)
	}
	return err
}

func (s *ttsStream) Next() (*tts.SynthesizedAudio, error) {
	for {
		s.mu.Lock()
		closed := s.closed
		conn := s.conn
		if closed || conn == nil {
			s.mu.Unlock()
			return nil, io.EOF
		}
		err := conn.SetReadDeadline(s.firstAudioDeadline)
		s.mu.Unlock()
		if err != nil {
			return nil, err
		}
		cancelRead := make(chan struct{})
		if s.ctx != nil && s.ctx.Done() != nil {
			go func() {
				select {
				case <-s.ctx.Done():
					_ = conn.SetReadDeadline(time.Now())
				case <-cancelRead:
				}
			}()
		}
		msgType, payload, err := conn.ReadMessage()
		close(cancelRead)
		if s.ctx != nil && s.ctx.Err() != nil {
			_ = s.Close()
			return nil, s.ctx.Err()
		}
		if err != nil {
			candidateErr := s.readError(err)
			var netErr net.Error
			s.mu.Lock()
			armedDeadline := s.firstAudioDeadline
			s.mu.Unlock()
			if errors.As(err, &netErr) && netErr.Timeout() && !armedDeadline.IsZero() {
				candidateErr = llm.NewAPITimeoutError("SLNG TTS first audio timed out")
			}
			if fallbackErr, ok := s.fallbackBeforeFirstAudio(candidateErr); ok {
				if fallbackErr == nil {
					continue
				}
				return nil, fallbackErr
			}
			if _, ok := candidateErr.(*llm.APITimeoutError); ok {
				_ = s.Close()
			}
			return nil, candidateErr
		}
		if msgType == websocket.BinaryMessage {
			s.mu.Lock()
			s.audioFrames++
			s.audioBytes += len(payload)
			s.lastMessageType = "binary"
			s.firstAudioDeadline = time.Time{}
			s.replay = nil
			s.mu.Unlock()
			_ = conn.SetReadDeadline(time.Time{})
			return &tts.SynthesizedAudio{
				Frame: &model.AudioFrame{
					Data:              payload,
					SampleRate:        uint32(s.sampleRate),
					NumChannels:       slngNumChannels,
					SamplesPerChannel: uint32(len(payload) / 2),
				},
			}, nil
		}
		if msgType != websocket.TextMessage {
			continue
		}
		s.textMessages++
		s.lastMessageType = slngTTSMessageKind(payload)
		audio, done, err := ttsAudioFromMessage(payload, s.sampleRate)
		if err != nil {
			if fallbackErr, ok := s.fallbackBeforeFirstAudio(err); ok {
				if fallbackErr == nil {
					continue
				}
				return nil, fallbackErr
			}
			return nil, err
		}
		if audio != nil && audio.Frame != nil {
			s.mu.Lock()
			s.audioFrames++
			s.audioBytes += len(audio.Frame.Data)
			s.firstAudioDeadline = time.Time{}
			s.replay = nil
			s.mu.Unlock()
			_ = conn.SetReadDeadline(time.Time{})
		}
		if done {
			s.mu.Lock()
			s.firstAudioDeadline = time.Time{}
			s.mu.Unlock()
			_ = conn.SetReadDeadline(time.Time{})
			return nil, io.EOF
		}
		if audio != nil {
			return audio, nil
		}
	}
}

func (s *ttsStream) fallbackBeforeFirstAudio(failure error) (error, bool) {
	s.mu.Lock()
	if s.closed || s.audioFrames > 0 || s.provider == nil || !isSLNGFallbackEligible(failure) {
		s.mu.Unlock()
		return failure, false
	}
	provider := s.provider
	ctx := s.ctx
	candidateIndex := s.candidateIndex
	originalConn := s.conn
	s.mu.Unlock()

	conn, candidate, nextIndex, attempt, err := provider.advanceTTSCandidate(ctx, candidateIndex)
	if err != nil {
		if strings.Contains(err.Error(), "fallback candidates exhausted") {
			_ = s.Close()
			return failure, true
		}
		return err, true
	}

	s.mu.Lock()
	if s.closed || s.conn != originalConn || (ctx != nil && ctx.Err() != nil) {
		s.mu.Unlock()
		_ = conn.Close()
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err(), true
		}
		return io.ErrClosedPipe, true
	}
	oldConn := originalConn
	s.conn = conn
	s.candidateIndex = nextIndex
	s.sampleRate = attempt.sampleRate
	s.model = candidate.model
	s.pendingText = ""
	s.wordBuffer = ""
	s.wordBufferHasLetter = false
	s.textMessages = 0
	s.lastMessageType = ""
	s.firstAudioTimeout = attempt.firstAudioTimeout
	s.firstAudioDeadline = time.Time{}
	s.textChunking = attempt.textChunking
	s.phraseMaxChars = attempt.phraseMaxChars
	actions := append([]ttsInputAction(nil), s.replay...)
	var replayErr error
	for _, action := range actions {
		if action.flush {
			replayErr = s.flushLocked()
		} else {
			replayErr = s.pushTextLocked(action.text)
		}
		if replayErr != nil {
			break
		}
	}
	s.mu.Unlock()
	_ = oldConn.Close()
	if replayErr != nil {
		_ = s.Close()
		return replayErr, true
	}
	return nil, true
}

func (s *ttsStream) readError(err error) error {
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) {
		return err
	}
	if closeErr.Code == websocket.CloseNormalClosure && (s.audioFrames > 0 || isRimeArcanaModel(s.model) || isRimeCodaModel(s.model)) {
		return io.EOF
	}
	message := fmt.Sprintf(
		"slng tts websocket closed before completion: %v (model=%s audio_frames=%d audio_bytes=%d text_messages=%d last_message_type=%q)",
		err,
		s.model,
		s.audioFrames,
		s.audioBytes,
		s.textMessages,
		s.lastMessageType,
	)
	return llm.NewAPIStatusError(message, closeErr.Code, "", err.Error())
}

func slngTTSMessageKind(payload []byte) string {
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		return "text/non-json"
	}
	if messageType := slngString(message["type"]); messageType != "" {
		return messageType
	}
	if slngString(message["audio"]) != "" {
		return "audio"
	}
	if slngBool(message["isFinal"]) {
		return "isFinal"
	}
	if message["error"] != nil {
		return "error"
	}
	return "text/unknown"
}

type ttsChunkedStream struct {
	stream tts.SynthesizeStream
}

func (s *ttsChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	return s.stream.Next()
}

func (s *ttsChunkedStream) Close() error {
	return s.stream.Close()
}

func extractSLNGAudio(message map[string]any) string {
	if data := slngString(message["data"]); data != "" {
		return data
	}
	if data := slngMap(message["data"]); len(data) > 0 {
		return slngString(data["audio"])
	}
	return slngString(message["audio"])
}
