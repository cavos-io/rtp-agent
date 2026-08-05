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
	maxSLNGSTTSameEndpointReplays          = 3
	maxSLNGSTTReplaySeconds                = 600
	slngSTTEncodingError                   = "only pcm_s16le encoding is supported: LiveKit audio frames are 16-bit PCM and the plugin does not transcode"
)

var (
	slngSTTNow               = time.Now
	slngSTTAfterFunc         = time.AfterFunc
	slngSTTKeepaliveInterval = 5 * time.Second
	slngSTTReadyTimeout      = 10 * time.Second
	slngSTTWriteTimeout      = 5 * time.Second
)

type STT struct {
	mu                       sync.Mutex
	optionError              error
	modelConfigured          bool
	connectionsConfigured    bool
	waitReady                bool
	apiKey                   string
	providerAPIKey           string
	providerAPIKeySet        bool
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
	externalAgentIDSet       bool
	externalSessionID        string
	externalSessionIDSet     bool
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
		endpoint, err := bridgeEndpoint(strings.TrimRight(baseURL, "/"), "stt", s.model)
		if err != nil {
			s.setOptionError(err)
			return
		}
		s.endpoint = endpoint
		s.modelEndpoints = nil
		s.connections = nil
		s.waitReady = true
	}
}

func WithSTTModel(modelName string) STTOption {
	return func(s *STT) {
		s.modelConfigured = true
		if s.connectionsConfigured {
			s.setOptionError(errors.New("use model or connections, not both"))
			return
		}
		endpoint, err := bridgeEndpoint(defaultSLNGBaseURL, "stt", modelName)
		if err != nil {
			s.setOptionError(err)
			return
		}
		s.model = modelName
		s.endpoint = endpoint
		s.modelEndpoints = nil
		s.connections = nil
		s.waitReady = true
	}
}

