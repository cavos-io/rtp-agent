package slng

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

const (
	defaultSLNGSTTFallbackRecoveryCooldown = time.Minute
	maxSLNGSTTSilentReconnects             = 3
)

var (
	slngSTTNow               = time.Now
	slngSTTKeepaliveInterval = 5 * time.Second
)

type STT struct {
	mu                       sync.Mutex
	apiKey                   string
	providerAPIKey           string
	model                    string
	endpoint                 string
	modelEndpoints           []string
	connections              []STTConnectionConfig
	candidateState           *candidateState
	fallbackRecoveryCooldown time.Duration
	finalTimeout             time.Duration
	regionOverride           string
	worldPartOverride        string
	externalAgentID          string
	externalSessionID        string
	extraHeaders             http.Header
	sampleRate               int
	bufferSizeSeconds        float64
	encoding                 string
	enablePartialTranscript  bool
	vadThreshold             float64
	vadMinSilenceDurationMS  int
	vadSpeechPadMS           int
	enableDiarization        bool
	minSpeakers              *int
	maxSpeakers              *int
	language                 string
	modelOptions             map[string]any
	streams                  map[*sttStream]struct{}
	closed                   bool
}

type STTOption func(*STT)

func WithSTTBaseURL(baseURL string) STTOption {
	return func(s *STT) {
		if baseURL != "" {
			s.endpoint = defaultSTTEndpoint(strings.TrimRight(baseURL, "/"), s.model)
			s.modelEndpoints = nil
			s.connections = nil
		}
	}
}

func WithSTTModel(modelName string) STTOption {
	return func(s *STT) {
		if modelName != "" {
			s.model = modelName
			s.endpoint = defaultSTTEndpoint(defaultSLNGBaseURL, modelName)
			s.modelEndpoints = nil
			s.connections = nil
		}
	}
}

func WithSTTEndpoint(endpoint string) STTOption {
	return func(s *STT) {
		if endpoint != "" {
			s.endpoint = endpoint
			s.modelEndpoints = []string{endpoint}
			s.connections = nil
			if model := sttModelFromEndpoint(endpoint); model != "" {
				s.model = model
			}
		}
	}
}

func WithSTTModelEndpoints(endpoints ...string) STTOption {
	return func(s *STT) {
		cleaned := make([]string, 0, len(endpoints))
		for _, endpoint := range endpoints {
			if endpoint != "" {
				cleaned = append(cleaned, endpoint)
			}
		}
		if len(cleaned) == 0 {
			return
		}
		s.modelEndpoints = cleaned
		s.connections = nil
		s.endpoint = cleaned[0]
		if model := sttModelFromEndpoint(cleaned[0]); model != "" {
			s.model = model
		}
	}
}

func WithSTTConnections(connections ...STTConnectionConfig) STTOption {
	return func(s *STT) {
		if len(connections) == 0 {
			return
		}
		s.connections = cloneSTTConnectionConfigs(connections)
		s.modelEndpoints = nil
		s.endpoint = connections[0].Endpoint
		if model := sttModelFromEndpoint(connections[0].Endpoint); model != "" {
			s.model = model
		}
	}
}

func WithSTTFallbackRecoveryCooldown(cooldown time.Duration) STTOption {
	return func(s *STT) {
		s.fallbackRecoveryCooldown = max(cooldown, 0)
	}
}

func WithSTTFinalTimeout(timeout time.Duration) STTOption {
	return func(s *STT) {
		s.finalTimeout = max(timeout, 0)
	}
}

func WithSTTRegionOverride(region any) STTOption {
	return func(s *STT) {
		s.regionOverride = normalizeRegionOverride(region)
	}
}

func WithSTTProviderAPIKey(apiKey string) STTOption {
	return func(s *STT) {
		s.providerAPIKey = apiKey
	}
}

func WithSTTWorldPartOverride(worldPart string) STTOption {
	return func(s *STT) {
		s.worldPartOverride = worldPart
	}
}

func WithSTTExternalTracking(agentID, sessionID string) STTOption {
	return func(s *STT) {
		s.externalAgentID = agentID
		s.externalSessionID = sessionID
	}
}

func WithSTTExtraHeaders(headers http.Header) STTOption {
	return func(s *STT) {
		s.extraHeaders = headers.Clone()
	}
}

func WithSTTEncoding(encoding string) STTOption {
	return func(s *STT) {
		if encoding != "" {
			s.encoding = encoding
		}
	}
}

func WithSTTLanguage(language string) STTOption {
	return func(s *STT) {
		if language != "" {
			s.language = language
		}
	}
}

