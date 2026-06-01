package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
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

type Instructions struct {
	Audio     string
	Text      string
	represent string
	textSet   bool
}

func NewInstructions(audio string, text ...string) *Instructions {
	textVariant := audio
	if len(text) > 0 {
		textVariant = text[0]
	}
	return &Instructions{
		Audio:     audio,
		Text:      textVariant,
		represent: audio,
		textSet:   len(text) > 0,
	}
}

func (i *Instructions) String() string {
	if i == nil {
		return ""
	}
	if i.represent != "" {
		return i.represent
	}
	return i.Audio
}

func (i *Instructions) AsModality(modality string) *Instructions {
	if i == nil {
		return nil
	}
	represent := i.Audio
	if modality == "text" {
		represent = i.Text
	}
	return &Instructions{
		Audio:     i.Audio,
		Text:      i.Text,
		represent: represent,
		textSet:   i.textSet,
	}
}

func (i *Instructions) Format(args ...any) *Instructions {
	if i == nil {
		return nil
	}

	audioArgs := make([]any, len(args))
	textArgs := make([]any, len(args))
	representArgs := make([]any, len(args))
	usesInstructions := false
	for idx, arg := range args {
		if instructions, ok := arg.(*Instructions); ok {
			usesInstructions = true
			audioArgs[idx] = instructions.Audio
			textArgs[idx] = instructions.Text
			representArgs[idx] = instructions.String()
			continue
		}
		audioArgs[idx] = arg
		textArgs[idx] = arg
		representArgs[idx] = arg
	}

	textSet := i.textSet || usesInstructions || i.Text != i.Audio
	text := fmt.Sprintf(i.Audio, audioArgs...)
	if textSet {
		text = fmt.Sprintf(i.Text, textArgs...)
	}

	return &Instructions{
		Audio:     fmt.Sprintf(i.Audio, audioArgs...),
		Text:      text,
		represent: fmt.Sprintf(i.String(), representArgs...),
		textSet:   textSet,
	}
}

func (i *Instructions) Concat(other *Instructions) *Instructions {
	if i == nil {
		return other
	}
	if other == nil {
		return i
	}

	textSet := i.textSet || other.textSet || i.Text != i.Audio || other.Text != other.Audio
	text := i.Audio + other.Audio
	if textSet {
		text = i.Text + other.Text
	}

	return &Instructions{
		Audio:     i.Audio + other.Audio,
		Text:      text,
		represent: i.String() + other.String(),
		textSet:   textSet,
	}
}

func (i *Instructions) AppendString(suffix string) *Instructions {
	if i == nil {
		return nil
	}

	textSet := i.textSet || i.Text != i.Audio
	text := i.Audio + suffix
	if textSet {
		text = i.Text + suffix
	}

	return &Instructions{
		Audio:     i.Audio + suffix,
		Text:      text,
		represent: i.String() + suffix,
		textSet:   textSet,
	}
}

func (i *Instructions) PrependString(prefix string) *Instructions {
	if i == nil {
		return nil
	}

	textSet := i.textSet || i.Text != i.Audio
	text := prefix + i.Audio
	if textSet {
		text = prefix + i.Text
	}

	return &Instructions{
		Audio:     prefix + i.Audio,
		Text:      text,
		represent: prefix + i.String(),
		textSet:   textSet,
	}
}

