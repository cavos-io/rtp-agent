package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

type Event interface {
	GetType() string
}

var ErrFunctionToolEventLengthMismatch = errors.New("function calls and outputs must have the same length")

type UserInputTranscribedEvent struct {
	Language   string
	Transcript string
	IsFinal    bool
	SpeakerID  string
	CreatedAt  time.Time
}

func (e *UserInputTranscribedEvent) GetType() string { return "user_input_transcribed" }

type AgentOutputTranscribedEvent struct {
	Language   string
	Transcript string
	IsFinal    bool
	CreatedAt  time.Time
}

func (e *AgentOutputTranscribedEvent) GetType() string { return "agent_output_transcribed" }

type UserTurnExceededEvent struct {
	Transcript            string
	AccumulatedTranscript string
	AccumulatedWordCount  int
	Duration              time.Duration
	CreatedAt             time.Time
}

func NewUserTurnExceededEvent(transcript string, accumulatedTranscript string, accumulatedWordCount int, duration time.Duration) *UserTurnExceededEvent {
	return &UserTurnExceededEvent{
		Transcript:            transcript,
		AccumulatedTranscript: accumulatedTranscript,
		AccumulatedWordCount:  accumulatedWordCount,
		Duration:              duration,
		CreatedAt:             time.Now(),
	}
}

func (e *UserTurnExceededEvent) GetType() string { return "user_turn_exceeded" }

type OverlappingSpeechEvent struct {
	CreatedAt          time.Time
	DetectedAt         time.Time
	IsInterruption     bool
	TotalDuration      time.Duration
	PredictionDuration time.Duration
	DetectionDelay     time.Duration
	OverlapStartedAt   *time.Time
	SpeechInput        []int16
	Probabilities      []float32
	Probability        float64
	NumRequests        int
}

func (e *OverlappingSpeechEvent) GetType() string { return "overlapping_speech" }

type ConversationItemAddedEvent struct {
	Item      llm.ChatItem
	CreatedAt time.Time
}

func (e *ConversationItemAddedEvent) GetType() string { return "conversation_item_added" }

type AgentFalseInterruptionEvent struct {
	Resumed           bool
	CreatedAt         time.Time
	Message           *llm.ChatMessage
	ExtraInstructions string
}

func NewAgentFalseInterruptionEvent(resumed bool) *AgentFalseInterruptionEvent {
	return &AgentFalseInterruptionEvent{
		Resumed:   resumed,
		CreatedAt: time.Now(),
	}
}

func (e *AgentFalseInterruptionEvent) GetType() string { return "agent_false_interruption" }

type FunctionToolsExecutedEvent struct {
	FunctionCalls       []*llm.FunctionCall
	FunctionCallOutputs []*llm.FunctionCallOutput
	CreatedAt           time.Time
	ReplyRequired       bool
	HandoffRequired     bool
}

type FunctionToolExecutionPair struct {
	FunctionCall       *llm.FunctionCall
	FunctionCallOutput *llm.FunctionCallOutput
}

func NewFunctionToolsExecutedEvent(calls []*llm.FunctionCall, outputs []*llm.FunctionCallOutput) (*FunctionToolsExecutedEvent, error) {
	if len(calls) != len(outputs) {
		return nil, ErrFunctionToolEventLengthMismatch
	}
	return &FunctionToolsExecutedEvent{
		FunctionCalls:       calls,
		FunctionCallOutputs: outputs,
		CreatedAt:           time.Now(),
	}, nil
}

func (e *FunctionToolsExecutedEvent) GetType() string { return "function_tools_executed" }

func (e *FunctionToolsExecutedEvent) Zipped() []FunctionToolExecutionPair {
	pairs := make([]FunctionToolExecutionPair, len(e.FunctionCalls))
	for i := range e.FunctionCalls {
		pairs[i] = FunctionToolExecutionPair{
			FunctionCall:       e.FunctionCalls[i],
			FunctionCallOutput: e.FunctionCallOutputs[i],
		}
	}
	return pairs
}

func (e *FunctionToolsExecutedEvent) CancelToolReply() {
	e.ReplyRequired = false
}

func (e *FunctionToolsExecutedEvent) CancelAgentHandoff() {
	e.HandoffRequired = false
}

func (e *FunctionToolsExecutedEvent) HasToolReply() bool {
	return e.ReplyRequired
}

func (e *FunctionToolsExecutedEvent) HasAgentHandoff() bool {
	return e.HandoffRequired
}

type MetricsCollectedEvent struct {
	Metrics   telemetry.AgentMetrics
	CreatedAt time.Time
}

func (e *MetricsCollectedEvent) GetType() string { return "metrics_collected" }

type SessionUsageUpdatedEvent struct {
	Usage     telemetry.UsageSummary
	CreatedAt time.Time
}

func (e *SessionUsageUpdatedEvent) GetType() string { return "session_usage_updated" }

type ErrorEvent struct {
	Error     error
	Source    any
	CreatedAt time.Time
}

