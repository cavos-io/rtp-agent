package telemetry

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

type Metadata struct {
	ModelName     string
	ModelProvider string
}

func (m *Metadata) MarshalJSON() ([]byte, error) {
	if m == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(map[string]any{
		"model_name":     m.ModelName,
		"model_provider": m.ModelProvider,
	})
}

type AgentMetrics interface {
	GetType() string
}

type LLMMetrics struct {
	Label              string
	RequestID          string
	Timestamp          time.Time
	Duration           float64
	TTFT               float64
	Cancelled          bool
	CompletionTokens   int
	PromptTokens       int
	PromptCachedTokens int
	TotalTokens        int
	TokensPerSecond    float64
	SpeechID           string
	Metadata           *Metadata
}

func (m *LLMMetrics) GetType() string { return "llm_metrics" }

func (m *LLMMetrics) MarshalJSON() ([]byte, error) {
	if m == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(map[string]any{
		"type":                 m.GetType(),
		"label":                m.Label,
		"request_id":           m.RequestID,
		"timestamp":            timeToUnixSeconds(m.Timestamp),
		"duration":             m.Duration,
		"ttft":                 m.TTFT,
		"cancelled":            m.Cancelled,
		"completion_tokens":    m.CompletionTokens,
		"prompt_tokens":        m.PromptTokens,
		"prompt_cached_tokens": m.PromptCachedTokens,
		"total_tokens":         m.TotalTokens,
		"tokens_per_second":    m.TokensPerSecond,
		"speech_id":            optionalString(m.SpeechID),
		"metadata":             m.Metadata,
	})
}

type STTMetrics struct {
	Label            string
	RequestID        string
	Timestamp        time.Time
	Duration         float64
	AudioDuration    float64
	InputTokens      int
	OutputTokens     int
	Streamed         bool
	AcquireTime      float64
	ConnectionReused bool
	Metadata         *Metadata
}

func (m *STTMetrics) GetType() string { return "stt_metrics" }

func (m *STTMetrics) MarshalJSON() ([]byte, error) {
	if m == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(map[string]any{
		"type":              m.GetType(),
		"label":             m.Label,
		"request_id":        m.RequestID,
		"timestamp":         timeToUnixSeconds(m.Timestamp),
		"duration":          m.Duration,
		"audio_duration":    m.AudioDuration,
		"input_tokens":      m.InputTokens,
		"output_tokens":     m.OutputTokens,
		"streamed":          m.Streamed,
		"acquire_time":      m.AcquireTime,
		"connection_reused": m.ConnectionReused,
		"metadata":          m.Metadata,
	})
}

type TTSMetrics struct {
	Label            string
	RequestID        string
	Timestamp        time.Time
	TTFB             float64
	Duration         float64
	AudioDuration    float64
	Cancelled        bool
	CharactersCount  int
	InputTokens      int
	OutputTokens     int
	Streamed         bool
	SegmentID        string
	SpeechID         string
	AcquireTime      float64
	ConnectionReused bool
	Metadata         *Metadata
}

func (m *TTSMetrics) GetType() string { return "tts_metrics" }

func (m *TTSMetrics) MarshalJSON() ([]byte, error) {
	if m == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(map[string]any{
		"type":              m.GetType(),
		"label":             m.Label,
		"request_id":        m.RequestID,
		"timestamp":         timeToUnixSeconds(m.Timestamp),
		"ttfb":              m.TTFB,
		"duration":          m.Duration,
		"audio_duration":    m.AudioDuration,
		"cancelled":         m.Cancelled,
		"characters_count":  m.CharactersCount,
		"input_tokens":      m.InputTokens,
		"output_tokens":     m.OutputTokens,
		"streamed":          m.Streamed,
		"segment_id":        optionalString(m.SegmentID),
		"speech_id":         optionalString(m.SpeechID),
		"acquire_time":      m.AcquireTime,
		"connection_reused": m.ConnectionReused,
		"metadata":          m.Metadata,
	})
}

type VADMetrics struct {
	Label                  string
	Timestamp              time.Time
	IdleTime               float64
	InferenceDurationTotal float64
	InferenceCount         int
	Metadata               *Metadata
}

func (m *VADMetrics) GetType() string { return "vad_metrics" }

func (m *VADMetrics) MarshalJSON() ([]byte, error) {
	if m == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(map[string]any{
		"type":                     m.GetType(),
		"label":                    m.Label,
		"timestamp":                timeToUnixSeconds(m.Timestamp),
		"idle_time":                m.IdleTime,
		"inference_duration_total": m.InferenceDurationTotal,
		"inference_count":          m.InferenceCount,
		"metadata":                 m.Metadata,
	})
}

