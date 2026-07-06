package rime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/adapter/blingfire"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	cavosmath "github.com/cavos-io/rtp-agent/library/math"
	"github.com/cavos-io/rtp-agent/library/tokenize"
	"github.com/gorilla/websocket"
)

const (
	defaultRimeHTTPBaseURL   = "https://users.rime.ai/v1/rime-tts"
	defaultRimeWSBaseURL     = "wss://users-ws.rime.ai"
	defaultRimeModel         = "arcana"
	defaultRimeArcanaVoice   = "astra"
	defaultRimeMistVoice     = "cove"
	defaultRimeCodaVoice     = "lyra"
	defaultRimeLang          = "eng"
	defaultRimeSampleRate    = 22050
	defaultRimeSegment       = "bySentence"
	defaultRimeStreamTimeout = 10 * time.Second
	rimeArcanaModelTimeout   = 240 * time.Second
	rimeMistModelTimeout     = 30 * time.Second
	rimeWebsocketMaxAge      = 300 * time.Second
)

type RimeTTS struct {
	mu                       sync.Mutex
	streams                  map[*rimeTTSSynthesizeStream]struct{}
	prewarmConn              *websocket.Conn
	prewarmURL               string
	prewarmRefreshedAt       time.Time
	prewarmInFlight          bool
	prewarmCancel            context.CancelFunc
	prewarmDone              chan struct{}
	prewarmSeq               uint64
	poolGeneration           uint64
	apiKey                   string
	baseURL                  string
	model                    string
	voice                    string
	voiceSet                 bool
	lang                     string
	langSet                  bool
	langByModel              map[string]rimeStringOption
	sampleRate               int
	requestSampleRate        int
	requestSampleRateSet     bool
	requestSampleRates       map[string]*int
	timeScaleFactor          *float64
	timeScaleFactors         map[string]*float64
	repetitionPenalty        *float64
	arcanaRepetitionPenalty  *float64
	temperature              *float64
	arcanaTemperature        *float64
	topP                     *float64
	arcanaTopP               *float64
	maxTokens                *int
	maxTokensByModel         map[string]*int
	speedAlpha               *float64
	mistSpeedAlpha           *float64
	reduceLatency            *bool
	mistReduceLatency        *bool
	pauseBetweenBrackets     *bool
	mistPauseBetweenBrackets *bool
	phonemizeBetweenBrackets *bool
	mistPhonemizeBrackets    *bool
	useWebsocket             bool
	segment                  string
	sentenceTokenizer        tokenize.SentenceTokenizer
	streamResponseTimeout    time.Duration
	closed                   bool
	modelTouched             bool
	langTouched              bool
	requestSampleRateTouched bool
	timeScaleFactorTouched   bool
	repetitionPenaltyTouched bool
	temperatureTouched       bool
	topPTouched              bool
	maxTokensTouched         bool
	speedAlphaTouched        bool
	reduceLatencyTouched     bool
	pauseBracketsTouched     bool
	phonemizeBracketsTouched bool
}

type rimeStringOption struct {
	value string
	set   bool
}

type RimeTTSOption func(*RimeTTS)

func WithRimeTTSBaseURL(baseURL string) RimeTTSOption {
	return func(t *RimeTTS) {
		t.baseURL = baseURL
		if strings.HasPrefix(baseURL, "ws://") || strings.HasPrefix(baseURL, "wss://") {
			t.useWebsocket = true
		}
	}
}

func WithRimeTTSModel(model string) RimeTTSOption {
	return func(t *RimeTTS) {
		t.model = model
		t.modelTouched = true
		t.restoreCommonParamsForModel()
		t.restoreTimeScaleFactorForModel()
		t.restoreArcanaParamsForModel()
		t.restoreMistParamsForModel()
		t.restoreMaxTokensForModel()
		if !t.voiceSet && t.voice == "" {
			t.voice = defaultRimeVoice(model)
		}
	}
}

func WithRimeTTSVoice(voice string) RimeTTSOption {
	return func(t *RimeTTS) {
		t.voice = voice
		t.voiceSet = true
	}
}

func WithRimeTTSSampleRate(sampleRate int) RimeTTSOption {
	return func(t *RimeTTS) {
		t.requestSampleRate = sampleRate
		t.requestSampleRateSet = true
		t.requestSampleRateTouched = true
	}
}

func WithRimeTTSLang(lang string) RimeTTSOption {
	return func(t *RimeTTS) {
		t.lang = lang
		t.langSet = true
		t.langTouched = true
	}
}

func WithRimeTTSTimeScaleFactor(timeScaleFactor float64) RimeTTSOption {
	return func(t *RimeTTS) {
		t.timeScaleFactor = &timeScaleFactor
		t.timeScaleFactorTouched = true
		t.storeTimeScaleFactorForModel()
	}
}

func WithRimeTTSRepetitionPenalty(repetitionPenalty float64) RimeTTSOption {
	return func(t *RimeTTS) {
		t.repetitionPenalty = &repetitionPenalty
		t.repetitionPenaltyTouched = true
	}
}

func WithRimeTTSTemperature(temperature float64) RimeTTSOption {
	return func(t *RimeTTS) {
		t.temperature = &temperature
		t.temperatureTouched = true
	}
}

func WithRimeTTSTopP(topP float64) RimeTTSOption {
	return func(t *RimeTTS) {
		t.topP = &topP
		t.topPTouched = true
	}
}

func WithRimeTTSMaxTokens(maxTokens int) RimeTTSOption {
	return func(t *RimeTTS) {
		t.maxTokens = &maxTokens
		t.maxTokensTouched = true
	}
}

func WithRimeTTSSpeedAlpha(speedAlpha float64) RimeTTSOption {
	return func(t *RimeTTS) {
		t.speedAlpha = &speedAlpha
		t.speedAlphaTouched = true
	}
}

func WithRimeTTSReduceLatency(reduceLatency bool) RimeTTSOption {
	return func(t *RimeTTS) {
		t.reduceLatency = &reduceLatency
		t.reduceLatencyTouched = true
	}
}

func WithRimeTTSPauseBetweenBrackets(pauseBetweenBrackets bool) RimeTTSOption {
	return func(t *RimeTTS) {
		t.pauseBetweenBrackets = &pauseBetweenBrackets
		t.pauseBracketsTouched = true
	}
}

func WithRimeTTSPhonemizeBetweenBrackets(phonemizeBetweenBrackets bool) RimeTTSOption {
	return func(t *RimeTTS) {
		t.phonemizeBetweenBrackets = &phonemizeBetweenBrackets
		t.phonemizeBracketsTouched = true
	}
}

func WithRimeTTSWebsocket(useWebsocket bool) RimeTTSOption {
	return func(t *RimeTTS) {
		t.useWebsocket = useWebsocket
	}
}

func WithRimeTTSSegment(segment string) RimeTTSOption {
	return func(t *RimeTTS) {
		t.segment = segment
	}
}

func WithRimeTTSSentenceTokenizer(tokenizer tokenize.SentenceTokenizer) RimeTTSOption {
	return func(t *RimeTTS) {
		if tokenizer != nil {
			t.sentenceTokenizer = tokenizer
		}
	}
}

func WithRimeTTSStreamResponseTimeout(timeout time.Duration) RimeTTSOption {
	return func(t *RimeTTS) {
		if timeout >= 0 {
			t.streamResponseTimeout = timeout
		}
	}
}

func NewRimeTTS(apiKey string, voice string, opts ...RimeTTSOption) *RimeTTS {
	if apiKey == "" {
		apiKey = os.Getenv("RIME_API_KEY")
	}
	provider := &RimeTTS{
		apiKey:                apiKey,
		baseURL:               defaultRimeHTTPBaseURL,
		model:                 defaultRimeModel,
		lang:                  defaultRimeLang,
		langByModel:           make(map[string]rimeStringOption),
		sampleRate:            defaultRimeSampleRate,
		requestSampleRate:     defaultRimeSampleRate,
		requestSampleRateSet:  true,
		requestSampleRates:    make(map[string]*int),
		timeScaleFactors:      make(map[string]*float64),
		maxTokensByModel:      make(map[string]*int),
		segment:               defaultRimeSegment,
		sentenceTokenizer:     newRimeTTSSentenceTokenizer(),
		streamResponseTimeout: defaultRimeStreamTimeout,
	}
	for _, opt := range opts {
		opt(provider)
	}
	provider.storeCommonParamsForModel()
	provider.storeArcanaParamsForModel()
	provider.storeMistParamsForModel()
	provider.storeTouchedMaxTokensForModel()
	provider.sampleRate = provider.requestSampleRate
	if strings.HasPrefix(provider.baseURL, "ws://") || strings.HasPrefix(provider.baseURL, "wss://") {
		provider.useWebsocket = true
	}
	normalizeRimeTransportBaseURL(provider)
	if voice != "" {
		provider.voice = voice
		provider.voiceSet = true
	}
	if !provider.voiceSet && provider.voice == "" {
		voice = defaultRimeVoice(provider.model)
		provider.voice = voice
	}
	return provider
}

