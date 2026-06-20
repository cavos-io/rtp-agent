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

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

const (
	defaultSLNGBaseURL         = "api.slng.ai"
	defaultSLNGSTTModel        = "deepgram/nova:3"
	defaultSLNGTTSModel        = "deepgram/aura:2"
	defaultSLNGSTTSampleRate   = 16000
	defaultSLNGTTSSampleRate   = 24000
	defaultSLNGBufferSeconds   = 0.064
	defaultSLNGSTTEncoding     = "pcm_s16le"
	defaultSLNGTTSEncoding     = "linear16"
	defaultSLNGTTSVoice        = "default"
	defaultSLNGLanguage        = "en"
	defaultSLNGVADThreshold    = 0.5
	defaultSLNGVADMinSilenceMS = 300
	defaultSLNGVADSpeechPadMS  = 30
	defaultSLNGSpeed           = 1.0
	slngAPIKeyEnv              = "SLNG_API_KEY"
	slngNumChannels            = 1
	slngFlushMessage           = `{"type":"flush"}`
	slngCancelMessage          = `{"type":"cancel"}`
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
			lastErr = fmt.Errorf("failed to dial slng stt websocket: %w", err)
			continue
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
			provider:          s,
			conn:              conn,
			language:          s.resolveLanguage(language),
			partials:          s.enablePartialTranscript,
			sampleRate:        s.sampleRate,
			bufferSizeSeconds: s.bufferSizeSeconds,
			encoding:          s.encoding,
		}
		s.registerStream(stream)
		return stream, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("slng stt websocket endpoint is empty")
}

