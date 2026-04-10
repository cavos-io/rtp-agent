package agent

import (
	"context"

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
	GetAgent() *Agent
	GetActivity() *AgentActivity
}

type Agent struct {
	ID           string
	Instructions string
	ChatCtx      *llm.ChatContext
	Tools        []llm.Tool

	TurnDetection TurnDetectionMode
	TurnDetector  TurnDetector
	STT           stt.STT
	VAD           vad.VAD
	LLM           llm.LLM
	TTS           tts.TTS

	AllowInterruptions        bool
	MinConsecutiveSpeechDelay float64
	UseTTSAlignedTranscript   bool
	MinEndpointingDelay       float64
	MaxEndpointingDelay       float64

	activity *AgentActivity
}

func NewAgent(instructions string) *Agent {
	return &Agent{
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

func (a *Agent) OnEnter() {}
func (a *Agent) OnExit()  {}

func (a *Agent) UpdateInstructions(ctx context.Context, instructions string) error {
	a.Instructions = instructions
	return nil
}

func (a *Agent) UpdateTools(ctx context.Context, tools []llm.Tool) error {
	a.Tools = tools
	return nil
}

func (a *Agent) Start(session *AgentSession, agentIntf AgentInterface) *AgentActivity {
	a.activity = NewAgentActivity(agentIntf, session)
	a.activity.Start()
	return a.activity
}

func (a *Agent) OnUserTurnCompleted(ctx context.Context, chatCtx *llm.ChatContext, newMsg *llm.ChatMessage) error {
	return nil
}

// AgentTask represents a sub-agent execution that returns a result
type AgentTask[T any] struct {
	Agent
	Result chan T
	Err    chan error
}

type TaskWaiter interface {
	WaitAny(ctx context.Context) (any, error)
}

func NewAgentTask[T any](instructions string) *AgentTask[T] {
	baseAgent := NewAgent(instructions)
	return &AgentTask[T]{
		Agent:  *baseAgent,
		Result: make(chan T, 1),
		Err:    make(chan error, 1),
	}
}

func (t *AgentTask[T]) Complete(result T) {
	t.Result <- result
}

func (t *AgentTask[T]) Fail(err error) {
	t.Err <- err
}

func (t *AgentTask[T]) WaitAny(ctx context.Context) (any, error) {
	select {
	case res := <-t.Result:
		return res, nil
	case err := <-t.Err:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
