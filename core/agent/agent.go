package agent

import (
	"context"
	"errors"
	"sync"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/core/vad"
)

type TurnDetectionMode string

const (
	TurnDetectionModeSTT         TurnDetectionMode = "stt"
	TurnDetectionModeVAD         TurnDetectionMode = "vad"
	TurnDetectionModeRealtimeLLM TurnDetectionMode = "realtime_llm"
	TurnDetectionModeManual      TurnDetectionMode = "manual"
)

type TurnDetector interface {
	PredictEndOfTurn(ctx context.Context, chatCtx *llm.ChatContext) (float64, error)
}

type AgentInterface interface {
	OnEnter()
	OnExit()
	OnUserTurnCompleted(ctx context.Context, chatCtx *llm.ChatContext, newMsg *llm.ChatMessage) error
	OnUserTurnExceeded(ctx context.Context, ev UserTurnExceededEvent) error
	GetAgent() *Agent
	GetActivity() *AgentActivity
}

type Agent struct {
	ID                  string
	Instructions        string
	InstructionVariants *llm.Instructions
	ChatCtx             *llm.ChatContext
	Tools               []llm.Tool

	TurnDetection TurnDetectionMode
	TurnDetector  TurnDetector
	Avatar        AvatarProvider
	STT           stt.STT
	VAD           vad.VAD
	LLM           llm.LLM
	RealtimeModel llm.RealtimeModel
	TTS           tts.TTS

	AllowInterruptions         bool
	AllowInterruptionsSet      bool
	MinConsecutiveSpeechDelay  float64
	UseTTSAlignedTranscript    bool
	UseTTSAlignedTranscriptSet bool
	MinEndpointingDelay        float64
	MaxEndpointingDelay        float64

	activity *AgentActivity
}

func NewAgent(instructions string) *Agent {
	return &Agent{
		ID:           "default_agent",
		Instructions: instructions,
		ChatCtx:      llm.NewChatContext(),
		Tools:        make([]llm.Tool, 0),
	}
}

func (a *Agent) GetAgent() *Agent {
	return a
}

func (a *Agent) GetActivity() *AgentActivity {
	return a.activity
}

func (a *Agent) Label() string {
	if a == nil {
		return ""
	}
	return a.ID
}

func (a *Agent) OnEnter() {}
func (a *Agent) OnExit()  {}

// ChatContext returns a copy of the agent's current chat context.
//
// This mirrors the reference SDK's read-only chat_ctx accessor: callers can
// inspect the returned context without mutating the agent's internal history.
// Use UpdateChatContext to replace the agent-owned context.
func (a *Agent) ChatContext() *llm.ChatContext {
	if a == nil || a.ChatCtx == nil {
		return llm.NewChatContext().ReadOnly()
	}
	return a.ChatCtx.ReadOnly()
}

func (a *Agent) UpdateInstructions(ctx context.Context, instructions string) error {
	if a.activity != nil {
		return a.activity.UpdateInstructions(ctx, instructions)
	}
	a.Instructions = instructions
	a.InstructionVariants = nil
	return nil
}

func (a *Agent) UpdateTools(ctx context.Context, tools []llm.Tool) error {
	if a.activity != nil {
		return a.activity.UpdateTools(ctx, tools)
	}
	a.Tools = dedupeAgentToolsByID(tools)
	if a.ChatCtx != nil {
		a.ChatCtx = a.ChatCtx.Copy(llm.ChatContextCopyOptions{
			Tools: agentToolsAsInterfaces(a.Tools),
		})
	}
	return nil
}

func dedupeAgentToolsByID(tools []llm.Tool) []llm.Tool {
	deduped := make([]llm.Tool, 0, len(tools))
	indexByID := make(map[string]int, len(tools))
	for _, tool := range tools {
		if idx, ok := indexByID[tool.ID()]; ok {
			deduped[idx] = tool
			continue
		}
		indexByID[tool.ID()] = len(deduped)
		deduped = append(deduped, tool)
	}
	return deduped
}

func (a *Agent) UpdateChatContext(ctx context.Context, chatCtx *llm.ChatContext, excludeInvalidFunctionCalls ...bool) error {
	return a.UpdateChatCtx(ctx, chatCtx, excludeInvalidFunctionCalls...)
}

