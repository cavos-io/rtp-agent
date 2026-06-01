package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
)

func TestAgentSessionGenerateReplyReturnsScheduledSpeechHandle(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{AllowInterruptions: true})
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.GenerateReply(context.Background(), "hello")

	if err != nil {
		t.Fatalf("GenerateReply error = %v, want nil", err)
	}
	if handle == nil {
		t.Fatal("GenerateReply handle = nil, want speech handle")
	}
	if !handle.IsScheduled() {
		t.Fatal("GenerateReply returned unscheduled handle")
	}
	if !handle.AllowInterruptions {
		t.Fatal("handle.AllowInterruptions = false, want session default true")
	}
	if got, want := handle.InputDetails.Modality, "text"; got != want {
		t.Fatalf("handle.InputDetails.Modality = %q, want %q", got, want)
	}
}

func TestNewAgentSessionInitializesUserStateListening(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	if session.UserState != UserStateListening {
		t.Fatalf("UserState = %q, want %q", session.UserState, UserStateListening)
	}
}

func TestNewAgentSessionInitializesAgentStateInitializing(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	if session.AgentState != AgentStateInitializing {
		t.Fatalf("AgentState = %q, want %q", session.AgentState, AgentStateInitializing)
	}
}

func TestAgentSessionGenerateReplyEmitsSpeechCreatedEvent(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{AllowInterruptions: true})
	session.activity = NewAgentActivity(agent, session)
	before := time.Now()

	handle, err := session.GenerateReply(context.Background(), "hello")

	if err != nil {
		t.Fatalf("GenerateReply error = %v, want nil", err)
	}
	select {
	case ev := <-session.SpeechCreatedEvents():
		if ev.GetType() != "speech_created" {
			t.Fatalf("event type = %q, want speech_created", ev.GetType())
		}
		if ev.SpeechHandle != handle {
			t.Fatalf("SpeechHandle = %#v, want returned handle", ev.SpeechHandle)
		}
		if !ev.UserInitiated {
			t.Fatal("UserInitiated = false, want true for GenerateReply")
		}
		if ev.Source != "generate_reply" {
			t.Fatalf("Source = %q, want generate_reply", ev.Source)
		}
		if ev.CreatedAt.Before(before) || ev.CreatedAt.IsZero() {
			t.Fatalf("CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
		}
	case <-time.After(time.Second):
		t.Fatal("SpeechCreatedEvents did not receive generate reply speech")
	}
}