type EOUMetrics struct {
	Timestamp                time.Time
	EndOfUtteranceDelay      float64
	TranscriptionDelay       float64
	OnUserTurnCompletedDelay float64
	SpeechID                 string
	Metadata                 *Metadata
}

func (m *EOUMetrics) GetType() string { return "eou_metrics" }

func (m *EOUMetrics) MarshalJSON() ([]byte, error) {
	if m == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(map[string]any{
		"type":                         m.GetType(),
		"timestamp":                    timeToUnixSeconds(m.Timestamp),
		"end_of_utterance_delay":       m.EndOfUtteranceDelay,
		"transcription_delay":          m.TranscriptionDelay,
		"on_user_turn_completed_delay": m.OnUserTurnCompletedDelay,
		"speech_id":                    optionalString(m.SpeechID),
		"metadata":                     m.Metadata,
	})
}

type InterruptionMetrics struct {
	Label              string
	Timestamp          time.Time
	TotalDuration      float64
	PredictionDuration float64
	DetectionDelay     float64
	NumInterruptions   int
	NumBackchannels    int
	NumRequests        int
	Metadata           *Metadata
}

func (m *InterruptionMetrics) GetType() string { return "interruption_metrics" }

func (m *InterruptionMetrics) MarshalJSON() ([]byte, error) {
	if m == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(map[string]any{
		"type":                m.GetType(),
		"timestamp":           timeToUnixSeconds(m.Timestamp),
		"total_duration":      m.TotalDuration,
		"prediction_duration": m.PredictionDuration,
		"detection_delay":     m.DetectionDelay,
		"num_interruptions":   m.NumInterruptions,
		"num_backchannels":    m.NumBackchannels,
		"num_requests":        m.NumRequests,
		"metadata":            m.Metadata,
	})
}

type AvatarMetrics struct {
	Label              string
	Timestamp          time.Time
	PlaybackLatency    float64
	SessionStartedTime *time.Time
	AvatarJoinedTime   *time.Time
	Metadata           *Metadata
}

func (m *AvatarMetrics) GetType() string { return "avatar_metrics" }

func (m *AvatarMetrics) MarshalJSON() ([]byte, error) {
	if m == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(map[string]any{
		"type":                 m.GetType(),
		"timestamp":            timeToUnixSeconds(m.Timestamp),
		"playback_latency":     m.PlaybackLatency,
		"session_started_time": optionalUnixSeconds(m.SessionStartedTime),
		"avatar_joined_time":   optionalUnixSeconds(m.AvatarJoinedTime),
		"metadata":             m.Metadata,
	})
}

type CachedTokenDetails struct {
	AudioTokens int
	TextTokens  int
	ImageTokens int
}

func (d CachedTokenDetails) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"audio_tokens": d.AudioTokens,
		"text_tokens":  d.TextTokens,
		"image_tokens": d.ImageTokens,
	})
}

type InputTokenDetails struct {
	AudioTokens         int
	TextTokens          int
	ImageTokens         int
	CachedTokens        int
	CachedTokensDetails *CachedTokenDetails
}

func (d InputTokenDetails) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"audio_tokens":          d.AudioTokens,
		"text_tokens":           d.TextTokens,
		"image_tokens":          d.ImageTokens,
		"cached_tokens":         d.CachedTokens,
		"cached_tokens_details": d.CachedTokensDetails,
	})
}

type OutputTokenDetails struct {
	TextTokens  int
	AudioTokens int
	ImageTokens int
}

func (d OutputTokenDetails) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"text_tokens":  d.TextTokens,
		"audio_tokens": d.AudioTokens,
		"image_tokens": d.ImageTokens,
	})
}

type RealtimeModelMetrics struct {
	Label              string
	RequestID          string
	Timestamp          time.Time
	Duration           float64
	SessionDuration    float64
	TTFT               float64
	Cancelled          bool
	InputTokens        int
	OutputTokens       int
	TotalTokens        int
	TokensPerSecond    float64
	InputTokenDetails  InputTokenDetails
	OutputTokenDetails OutputTokenDetails
	AcquireTime        float64
	ConnectionReused   bool
	Metadata           *Metadata
}

func (m *RealtimeModelMetrics) GetType() string { return "realtime_model_metrics" }