func defaultRimeVoice(model string) string {
	switch {
	case model == "coda":
		return defaultRimeCodaVoice
	case strings.Contains(model, "mist"):
		return defaultRimeMistVoice
	default:
		return defaultRimeArcanaVoice
	}
}

func (t *RimeTTS) storeTimeScaleFactorForModel() {
	bucket := rimeTimeScaleFactorBucket(t.model)
	if bucket == "" {
		return
	}
	if t.timeScaleFactors == nil {
		t.timeScaleFactors = make(map[string]*float64)
	}
	t.timeScaleFactors[bucket] = cloneFloat64Ptr(t.timeScaleFactor)
}

func (t *RimeTTS) restoreTimeScaleFactorForModel() {
	if t.timeScaleFactorTouched {
		t.storeTimeScaleFactorForModel()
		return
	}
	if t.model == "mistv2" && !t.timeScaleFactorTouched {
		t.timeScaleFactor = nil
		return
	}
	bucket := rimeTimeScaleFactorBucket(t.model)
	if bucket == "" {
		t.timeScaleFactor = nil
		return
	}
	t.timeScaleFactor = cloneFloat64Ptr(t.timeScaleFactors[bucket])
}

func rimeTimeScaleFactorBucket(model string) string {
	switch {
	case model == "arcana":
		return "arcana"
	case model == "coda":
		return "coda"
	case strings.Contains(model, "mist"):
		return "mist"
	default:
		return ""
	}
}

func (t *RimeTTS) storeCommonParamsForModel() {
	bucket := rimeCommonParamsBucket(t.model)
	if bucket == "" {
		return
	}
	if t.langByModel == nil {
		t.langByModel = make(map[string]rimeStringOption)
	}
	t.langByModel[bucket] = rimeStringOption{value: t.lang, set: t.langSet || t.lang != ""}
	if t.requestSampleRates == nil {
		t.requestSampleRates = make(map[string]*int)
	}
	if t.requestSampleRateSet {
		t.requestSampleRates[bucket] = cloneIntPtr(&t.requestSampleRate)
	} else {
		t.requestSampleRates[bucket] = nil
	}
}

func (t *RimeTTS) storeTouchedCommonParamsForModel() {
	if !t.langTouched && !t.requestSampleRateTouched {
		return
	}
	bucket := rimeCommonParamsBucket(t.model)
	if bucket == "" {
		return
	}
	if t.langTouched {
		if t.langByModel == nil {
			t.langByModel = make(map[string]rimeStringOption)
		}
		t.langByModel[bucket] = rimeStringOption{value: t.lang, set: true}
	}
	if t.requestSampleRateTouched {
		if t.requestSampleRates == nil {
			t.requestSampleRates = make(map[string]*int)
		}
		t.requestSampleRates[bucket] = cloneIntPtr(&t.requestSampleRate)
	}
}

func (t *RimeTTS) restoreCommonParamsForModel() {
	if !t.langTouched {
		bucket := rimeCommonParamsBucket(t.model)
		if bucket == "" {
			t.lang = ""
			t.langSet = false
		} else if opt, ok := t.langByModel[bucket]; ok {
			t.lang = opt.value
			t.langSet = opt.set
		} else {
			t.lang = ""
			t.langSet = false
		}
	}
	if !t.requestSampleRateTouched {
		bucket := rimeCommonParamsBucket(t.model)
		if bucket == "" {
			t.requestSampleRate = 0
			t.requestSampleRateSet = false
		} else if value, ok := t.requestSampleRates[bucket]; ok && value != nil {
			t.requestSampleRate = *value
			t.requestSampleRateSet = true
		} else {
			t.requestSampleRate = 0
			t.requestSampleRateSet = false
		}
	}
}

func rimeCommonParamsBucket(model string) string {
	switch {
	case model == "arcana":
		return "arcana"
	case model == "coda":
		return "coda"
	case strings.Contains(model, "mist"):
		return "mist"
	default:
		return ""
	}
}

func (t *RimeTTS) storeArcanaParamsForModel() {
	if t.model != "arcana" {
		return
	}
	t.arcanaRepetitionPenalty = cloneFloat64Ptr(t.repetitionPenalty)
	t.arcanaTemperature = cloneFloat64Ptr(t.temperature)
	t.arcanaTopP = cloneFloat64Ptr(t.topP)
}

func (t *RimeTTS) storeTouchedArcanaParamsForModel() {
	if t.model != "arcana" {
		return
	}
	if t.repetitionPenaltyTouched {
		t.arcanaRepetitionPenalty = cloneFloat64Ptr(t.repetitionPenalty)
	}
	if t.temperatureTouched {
		t.arcanaTemperature = cloneFloat64Ptr(t.temperature)
	}
	if t.topPTouched {
		t.arcanaTopP = cloneFloat64Ptr(t.topP)
	}
}

func (t *RimeTTS) restoreArcanaParamsForModel() {
	if t.model != "arcana" {
		return
	}
	if !t.repetitionPenaltyTouched {
		t.repetitionPenalty = cloneFloat64Ptr(t.arcanaRepetitionPenalty)
	}
	if !t.temperatureTouched {
		t.temperature = cloneFloat64Ptr(t.arcanaTemperature)
	}
	if !t.topPTouched {
		t.topP = cloneFloat64Ptr(t.arcanaTopP)
	}
}

func (t *RimeTTS) storeMistParamsForModel() {
	if !strings.Contains(t.model, "mist") {
		return
	}
	t.mistSpeedAlpha = cloneFloat64Ptr(t.speedAlpha)
	t.mistReduceLatency = cloneBoolPtr(t.reduceLatency)
	t.mistPauseBetweenBrackets = cloneBoolPtr(t.pauseBetweenBrackets)
	t.mistPhonemizeBrackets = cloneBoolPtr(t.phonemizeBetweenBrackets)
}

func (t *RimeTTS) storeTouchedMistParamsForModel() {
	if !strings.Contains(t.model, "mist") {
		return
	}
	if t.speedAlphaTouched {
		t.mistSpeedAlpha = cloneFloat64Ptr(t.speedAlpha)
	}
	if t.reduceLatencyTouched {
		t.mistReduceLatency = cloneBoolPtr(t.reduceLatency)
	}
	if t.pauseBracketsTouched {
		t.mistPauseBetweenBrackets = cloneBoolPtr(t.pauseBetweenBrackets)
	}
	if t.phonemizeBracketsTouched {
		t.mistPhonemizeBrackets = cloneBoolPtr(t.phonemizeBetweenBrackets)
	}
}

func (t *RimeTTS) restoreMistParamsForModel() {
	if !strings.Contains(t.model, "mist") {
		return
	}
	if !t.speedAlphaTouched {
		t.speedAlpha = cloneFloat64Ptr(t.mistSpeedAlpha)
	}
	if !t.reduceLatencyTouched {
		t.reduceLatency = cloneBoolPtr(t.mistReduceLatency)
	}
	if !t.pauseBracketsTouched {
		t.pauseBetweenBrackets = cloneBoolPtr(t.mistPauseBetweenBrackets)
	}
	if !t.phonemizeBracketsTouched {
		t.phonemizeBetweenBrackets = cloneBoolPtr(t.mistPhonemizeBrackets)
	}
}

func (t *RimeTTS) storeTouchedMaxTokensForModel() {
	if !t.maxTokensTouched {
		return
	}
	t.storeMaxTokensForModel()
}

func (t *RimeTTS) storeMaxTokensForModel() {
	bucket := rimeMaxTokensBucket(t.model)
	if bucket == "" {
		return
	}
	if t.maxTokensByModel == nil {
		t.maxTokensByModel = make(map[string]*int)
	}
	t.maxTokensByModel[bucket] = cloneIntPtr(t.maxTokens)
}