func WithSTTPartialTranscripts(enabled bool) STTOption {
	return func(s *STT) {
		s.enablePartialTranscript = enabled
	}
}

func WithSTTSampleRate(sampleRate int) STTOption {
	return func(s *STT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithSTTBufferSizeSeconds(seconds float64) STTOption {
	return func(s *STT) {
		if seconds > 0 {
			s.bufferSizeSeconds = seconds
		}
	}
}

func WithSTTVADThreshold(threshold float64) STTOption {
	return func(s *STT) {
		s.vadThreshold = threshold
	}
}

func WithSTTVADMinSilenceDurationMS(milliseconds int) STTOption {
	return func(s *STT) {
		s.vadMinSilenceDurationMS = milliseconds
	}
}

func WithSTTVADSpeechPadMS(milliseconds int) STTOption {
	return func(s *STT) {
		s.vadSpeechPadMS = milliseconds
	}
}

func WithSTTDiarization(enabled bool, minSpeakers, maxSpeakers int) STTOption {
	return func(s *STT) {
		s.enableDiarization = enabled
		s.minSpeakers = &minSpeakers
		s.maxSpeakers = &maxSpeakers
	}
}

func WithSTTModelOptions(options map[string]any) STTOption {
	return func(s *STT) {
		s.modelOptions = cloneSLNGMap(options)
	}
}

func NewSTT(apiKey string, opts ...STTOption) *STT {
	if apiKey == "" {
		apiKey = os.Getenv(slngAPIKeyEnv)
	}
	provider := &STT{
		apiKey:                   apiKey,
		model:                    defaultSLNGSTTModel,
		endpoint:                 defaultSTTEndpoint(defaultSLNGBaseURL, defaultSLNGSTTModel),
		sampleRate:               defaultSLNGSTTSampleRate,
		bufferSizeSeconds:        defaultSLNGBufferSeconds,
		encoding:                 defaultSLNGSTTEncoding,
		enablePartialTranscript:  true,
		vadThreshold:             defaultSLNGVADThreshold,
		vadMinSilenceDurationMS:  defaultSLNGVADMinSilenceMS,
		vadSpeechPadMS:           defaultSLNGVADSpeechPadMS,
		language:                 defaultSLNGLanguage,
		fallbackRecoveryCooldown: defaultSLNGSTTFallbackRecoveryCooldown,
	}
	for _, opt := range opts {
		opt(provider)
	}
	provider.candidateState = newCandidateState(provider.sttCandidateCount(), provider.fallbackRecoveryCooldown)
	return provider
}

func (s *STT) Label() string { return "slng.STT" }
func (s *STT) Model() string { return "slng" }
func (s *STT) Provider() string {
	return "SLNG"
}
func (s *STT) Capabilities() stt.STTCapabilities {
	streaming := strings.HasPrefix(s.endpoint, "ws://") || strings.HasPrefix(s.endpoint, "wss://")
	return stt.STTCapabilities{
		Streaming:        streaming,
		InterimResults:   streaming,
		OfflineRecognize: !streaming,
		Diarization:      s.enableDiarization,
	}
}

func (s *STT) UpdateOptions(opts ...STTOption) {
	s.mu.Lock()
	before := slngSTTActiveOptions{
		language:                s.language,
		partials:                s.enablePartialTranscript,
		bufferSizeSeconds:       s.bufferSizeSeconds,
		diarization:             s.enableDiarization,
		vadThreshold:            s.vadThreshold,
		vadMinSilenceDurationMS: s.vadMinSilenceDurationMS,
		vadSpeechPadMS:          s.vadSpeechPadMS,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	after := slngSTTActiveOptions{
		language:                s.language,
		partials:                s.enablePartialTranscript,
		bufferSizeSeconds:       s.bufferSizeSeconds,
		diarization:             s.enableDiarization,
		vadThreshold:            s.vadThreshold,
		vadMinSilenceDurationMS: s.vadMinSilenceDurationMS,
		vadSpeechPadMS:          s.vadSpeechPadMS,
	}
	streams := make([]*sttStream, 0, len(s.streams))
	if before != after {
		for stream := range s.streams {
			streams = append(streams, stream)
		}
	}
	s.mu.Unlock()

	for _, stream := range streams {
		stream.updateOptions(after)
	}
}

type slngSTTActiveOptions struct {
	language                string
	partials                bool
	bufferSizeSeconds       float64
	diarization             bool
	vadThreshold            float64
	vadMinSilenceDurationMS int
	vadSpeechPadMS          int
}

func (s *STT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	if err := s.requireAPIKey(); err != nil {
		return nil, err
	}
	var audio bytes.Buffer
	for _, frame := range frames {
		if frame != nil {
			audio.Write(frame.Data)
		}
	}
	payload := map[string]any{
		"audio_b64": base64.StdEncoding.EncodeToString(audio.Bytes()),
		"language":  s.resolveLanguage(language),
	}
	for key, value := range s.modelOptions {
		payload[key] = value
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	headers, err := buildSTTWebsocketHeaders(s)
	if err != nil {
		return nil, err
	}
	req.Header = headers
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, slngStatusError(map[string]any{
			"status_code": resp.StatusCode,
			"message":     strings.TrimSpace(string(respBody)),
		})
	}
	var result struct {
		Text     string `json:"text"`
		Language string `json:"language"`
		Segments []struct {
			Start float64 `json:"start"`
			End   float64 `json:"end"`
		} `json:"segments"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	language = result.Language
	if language == "" {
		language = s.resolveLanguage("")
	}
	start, end := 0.0, 0.0
	if len(result.Segments) > 0 {
		start = result.Segments[0].Start
		end = result.Segments[len(result.Segments)-1].End
	}
	return &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{{
			Language:   language,
			Text:       result.Text,
			Confidence: 1.0,
			StartTime:  start,
			EndTime:    end,
		}},
	}, nil
}

func (s *STT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if s.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if err := s.requireAPIKey(); err != nil {
		return nil, err
	}
	candidates, err := s.resolvedSTTCandidates()
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, errors.New("slng stt websocket endpoint is empty")
	}

	s.mu.Lock()
	if s.candidateState == nil || s.candidateState.count != len(candidates) {
		s.candidateState = newCandidateState(len(candidates), s.fallbackRecoveryCooldown)
	}
	candidateIndex := s.candidateState.start(slngSTTNow())
	s.mu.Unlock()

	var lastErr error
	for candidateIndex >= 0 && candidateIndex < len(candidates) {
		candidate := candidates[candidateIndex]
		attempt := s.sttAttempt(candidate)
		var candidateErr error
		headers, err := buildSTTWebsocketHeadersForCandidate(&attempt, candidate.headers)
		if err != nil {
			return nil, err
		}
		conn, response, err := websocket.DefaultDialer.DialContext(ctx, candidate.endpoint, headers)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil, context.Canceled
			}
			candidateErr = slngSTTDialError(response, err)
		} else {
			initPayload := buildSTTInitPayload(&attempt)
			if candidate.init != nil {
				initPayload, err = json.Marshal(candidate.init)
			}
			if err == nil {
				err = conn.WriteMessage(websocket.TextMessage, initPayload)
			}
			if err != nil {
				_ = conn.Close()
				candidateErr = llm.NewAPIConnectionError(fmt.Sprintf("failed to initialize slng stt websocket: %v", err))
			}
		}

		if candidateErr != nil {
			lastErr = candidateErr
			if !isSLNGFallbackEligible(candidateErr) {
				return nil, candidateErr
			}
			s.mu.Lock()
			nextIndex, ok := s.candidateState.advance(candidateIndex, slngSTTNow())
			s.mu.Unlock()
			if !ok {
				return nil, lastErr
			}
			candidateIndex = nextIndex
			continue
		}

		if s.isClosed() {
			_ = conn.Close()
			return nil, io.ErrClosedPipe
		}
		s.mu.Lock()
		s.endpoint = candidate.endpoint
		s.model = candidate.model
		s.candidateState.selectCandidate(candidateIndex)
		s.mu.Unlock()
		stream := &sttStream{
			ctx:                     ctx,
			provider:                s,
			conn:                    conn,
			candidateIndex:          candidateIndex,
			legacyEndpoint:          candidate.legacyEndpoint,
			language:                attempt.resolveLanguage(language),
			partials:                attempt.enablePartialTranscript,
			sampleRate:              attempt.sampleRate,
			bufferSizeSeconds:       attempt.bufferSizeSeconds,
			encoding:                attempt.encoding,
			diarization:             attempt.enableDiarization,
			vadThreshold:            attempt.vadThreshold,
			vadMinSilenceDurationMS: attempt.vadMinSilenceDurationMS,
			vadSpeechPadMS:          attempt.vadSpeechPadMS,
			finalTimeout:            attempt.finalTimeout,
			lifecycleDone:           make(chan struct{}),
		}
		if !s.registerStream(stream) {
			stream.Close()
			return nil, io.ErrClosedPipe
		}
		go stream.runLifecycle()
		return stream, nil
	}
	return nil, lastErr
}

func (s *STT) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	streams := make([]*sttStream, 0, len(s.streams))
	for stream := range s.streams {
		streams = append(streams, stream)
	}
	s.mu.Unlock()

	var firstErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *STT) isClosed() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *STT) registerStream(stream *sttStream) bool {
	if stream == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	if s.streams == nil {
		s.streams = make(map[*sttStream]struct{})
	}
	stream.provider = s
	s.streams[stream] = struct{}{}
	return true
}

func (s *STT) unregisterStream(stream *sttStream) {
	if stream == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.streams, stream)
}

func (s *STT) requireAPIKey() error {
	if s.apiKey == "" {
		return fmt.Errorf("api key is required, or set %s environment variable", slngAPIKeyEnv)
	}
	return nil
}

func (s *STT) sttEndpoints() []string {
	if len(s.modelEndpoints) > 0 {
		return s.modelEndpoints
	}
	if s.endpoint == "" {
		return nil
	}
	return []string{s.endpoint}
}

func (s *STT) sttCandidateCount() int {
	if len(s.connections) > 0 {
		return len(s.connections)
	}
	return len(s.sttEndpoints())
}

func (s *STT) resolvedSTTCandidates() ([]sttConnectionCandidate, error) {
	s.mu.Lock()
	connections := cloneSTTConnectionConfigs(s.connections)
	endpoints := append([]string(nil), s.sttEndpoints()...)
	defaultModel := s.model
	s.mu.Unlock()

	if len(connections) > 0 {
		candidates := make([]sttConnectionCandidate, 0, len(connections))
		for _, config := range connections {
			model, err := bridgeModel(config.Endpoint, "stt")
			if err != nil {
				return nil, err
			}
			if config.Model != "" && config.Model != model {
				return nil, errors.New("STT connection model must match its endpoint")
			}
			candidates = append(candidates, sttConnectionCandidate{
				endpoint: config.Endpoint,
				model:    model,
				headers:  config.Headers.Clone(),
				init:     cloneSLNGMap(config.Init),
			})
		}
		return candidates, nil
	}

	candidates := make([]sttConnectionCandidate, 0, len(endpoints))
	for _, endpoint := range endpoints {
		model, err := bridgeModel(endpoint, "stt")
		legacy := err != nil
		if legacy {
			model = extractSTTModelFromEndpoint(endpoint)
		}
		if model == "" {
			model = defaultModel
		}
		candidates = append(candidates, sttConnectionCandidate{
			endpoint:       endpoint,
			model:          model,
			legacyEndpoint: legacy,
		})
	}
	return candidates, nil
}

func (s *STT) sttAttempt(candidate sttConnectionCandidate) STT {
	s.mu.Lock()
	defer s.mu.Unlock()
	attempt := STT{
		apiKey:                  s.apiKey,
		providerAPIKey:          s.providerAPIKey,
		model:                   candidate.model,
		endpoint:                candidate.endpoint,
		regionOverride:          s.regionOverride,
		worldPartOverride:       s.worldPartOverride,
		externalAgentID:         s.externalAgentID,
		externalSessionID:       s.externalSessionID,
		extraHeaders:            s.extraHeaders.Clone(),
		sampleRate:              s.sampleRate,
		bufferSizeSeconds:       s.bufferSizeSeconds,
		encoding:                s.encoding,
		enablePartialTranscript: s.enablePartialTranscript,
		vadThreshold:            s.vadThreshold,
		vadMinSilenceDurationMS: s.vadMinSilenceDurationMS,
		vadSpeechPadMS:          s.vadSpeechPadMS,
		enableDiarization:       s.enableDiarization,
		language:                s.language,
		modelOptions:            cloneSLNGMap(s.modelOptions),
		finalTimeout:            s.finalTimeout,
	}
	if s.minSpeakers != nil {
		value := *s.minSpeakers
		attempt.minSpeakers = &value
	}
	if s.maxSpeakers != nil {
		value := *s.maxSpeakers
		attempt.maxSpeakers = &value
	}
	return attempt
}

func extractSTTModelFromEndpoint(endpoint string) string {
	marker := "/v1/stt/"
	index := strings.Index(endpoint, marker)
	if index < 0 {
		return ""
	}
	model := endpoint[index+len(marker):]
	if query := strings.IndexAny(model, "?#"); query >= 0 {
		model = model[:query]
	}
	return strings.TrimRight(model, "/")
}

func sttModelFromEndpoint(endpoint string) string {
	if model, err := bridgeModel(endpoint, "stt"); err == nil {
		return model
	}
	return extractSTTModelFromEndpoint(endpoint)
}

func (s *STT) resolveLanguage(language string) string {
	if language != "" {
		return language
	}
	return s.language
}

func buildSTTWebsocketHeaders(s *STT) (http.Header, error) {
	return buildSTTWebsocketHeadersForCandidate(s, nil)
}

func buildSTTWebsocketHeadersForCandidate(s *STT, candidate http.Header) (http.Header, error) {
	return (gatewayHeaders{
		APIKey:            s.apiKey,
		ProviderAPIKey:    s.providerAPIKey,
		RegionOverride:    s.regionOverride,
		WorldPartOverride: s.worldPartOverride,
		ExternalAgentID:   s.externalAgentID,
		ExternalSessionID: s.externalSessionID,
		Extra:             s.extraHeaders,
	}).build(candidate)
}

func slngSTTDialError(response *http.Response, err error) error {
	if response == nil {
		return llm.NewAPIConnectionError(fmt.Sprintf("failed to dial slng stt websocket: %v", err))
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = http.StatusText(response.StatusCode)
	}
	return llm.NewAPIStatusError(message, response.StatusCode, response.Header.Get("X-Request-ID"), string(body))
}

func buildSTTInitPayload(s *STT) []byte {
	encoding := s.encoding
	if encoding == "pcm_s16le" {
		encoding = "linear16"
	}
	config := map[string]any{
		"language":                    normalizeLanguageForModel(s.model, s.language, s.modelOptions),
		"sample_rate":                 s.sampleRate,
		"encoding":                    encoding,
		"vad_threshold":               s.vadThreshold,
		"vad_min_silence_duration_ms": s.vadMinSilenceDurationMS,
		"vad_speech_pad_ms":           s.vadSpeechPadMS,
		"enable_diarization":          s.enableDiarization,
		"enable_partials":             s.enablePartialTranscript,
		"enable_partial_transcripts":  s.enablePartialTranscript,
	}
	if s.minSpeakers != nil {
		config["min_speakers"] = *s.minSpeakers
	}
	if s.maxSpeakers != nil {
		config["max_speakers"] = *s.maxSpeakers
	}
	for key, value := range s.modelOptions {
		config[key] = value
	}
	partials := slngOptionDefault(config, "enable_partials", slngOptionDefault(config, "enable_partial_transcripts", s.enablePartialTranscript))
	config["enable_partials"] = partials
	config["enable_partial_transcripts"] = partials

	payload := map[string]any{
		"type":   "init",
		"config": config,
	}
	if ref, err := parseModelRef(s.model); err == nil {
		if model := resolveDeepgramSTTModel(ref); model != "" {
			payload["model"] = model
		}
	}
	data, _ := json.Marshal(payload)
	return data
}
func resolveDeepgramSTTModel(ref modelRef) string {
	if ref.routeProvider != "deepgram" || ref.routeModel != "nova" {
		return ""
	}
	variant := strings.ToLower(ref.variant)
	if strings.HasPrefix(variant, "3-medical") {
		return "nova-3-medical"
	}
	if strings.HasPrefix(variant, "3") {
		return "nova-3"
	}
	if strings.HasPrefix(variant, "2") {
		return "nova-2"
	}
	return ""
}
func sttEventsFromMessage(payload []byte, defaultLanguage string, partials bool) ([]*stt.SpeechEvent, error) {
	events, _, _, err := sttEventsFromMessageWithSpeechState(payload, defaultLanguage, partials, false, 0)
	return events, err
}

func sttEventsFromMessageWithSpeechState(payload []byte, defaultLanguage string, partials bool, speechStarted bool, speechDuration float64) ([]*stt.SpeechEvent, bool, float64, error) {
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, speechStarted, speechDuration, nil
	}
	messageType := slngString(message["type"])
	if messageType == "Results" {
		message = normalizeSLNGResults(message)
		messageType = slngString(message["type"])
	}
	if messageType == "Error" || messageType == "error" {
		return nil, speechStarted, speechDuration, slngStatusError(message)
	}
	if messageType == "partial_transcript" && !partials {
		return nil, speechStarted, speechDuration, nil
	}
	if messageType != "partial_transcript" && messageType != "final_transcript" {
		return nil, speechStarted, speechDuration, nil
	}
	isFinal := messageType == "final_transcript"
	text := slngString(message["transcript"])
	if text == "" {
		if isFinal && speechDuration > 0 {
			return []*stt.SpeechEvent{slngSTTRecognitionUsageEvent(speechDuration)}, speechStarted, 0, nil
		}
		return nil, speechStarted, speechDuration, nil
	}
	eventType := stt.SpeechEventInterimTranscript
	if isFinal {
		eventType = stt.SpeechEventFinalTranscript
	}
	alternative := stt.SpeechData{
		Language:   slngStringDefault(message["language"], defaultLanguage),
		Text:       text,
		Confidence: slngFloat(message["confidence"]),
	}
	if isFinal {
		words := slngSlice(message["words"])
		if len(words) > 0 {
			alternative.StartTime = slngFloat(slngMap(words[0])["start"])
			alternative.EndTime = slngFloat(slngMap(words[len(words)-1])["end"])
		}
	}
	events := []*stt.SpeechEvent{}
	if !speechStarted {
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech})
		speechStarted = true
	}
	events = append(events, &stt.SpeechEvent{
		Type:         eventType,
		Alternatives: []stt.SpeechData{alternative},
	})
	if isFinal {
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech})
		speechStarted = false
		if speechDuration > 0 {
			events = append(events, slngSTTRecognitionUsageEvent(speechDuration))
			speechDuration = 0
		}
	}
	return events, speechStarted, speechDuration, nil
}

func slngSTTRecognitionUsageEvent(audioDuration float64) *stt.SpeechEvent {
	return &stt.SpeechEvent{
		Type:             stt.SpeechEventRecognitionUsage,
		RecognitionUsage: &stt.RecognitionUsage{AudioDuration: audioDuration},
	}
}

func normalizeSLNGResults(message map[string]any) map[string]any {
	channel := slngMap(message["channel"])
	alternatives := slngSlice(channel["alternatives"])
	alt := map[string]any{}
	if len(alternatives) > 0 {
		alt = slngMap(alternatives[0])
	}
	messageType := "partial_transcript"
	if slngBool(message["is_final"]) {
		messageType = "final_transcript"
	}
	return map[string]any{
		"type":       messageType,
		"transcript": alt["transcript"],
		"confidence": alt["confidence"],
		"words":      alt["words"],
		"language":   slngStringDefault(message["language"], slngString(alt["language"])),
	}
}

type sttStream struct {
	mu                      sync.Mutex
	ctx                     context.Context
	provider                *STT
	conn                    *websocket.Conn
	candidateIndex          int
	language                string
	partials                bool
	sampleRate              int
	bufferSizeSeconds       float64
	encoding                string
	diarization             bool
	vadThreshold            float64
	vadMinSilenceDurationMS int
	vadSpeechPadMS          int
	audioBuffer             []byte
	pendingEvents           []*stt.SpeechEvent
	speechStarted           bool
	speechDuration          float64
	reconnectRequested      bool
	legacyEndpoint          bool
	inputEnded              bool
	finalTimeout            time.Duration
	silentReconnects        int
	lifecycleDone           chan struct{}
	lifecycleDoneOnce       sync.Once
	closed                  bool
}

func (s *sttStream) runLifecycle() {
	interval := slngSTTKeepaliveInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		select {
		case <-ctx.Done():
			s.closeFromLifecycle()
			return
		case <-s.lifecycleDone:
			return
		case <-ticker.C:
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				return
			}
			if s.inputEnded || s.reconnectRequested || s.conn == nil {
				s.mu.Unlock()
				continue
			}
			err := s.conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"keepalive"}`))
			if err != nil {
				s.closed = true
				conn := s.conn
				provider := s.provider
				s.mu.Unlock()
				_ = conn.Close()
				if provider != nil {
					provider.unregisterStream(s)
				}
				return
			}
			s.mu.Unlock()
		}
	}
}