func (m *RealtimeModelMetrics) MarshalJSON() ([]byte, error) {
	if m == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(map[string]any{
		"type":                 m.GetType(),
		"label":                m.Label,
		"request_id":           m.RequestID,
		"timestamp":            timeToUnixSeconds(m.Timestamp),
		"duration":             m.Duration,
		"session_duration":     m.SessionDuration,
		"ttft":                 m.TTFT,
		"cancelled":            m.Cancelled,
		"input_tokens":         m.InputTokens,
		"output_tokens":        m.OutputTokens,
		"total_tokens":         m.TotalTokens,
		"tokens_per_second":    m.TokensPerSecond,
		"input_token_details":  m.InputTokenDetails,
		"output_token_details": m.OutputTokenDetails,
		"acquire_time":         m.AcquireTime,
		"connection_reused":    m.ConnectionReused,
		"metadata":             m.Metadata,
	})
}

func timeToUnixSeconds(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return float64(t.UnixNano()) / 1e9
}

func optionalUnixSeconds(t *time.Time) any {
	if t == nil {
		return nil
	}
	return timeToUnixSeconds(*t)
}

func optionalString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

type ModelUsage interface {
	GetType() string
}

type LLMModelUsage struct {
	Type                   string  `json:"type"`
	Provider               string  `json:"provider"`
	Model                  string  `json:"model"`
	InputTokens            int     `json:"input_tokens"`
	InputCachedTokens      int     `json:"input_cached_tokens"`
	InputAudioTokens       int     `json:"input_audio_tokens"`
	InputCachedAudioTokens int     `json:"input_cached_audio_tokens"`
	InputTextTokens        int     `json:"input_text_tokens"`
	InputCachedTextTokens  int     `json:"input_cached_text_tokens"`
	InputImageTokens       int     `json:"input_image_tokens"`
	InputCachedImageTokens int     `json:"input_cached_image_tokens"`
	OutputTokens           int     `json:"output_tokens"`
	OutputAudioTokens      int     `json:"output_audio_tokens"`
	OutputTextTokens       int     `json:"output_text_tokens"`
	SessionDuration        float64 `json:"session_duration"`
}

func (u *LLMModelUsage) GetType() string {
	if u == nil || u.Type == "" {
		return "llm_usage"
	}
	return u.Type
}

type TTSModelUsage struct {
	Type            string  `json:"type"`
	Provider        string  `json:"provider"`
	Model           string  `json:"model"`
	InputTokens     int     `json:"input_tokens"`
	OutputTokens    int     `json:"output_tokens"`
	CharactersCount int     `json:"characters_count"`
	AudioDuration   float64 `json:"audio_duration"`
}

func (u *TTSModelUsage) GetType() string {
	if u == nil || u.Type == "" {
		return "tts_usage"
	}
	return u.Type
}

type STTModelUsage struct {
	Type          string  `json:"type"`
	Provider      string  `json:"provider"`
	Model         string  `json:"model"`
	InputTokens   int     `json:"input_tokens"`
	OutputTokens  int     `json:"output_tokens"`
	AudioDuration float64 `json:"audio_duration"`
}

func (u *STTModelUsage) GetType() string {
	if u == nil || u.Type == "" {
		return "stt_usage"
	}
	return u.Type
}