func (t *RimeTTS) restoreMaxTokensForModel() {
	if t.maxTokensTouched {
		return
	}
	bucket := rimeMaxTokensBucket(t.model)
	if bucket == "" {
		t.maxTokens = nil
		return
	}
	t.maxTokens = cloneIntPtr(t.maxTokensByModel[bucket])
}

func rimeMaxTokensBucket(model string) string {
	switch model {
	case "arcana", "coda":
		return model
	default:
		return ""
	}
}

func cloneRimeTimeScaleFactors(src map[string]*float64) map[string]*float64 {
	if src == nil {
		return nil
	}
	dst := make(map[string]*float64, len(src))
	for key, value := range src {
		dst[key] = cloneFloat64Ptr(value)
	}
	return dst
}

func cloneRimeMaxTokensByModel(src map[string]*int) map[string]*int {
	if src == nil {
		return nil
	}
	dst := make(map[string]*int, len(src))
	for key, value := range src {
		dst[key] = cloneIntPtr(value)
	}
	return dst
}

func cloneRimeStringOptions(src map[string]rimeStringOption) map[string]rimeStringOption {
	if src == nil {
		return nil
	}
	dst := make(map[string]rimeStringOption, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func cloneRimeIntPtrs(src map[string]*int) map[string]*int {
	if src == nil {
		return nil
	}
	dst := make(map[string]*int, len(src))
	for key, value := range src {
		dst[key] = cloneIntPtr(value)
	}
	return dst
}

func cloneFloat64Ptr(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneIntPtr(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func (t *RimeTTS) Label() string { return "rime.TTS" }
func (t *RimeTTS) Model() string { return t.model }
func (t *RimeTTS) Provider() string {
	return "Rime"
}

func (t *RimeTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: t.useWebsocket, AlignedTranscript: t.useWebsocket}
}
func (t *RimeTTS) SampleRate() int  { return t.sampleRate }
func (t *RimeTTS) NumChannels() int { return 1 }

func (t *RimeTTS) UpdateOptions(opts ...RimeTTSOption) error {
	if t == nil {
		return io.ErrClosedPipe
	}
	t.mu.Lock()
	currentUseWebsocket := t.useWebsocket
	candidate := &RimeTTS{
		apiKey:                   t.apiKey,
		baseURL:                  t.baseURL,
		model:                    t.model,
		voice:                    t.voice,
		voiceSet:                 t.voiceSet,
		lang:                     t.lang,
		langSet:                  t.langSet,
		langByModel:              cloneRimeStringOptions(t.langByModel),
		sampleRate:               t.sampleRate,
		requestSampleRate:        t.requestSampleRate,
		requestSampleRateSet:     t.requestSampleRateSet,
		requestSampleRates:       cloneRimeIntPtrs(t.requestSampleRates),
		timeScaleFactor:          t.timeScaleFactor,
		timeScaleFactors:         cloneRimeTimeScaleFactors(t.timeScaleFactors),
		repetitionPenalty:        t.repetitionPenalty,
		arcanaRepetitionPenalty:  cloneFloat64Ptr(t.arcanaRepetitionPenalty),
		temperature:              t.temperature,
		arcanaTemperature:        cloneFloat64Ptr(t.arcanaTemperature),
		topP:                     t.topP,
		arcanaTopP:               cloneFloat64Ptr(t.arcanaTopP),
		maxTokens:                cloneIntPtr(t.maxTokens),
		maxTokensByModel:         cloneRimeMaxTokensByModel(t.maxTokensByModel),
		speedAlpha:               t.speedAlpha,
		mistSpeedAlpha:           cloneFloat64Ptr(t.mistSpeedAlpha),
		reduceLatency:            t.reduceLatency,
		mistReduceLatency:        cloneBoolPtr(t.mistReduceLatency),
		pauseBetweenBrackets:     t.pauseBetweenBrackets,
		mistPauseBetweenBrackets: cloneBoolPtr(t.mistPauseBetweenBrackets),
		phonemizeBetweenBrackets: t.phonemizeBetweenBrackets,
		mistPhonemizeBrackets:    cloneBoolPtr(t.mistPhonemizeBrackets),
		useWebsocket:             t.useWebsocket,
		segment:                  t.segment,
		sentenceTokenizer:        t.sentenceTokenizer,
		streamResponseTimeout:    t.streamResponseTimeout,
	}
	t.mu.Unlock()

	for _, opt := range opts {
		opt(candidate)
	}
	candidate.storeTouchedCommonParamsForModel()
	candidate.storeTouchedArcanaParamsForModel()
	candidate.storeTouchedMistParamsForModel()
	candidate.storeTouchedMaxTokensForModel()
	if err := validateRimeTimeScaleFactor(candidate); err != nil {
		return err
	}
	candidate.useWebsocket = currentUseWebsocket

	t.mu.Lock()
	var stalePrewarm *websocket.Conn
	if currentUseWebsocket && buildRimeTTSWebsocketURL(t).String() != buildRimeTTSWebsocketURL(candidate).String() {
		stalePrewarm = t.prewarmConn
		t.prewarmConn = nil
		t.prewarmURL = ""
		t.prewarmRefreshedAt = time.Time{}
		t.poolGeneration++
	}
	t.apiKey = candidate.apiKey
	t.baseURL = candidate.baseURL
	t.model = candidate.model
	t.voice = candidate.voice
	t.voiceSet = candidate.voiceSet
	t.lang = candidate.lang
	t.langSet = candidate.langSet
	t.langByModel = candidate.langByModel
	t.requestSampleRate = candidate.requestSampleRate
	t.requestSampleRateSet = candidate.requestSampleRateSet
	t.requestSampleRates = candidate.requestSampleRates
	t.timeScaleFactor = candidate.timeScaleFactor
	t.timeScaleFactors = candidate.timeScaleFactors
	t.repetitionPenalty = candidate.repetitionPenalty
	t.arcanaRepetitionPenalty = candidate.arcanaRepetitionPenalty
	t.temperature = candidate.temperature
	t.arcanaTemperature = candidate.arcanaTemperature
	t.topP = candidate.topP
	t.arcanaTopP = candidate.arcanaTopP
	t.maxTokens = candidate.maxTokens
	t.maxTokensByModel = candidate.maxTokensByModel
	t.speedAlpha = candidate.speedAlpha
	t.mistSpeedAlpha = candidate.mistSpeedAlpha
	t.reduceLatency = candidate.reduceLatency
	t.mistReduceLatency = candidate.mistReduceLatency
	t.pauseBetweenBrackets = candidate.pauseBetweenBrackets
	t.mistPauseBetweenBrackets = candidate.mistPauseBetweenBrackets
	t.phonemizeBetweenBrackets = candidate.phonemizeBetweenBrackets
	t.mistPhonemizeBrackets = candidate.mistPhonemizeBrackets
	t.useWebsocket = candidate.useWebsocket
	t.segment = candidate.segment
	t.sentenceTokenizer = candidate.sentenceTokenizer
	t.streamResponseTimeout = candidate.streamResponseTimeout
	t.mu.Unlock()

	if stalePrewarm != nil {
		_ = closeRimePrewarmedConn(stalePrewarm)
	}
	return nil
}

func normalizeRimeTransportBaseURL(t *RimeTTS) {
	if t.useWebsocket && t.baseURL == defaultRimeHTTPBaseURL {
		t.baseURL = defaultRimeWSBaseURL
		return
	}
	if !t.useWebsocket && t.baseURL == defaultRimeWSBaseURL {
		t.baseURL = defaultRimeHTTPBaseURL
	}
}

func (t *RimeTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if t.useWebsocket {
		return nil, fmt.Errorf("rime tts one-shot synthesize requires websocket mode disabled")
	}
	if err := validateRimeAPIKey(t.apiKey); err != nil {
		return nil, err
	}
	if _, err := buildRimeTTSRequest(ctx, t, text); err != nil {
		return nil, err
	}

	opts := *t
	return &rimeTTSChunkedStream{
		ctx:        ctx,
		text:       text,
		provider:   t,
		opts:       opts,
		sampleRate: t.sampleRate,
		requestID:  cavosmath.ShortUUID(""),
	}, nil
}

func buildRimeTTSRequest(ctx context.Context, t *RimeTTS, text string) (*http.Request, error) {
	if err := validateRimeTimeScaleFactor(t); err != nil {
		return nil, err
	}
	reqBody := map[string]interface{}{
		"speaker": t.voice,
		"text":    text,
		"modelId": t.model,
	}
	addRimeModelParams(reqBody, t, true)

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "audio/pcm")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	return req, nil
}

func (t *RimeTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if !t.useWebsocket {
		return nil, fmt.Errorf("rime tts streaming requires websocket mode enabled")
	}
	if err := validateRimeAPIKey(t.apiKey); err != nil {
		return nil, err
	}
	if err := validateRimeTimeScaleFactor(t); err != nil {
		return nil, err
	}
	websocketURL := buildRimeTTSWebsocketURL(t).String()
	poolGeneration := t.currentPoolGeneration()
	conn := t.takePrewarmedConn()
	if conn == nil {
		var err error
		conn, err = t.dialWebsocket(ctx)
		if err != nil {
			return nil, err
		}
	}
	if t.isClosed() {
		conn.Close()
		return nil, io.ErrClosedPipe
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &rimeTTSSynthesizeStream{
		conn:              conn,
		ctx:               streamCtx,
		cancel:            cancel,
		provider:          t,
		requestID:         cavosmath.ShortUUID(""),
		contextID:         cavosmath.ShortUUID(""),
		websocketURL:      websocketURL,
		poolGeneration:    poolGeneration,
		responseTimeout:   t.streamResponseTimeout,
		sentenceTokenizer: t.sentenceTokenizer,
		events:            make(chan *tts.SynthesizedAudio, 100),
		errCh:             make(chan error, 1),
	}
	stream.writeMessage = stream.writeWebsocketMessage
	stream.closeConn = stream.closeWebsocketConn
	if !t.registerStream(stream) {
		cancel()
		conn.Close()
		return nil, io.ErrClosedPipe
	}
	return stream, nil
}

func (t *RimeTTS) Prewarm() {
	if t == nil || !t.useWebsocket || validateRimeAPIKey(t.apiKey) != nil || validateRimeTimeScaleFactor(t) != nil {
		return
	}
	prewarmCtx, cancelPrewarm := context.WithCancel(context.Background())
	t.mu.Lock()
	if t.closed || t.prewarmConn != nil || t.prewarmInFlight {
		t.mu.Unlock()
		cancelPrewarm()
		return
	}
	prewarmURL := buildRimeTTSWebsocketURL(t).String()
	prewarmDone := make(chan struct{})
	t.prewarmInFlight = true
	t.prewarmCancel = cancelPrewarm
	t.prewarmDone = prewarmDone
	t.prewarmSeq++
	prewarmSeq := t.prewarmSeq
	poolGeneration := t.poolGeneration
	t.mu.Unlock()

	go func() {
		defer cancelPrewarm()
		defer close(prewarmDone)
		conn, err := t.dialWebsocket(prewarmCtx)
		t.mu.Lock()
		if t.prewarmSeq == prewarmSeq {
			t.prewarmInFlight = false
			t.prewarmCancel = nil
			t.prewarmDone = nil
		}
		var closeConn *websocket.Conn
		if err != nil || t.closed || t.prewarmConn != nil {
			closeConn = conn
		} else if t.poolGeneration != poolGeneration || buildRimeTTSWebsocketURL(t).String() != prewarmURL {
			closeConn = conn
		} else {
			t.prewarmConn = conn
			t.prewarmURL = prewarmURL
			t.prewarmRefreshedAt = time.Now()
		}
		t.mu.Unlock()
		if closeConn != nil {
			_ = closeRimePrewarmedConn(closeConn)
		}
	}()
}

func (t *RimeTTS) takePrewarmedConn() *websocket.Conn {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	conn := t.prewarmConn
	expired := conn != nil && time.Since(t.prewarmRefreshedAt) > rimeWebsocketMaxAge
	if conn != nil && (expired || t.prewarmURL != buildRimeTTSWebsocketURL(t).String()) {
		t.prewarmConn = nil
		t.prewarmURL = ""
		t.prewarmRefreshedAt = time.Time{}
		t.mu.Unlock()
		_ = closeRimePrewarmedConn(conn)
		return nil
	}
	t.prewarmConn = nil
	t.prewarmURL = ""
	t.prewarmRefreshedAt = time.Time{}
	t.mu.Unlock()
	return conn
}

func (t *RimeTTS) currentPoolGeneration() uint64 {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.poolGeneration
}

func (t *RimeTTS) cachePrewarmedConn(conn *websocket.Conn, websocketURL string, poolGeneration uint64) {
	if t == nil || conn == nil {
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	t.mu.Lock()
	if t.closed || !t.useWebsocket || t.prewarmConn != nil || t.poolGeneration != poolGeneration || buildRimeTTSWebsocketURL(t).String() != websocketURL {
		t.mu.Unlock()
		_ = closeRimePrewarmedConn(conn)
		return
	}
	t.prewarmConn = conn
	t.prewarmURL = websocketURL
	t.prewarmRefreshedAt = time.Now()
	t.mu.Unlock()
}

func (t *RimeTTS) dialWebsocket(ctx context.Context) (*websocket.Conn, error) {
	dialCtx := ctx
	var cancelDial context.CancelFunc
	if t.streamResponseTimeout > 0 {
		dialCtx, cancelDial = context.WithTimeout(ctx, t.streamResponseTimeout)
		defer cancelDial()
	}
	conn, resp, err := websocket.DefaultDialer.DialContext(dialCtx, buildRimeTTSWebsocketURL(t).String(), buildRimeTTSWebsocketHeaders(t))
	if err != nil {
		if resp != nil && resp.StatusCode > 0 {
			if resp.Body != nil {
				_ = resp.Body.Close()
			}
			return nil, llm.NewAPIStatusError(rimeHTTPStatusReason(resp.StatusCode, resp.Status), resp.StatusCode, "", nil)
		}
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, llm.NewAPITimeoutError(err.Error())
		}
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("failed to dial rime tts websocket: %v", err))
	}
	return conn, nil
}

func rimeHTTPStatusReason(statusCode int, status string) string {
	message := strings.TrimSpace(strings.TrimPrefix(status, strconv.Itoa(statusCode)))
	if message == "" {
		message = http.StatusText(statusCode)
	}
	if message == "" {
		message = fmt.Sprintf("HTTP %d", statusCode)
	}
	return message
}

func (t *RimeTTS) Close() error {
	t.mu.Lock()
	t.closed = true
	prewarmConn := t.prewarmConn
	prewarmCancel := t.prewarmCancel
	prewarmDone := t.prewarmDone
	t.prewarmConn = nil
	t.prewarmURL = ""
	t.prewarmRefreshedAt = time.Time{}
	t.prewarmInFlight = false
	t.prewarmCancel = nil
	t.prewarmDone = nil
	t.prewarmSeq++
	streams := make([]*rimeTTSSynthesizeStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.mu.Unlock()

	if prewarmCancel != nil {
		prewarmCancel()
	}
	if prewarmDone != nil {
		<-prewarmDone
	}

	var closeErr error
	for _, stream := range streams {
		if err := stream.closeFromProvider(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	if prewarmConn != nil {
		if err := closeRimePrewarmedConn(prewarmConn); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func closeRimePrewarmedConn(conn *websocket.Conn) error {
	if conn == nil {
		return nil
	}
	stream := &rimeTTSSynthesizeStream{
		conn:   conn,
		cancel: func() {},
		writeMessage: func(messageType int, data []byte) error {
			return conn.WriteMessage(messageType, data)
		},
		readMessage: func() (int, []byte, error) {
			return conn.ReadMessage()
		},
		closeConn: func() error {
			return conn.Close()
		},
	}
	return stream.closeFromProvider()
}

func (t *RimeTTS) isClosed() bool {
	if t == nil {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

func (t *RimeTTS) registerStream(stream *rimeTTSSynthesizeStream) bool {
	if t == nil || stream == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	if t.streams == nil {
		t.streams = make(map[*rimeTTSSynthesizeStream]struct{})
	}
	t.streams[stream] = struct{}{}
	stream.provider = t
	return true
}

func (t *RimeTTS) unregisterStream(stream *rimeTTSSynthesizeStream) {
	if t == nil || stream == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.streams, stream)
}

func validateRimeAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("rime API key is required, either as argument or set RIME_API_KEY environmental variable")
	}
	return nil
}

func validateRimeTimeScaleFactor(t *RimeTTS) error {
	if t.model == "mistv2" && t.timeScaleFactor != nil {
		return fmt.Errorf("time_scale_factor is not supported by the mistv2 model; use arcana, mistv3, or coda")
	}
	return nil
}

func buildRimeTTSWebsocketURL(t *RimeTTS) *url.URL {
	wsURL, err := url.Parse(t.baseURL + "/ws3")
	if err != nil {
		wsURL = &url.URL{Scheme: "wss", Host: strings.TrimPrefix(t.baseURL, "wss://"), Path: "/ws3"}
	}
	query := wsURL.Query()
	query.Set("speaker", t.voice)
	query.Set("modelId", t.model)
	query.Set("audioFormat", "pcm")
	query.Set("samplingRate", strconv.Itoa(t.sampleRate))
	query.Set("segment", t.segment)
	addRimeModelQueryParams(query, t)
	wsURL.RawQuery = query.Encode()
	return wsURL
}

func addRimeModelParams(params map[string]interface{}, t *RimeTTS, includeHTTPOnly bool) {
	switch {
	case t.model == "arcana":
		addRimeCommonModelParams(params, t, includeHTTPOnly)
		if t.repetitionPenalty != nil {
			params["repetition_penalty"] = *t.repetitionPenalty
		}
		if t.temperature != nil {
			params["temperature"] = *t.temperature
		}
		if t.topP != nil {
			params["top_p"] = *t.topP
		}
		if t.maxTokens != nil {
			params["max_tokens"] = *t.maxTokens
		}
	case t.model == "coda":
		addRimeCommonModelParams(params, t, includeHTTPOnly)
		if t.maxTokens != nil {
			params["max_tokens"] = *t.maxTokens
		}
	case strings.Contains(t.model, "mist"):
		addRimeCommonModelParams(params, t, includeHTTPOnly)
		if t.speedAlpha != nil {
			params["speedAlpha"] = *t.speedAlpha
		}
		if t.pauseBetweenBrackets != nil {
			params["pauseBetweenBrackets"] = *t.pauseBetweenBrackets
		}
		if t.phonemizeBetweenBrackets != nil {
			params["phonemizeBetweenBrackets"] = *t.phonemizeBetweenBrackets
		}
		if includeHTTPOnly && t.model == "mistv2" && t.reduceLatency != nil {
			params["reduceLatency"] = *t.reduceLatency
		}
	}
}

func addRimeCommonModelParams(params map[string]interface{}, t *RimeTTS, includeHTTPOnly bool) {
	if t.lang != "" || t.langSet {
		params["lang"] = t.lang
	}
	if includeHTTPOnly && t.requestSampleRateSet {
		params["samplingRate"] = t.requestSampleRate
	}
	if t.timeScaleFactor != nil {
		params["timeScaleFactor"] = *t.timeScaleFactor
	}
}

func addRimeModelQueryParams(query url.Values, t *RimeTTS) {
	params := map[string]interface{}{}
	addRimeModelParams(params, t, false)
	for key, value := range params {
		query.Set(key, fmt.Sprint(value))
	}
}

func buildRimeTTSWebsocketHeaders(t *RimeTTS) http.Header {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+t.apiKey)
	return header
}

func buildRimeTTSTextMessage(contextID string, text string) ([]byte, error) {
	if !strings.HasSuffix(text, " ") {
		text += " "
	}
	return json.Marshal(map[string]interface{}{
		"text":      text,
		"contextId": contextID,
	})
}

func buildRimeTTSFlushMessage(contextID string) ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"operation": "flush",
		"contextId": contextID,
	})
}

func buildRimeTTSEOSMessage() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"operation": "eos",
	})
}

