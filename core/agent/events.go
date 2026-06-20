package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
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
		"speaker_id": optionalStringValue(e.SpeakerID),
		"language":   optionalStringValue(e.Language),
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

type AgentReasoningTranscribedEvent struct {
	Text      string
	IsFinal   bool
	CreatedAt time.Time
}

func (e *AgentReasoningTranscribedEvent) GetType() string { return "agent_reasoning_transcribed" }

func (e *AgentReasoningTranscribedEvent) MarshalJSON() ([]byte, error) {
	if e == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(map[string]any{
		"type":       e.GetType(),
		"text":       e.Text,
		"is_final":   e.IsFinal,
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

func (e *UserTurnExceededEvent) MarshalJSON() ([]byte, error) {
	if e == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(map[string]any{
		"type":                   e.GetType(),
		"transcript":             e.Transcript,
		"accumulated_transcript": e.AccumulatedTranscript,
		"accumulated_word_count": e.AccumulatedWordCount,
		"duration":               e.Duration.Seconds(),
		"created_at":             timeToUnixSeconds(e.CreatedAt),
	})
}

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

func (e *OverlappingSpeechEvent) MarshalJSON() ([]byte, error) {
	if e == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(map[string]any{
		"type":                e.GetType(),
		"created_at":          timeToUnixSeconds(e.CreatedAt),
		"detected_at":         timeToUnixSeconds(e.DetectedAt),
		"is_interruption":     e.IsInterruption,
		"total_duration":      e.TotalDuration.Seconds(),
		"prediction_duration": e.PredictionDuration.Seconds(),
		"detection_delay":     e.DetectionDelay.Seconds(),
		"overlap_started_at":  optionalTimeToUnixSeconds(e.OverlapStartedAt),
		"speech_input":        nil,
		"probabilities":       nil,
		"probability":         e.Probability,
		"num_requests":        e.NumRequests,
	})
}

type ConversationItemAddedEvent struct {
	Item      llm.ChatItem
	CreatedAt time.Time
}

func (e *ConversationItemAddedEvent) GetType() string { return "conversation_item_added" }

func (e *ConversationItemAddedEvent) MarshalJSON() ([]byte, error) {
	if e == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(map[string]any{
		"type":       e.GetType(),
		"item":       chatItemReportDict(e.Item),
		"created_at": timeToUnixSeconds(e.CreatedAt),
	})
}

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

func (e *AgentFalseInterruptionEvent) MarshalJSON() ([]byte, error) {
	if e == nil {
		return json.Marshal(nil)
	}
	var extraInstructions any
	if e.ExtraInstructions != "" {
		extraInstructions = e.ExtraInstructions
	}
	return json.Marshal(map[string]any{
		"type":               e.GetType(),
		"resumed":            e.Resumed,
		"message":            e.Message,
		"extra_instructions": extraInstructions,
		"created_at":         timeToUnixSeconds(e.CreatedAt),
	})
}

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

func (e *FunctionToolsExecutedEvent) MarshalJSON() ([]byte, error) {
	if e == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(map[string]any{
		"type":                  e.GetType(),
		"function_calls":        chatItemsReportDict(e.FunctionCalls),
		"function_call_outputs": functionCallOutputsReportDict(e.FunctionCallOutputs),
		"created_at":            timeToUnixSeconds(e.CreatedAt),
	})
}

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

func (e *MetricsCollectedEvent) MarshalJSON() ([]byte, error) {
	if e == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(map[string]any{
		"type":       e.GetType(),
		"metrics":    e.Metrics,
		"created_at": timeToUnixSeconds(e.CreatedAt),
	})
}

type SessionUsageUpdatedEvent struct {
	Usage     telemetry.AgentSessionUsage
	CreatedAt time.Time
}

func (e *SessionUsageUpdatedEvent) GetType() string { return "session_usage_updated" }

func (e *SessionUsageUpdatedEvent) MarshalJSON() ([]byte, error) {
	if e == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(map[string]any{
		"type":       e.GetType(),
		"usage":      e.Usage,
		"created_at": timeToUnixSeconds(e.CreatedAt),
	})
}

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

func (e *SpeechCreatedEvent) MarshalJSON() ([]byte, error) {
	if e == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(map[string]any{
		"type":           e.GetType(),
		"user_initiated": e.UserInitiated,
		"source":         e.Source,
		"created_at":     timeToUnixSeconds(e.CreatedAt),
	})
}

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
	fillerSchedulers []*runContextFillerScheduler
	attached         bool
	updateSink       chan<- ToolExecutionOutput
	emittedUpdates   int
	mu               sync.Mutex
}

type RunContextUpdate struct {
	FunctionCall       *llm.FunctionCall
	FunctionCallOutput *llm.FunctionCallOutput
}

type FillerOptions struct {
	Text         string
	Source       func(step int) (string, bool)
	SpeechSource func(step int) (*SpeechHandle, bool)
	Delay        time.Duration
	Interval     *time.Duration
	MaxSteps     *int
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

func (r *RunContext) WithFiller(ctx context.Context, opts FillerOptions, fn func(context.Context) error) error {
	if r == nil || r.Session == nil || fn == nil {
		if fn != nil {
			if ctx == nil {
				ctx = context.Background()
			}
			return fn(ctx)
		}
		return nil
	}
	if opts.Delay < 0 {
		return fmt.Errorf("delay must be non-negative")
	}
	if opts.Interval != nil && *opts.Interval < 0 {
		return fmt.Errorf("interval must be non-negative when set")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	schedulerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	scheduler := newRunContextFillerScheduler(r, opts)
	r.addFillerScheduler(scheduler)
	done := make(chan struct{})
	defer func() {
		cancel()
		<-done
		r.removeFillerScheduler(scheduler)
	}()
	go func() {
		defer close(done)
		scheduler.run(schedulerCtx)
	}()
	return fn(ctx)
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
	r.resetFillerDwell()
	if text, ok := message.(string); ok {
		template := runContextUpdateTemplate
		if len(templates) > 0 && templates[0] != "" {
			template = templates[0]
		}
		message = renderRunContextUpdateTemplate(template, r.FunctionCall, text)
	}

	r.mu.Lock()
	updateStep := len(r.updates)
	call := copyRunContextUpdateCall(r.FunctionCall, runContextUpdateCallIDSuffix(updateStep))
	if updateStep == 0 && r.attached {
		if r.FunctionCall.Extra == nil {
			r.FunctionCall.Extra = make(map[string]any)
		}
		r.FunctionCall.Extra["__livekit_agents_tool_non_blocking"] = true
	}
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
	update := RunContextUpdate{FunctionCall: call, FunctionCallOutput: output}
	r.updates = append(r.updates, update)
	var sink chan<- ToolExecutionOutput
	if r.attached && r.updateSink != nil {
		sink = r.updateSink
		r.emittedUpdates = len(r.updates)
	}
	r.mu.Unlock()
	if sink != nil {
		sink <- toolExecutionOutputFromUpdate(update)
	}
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

func (r *RunContext) EmittedUpdateCount() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.emittedUpdates
}

func (r *RunContext) addFillerScheduler(scheduler *runContextFillerScheduler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fillerSchedulers = append(r.fillerSchedulers, scheduler)
}

func (r *RunContext) removeFillerScheduler(scheduler *runContextFillerScheduler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, existing := range r.fillerSchedulers {
		if existing == scheduler {
			r.fillerSchedulers = append(r.fillerSchedulers[:i], r.fillerSchedulers[i+1:]...)
			return
		}
	}
}

func (r *RunContext) resetFillerDwell() {
	r.mu.Lock()
	schedulers := append([]*runContextFillerScheduler(nil), r.fillerSchedulers...)
	r.mu.Unlock()
	for _, scheduler := range schedulers {
		scheduler.resetDwell()
	}
}

type runContextFillerScheduler struct {
	runCtx *RunContext
	opts   FillerOptions
	reset  chan struct{}
}

func newRunContextFillerScheduler(runCtx *RunContext, opts FillerOptions) *runContextFillerScheduler {
	return &runContextFillerScheduler{
		runCtx: runCtx,
		opts:   opts,
		reset:  make(chan struct{}, 1),
	}
}

func (s *runContextFillerScheduler) resetDwell() {
	select {
	case s.reset <- struct{}{}:
	default:
	}
}

func (s *runContextFillerScheduler) run(ctx context.Context) {
	if s == nil || s.runCtx == nil || s.runCtx.Session == nil {
		return
	}
	agentEvents := s.runCtx.Session.AgentStateChangedEvents()
	userEvents := s.runCtx.Session.UserStateChangedEvents()
	created := 0
	for {
		if err := s.runCtx.Session.WaitForInactive(ctx); err != nil {
			return
		}
		if !s.waitForDwell(ctx, agentEvents, userEvents) {
			return
		}
		text, handle, ok, sayText := s.nextFiller(created)
		if ok {
			if sayText {
				var err error
				handle, err = s.runCtx.Session.Say(ctx, text)
				if err != nil {
					return
				}
			}
			if handle != nil {
				created++
			}
		}
		if s.opts.Interval == nil || (s.opts.MaxSteps != nil && created >= *s.opts.MaxSteps) {
			return
		}
		if !s.waitForInterval(ctx, *s.opts.Interval, agentEvents, userEvents) {
			return
		}
	}
}

func (s *runContextFillerScheduler) nextFiller(step int) (string, *SpeechHandle, bool, bool) {
	if s.opts.SpeechSource != nil {
		handle, ok := s.opts.SpeechSource(step)
		return "", handle, ok, false
	}
	if s.opts.Source != nil {
		text, ok := s.opts.Source(step)
		return text, nil, ok, true
	}
	return s.opts.Text, nil, true, true
}

func (s *runContextFillerScheduler) waitForDwell(ctx context.Context, agentEvents <-chan AgentStateChangedEvent, userEvents <-chan UserStateChangedEvent) bool {
	timer := time.NewTimer(s.opts.Delay)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return true
		case <-ctx.Done():
			return false
		case <-s.interruptDone():
			return false
		case <-s.reset:
			resetTimer(timer, s.opts.Delay)
		case ev := <-agentEvents:
			if ev.NewState == AgentStateSpeaking || ev.NewState == AgentStateThinking {
				resetTimer(timer, s.opts.Delay)
			}
		case ev := <-userEvents:
			if ev.NewState == UserStateSpeaking {
				resetTimer(timer, s.opts.Delay)
			}
		}
	}
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(duration)
}

func (s *runContextFillerScheduler) waitForInterval(ctx context.Context, interval time.Duration, agentEvents <-chan AgentStateChangedEvent, userEvents <-chan UserStateChangedEvent) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return true
		case <-ctx.Done():
			return false
		case <-s.interruptDone():
			return false
		case <-s.reset:
		case <-agentEvents:
		case <-userEvents:
		}
	}
}

func (s *runContextFillerScheduler) interruptDone() <-chan struct{} {
	if s.runCtx == nil || s.runCtx.SpeechHandle == nil {
		return nil
	}
	return s.runCtx.SpeechHandle.interruptCh
}

func (r *RunContext) attach(updateSink ...chan<- ToolExecutionOutput) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.attached = true
	if len(updateSink) > 0 {
		r.updateSink = updateSink[0]
	}
	r.mu.Unlock()
}

func (r *RunContext) detach() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.attached = false
	r.updateSink = nil
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

func ClientAgentStateString(state AgentState) (string, bool) {
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

func ClientUserStateString(state UserState) (string, bool) {
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
