package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

func TestRunContextDisallowInterruptionsUpdatesSpeechHandle(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())
	runCtx := NewRunContext(nil, speech, &llm.FunctionCall{Name: "lookup"})

	if err := runCtx.DisallowInterruptions(); err != nil {
		t.Fatalf("DisallowInterruptions error = %v, want nil", err)
	}

	if speech.AllowInterruptions {
		t.Fatal("speech.AllowInterruptions = true, want false")
	}
	if err := speech.Interrupt(false); !errors.Is(err, ErrSpeechInterruptionsDisabled) {
		t.Fatalf("Interrupt(false) error = %v, want ErrSpeechInterruptionsDisabled", err)
	}
}

func TestRunContextDisallowInterruptionsFailsAfterInterruption(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())
	if err := speech.Interrupt(false); err != nil {
		t.Fatalf("Interrupt(false) error = %v, want nil", err)
	}
	runCtx := NewRunContext(nil, speech, &llm.FunctionCall{Name: "lookup"})

	err := runCtx.DisallowInterruptions()

	if !errors.Is(err, ErrSpeechAlreadyInterrupted) {
		t.Fatalf("DisallowInterruptions error = %v, want ErrSpeechAlreadyInterrupted", err)
	}
}

func TestRunContextWaitForPlayoutWaitsOnlyInitialGenerationStep(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())
	speech.AuthorizeGeneration()
	runCtx := NewRunContext(nil, speech, &llm.FunctionCall{Name: "lookup"})

	done := make(chan error, 1)
	go func() {
		done <- runCtx.WaitForPlayout(context.Background())
	}()

	select {
	case err := <-done:
		t.Fatalf("WaitForPlayout returned before generation finished: %v", err)
	case <-time.After(10 * time.Millisecond):
	}

	if err := speech.MarkGenerationDone(); err != nil {
		t.Fatalf("MarkGenerationDone error = %v, want nil", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitForPlayout error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitForPlayout did not return after initial generation finished")
	}

	if speech.IsDone() {
		t.Fatal("speech is done, want WaitForPlayout to return without requiring full speech completion")
	}
}

func TestRunContextUserdataReturnsSessionUserdata(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	want := map[string]string{"account_id": "acct_123"}
	session.SetUserdata(want)
	runCtx := NewRunContext(session, nil, &llm.FunctionCall{Name: "lookup"})

	value, err := runCtx.Userdata()

	if err != nil {
		t.Fatalf("Userdata error = %v, want nil", err)
	}
	got, ok := value.(map[string]string)
	if !ok {
		t.Fatalf("Userdata value = %T, want map[string]string", value)
	}
	if got["account_id"] != want["account_id"] {
		t.Fatalf("Userdata account_id = %q, want %q", got["account_id"], want["account_id"])
	}
}

func TestRunContextUserdataRequiresSessionUserdata(t *testing.T) {
	runCtx := NewRunContext(NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{}), nil, &llm.FunctionCall{Name: "lookup"})

	value, err := runCtx.Userdata()

	if value != nil {
		t.Fatalf("Userdata value = %#v, want nil when unset", value)
	}
	if !errors.Is(err, ErrAgentSessionUserdataNotSet) {
		t.Fatalf("Userdata error = %v, want ErrAgentSessionUserdataNotSet", err)
	}
}

func TestRunContextUserdataRequiresSession(t *testing.T) {
	runCtx := NewRunContext(nil, nil, &llm.FunctionCall{Name: "lookup"})

	value, err := runCtx.Userdata()

	if value != nil {
		t.Fatalf("Userdata value = %#v, want nil without session", value)
	}
	if !errors.Is(err, ErrAgentSessionUserdataNotSet) {
		t.Fatalf("Userdata error = %v, want ErrAgentSessionUserdataNotSet", err)
	}
}