func WithSTTEndpoint(endpoint string) STTOption {
	return func(s *STT) {
		if endpoint != "" {
			s.endpoint = endpoint
			s.modelEndpoints = []string{endpoint}
			s.connections = nil
			s.waitReady = false
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
		s.waitReady = false
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
		s.connectionsConfigured = true
		if s.modelConfigured {
			s.setOptionError(errors.New("use model or connections, not both"))
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
		s.providerAPIKeySet = true
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
		s.externalAgentIDSet = true
		s.externalSessionID = sessionID
		s.externalSessionIDSet = true
	}
}

func WithSTTExternalAgentID(agentID string) STTOption {
	return func(s *STT) {
		s.externalAgentID = agentID
		s.externalAgentIDSet = true
	}
}

func WithSTTExternalSessionID(sessionID string) STTOption {
	return func(s *STT) {
		s.externalSessionID = sessionID
		s.externalSessionIDSet = true
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
			if encoding != "pcm_s16le" && encoding != "pcm_mulaw" {
				s.setOptionError(errors.New(slngSTTEncodingError))
			}
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
		waitReady:                true,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.optionError == nil {
		candidates, err := provider.resolvedSTTCandidates()
		if err != nil {
			provider.optionError = err
		} else if provider.encoding != "pcm_s16le" {
			for _, candidate := range candidates {
				if !candidate.legacyEndpoint {
					provider.optionError = errors.New(slngSTTEncodingError)
					break
				}
			}
		}
	}
	provider.candidateState = newCandidateState(provider.sttCandidateCount(), provider.fallbackRecoveryCooldown)
	return provider
}

func (s *STT) setOptionError(err error) {
	if err != nil && s.optionError == nil {
		s.optionError = err
	}
}

func (s *STT) Label() string { return "slng.STT" }
func (s *STT) Model() string { return "slng" }
func (s *STT) InputSampleRate() uint32 {
	if s == nil || s.sampleRate <= 0 {
		return uint32(defaultSLNGSTTSampleRate)
	}
	return uint32(s.sampleRate)
}
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
	if s.optionError != nil {
		return nil, s.optionError
	}
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
	if s.optionError != nil {
		return nil, s.optionError
	}
	if err := s.requireAPIKey(); err != nil {
		return nil, err
	}
	conn, candidate, candidateIndex, attempt, err := s.dialSTTCandidates(ctx, nil)
	if err != nil {
		return nil, err
	}
	stream := &sttStream{
		ctx:                     ctx,
		provider:                s,
		conn:                    conn,
		connGeneration:          1,
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
		lastClientSendAt:        time.Now(),
		lifecycleDone:           make(chan struct{}),
	}
	if !s.registerStream(stream) {
		stream.Close()
		return nil, io.ErrClosedPipe
	}
	go stream.runLifecycle()
	return stream, nil
}

func (s *STT) dialSTTCandidates(ctx context.Context, active *sttStream) (*websocket.Conn, sttConnectionCandidate, int, STT, error) {
	return s.dialSTTCandidatesFrom(ctx, active, -1)
}

func (s *STT) dialSTTCandidatesFrom(ctx context.Context, active *sttStream, candidateIndex int) (*websocket.Conn, sttConnectionCandidate, int, STT, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	candidates, err := s.resolvedSTTCandidates()
	if err != nil {
		return nil, sttConnectionCandidate{}, -1, STT{}, err
	}
	if len(candidates) == 0 {
		return nil, sttConnectionCandidate{}, -1, STT{}, errors.New("slng stt websocket endpoint is empty")
	}

	s.mu.Lock()
	if s.candidateState == nil || s.candidateState.count != len(candidates) {
		s.candidateState = newCandidateState(len(candidates), s.fallbackRecoveryCooldown)
	}
	if candidateIndex < 0 {
		candidateIndex = s.candidateState.start(slngSTTNow())
	}
	s.mu.Unlock()

	var lastErr error
	for candidateIndex >= 0 && candidateIndex < len(candidates) {
		if err := ctx.Err(); err != nil {
			return nil, sttConnectionCandidate{}, -1, STT{}, err
		}
		candidate := candidates[candidateIndex]
		attempt := s.sttAttempt(candidate)
		if active != nil {
			attempt.sampleRate = active.sampleRate
			attempt.bufferSizeSeconds = active.bufferSizeSeconds
			attempt.encoding = active.encoding
			attempt.enablePartialTranscript = active.partials
			attempt.vadThreshold = active.vadThreshold
			attempt.vadMinSilenceDurationMS = active.vadMinSilenceDurationMS
			attempt.vadSpeechPadMS = active.vadSpeechPadMS
			attempt.enableDiarization = active.diarization
			attempt.language = active.language
		}
		headers, err := buildSTTWebsocketHeadersForCandidate(&attempt, candidate.headers)
		if err != nil {
			return nil, sttConnectionCandidate{}, -1, STT{}, err
		}
		conn, response, dialErr := websocket.DefaultDialer.DialContext(ctx, candidate.endpoint, headers)
		var candidateErr error
		if dialErr != nil {
			if err := ctx.Err(); err != nil {
				return nil, sttConnectionCandidate{}, -1, STT{}, err
			}
			candidateErr = slngSTTDialError(response, dialErr)
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
				if ctxErr := ctx.Err(); ctxErr != nil {
					return nil, sttConnectionCandidate{}, -1, STT{}, ctxErr
				}
				candidateErr = llm.NewAPIConnectionError(fmt.Sprintf("failed to initialize slng stt websocket: %v", err))
			} else if candidate.waitReady {
				candidateErr = waitForSLNGSTTReady(ctx, conn)
				if candidateErr != nil {
					_ = conn.Close()
				}
			}
		}
		if candidateErr != nil {
			lastErr = candidateErr
			if !isSLNGFallbackEligible(candidateErr) {
				return nil, sttConnectionCandidate{}, -1, STT{}, candidateErr
			}
			s.mu.Lock()
			nextIndex, ok := s.candidateState.advance(candidateIndex, slngSTTNow())
			s.mu.Unlock()
			if !ok {
				return nil, sttConnectionCandidate{}, -1, STT{}, lastErr
			}
			candidateIndex = nextIndex
			continue
		}
		if s.isClosed() {
			_ = conn.Close()
			return nil, sttConnectionCandidate{}, -1, STT{}, io.ErrClosedPipe
		}
		s.mu.Lock()
		s.endpoint = candidate.endpoint
		s.model = candidate.model
		s.candidateState.selectCandidate(candidateIndex)
		s.mu.Unlock()
		return conn, candidate, candidateIndex, attempt, nil
	}
	return nil, sttConnectionCandidate{}, -1, STT{}, lastErr
}

func (s *STT) nextSTTRecoveryCandidate(current int) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := s.sttCandidateCount()
	if s.candidateState == nil || s.candidateState.count != count {
		s.candidateState = newCandidateState(count, s.fallbackRecoveryCooldown)
	}
	if current < 0 || current >= count {
		current = s.candidateState.active
	}
	return s.candidateState.advance(current, slngSTTNow())
}

