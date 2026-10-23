package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func traceParityRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	r := tracetest.NewSpanRecorder()
	p := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(r))
	old := telemetry.Tracer
	telemetry.Tracer = p.Tracer("test")
	t.Cleanup(func() { telemetry.Tracer = old; _ = p.Shutdown(context.Background()) })
	return r
}

func TestUserTurnCompletedHookParent(t *testing.T) {
	r := traceParityRecorder(t)
	ctx, root := telemetry.StartSpan(context.Background(), "agent_session")
	s := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	a := NewAgentActivity(s.Agent, s)
	defer a.Stop()
	if _, err := a.completeUserTurn(ctx, EndOfTurnInfo{
		NewTranscript: "hello", TranscriptConfidence: 1, ReplyAlreadyGenerated: true,
	}); err != nil {
		t.Fatal(err)
	}
	root.End()
	spans := map[string]sdktrace.ReadOnlySpan{}
	for _, span := range r.Ended() {
		spans[span.Name()] = span
	}
	hook, turn := spans["on_user_turn_completed"], spans["user_turn"]
	if hook == nil || turn == nil {
		t.Fatal("missing hook or user turn span")
	}
	if hook.Parent().SpanID() != turn.SpanContext().SpanID() {
		t.Fatal("on_user_turn_completed must be a child of its user_turn")
	}
	if hook.StartTime().Before(turn.StartTime()) || hook.EndTime().After(turn.EndTime()) {
		t.Fatal("hook must run within its user turn")
	}
}

func TestSessionLifecycleTraceParity(t *testing.T) {
	r := traceParityRecorder(t)
	s := NewAgentSession(NewAgent("first"), nil, AgentSessionOptions{})
	s.Assistant = &fakeSessionAssistant{}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.UpdateAgent(NewAgent("second"))
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	spans := map[string][]sdktrace.ReadOnlySpan{}
	for _, span := range r.Ended() {
		spans[span.Name()] = append(spans[span.Name()], span)
	}
	for _, name := range []string{"agent_session", "session_start", "session_close", "update_agent", "drain_agent_activity", "on_enter", "on_exit"} {
		if len(spans[name]) == 0 {
			t.Fatalf("missing %s", name)
		}
	}
	for _, pair := range [][2]string{{"session_start", "agent_session"}, {"session_close", "agent_session"}, {"update_agent", "agent_session"}} {
		child, parent := spans[pair[0]][0], spans[pair[1]][0]
		if child.Parent().SpanID() != parent.SpanContext().SpanID() {
			t.Errorf("%s not child of %s", pair[0], pair[1])
		}
		if child.EndTime().After(parent.EndTime()) {
			t.Errorf("%s outlived %s", pair[0], pair[1])
		}
	}
}

func TestSpeechTurnGenerationEvents(t *testing.T) {
	r := traceParityRecorder(t)
	h := NewSpeechHandle(true, DefaultInputDetails())
	ctx := ensureSpeechAgentTurn(context.Background(), h)
	h.IncrementStep()
	_ = ensureSpeechAgentTurn(ctx, h)
	h.MarkDone()
	var turn sdktrace.ReadOnlySpan
	for _, s := range r.Ended() {
		if s.Name() == "agent_turn" {
			if turn != nil {
				t.Fatal("duplicate turn")
			}
			turn = s
		}
	}
	if turn == nil {
		t.Fatal("turn not ended")
	}
	if len(turn.Events()) != 2 {
		t.Fatalf("generation events=%d, want 2", len(turn.Events()))
	}
}

func TestEOUWaitTraceParity(t *testing.T) {
	r := traceParityRecorder(t)
	s := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{MinEndpointingDelay: 0.001, MaxEndpointingDelay: 0.001})
	a := NewAgentActivity(s.Agent, s)
	defer a.Stop()
	s.UpdateUserState(UserStateSpeaking)
	s.UpdateUserState(UserStateListening)
	a.runEOUDetection(EndOfTurnInfo{NewTranscript: "hello", SkipReply: true})
	a.eouMu.Lock()
	done := a.eouDone
	a.eouMu.Unlock()
	if done != nil {
		<-done
	}
	var found bool
	for _, span := range r.Ended() {
		if span.Name() == "eou_wait" {
			found = true
			attrs := spanAttributeValues(span.Attributes())
			if attrs["lk.eou.outcome"].AsString() != "committed" {
				t.Fatal(attrs)
			}
		}
	}
	if !found {
		t.Fatal("missing eou_wait")
	}
}

