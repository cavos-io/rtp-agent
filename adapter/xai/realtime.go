package xai

import (
	"fmt"
	"net/http"
	"os"

	adapteropenai "github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/gorilla/websocket"
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
	model         string
	baseURL       string
	dialWebsocket adapteropenai.OpenAIRealtimeWebsocketDialer
}

func WithXaiRealtimeModel(model string) XaiRealtimeOption {
	return func(options *xaiRealtimeOptions) {
		if model != "" {
			options.model = model
		}
	}
}

func WithXaiRealtimeBaseURL(baseURL string) XaiRealtimeOption {
	return func(options *xaiRealtimeOptions) {
		if baseURL != "" {
			options.baseURL = baseURL
		}
	}
}

func WithXaiRealtimeWebsocketDialer(dialer func(string, http.Header) (*websocket.Conn, *http.Response, error)) XaiRealtimeOption {
	return func(options *xaiRealtimeOptions) {
		options.dialWebsocket = dialer
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
	baseURL := defaultXaiRealtimeBaseURL
	if options.baseURL != "" {
		baseURL = options.baseURL
	}
	inner := adapteropenai.NewRealtimeModel(apiKey, model,
		adapteropenai.WithOpenAIRealtimeBaseURL(baseURL),
		adapteropenai.WithOpenAIRealtimeWebsocketDialer(options.dialWebsocket),
		adapteropenai.WithOpenAIRealtimeToolFormatter(xaiRealtimeTools),
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

func xaiRealtimeTools(tools []llm.Tool) []map[string]any {
	formatted := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		if providerTool := xaiProviderToolPayload(tool); providerTool != nil {
			formatted = append(formatted, providerTool)
			continue
		}
		formatted = append(formatted, map[string]any{
			"type":        "function",
			"name":        tool.Name(),
			"description": tool.Description(),
			"parameters":  llm.ToolParameters(tool),
		})
	}
	return formatted
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