func (s *STT) activeSTTCandidateIndex(fallback int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.candidateState == nil || s.candidateState.active < 0 || s.candidateState.active >= s.candidateState.count {
		return fallback
	}
	return s.candidateState.active
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
	waitReady := s.waitReady
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
				endpoint:  config.Endpoint,
				model:     model,
				headers:   config.Headers.Clone(),
				init:      cloneSLNGMap(config.Init),
				waitReady: true,
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
			waitReady:      waitReady && !legacy,
		})
	}
	return candidates, nil
}

func waitForSLNGSTTReady(ctx context.Context, conn *websocket.Conn) error {
	timeout := slngSTTReadyTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		return llm.NewAPIConnectionError(fmt.Sprintf("failed to set SLNG STT ready deadline: %v", err))
	}
	readDone := make(chan struct{})
	watcherDone := make(chan struct{})
	if ctx.Done() != nil {
		go func() {
			defer close(watcherDone)
			select {
			case <-ctx.Done():
				_ = conn.SetReadDeadline(time.Now())
			case <-readDone:
			}
		}()
	} else {
		close(watcherDone)
	}
	defer func() {
		close(readDone)
		<-watcherDone
		_ = conn.SetReadDeadline(time.Time{})
	}()

	for {
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				return llm.NewAPIConnectionError("timed out waiting for SLNG STT ready")
			}
			var closeErr *websocket.CloseError
			if errors.As(err, &closeErr) {
				return llm.NewAPIConnectionError("SLNG STT closed before ready")
			}
			return llm.NewAPIConnectionError(fmt.Sprintf("failed waiting for SLNG STT ready: %v", err))
		}
		if messageType != websocket.TextMessage {
			continue
		}
		var message map[string]any
		if json.Unmarshal(payload, &message) != nil {
			continue
		}
		switch slngString(message["type"]) {
		case "ready":
			return nil
		case "Error", "error":
			return slngStatusError(message)
		}
	}
}