func newRimeTTSSentenceTokenizer() tokenize.SentenceTokenizer {
	return blingfire.NewSentenceTokenizer("", 20, 10)
}

type rimeTTSChunkedStream struct {
	resp         *http.Response
	ctx          context.Context
	cancel       context.CancelFunc
	text         string
	provider     *RimeTTS
	opts         RimeTTS
	sampleRate   int
	requestID    string
	requested    bool
	audioSeen    bool
	pendingPCM   []byte
	pendingFinal bool
	pendingErr   error
	finalSent    bool
}

func (s *rimeTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.finalSent {
		return nil, io.EOF
	}
	if err := s.ensureResponse(); err != nil {
		return nil, err
	}
	if s.pendingFinal {
		s.pendingFinal = false
		s.finalSent = true
		s.closeResponseBody()
		return s.annotateAudio(&tts.SynthesizedAudio{IsFinal: true}), nil
	}
	if s.pendingErr != nil {
		err := s.pendingErr
		s.pendingErr = nil
		s.closeResponseBody()
		return nil, err
	}
	if s.resp == nil || s.resp.Body == nil {
		return nil, io.EOF
	}
	for {
		buf := make([]byte, 4096)
		n, err := s.resp.Body.Read(buf)
		if s.finalSent {
			s.pendingPCM = nil
			return nil, io.EOF
		}
		if n > 0 {
			if err == io.EOF {
				s.pendingFinal = true
			} else if err != nil {
				s.pendingErr = rimeTTSReadBodyError(err)
			}
			frameData := rimeTTSPCMFrameData(&s.pendingPCM, buf[:n])
			if len(frameData) == 0 {
				if s.pendingFinal {
					s.pendingPCM = nil
					s.pendingFinal = false
					s.finalSent = true
					if !s.audioSeen {
						return nil, s.noAudioError()
					}
					s.closeResponseBody()
					return s.annotateAudio(&tts.SynthesizedAudio{IsFinal: true}), nil
				}
				if s.pendingErr != nil {
					err := s.pendingErr
					s.pendingErr = nil
					s.closeResponseBody()
					return nil, err
				}
				continue
			}
			s.audioSeen = true
			return s.annotateAudio(&tts.SynthesizedAudio{
				Frame: &model.AudioFrame{
					Data:              frameData,
					SampleRate:        uint32(s.sampleRate),
					NumChannels:       1,
					SamplesPerChannel: uint32(len(frameData) / 2),
				},
			}), nil
		}
		if err != nil {
			if err == io.EOF {
				s.pendingPCM = nil
				if !s.finalSent {
					s.finalSent = true
					if !s.audioSeen {
						return nil, s.noAudioError()
					}
					s.closeResponseBody()
					return s.annotateAudio(&tts.SynthesizedAudio{IsFinal: true}), nil
				}
				return nil, io.EOF
			}
			if s.finalSent {
				return nil, io.EOF
			}
			readErr := rimeTTSReadBodyError(err)
			s.closeResponseBody()
			return nil, readErr
		}
	}
}