func TestRunContextJobContextReturnsSessionJobContext(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	jobCtx := struct{ jobID string }{jobID: "job-a"}
	session.SetJobContext(jobCtx)
	runCtx := NewRunContext(session, nil, &llm.FunctionCall{Name: "lookup"})

	value, err := runCtx.JobContext()

	if err != nil {
		t.Fatalf("JobContext error = %v, want nil", err)
	}
	if value != jobCtx {
		t.Fatalf("JobContext value = %#v, want %#v", value, jobCtx)
	}
}

func TestFunctionToolsExecutedEventPairsCallsAndOutputs(t *testing.T) {
	callA := &llm.FunctionCall{CallID: "call_a", Name: "lookup"}
	callB := &llm.FunctionCall{CallID: "call_b", Name: "notify"}
	outA := &llm.FunctionCallOutput{CallID: "call_a", Name: "lookup", Output: "ok"}

	ev, err := NewFunctionToolsExecutedEvent(
		[]*llm.FunctionCall{callA, callB},
		[]*llm.FunctionCallOutput{outA, nil},
	)

	if err != nil {
		t.Fatalf("NewFunctionToolsExecutedEvent error = %v, want nil", err)
	}
	if ev.GetType() != "function_tools_executed" {
		t.Fatalf("GetType() = %q, want function_tools_executed", ev.GetType())
	}
	if ev.CreatedAt.IsZero() {
		t.Fatal("CreatedAt is zero")
	}
	pairs := ev.Zipped()
	if len(pairs) != 2 {
		t.Fatalf("Zipped length = %d, want 2", len(pairs))
	}
	if pairs[0].FunctionCall != callA || pairs[0].FunctionCallOutput != outA {
		t.Fatalf("first pair = %#v, want callA/outA", pairs[0])
	}
	if pairs[1].FunctionCall != callB || pairs[1].FunctionCallOutput != nil {
		t.Fatalf("second pair = %#v, want callB/nil", pairs[1])
	}
}

func TestFunctionToolsExecutedEventRequiresParallelLists(t *testing.T) {
	_, err := NewFunctionToolsExecutedEvent(
		[]*llm.FunctionCall{{CallID: "call_a", Name: "lookup"}},
		nil,
	)

	if !errors.Is(err, ErrFunctionToolEventLengthMismatch) {
		t.Fatalf("NewFunctionToolsExecutedEvent error = %v, want ErrFunctionToolEventLengthMismatch", err)
	}
	if got, want := err.Error(), "The number of function_calls and function_call_outputs must match."; got != want {
		t.Fatalf("NewFunctionToolsExecutedEvent error message = %q, want reference message %q", got, want)
	}
}

func TestFunctionToolsExecutedEventReplyAndHandoffFlagsCanBeCanceled(t *testing.T) {
	ev, err := NewFunctionToolsExecutedEvent(
		[]*llm.FunctionCall{{CallID: "call_a", Name: "lookup"}},
		[]*llm.FunctionCallOutput{{CallID: "call_a", Name: "lookup", Output: "ok"}},
	)
	if err != nil {
		t.Fatalf("NewFunctionToolsExecutedEvent error = %v, want nil", err)
	}
	ev.ReplyRequired = true
	ev.HandoffRequired = true

	ev.CancelToolReply()
	ev.CancelAgentHandoff()

	if ev.HasToolReply() {
		t.Fatal("HasToolReply() = true, want false after CancelToolReply")
	}
	if ev.HasAgentHandoff() {
		t.Fatal("HasAgentHandoff() = true, want false after CancelAgentHandoff")
	}
}

func TestAgentFalseInterruptionEventIsTypedAndTimestamped(t *testing.T) {
	before := time.Now()
	ev := NewAgentFalseInterruptionEvent(true)

	var event Event = ev
	if event.GetType() != "agent_false_interruption" {
		t.Fatalf("GetType() = %q, want agent_false_interruption", event.GetType())
	}
	if !ev.Resumed {
		t.Fatal("Resumed = false, want true")
	}
	if ev.CreatedAt.IsZero() || ev.CreatedAt.Before(before) {
		t.Fatalf("CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
	}
}

func TestAgentFalseInterruptionEventPreservesDeprecatedCompatibilityFields(t *testing.T) {
	msg := &llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleAssistant}
	instructions := "continue from the apology"

	ev := &AgentFalseInterruptionEvent{
		Resumed:           false,
		Message:           msg,
		ExtraInstructions: instructions,
		CreatedAt:         time.Now(),
	}

	if ev.Message != msg {
		t.Fatalf("Message = %#v, want original message", ev.Message)
	}
	if ev.ExtraInstructions != instructions {
		t.Fatalf("ExtraInstructions = %q, want %q", ev.ExtraInstructions, instructions)
	}
}