func (s *STT) sttAttempt(candidate sttConnectionCandidate) STT {
	s.mu.Lock()
	defer s.mu.Unlock()
	attempt := STT{
		apiKey:                  s.apiKey,
		providerAPIKey:          s.providerAPIKey,
		providerAPIKeySet:       s.providerAPIKeySet,
		model:                   candidate.model,
		endpoint:                candidate.endpoint,
		regionOverride:          s.regionOverride,
		worldPartOverride:       s.worldPartOverride,
		externalAgentID:         s.externalAgentID,
		externalAgentIDSet:      s.externalAgentIDSet,
		externalSessionID:       s.externalSessionID,
		externalSessionIDSet:    s.externalSessionIDSet,
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
		APIKey:               s.apiKey,
		ProviderAPIKey:       s.providerAPIKey,
		ProviderAPIKeySet:    s.providerAPIKeySet,
		RegionOverride:       s.regionOverride,
		WorldPartOverride:    s.worldPartOverride,
		ExternalAgentID:      s.externalAgentID,
		ExternalAgentIDSet:   s.externalAgentIDSet,
		ExternalSessionID:    s.externalSessionID,
		ExternalSessionIDSet: s.externalSessionIDSet,
		Extra:                s.extraHeaders,
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
	events, _, _, _, _, err := sttEventsFromMessageWithSpeechState(payload, defaultLanguage, partials, false, 0, false)
	return events, err
}

func sttEventsFromMessageWithSpeechState(
	payload []byte,
	defaultLanguage string,
	partials bool,
	speechStarted bool,
	speechDuration float64,
	pendingNonEmptyTranscript bool,
) ([]*stt.SpeechEvent, bool, float64, bool, bool, error) {
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, speechStarted, speechDuration, pendingNonEmptyTranscript, false, nil
	}
	messageType := slngString(message["type"])
	if messageType == "Results" {
		message = normalizeSLNGResults(message)
		messageType = slngString(message["type"])
	}
	if messageType == "Error" || messageType == "error" {
		return nil, speechStarted, speechDuration, pendingNonEmptyTranscript, false, slngStatusError(message)
	}
	if messageType != "partial_transcript" && messageType != "final_transcript" {
		return nil, speechStarted, speechDuration, pendingNonEmptyTranscript, false, nil
	}
	isFinal := messageType == "final_transcript"
	text := slngString(message["transcript"])
	if !isFinal && text != "" {
		pendingNonEmptyTranscript = true
	}
	if !isFinal && !partials {
		return nil, speechStarted, speechDuration, pendingNonEmptyTranscript, false, nil
	}
	if text == "" {
		if !isFinal || pendingNonEmptyTranscript {
			return nil, speechStarted, speechDuration, pendingNonEmptyTranscript, false, nil
		}
		events := []*stt.SpeechEvent{}
		if speechStarted {
			events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech})
			speechStarted = false
		}
		if speechDuration > 0 {
			events = append(events, slngSTTRecognitionUsageEvent(speechDuration))
			speechDuration = 0
		}
		return events, speechStarted, speechDuration, false, true, nil
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
		pendingNonEmptyTranscript = false
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech})
		speechStarted = false
		if speechDuration > 0 {
			events = append(events, slngSTTRecognitionUsageEvent(speechDuration))
			speechDuration = 0
		}
	}
	return events, speechStarted, speechDuration, pendingNonEmptyTranscript, isFinal, nil
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
	mu                         sync.Mutex
	ctx                        context.Context
	provider                   *STT
	conn                       *websocket.Conn
	connGeneration             uint64
	candidateIndex             int
	language                   string
	partials                   bool
	sampleRate                 int
	bufferSizeSeconds          float64
	encoding                   string
	diarization                bool
	vadThreshold               float64
	vadMinSilenceDurationMS    int
	vadSpeechPadMS             int
	audioBuffer                []byte
	utteranceAudio             []byte
	pendingEvents              []*stt.SpeechEvent
	speechStarted              bool
	speechDuration             float64
	pendingNonEmptyTranscript  bool
	sentAudioSinceFinalize     bool
	finalizeRequested          bool
	lastClientSendAt           time.Time
	reconnectRequested         bool
	legacyEndpoint             bool
	inputEnded                 bool
	finalTimeout               time.Duration
	finalTimer                 *time.Timer
	finalTimerGeneration       uint64
	terminalErr                error
	silentReconnects           int
	sameEndpointReplayAttempts int
	lifecycleDone              chan struct{}
	lifecycleDoneOnce          sync.Once
	closed                     bool
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
			if !s.lastClientSendAt.IsZero() && time.Since(s.lastClientSendAt) < interval {
				s.mu.Unlock()
				continue
			}
			err := s.writeMessageLocked(websocket.TextMessage, []byte(`{"type":"keepalive"}`))
			if err != nil {
				if recovered, _ := s.recoverRuntimeLocked(err); recovered {
					s.mu.Unlock()
					continue
				}
				conn, provider := s.terminateLocked()
				s.mu.Unlock()
				if conn != nil {
					_ = conn.Close()
				}
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
	s.stopFinalTimerLocked()
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
	if s.reconnectRequested {
		if err := s.reconnectLocked(); err != nil {
			return err
		}
	}
	if len(s.audioBuffer) > 0 {
		chunk := append([]byte(nil), s.audioBuffer...)
		s.audioBuffer = nil
		if err := s.writeAlignedAudio(chunk); err != nil {
			return err
		}
	}
	if s.legacyEndpoint {
		return nil
	}
	return s.sendFinalizeIfNeededLocked()
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
	if s.reconnectRequested {
		if err := s.reconnectLocked(); err != nil {
			return err
		}
	}
	if len(s.audioBuffer) > 0 {
		chunk := append([]byte(nil), s.audioBuffer...)
		s.audioBuffer = nil
		if err := s.writeAlignedAudio(chunk); err != nil {
			return err
		}
	}
	if s.legacyEndpoint {
		if err := s.writeMessageLocked(websocket.TextMessage, []byte(`{"type":"flush"}`)); err != nil {
			return s.failWriteLocked(err)
		}
		s.inputEnded = true
		s.finalizeRequested = true
		s.startFinalTimerLocked()
		return nil
	}
	if err := s.sendFinalizeIfNeededLocked(); err != nil {
		return err
	}
	s.inputEnded = true
	if err := s.writeMessageLocked(websocket.TextMessage, []byte(`{"type":"close"}`)); err != nil {
		recovered, recoveryErr := s.recoverRuntimeLocked(err)
		if recovered {
			if s.finalizeRequested {
				s.startFinalTimerLocked()
			}
			return nil
		}
		if recoveryErr != nil {
			err = recoveryErr
		}
		return s.failWriteLocked(err)
	}
	if s.finalizeRequested {
		s.startFinalTimerLocked()
	}
	return nil
}

