package xai

import (
	"fmt"
	"net/http"
	"os"
	"time"

	adapteropenai "github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
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
	model            string
	voice            string
	baseURL          string
	dialWebsocket    adapteropenai.OpenAIRealtimeWebsocketDialer
	maxSession       time.Duration
	connect          *llm.APIConnectOptions
	turnDetection    any
	turnDetectionSet bool
}

func WithXaiRealtimeModel(model string) XaiRealtimeOption {
	return func(options *xaiRealtimeOptions) {
		if model != "" {
			options.model = model
		}
	}
}

func WithXaiRealtimeVoice(voice string) XaiRealtimeOption {
	return func(options *xaiRealtimeOptions) {
		if voice != "" {
			options.voice = voice
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

func WithXaiRealtimeTurnDetection(turnDetection any) XaiRealtimeOption {
	return func(options *xaiRealtimeOptions) {
		options.turnDetection = turnDetection
		options.turnDetectionSet = true
	}
}

func WithXaiRealtimeMaxSessionDuration(duration time.Duration) XaiRealtimeOption {
	return func(options *xaiRealtimeOptions) {
		options.maxSession = duration
	}
}

func WithXaiRealtimeConnectOptions(connectOptions llm.APIConnectOptions) XaiRealtimeOption {
	return func(options *xaiRealtimeOptions) {
		options.connect = &connectOptions
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
	voice := defaultXaiRealtimeVoice
	if options.voice != "" {
		voice = options.voice
	}
	baseURL := defaultXaiRealtimeBaseURL
	if options.baseURL != "" {
		baseURL = options.baseURL
	}
	turnDetection := any(map[string]any{
		"type":                "server_vad",
		"threshold":           0.5,
		"prefix_padding_ms":   300,
		"silence_duration_ms": 200,
		"create_response":     true,
		"interrupt_response":  true,
	})
	if options.turnDetectionSet {
		turnDetection = options.turnDetection
	}
	innerOptions := []adapteropenai.OpenAIRealtimeOption{
		adapteropenai.WithOpenAIRealtimeBaseURL(baseURL),
		adapteropenai.WithOpenAIRealtimeWebsocketDialer(options.dialWebsocket),
		adapteropenai.WithOpenAIRealtimeToolFormatter(xaiRealtimeTools),
		adapteropenai.WithOpenAIRealtimeInputTranscriptionFinalHook(xaiRealtimeDeduplicateFinalInputTranscription),
		adapteropenai.WithOpenAIRealtimeRemoteItemAddedHook(xaiRealtimeAppendNilPreviousItemID),
		adapteropenai.WithOpenAIRealtimeFunctionCallFilter(xaiRealtimeKnownFunctionTool),
		adapteropenai.WithOpenAIRealtimeSessionCloseMetricsHook(xaiRealtimeSessionCloseMetrics),
		adapteropenai.WithOpenAIRealtimeVoice(voice),
		adapteropenai.WithOpenAIRealtimeModalities([]string{"audio"}),
		adapteropenai.WithOpenAIRealtimeInputAudioTranscription(map[string]any{}),
		adapteropenai.WithOpenAIRealtimeTurnDetection(turnDetection),
		adapteropenai.WithOpenAIRealtimeMaxSessionDuration(options.maxSession),
	}
	if options.connect != nil {
		innerOptions = append(innerOptions, adapteropenai.WithOpenAIRealtimeConnectOptions(*options.connect))
	}
	inner := adapteropenai.NewRealtimeModel(apiKey, model, innerOptions...)
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

func xaiRealtimeDeduplicateFinalInputTranscription(msg *llm.ChatMessage, transcription *llm.InputTranscriptionCompleted) {
	if msg == nil || transcription == nil || !transcription.IsFinal || transcription.Transcript == "" {
		return
	}
	if msg.TextContent() == transcription.Transcript {
		msg.Content = nil
	}
}

func xaiRealtimeAppendNilPreviousItemID(remote *llm.RemoteChatContext, event *llm.RemoteItemAddedEvent) {
	if remote == nil || event == nil || event.PreviousItemID != "" {
		return
	}
	items := remote.ToChatCtx().Items
	if len(items) == 0 {
		return
	}
	event.PreviousItemID = items[len(items)-1].GetID()
}

func xaiRealtimeKnownFunctionTool(tools []llm.Tool, name string) bool {
	for _, tool := range tools {
		switch tool.(type) {
		case *WebSearchTool, *XSearchTool, *FileSearchTool:
			continue
		}
		if tool.Name() == name {
			return true
		}
	}
	return false
}

func xaiRealtimeSessionCloseMetrics(duration time.Duration) *telemetry.RealtimeModelMetrics {
	return &telemetry.RealtimeModelMetrics{
		Label:           "xAI Realtime API",
		RequestID:       "session_close",
		Timestamp:       time.Now(),
		SessionDuration: duration.Seconds(),
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