func (a *Agent) UpdateChatCtx(ctx context.Context, chatCtx *llm.ChatContext, excludeInvalidFunctionCalls ...bool) error {
	if a.activity != nil {
		return a.activity.UpdateChatCtx(ctx, chatCtx, excludeInvalidFunctionCalls...)
	}
	excludeInvalid := true
	if len(excludeInvalidFunctionCalls) > 0 {
		excludeInvalid = excludeInvalidFunctionCalls[0]
	}
	if chatCtx == nil {
		a.ChatCtx = llm.NewChatContext()
		return nil
	}
	if !excludeInvalid {
		a.ChatCtx = chatCtx.Copy()
		return nil
	}

	a.ChatCtx = chatCtx.Copy(llm.ChatContextCopyOptions{
		Tools: agentToolsAsInterfaces(a.Tools),
	})
	return nil
}

func agentToolsAsInterfaces(tools []llm.Tool) []interface{} {
	converted := make([]interface{}, 0, len(tools))
	for _, tool := range tools {
		converted = append(converted, tool)
	}
	return converted
}

func (a *Agent) Start(session *AgentSession, agentIntf AgentInterface) *AgentActivity {
	a.activity = NewAgentActivity(agentIntf, session)
	a.activity.Start()
	return a.activity
}

func (a *Agent) OnUserTurnCompleted(ctx context.Context, chatCtx *llm.ChatContext, newMsg *llm.ChatMessage) error {
	return nil
}

const defaultUserTurnExceededInstructions = "The user has been speaking too long without giving a chance to reply. Politely cut in with a short reply or notice. Keep it short since the user cannot interrupt it."

func (a *Agent) OnUserTurnExceeded(ctx context.Context, ev UserTurnExceededEvent) error {
	if a.activity == nil || a.activity.Session == nil {
		return ErrAgentSessionNotRunning
	}
	allowInterruptions := false
	_, err := a.activity.Session.GenerateReplyWithOptions(ctx, GenerateReplyOptions{
		UserInput:          ev.Transcript,
		Instructions:       defaultUserTurnExceededInstructions,
		AllowInterruptions: &allowInterruptions,
		ToolChoice:         "none",
		InputModality:      "text",
	})
	return err
}

// AgentTask represents a sub-agent execution that returns a result
type AgentTask[T any] struct {
	Agent
	Result    chan T
	Err       chan error
	doneCh    chan struct{}
	mu        sync.Mutex
	completed bool
	waited    bool
}

type TaskWaiter interface {
	WaitAny(ctx context.Context) (any, error)
}

var ErrAgentTaskAlreadyDone = errors.New("agent task is already done")
var ErrAgentTaskAlreadyWaited = errors.New("agent task is not re-entrant, wait only once")

func NewAgentTask[T any](instructions string) *AgentTask[T] {
	baseAgent := NewAgent(instructions)
	return &AgentTask[T]{
		Agent:  *baseAgent,
		Result: make(chan T, 1),
		Err:    make(chan error, 1),
		doneCh: make(chan struct{}),
	}
}

func (t *AgentTask[T]) Complete(result T) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.completed {
		return ErrAgentTaskAlreadyDone
	}
	t.completed = true
	t.Result <- result
	close(t.doneCh)
	return nil
}

func (t *AgentTask[T]) Fail(err error) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.completed {
		return ErrAgentTaskAlreadyDone
	}
	t.completed = true
	t.Err <- err
	close(t.doneCh)
	return nil
}

func (t *AgentTask[T]) Done() bool {
	select {
	case <-t.doneCh:
		return true
	default:
		return false
	}
}

func (t *AgentTask[T]) Cancel() {
	if t.Done() {
		return
	}
	if t.activity != nil {
		_ = t.activity.Interrupt(true)
	}
	_ = t.Fail(llm.NewToolError("AgentTask " + t.ID + " is cancelled"))
}

func (t *AgentTask[T]) claimWait() error {
	if t == nil {
		return errors.New("agent task is nil")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.waited {
		return ErrAgentTaskAlreadyWaited
	}
	t.waited = true
	return nil
}

func (t *AgentTask[T]) Wait(ctx context.Context) (T, error) {
	var zero T
	if err := t.claimWait(); err != nil {
		return zero, err
	}
	select {
	case res := <-t.Result:
		return res, nil
	case err := <-t.Err:
		return zero, err
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

func (t *AgentTask[T]) WaitAny(ctx context.Context) (any, error) {
	if err := t.claimWait(); err != nil {
		return nil, err
	}
	select {
	case res := <-t.Result:
		return res, nil
	case err := <-t.Err:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