func (s *rimeTTSChunkedStream) noAudioError() error {
	s.closeResponseBody()
	if strings.TrimSpace(s.text) == "" {
		return io.EOF
	}
	return llm.NewAPIError(fmt.Sprintf("no audio frames were pushed for text: %s", s.text), nil, true)
}

func (s *rimeTTSChunkedStream) closeResponseBody() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.resp == nil || s.resp.Body == nil {
		return
	}
	_ = s.resp.Body.Close()
	s.resp = nil
}

func rimeTTSReadBodyError(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return llm.NewAPITimeoutError(err.Error())
	}
	var timeoutErr interface{ Timeout() bool }
	if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
		return llm.NewAPITimeoutError(err.Error())
	}
	return rimeTTSConnectionError("Rime TTS stream read failed", err)
}

func rimeTTSPCMFrameData(pending *[]byte, data []byte) []byte {
	if len(*pending) > 0 {
		combined := make([]byte, 0, len(*pending)+len(data))
		combined = append(combined, (*pending)...)
		combined = append(combined, data...)
		data = combined
		*pending = nil
	}
	if len(data)%2 != 0 {
		*pending = append((*pending)[:0], data[len(data)-1])
		data = data[:len(data)-1]
	}
	if len(data) == 0 {
		return nil
	}
	return bytes.Clone(data)
}

func (s *rimeTTSChunkedStream) annotateAudio(audio *tts.SynthesizedAudio) *tts.SynthesizedAudio {
	if audio == nil {
		return nil
	}
	if s.requestID == "" {
		s.requestID = cavosmath.ShortUUID("")
	}
	audio.RequestID = s.requestID
	audio.SegmentID = ""
	return audio
}