func (s *sttStream) closeFromLifecycle() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	conn := s.conn
	provider := s.provider
	s.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	if provider != nil {
		provider.unregisterStream(s)
	}
}

func (s *sttStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.inputEnded {
		return io.ErrClosedPipe
	}
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	if s.reconnectRequested {
		if err := s.reconnectLocked(); err != nil {
			return err
		}
	}
	s.audioBuffer = append(s.audioBuffer, frame.Data...)
	chunkSize := s.audioChunkBytes()
	for len(s.audioBuffer) >= chunkSize {
		chunk := append([]byte(nil), s.audioBuffer[:chunkSize]...)
		if err := s.writeAlignedAudio(chunk); err != nil {
			return err
		}
		s.audioBuffer = s.audioBuffer[chunkSize:]
	}
	return nil
}

func (s *sttStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if len(s.audioBuffer) == 0 {
		return nil
	}
	chunk := append([]byte(nil), s.audioBuffer...)
	s.audioBuffer = nil
	return s.writeAlignedAudio(chunk)
}

func (s *sttStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.inputEnded {
		return nil
	}
	if len(s.audioBuffer) > 0 {
		chunk := append([]byte(nil), s.audioBuffer...)
		s.audioBuffer = nil
		if err := s.writeAlignedAudio(chunk); err != nil {
			return err
		}
	}
	messageType := "finalize"
	if s.legacyEndpoint {
		messageType = "flush"
	}
	payload, _ := json.Marshal(map[string]string{"type": messageType})
	if err := s.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		s.closed = true
		s.stopLifecycleLocked()
		_ = s.conn.Close()
		if s.provider != nil {
			s.provider.unregisterStream(s)
		}
		return err
	}
	s.inputEnded = true
	if s.finalTimeout > 0 {
		if err := s.conn.SetReadDeadline(time.Now().Add(s.finalTimeout)); err != nil {
			return err
		}
	}
	return nil
}