func TestAgentSessionEmitErrorEmitsTimestampedEvent(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	cause := errors.New("provider failed")
	source := "stt"
	before := time.Now()

	session.EmitError(ErrorEvent{Error: cause, Source: source})

	select {
	case ev := <-session.ErrorEvents():
		if ev.GetType() != "error" {
			t.Fatalf("event type = %q, want error", ev.GetType())
		}
		if !errors.Is(ev.Error, cause) {
			t.Fatalf("Error = %v, want %v", ev.Error, cause)
		}
		if ev.Source != source {
			t.Fatalf("Source = %#v, want %q", ev.Source, source)
		}
		if ev.CreatedAt.Before(before) || ev.CreatedAt.IsZero() {
			t.Fatalf("CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive error event")
	}
}

func TestAgentSessionEmitAgentFalseInterruptionEmitsTimestampedEvent(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	before := time.Now()

	session.EmitAgentFalseInterruption(AgentFalseInterruptionEvent{Resumed: true})

	select {
	case ev := <-session.AgentFalseInterruptionEvents():
		if ev.GetType() != "agent_false_interruption" {
			t.Fatalf("event type = %q, want agent_false_interruption", ev.GetType())
		}
		if !ev.Resumed {
			t.Fatal("Resumed = false, want true")
		}
		if ev.CreatedAt.Before(before) || ev.CreatedAt.IsZero() {
			t.Fatalf("CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
		}
	case <-time.After(time.Second):
		t.Fatal("AgentFalseInterruptionEvents did not receive event")
	}
}

func TestAgentSessionEmitUserTurnExceededEmitsTimestampedEvent(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	before := time.Now()

	session.EmitUserTurnExceeded(UserTurnExceededEvent{
		Transcript:            "latest words",
		AccumulatedTranscript: "all words",
		AccumulatedWordCount:  2,
		Duration:              3 * time.Second,
	})

	select {
	case ev := <-session.UserTurnExceededEvents():
		if ev.GetType() != "user_turn_exceeded" {
			t.Fatalf("event type = %q, want user_turn_exceeded", ev.GetType())
		}
		if ev.Transcript != "latest words" || ev.AccumulatedTranscript != "all words" {
			t.Fatalf("event transcript fields = %#v, want latest/all words", ev)
		}
		if ev.AccumulatedWordCount != 2 || ev.Duration != 3*time.Second {
			t.Fatalf("event accumulation fields = %#v, want 2 words and 3s", ev)
		}
		if ev.CreatedAt.Before(before) || ev.CreatedAt.IsZero() {
			t.Fatalf("CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
		}
	case <-time.After(time.Second):
		t.Fatal("UserTurnExceededEvents did not receive event")
	}
}

func TestAgentSessionEmitOverlappingSpeechEmitsTimestampedEvent(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	overlapStartedAt := time.Now().Add(-100 * time.Millisecond)
	before := time.Now()

	session.EmitOverlappingSpeech(OverlappingSpeechEvent{
		IsInterruption:   true,
		DetectionDelay:   100 * time.Millisecond,
		OverlapStartedAt: &overlapStartedAt,
		Probability:      0.8,
	})

	select {
	case ev := <-session.OverlappingSpeechEvents():
		if ev.GetType() != "overlapping_speech" {
			t.Fatalf("event type = %q, want overlapping_speech", ev.GetType())
		}
		if !ev.IsInterruption || ev.DetectionDelay != 100*time.Millisecond || ev.Probability != 0.8 {
			t.Fatalf("event fields = %#v, want interruption with delay/probability", ev)
		}
		if ev.OverlapStartedAt == nil || !ev.OverlapStartedAt.Equal(overlapStartedAt) {
			t.Fatalf("OverlapStartedAt = %#v, want %v", ev.OverlapStartedAt, overlapStartedAt)
		}
		if ev.CreatedAt.Before(before) || ev.CreatedAt.IsZero() {
			t.Fatalf("CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
		}
		if ev.DetectedAt.Before(before) || ev.DetectedAt.IsZero() {
			t.Fatalf("DetectedAt = %v, want timestamp after %v", ev.DetectedAt, before)
		}
	case <-time.After(time.Second):
		t.Fatal("OverlappingSpeechEvents did not receive event")
	}
}

func TestAgentSessionGenerateReplyAddsUserInputToChatContext(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)

	if _, err := session.GenerateReply(context.Background(), "hello"); err != nil {
		t.Fatalf("GenerateReply error = %v, want nil", err)
	}

	if len(session.ChatCtx.Items) != 1 {
		t.Fatalf("ChatCtx.Items length = %d, want 1", len(session.ChatCtx.Items))
	}
	msg, ok := session.ChatCtx.Items[0].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("ChatCtx item type = %T, want *llm.ChatMessage", session.ChatCtx.Items[0])
	}
	if msg.Role != llm.ChatRoleUser || msg.TextContent() != "hello" {
		t.Fatalf("ChatCtx message = %#v, want user message with text hello", msg)
	}
	select {
	case ev := <-session.ConversationItemAddedEvents():
		if ev.Item != msg {
			t.Fatalf("ConversationItemAdded item = %#v, want committed user message", ev.Item)
		}
	case <-time.After(time.Second):
		t.Fatal("ConversationItemAddedEvents did not receive generated user input")
	}
}

func TestAgentSessionGenerateReplyOptionsOverrideInterruptionsAndInputModality(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{AllowInterruptions: true})
	session.activity = NewAgentActivity(agent, session)
	allowInterruptions := false

	handle, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserInput:          "hello",
		AllowInterruptions: &allowInterruptions,
		InputModality:      "audio",
	})

	if err != nil {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil", err)
	}
	if handle.AllowInterruptions {
		t.Fatal("handle.AllowInterruptions = true, want per-call false override")
	}
	if got, want := handle.InputDetails.Modality, "audio"; got != want {
		t.Fatalf("handle.InputDetails.Modality = %q, want %q", got, want)
	}
}

func TestAgentSessionRunReturnsRunResultWatchingGeneratedSpeech(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{AllowInterruptions: true})
	session.activity = NewAgentActivity(agent, session)

	result, err := session.Run(context.Background(), "hello")

	if err != nil {
		t.Fatalf("Run error = %v, want nil", err)
	}
	if result == nil {
		t.Fatal("Run result = nil, want RunResult")
	}
	if got := result.UserInput(); got != "hello" {
		t.Fatalf("UserInput = %q, want hello", got)
	}

	handle := session.activity.speechQueue[0].speech
	msg := &llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleAssistant, CreatedAt: time.Now()}
	handle.AddChatItems(msg)
	handle.MarkDone()

	if !result.Done() {
		t.Fatal("Run result not done after generated speech completed")
	}
	events := result.Events()
	if len(events) != 1 {
		t.Fatalf("Events length = %d, want 1", len(events))
	}
	if ev, ok := events[0].(*ChatMessageEvent); !ok || ev.Item != msg {
		t.Fatalf("events[0] = %#v, want recorded assistant message", events[0])
	}
}