func NewErrorEvent(err error, source any) *ErrorEvent {
	return &ErrorEvent{
		Error:     err,
		Source:    source,
		CreatedAt: time.Now(),
	}
}

func (e *ErrorEvent) GetType() string { return "error" }

type SpeechCreatedEvent struct {
	UserInitiated bool
	Source        string // "say" or "generate_reply"
	SpeechHandle  *SpeechHandle
	CreatedAt     time.Time
}

func (e *SpeechCreatedEvent) GetType() string { return "speech_created" }

type CloseReason string

const (
	CloseReasonError                   CloseReason = "error"
	CloseReasonJobShutdown             CloseReason = "job_shutdown"
	CloseReasonParticipantDisconnected CloseReason = "participant_disconnected"
	CloseReasonUserInitiated           CloseReason = "user_initiated"
	CloseReasonTaskCompleted           CloseReason = "task_completed"
)

type CloseEvent struct {
	Reason    CloseReason
	Error     error
	CreatedAt time.Time
}

func (e *CloseEvent) GetType() string { return "close" }

type RunContext struct {
	Session          *AgentSession
	SpeechHandle     *SpeechHandle
	FunctionCall     *llm.FunctionCall
	initialStepIndex int
}

func NewRunContext(session *AgentSession, speechHandle *SpeechHandle, functionCall *llm.FunctionCall) *RunContext {
	initialStepIndex := -1
	if speechHandle != nil {
		initialStepIndex = speechHandle.NumSteps() - 1
	}
	return &RunContext{
		Session:          session,
		SpeechHandle:     speechHandle,
		FunctionCall:     functionCall,
		initialStepIndex: initialStepIndex,
	}
}

func (r *RunContext) DisallowInterruptions() error {
	if r == nil || r.SpeechHandle == nil {
		return nil
	}
	return r.SpeechHandle.SetAllowInterruptions(false)
}

func (r *RunContext) WaitForPlayout(ctx context.Context) error {
	if r == nil || r.SpeechHandle == nil {
		return nil
	}
	if r.initialStepIndex >= 0 {
		return r.SpeechHandle.WaitForGeneration(ctx, r.initialStepIndex)
	}
	return r.SpeechHandle.Wait(ctx)
}

func (r *RunContext) Userdata() (any, error) {
	if r == nil || r.Session == nil {
		return nil, ErrAgentSessionUserdataNotSet
	}
	return r.Session.Userdata()
}

func (r *RunContext) JobContext() (any, error) {
	if r == nil || r.Session == nil {
		return nil, ErrAgentSessionJobContextNotSet
	}
	return r.Session.JobContext()
}

type contextKey string

const runContextKey contextKey = "run_context"

func WithRunContext(ctx context.Context, rc *RunContext) context.Context {
	return context.WithValue(ctx, runContextKey, rc)
}

func GetRunContext(ctx context.Context) *RunContext {
	if rc, ok := ctx.Value(runContextKey).(*RunContext); ok {
		return rc
	}
	return nil
}

// ClientEventsDispatcher manages sending Agent states to the LiveKit Room DataChannel
type ClientEventsDispatcher struct {
	room *lksdk.Room
	mu   sync.Mutex
}

func NewClientEventsDispatcher(room *lksdk.Room) *ClientEventsDispatcher {
	return &ClientEventsDispatcher{
		room: room,
	}
}

type ClientEventPayload struct {
	Type  string `json:"type"`
	State string `json:"state,omitempty"`
}

func (d *ClientEventsDispatcher) dispatchData(payload ClientEventPayload) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.room == nil || d.room.LocalParticipant == nil {
		return
	}

	b, err := json.Marshal(payload)
	if err != nil {
		logger.Logger.Errorw("Failed to marshal client event", err)
		return
	}

	// Publish to the "lk-agent-state" topic which the frontend UI components listen to
	err = d.room.LocalParticipant.PublishDataPacket(lksdk.UserData(b), lksdk.WithDataPublishReliable(true), lksdk.WithDataPublishTopic("lk-agent-state"))
	if err != nil {
		logger.Logger.Errorw("Failed to publish client event data", err)
	}
}

// DispatchAgentState emits AgentStateIdle, AgentStateThinking, AgentStateSpeaking
func (d *ClientEventsDispatcher) DispatchAgentState(state AgentState) {
	var stateStr string
	switch state {
	case AgentStateIdle:
		stateStr = "idle"
	case AgentStateThinking:
		stateStr = "thinking"
	case AgentStateSpeaking:
		stateStr = "speaking"
	default:
		return
	}

	d.dispatchData(ClientEventPayload{
		Type:  "agent_state_changed",
		State: stateStr,
	})
}

// DispatchUserState emits UserStateListening, UserStateSpeaking
func (d *ClientEventsDispatcher) DispatchUserState(state UserState) {
	var stateStr string
	switch state {
	case UserStateListening:
		stateStr = "listening"
	case UserStateSpeaking:
		stateStr = "speaking"
	default:
		return
	}

	d.dispatchData(ClientEventPayload{
		Type:  "user_state_changed",
		State: stateStr,
	})
}