func (s *sttStream) writeAlignedAudio(chunk []byte) error {
	if len(chunk)%slngSTTBytesPerSample(s.encoding) != 0 {
		return nil
	}
	if err := s.conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
		s.closed = true
		s.stopLifecycleLocked()
		_ = s.conn.Close()
		if s.provider != nil {
			s.provider.unregisterStream(s)
		}
		return err
	}
	s.speechDuration += s.audioDuration(chunk)
	return nil
}

func (s *sttStream) audioDuration(chunk []byte) float64 {
	sampleRate := s.sampleRate
	if sampleRate <= 0 {
		sampleRate = defaultSLNGSTTSampleRate
	}
	bytesPerSample := slngSTTBytesPerSample(s.encoding)
	if bytesPerSample <= 0 || len(chunk) == 0 {
		return 0
	}
	return float64(len(chunk)/bytesPerSample) / float64(sampleRate)
}

func (s *sttStream) audioChunkBytes() int {
	sampleRate := s.sampleRate
	if sampleRate <= 0 {
		sampleRate = defaultSLNGSTTSampleRate
	}
	bufferSizeSeconds := s.bufferSizeSeconds
	if bufferSizeSeconds <= 0 {
		bufferSizeSeconds = defaultSLNGBufferSeconds
	}
	samplesPerBuffer := int(math.Round(float64(sampleRate) * bufferSizeSeconds))
	if samplesPerBuffer < 1 {
		samplesPerBuffer = 1
	}
	return samplesPerBuffer * slngSTTBytesPerSample(s.encoding)
}

