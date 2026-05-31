package llm

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/cavos-io/conversation-worker/library/telemetry"
	"github.com/cavos-io/conversation-worker/model"
)

type ChatRole string

const (
	ChatRoleDeveloper ChatRole = "developer"
	ChatRoleSystem    ChatRole = "system"
	ChatRoleUser      ChatRole = "user"
	ChatRoleAssistant ChatRole = "assistant"
)

type ImageContent struct {
	ID              string
	Image           any
	InferenceWidth  *int
	InferenceHeight *int
	InferenceDetail string
	MimeType        string
}

type AudioContent struct {
	Frames     []any
	Transcript string
}

type ChatContent struct {
	Text  string
	Image *ImageContent
	Audio *AudioContent
}

type ChatMessage struct {
	ID                   string
	Role                 ChatRole
	Content              []ChatContent
	Interrupted          bool
	TranscriptConfidence *float64
	Extra                map[string]any
	Metrics              map[string]any
	CreatedAt            time.Time
}

func (m *ChatMessage) TextContent() string {
	var text string
	for _, c := range m.Content {
		if c.Text != "" {
			if text != "" {
				text += "\n"
			}
			text += c.Text
		}
	}
	return text
}

type FunctionCall struct {
	ID        string
	CallID    string
	Name      string
	Arguments string
	Extra     map[string]any
	GroupID   *string
	CreatedAt time.Time
}

type FunctionCallOutput struct {
	ID        string
	CallID    string
	Name      string
	Output    string
	IsError   bool
	CreatedAt time.Time
}

type AgentHandoff struct {
	ID         string
	OldAgentID *string
	NewAgentID string
	CreatedAt  time.Time
}

type AgentConfigUpdate struct {
	ID           string
	Instructions *string
	ToolsAdded   []string
	ToolsRemoved []string
	CreatedAt    time.Time
}

type ChatItem interface {
	GetID() string
	GetType() string
	GetCreatedAt() time.Time
}

func (m *ChatMessage) GetID() string                  { return m.ID }
func (m *ChatMessage) GetType() string                { return "message" }
func (m *ChatMessage) GetCreatedAt() time.Time        { return m.CreatedAt }
func (f *FunctionCall) GetID() string                 { return f.ID }
func (f *FunctionCall) GetType() string               { return "function_call" }
func (f *FunctionCall) GetCreatedAt() time.Time       { return f.CreatedAt }
func (f *FunctionCallOutput) GetID() string           { return f.ID }
func (f *FunctionCallOutput) GetType() string         { return "function_call_output" }
func (f *FunctionCallOutput) GetCreatedAt() time.Time { return f.CreatedAt }
func (a *AgentHandoff) GetID() string                 { return a.ID }
func (a *AgentHandoff) GetType() string               { return "agent_handoff" }
func (a *AgentHandoff) GetCreatedAt() time.Time       { return a.CreatedAt }
func (a *AgentConfigUpdate) GetID() string            { return a.ID }
func (a *AgentConfigUpdate) GetType() string          { return "agent_config_update" }
func (a *AgentConfigUpdate) GetCreatedAt() time.Time  { return a.CreatedAt }

type MetricsReport struct {
	Usage     telemetry.UsageSummary
	CreatedAt time.Time
}

func (m *MetricsReport) GetID() string           { return "" }
func (m *MetricsReport) GetType() string         { return "metrics_report" }
func (m *MetricsReport) GetCreatedAt() time.Time { return m.CreatedAt }

type ChatContext struct {
	Items []ChatItem
}

func NewChatContext() *ChatContext {
	return &ChatContext{
		Items: make([]ChatItem, 0),
	}
}

func (c *ChatContext) Append(item ChatItem) {
	c.Items = append(c.Items, item)

	// Emit OTLP log event
	attrs := map[string]interface{}{
		"item_id": item.GetID(),
		"type":    item.GetType(),
	}

	switch v := item.(type) {
	case *ChatMessage:
		attrs["role"] = string(v.Role)
		attrs["content"] = v.TextContent()
	case *FunctionCall:
		attrs["function_name"] = v.Name
		attrs["function_arguments"] = v.Arguments
	case *FunctionCallOutput:
		attrs["function_name"] = v.Name
		attrs["function_output"] = v.Output
		attrs["is_error"] = v.IsError
	}

	telemetry.RecordChatEvent(context.Background(), item.GetType(), "chat item appended", attrs)
}

type CompletionUsage struct {
	CompletionTokens   int
	PromptTokens       int
	PromptCachedTokens int
	TotalTokens        int
}

type ChoiceDelta struct {
	Role      ChatRole
	Content   string
	ToolCalls []FunctionToolCall
	Extra     map[string]any
}

type FunctionToolCall struct {
	Type      string
	Name      string
	Arguments string
	CallID    string
	Extra     map[string]any
}

type ChatChunk struct {
	ID    string
	Delta *ChoiceDelta
	Usage *CompletionUsage
}

type Tool interface {
	ID() string
	Name() string
	Description() string
	Parameters() map[string]any
	Execute(ctx context.Context, args string) (string, error)
}

type Toolset interface {
	ID() string
	Tools() []Tool
}

type ToolChoice any

type ChatOptions struct {
	Tools             []Tool
	ToolChoice        ToolChoice
	ParallelToolCalls bool
	ExtraParams       map[string]any
}

type LLM interface {
	Chat(ctx context.Context, chatCtx *ChatContext, opts ...ChatOption) (LLMStream, error)
}

type LLMStream interface {
	Next() (*ChatChunk, error)
	Close() error
}

type ChatOption func(*ChatOptions)

func WithTools(tools []Tool) ChatOption {
	return func(o *ChatOptions) {
		o.Tools = tools
	}
}