func (s *rimeTTSChunkedStream) ensureResponse() error {
	if s.resp != nil || s.requested {
		return nil
	}
	s.requested = true
	if s.provider != nil {
		s.provider.mu.Lock()
		s.opts.baseURL = s.provider.baseURL
		rimeTTSApplyLazyHTTPOptionUpdates(&s.opts, s.provider)
		timeout := rimeTTSTotalTimeout(s.provider.model)
		s.provider.mu.Unlock()
		requestCtx, cancel := context.WithTimeout(s.ctx, timeout)
		return s.openResponse(requestCtx, cancel)
	}
	requestCtx, cancel := context.WithTimeout(s.ctx, rimeTTSTotalTimeout(s.opts.model))
	return s.openResponse(requestCtx, cancel)
}

func rimeTTSApplyLazyHTTPOptionUpdates(opts *RimeTTS, provider *RimeTTS) {
	if opts == nil || provider == nil || opts.model != provider.model {
		return
	}
	opts.lang = provider.lang
	opts.langSet = provider.langSet
	opts.requestSampleRate = provider.requestSampleRate
	opts.requestSampleRateSet = provider.requestSampleRateSet
	opts.timeScaleFactor = provider.timeScaleFactor

	switch {
	case opts.model == "arcana":
		opts.repetitionPenalty = provider.repetitionPenalty
		opts.temperature = provider.temperature
		opts.topP = provider.topP
		opts.maxTokens = provider.maxTokens
	case opts.model == "coda":
		opts.maxTokens = provider.maxTokens
	case strings.Contains(opts.model, "mist"):
		opts.speedAlpha = provider.speedAlpha
		opts.reduceLatency = provider.reduceLatency
		opts.pauseBetweenBrackets = provider.pauseBetweenBrackets
		opts.phonemizeBetweenBrackets = provider.phonemizeBetweenBrackets
	}
}

func (s *rimeTTSChunkedStream) openResponse(requestCtx context.Context, cancel context.CancelFunc) error {
	req, err := buildRimeTTSRequest(requestCtx, &s.opts, s.text)
	if err != nil {
		cancel()
		return err
	}

	s.cancel = cancel
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		if s.finalSent {
			return io.EOF
		}
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return llm.NewAPITimeoutError(err.Error())
		}
		return rimeTTSConnectionError("Rime TTS request failed", err)
	}
	if s.finalSent {
		resp.Body.Close()
		cancel()
		return io.EOF
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		if resp.StatusCode == 499 {
			resp.Body.Close()
			cancel()
			return nil
		}
		resp.Body.Close()
		cancel()
		return llm.NewAPIStatusError(rimeHTTPStatusReason(resp.StatusCode, resp.Status), resp.StatusCode, "", nil)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "audio") {
		resp.Body.Close()
		cancel()
		if strings.TrimSpace(s.text) != "" {
			s.pendingErr = llm.NewAPIError(fmt.Sprintf("no audio frames were pushed for text: %s", s.text), nil, true)
		}
		return nil
	}

	s.resp = resp
	s.cancel = cancel
	return nil
}

func (s *rimeTTSChunkedStream) Close() error {
	s.finalSent = true
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.resp == nil || s.resp.Body == nil {
		return nil
	}
	body := s.resp.Body
	s.resp = nil
	return body.Close()
}

func rimeTTSTotalTimeout(model string) time.Duration {
	if model == "arcana" || model == "coda" {
		return rimeArcanaModelTimeout
	}
	return rimeMistModelTimeout
}

type rimeTTSSynthesizeStream struct {
	conn                  *websocket.Conn
	ctx                   context.Context
	cancel                context.CancelFunc
	provider              *RimeTTS
	requestID             string
	contextID             string
	websocketURL          string
	poolGeneration        uint64
	responseTimeout       time.Duration
	events                chan *tts.SynthesizedAudio
	errCh                 chan error
	mu                    sync.Mutex
	closed                bool
	started               bool
	readStarted           bool
	pendingText           string
	pushedText            string
	sentenceTokenizer     tokenize.SentenceTokenizer
	audioSeen             bool
	pendingPCM            []byte
	pendingTranscriptText string
	pendingTranscript     []tts.TimedString
	segmentDone           bool
	inputEnded            bool

	writeMessage func(int, []byte) error
	readMessage  func() (int, []byte, error)
	closeConn    func() error
}