func TestAgentSessionRunRejectsNestedActiveRun(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)

	first, err := session.Run(context.Background(), "first")
	if err != nil {
		t.Fatalf("first Run error = %v, want nil", err)
	}

	second, err := session.Run(context.Background(), "second")

	if second != nil {
		t.Fatalf("second Run result = %#v, want nil", second)
	}
	if !errors.Is(err, ErrAgentSessionNestedRun) {
		t.Fatalf("second Run error = %v, want ErrAgentSessionNestedRun", err)
	}

	session.activity.speechQueue[0].speech.MarkDone()
	if !first.Done() {
		t.Fatal("first Run result not done after scheduled speech completed")
	}
}

func TestAgentSessionGenerateReplyRequiresRunningActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	handle, err := session.GenerateReply(context.Background(), "hello")

	if handle != nil {
		t.Fatalf("GenerateReply handle = %#v, want nil when session is not running", handle)
	}
	if !errors.Is(err, ErrAgentSessionNotRunning) {
		t.Fatalf("GenerateReply error = %v, want ErrAgentSessionNotRunning", err)
	}
}

func TestAgentSessionCloseSoonStopsRunningSession(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	session.started = true

	session.CloseSoon(CloseReasonParticipantDisconnected)

	select {
	case ev := <-session.CloseEvents():
		if ev.Reason != CloseReasonParticipantDisconnected {
			t.Fatalf("CloseEvent.Reason = %q, want participant_disconnected", ev.Reason)
		}
	default:
		t.Fatal("CloseSoon did not emit close event")
	}

	handle, err := session.GenerateReply(context.Background(), "hello")
	if handle != nil {
		t.Fatalf("GenerateReply handle after CloseSoon = %#v, want nil", handle)
	}
	if !errors.Is(err, ErrAgentSessionNotRunning) {
		t.Fatalf("GenerateReply error after CloseSoon = %v, want ErrAgentSessionNotRunning", err)
	}
}

func TestAgentSessionShutdownClosesWithUserInitiatedReason(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	session.started = true

	session.Shutdown()

	select {
	case ev := <-session.CloseEvents():
		if ev.Reason != CloseReasonUserInitiated {
			t.Fatalf("CloseEvent.Reason = %q, want user_initiated", ev.Reason)
		}
	default:
		t.Fatal("Shutdown did not emit close event")
	}

	handle, err := session.GenerateReply(context.Background(), "hello")
	if handle != nil {
		t.Fatalf("GenerateReply handle after Shutdown = %#v, want nil", handle)
	}
	if !errors.Is(err, ErrAgentSessionNotRunning) {
		t.Fatalf("GenerateReply error after Shutdown = %v, want ErrAgentSessionNotRunning", err)
	}
}

func TestAgentSessionStopResetsSessionStates(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	session.started = true
	session.UserState = UserStateSpeaking
	session.AgentState = AgentStateThinking

	if err := session.Stop(context.Background()); err != nil {
		t.Fatalf("Stop error = %v, want nil", err)
	}

	if session.UserState != UserStateListening {
		t.Fatalf("UserState after Stop = %q, want %q", session.UserState, UserStateListening)
	}
	if session.AgentState != AgentStateInitializing {
		t.Fatalf("AgentState after Stop = %q, want %q", session.AgentState, AgentStateInitializing)
	}
}

func TestAgentSessionStopAllowsOnExitSessionCallbacks(t *testing.T) {
	agent := &sessionCallbackAgent{Agent: NewAgent("test")}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	agent.session = session
	session.activity = NewAgentActivity(agent, session)
	session.started = true

	done := make(chan error, 1)
	go func() {
		done <- session.Stop(context.Background())
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop error = %v, want nil", err)
		}
	case <-testTimeout():
		t.Fatal("Stop deadlocked while OnExit called back into session")
	}
	if !agent.exited {
		t.Fatal("OnExit was not called")
	}
}

func TestAgentSessionCurrentSpeechReturnsNilWithoutActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	if got := session.CurrentSpeech(); got != nil {
		t.Fatalf("CurrentSpeech = %#v, want nil without activity", got)
	}
}