func WithToolChoice(choice ToolChoice) ChatOption {
	return func(o *ChatOptions) {
		o.ToolChoice = choice
	}
}

func WithParallelToolCalls(parallel bool) ChatOption {
	return func(o *ChatOptions) {
		o.ParallelToolCalls = parallel
	}
}

func WithExtraParams(params map[string]any) ChatOption {
	return func(o *ChatOptions) {
		o.ExtraParams = cloneAnyMap(params)
	}
}

func cloneAnyMap(params map[string]any) map[string]any {
	if params == nil {
		return nil
	}
	clone := make(map[string]any, len(params))
	for k, v := range params {
		clone[k] = v
	}
	return clone
}

// Realtime Models

type RealtimeCapabilities struct {
	MessageTruncation       bool
	TurnDetection           bool
	UserTranscription       bool
	AutoToolReplyGeneration bool
	AudioOutput             bool
}

type RealtimeModel interface {
	Session() (RealtimeSession, error)
	Close() error
}

type RealtimeSession interface {
	UpdateInstructions(instructions string) error
	UpdateChatContext(chatCtx *ChatContext) error
	UpdateTools(tools []Tool) error
	Interrupt() error
	Close() error
	EventCh() <-chan RealtimeEvent
	PushAudio(frame *model.AudioFrame) error
}

type RealtimeEventType string

const (
	RealtimeEventTypeAudio         RealtimeEventType = "audio"
	RealtimeEventTypeText          RealtimeEventType = "text"
	RealtimeEventTypeFunctionCall  RealtimeEventType = "function_call"
	RealtimeEventTypeSpeechStarted RealtimeEventType = "speech_started"
	RealtimeEventTypeSpeechStopped RealtimeEventType = "speech_stopped"
	RealtimeEventTypeError         RealtimeEventType = "error"
)

type RealtimeEvent struct {
	Type     RealtimeEventType
	Data     []byte // For audio frames
	Text     string // For text deltas
	Function *FunctionToolCall
	Error    error
}

// Fallback Adapter

type FallbackAdapter struct {
	llms             []LLM
	maxRetryPerLLM   int
	retryOnChunkSent bool
}

type FallbackAdapterOptions struct {
	MaxRetryPerLLM   int
	RetryOnChunkSent bool
}

func NewFallbackAdapter(llms []LLM) *FallbackAdapter {
	return NewFallbackAdapterWithOptions(llms, FallbackAdapterOptions{})
}

func NewFallbackAdapterWithOptions(llms []LLM, options FallbackAdapterOptions) *FallbackAdapter {
	if len(llms) == 0 {
		panic("FallbackAdapter requires at least one LLM")
	}
	return &FallbackAdapter{
		llms:             llms,
		maxRetryPerLLM:   options.MaxRetryPerLLM,
		retryOnChunkSent: options.RetryOnChunkSent,
	}
}

func (f *FallbackAdapter) Chat(ctx context.Context, chatCtx *ChatContext, opts ...ChatOption) (LLMStream, error) {
	stream := &fallbackLLMStream{
		adapter: f,
		ctx:     ctx,
		chatCtx: chatCtx,
		opts:    opts,
	}
	if err := stream.tryStart(0); err != nil {
		return nil, err
	}
	return stream, nil
}

type fallbackLLMStream struct {
	adapter *FallbackAdapter
	ctx     context.Context
	chatCtx *ChatContext
	opts    []ChatOption

	activeStream LLMStream
	activeIndex  int
	retries      map[int]int
	outputSent   bool
	closed       bool
}

func (s *fallbackLLMStream) tryStart(index int) error {
	if s.retries == nil {
		s.retries = make(map[int]int)
	}
	var lastErr error
	for i := index; i < len(s.adapter.llms); i++ {
		for {
			stream, err := s.adapter.llms[i].Chat(s.ctx, s.chatCtx, s.opts...)
			if err == nil {
				s.activeStream = stream
				s.activeIndex = i
				return nil
			}
			lastErr = err
			if !s.canRetryLLM(i) {
				break
			}
			s.retries[i]++
		}
	}
	return lastErr
}

func (s *fallbackLLMStream) Next() (*ChatChunk, error) {
	for {
		chunk, err := s.activeStream.Next()
		if err == nil {
			if chunkHasVisibleOutput(chunk) {
				s.outputSent = true
			}
			return chunk, nil
		}
		if errors.Is(err, io.EOF) || (s.outputSent && !s.adapter.retryOnChunkSent) {
			return nil, err
		}

		_ = s.activeStream.Close()
		if s.canRetryLLM(s.activeIndex) {
			s.retries[s.activeIndex]++
			if startErr := s.tryStart(s.activeIndex); startErr != nil {
				return nil, startErr
			}
			continue
		}
		if s.activeIndex+1 >= len(s.adapter.llms) {
			return nil, err
		}
		if startErr := s.tryStart(s.activeIndex + 1); startErr != nil {
			return nil, startErr
		}
	}
}

func (s *fallbackLLMStream) canRetryLLM(index int) bool {
	if s.adapter.maxRetryPerLLM <= 0 {
		return false
	}
	if s.retries == nil {
		s.retries = make(map[int]int)
	}
	return s.retries[index] < s.adapter.maxRetryPerLLM
}

func chunkHasVisibleOutput(chunk *ChatChunk) bool {
	if chunk == nil || chunk.Delta == nil {
		return false
	}
	return chunk.Delta.Content != "" || len(chunk.Delta.ToolCalls) > 0
}

func (s *fallbackLLMStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if s.activeStream == nil {
		return nil
	}
	return s.activeStream.Close()
}