type InterruptionModelUsage struct {
	Type          string `json:"type"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	TotalRequests int    `json:"total_requests"`
}

func (u *InterruptionModelUsage) GetType() string {
	if u == nil || u.Type == "" {
		return "interruption_usage"
	}
	return u.Type
}

type AgentSessionUsage struct {
	ModelUsage []ModelUsage `json:"model_usage"`
}

func (s AgentSessionUsage) LLMInputTokens() int {
	total := 0
	for _, usage := range s.ModelUsage {
		if llmUsage, ok := usage.(*LLMModelUsage); ok {
			total += llmUsage.InputTokens
		}
	}
	return total
}

func (s AgentSessionUsage) LLMOutputTokens() int {
	total := 0
	for _, usage := range s.ModelUsage {
		if llmUsage, ok := usage.(*LLMModelUsage); ok {
			total += llmUsage.OutputTokens
		}
	}
	return total
}

type ModelUsageCollector struct {
	llmUsage          map[[2]string]*LLMModelUsage
	ttsUsage          map[[2]string]*TTSModelUsage
	sttUsage          map[[2]string]*STTModelUsage
	interruptionUsage map[[2]string]*InterruptionModelUsage
	mu                sync.Mutex
}

func NewModelUsageCollector() *ModelUsageCollector {
	return &ModelUsageCollector{
		llmUsage:          make(map[[2]string]*LLMModelUsage),
		ttsUsage:          make(map[[2]string]*TTSModelUsage),
		sttUsage:          make(map[[2]string]*STTModelUsage),
		interruptionUsage: make(map[[2]string]*InterruptionModelUsage),
	}
}

func (c *ModelUsageCollector) Collect(metrics AgentMetrics) {
	if c == nil || metrics == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	provider, model := extractMetricsProviderModel(metrics)
	switch m := metrics.(type) {
	case *LLMMetrics:
		usage := c.llmUsageFor(provider, model)
		usage.InputTokens += m.PromptTokens
		usage.InputCachedTokens += m.PromptCachedTokens
		usage.OutputTokens += m.CompletionTokens
	case *RealtimeModelMetrics:
		usage := c.llmUsageFor(provider, model)
		usage.InputTokens += m.InputTokens
		usage.InputCachedTokens += m.InputTokenDetails.CachedTokens
		usage.InputTextTokens += m.InputTokenDetails.TextTokens
		usage.InputImageTokens += m.InputTokenDetails.ImageTokens
		usage.InputAudioTokens += m.InputTokenDetails.AudioTokens
		if m.InputTokenDetails.CachedTokensDetails != nil {
			usage.InputCachedTextTokens += m.InputTokenDetails.CachedTokensDetails.TextTokens
			usage.InputCachedImageTokens += m.InputTokenDetails.CachedTokensDetails.ImageTokens
			usage.InputCachedAudioTokens += m.InputTokenDetails.CachedTokensDetails.AudioTokens
		}
		usage.OutputTextTokens += m.OutputTokenDetails.TextTokens
		usage.OutputAudioTokens += m.OutputTokenDetails.AudioTokens
		usage.OutputTokens += m.OutputTokens
		usage.SessionDuration += m.SessionDuration
	case *TTSMetrics:
		usage := c.ttsUsageFor(provider, model)
		usage.InputTokens += m.InputTokens
		usage.OutputTokens += m.OutputTokens
		usage.CharactersCount += m.CharactersCount
		usage.AudioDuration += m.AudioDuration
	case *STTMetrics:
		usage := c.sttUsageFor(provider, model)
		usage.InputTokens += m.InputTokens
		usage.OutputTokens += m.OutputTokens
		usage.AudioDuration += m.AudioDuration
	case *InterruptionMetrics:
		usage := c.interruptionUsageFor(provider, model)
		usage.TotalRequests += m.NumRequests
	}
}

func (c *ModelUsageCollector) Flatten() []ModelUsage {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	result := make([]ModelUsage, 0, len(c.llmUsage)+len(c.ttsUsage)+len(c.sttUsage)+len(c.interruptionUsage))
	for _, usage := range c.llmUsage {
		copy := *usage
		result = append(result, &copy)
	}
	for _, usage := range c.ttsUsage {
		copy := *usage
		result = append(result, &copy)
	}
	for _, usage := range c.sttUsage {
		copy := *usage
		result = append(result, &copy)
	}
	for _, usage := range c.interruptionUsage {
		copy := *usage
		result = append(result, &copy)
	}
	return result
}

func (c *ModelUsageCollector) Usage() AgentSessionUsage {
	return AgentSessionUsage{ModelUsage: c.Flatten()}
}

func (c *ModelUsageCollector) llmUsageFor(provider, model string) *LLMModelUsage {
	key := [2]string{provider, model}
	usage, ok := c.llmUsage[key]
	if !ok {
		usage = &LLMModelUsage{Type: "llm_usage", Provider: provider, Model: model}
		c.llmUsage[key] = usage
	}
	return usage
}

func (c *ModelUsageCollector) ttsUsageFor(provider, model string) *TTSModelUsage {
	key := [2]string{provider, model}
	usage, ok := c.ttsUsage[key]
	if !ok {
		usage = &TTSModelUsage{Type: "tts_usage", Provider: provider, Model: model}
		c.ttsUsage[key] = usage
	}
	return usage
}

func (c *ModelUsageCollector) sttUsageFor(provider, model string) *STTModelUsage {
	key := [2]string{provider, model}
	usage, ok := c.sttUsage[key]
	if !ok {
		usage = &STTModelUsage{Type: "stt_usage", Provider: provider, Model: model}
		c.sttUsage[key] = usage
	}
	return usage
}

func (c *ModelUsageCollector) interruptionUsageFor(provider, model string) *InterruptionModelUsage {
	key := [2]string{provider, model}
	usage, ok := c.interruptionUsage[key]
	if !ok {
		usage = &InterruptionModelUsage{Type: "interruption_usage", Provider: provider, Model: model}
		c.interruptionUsage[key] = usage
	}
	return usage
}

func extractMetricsProviderModel(metrics AgentMetrics) (string, string) {
	var metadata *Metadata
	switch m := metrics.(type) {
	case *LLMMetrics:
		metadata = m.Metadata
	case *RealtimeModelMetrics:
		metadata = m.Metadata
	case *TTSMetrics:
		metadata = m.Metadata
	case *STTMetrics:
		metadata = m.Metadata
	case *InterruptionMetrics:
		metadata = m.Metadata
	}
	if metadata == nil {
		return "", ""
	}
	return metadata.ModelProvider, metadata.ModelName
}

type UsageSummary struct {
	LLMPromptTokens           int
	LLMPromptCachedTokens     int
	LLMInputAudioTokens       int
	LLMInputCachedAudioTokens int
	LLMInputTextTokens        int
	LLMInputCachedTextTokens  int
	LLMInputImageTokens       int
	LLMInputCachedImageTokens int
	LLMCompletionTokens       int
	LLMOutputAudioTokens      int
	LLMOutputImageTokens      int
	LLMOutputTextTokens       int
	TTSCharactersCount        int
	TTSAudioDuration          float64
	STTAudioDuration          float64
}

func (s UsageSummary) LLMInputTokens() int {
	return s.LLMPromptTokens
}

func (s *UsageSummary) SetLLMInputTokens(value int) {
	if s == nil {
		return
	}
	s.LLMPromptTokens = value
}

func (s UsageSummary) LLMOutputTokens() int {
	return s.LLMCompletionTokens
}

func (s *UsageSummary) SetLLMOutputTokens(value int) {
	if s == nil {
		return
	}
	s.LLMCompletionTokens = value
}

type UsageCollector struct {
	summary UsageSummary
	mu      sync.Mutex
}

func NewUsageCollector() *UsageCollector {
	return &UsageCollector{}
}

func (c *UsageCollector) Collect(metrics AgentMetrics) {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch m := metrics.(type) {
	case *LLMMetrics:
		c.summary.LLMPromptTokens += m.PromptTokens
		c.summary.LLMPromptCachedTokens += m.PromptCachedTokens
		c.summary.LLMCompletionTokens += m.CompletionTokens
	case *RealtimeModelMetrics:
		c.summary.LLMPromptTokens += m.InputTokens
		c.summary.LLMPromptCachedTokens += m.InputTokenDetails.CachedTokens

		c.summary.LLMInputTextTokens += m.InputTokenDetails.TextTokens
		if m.InputTokenDetails.CachedTokensDetails != nil {
			c.summary.LLMInputCachedTextTokens += m.InputTokenDetails.CachedTokensDetails.TextTokens
		}
		c.summary.LLMInputImageTokens += m.InputTokenDetails.ImageTokens
		if m.InputTokenDetails.CachedTokensDetails != nil {
			c.summary.LLMInputCachedImageTokens += m.InputTokenDetails.CachedTokensDetails.ImageTokens
		}
		c.summary.LLMInputAudioTokens += m.InputTokenDetails.AudioTokens
		if m.InputTokenDetails.CachedTokensDetails != nil {
			c.summary.LLMInputCachedAudioTokens += m.InputTokenDetails.CachedTokensDetails.AudioTokens
		}

		c.summary.LLMOutputTextTokens += m.OutputTokenDetails.TextTokens
		c.summary.LLMOutputImageTokens += m.OutputTokenDetails.ImageTokens
		c.summary.LLMOutputAudioTokens += m.OutputTokenDetails.AudioTokens
		c.summary.LLMCompletionTokens += m.OutputTokens
	case *TTSMetrics:
		c.summary.TTSCharactersCount += m.CharactersCount
		c.summary.TTSAudioDuration += m.AudioDuration
	case *STTMetrics:
		c.summary.STTAudioDuration += m.AudioDuration
	}
}

func (c *UsageCollector) GetSummary() UsageSummary {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.summary
}

type MetricLabels struct {
	AgentName           string
	AgentVersion        string
	RoomName            string
	ParticipantIdentity string
}

type MetricRegistry struct {
	usageCollectors map[string]*UsageCollector
	mu              sync.Mutex
}

func NewMetricRegistry() *MetricRegistry {
	return &MetricRegistry{
		usageCollectors: make(map[string]*UsageCollector),
	}
}

func (r *MetricRegistry) GetUsageCollector(labels MetricLabels) *UsageCollector {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := fmt.Sprintf("%s:%s:%s", labels.AgentName, labels.RoomName, labels.ParticipantIdentity)
	if c, ok := r.usageCollectors[key]; ok {
		return c
	}

	c := NewUsageCollector()
	r.usageCollectors[key] = c
	return c
}