func TestAgentSessionCurrentSpeechReturnsActivitySpeech(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	session.activity.currentSpeech = current

	if got := session.CurrentSpeech(); got != current {
		t.Fatalf("CurrentSpeech = %#v, want current activity speech %#v", got, current)
	}
}

func TestAgentSessionWaitForInactiveReturnsWithoutActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	if err := session.WaitForInactive(context.Background()); err != nil {
		t.Fatalf("WaitForInactive error = %v, want nil without activity", err)
	}
}

func TestAgentSessionWaitForInactiveWaitsForCurrentSpeech(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	session.activity.currentSpeech = current

	done := make(chan error, 1)
	go func() {
		done <- session.WaitForInactive(context.Background())
	}()

	select {
	case err := <-done:
		t.Fatalf("WaitForInactive returned before current speech completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	current.MarkDone()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitForInactive error = %v, want nil", err)
		}
	case <-testTimeout():
		t.Fatal("WaitForInactive did not return after current speech completed")
	}
}

func TestAgentSessionInterruptRequiresRunningActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	err := session.Interrupt(false)

	if !errors.Is(err, ErrAgentSessionNotRunning) {
		t.Fatalf("Interrupt error = %v, want ErrAgentSessionNotRunning", err)
	}
}

func TestAgentSessionInterruptDelegatesToActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	session.activity.currentSpeech = current

	done := make(chan error, 1)
	go func() {
		done <- session.Interrupt(false)
	}()

	waitForInterrupted(t, current)
	current.MarkDone()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Interrupt error = %v, want nil", err)
		}
	case <-testTimeout():
		t.Fatal("Interrupt did not return after current speech was done")
	}
}

func TestAgentSessionUpdateAgentBeforeStartSwapsAgentOnly(t *testing.T) {
	initial := &trackingAgent{Agent: NewAgent("initial")}
	next := &trackingAgent{Agent: NewAgent("next")}
	session := NewAgentSession(initial, nil, AgentSessionOptions{})

	session.UpdateAgent(next)

	if session.Agent != next {
		t.Fatalf("session.Agent = %#v, want next agent", session.Agent)
	}
	if session.activity != nil {
		t.Fatalf("session.activity = %#v, want nil before start", session.activity)
	}
	if initial.entered != 0 || initial.exited != 0 || next.entered != 0 || next.exited != 0 {
		t.Fatalf("lifecycle calls initial=%d/%d next=%d/%d, want none", initial.entered, initial.exited, next.entered, next.exited)
	}
}

func TestAgentSessionUpdateAgentWhileRunningStartsNewActivity(t *testing.T) {
	initial := &trackingAgent{Agent: NewAgent("initial")}
	next := &trackingAgent{Agent: NewAgent("next")}
	session := NewAgentSession(initial, nil, AgentSessionOptions{})
	oldActivity := NewAgentActivity(initial, session)
	session.activity = oldActivity
	session.started = true

	session.UpdateAgent(next)

	if initial.exited != 1 {
		t.Fatalf("initial exits = %d, want 1", initial.exited)
	}
	if next.entered != 1 {
		t.Fatalf("next enters = %d, want 1", next.entered)
	}
	if session.Agent != next {
		t.Fatalf("session.Agent = %#v, want next agent", session.Agent)
	}
	if session.activity == nil || session.activity == oldActivity {
		t.Fatalf("session.activity = %#v, want replacement activity", session.activity)
	}
	if session.activity.Agent != next.Agent {
		t.Fatalf("session.activity.Agent = %#v, want next base agent", session.activity.Agent)
	}
	if initial.GetActivity() != nil {
		t.Fatalf("initial activity = %#v, want cleared", initial.GetActivity())
	}
	if next.GetActivity() != session.activity {
		t.Fatalf("next activity = %#v, want session activity", next.GetActivity())
	}
}

func testTimeout() <-chan time.Time {
	return time.After(time.Second)
}

type trackingAgent struct {
	*Agent
	entered int
	exited  int
}

func (a *trackingAgent) OnEnter() {
	a.entered++
}

func (a *trackingAgent) OnExit() {
	a.exited++
}

type sessionCallbackAgent struct {
	*Agent
	session *AgentSession
	exited  bool
}

func (a *sessionCallbackAgent) OnExit() {
	a.exited = true
	a.session.UpdateUserState(UserStateSpeaking)
}