func (s *sttStream) updateOptions(opts slngSTTActiveOptions) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if opts.language != "" {
		s.language = opts.language
	}
	s.partials = opts.partials
	if opts.bufferSizeSeconds > 0 {
		s.bufferSizeSeconds = opts.bufferSizeSeconds
	}
	s.diarization = opts.diarization
	s.vadThreshold = opts.vadThreshold
	s.vadMinSilenceDurationMS = opts.vadMinSilenceDurationMS
	s.vadSpeechPadMS = opts.vadSpeechPadMS
	s.reconnectRequested = true
	conn := s.conn
	s.mu.Unlock()

	if conn != nil {
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		_ = conn.Close()
	}
}

func (s *sttStream) reconnectLocked() error {
	if s.closed {
		return io.ErrClosedPipe
	}
	provider := s.provider
	if provider == nil {
		return io.ErrClosedPipe
	}

	candidates, err := provider.resolvedSTTCandidates()
	if err != nil {
		return err
	}
	if s.candidateIndex < 0 || s.candidateIndex >= len(candidates) {
		return errors.New("slng stt active connection candidate is unavailable")
	}
	candidate := candidates[s.candidateIndex]
	attempt := provider.sttAttempt(candidate)
	attempt.sampleRate = s.sampleRate
	attempt.bufferSizeSeconds = s.bufferSizeSeconds
	attempt.encoding = s.encoding
	attempt.enablePartialTranscript = s.partials
	attempt.vadThreshold = s.vadThreshold
	attempt.vadMinSilenceDurationMS = s.vadMinSilenceDurationMS
	attempt.vadSpeechPadMS = s.vadSpeechPadMS
	attempt.enableDiarization = s.diarization
	attempt.language = s.language
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	headers, err := buildSTTWebsocketHeadersForCandidate(&attempt, candidate.headers)
	if err != nil {
		return err
	}
	conn, response, err := websocket.DefaultDialer.DialContext(ctx, candidate.endpoint, headers)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		return slngSTTDialError(response, err)
	}
	initPayload := buildSTTInitPayload(&attempt)
	if candidate.init != nil {
		initPayload, err = json.Marshal(candidate.init)
	}
	if err == nil {
		err = conn.WriteMessage(websocket.TextMessage, initPayload)
	}
	if err != nil {
		_ = conn.Close()
		return llm.NewAPIConnectionError(fmt.Sprintf("failed to initialize slng stt websocket: %v", err))
	}
	if s.closed {
		_ = conn.Close()
		return io.ErrClosedPipe
	}
	s.conn = conn
	s.legacyEndpoint = candidate.legacyEndpoint
	s.reconnectRequested = false
	s.audioBuffer = nil
	s.pendingEvents = nil
	s.speechStarted = false
	s.speechDuration = 0
	return nil
}

