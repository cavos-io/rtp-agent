package telemetry

import (
	"fmt"
	"sync"
	"time"
)

type Metadata struct {
	ModelName     string
	ModelProvider string
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

type STTMetrics struct {
	Label         string
	RequestID     string
	Timestamp     time.Time
	Duration      float64
	AudioDuration float64
	Streamed      bool
	Metadata      *Metadata
}

func (m *STTMetrics) GetType() string { return "stt_metrics" }

type TTSMetrics struct {
	Label           string
	RequestID       string
	Timestamp       time.Time
	TTFB            float64
	Duration        float64
	AudioDuration   float64
	Cancelled       bool
	CharactersCount int
	Streamed        bool
	SegmentID       string
	SpeechID        string
	Metadata        *Metadata
}

func (m *TTSMetrics) GetType() string { return "tts_metrics" }

type VADMetrics struct {
	Label                  string
	Timestamp              time.Time
	IdleTime               float64
	InferenceDurationTotal float64
	InferenceCount         int
	Metadata               *Metadata
}

func (m *VADMetrics) GetType() string { return "vad_metrics" }

type EOUMetrics struct {
	Timestamp                time.Time
	EndOfUtteranceDelay      float64
	TranscriptionDelay       float64
	OnUserTurnCompletedDelay float64
	SpeechID                 string
	Metadata                 *Metadata
}

func (m *EOUMetrics) GetType() string { return "eou_metrics" }

type CachedTokenDetails struct {
	AudioTokens int
	TextTokens  int
	ImageTokens int
}

type InputTokenDetails struct {
	AudioTokens         int
	TextTokens          int
	ImageTokens         int
	CachedTokens        int
	CachedTokensDetails *CachedTokenDetails
}

type OutputTokenDetails struct {
	TextTokens  int
	AudioTokens int
	ImageTokens int
}

type RealtimeModelMetrics struct {
	Label              string
	RequestID          string
	Timestamp          time.Time
	Duration           float64
	TTFT               float64
	Cancelled          bool
	InputTokens        int
	OutputTokens       int
	TotalTokens        int
	TokensPerSecond    float64
	InputTokenDetails  InputTokenDetails
	OutputTokenDetails OutputTokenDetails
	Metadata           *Metadata
}

func (m *RealtimeModelMetrics) GetType() string { return "realtime_model_metrics" }

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

func (m *UsageSummary) GetType() string { return "usage_summary" }

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
	mu             sync.Mutex
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
