package agent

import (
	"context"
	"time"

	"github.com/cavos-io/conversation-worker/core/llm"
	"github.com/cavos-io/conversation-worker/core/stt"
	"github.com/cavos-io/conversation-worker/core/tts"
	"github.com/cavos-io/conversation-worker/core/vad"
	"github.com/cavos-io/conversation-worker/model"
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

type LLMNodeFunc func(ctx context.Context, l llm.LLM, chatCtx *llm.ChatContext, tools []llm.Tool) (*LLMGenerationData, error)
type TTSNodeFunc func(ctx context.Context, t tts.TTS, textCh <-chan string) (*TTSGenerationData, error)
type STTNodeFunc func(ctx context.Context, s stt.STT, audio <-chan *model.AudioFrame) (<-chan *stt.SpeechEvent, error)
type TranscriptionNodeFunc func(ctx context.Context, textCh <-chan string) (<-chan string, error)
type RealtimeAudioOutputNodeFunc func(ctx context.Context, audio <-chan *model.AudioFrame) (<-chan *model.AudioFrame, error)

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

	LLMNode                 LLMNodeFunc
	TTSNode                 TTSNodeFunc
	STTNode                 STTNodeFunc
	TranscriptionNode       TranscriptionNodeFunc
	RealtimeAudioOutputNode RealtimeAudioOutputNodeFunc

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
	if newMsg == nil {
		return nil
	}

	if newMsg.CreatedAt.IsZero() {
		newMsg.CreatedAt = time.Now()
	}

	targetCtx := chatCtx
	if targetCtx == nil {
		targetCtx = a.ChatCtx
	}
	if targetCtx == nil {
		targetCtx = llm.NewChatContext()
		a.ChatCtx = targetCtx
	}

	targetCtx.Append(newMsg)

	if a.activity == nil || a.activity.Session == nil {
		return nil
	}

	handle := NewSpeechHandle(a.activity.Session.Options.AllowInterruptions, DefaultInputDetails())
	if err := a.activity.ScheduleSpeech(handle, SpeechPriorityNormal, false); err != nil {
		return err
	}

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