func TestAgentSessionUpdateAgentStateEmitsTypedTimestampedEvent(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	before := time.Now()

	session.UpdateAgentState(AgentStateThinking)

	select {
	case ev := <-session.AgentStateChangedCh:
		var event Event = &ev
		if event.GetType() != "agent_state_changed" {
			t.Fatalf("event type = %q, want agent_state_changed", event.GetType())
		}
		if ev.OldState != AgentStateInitializing || ev.NewState != AgentStateThinking {
			t.Fatalf("event states = %q -> %q, want initializing -> thinking", ev.OldState, ev.NewState)
		}
		if ev.CreatedAt.Before(before) || ev.CreatedAt.IsZero() {
			t.Fatalf("CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
		}
	case <-time.After(time.Second):
		t.Fatal("UpdateAgentState did not emit an event")
	}
}

func TestAgentSessionStartEmitsInitializingThenListening(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	agent.STT = &fakePipelineSTT{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- session.Start(ctx)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start error = %v, want nil", err)
		}
	case <-testTimeout():
		t.Fatal("Start did not return")
	}
	defer func() {
		if err := session.Stop(context.Background()); err != nil {
			t.Fatalf("Stop error = %v, want nil", err)
		}
	}()

	ev := receiveAgentStateChangedEvent(t, session)
	if ev.OldState != AgentStateInitializing || ev.NewState != AgentStateListening {
		t.Fatalf("state event = %q -> %q, want initializing -> listening", ev.OldState, ev.NewState)
	}
	select {
	case extra := <-session.AgentStateChangedCh:
		t.Fatalf("unexpected extra state event = %q -> %q", extra.OldState, extra.NewState)
	default:
	}
}

func TestAgentSessionUpdateUserStateEmitsTypedTimestampedEvent(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	before := time.Now()

	session.UpdateUserState(UserStateSpeaking)

	select {
	case ev := <-session.UserStateChangedCh:
		var event Event = &ev
		if event.GetType() != "user_state_changed" {
			t.Fatalf("event type = %q, want user_state_changed", event.GetType())
		}
		if ev.OldState != UserStateListening || ev.NewState != UserStateSpeaking {
			t.Fatalf("event states = %q -> %q, want listening -> speaking", ev.OldState, ev.NewState)
		}
		if ev.CreatedAt.Before(before) || ev.CreatedAt.IsZero() {
			t.Fatalf("CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
		}
	case <-time.After(time.Second):
		t.Fatal("UpdateUserState did not emit an event")
	}
}

func receiveAgentStateChangedEvent(t *testing.T, session *AgentSession) AgentStateChangedEvent {
	t.Helper()
	select {
	case ev := <-session.AgentStateChangedCh:
		return ev
	case <-testTimeout():
		t.Fatal("AgentStateChangedCh did not receive event")
	}
	return AgentStateChangedEvent{}
}

func TestAgentSessionEmitMetricsCollectedCollectsUsageAndEmitsEvent(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.MetricsCollector = telemetry.NewUsageCollector()
	metrics := &telemetry.LLMMetrics{
		PromptTokens:     7,
		CompletionTokens: 11,
	}
	before := time.Now()

	session.EmitMetricsCollected(metrics)

	if got := session.MetricsCollector.GetSummary(); got.LLMPromptTokens != 7 || got.LLMCompletionTokens != 11 {
		t.Fatalf("usage summary = %#v, want prompt=7 completion=11", got)
	}
	select {
	case ev := <-session.MetricsCollectedEvents():
		if ev.GetType() != "metrics_collected" {
			t.Fatalf("event type = %q, want metrics_collected", ev.GetType())
		}
		if ev.Metrics != metrics {
			t.Fatalf("Metrics = %#v, want original metrics", ev.Metrics)
		}
		if ev.CreatedAt.Before(before) || ev.CreatedAt.IsZero() {
			t.Fatalf("CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
		}
	case <-time.After(time.Second):
		t.Fatal("MetricsCollectedEvents did not receive event")
	}
	select {
	case ev := <-session.SessionUsageUpdatedEvents():
		if ev.GetType() != "session_usage_updated" {
			t.Fatalf("event type = %q, want session_usage_updated", ev.GetType())
		}
		if ev.Usage.LLMPromptTokens != 7 || ev.Usage.LLMCompletionTokens != 11 {
			t.Fatalf("usage event summary = %#v, want prompt=7 completion=11", ev.Usage)
		}
		if ev.CreatedAt.Before(before) || ev.CreatedAt.IsZero() {
			t.Fatalf("usage event CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
		}
	case <-time.After(time.Second):
		t.Fatal("SessionUsageUpdatedEvents did not receive event")
	}
}