func slngSTTBytesPerSample(encoding string) int {
	if encoding == "pcm_mulaw" {
		return 1
	}
	return 2
}

func (s *sttStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.stopLifecycleLocked()
	if s.provider != nil {
		defer s.provider.unregisterStream(s)
	}
	if s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

func (s *sttStream) Next() (*stt.SpeechEvent, error) {
	for {
		s.mu.Lock()
		closed := s.closed
		conn := s.conn
		if closed || conn == nil {
			s.mu.Unlock()
			return nil, io.EOF
		}
		if len(s.pendingEvents) > 0 {
			event := s.pendingEvents[0]
			s.pendingEvents = s.pendingEvents[1:]
			s.mu.Unlock()
			return event, nil
		}
		if s.reconnectRequested {
			if err := s.reconnectLocked(); err != nil {
				s.mu.Unlock()
				return nil, err
			}
			s.mu.Unlock()
			continue
		}
		s.mu.Unlock()
		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			if s.isFinalTimeout(err) {
				return nil, llm.NewAPITimeoutError("SLNG STT timed out waiting for final transcript")
			}
			if s.shouldReconnect() {
				s.mu.Lock()
				err := s.reconnectLocked()
				s.mu.Unlock()
				if err != nil {
					return nil, err
				}
				continue
			}
			if reconnected, reconnectErr := s.trySilentReconnect(err); reconnected {
				if reconnectErr != nil {
					return nil, reconnectErr
				}
				continue
			}
			if s.isClosed() {
				return nil, io.EOF
			}
			if s.ctx != nil && s.ctx.Err() != nil {
				return nil, s.ctx.Err()
			}
			return nil, slngSTTReadError(err)
		}
		if msgType != websocket.TextMessage {
			continue
		}
		if slngSTTMessageIsFinal(payload) {
			_ = conn.SetReadDeadline(time.Time{})
		}
		events, speechStarted, speechDuration, err := sttEventsFromMessageWithSpeechState(payload, s.language, s.partials, s.speechStarted, s.speechDuration)
		if err != nil {
			return nil, err
		}
		s.speechStarted = speechStarted
		s.speechDuration = speechDuration
		if len(events) > 0 {
			s.silentReconnects = 0
			event := events[0]
			s.pendingEvents = append(s.pendingEvents, events[1:]...)
			return event, nil
		}
	}
}