type ChatContent struct {
	Text         string
	Image        *ImageContent
	Audio        *AudioContent
	Instructions *Instructions
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
	var parts []string
	for _, c := range m.Content {
		if c.Text != "" {
			parts = append(parts, c.Text)
		} else if c.Instructions != nil && c.Instructions.String() != "" {
			parts = append(parts, c.Instructions.String())
		}
	}
	out := ""
	for _, part := range parts {
		if out != "" {
			out += "\n"
		}
		out += part
	}
	return out
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

type ProviderTool interface {
	Tool
	IsProviderTool() bool
}

type ToolError struct {
	Message string
}

func NewToolError(message string) ToolError {
	return ToolError{Message: message}
}

func (e ToolError) Error() string {
	return e.Message
}

type StopResponse struct{}

func (s StopResponse) Error() string {
	return "stop response"
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
	ResponseFormat    map[string]any
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

func WithResponseFormat(format map[string]any) ChatOption {
	return func(o *ChatOptions) {
		o.ResponseFormat = cloneAnyMap(format)
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
	attemptTimeout   time.Duration
	maxRetryPerLLM   int
	retryInterval    time.Duration
	retryOnChunkSent bool
	mu               sync.Mutex
	available        []bool
	recovering       []bool
}

type FallbackAdapterOptions struct {
	AttemptTimeout   time.Duration
	MaxRetryPerLLM   int
	RetryInterval    time.Duration
	RetryOnChunkSent bool
}

type FallbackAllFailedError struct {
	Count    int
	Duration time.Duration
	Err      error
}

func (e *FallbackAllFailedError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("all LLMs failed (%d providers) after %s", e.Count, e.Duration)
	}
	return fmt.Sprintf("all LLMs failed (%d providers) after %s: %v", e.Count, e.Duration, e.Err)
}

func (e *FallbackAllFailedError) Unwrap() error {
	return e.Err
}

const (
	defaultFallbackAttemptTimeout = 5 * time.Second
	defaultFallbackRetryInterval  = 500 * time.Millisecond
)

func NewFallbackAdapter(llms []LLM) *FallbackAdapter {
	return NewFallbackAdapterWithOptions(llms, FallbackAdapterOptions{})
}

func NewFallbackAdapterWithOptions(llms []LLM, options FallbackAdapterOptions) *FallbackAdapter {
	if len(llms) == 0 {
		panic("FallbackAdapter requires at least one LLM")
	}
	attemptTimeout := options.AttemptTimeout
	if attemptTimeout <= 0 {
		attemptTimeout = defaultFallbackAttemptTimeout
	}
	retryInterval := options.RetryInterval
	if retryInterval <= 0 {
		retryInterval = defaultFallbackRetryInterval
	}
	return &FallbackAdapter{
		llms:             llms,
		attemptTimeout:   attemptTimeout,
		maxRetryPerLLM:   options.MaxRetryPerLLM,
		retryInterval:    retryInterval,
		retryOnChunkSent: options.RetryOnChunkSent,
		available:        initialAvailability(len(llms)),
		recovering:       make([]bool, len(llms)),
	}
}

func initialAvailability(n int) []bool {
	available := make([]bool, n)
	for i := range available {
		available[i] = true
	}
	return available
}

func (f *FallbackAdapter) isAvailable(index int, allUnavailable bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return allUnavailable || f.available[index]
}

func (f *FallbackAdapter) allUnavailable() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, available := range f.available {
		if available {
			return false
		}
	}
	return true
}

func (f *FallbackAdapter) setAvailable(index int, available bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.available[index] = available
	if available {
		f.recovering[index] = false
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
	activeCancel context.CancelFunc
	activeIndex  int
	retries      map[int]int
	outputSent   bool
	closed       bool
}

func (s *fallbackLLMStream) markUnavailable(index int, recover bool) {
	s.adapter.mu.Lock()
	s.adapter.available[index] = false
	if !recover || s.adapter.recovering[index] {
		s.adapter.mu.Unlock()
		return
	}
	s.adapter.recovering[index] = true
	llm := s.adapter.llms[index]
	chatCtx := s.chatCtx
	opts := append([]ChatOption(nil), s.opts...)
	s.adapter.mu.Unlock()

	go s.adapter.recoverLLM(index, llm, chatCtx, opts)
}

func (f *FallbackAdapter) recoverLLM(index int, llm LLM, chatCtx *ChatContext, opts []ChatOption) {
	ctx, cancel := f.attemptContext(context.Background())
	stream, err := llm.Chat(ctx, chatCtx, opts...)
	if err != nil {
		cancel()
		f.finishRecovery(index, false)
		return
	}
	defer func() {
		_ = stream.Close()
		cancel()
	}()
	for {
		_, err := stream.Next()
		if err == nil {
			continue
		}
		f.finishRecovery(index, errors.Is(err, io.EOF))
		return
	}
}

func (f *FallbackAdapter) finishRecovery(index int, available bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.available[index] = available
	f.recovering[index] = false
}

func (s *fallbackLLMStream) tryStart(index int) error {
	if s.retries == nil {
		s.retries = make(map[int]int)
	}
	start := time.Now()
	var lastErr error
	allUnavailable := s.adapter.allUnavailable()
	for i := index; i < len(s.adapter.llms); i++ {
		if !s.adapter.isAvailable(i, allUnavailable) {
			continue
		}
		for {
			ctx, cancel := s.adapter.attemptContext(s.ctx)
			stream, err := s.adapter.llms[i].Chat(ctx, s.chatCtx, s.opts...)
			if err == nil {
				s.adapter.setAvailable(i, true)
				s.closeActive()
				s.activeStream = stream
				s.activeCancel = cancel
				s.activeIndex = i
				return nil
			}
			cancel()
			lastErr = err
			if !s.canRetryLLM(i) {
				s.markUnavailable(i, true)
				break
			}
			s.retries[i]++
			if err := s.adapter.waitRetryInterval(s.ctx); err != nil {
				return err
			}
		}
	}
	if lastErr != nil {
		return &FallbackAllFailedError{
			Count:    len(s.adapter.llms),
			Duration: time.Since(start),
			Err:      lastErr,
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
		if errors.Is(err, io.EOF) {
			return nil, err
		}
		if s.outputSent && !s.adapter.retryOnChunkSent {
			s.markUnavailable(s.activeIndex, false)
			return nil, err
		}

		s.closeActive()
		if s.canRetryLLM(s.activeIndex) {
			s.retries[s.activeIndex]++
			if retryErr := s.adapter.waitRetryInterval(s.ctx); retryErr != nil {
				return nil, retryErr
			}
			if startErr := s.tryStart(s.activeIndex); startErr != nil {
				return nil, startErr
			}
			continue
		}
		s.markUnavailable(s.activeIndex, true)
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

func (f *FallbackAdapter) attemptContext(parent context.Context) (context.Context, context.CancelFunc) {
	if f.attemptTimeout <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, f.attemptTimeout)
}

func (f *FallbackAdapter) waitRetryInterval(ctx context.Context) error {
	if f.retryInterval <= 0 {
		return nil
	}
	timer := time.NewTimer(f.retryInterval)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *fallbackLLMStream) closeActive() {
	if s.activeStream != nil {
		_ = s.activeStream.Close()
		s.activeStream = nil
	}
	if s.activeCancel != nil {
		s.activeCancel()
		s.activeCancel = nil
	}
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
	err := s.activeStream.Close()
	if s.activeCancel != nil {
		s.activeCancel()
		s.activeCancel = nil
	}
	return err
}