func (s *sttStream) sendFinalizeIfNeededLocked() error {
	if !s.sentAudioSinceFinalize {
		return nil
	}
	s.finalizeRequested = true
	if err := s.writeMessageLocked(websocket.TextMessage, []byte(`{"type":"finalize"}`)); err != nil {
		recovered, recoveryErr := s.recoverRuntimeLocked(err)
		if recovered {
			s.sentAudioSinceFinalize = false
			return nil
		}
		if recoveryErr != nil {
			err = recoveryErr
		}
		s.finalizeRequested = false
		return s.failWriteLocked(err)
	}
	s.sentAudioSinceFinalize = false
	return nil
}

func (s *sttStream) writeAlignedAudio(chunk []byte) error {
	if len(chunk)%slngSTTBytesPerSample(s.encoding) != 0 {
		return nil
	}
	for {
		if err := s.writeMessageLocked(websocket.BinaryMessage, chunk); err != nil {
			recovered, recoveryErr := s.recoverRuntimeLocked(err)
			if recovered {
				continue
			}
			if recoveryErr != nil {
				err = recoveryErr
			}
			return s.failWriteLocked(err)
		}
		s.speechDuration += s.audioDuration(chunk)
		s.sentAudioSinceFinalize = true
		s.retainUtteranceAudioLocked(chunk)
		return nil
	}
}

func (s *sttStream) retainUtteranceAudioLocked(chunk []byte) {
	if len(chunk) == 0 || s.legacyEndpoint {
		return
	}
	sampleRate := s.sampleRate
	if sampleRate <= 0 {
		sampleRate = defaultSLNGSTTSampleRate
	}
	maxBytes := sampleRate * slngSTTBytesPerSample(s.encoding) * maxSLNGSTTReplaySeconds
	if maxBytes <= 0 {
		return
	}
	if len(chunk) >= maxBytes {
		s.utteranceAudio = append(s.utteranceAudio[:0], chunk[len(chunk)-maxBytes:]...)
		return
	}
	s.utteranceAudio = append(s.utteranceAudio, chunk...)
	if excess := len(s.utteranceAudio) - maxBytes; excess > 0 {
		copy(s.utteranceAudio, s.utteranceAudio[excess:])
		s.utteranceAudio = s.utteranceAudio[:maxBytes]
	}
}