func TestUserTurnExceededEventIsTypedAndTimestamped(t *testing.T) {
	before := time.Now()
	ev := NewUserTurnExceededEvent("latest words", "all accumulated words", 3, 4*time.Second)

	var event Event = ev
	if event.GetType() != "user_turn_exceeded" {
		t.Fatalf("GetType() = %q, want user_turn_exceeded", event.GetType())
	}
	if ev.Transcript != "latest words" {
		t.Fatalf("Transcript = %q, want latest words", ev.Transcript)
	}
	if ev.AccumulatedTranscript != "all accumulated words" {
		t.Fatalf("AccumulatedTranscript = %q, want all accumulated words", ev.AccumulatedTranscript)
	}
	if ev.AccumulatedWordCount != 3 {
		t.Fatalf("AccumulatedWordCount = %d, want 3", ev.AccumulatedWordCount)
	}
	if ev.Duration != 4*time.Second {
		t.Fatalf("Duration = %v, want 4s", ev.Duration)
	}
	if ev.CreatedAt.IsZero() || ev.CreatedAt.Before(before) {
		t.Fatalf("CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
	}
}

func TestOverlappingSpeechEventIsTypedAndCarriesTimingAndPredictionFields(t *testing.T) {
	overlapStartedAt := time.Now().Add(-250 * time.Millisecond)
	detectedAt := time.Now()
	ev := &OverlappingSpeechEvent{
		DetectedAt:         detectedAt,
		IsInterruption:     true,
		TotalDuration:      120 * time.Millisecond,
		PredictionDuration: 35 * time.Millisecond,
		DetectionDelay:     250 * time.Millisecond,
		OverlapStartedAt:   &overlapStartedAt,
		SpeechInput:        []int16{1, -1},
		Probabilities:      []float32{0.1, 0.9},
		Probability:        0.9,
		NumRequests:        2,
		CreatedAt:          detectedAt.Add(time.Millisecond),
	}

	var event Event = ev
	if event.GetType() != "overlapping_speech" {
		t.Fatalf("GetType() = %q, want overlapping_speech", event.GetType())
	}
	if !ev.IsInterruption {
		t.Fatal("IsInterruption = false, want true")
	}
	if !ev.DetectedAt.Equal(detectedAt) || ev.OverlapStartedAt == nil || !ev.OverlapStartedAt.Equal(overlapStartedAt) {
		t.Fatalf("timing fields = %#v, want detected and overlap timestamps", ev)
	}
	if ev.TotalDuration != 120*time.Millisecond || ev.PredictionDuration != 35*time.Millisecond || ev.DetectionDelay != 250*time.Millisecond {
		t.Fatalf("duration fields = %#v, want reference durations", ev)
	}
	if len(ev.SpeechInput) != 2 || len(ev.Probabilities) != 2 || ev.Probability != 0.9 || ev.NumRequests != 2 {
		t.Fatalf("prediction fields = %#v, want preserved samples/probability/request count", ev)
	}
}

func TestCloseReasonIncludesTaskCompleted(t *testing.T) {
	ev := &CloseEvent{Reason: CloseReasonTaskCompleted, CreatedAt: time.Now()}

	var event Event = ev
	if event.GetType() != "close" {
		t.Fatalf("GetType() = %q, want close", event.GetType())
	}
	if ev.Reason != "task_completed" {
		t.Fatalf("Reason = %q, want task_completed", ev.Reason)
	}
}

func TestErrorEventIsTypedAndTimestamped(t *testing.T) {
	before := time.Now()
	source := "llm"
	cause := errors.New("provider failed")

	ev := NewErrorEvent(cause, source)

	var event Event = ev
	if event.GetType() != "error" {
		t.Fatalf("GetType() = %q, want error", event.GetType())
	}
	if !errors.Is(ev.Error, cause) {
		t.Fatalf("Error = %v, want provider failure", ev.Error)
	}
	if ev.Source != source {
		t.Fatalf("Source = %#v, want %q", ev.Source, source)
	}
	if ev.CreatedAt.IsZero() || ev.CreatedAt.Before(before) {
		t.Fatalf("CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
	}
}

func TestRunContextRoundTrip(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	runCtx := NewRunContext(session, nil, nil)

	ctx := WithRunContext(context.Background(), runCtx)
	if got := GetRunContext(ctx); got != runCtx {
		t.Fatalf("GetRunContext = %#v, want original run context", got)
	}
	if got := GetRunContext(context.Background()); got != nil {
		t.Fatalf("GetRunContext without value = %#v, want nil", got)
	}
}

func TestClientEventsDispatcherNoopsWithoutRoom(t *testing.T) {
	dispatcher := NewClientEventsDispatcher(nil)

	dispatcher.DispatchAgentState(AgentStateIdle)
	dispatcher.DispatchAgentState(AgentStateThinking)
	dispatcher.DispatchAgentState(AgentStateSpeaking)
	dispatcher.DispatchAgentState(AgentState("unknown"))
	dispatcher.DispatchUserState(UserStateListening)
	dispatcher.DispatchUserState(UserStateSpeaking)
	dispatcher.DispatchUserState(UserStateAway)
	dispatcher.DispatchUserState(UserState("unknown"))
}

func TestClientEventsDispatcherNoopsWhenRoomDisconnected(t *testing.T) {
	dispatcher := NewClientEventsDispatcher(&lksdk.Room{LocalParticipant: &lksdk.LocalParticipant{}})

	dispatcher.DispatchAgentState(AgentStateThinking)
	dispatcher.DispatchUserState(UserStateSpeaking)
}

func TestClientAgentStateStringMapsIdleToReferenceListening(t *testing.T) {
	tests := []struct {
		state AgentState
		want  string
		ok    bool
	}{
		{state: AgentStateIdle, want: "listening", ok: true},
		{state: AgentStateListening, want: "listening", ok: true},
		{state: AgentStateThinking, want: "thinking", ok: true},
		{state: AgentStateSpeaking, want: "speaking", ok: true},
		{state: AgentStateInitializing, want: "initializing", ok: true},
		{state: AgentState("unknown"), ok: false},
	}

	for _, tt := range tests {
		got, ok := clientAgentStateString(tt.state)
		if ok != tt.ok || got != tt.want {
			t.Fatalf("clientAgentStateString(%q) = %q, %v; want %q, %v", tt.state, got, ok, tt.want, tt.ok)
		}
	}
}

func TestClientAgentStateStringIncludesReferenceInitializing(t *testing.T) {
	got, ok := clientAgentStateString(AgentStateInitializing)
	if !ok || got != "initializing" {
		t.Fatalf("clientAgentStateString(%q) = %q, %v; want initializing, true", AgentStateInitializing, got, ok)
	}
}

func TestClientUserStateStringIncludesReferenceAway(t *testing.T) {
	tests := []struct {
		state UserState
		want  string
		ok    bool
	}{
		{state: UserStateListening, want: "listening", ok: true},
		{state: UserStateSpeaking, want: "speaking", ok: true},
		{state: UserStateAway, want: "away", ok: true},
		{state: UserState("unknown"), ok: false},
	}

	for _, tt := range tests {
		got, ok := clientUserStateString(tt.state)
		if ok != tt.ok || got != tt.want {
			t.Fatalf("clientUserStateString(%q) = %q, %v; want %q, %v", tt.state, got, ok, tt.want, tt.ok)
		}
	}
}
