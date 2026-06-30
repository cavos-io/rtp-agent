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

type STT struct {
	mu                      sync.Mutex
	apiKey                  string
	model                   string
	endpoint                string
	modelEndpoints          []string
	regionOverride          string
	sampleRate              int
	bufferSizeSeconds       float64
	encoding                string
	enablePartialTranscript bool
	vadThreshold            float64
	vadMinSilenceDurationMS int
	vadSpeechPadMS          int
	enableDiarization       bool
	minSpeakers             *int
	maxSpeakers             *int
	language                string
	modelOptions            map[string]any
	streams                 map[*sttStream]struct{}
	closed                  bool
}

type STTOption func(*STT)

func WithSTTBaseURL(baseURL string) STTOption {
	return func(s *STT) {
		if baseURL != "" {
			s.endpoint = defaultSTTEndpoint(strings.TrimRight(baseURL, "/"), s.model)
			s.modelEndpoints = nil
		}
	}
}

func WithSTTModel(modelName string) STTOption {
	return func(s *STT) {
		if modelName != "" {
			s.model = modelName
			s.endpoint = defaultSTTEndpoint(defaultSLNGBaseURL, modelName)
			s.modelEndpoints = nil
		}
	}
}

func WithSTTEndpoint(endpoint string) STTOption {
	return func(s *STT) {
		if endpoint != "" {
			s.endpoint = endpoint
			s.modelEndpoints = []string{endpoint}
			if model := extractSTTModelFromEndpoint(endpoint); model != "" {
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
		s.endpoint = cleaned[0]
		if model := extractSTTModelFromEndpoint(cleaned[0]); model != "" {
			s.model = model
		}
	}
}

func WithSTTRegionOverride(region any) STTOption {
	return func(s *STT) {
		s.regionOverride = normalizeRegionOverride(region)
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
		apiKey:                  apiKey,
		model:                   defaultSLNGSTTModel,
		endpoint:                defaultSTTEndpoint(defaultSLNGBaseURL, defaultSLNGSTTModel),
		sampleRate:              defaultSLNGSTTSampleRate,
		bufferSizeSeconds:       defaultSLNGBufferSeconds,
		encoding:                defaultSLNGSTTEncoding,
		enablePartialTranscript: true,
		vadThreshold:            defaultSLNGVADThreshold,
		vadMinSilenceDurationMS: defaultSLNGVADMinSilenceMS,
		vadSpeechPadMS:          defaultSLNGVADSpeechPadMS,
		language:                defaultSLNGLanguage,
	}
	for _, opt := range opts {
		opt(provider)
	}
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
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	if s.regionOverride != "" {
		req.Header.Set("X-Region-Override", s.regionOverride)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("slng stt error: %s", string(respBody))
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
	endpoints := s.sttEndpoints()
	var lastErr error
	for endpointIndex, endpoint := range endpoints {
		attempt := *s
		attempt.endpoint = endpoint
		if model := extractSTTModelFromEndpoint(endpoint); model != "" {
			attempt.model = model
		}
		conn, _, err := websocket.DefaultDialer.DialContext(ctx, endpoint, buildSTTWebsocketHeaders(&attempt))
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil, context.Canceled
			}
			lastErr = llm.NewAPIConnectionError(fmt.Sprintf("failed to dial slng stt websocket: %v", err))
			continue
		}
		if s.isClosed() {
			conn.Close()
			return nil, io.ErrClosedPipe
		}
		if err := conn.WriteMessage(websocket.TextMessage, buildSTTInitPayload(&attempt)); err != nil {
			conn.Close()
			lastErr = err
			continue
		}
		s.endpoint = endpoint
		s.model = attempt.model
		if len(s.modelEndpoints) > 0 && endpointIndex > 0 {
			s.modelEndpoints = append([]string(nil), endpoints[endpointIndex:]...)
		}
		stream := &sttStream{
			ctx:                     ctx,
			provider:                s,
			conn:                    conn,
			language:                s.resolveLanguage(language),
			partials:                s.enablePartialTranscript,
			sampleRate:              s.sampleRate,
			bufferSizeSeconds:       s.bufferSizeSeconds,
			encoding:                s.encoding,
			diarization:             s.enableDiarization,
			vadThreshold:            s.vadThreshold,
			vadMinSilenceDurationMS: s.vadMinSilenceDurationMS,
			vadSpeechPadMS:          s.vadSpeechPadMS,
		}
		if !s.registerStream(stream) {
			stream.Close()
			return nil, io.ErrClosedPipe
		}
		return stream, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("slng stt websocket endpoint is empty")
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

func (s *STT) resolveLanguage(language string) string {
	if language != "" {
		return language
	}
	return s.language
}

func buildSTTWebsocketHeaders(s *STT) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+s.apiKey)
	headers.Set("X-API-Key", s.apiKey)
	if s.regionOverride != "" {
		headers.Set("X-Region-Override", s.regionOverride)
	}
	return headers
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
		return nil, speechStarted, speechDuration, err
	}
	messageType := slngString(message["type"])
	if messageType == "Results" {
		message = normalizeSLNGResults(message)
		messageType = slngString(message["type"])
	}
	if messageType == "Error" {
		return nil, speechStarted, speechDuration, fmt.Errorf("slng stt error: %s", extractSLNGError(message))
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
	closed                  bool
}

func (s *sttStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
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

func (s *sttStream) writeAlignedAudio(chunk []byte) error {
	if len(chunk)%slngSTTBytesPerSample(s.encoding) != 0 {
		return nil
	}
	if err := s.conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
		s.closed = true
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

	provider.mu.Lock()
	attempt := STT{
		apiKey:                  provider.apiKey,
		endpoint:                provider.endpoint,
		model:                   provider.model,
		regionOverride:          provider.regionOverride,
		sampleRate:              s.sampleRate,
		bufferSizeSeconds:       s.bufferSizeSeconds,
		encoding:                s.encoding,
		enablePartialTranscript: s.partials,
		vadThreshold:            s.vadThreshold,
		vadMinSilenceDurationMS: s.vadMinSilenceDurationMS,
		vadSpeechPadMS:          s.vadSpeechPadMS,
		enableDiarization:       s.diarization,
		language:                s.language,
		modelOptions:            cloneSLNGMap(provider.modelOptions),
	}
	if provider.minSpeakers != nil {
		minSpeakers := *provider.minSpeakers
		attempt.minSpeakers = &minSpeakers
	}
	if provider.maxSpeakers != nil {
		maxSpeakers := *provider.maxSpeakers
		attempt.maxSpeakers = &maxSpeakers
	}
	provider.mu.Unlock()
	endpoint := attempt.endpoint
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, endpoint, buildSTTWebsocketHeaders(&attempt))
	if err != nil {
		return fmt.Errorf("failed to reconnect slng stt websocket: %w", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, buildSTTInitPayload(&attempt)); err != nil {
		_ = conn.Close()
		return err
	}
	if s.closed {
		_ = conn.Close()
		return io.ErrClosedPipe
	}
	s.conn = conn
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
			if s.shouldReconnect() {
				s.mu.Lock()
				err := s.reconnectLocked()
				s.mu.Unlock()
				if err != nil {
					return nil, err
				}
				continue
			}
			if s.isClosed() {
				return nil, io.EOF
			}
			return nil, slngSTTReadError(err)
		}
		if msgType != websocket.TextMessage {
			continue
		}
		events, speechStarted, speechDuration, err := sttEventsFromMessageWithSpeechState(payload, s.language, s.partials, s.speechStarted, s.speechDuration)
		if err != nil {
			return nil, err
		}
		s.speechStarted = speechStarted
		s.speechDuration = speechDuration
		if len(events) > 0 {
			event := events[0]
			s.pendingEvents = append(s.pendingEvents, events[1:]...)
			return event, nil
		}
	}
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