func (s *sttStream) recoverRuntimeLocked(cause error) (bool, error) {
	if s.closed || s.legacyEndpoint || s.reconnectRequested || !isSLNGFallbackEligible(cause) {
		return false, cause
	}
	provider := s.provider
	if provider == nil {
		return false, cause
	}
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	currentIndex := s.candidateIndex
	lastErr := cause
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		nextIndex, advanced := provider.nextSTTRecoveryCandidate(currentIndex)
		if !advanced {
			if s.sameEndpointReplayAttempts >= maxSLNGSTTSameEndpointReplays {
				return false, lastErr
			}
			s.sameEndpointReplayAttempts++
			nextIndex = currentIndex
		} else {
			s.sameEndpointReplayAttempts = 0
		}

		conn, candidate, candidateIndex, _, err := provider.dialSTTCandidatesFrom(ctx, s, nextIndex)
		if err != nil {
			lastErr = err
			if !isSLNGFallbackEligible(err) {
				return false, err
			}
			currentIndex = provider.activeSTTCandidateIndex(currentIndex)
			continue
		}

		oldConn := s.conn
		s.conn = conn
		s.connGeneration++
		s.candidateIndex = candidateIndex
		s.legacyEndpoint = candidate.legacyEndpoint
		s.lastClientSendAt = time.Now()
		if oldConn != nil && oldConn != conn {
			_ = oldConn.Close()
		}
		currentIndex = candidateIndex

		if len(s.utteranceAudio) > 0 {
			if err := s.writeMessageLocked(websocket.BinaryMessage, s.utteranceAudio); err != nil {
				lastErr = err
				_ = conn.Close()
				continue
			}
		}
		if s.finalizeRequested {
			if err := s.writeMessageLocked(websocket.TextMessage, []byte(`{"type":"finalize"}`)); err != nil {
				lastErr = err
				_ = conn.Close()
				continue
			}
		}
		if s.inputEnded {
			if err := s.writeMessageLocked(websocket.TextMessage, []byte(`{"type":"close"}`)); err != nil {
				lastErr = err
				_ = conn.Close()
				continue
			}
		}
		s.silentReconnects = 0
		return true, nil
	}
}

func (s *sttStream) writeMessageLocked(messageType int, payload []byte) error {
	if s.conn == nil {
		return io.ErrClosedPipe
	}
	timeout := slngSTTWriteTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	if s.ctx != nil {
		if ctxDeadline, ok := s.ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
			deadline = ctxDeadline
		}
		if err := s.ctx.Err(); err != nil {
			return err
		}
	}
	if err := s.conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	if err := s.conn.WriteMessage(messageType, payload); err != nil {
		if s.ctx != nil && s.ctx.Err() != nil {
			return s.ctx.Err()
		}
		return err
	}
	s.lastClientSendAt = time.Now()
	return nil
}

func (s *sttStream) failWriteLocked(err error) error {
	conn, provider := s.terminateLocked()
	if conn != nil {
		_ = conn.Close()
	}
	if provider != nil {
		provider.unregisterStream(s)
	}
	return err
}