func (s *sttStream) isFinalTimeout(err error) bool {
	s.mu.Lock()
	inputEnded := s.inputEnded
	timeout := s.finalTimeout
	s.mu.Unlock()
	var netErr net.Error
	return inputEnded && timeout > 0 && errors.As(err, &netErr) && netErr.Timeout()
}

func (s *sttStream) trySilentReconnect(readErr error) (bool, error) {
	var closeErr *websocket.CloseError
	if !errors.As(readErr, &closeErr) || closeErr.Code != websocket.CloseNormalClosure {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.inputEnded || s.reconnectRequested {
		return false, nil
	}
	if s.ctx != nil && s.ctx.Err() != nil {
		return true, s.ctx.Err()
	}
	s.silentReconnects++
	if s.silentReconnects >= maxSLNGSTTSilentReconnects {
		return true, llm.NewAPIStatusError(
			"SLNG STT closed the connection repeatedly without producing transcripts",
			closeErr.Code,
			"",
			nil,
		)
	}
	if err := s.reconnectLocked(); err != nil {
		return true, err
	}
	return true, nil
}

func (s *sttStream) stopLifecycleLocked() {
	if s.lifecycleDone != nil {
		s.lifecycleDoneOnce.Do(func() { close(s.lifecycleDone) })
	}
}

func slngSTTMessageIsFinal(payload []byte) bool {
	var message map[string]any
	if json.Unmarshal(payload, &message) != nil {
		return false
	}
	if slngString(message["type"]) == "final_transcript" {
		return true
	}
	return slngString(message["type"]) == "Results" && slngBool(message["is_final"])
}

func (s *sttStream) shouldReconnect() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed && s.reconnectRequested
}

func (s *sttStream) isClosed() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func slngSTTReadError(err error) error {
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return llm.NewAPIStatusError("SLNG connection closed unexpectedly", closeErr.Code, "", err.Error())
	}
	return err
}

func isSTTBridgeEndpoint(endpoint string) bool {
	_, err := bridgeModel(endpoint, "stt")
	return err == nil
}