func TestRealtimeInferenceAndLateMetricsTraceParity(t *testing.T) {
	r := traceParityRecorder(t)
	s := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	ma := NewMultimodalAgent(&fakeRealtimeModel{}, nil)
	ma.session = s
	messages := make(chan llm.MessageGeneration)
	close(messages)
	h := NewSpeechHandle(true, DefaultInputDetails())
	h.Generation.RealtimeGeneration = &llm.GenerationCreatedEvent{ResponseID: "response", MessageCh: messages}
	ma.OnSpeechScheduled(context.Background(), h)
	finishSpeechAgentTurn(h)
	ma.handleRealtimeEvent(llm.RealtimeEvent{Type: llm.RealtimeEventTypeMetricsCollected, Metrics: &telemetry.RealtimeModelMetrics{RequestID: "response", InputTokens: 3, OutputTokens: 2}})
	spans := map[string]sdktrace.ReadOnlySpan{}
	for _, span := range r.Ended() {
		spans[span.Name()] = span
	}
	for _, name := range []string{"agent_turn", "realtime_inference", "realtime_metrics"} {
		if spans[name] == nil {
			t.Fatalf("missing %s", name)
		}
	}
	if spans["realtime_inference"].Parent().SpanID() != spans["agent_turn"].SpanContext().SpanID() {
		t.Fatal("inference ancestry")
	}
	if spans["realtime_metrics"].Parent().SpanID() != spans["realtime_inference"].SpanContext().SpanID() {
		t.Fatal("late metrics ancestry")
	}
}

func TestSessionCloseSpanIncludesCloseHandlers(t *testing.T) {
	r := traceParityRecorder(t)
	s := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	s.Assistant = &fakeSessionAssistant{}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.On("close", func(Event) { _, span := telemetry.StartSpan(s.TraceContext(), "close_handler"); span.End() })
	s.CloseSoon(CloseReasonUserInitiated)
	spans := map[string]sdktrace.ReadOnlySpan{}
	for _, span := range r.Ended() {
		spans[span.Name()] = span
	}
	handler, close, root := spans["close_handler"], spans["session_close"], spans["agent_session"]
	if handler == nil || close == nil || root == nil {
		t.Fatal("missing close spans")
	}
	if handler.Parent().SpanID() != close.SpanContext().SpanID() || handler.EndTime().After(close.EndTime()) || close.EndTime().After(root.EndTime()) {
		t.Fatal("close handler escaped its lifecycle")
	}
}

func TestEOUWaitClosesSupersededPredictions(t *testing.T) {
	r := traceParityRecorder(t)
	s := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	a := NewAgentActivity(s.Agent, s)
	defer a.Stop()
	stopped := time.Now().Add(-time.Second)
	anchor := timeToUnixSeconds(stopped)
	ctx := a.beginEOUWait(context.Background(), EndOfTurnInfo{StoppedSpeakingAt: &anchor})
	_, first := a.startEOUPrediction(ctx)
	ctx = a.beginEOUWait(context.Background(), EndOfTurnInfo{StoppedSpeakingAt: &anchor})
	_, second := a.startEOUPrediction(ctx)
	a.endEOUWait("user_resumed", stopped.Add(time.Millisecond))
	if first.IsRecording() || second.IsRecording() {
		t.Fatal("superseded prediction still open")
	}
	var wait sdktrace.ReadOnlySpan
	for _, span := range r.Ended() {
		if span.Name() == "eou_wait" {
			wait = span
		}
	}
	if wait == nil {
		t.Fatal("missing wait")
	}
	attrs := spanAttributeValues(wait.Attributes())
	if attrs["lk.eou.rearm_count"].AsInt64() != 1 || attrs["lk.eou.outcome"].AsString() != "user_resumed" {
		t.Fatal(attrs)
	}
	if got, want := attrs["lk.eou.wait_duration"].AsFloat64(), wait.EndTime().Sub(wait.StartTime()).Seconds(); got != want {
		t.Fatalf("wait duration=%g,want %g", got, want)
	}
}

func TestRealtimeSpeechWaitEndsAtAutomaticGeneration(t *testing.T) {
	r := traceParityRecorder(t)
	s := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	a := NewAgentActivity(s.Agent, s)
	defer a.Stop()
	a.OnInputSpeechStarted()
	a.OnInputSpeechStopped(llm.InputSpeechStoppedEvent{})
	messages := make(chan llm.MessageGeneration)
	close(messages)
	if _, err := a.OnGenerationCreated(llm.GenerationCreatedEvent{ResponseID: "response", MessageCh: messages}); err != nil {
		t.Fatal(err)
	}
	var wait, parent sdktrace.ReadOnlySpan
	for _, span := range r.Ended() {
		if span.Name() == "eou_wait" {
			wait = span
		}
		if span.Name() == "user_turn" {
			parent = span
		}
	}
	if wait == nil || parent == nil {
		t.Fatal("realtime input wait did not finish at turn acceptance")
	}
	if wait.Parent().SpanID() != parent.SpanContext().SpanID() {
		t.Fatal("wait not nested under realtime user turn")
	}
}

func TestRuntimeTaskToolsAreTraced(t *testing.T) {
	r := traceParityRecorder(t)
	ctx, parent := telemetry.StartSpan(context.Background(), "agent_turn")
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	calls := make(chan *llm.FunctionToolCall, 1)
	calls <- &llm.FunctionToolCall{Name: getRunningTasksToolName, CallID: "tasks", Arguments: "{}"}
	close(calls)
	for range PerformToolExecutions(ctx, calls, nil, WithToolExecutionSession(session)) {
	}
	parent.End()
	spans := r.Ended()
	if len(spans) != 2 || spans[0].Name() != "function_tool" || spans[0].Parent().SpanID() != parent.SpanContext().SpanID() {
		t.Fatal("runtime task tool escaped tool tracing")
	}
}

