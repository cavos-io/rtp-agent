package xai

import (
	"fmt"
	"os"

	adapteropenai "github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

const (
	defaultXaiRealtimeBaseURL = "wss://api.x.ai/v1/realtime"
	defaultXaiRealtimeModel   = "grok-voice-think-fast-1.0"
	defaultXaiRealtimeVoice   = "Ara"
)

type XaiRealtimeModel struct {
	apiKey string
	model  string
	inner  *adapteropenai.RealtimeModel
}

type XaiRealtimeOption func(*xaiRealtimeOptions)

type xaiRealtimeOptions struct {
	model string
}

func WithXaiRealtimeModel(model string) XaiRealtimeOption {
	return func(options *xaiRealtimeOptions) {
		if model != "" {
			options.model = model
		}
	}
}

func NewXaiRealtimeModel(apiKey string, opts ...XaiRealtimeOption) *XaiRealtimeModel {
	if apiKey == "" {
		apiKey = os.Getenv(xaiAPIKeyEnv)
	}
	model := defaultXaiRealtimeModel
	options := xaiRealtimeOptions{}
	for _, opt := range opts {
		opt(&options)
	}
	if options.model != "" {
		model = options.model
	}
	inner := adapteropenai.NewRealtimeModel(apiKey, model,
		adapteropenai.WithOpenAIRealtimeBaseURL(defaultXaiRealtimeBaseURL),
		adapteropenai.WithOpenAIRealtimeVoice(defaultXaiRealtimeVoice),
		adapteropenai.WithOpenAIRealtimeModalities([]string{"audio"}),
		adapteropenai.WithOpenAIRealtimeInputAudioTranscription(map[string]any{}),
		adapteropenai.WithOpenAIRealtimeTurnDetection(map[string]any{
			"type":                "server_vad",
			"threshold":           0.5,
			"prefix_padding_ms":   300,
			"silence_duration_ms": 200,
			"create_response":     true,
			"interrupt_response":  true,
		}),
	)
	return &XaiRealtimeModel{
		apiKey: apiKey,
		model:  model,
		inner:  inner,
	}
}

func (m *XaiRealtimeModel) Model() string { return m.model }
func (m *XaiRealtimeModel) Provider() string {
	return "xAI Realtime API"
}

func (m *XaiRealtimeModel) Capabilities() llm.RealtimeCapabilities {
	caps := m.inner.Capabilities()
	caps.PerResponseToolChoice = false
	return caps
}

func (m *XaiRealtimeModel) Session() (llm.RealtimeSession, error) {
	if m.apiKey == "" {
		return nil, fmt.Errorf("xAI API key is required, either as argument or set XAI_API_KEY environment variable")
	}
	return m.inner.Session()
}

func (m *XaiRealtimeModel) Close() error {
	return m.inner.Close()
}
