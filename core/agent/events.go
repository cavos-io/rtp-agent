package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

var ErrFunctionToolEventLengthMismatch = functionToolEventLengthMismatchError("The number of function_calls and function_call_outputs must match.")

type functionToolEventLengthMismatchError string

func (e functionToolEventLengthMismatchError) Error() string {
	return string(e)
}

type UserInputTranscribedEvent struct {
	Language   string
	Transcript string
	IsFinal    bool
	SpeakerID  string
	CreatedAt  time.Time
}

func (e *UserInputTranscribedEvent) GetType() string { return "user_input_transcribed" }

func (e *UserInputTranscribedEvent) MarshalJSON() ([]byte, error) {
	if e == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(map[string]any{
		"type":       e.GetType(),
		"transcript": e.Transcript,
		"is_final":   e.IsFinal,
		"speaker_id": e.SpeakerID,
		"language":   e.Language,
		"created_at": timeToUnixSeconds(e.CreatedAt),
	})
}

type AgentOutputTranscribedEvent struct {
	Language   string
	Transcript string
	IsFinal    bool
	CreatedAt  time.Time
}

func (e *AgentOutputTranscribedEvent) GetType() string { return "agent_output_transcribed" }

func (e *AgentOutputTranscribedEvent) MarshalJSON() ([]byte, error) {
	if e == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(map[string]any{
		"type":       e.GetType(),
		"transcript": e.Transcript,
		"is_final":   e.IsFinal,
		"language":   e.Language,
		"created_at": timeToUnixSeconds(e.CreatedAt),
	})
}

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
	Usage     telemetry.AgentSessionUsage
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

func (e *ErrorEvent) MarshalJSON() ([]byte, error) {
	if e == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(map[string]any{
		"type":       e.GetType(),
		"error":      errorReportValue(e.Error),
		"source":     errorReportSourceValue(e.Source),
		"created_at": timeToUnixSeconds(e.CreatedAt),
	})
}

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

func (e *CloseEvent) MarshalJSON() ([]byte, error) {
	if e == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(map[string]any{
		"type":       e.GetType(),
		"reason":     e.Reason,
		"error":      errorReportValue(e.Error),
		"created_at": timeToUnixSeconds(e.CreatedAt),
	})
}

type RunContext struct {
	Session          *AgentSession
	SpeechHandle     *SpeechHandle
	FunctionCall     *llm.FunctionCall
	initialStepIndex int
	updates          []RunContextUpdate
	attached         bool
	detached         bool
	mu               sync.Mutex
}

type RunContextUpdate struct {
	FunctionCall       *llm.FunctionCall
	FunctionCallOutput *llm.FunctionCallOutput
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

func (r *RunContext) Foreground(ctx context.Context, fn func(context.Context) error) error {
	if r == nil || r.Session == nil || fn == nil {
		return nil
	}
	return r.Session.WaitForInactiveAndHold(ctx, fn)
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

const runContextUpdateTemplate = "The tool `{function_name}` has updated, message: {message}\nThe task is still running, so DON'T make up or give information not included in the message above."

func (r *RunContext) Update(message any, templates ...string) error {
	if r == nil || r.FunctionCall == nil {
		return nil
	}
	if text, ok := message.(string); ok {
		template := runContextUpdateTemplate
		if len(templates) > 0 && templates[0] != "" {
			template = templates[0]
		}
		message = renderRunContextUpdateTemplate(template, r.FunctionCall, text)
	}

	r.mu.Lock()
	if r.detached {
		r.mu.Unlock()
		return nil
	}
	updateStep := len(r.updates)
	if updateStep == 0 && r.attached {
		if r.FunctionCall.Extra == nil {
			r.FunctionCall.Extra = make(map[string]any)
		}
		r.FunctionCall.Extra["__livekit_agents_tool_non_blocking"] = true
	}
	call := copyRunContextUpdateCall(r.FunctionCall, runContextUpdateCallIDSuffix(updateStep))
	result := llm.MakeToolOutput(*call, message, nil)
	output := result.FncCallOut
	if output == nil {
		output = &llm.FunctionCallOutput{
			CallID:    call.CallID,
			Name:      call.Name,
			Output:    fmt.Sprint(message),
			IsError:   false,
			CreatedAt: time.Now(),
		}
	}
	r.updates = append(r.updates, RunContextUpdate{FunctionCall: call, FunctionCallOutput: output})
	r.mu.Unlock()
	return nil
}

func (r *RunContext) Updates() []RunContextUpdate {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	updates := make([]RunContextUpdate, len(r.updates))
	copy(updates, r.updates)
	return updates
}

func (r *RunContext) attach() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.attached = true
	r.detached = false
	r.mu.Unlock()
}

func (r *RunContext) detach() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.attached = false
	r.detached = true
	r.mu.Unlock()
}

func renderRunContextUpdateTemplate(template string, call *llm.FunctionCall, message string) string {
	return strings.NewReplacer(
		"{function_name}", call.Name,
		"{call_id}", call.CallID,
		"{message}", message,
	).Replace(template)
}

func runContextUpdateCallIDSuffix(updateStep int) string {
	if updateStep == 0 {
		return ""
	}
	return fmt.Sprintf("_update_%d", updateStep)
}

func copyRunContextUpdateCall(call *llm.FunctionCall, callIDSuffix string) *llm.FunctionCall {
	updateCall := *call
	updateCall.CallID = call.CallID + callIDSuffix
	if call.Extra != nil {
		updateCall.Extra = make(map[string]any, len(call.Extra))
		for key, value := range call.Extra {
			updateCall.Extra[key] = value
		}
	}
	return &updateCall
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

	if d.room == nil || d.room.LocalParticipant == nil || d.room.ConnectionState() != lksdk.ConnectionStateConnected {
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

func clientAgentStateString(state AgentState) (string, bool) {
	switch state {
	case AgentStateInitializing:
		return "initializing", true
	case AgentStateIdle, AgentStateListening:
		return "listening", true
	case AgentStateThinking:
		return "thinking", true
	case AgentStateSpeaking:
		return "speaking", true
	default:
		return "", false
	}
}

// DispatchAgentState emits reference-style client agent states.
func (d *ClientEventsDispatcher) DispatchAgentState(state AgentState) {
	stateStr, ok := clientAgentStateString(state)
	if !ok {
		return
	}

	d.dispatchData(ClientEventPayload{
		Type:  "agent_state_changed",
		State: stateStr,
	})
}

func clientUserStateString(state UserState) (string, bool) {
	switch state {
	case UserStateListening:
		return "listening", true
	case UserStateSpeaking:
		return "speaking", true
	case UserStateAway:
		return "away", true
	default:
		return "", false
	}
}

// DispatchUserState emits reference-style client user states.
func (d *ClientEventsDispatcher) DispatchUserState(state UserState) {
	stateStr, ok := clientUserStateString(state)
	if !ok {
		return
	}

	d.dispatchData(ClientEventPayload{
		Type:  "user_state_changed",
		State: stateStr,
	})
}