func (s *rimeTTSSynthesizeStream) PushText(text string) error {
	if text == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputEnded {
		return nil
	}
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.segmentDone {
		return nil
	}
	s.pendingText += text
	if err := s.sendCompleteSentencesLocked(); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

func (s *rimeTTSSynthesizeStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputEnded {
		return nil
	}
	if s.closed {
		return io.ErrClosedPipe
	}
	return s.flushLocked(false)
}

func (s *rimeTTSSynthesizeStream) EndInput() error {
	s.mu.Lock()
	if s.inputEnded {
		s.mu.Unlock()
		return nil
	}
	if s.closed {
		s.mu.Unlock()
		return io.ErrClosedPipe
	}
	if err := s.flushLocked(true); err != nil {
		s.mu.Unlock()
		return err
	}
	s.inputEnded = true
	if !s.started {
		s.closed = true
		s.cancel()
		if s.events != nil {
			close(s.events)
		}
		if s.provider != nil {
			s.provider.unregisterStream(s)
		}
		if s.provider != nil && s.conn != nil {
			conn := s.conn
			s.conn = nil
			websocketURL := s.websocketURL
			poolGeneration := s.poolGeneration
			s.mu.Unlock()
			s.provider.cachePrewarmedConn(conn, websocketURL, poolGeneration)
			return nil
		}
		err := s.closeConnection()
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	return nil
}

func (s *rimeTTSSynthesizeStream) flushLocked(sendProviderFlush bool) error {
	if s.pendingText != "" {
		text := strings.Join(s.tokenizerLocked().Tokenize(s.pendingText, ""), " ")
		s.pendingText = ""
		if err := s.sendSentenceLocked(text); err != nil {
			s.closeAfterWriteFailureLocked()
			return err
		}
	}
	if !s.started {
		return nil
	}
	if !sendProviderFlush {
		s.segmentDone = true
		return nil
	}
	message, err := buildRimeTTSFlushMessage(s.contextID)
	if err != nil {
		return err
	}
	if err := s.writeMessageData(websocket.TextMessage, message); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

func (s *rimeTTSSynthesizeStream) sendCompleteSentencesLocked() error {
	for {
		tokens := s.tokenizerLocked().Tokenize(s.pendingText, "")
		if len(tokens) <= 1 {
			return nil
		}
		sentence := tokens[0]
		if err := s.sendSentenceLocked(sentence); err != nil {
			return err
		}
		tokenIdx := strings.Index(s.pendingText, sentence)
		if tokenIdx < 0 {
			s.pendingText = strings.TrimSpace(strings.TrimPrefix(s.pendingText, sentence))
			continue
		}
		s.pendingText = strings.TrimLeftFunc(s.pendingText[tokenIdx+len(sentence):], func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\n' || r == '\r'
		})
	}
}

func (s *rimeTTSSynthesizeStream) tokenizerLocked() tokenize.SentenceTokenizer {
	if s.sentenceTokenizer != nil {
		return s.sentenceTokenizer
	}
	return newRimeTTSSentenceTokenizer()
}

func (s *rimeTTSSynthesizeStream) sendSentenceLocked(text string) error {
	if text == "" {
		return nil
	}
	message, err := buildRimeTTSTextMessage(s.contextID, text)
	if err != nil {
		return err
	}
	if err := s.writeMessageData(websocket.TextMessage, message); err != nil {
		return err
	}
	if s.pushedText != "" {
		s.pushedText += " "
	}
	s.pushedText += text
	s.started = true
	s.startReadLoopLocked()
	return nil
}

func (s *rimeTTSSynthesizeStream) startReadLoopLocked() {
	if s.readStarted || s.conn == nil || s.events == nil || s.errCh == nil {
		return
	}
	s.readStarted = true
	go s.readLoop()
}

func (s *rimeTTSSynthesizeStream) Close() error {
	return s.close(false)
}

func (s *rimeTTSSynthesizeStream) closeFromProvider() error {
	return s.close(true)
}

func (s *rimeTTSSynthesizeStream) close(sendEOS bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	defer func() {
		if s.provider != nil {
			s.provider.unregisterStream(s)
		}
	}()
	if sendEOS {
		message, err := buildRimeTTSEOSMessage()
		if err != nil {
			return err
		}
		if err := s.writeMessageData(websocket.TextMessage, message); err != nil {
			if closeErr := s.closeConnection(); closeErr != nil {
				return closeErr
			}
			return err
		}
		s.waitForEOSAckLocked()
	}
	if s.conn != nil {
		_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	}
	if !sendEOS {
		_ = s.closeConnection()
		return nil
	}
	return s.closeConnection()
}

func (s *rimeTTSSynthesizeStream) waitForEOSAckLocked() {
	if s.readStarted {
		return
	}
	if s.conn != nil {
		_ = s.conn.SetReadDeadline(time.Now().Add(time.Second))
	}
	_, _, _ = s.readMessageData()
}

func (s *rimeTTSSynthesizeStream) writeMessageData(messageType int, data []byte) error {
	if s.writeMessage != nil {
		return s.writeMessage(messageType, data)
	}
	return s.writeWebsocketMessage(messageType, data)
}

func (s *rimeTTSSynthesizeStream) readMessageData() (int, []byte, error) {
	if s.readMessage != nil {
		return s.readMessage()
	}
	return s.readWebsocketMessage()
}

func (s *rimeTTSSynthesizeStream) writeWebsocketMessage(messageType int, data []byte) error {
	if s.conn == nil {
		return io.ErrClosedPipe
	}
	return s.conn.WriteMessage(messageType, data)
}

func (s *rimeTTSSynthesizeStream) readWebsocketMessage() (int, []byte, error) {
	if s.conn == nil {
		return 0, nil, io.ErrClosedPipe
	}
	return s.conn.ReadMessage()
}

func (s *rimeTTSSynthesizeStream) closeConnection() error {
	if s.closeConn != nil {
		return s.closeConn()
	}
	return s.closeWebsocketConn()
}

func (s *rimeTTSSynthesizeStream) closeWebsocketConn() error {
	if s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

func (s *rimeTTSSynthesizeStream) closeAfterWriteFailureLocked() {
	if s.closed {
		return
	}
	s.closed = true
	s.cancel()
	_ = s.closeConnection()
	if s.provider != nil {
		s.provider.unregisterStream(s)
	}
}

func (s *rimeTTSSynthesizeStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *rimeTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
	if s.isClosed() {
		return nil, io.EOF
	}
	select {
	case audio, ok := <-s.events:
		if ok {
			return audio, nil
		}
		select {
		case err := <-s.errCh:
			return nil, err
		default:
			return nil, io.EOF
		}
	default:
	}
	select {
	case audio, ok := <-s.events:
		if !ok {
			select {
			case err := <-s.errCh:
				return nil, err
			default:
				return nil, io.EOF
			}
		}
		return audio, nil
	case err := <-s.errCh:
		return nil, err
	case <-s.ctx.Done():
		if s.isClosed() {
			return nil, io.EOF
		}
		return nil, s.ctx.Err()
	}
}

func (s *rimeTTSSynthesizeStream) readLoop() {
	defer close(s.events)
	for {
		if s.responseTimeout > 0 && s.conn != nil {
			_ = s.conn.SetReadDeadline(time.Now().Add(s.responseTimeout))
		}
		msgType, payload, err := s.readMessageData()
		if err != nil {
			if !s.isClosed() {
				s.dropConnectionAfterProviderError()
				s.errCh <- rimeTTSReadErrorWithRequestID(err, s.requestID)
			}
			return
		}
		if msgType != websocket.TextMessage {
			continue
		}
		audio, done, transcript, err := rimeTTSAudioFromWebsocketMessage(payload, s.provider.sampleRate)
		if err != nil {
			s.dropConnectionAfterProviderError()
			s.errCh <- err
			return
		}
		if audio != nil && audio.Frame != nil {
			frameData := rimeTTSPCMFrameData(&s.pendingPCM, audio.Frame.Data)
			if len(frameData) == 0 {
				audio = nil
			} else {
				audio.Frame.Data = frameData
				audio.Frame.SamplesPerChannel = uint32(len(frameData) / 2)
			}
		}
		if done {
			s.pendingPCM = nil
		}
		hasAudio := audio != nil && audio.Frame != nil && len(audio.Frame.Data) > 0
		s.mu.Lock()
		if hasAudio {
			s.audioSeen = true
		}
		audioSeen := s.audioSeen
		pushedText := s.pushedText
		s.mu.Unlock()
		if done && !audioSeen && strings.TrimSpace(pushedText) != "" {
			s.dropConnectionAfterProviderError()
			s.errCh <- llm.NewAPIError(fmt.Sprintf("no audio frames were pushed for text: %s", pushedText), nil, true)
			return
		}
		if audio != nil && audio.Frame == nil && len(audio.TimedTranscript) > 0 {
			s.pendingTranscriptText += audio.DeltaText
			s.pendingTranscript = append(s.pendingTranscript, audio.TimedTranscript...)
			audio = nil
		}
		if audio != nil {
			if s.pendingTranscriptText != "" && audio.DeltaText == "" {
				audio.DeltaText = s.pendingTranscriptText
				s.pendingTranscriptText = ""
			}
			if len(s.pendingTranscript) > 0 && len(audio.TimedTranscript) == 0 {
				audio.TimedTranscript = append(audio.TimedTranscript, s.pendingTranscript...)
				s.pendingTranscript = nil
			}
			s.annotateAudio(audio)
			if !s.sendAudio(audio) {
				return
			}
		}
		if transcript != "" {
			audio := &tts.SynthesizedAudio{DeltaText: transcript}
			s.annotateAudio(audio)
			if !s.sendAudio(audio) {
				return
			}
		}
		if done {
			s.releaseConnectionAfterDone()
			return
		}
	}
}

func (s *rimeTTSSynthesizeStream) sendAudio(audio *tts.SynthesizedAudio) bool {
	if s.events == nil {
		return false
	}
	if s.ctx == nil {
		s.events <- audio
		return true
	}
	select {
	case s.events <- audio:
		return true
	case <-s.ctx.Done():
		return false
	}
}

func (s *rimeTTSSynthesizeStream) dropConnectionAfterProviderError() {
	s.mu.Lock()
	conn := s.conn
	s.conn = nil
	s.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	if s.provider != nil {
		s.provider.unregisterStream(s)
	}
}

func (s *rimeTTSSynthesizeStream) releaseConnectionAfterDone() {
	if s.provider == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	conn := s.conn
	s.conn = nil
	websocketURL := s.websocketURL
	poolGeneration := s.poolGeneration
	s.mu.Unlock()
	s.provider.cachePrewarmedConn(conn, websocketURL, poolGeneration)
	s.provider.unregisterStream(s)
}

func (s *rimeTTSSynthesizeStream) annotateAudio(audio *tts.SynthesizedAudio) {
	if audio == nil {
		return
	}
	audio.RequestID = s.requestID
	audio.SegmentID = s.contextID
}

func rimeTTSReadError(err error) error {
	return rimeTTSReadErrorWithRequestID(err, "")
}

func rimeTTSReadErrorWithRequestID(err error, requestID string) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return llm.NewAPITimeoutError(err.Error())
	}
	var timeoutErr interface{ Timeout() bool }
	if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
		return llm.NewAPITimeoutError(err.Error())
	}
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return llm.NewAPIStatusError("Rime ws closed unexpectedly", 0, requestID, nil)
	}
	return llm.NewAPIConnectionError(fmt.Sprintf("Rime WS error: %v", err))
}

