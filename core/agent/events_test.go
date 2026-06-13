package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
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

func TestRunContextForegroundHoldsSessionIdle(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	runCtx := NewRunContext(session, nil, &llm.FunctionCall{Name: "lookup"})
	held := make(chan struct{})
	release := make(chan struct{})
	foregroundDone := make(chan error, 1)

	go func() {
		foregroundDone <- runCtx.Foreground(context.Background(), func(ctx context.Context) error {
			if err := session.WaitForInactive(ctx); err != nil {
				return err
			}
			close(held)
			<-release
			return nil
		})
	}()
	<-held

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- session.WaitForInactive(context.Background())
	}()

	select {
	case err := <-waitDone:
		t.Fatalf("WaitForInactive returned while foreground run context held idle: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-foregroundDone:
		if err != nil {
			t.Fatalf("RunContext.Foreground error = %v", err)
		}
	case <-testTimeout():
		t.Fatal("RunContext.Foreground did not release")
	}
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("WaitForInactive error = %v, want nil after foreground release", err)
		}
	case <-testTimeout():
		t.Fatal("WaitForInactive did not return after foreground release")
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

func TestRunContextUpdateRecordsStandaloneProgress(t *testing.T) {
	extra := map[string]any{"trace_id": "trace_123"}
	runCtx := NewRunContext(nil, nil, &llm.FunctionCall{
		CallID:    "call_lookup",
		Name:      "lookup",
		Arguments: `{"city":"Paris"}`,
		Extra:     extra,
		CreatedAt: time.Unix(12, 0),
	})

	if err := runCtx.Update("halfway there"); err != nil {
		t.Fatalf("Update first error = %v, want nil", err)
	}
	extra["trace_id"] = "mutated"
	if err := runCtx.Update(map[string]any{"status": "done"}); err != nil {
		t.Fatalf("Update second error = %v, want nil", err)
	}

	updates := runCtx.Updates()
	if len(updates) != 2 {
		t.Fatalf("len(Updates()) = %d, want 2", len(updates))
	}

	firstCall := updates[0].FunctionCall
	if firstCall.CallID != "call_lookup" || firstCall.Name != "lookup" || firstCall.Arguments != `{"city":"Paris"}` {
		t.Fatalf("first update call = %#v, want original call identity and arguments", firstCall)
	}
	if firstCall.Extra["trace_id"] != "trace_123" {
		t.Fatalf("first update extra trace_id = %#v, want copied original value", firstCall.Extra["trace_id"])
	}
	if _, ok := firstCall.Extra["__livekit_agents_tool_non_blocking"]; ok {
		t.Fatalf("first update extra has nonblocking marker for standalone update: %#v", firstCall.Extra)
	}
	if got, want := updates[0].FunctionCallOutput.Output, "The tool `lookup` has updated, message: halfway there\nThe task is still running, so DON'T make up or give information not included in the message above."; got != want {
		t.Fatalf("first update output = %q, want default template output %q", got, want)
	}

	secondCall := updates[1].FunctionCall
	if secondCall.CallID != "call_lookup_update_1" || secondCall.Name != "lookup" || secondCall.Arguments != `{"city":"Paris"}` {
		t.Fatalf("second update call = %#v, want suffixed call identity and copied arguments", secondCall)
	}
	if secondCall.Extra["trace_id"] != "mutated" {
		t.Fatalf("second update extra trace_id = %#v, want latest copied value", secondCall.Extra["trace_id"])
	}
	if _, ok := secondCall.Extra["__livekit_agents_tool_non_blocking"]; ok {
		t.Fatalf("second update extra has nonblocking marker for standalone update: %#v", secondCall.Extra)
	}
	if _, ok := runCtx.FunctionCall.Extra["__livekit_agents_tool_non_blocking"]; ok {
		t.Fatalf("original function call extra has nonblocking marker for standalone update: %#v", runCtx.FunctionCall.Extra)
	}
	if got, want := updates[1].FunctionCallOutput.Output, `{'status': 'done'}`; got != want {
		t.Fatalf("second update output = %q, want Python-style dict output %q", got, want)
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

func TestUserInputTranscribedEventMarshalJSONMatchesReferencePayload(t *testing.T) {
	ev := &UserInputTranscribedEvent{
		Language:   "en-US",
		Transcript: "hello there",
		IsFinal:    true,
		SpeakerID:  "speaker-1",
		CreatedAt:  time.Unix(20, 125_000_000),
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal UserInputTranscribedEvent returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal marshaled UserInputTranscribedEvent returned error: %v", err)
	}
	if payload["type"] != "user_input_transcribed" {
		t.Fatalf("type = %#v, want user_input_transcribed", payload["type"])
	}
	if payload["transcript"] != "hello there" || payload["is_final"] != true {
		t.Fatalf("payload transcript/finality = %#v, want reference transcript fields", payload)
	}
	if payload["speaker_id"] != "speaker-1" || payload["language"] != "en-US" {
		t.Fatalf("payload speaker/language = %#v, want reference optional fields", payload)
	}
	if payload["created_at"] != 20.125 {
		t.Fatalf("created_at = %#v, want 20.125", payload["created_at"])
	}
	if _, ok := payload["IsFinal"]; ok {
		t.Fatalf("payload used Go field names: %#v", payload)
	}
}

func TestAgentOutputTranscribedEventMarshalJSONMatchesReferencePayload(t *testing.T) {
	ev := &AgentOutputTranscribedEvent{
		Language:   "en-US",
		Transcript: "assistant reply",
		IsFinal:    false,
		CreatedAt:  time.Unix(21, 750_000_000),
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal AgentOutputTranscribedEvent returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal marshaled AgentOutputTranscribedEvent returned error: %v", err)
	}
	if payload["type"] != "agent_output_transcribed" {
		t.Fatalf("type = %#v, want agent_output_transcribed", payload["type"])
	}
	if payload["transcript"] != "assistant reply" || payload["is_final"] != false {
		t.Fatalf("payload transcript/finality = %#v, want reference transcript fields", payload)
	}
	if payload["language"] != "en-US" {
		t.Fatalf("language = %#v, want en-US", payload["language"])
	}
	if payload["created_at"] != 21.75 {
		t.Fatalf("created_at = %#v, want 21.75", payload["created_at"])
	}
	if _, ok := payload["IsFinal"]; ok {
		t.Fatalf("payload used Go field names: %#v", payload)
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

func TestAgentFalseInterruptionEventMarshalJSONMatchesReferencePayload(t *testing.T) {
	ev := &AgentFalseInterruptionEvent{
		Resumed:   true,
		CreatedAt: time.Unix(26, 500_000_000),
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal AgentFalseInterruptionEvent returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal marshaled AgentFalseInterruptionEvent returned error: %v", err)
	}
	if payload["type"] != "agent_false_interruption" {
		t.Fatalf("type = %#v, want agent_false_interruption", payload["type"])
	}
	if payload["resumed"] != true {
		t.Fatalf("resumed = %#v, want true", payload["resumed"])
	}
	if payload["message"] != nil || payload["extra_instructions"] != nil {
		t.Fatalf("deprecated fields = %#v/%#v, want null reference fields", payload["message"], payload["extra_instructions"])
	}
	if payload["created_at"] != 26.5 {
		t.Fatalf("created_at = %#v, want 26.5", payload["created_at"])
	}
	if _, ok := payload["ExtraInstructions"]; ok {
		t.Fatalf("payload used Go field names: %#v", payload)
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

func TestUserTurnExceededEventMarshalJSONMatchesReferencePayload(t *testing.T) {
	ev := &UserTurnExceededEvent{
		Transcript:            "latest words",
		AccumulatedTranscript: "all accumulated words",
		AccumulatedWordCount:  3,
		Duration:              1500 * time.Millisecond,
		CreatedAt:             time.Unix(24, 250_000_000),
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal UserTurnExceededEvent returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal marshaled UserTurnExceededEvent returned error: %v", err)
	}
	if payload["type"] != "user_turn_exceeded" {
		t.Fatalf("type = %#v, want user_turn_exceeded", payload["type"])
	}
	if payload["transcript"] != "latest words" || payload["accumulated_transcript"] != "all accumulated words" {
		t.Fatalf("transcript payload = %#v, want reference transcript fields", payload)
	}
	if payload["accumulated_word_count"] != float64(3) {
		t.Fatalf("accumulated_word_count = %#v, want 3", payload["accumulated_word_count"])
	}
	if payload["duration"] != 1.5 {
		t.Fatalf("duration = %#v, want 1.5", payload["duration"])
	}
	if payload["created_at"] != 24.25 {
		t.Fatalf("created_at = %#v, want 24.25", payload["created_at"])
	}
	if _, ok := payload["AccumulatedTranscript"]; ok {
		t.Fatalf("payload used Go field names: %#v", payload)
	}
}

func TestSpeechCreatedEventMarshalJSONMatchesReferencePayload(t *testing.T) {
	ev := &SpeechCreatedEvent{
		UserInitiated: true,
		Source:        "generate_reply",
		SpeechHandle:  NewSpeechHandle(true, DefaultInputDetails()),
		CreatedAt:     time.Unix(25, 125_000_000),
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal SpeechCreatedEvent returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal marshaled SpeechCreatedEvent returned error: %v", err)
	}
	if payload["type"] != "speech_created" {
		t.Fatalf("type = %#v, want speech_created", payload["type"])
	}
	if payload["user_initiated"] != true || payload["source"] != "generate_reply" {
		t.Fatalf("payload = %#v, want reference speech created public fields", payload)
	}
	if payload["created_at"] != 25.125 {
		t.Fatalf("created_at = %#v, want 25.125", payload["created_at"])
	}
	if _, ok := payload["speech_handle"]; ok {
		t.Fatalf("payload serialized excluded speech_handle: %#v", payload)
	}
	if _, ok := payload["SpeechHandle"]; ok {
		t.Fatalf("payload used Go field names: %#v", payload)
	}
}

func TestSessionUsageUpdatedEventMarshalJSONMatchesReferencePayload(t *testing.T) {
	ev := &SessionUsageUpdatedEvent{
		Usage: telemetry.AgentSessionUsage{ModelUsage: []telemetry.ModelUsage{
			&telemetry.LLMModelUsage{
				Type:              "llm_usage",
				Provider:          "openai",
				Model:             "gpt-4o-mini",
				InputTokens:       17,
				InputCachedTokens: 3,
				OutputTokens:      11,
				SessionDuration:   2.5,
			},
		}},
		CreatedAt: time.Unix(27, 250_000_000),
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal SessionUsageUpdatedEvent returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal marshaled SessionUsageUpdatedEvent returned error: %v", err)
	}
	if payload["type"] != "session_usage_updated" {
		t.Fatalf("type = %#v, want session_usage_updated", payload["type"])
	}
	usage, ok := payload["usage"].(map[string]any)
	if !ok {
		t.Fatalf("usage = %T %#v, want reference usage object", payload["usage"], payload["usage"])
	}
	modelUsage, ok := usage["model_usage"].([]any)
	if !ok || len(modelUsage) != 1 {
		t.Fatalf("model_usage = %T %#v, want one usage entry", usage["model_usage"], usage["model_usage"])
	}
	llmUsage, ok := modelUsage[0].(map[string]any)
	if !ok {
		t.Fatalf("model_usage[0] = %T %#v, want object", modelUsage[0], modelUsage[0])
	}
	if llmUsage["type"] != "llm_usage" || llmUsage["provider"] != "openai" || llmUsage["model"] != "gpt-4o-mini" {
		t.Fatalf("llm usage identity = %#v, want reference type/provider/model", llmUsage)
	}
	if llmUsage["input_tokens"] != float64(17) || llmUsage["input_cached_tokens"] != float64(3) || llmUsage["output_tokens"] != float64(11) {
		t.Fatalf("llm token usage = %#v, want reference token fields", llmUsage)
	}
	if llmUsage["session_duration"] != 2.5 {
		t.Fatalf("session_duration = %#v, want 2.5", llmUsage["session_duration"])
	}
	if payload["created_at"] != 27.25 {
		t.Fatalf("created_at = %#v, want 27.25", payload["created_at"])
	}
	if _, ok := payload["Usage"]; ok {
		t.Fatalf("payload used Go field names: %#v", payload)
	}
}

func TestMetricsCollectedEventMarshalJSONMatchesReferencePayload(t *testing.T) {
	ev := &MetricsCollectedEvent{
		Metrics: &telemetry.LLMMetrics{
			Label:              "agent.LLM",
			RequestID:          "req_123",
			Timestamp:          time.Unix(28, 500_000_000),
			Duration:           1.25,
			TTFT:               0.35,
			Cancelled:          false,
			CompletionTokens:   13,
			PromptTokens:       17,
			PromptCachedTokens: 5,
			TotalTokens:        30,
			TokensPerSecond:    24,
			SpeechID:           "speech_123",
			Metadata: &telemetry.Metadata{
				ModelName:     "gpt-4o-mini",
				ModelProvider: "openai",
			},
		},
		CreatedAt: time.Unix(29, 750_000_000),
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal MetricsCollectedEvent returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal marshaled MetricsCollectedEvent returned error: %v", err)
	}
	if payload["type"] != "metrics_collected" {
		t.Fatalf("type = %#v, want metrics_collected", payload["type"])
	}
	metrics, ok := payload["metrics"].(map[string]any)
	if !ok {
		t.Fatalf("metrics = %T %#v, want reference metrics object", payload["metrics"], payload["metrics"])
	}
	if metrics["type"] != "llm_metrics" || metrics["label"] != "agent.LLM" || metrics["request_id"] != "req_123" {
		t.Fatalf("metrics identity = %#v, want reference type/label/request_id", metrics)
	}
	if metrics["timestamp"] != 28.5 || metrics["duration"] != 1.25 || metrics["ttft"] != 0.35 {
		t.Fatalf("metrics timing = %#v, want reference seconds fields", metrics)
	}
	if metrics["completion_tokens"] != float64(13) || metrics["prompt_tokens"] != float64(17) || metrics["prompt_cached_tokens"] != float64(5) || metrics["total_tokens"] != float64(30) {
		t.Fatalf("metrics tokens = %#v, want reference token fields", metrics)
	}
	if metrics["tokens_per_second"] != float64(24) || metrics["speech_id"] != "speech_123" {
		t.Fatalf("metrics throughput/speech = %#v, want reference fields", metrics)
	}
	metadata, ok := metrics["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata = %T %#v, want reference metadata object", metrics["metadata"], metrics["metadata"])
	}
	if metadata["model_name"] != "gpt-4o-mini" || metadata["model_provider"] != "openai" {
		t.Fatalf("metadata = %#v, want reference model metadata fields", metadata)
	}
	if payload["created_at"] != 29.75 {
		t.Fatalf("created_at = %#v, want 29.75", payload["created_at"])
	}
	if _, ok := payload["Metrics"]; ok {
		t.Fatalf("payload used Go field names: %#v", payload)
	}
	if _, ok := metrics["RequestID"]; ok {
		t.Fatalf("metrics used Go field names: %#v", metrics)
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

func TestOverlappingSpeechEventMarshalJSONMatchesReferencePayload(t *testing.T) {
	overlapStartedAt := time.Unix(30, 125_000_000)
	ev := &OverlappingSpeechEvent{
		CreatedAt:          time.Unix(31, 250_000_000),
		DetectedAt:         time.Unix(32, 500_000_000),
		IsInterruption:     true,
		TotalDuration:      120 * time.Millisecond,
		PredictionDuration: 35 * time.Millisecond,
		DetectionDelay:     250 * time.Millisecond,
		OverlapStartedAt:   &overlapStartedAt,
		SpeechInput:        []int16{1, -1},
		Probabilities:      []float32{0.1, 0.9},
		Probability:        0.9,
		NumRequests:        2,
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal OverlappingSpeechEvent returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal marshaled OverlappingSpeechEvent returned error: %v", err)
	}
	if payload["type"] != "overlapping_speech" {
		t.Fatalf("type = %#v, want overlapping_speech", payload["type"])
	}
	if payload["created_at"] != 31.25 || payload["detected_at"] != 32.5 || payload["overlap_started_at"] != 30.125 {
		t.Fatalf("timestamps = %#v, want reference seconds fields", payload)
	}
	if payload["is_interruption"] != true || payload["probability"] != 0.9 || payload["num_requests"] != float64(2) {
		t.Fatalf("prediction fields = %#v, want reference prediction fields", payload)
	}
	if payload["total_duration"] != 0.12 || payload["prediction_duration"] != 0.035 || payload["detection_delay"] != 0.25 {
		t.Fatalf("duration fields = %#v, want seconds durations", payload)
	}
	if payload["speech_input"] != nil || payload["probabilities"] != nil {
		t.Fatalf("raw arrays = %#v/%#v, want null reference serialization", payload["speech_input"], payload["probabilities"])
	}
	if _, ok := payload["TotalDuration"]; ok {
		t.Fatalf("payload used Go field names: %#v", payload)
	}
}

func TestConversationItemAddedEventMarshalJSONMatchesReferencePayload(t *testing.T) {
	msg := &llm.ChatMessage{
		ID:        "msg_123",
		Role:      llm.ChatRoleAssistant,
		Content:   []llm.ChatContent{{Text: "hello there"}},
		CreatedAt: time.Unix(33, 125_000_000),
	}
	ev := &ConversationItemAddedEvent{
		Item:      msg,
		CreatedAt: time.Unix(34, 500_000_000),
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal ConversationItemAddedEvent returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal marshaled ConversationItemAddedEvent returned error: %v", err)
	}
	if payload["type"] != "conversation_item_added" {
		t.Fatalf("type = %#v, want conversation_item_added", payload["type"])
	}
	item, ok := payload["item"].(map[string]any)
	if !ok {
		t.Fatalf("item = %T %#v, want reference chat item object", payload["item"], payload["item"])
	}
	if item["type"] != "message" || item["id"] != "msg_123" || item["role"] != string(llm.ChatRoleAssistant) {
		t.Fatalf("item identity = %#v, want reference chat message fields", item)
	}
	content, ok := item["content"].([]any)
	if !ok || len(content) != 1 || content[0] != "hello there" {
		t.Fatalf("content = %#v, want single hello there content part", item["content"])
	}
	if item["created_at"] != 33.125 {
		t.Fatalf("item created_at = %#v, want 33.125", item["created_at"])
	}
	if payload["created_at"] != 34.5 {
		t.Fatalf("event created_at = %#v, want 34.5", payload["created_at"])
	}
	if _, ok := payload["Item"]; ok {
		t.Fatalf("payload used Go field names: %#v", payload)
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

func TestErrorEventMarshalJSONMatchesReferencePayload(t *testing.T) {
	createdAt := time.Unix(12, 250_000_000)
	ev := &ErrorEvent{
		Error:     llm.NewLLMError("openai.LLM", errors.New("failed"), false),
		Source:    &reportMetadataLLM{model: "gpt-report", provider: "openai"},
		CreatedAt: createdAt,
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal ErrorEvent returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal marshaled ErrorEvent returned error: %v", err)
	}
	if payload["type"] != "error" {
		t.Fatalf("type = %#v, want error", payload["type"])
	}
	errorData, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("error = %T %#v, want structured error payload", payload["error"], payload["error"])
	}
	if errorData["type"] != "llm_error" || errorData["label"] != "openai.LLM" || errorData["recoverable"] != false {
		t.Fatalf("error = %#v, want reference LLMError payload", errorData)
	}
	if _, ok := errorData["err"]; ok {
		t.Fatalf("error payload serialized internal err: %#v", errorData)
	}
	sourceData, ok := payload["source"].(map[string]any)
	if !ok {
		t.Fatalf("source = %T %#v, want structured source payload", payload["source"], payload["source"])
	}
	if sourceData["model"] != "gpt-report" || sourceData["provider"] != "openai" {
		t.Fatalf("source = %#v, want model/provider metadata", sourceData)
	}
	if payload["created_at"] != 12.25 {
		t.Fatalf("created_at = %#v, want 12.25", payload["created_at"])
	}
	if _, ok := payload["Error"]; ok {
		t.Fatalf("payload used Go field names: %#v", payload)
	}
}

func TestCloseEventMarshalJSONMatchesReferencePayload(t *testing.T) {
	ev := &CloseEvent{
		Reason:    CloseReasonError,
		Error:     llm.NewLLMError("openai.LLM", errors.New("failed"), false),
		CreatedAt: time.Unix(14, 500_000_000),
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal CloseEvent returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal marshaled CloseEvent returned error: %v", err)
	}
	if payload["type"] != "close" {
		t.Fatalf("type = %#v, want close", payload["type"])
	}
	if payload["reason"] != string(CloseReasonError) {
		t.Fatalf("reason = %#v, want error", payload["reason"])
	}
	errorData, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("error = %T %#v, want structured error payload", payload["error"], payload["error"])
	}
	if errorData["type"] != "llm_error" || errorData["label"] != "openai.LLM" {
		t.Fatalf("error = %#v, want reference LLMError payload", errorData)
	}
	if payload["created_at"] != 14.5 {
		t.Fatalf("created_at = %#v, want 14.5", payload["created_at"])
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