func TestEOUTraceDoesNotReopenEndedWait(t *testing.T) {
	r := traceParityRecorder(t)
	s := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	a := NewAgentActivity(s.Agent, s)
	defer a.Stop()
	ctx := a.beginEOUWait(context.Background(), EndOfTurnInfo{})
	a.endEOUWait("dropped", time.Time{})
	_, prediction := a.startEOUPrediction(ctx)
	prediction.End()
	for _, span := range r.Ended() {
		if span.Name() == "eou_detection" {
			t.Fatal("late prediction started under an ended wait")
		}
	}
}

func TestUserSpeakingSpanTimestampOrder(t *testing.T) {
	for _, finishTurn := range []bool{false, true} {
		t.Run(fmt.Sprint(finishTurn), func(t *testing.T) {
			r := traceParityRecorder(t)
			s := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
			start := time.Now().Add(-time.Second)
			s.updateUserStateAt(UserStateSpeaking, start)
			turn := s.userTurnSpan
			if finishTurn {
				s.finishUserTurnSpan(turn)
			} else {
				s.updateUserStateAt(UserStateListening, start.Add(-2*time.Millisecond))
				s.finishUserTurnSpan(turn)
			}
			spans := map[string]sdktrace.ReadOnlySpan{}
			for _, span := range r.Ended() {
				spans[span.Name()] = span
			}
			speech, parent := spans["user_speaking"], spans["user_turn"]
			if speech == nil || parent == nil {
				t.Fatal("speech and turn must both finish")
			}
			if speech.EndTime().Before(speech.StartTime()) {
				t.Fatal("negative speech duration")
			}
			if speech.EndTime().After(parent.EndTime()) {
				t.Fatal("speech outlived its turn")
			}
		})
	}
}

func TestInferenceSpanCancellationClassification(t *testing.T) {
	for _, tc := range []struct {
		name                string
		err                 error
		canceled, wantError bool
	}{
		{"canceled", context.Canceled, false, false},
		{"mapped cancellation", llm.NewAPIConnectionError("context canceled"), true, false},
		{"uncanceled connection error", llm.NewAPIConnectionError("context canceled"), false, true},
		{"real error during cancellation", llm.NewAPIConnectionError("connection reset"), true, true},
		{"timeout", context.DeadlineExceeded, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := traceParityRecorder(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.canceled {
				cancel()
			}
			ctx, span := telemetry.StartSpan(ctx, "llm_request")
			recordInferenceSpanError(ctx, span, tc.err)
			span.End()
			got := r.Ended()[0]
			if (got.Status().Code == codes.Error) != tc.wantError {
				t.Fatalf("status=%v wantError=%v", got.Status(), tc.wantError)
			}
			if !tc.wantError && len(got.Events()) != 0 {
				t.Fatal("cancellation emitted exception")
			}
		})
	}
}

func TestUserSpeakingSpanEndsBeforeShutdownCommit(t *testing.T) {
	r := traceParityRecorder(t)
	s := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	s.Assistant = &fakeSessionAssistant{}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.updateUserStateAt(UserStateSpeaking, time.Now().Add(-time.Second))
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	spans := map[string]sdktrace.ReadOnlySpan{}
	for _, span := range r.Ended() {
		spans[span.Name()] = span
	}
	speech, turn := spans["user_speaking"], spans["user_turn"]
	if speech == nil || turn == nil {
		t.Fatal("shutdown omitted speech or turn")
	}
	if speech.EndTime().After(turn.EndTime()) {
		t.Fatal("speech outlived turn during shutdown")
	}
}

func TestCanceledInferencePreservesErrorWithoutFailureSpan(t *testing.T) {
	r := traceParityRecorder(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	streamErr := llm.NewAPIConnectionError("context canceled")
	model := &fakeGenerationLLM{stream: &fakeGenerationLLMStream{err: streamErr}}
	data, err := PerformLLMInference(ctx, model, llm.NewChatContext(), nil)
	if err != nil {
		t.Fatal(err)
	}
	<-data.Done
	if data.StreamErr != streamErr {
		t.Fatal("trace classification changed stream error")
	}
	found := false
	for _, span := range r.Ended() {
		if span.Name() == "llm_request" {
			found = true
			if span.Status().Code == codes.Error || len(span.Events()) != 0 {
				t.Fatal("cancellation became a failure")
			}
			attrs := spanAttributeValues(span.Attributes())
			reasons := attrs[telemetry.AttrGenAIResponseReasons].AsStringSlice()
			if len(reasons) != 1 || reasons[0] != "cancelled" {
				t.Fatalf("finish reasons = %v, want cancelled", reasons)
			}
		}
	}
	if !found {
		t.Fatal("missing request span")
	}
}