func rimeTTSAudioFromWebsocketMessage(payload []byte, sampleRate int) (*tts.SynthesizedAudio, bool, string, error) {
	if bytes.Equal(bytes.TrimSpace(payload), []byte("null")) {
		return nil, false, "", rimeTTSConnectionError("Rime websocket payload decode failed", fmt.Errorf("expected JSON object, got null"))
	}
	var message struct {
		Type           json.RawMessage `json:"type"`
		Data           *string         `json:"data"`
		Message        json.RawMessage `json:"message"`
		WordTimestamps json.RawMessage `json:"word_timestamps"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, "", rimeTTSConnectionError("Rime websocket payload decode failed", err)
	}
	switch rimeTTSWebsocketMessageType(message.Type) {
	case "chunk":
		if message.Data == nil {
			return nil, false, "", rimeTTSConnectionError("Rime websocket chunk missing data", nil)
		}
		if *message.Data == "" {
			return nil, false, "", nil
		}
		audio, err := rimeDecodeBase64Chunk(*message.Data)
		if err != nil {
			return nil, false, "", rimeTTSConnectionError("Rime websocket audio decode failed", err)
		}
		if len(audio) == 0 {
			return nil, false, "", nil
		}
		return rimeTTSAudioFrame(audio, sampleRate), false, "", nil
	case "timestamps":
		wordTimestamps, err := rimeTTSWordTimestamps(message.WordTimestamps)
		if err != nil {
			return nil, false, "", rimeTTSConnectionError("Rime websocket timestamp decode failed", err)
		}
		words, err := rimeTTSTimestampWords(wordTimestamps.Words)
		if err != nil {
			return nil, false, "", rimeTTSConnectionError("Rime websocket timestamp decode failed", err)
		}
		starts, err := rimeTTSTimestampTimes(wordTimestamps.Start)
		if err != nil {
			return nil, false, "", rimeTTSConnectionError("Rime websocket timestamp decode failed", err)
		}
		ends, err := rimeTTSTimestampTimes(wordTimestamps.End)
		if err != nil {
			return nil, false, "", rimeTTSConnectionError("Rime websocket timestamp decode failed", err)
		}
		timed := rimeTTSTimedTranscript(words, starts, ends)
		if len(timed) == 0 {
			return nil, false, "", nil
		}
		return &tts.SynthesizedAudio{
			DeltaText:       rimeTTSTimedTranscriptText(timed),
			TimedTranscript: timed,
		}, false, "", nil
	case "done":
		return &tts.SynthesizedAudio{IsFinal: true}, true, "", nil
	case "error":
		return nil, false, "", llm.NewAPIError("Rime ws error: "+rimeTTSWebsocketErrorMessage(message.Message), nil, true)
	default:
		return nil, false, "", nil
	}
}

func rimeDecodeBase64Chunk(data string) ([]byte, error) {
	clean := make([]byte, 0, len(data))
	dataChars := 0
	for i := 0; i < len(data); i++ {
		b := data[i]
		switch {
		case b >= 0x80:
			return nil, fmt.Errorf("string argument should contain only ASCII characters")
		case b >= 'A' && b <= 'Z',
			b >= 'a' && b <= 'z',
			b >= '0' && b <= '9',
			b == '+',
			b == '/':
			clean = append(clean, b)
			dataChars++
		case b == '=':
			clean = append(clean, b)
		}
	}
	if dataChars == 0 {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(string(clean))
}

func rimeTTSConnectionError(message string, err error) *llm.APIConnectionError {
	if err == nil {
		return llm.NewAPIConnectionError(message)
	}
	return llm.NewAPIConnectionError(fmt.Sprintf("%s: %v", message, err))
}

func rimeTTSWebsocketMessageType(raw json.RawMessage) string {
	var messageType string
	if err := json.Unmarshal(raw, &messageType); err != nil {
		return ""
	}
	return messageType
}

func rimeTTSWebsocketErrorMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "(no message)"
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "None"
	}
	var boolValue bool
	if err := json.Unmarshal(raw, &boolValue); err == nil {
		if boolValue {
			return "True"
		}
		return "False"
	}
	if repr, ok := rimeTTSPythonJSONRepr(raw); ok {
		return repr
	}
	return string(raw)
}

type rimeTTSOrderedObject []struct {
	key   string
	value any
}

func rimeTTSPythonJSONRepr(raw json.RawMessage) (string, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := rimeTTSDecodeOrderedJSONValue(decoder)
	if err != nil {
		return "", false
	}
	return rimeTTSPythonRepr(value), true
}

func rimeTTSDecodeOrderedJSONValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delim {
	case '{':
		var object rimeTTSOrderedObject
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("invalid JSON object key")
			}
			value, err := rimeTTSDecodeOrderedJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			object = append(object, struct {
				key   string
				value any
			}{key: key, value: value})
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return object, nil
	case '[':
		var items []any
		for decoder.More() {
			value, err := rimeTTSDecodeOrderedJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			items = append(items, value)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return items, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
}

func rimeTTSPythonRepr(value any) string {
	switch typed := value.(type) {
	case nil:
		return "None"
	case string:
		return rimeTTSPythonStringRepr(typed)
	case bool:
		if typed {
			return "True"
		}
		return "False"
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			parts = append(parts, rimeTTSPythonRepr(item))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case rimeTTSOrderedObject:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			parts = append(parts, rimeTTSPythonRepr(item.key)+": "+rimeTTSPythonRepr(item.value))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, rimeTTSPythonRepr(key)+": "+rimeTTSPythonRepr(typed[key]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		return fmt.Sprint(value)
	}
}

func rimeTTSPythonStringRepr(value string) string {
	if strings.Contains(value, "'") && !strings.Contains(value, `"`) {
		return strconv.Quote(value)
	}
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`'`, `\'`,
		"\n", `\n`,
		"\r", `\r`,
		"\t", `\t`,
	)
	return "'" + replacer.Replace(value) + "'"
}

type rimeTTSWordTimestampsPayload struct {
	Words json.RawMessage `json:"words"`
	Start json.RawMessage `json:"start"`
	End   json.RawMessage `json:"end"`
}

func rimeTTSWordTimestamps(raw json.RawMessage) (rimeTTSWordTimestampsPayload, error) {
	if len(raw) == 0 || rimeTTSJSONNullOrFalsey(raw) {
		return rimeTTSWordTimestampsPayload{}, nil
	}
	var payload rimeTTSWordTimestampsPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return rimeTTSWordTimestampsPayload{}, err
	}
	return payload, nil
}

func rimeTTSTimestampWords(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || rimeTTSJSONNullOrFalsey(raw) {
		return nil, nil
	}
	var words []string
	if err := json.Unmarshal(raw, &words); err == nil {
		return words, nil
	}
	var wordText string
	if err := json.Unmarshal(raw, &wordText); err != nil {
		keys, keyErr := rimeTTSJSONRawObjectKeys(raw)
		if keyErr == nil {
			return keys, nil
		}
		return nil, err
	}
	words = make([]string, 0, len(wordText))
	for _, r := range wordText {
		words = append(words, string(r))
	}
	return words, nil
}

func rimeTTSJSONRawObjectKeys(raw json.RawMessage) ([]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if token != json.Delim('{') {
		return nil, fmt.Errorf("expected JSON object")
	}
	var keys []string
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("invalid JSON object key")
		}
		var discard any
		if err := decoder.Decode(&discard); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	return keys, nil
}

func rimeTTSTimestampTimes(raw json.RawMessage) ([]float64, error) {
	if len(raw) == 0 || rimeTTSJSONNullOrFalsey(raw) {
		return nil, nil
	}
	var times []float64
	if err := json.Unmarshal(raw, &times); err != nil {
		return nil, err
	}
	return times, nil
}

func rimeTTSJSONNullOrFalsey(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return bytes.Equal(trimmed, []byte("null")) ||
		bytes.Equal(trimmed, []byte("false")) ||
		bytes.Equal(trimmed, []byte("0")) ||
		bytes.Equal(trimmed, []byte(`""`)) ||
		bytes.Equal(trimmed, []byte("[]")) ||
		bytes.Equal(trimmed, []byte("{}"))
}

func rimeTTSTimedTranscript(words []string, starts []float64, ends []float64) []tts.TimedString {
	count := min(len(words), len(starts), len(ends))
	if count == 0 {
		return nil
	}
	timed := make([]tts.TimedString, 0, count)
	for i := 0; i < count; i++ {
		timed = append(timed, tts.TimedString{
			Text:      words[i] + " ",
			StartTime: starts[i],
			EndTime:   ends[i],
		})
	}
	return timed
}

func rimeTTSTimedTranscriptText(timed []tts.TimedString) string {
	if len(timed) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, word := range timed {
		builder.WriteString(word.Text)
	}
	return builder.String()
}

func rimeTTSAudioFrame(audio []byte, sampleRate int) *tts.SynthesizedAudio {
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              bytes.Clone(audio),
			SampleRate:        uint32(sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(len(audio) / 2),
		},
	}
}