func (s *STT) Close() error {
	s.mu.Lock()
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

func (s *STT) registerStream(stream *sttStream) {
	if stream == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streams == nil {
		s.streams = make(map[*sttStream]struct{})
	}
	stream.provider = s
	s.streams[stream] = struct{}{}
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

type TTS struct {
	mu             sync.Mutex
	apiKey         string
	model          string
	endpoint       string
	regionOverride string
	voice          string
	language       string
	sampleRate     int
	speed          float64
	encoding       string
	modelOptions   map[string]any
	streams        map[*ttsStream]struct{}
}

type TTSOption func(*TTS)

func WithTTSBaseURL(baseURL string) TTSOption {
	return func(t *TTS) {
		if baseURL != "" {
			t.endpoint = defaultTTSEndpoint(strings.TrimRight(baseURL, "/"), t.model)
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
		}
	}
}

func WithTTSEndpoint(endpoint string) TTSOption {
	return func(t *TTS) {
		if endpoint != "" {
			t.endpoint = endpoint
		}
	}
}

func WithTTSRegionOverride(region any) TTSOption {
	return func(t *TTS) {
		t.regionOverride = normalizeRegionOverride(region)
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

func NewTTS(apiKey string, opts ...TTSOption) *TTS {
	if apiKey == "" {
		apiKey = os.Getenv(slngAPIKeyEnv)
	}
	provider := &TTS{
		apiKey:     apiKey,
		model:      defaultSLNGTTSModel,
		endpoint:   defaultTTSEndpoint(defaultSLNGBaseURL, defaultSLNGTTSModel),
		voice:      normalizeTTSVoice(defaultSLNGTTSModel, defaultSLNGTTSVoice),
		language:   defaultSLNGLanguage,
		sampleRate: defaultSLNGTTSSampleRate,
		speed:      defaultSLNGSpeed,
		encoding:   defaultSLNGTTSEncoding,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
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
	defer t.mu.Unlock()
	for _, opt := range opts {
		if opt != nil {
			opt(t)
		}
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

func (t *TTS) stream(ctx context.Context, appendTextSpace bool) (tts.SynthesizeStream, error) {
	if err := t.requireAPIKey(); err != nil {
		return nil, err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, t.endpoint, buildTTSWebsocketHeaders(t))
	if err != nil {
		return nil, fmt.Errorf("failed to dial slng tts websocket: %w", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, buildTTSInitPayload(t)); err != nil {
		conn.Close()
		return nil, err
	}
	stream := &ttsStream{provider: t, conn: conn, sampleRate: t.sampleRate, model: t.model, appendTextSpace: appendTextSpace}
	t.registerStream(stream)
	return stream, nil
}

func (t *TTS) Close() error {
	t.mu.Lock()
	streams := make([]*ttsStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.mu.Unlock()

	var firstErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (t *TTS) registerStream(stream *ttsStream) {
	if stream == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.streams == nil {
		t.streams = make(map[*ttsStream]struct{})
	}
	stream.provider = t
	t.streams[stream] = struct{}{}
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

func defaultSTTEndpoint(baseURL, model string) string {
	return defaultSLNGEndpoint(baseURL, "stt", model)
}

func defaultTTSEndpoint(baseURL, model string) string {
	return defaultSLNGEndpoint(baseURL, "tts", model)
}

func defaultSLNGEndpoint(baseURL, kind, modelName string) string {
	host := strings.Split(baseURL, ":")[0]
	scheme := "wss"
	if host == "localhost" || host == "127.0.0.1" {
		scheme = "ws"
	}
	return fmt.Sprintf("%s://%s/v1/%s/%s", scheme, strings.TrimRight(baseURL, "/"), kind, modelName)
}

func normalizeRegionOverride(region any) string {
	var raw []string
	switch v := region.(type) {
	case nil:
		return ""
	case string:
		raw = strings.Split(v, ",")
	case []string:
		raw = v
	default:
		raw = []string{fmt.Sprint(v)}
	}
	values := make([]string, 0, len(raw))
	for _, value := range raw {
		cleaned := strings.ToLower(strings.TrimSpace(value))
		if cleaned != "" {
			values = append(values, cleaned)
		}
	}
	return strings.Join(values, ", ")
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

func buildTTSWebsocketHeaders(t *TTS) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+t.apiKey)
	headers.Set("X-API-Key", t.apiKey)
	if t.regionOverride != "" {
		headers.Set("X-Region-Override", t.regionOverride)
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

func slngOptionDefault(options map[string]any, key string, fallback any) any {
	if value, ok := options[key]; ok {
		return value
	}
	return fallback
}

type modelRef struct {
	raw           string
	routeProvider string
	routeModel    string
	variant       string
}

func parseModelRef(modelName string) (modelRef, error) {
	raw := strings.TrimSpace(modelName)
	if raw == "" {
		return modelRef{}, fmt.Errorf("model must not be empty")
	}
	modelPath, variant, _ := strings.Cut(raw, ":")
	if strings.Contains(raw, ":") {
		before, after, _ := strings.Cut(raw, ":")
		modelPath, variant = before, after
		if variant == "" {
			return modelRef{}, fmt.Errorf("model variant must not be empty")
		}
	}
	parts := strings.Split(modelPath, "/")
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			cleaned = append(cleaned, part)
		}
	}
	if len(cleaned) < 2 {
		return modelRef{}, fmt.Errorf("invalid model %q", raw)
	}
	if cleaned[0] == "slng" {
		if len(cleaned) < 3 {
			return modelRef{}, fmt.Errorf("invalid model %q", raw)
		}
		return modelRef{raw: raw, routeProvider: cleaned[1], routeModel: strings.Join(cleaned[2:], "/"), variant: variant}, nil
	}
	return modelRef{raw: raw, routeProvider: cleaned[0], routeModel: strings.Join(cleaned[1:], "/"), variant: variant}, nil
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

func isRimeArcanaModel(modelName string) bool {
	ref, err := parseModelRef(modelName)
	return err == nil && ref.routeProvider == "rime" && ref.routeModel == "arcana"
}

func normalizeLanguageForModel(modelName, language string, options map[string]any) string {
	cleaned := strings.TrimSpace(language)
	if candidate, ok := options["target_language_code"].(string); ok && strings.TrimSpace(candidate) != "" {
		cleaned = strings.TrimSpace(candidate)
	}
	ref, err := parseModelRef(modelName)
	if err != nil || ref.routeProvider != "sarvam" {
		return cleaned
	}
	if mapped := sarvamLanguageMap[strings.ToLower(cleaned)]; mapped != "" {
		return mapped
	}
	return cleaned
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

var sarvamLanguageMap = map[string]string{
	"bn": "bn-IN", "bn-in": "bn-IN",
	"en": "en-IN", "en-in": "en-IN",
	"gu": "gu-IN", "gu-in": "gu-IN",
	"hi": "hi-IN", "hi-in": "hi-IN",
	"kn": "kn-IN", "kn-in": "kn-IN",
	"ml": "ml-IN", "ml-in": "ml-IN",
	"mr": "mr-IN", "mr-in": "mr-IN",
	"od": "od-IN", "od-in": "od-IN",
	"pa": "pa-IN", "pa-in": "pa-IN",
	"ta": "ta-IN", "ta-in": "ta-IN",
	"te": "te-IN", "te-in": "te-IN",
}

func ttsAudioFromMessage(payload []byte, sampleRate int) (*tts.SynthesizedAudio, bool, error) {
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, nil
	}
	if isSLNGTTSEndEvent(message) {
		return nil, true, nil
	}
	messageType := slngString(message["type"])
	switch messageType {
	case "Audio", "audio", "audio_chunk", "chunk":
		encoded := extractSLNGAudio(message)
		if encoded == "" {
			return nil, false, nil
		}
		data, err := base64.StdEncoding.DecodeString(encoded)
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
		return nil, true, nil
	case "Error", "error":
		return nil, false, fmt.Errorf("slng tts error: %s", extractSLNGError(message))
	case "":
		if encoded := slngString(message["audio"]); encoded != "" {
			data, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return nil, slngBool(message["isFinal"]), nil
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
			return nil, true, nil
		}
		if message["error"] != nil {
			return nil, false, fmt.Errorf("slng tts error: %s", extractSLNGError(message))
		}
	}
	return nil, false, nil
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
	mu                sync.Mutex
	provider          *STT
	conn              *websocket.Conn
	language          string
	partials          bool
	sampleRate        int
	bufferSizeSeconds float64
	encoding          string
	audioBuffer       []byte
	pendingEvents     []*stt.SpeechEvent
	speechStarted     bool
	speechDuration    float64
	closed            bool
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
		if len(s.pendingEvents) > 0 {
			event := s.pendingEvents[0]
			s.pendingEvents = s.pendingEvents[1:]
			return event, nil
		}
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			return nil, err
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

type ttsStream struct {
	mu              sync.Mutex
	provider        *TTS
	conn            *websocket.Conn
	sampleRate      int
	model           string
	audioFrames     int
	audioBytes      int
	textMessages    int
	lastMessageType string
	appendTextSpace bool
	closed          bool
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
	return nil
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

func (s *ttsStream) Close() error {
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

func (s *ttsStream) Next() (*tts.SynthesizedAudio, error) {
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			return nil, s.readError(err)
		}
		if msgType == websocket.BinaryMessage {
			s.audioFrames++
			s.audioBytes += len(payload)
			s.lastMessageType = "binary"
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
			return nil, err
		}
		if audio != nil && audio.Frame != nil {
			s.audioFrames++
			s.audioBytes += len(audio.Frame.Data)
		}
		if done {
			return nil, io.EOF
		}
		if audio != nil {
			return audio, nil
		}
	}
}

func (s *ttsStream) readError(err error) error {
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) {
		return err
	}
	if closeErr.Code == websocket.CloseNormalClosure && (s.audioFrames > 0 || isRimeArcanaModel(s.model)) {
		return io.EOF
	}
	return fmt.Errorf(
		"slng tts websocket closed before completion: %w (model=%s audio_frames=%d audio_bytes=%d text_messages=%d last_message_type=%q)",
		err,
		s.model,
		s.audioFrames,
		s.audioBytes,
		s.textMessages,
		s.lastMessageType,
	)
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

func extractSLNGError(message map[string]any) string {
	for _, key := range []string{"message", "description", "error"} {
		if value := slngString(message[key]); value != "" {
			return value
		}
	}
	return "Unknown error"
}

func slngString(value any) string {
	text, _ := value.(string)
	return text
}

func slngStringDefault(value any, fallback string) string {
	if text := slngString(value); text != "" {
		return text
	}
	return fallback
}

func slngMap(value any) map[string]any {
	mapped, _ := value.(map[string]any)
	return mapped
}

func slngSlice(value any) []any {
	items, _ := value.([]any)
	return items
}

func slngFloat(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	default:
		return 0
	}
}

func slngBool(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func cloneSLNGMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