func (s *sttStream) terminateLocked() (*websocket.Conn, *STT) {
	s.closed = true
	s.stopFinalTimerLocked()
	s.stopLifecycleLocked()
	conn := s.conn
	s.conn = nil
	s.connGeneration++
	return conn, s.provider
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
	if s.closed || s.inputEnded {
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

	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	conn, candidate, candidateIndex, _, err := provider.dialSTTCandidates(ctx, s)
	if err != nil {
		return err
	}
	if s.closed {
		_ = conn.Close()
		return io.ErrClosedPipe
	}
	oldConn := s.conn
	s.conn = conn
	s.connGeneration++
	s.candidateIndex = candidateIndex
	s.legacyEndpoint = candidate.legacyEndpoint
	s.reconnectRequested = false
	if s.speechStarted {
		s.pendingEvents = append(s.pendingEvents, &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech})
	}
	s.speechStarted = false
	s.speechDuration = 0
	s.pendingNonEmptyTranscript = false
	s.utteranceAudio = nil
	s.sentAudioSinceFinalize = false
	s.finalizeRequested = false
	s.sameEndpointReplayAttempts = 0
	s.lastClientSendAt = time.Now()
	if oldConn != nil && oldConn != conn {
		_ = oldConn.Close()
	}
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
	s.stopFinalTimerLocked()
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
		if s.terminalErr != nil {
			err := s.terminalErr
			s.mu.Unlock()
			return nil, err
		}
		if len(s.pendingEvents) > 0 {
			event := s.pendingEvents[0]
			s.pendingEvents = s.pendingEvents[1:]
			s.mu.Unlock()
			return event, nil
		}
		if s.closed || s.conn == nil {
			s.mu.Unlock()
			return nil, io.EOF
		}
		if s.reconnectRequested {
			if err := s.reconnectLocked(); err != nil {
				s.mu.Unlock()
				return nil, err
			}
			s.mu.Unlock()
			continue
		}
		conn := s.conn
		generation := s.connGeneration
		s.mu.Unlock()

		msgType, payload, err := conn.ReadMessage()
		s.mu.Lock()
		if conn != s.conn || generation != s.connGeneration {
			s.mu.Unlock()
			continue
		}
		if err != nil {
			if s.reconnectRequested {
				reconnectErr := s.reconnectLocked()
				s.mu.Unlock()
				if reconnectErr != nil {
					return nil, reconnectErr
				}
				continue
			}
			if s.ctx != nil && s.ctx.Err() != nil {
				ctxErr := s.ctx.Err()
				s.mu.Unlock()
				return nil, ctxErr
			}
			if len(s.utteranceAudio) > 0 && !s.legacyEndpoint {
				recovered, recoveryErr := s.recoverRuntimeLocked(slngSTTReadError(err))
				if recovered {
					s.mu.Unlock()
					continue
				}
				if recoveryErr != nil {
					s.mu.Unlock()
					return nil, recoveryErr
				}
			}
			if reconnected, reconnectErr := s.trySilentReconnectLocked(err); reconnected {
				s.mu.Unlock()
				if reconnectErr != nil {
					return nil, reconnectErr
				}
				continue
			}
			if s.closed {
				s.mu.Unlock()
				return nil, io.EOF
			}
			s.mu.Unlock()
			return nil, slngSTTReadError(err)
		}
		if msgType != websocket.TextMessage {
			s.mu.Unlock()
			continue
		}
		events, speechStarted, speechDuration, pendingTranscript, acceptedFinal, err := sttEventsFromMessageWithSpeechState(
			payload,
			s.language,
			s.partials,
			s.speechStarted,
			s.speechDuration,
			s.pendingNonEmptyTranscript,
		)
		if err != nil {
			recovered, recoveryErr := s.recoverRuntimeLocked(err)
			if recovered {
				s.mu.Unlock()
				continue
			}
			if recoveryErr != nil {
				err = recoveryErr
			}
			s.mu.Unlock()
			return nil, err
		}
		s.speechStarted = speechStarted
		s.speechDuration = speechDuration
		s.pendingNonEmptyTranscript = pendingTranscript
		if acceptedFinal {
			s.stopFinalTimerLocked()
			s.finalizeRequested = false
			s.sentAudioSinceFinalize = false
			s.utteranceAudio = nil
			s.sameEndpointReplayAttempts = 0
		}
		if len(events) > 0 {
			s.silentReconnects = 0
			event := events[0]
			s.pendingEvents = append(s.pendingEvents, events[1:]...)
			s.mu.Unlock()
			return event, nil
		}
		s.mu.Unlock()
	}
}

func (s *sttStream) trySilentReconnectLocked(readErr error) (bool, error) {
	var closeErr *websocket.CloseError
	if !errors.As(readErr, &closeErr) || closeErr.Code != websocket.CloseNormalClosure {
		return false, nil
	}
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

func (s *sttStream) startFinalTimerLocked() {
	if s.finalTimeout <= 0 {
		return
	}
	s.stopFinalTimerLocked()
	generation := s.finalTimerGeneration
	var timer *time.Timer
	timer = slngSTTAfterFunc(s.finalTimeout, func() {
		timeoutErr := llm.NewAPITimeoutError("SLNG STT timed out waiting for final transcript")
		s.mu.Lock()
		if s.finalTimerGeneration != generation || s.finalTimer != timer {
			s.mu.Unlock()
			return
		}
		s.finalTimer = nil
		if s.closed || !s.inputEnded || s.terminalErr != nil {
			s.mu.Unlock()
			return
		}
		s.terminalErr = timeoutErr
		s.closed = true
		s.stopLifecycleLocked()
		conn := s.conn
		s.conn = nil
		s.connGeneration++
		provider := s.provider
		s.mu.Unlock()
		if conn != nil {
			_ = conn.Close()
		}
		if provider != nil {
			provider.unregisterStream(s)
		}
	})
	s.finalTimer = timer
}

func (s *sttStream) stopFinalTimerLocked() {
	s.finalTimerGeneration++
	if s.finalTimer != nil {
		s.finalTimer.Stop()
		s.finalTimer = nil
	}
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
