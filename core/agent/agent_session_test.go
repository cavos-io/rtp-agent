package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	logutil "github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/library/utils/images"
	livekitlogger "github.com/livekit/protocol/logger"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestAgentSessionHistoryReturnsLiveContext(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.ChatCtx.Append(&llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleUser})

	history := session.History()
	if history != session.ChatCtx {
		t.Fatal("History() did not return internal chat context pointer")
	}
	if got := len(history.Items); got != 1 {
		t.Fatalf("History() item count = %d, want 1", got)
	}
	history.Append(&llm.ChatMessage{ID: "msg_2", Role: llm.ChatRoleAssistant})
	if got := len(session.ChatCtx.Items); got != 2 {
		t.Fatalf("mutating History() result left session ChatCtx item count at %d, want 2", got)
	}
	session.ChatCtx.Append(&llm.ChatMessage{ID: "msg_3", Role: llm.ChatRoleUser})
	if got := len(history.Items); got != 3 {
		t.Fatalf("mutating session ChatCtx left History() result item count at %d, want 3", got)
	}
}

func TestAgentSessionStopFlushesOTelTurnMetrics(t *testing.T) {
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() {
		otel.SetMeterProvider(previous)
	})

	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.ChatCtx.Append(&llm.ChatMessage{
		ID:   "msg_1",
		Role: llm.ChatRoleAssistant,
		Metrics: map[string]any{
			"llm_node_ttft": 0.5,
			"llm_metadata": map[string]any{
				"model_provider": "openai",
				"model_name":     "gpt-4o",
			},
		},
	})
	session.started = true

	if err := session.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	assertAgentFloatHistogramPoint(t, rm, "lk.agents.turn.llm_ttft", attribute.NewSet(
		attribute.String("model_provider", "openai"),
		attribute.String("model_name", "gpt-4o"),
	), 0.5)
}

func assertAgentFloatHistogramPoint(t *testing.T, rm metricdata.ResourceMetrics, name string, attrs attribute.Set, want float64) {
	t.Helper()

	for _, scope := range rm.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name != name {
				continue
			}
			histogram, ok := metric.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("%s data = %T, want float64 histogram", name, metric.Data)
			}
			for _, point := range histogram.DataPoints {
				if point.Attributes.Equals(&attrs) {
					if point.Count != 1 || point.Sum != want {
						t.Fatalf("%s point = count %d sum %v, want count 1 sum %v", name, point.Count, point.Sum, want)
					}
					return
				}
			}
			t.Fatalf("%s did not include attributes %v", name, attrs)
		}
	}
	t.Fatalf("missing metric %s", name)
}

func TestAgentSessionHistoryHandlesNilChatContext(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.ChatCtx = nil

	history := session.History()
	if history == nil {
		t.Fatal("History() = nil, want empty chat context")
	}
	if got := len(history.Items); got != 0 {
		t.Fatalf("History() item count = %d, want 0", got)
	}
}

func TestAgentSessionOptionsReturnsSnapshot(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{MaxToolSteps: 7})

	options := session.SessionOptions()
	if options.MaxToolSteps != 7 {
		t.Fatalf("SessionOptions().MaxToolSteps = %d, want 7", options.MaxToolSteps)
	}

	options.MaxToolSteps = 99
	if session.Options.MaxToolSteps != 7 {
		t.Fatalf("mutating SessionOptions() result changed session option to %d, want 7", session.Options.MaxToolSteps)
	}
}

func TestAgentSessionStateValueAccessorsReturnCurrentStates(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})

	if got, want := session.UserState(), UserStateListening; got != want {
		t.Fatalf("UserState() = %q, want %q", got, want)
	}
	if got, want := session.AgentState(), AgentStateInitializing; got != want {
		t.Fatalf("AgentState() = %q, want %q", got, want)
	}

	session.UpdateUserState(UserStateSpeaking)
	session.UpdateAgentState(AgentStateThinking)

	if got, want := session.UserState(), UserStateSpeaking; got != want {
		t.Fatalf("UserState() after update = %q, want %q", got, want)
	}
	if got, want := session.AgentState(), AgentStateThinking; got != want {
		t.Fatalf("AgentState() after update = %q, want %q", got, want)
	}
	if got, want := session.UserStateValue(), UserStateSpeaking; got != want {
		t.Fatalf("UserStateValue() compatibility alias = %q, want %q", got, want)
	}
	if got, want := session.AgentStateValue(), AgentStateThinking; got != want {
		t.Fatalf("AgentStateValue() compatibility alias = %q, want %q", got, want)
	}
}

func TestAgentSessionTurnDetectionReturnsUpdatedOption(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{
		TurnDetection: TurnDetectionModeManual,
	})

	if got := session.TurnDetection(); got != TurnDetectionModeManual {
		t.Fatalf("TurnDetection() = %q, want %q", got, TurnDetectionModeManual)
	}

	mode := TurnDetectionModeSTT
	if err := session.UpdateOptions(AgentSessionUpdateOptions{TurnDetection: &mode}); err != nil {
		t.Fatalf("UpdateOptions() error = %v", err)
	}
	if got := session.TurnDetection(); got != TurnDetectionModeSTT {
		t.Fatalf("TurnDetection() after UpdateOptions = %q, want %q", got, TurnDetectionModeSTT)
	}
}

func TestAgentSessionMCPServersReturnsLiveList(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	servers := []llm.MCPServer{&fakeSessionMCPServer{id: "lookup"}}

	session.SetMCPServers(servers)
	got := session.MCPServers()
	if len(got) != 1 || got[0] != servers[0] {
		t.Fatalf("MCPServers() = %#v, want configured server", got)
	}
	mutated := &fakeSessionMCPServer{id: "mutated"}
	got[0] = mutated

	gotAgain := session.MCPServers()
	if len(gotAgain) != 1 || gotAgain[0] != mutated {
		t.Fatal("mutating MCPServers() result did not update session server list")
	}
}

func TestNewAgentSessionCopiesInitialAgentTools(t *testing.T) {
	agent := NewAgent("test")
	lookup := &fakeGenerationTool{name: "lookup"}
	agent.Tools = []llm.Tool{lookup}

	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	if len(session.Tools) != 1 || session.Tools[0] != lookup {
		t.Fatalf("session.Tools = %#v, want initial agent tool", session.Tools)
	}
}

func TestAgentSessionUserInputTranscribedEventsFanOutToSubscribers(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	first := session.UserInputTranscribedEvents()
	second := session.UserInputTranscribedEvents()

	session.EmitUserInputTranscribed(UserInputTranscribedEvent{
		Transcript: "hello",
		IsFinal:    true,
	})

	assertUserTranscriptEvent(t, first, "first")
	assertUserTranscriptEvent(t, second, "second")
}

func TestAgentSessionUserTranscriptFilterAppliesBeforeFanOut(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.UserTranscriptFilter = func(text string) string {
		if text == "my secret code" {
			return "my [redacted] code"
		}
		return text
	}
	events := session.UserInputTranscribedEvents()

	session.EmitUserInputTranscribed(UserInputTranscribedEvent{
		Transcript: "my secret code",
		IsFinal:    true,
	})

	select {
	case ev := <-events:
		if ev.Transcript != "my [redacted] code" {
			t.Fatalf("transcript event = %q, want filtered transcript", ev.Transcript)
		}
	case <-time.After(time.Second):
		t.Fatal("UserInputTranscribedEvents did not receive filtered transcript")
	}

	recorded := session.RecordedEvents()
	if len(recorded) != 1 {
		t.Fatalf("RecordedEvents length = %d, want 1", len(recorded))
	}
	userEvent, ok := recorded[0].(*UserInputTranscribedEvent)
	if !ok {
		t.Fatalf("RecordedEvents[0] = %T, want *UserInputTranscribedEvent", recorded[0])
	}
	if userEvent.Transcript != "my [redacted] code" {
		t.Fatalf("recorded transcript = %q, want filtered transcript", userEvent.Transcript)
	}
}

func TestAgentSessionFinalTranscriptResetsAwayBeforeTranscriptEvent(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.UpdateUserState(UserStateAway)
	var order []string
	session.On("user_state_changed", func(ev Event) {
		stateEvent, ok := ev.(*UserStateChangedEvent)
		if ok && stateEvent.NewState == UserStateListening {
			order = append(order, "state:listening")
		}
	})
	session.On("user_input_transcribed", func(ev Event) {
		transcriptEvent, ok := ev.(*UserInputTranscribedEvent)
		if ok {
			order = append(order, "transcript:"+transcriptEvent.Transcript)
		}
	})

	session.EmitUserInputTranscribed(UserInputTranscribedEvent{
		Transcript: "hello",
		IsFinal:    true,
	})

	want := []string{"state:listening", "transcript:hello"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("event order = %#v, want %#v", order, want)
	}
}

func TestAgentSessionAgentOutputTranscribedEventsFanOutToSubscribers(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	first := session.AgentOutputTranscribedEvents()
	second := session.AgentOutputTranscribedEvents()

	session.EmitAgentOutputTranscribed(AgentOutputTranscribedEvent{
		Transcript: "hi",
	})

	assertAgentTranscriptEvent(t, first, "first")
	assertAgentTranscriptEvent(t, second, "second")
}

func TestAgentSessionUserStateChangedEventsFanOutToSubscribers(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	first := session.UserStateChangedEvents()
	second := session.UserStateChangedEvents()

	session.UpdateUserState(UserStateSpeaking)

	assertUserStateChangedEvent(t, first, "first")
	assertUserStateChangedEvent(t, second, "second")
}

func TestAgentSessionAudioDisabledEndsSpeakingState(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.UpdateUserState(UserStateSpeaking)

	session.OnAudioEnabledChanged(false)

	if got := session.UserState(); got != UserStateListening {
		t.Fatalf("UserState() = %q, want listening after audio disabled", got)
	}
}

func TestAgentSessionAgentStateChangedEventsFanOutToSubscribers(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	first := session.AgentStateChangedEvents()
	second := session.AgentStateChangedEvents()

	session.UpdateAgentState(AgentStateThinking)

	assertAgentStateChangedEvent(t, first, "first")
	assertAgentStateChangedEvent(t, second, "second")
}

func TestAgentSessionErrorEventsFanOutToSubscribers(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	first := session.ErrorEvents()
	second := session.ErrorEvents()

	session.EmitError(ErrorEvent{Error: errors.New("failed"), Source: "llm"})

	assertErrorEvent(t, first, "first")
	assertErrorEvent(t, second, "second")
}

func TestAgentSessionCloseEventsFanOutToSubscribers(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(session.Agent, session)
	session.started = true
	first := session.CloseEvents()
	second := session.CloseEvents()

	session.CloseSoon(CloseReasonUserInitiated)

	assertCloseEvent(t, first, "first")
	assertCloseEvent(t, second, "second")
}

func TestAgentSessionMetricsCollectedEventsFanOutToSubscribers(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	first := session.MetricsCollectedEvents()
	second := session.MetricsCollectedEvents()
	metrics := &telemetry.LLMMetrics{RequestID: "llm_req", PromptTokens: 3}

	session.EmitMetricsCollected(metrics)

	assertMetricsCollectedEvent(t, first, metrics, "first")
	assertMetricsCollectedEvent(t, second, metrics, "second")
}

func TestAgentSessionOnMetricsCollectedWarnsDeprecated(t *testing.T) {
	oldLogger := logutil.Logger
	recorder := &recordingLogger{}
	logutil.SetLogger(recorder)
	defer logutil.SetLogger(oldLogger)

	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})

	session.On("metrics_collected", func(Event) {})

	if len(recorder.warnMessages) != 1 {
		t.Fatalf("warn messages = %#v, want one deprecation warning", recorder.warnMessages)
	}
	if got := recorder.warnMessages[0]; got != "metrics_collected is deprecated. Use session_usage_updated for usage tracking and ChatMessage.metrics for per-turn latency." {
		t.Fatalf("warning = %q, want reference deprecation message", got)
	}
}

func TestAgentSessionUsageUpdatedEventsFanOutToSubscribers(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	first := session.SessionUsageUpdatedEvents()
	second := session.SessionUsageUpdatedEvents()

	session.EmitMetricsCollected(&telemetry.LLMMetrics{RequestID: "llm_req", PromptTokens: 3})

	assertUsageUpdatedEvent(t, first, "first")
	assertUsageUpdatedEvent(t, second, "second")
}

func TestAgentSessionConversationItemAddedEventsFanOutToSubscribers(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	first := session.ConversationItemAddedEvents()
	second := session.ConversationItemAddedEvents()
	msg := &llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleUser}

	session.EmitConversationItemAdded(msg)

	assertConversationItemAddedEvent(t, first, msg, "first")
	assertConversationItemAddedEvent(t, second, msg, "second")
}

func TestAgentSessionFunctionToolsExecutedEventsFanOutToSubscribers(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	first := session.FunctionToolsExecutedEvents()
	second := session.FunctionToolsExecutedEvents()
	call := &llm.FunctionCall{ID: "call_item", CallID: "call_lookup", Name: "lookup"}
	output := &llm.FunctionCallOutput{ID: "output_item", CallID: "call_lookup", Name: "lookup", Output: "ok"}
	ev, err := NewFunctionToolsExecutedEvent([]*llm.FunctionCall{call}, []*llm.FunctionCallOutput{output})
	if err != nil {
		t.Fatalf("NewFunctionToolsExecutedEvent error = %v", err)
	}

	session.EmitFunctionToolsExecuted(*ev)

	assertFunctionToolsExecutedEvent(t, first, call, output, "first")
	assertFunctionToolsExecutedEvent(t, second, call, output, "second")
}

func TestAgentSessionSpeechCreatedEventsFanOutToSubscribers(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	first := session.SpeechCreatedEvents()
	second := session.SpeechCreatedEvents()
	speech := NewSpeechHandle(true, DefaultInputDetails())

	session.EmitSpeechCreated(SpeechCreatedEvent{SpeechHandle: speech, Source: "say"})

	assertSpeechCreatedEvent(t, first, speech, "first")
	assertSpeechCreatedEvent(t, second, speech, "second")
}

func TestAgentSessionFalseInterruptionEventsFanOutToSubscribers(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	first := session.AgentFalseInterruptionEvents()
	second := session.AgentFalseInterruptionEvents()

	session.EmitAgentFalseInterruption(AgentFalseInterruptionEvent{Resumed: true})

	assertFalseInterruptionEvent(t, first, "first")
	assertFalseInterruptionEvent(t, second, "second")
}

func TestAgentSessionUserTurnExceededEventsFanOutToSubscribers(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	first := session.UserTurnExceededEvents()
	second := session.UserTurnExceededEvents()

	session.EmitUserTurnExceeded(UserTurnExceededEvent{Transcript: "too long"})

	assertUserTurnExceededEvent(t, first, "first")
	assertUserTurnExceededEvent(t, second, "second")
}

func TestAgentSessionOverlappingSpeechEventsFanOutToSubscribers(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	first := session.OverlappingSpeechEvents()
	second := session.OverlappingSpeechEvents()

	session.EmitOverlappingSpeech(OverlappingSpeechEvent{IsInterruption: true})

	assertOverlappingSpeechEvent(t, first, "first")
	assertOverlappingSpeechEvent(t, second, "second")
}

func assertUserTranscriptEvent(t *testing.T, events <-chan UserInputTranscribedEvent, name string) {
	t.Helper()

	select {
	case ev := <-events:
		if ev.Transcript != "hello" || !ev.IsFinal {
			t.Fatalf("%s subscriber event = %#v, want final hello transcript", name, ev)
		}
	case <-time.After(time.Second):
		t.Fatalf("%s subscriber did not receive user transcript event", name)
	}
}

func assertAgentTranscriptEvent(t *testing.T, events <-chan AgentOutputTranscribedEvent, name string) {
	t.Helper()

	select {
	case ev := <-events:
		if ev.Transcript != "hi" {
			t.Fatalf("%s subscriber event = %#v, want hi transcript", name, ev)
		}
	case <-time.After(time.Second):
		t.Fatalf("%s subscriber did not receive agent transcript event", name)
	}
}

func assertUserStateChangedEvent(t *testing.T, events <-chan UserStateChangedEvent, name string) {
	t.Helper()

	select {
	case ev := <-events:
		if ev.OldState != UserStateListening || ev.NewState != UserStateSpeaking {
			t.Fatalf("%s subscriber event = %#v, want listening to speaking", name, ev)
		}
	case <-time.After(time.Second):
		t.Fatalf("%s subscriber did not receive user state event", name)
	}
}

func assertAgentStateChangedEvent(t *testing.T, events <-chan AgentStateChangedEvent, name string) {
	t.Helper()

	select {
	case ev := <-events:
		if ev.OldState != AgentStateInitializing || ev.NewState != AgentStateThinking {
			t.Fatalf("%s subscriber event = %#v, want initializing to thinking", name, ev)
		}
	case <-time.After(time.Second):
		t.Fatalf("%s subscriber did not receive agent state event", name)
	}
}

func assertErrorEvent(t *testing.T, events <-chan ErrorEvent, name string) {
	t.Helper()

	select {
	case ev := <-events:
		if ev.Error == nil || ev.Error.Error() != "failed" || ev.Source != "llm" {
			t.Fatalf("%s subscriber event = %#v, want failed llm error", name, ev)
		}
	case <-time.After(time.Second):
		t.Fatalf("%s subscriber did not receive error event", name)
	}
}

func assertCloseEvent(t *testing.T, events <-chan CloseEvent, name string) {
	t.Helper()

	select {
	case ev := <-events:
		if ev.Reason != CloseReasonUserInitiated {
			t.Fatalf("%s subscriber event = %#v, want user initiated close", name, ev)
		}
	case <-time.After(time.Second):
		t.Fatalf("%s subscriber did not receive close event", name)
	}
}

func assertMetricsCollectedEvent(t *testing.T, events <-chan MetricsCollectedEvent, metrics telemetry.AgentMetrics, name string) {
	t.Helper()

	select {
	case ev := <-events:
		if ev.Metrics != metrics {
			t.Fatalf("%s subscriber metrics = %#v, want original metrics", name, ev.Metrics)
		}
	case <-time.After(time.Second):
		t.Fatalf("%s subscriber did not receive metrics event", name)
	}
}

func assertUsageUpdatedEvent(t *testing.T, events <-chan SessionUsageUpdatedEvent, name string) {
	t.Helper()

	select {
	case ev := <-events:
		if ev.Usage.LLMInputTokens() != 3 {
			t.Fatalf("%s subscriber usage = %#v, want 3 input tokens", name, ev.Usage)
		}
	case <-time.After(time.Second):
		t.Fatalf("%s subscriber did not receive usage event", name)
	}
}

func assertConversationItemAddedEvent(t *testing.T, events <-chan ConversationItemAddedEvent, item llm.ChatItem, name string) {
	t.Helper()

	select {
	case ev := <-events:
		if ev.Item != item {
			t.Fatalf("%s subscriber item = %#v, want original item", name, ev.Item)
		}
	case <-time.After(time.Second):
		t.Fatalf("%s subscriber did not receive conversation item event", name)
	}
}

func assertFunctionToolsExecutedEvent(t *testing.T, events <-chan FunctionToolsExecutedEvent, call *llm.FunctionCall, output *llm.FunctionCallOutput, name string) {
	t.Helper()

	select {
	case ev := <-events:
		if len(ev.FunctionCalls) != 1 || ev.FunctionCalls[0] != call {
			t.Fatalf("%s subscriber function calls = %#v, want original call", name, ev.FunctionCalls)
		}
		if len(ev.FunctionCallOutputs) != 1 || ev.FunctionCallOutputs[0] != output {
			t.Fatalf("%s subscriber function outputs = %#v, want original output", name, ev.FunctionCallOutputs)
		}
	case <-time.After(time.Second):
		t.Fatalf("%s subscriber did not receive function tools event", name)
	}
}

func assertSpeechCreatedEvent(t *testing.T, events <-chan SpeechCreatedEvent, speech *SpeechHandle, name string) {
	t.Helper()

	select {
	case ev := <-events:
		if ev.SpeechHandle != speech || ev.Source != "say" {
			t.Fatalf("%s subscriber event = %#v, want original say speech", name, ev)
		}
	case <-time.After(time.Second):
		t.Fatalf("%s subscriber did not receive speech created event", name)
	}
}

func assertFalseInterruptionEvent(t *testing.T, events <-chan AgentFalseInterruptionEvent, name string) {
	t.Helper()

	select {
	case ev := <-events:
		if !ev.Resumed {
			t.Fatalf("%s subscriber event = %#v, want resumed interruption", name, ev)
		}
	case <-time.After(time.Second):
		t.Fatalf("%s subscriber did not receive false interruption event", name)
	}
}

func assertUserTurnExceededEvent(t *testing.T, events <-chan UserTurnExceededEvent, name string) {
	t.Helper()

	select {
	case ev := <-events:
		if ev.Transcript != "too long" {
			t.Fatalf("%s subscriber event = %#v, want too long transcript", name, ev)
		}
	case <-time.After(time.Second):
		t.Fatalf("%s subscriber did not receive user turn exceeded event", name)
	}
}

func assertOverlappingSpeechEvent(t *testing.T, events <-chan OverlappingSpeechEvent, name string) {
	t.Helper()

	select {
	case ev := <-events:
		if !ev.IsInterruption {
			t.Fatalf("%s subscriber event = %#v, want interruption", name, ev)
		}
	case <-time.After(time.Second):
		t.Fatalf("%s subscriber did not receive overlapping speech event", name)
	}
}

func TestNewIVRActivityInitializesFromSessionStateAccessors(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.UpdateUserState(UserStateAway)
	session.UpdateAgentState(AgentStateListening)

	ivr := NewIVRActivity(session)
	defer ivr.Stop()

	if got, want := ivr.currentUserState, UserStateAway; got != want {
		t.Fatalf("currentUserState = %q, want %q", got, want)
	}
	if got, want := ivr.currentAgentState, AgentStateListening; got != want {
		t.Fatalf("currentAgentState = %q, want %q", got, want)
	}
}

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

func TestAgentSessionGenerateReplyUsesAgentAllowInterruptionsDefault(t *testing.T) {
	agent := NewAgent("test")
	agent.AllowInterruptions = true
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Options.AllowInterruptions = false
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.GenerateReply(context.Background(), "hello")

	if err != nil {
		t.Fatalf("GenerateReply error = %v, want nil", err)
	}
	if !handle.AllowInterruptions {
		t.Fatal("handle.AllowInterruptions = false, want agent default true")
	}
}

func TestAgentSessionGenerateReplyAgentAllowInterruptionsCanDisableSessionDefault(t *testing.T) {
	agent := NewAgent("test")
	agent.AllowInterruptions = false
	agent.AllowInterruptionsSet = true
	session := NewAgentSession(agent, nil, AgentSessionOptions{AllowInterruptions: true})
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.GenerateReply(context.Background(), "hello")

	if err != nil {
		t.Fatalf("GenerateReply error = %v, want nil", err)
	}
	if handle.AllowInterruptions {
		t.Fatal("handle.AllowInterruptions = true, want agent default false")
	}
}

func TestAgentSessionStartConfiguresTTSStreamPacer(t *testing.T) {
	baseAgent := NewAgent("test")
	baseAgent.VAD = &fakePipelineVAD{}
	baseAgent.STT = &fakePipelineSTT{}
	baseAgent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	baseAgent.TTS = &fakePipelineTTS{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{
		TTSStreamPacer: &tts.SentenceStreamPacerOptions{
			MinRemainingAudio: 25 * time.Millisecond,
			MaxTextLength:     42,
		},
	})

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}

	if session.Assistant == nil {
		t.Fatal("Assistant is nil")
	}
	pipeline, ok := session.Assistant.(*PipelineAgent)
	if !ok {
		t.Fatalf("Assistant = %T, want *PipelineAgent", session.Assistant)
	}
	if pipeline.ttsStreamPacer == nil {
		t.Fatal("Assistant ttsStreamPacer is nil")
	}
	if got := pipeline.ttsStreamPacer.MinRemainingAudio; got != 25*time.Millisecond {
		t.Fatalf("MinRemainingAudio = %v, want 25ms", got)
	}
	if got := pipeline.ttsStreamPacer.MaxTextLength; got != 42 {
		t.Fatalf("MaxTextLength = %d, want 42", got)
	}
}

func TestAgentSessionStartCreatesMultimodalAssistantWithRealtimeModel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.RealtimeModel = &fakeRealtimeModel{session: &fakeRealtimeSession{}}
	session.VAD = &fakePipelineVAD{}
	session.STT = &fakePipelineSTT{}
	session.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	session.TTS = &fakePipelineTTS{}

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v, want nil", err)
	}
	defer session.Stop(context.Background())

	if _, ok := session.Assistant.(*MultimodalAgent); !ok {
		t.Fatalf("Assistant = %T, want *MultimodalAgent", session.Assistant)
	}
}

func TestAgentSessionUsesAgentRealtimeModel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	baseAgent := NewAgent("test")
	baseAgent.RealtimeModel = &fakeRealtimeModel{session: &fakeRealtimeSession{}}
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{})

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v, want nil", err)
	}
	defer session.Stop(context.Background())

	if session.RealtimeModel != baseAgent.RealtimeModel {
		t.Fatalf("session.RealtimeModel = %#v, want agent realtime model", session.RealtimeModel)
	}
	if _, ok := session.Assistant.(*MultimodalAgent); !ok {
		t.Fatalf("Assistant = %T, want *MultimodalAgent", session.Assistant)
	}
}

func TestAgentSessionStartEnablesIVRDetectionActivity(t *testing.T) {
	baseAgent := NewAgent("test")
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{
		IVRDetection: true,
	})
	session.Assistant = &fakeSessionAssistant{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer session.Stop(context.Background())

	if session.ivrActivity == nil {
		t.Fatal("ivrActivity = nil, want configured IVR activity")
	}
}

func TestAgentSessionStartWithOptionsCapturesOnEnterSpeechRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agent := &onEnterSayAgent{Agent: NewAgent("test")}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	agent.session = session
	session.Assistant = &doneScheduledSpeechAssistant{}

	result, err := session.StartWithOptions(ctx, StartOptions{CaptureRun: true})
	if err != nil {
		t.Fatalf("StartWithOptions error = %v, want nil", err)
	}
	if result == nil {
		t.Fatal("StartWithOptions result = nil, want captured RunResult")
	}
	if err := result.Wait(ctx); err != nil {
		t.Fatalf("captured RunResult did not complete: %v", err)
	}
	events := result.Events()
	if len(events) != 1 {
		t.Fatalf("RunResult events length = %d, want on-enter assistant message", len(events))
	}
	msgEvent, ok := events[0].(*ChatMessageEvent)
	if !ok {
		t.Fatalf("events[0] = %T, want *ChatMessageEvent", events[0])
	}
	if msgEvent.Item.TextContent() != "hello from on enter" {
		t.Fatalf("message text = %q, want hello from on enter", msgEvent.Item.TextContent())
	}
}

func TestAgentSessionOnEnterGenerateReplyPreservesToolChoiceAndFiltersIgnoredTools(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agent := &onEnterGenerateReplyAgent{Agent: NewAgent("test")}
	toolChoice := llm.ToolChoice("auto")
	session := NewAgentSession(agent, nil, AgentSessionOptions{ToolChoice: toolChoice})
	agent.session = session
	session.Assistant = &fakeSessionAssistant{}
	speechEvents := session.SpeechCreatedEvents()

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v, want nil", err)
	}
	defer session.Stop(context.Background())

	select {
	case ev := <-speechEvents:
		if ev.Source != "generate_reply" {
			t.Fatalf("SpeechCreated Source = %q, want generate_reply", ev.Source)
		}
		if ev.SpeechHandle.Generation.ToolChoice != "auto" {
			t.Fatalf("OnEnter GenerateReply ToolChoice = %#v, want auto", ev.SpeechHandle.Generation.ToolChoice)
		}
		if !ev.SpeechHandle.Generation.IgnoreOnEnterTools {
			t.Fatal("OnEnter GenerateReply IgnoreOnEnterTools = false, want true")
		}
	case <-time.After(time.Second):
		t.Fatal("SpeechCreatedEvents did not receive OnEnter generate reply")
	}
}

func TestAgentSessionStartWithOptionsCaptureRunWaitsForSpeech(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.Assistant = &fakeSessionAssistant{}

	result, err := session.StartWithOptions(ctx, StartOptions{CaptureRun: true})

	if result != nil {
		t.Fatalf("StartWithOptions result = %#v, want nil while capture run has no speech", result)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StartWithOptions error = %v, want context deadline exceeded", err)
	}
}

func TestAgentSessionIVRDetectionGeneratesReplyAfterSilence(t *testing.T) {
	baseAgent := NewAgent("test")
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{
		IVRDetection:       true,
		IVRSilenceDuration: 10 * time.Millisecond,
	})
	session.Assistant = &fakeSessionAssistant{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer session.Stop(context.Background())

	session.UpdateAgentState(AgentStateIdle)
	session.UpdateUserState(UserStateListening)

	select {
	case ev := <-session.SpeechCreatedEvents():
		if ev.Source != "generate_reply" {
			t.Fatalf("SpeechCreated source = %q, want generate_reply", ev.Source)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for IVR silence generated reply")
	}
}

func TestAgentSessionOnVideoFrameSamplesAndForwardsToVideoAssistant(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	assistant := &fakeVideoSessionAssistant{}
	session.Assistant = assistant

	frame := &images.VideoFrame{}
	session.OnVideoFrame(context.Background(), frame)

	if assistant.videoFrames != 1 {
		t.Fatalf("videoFrames = %d, want first sampled frame forwarded", assistant.videoFrames)
	}
}

type fakeSessionAssistant struct{}

func (f *fakeSessionAssistant) Start(context.Context, *AgentSession) error { return nil }
func (f *fakeSessionAssistant) OnAudioFrame(context.Context, *model.AudioFrame) {
}
func (f *fakeSessionAssistant) SetPublishAudio(func(context.Context, *model.AudioFrame) error) {
}

type doneScheduledSpeechAssistant struct {
	fakeSessionAssistant
}

func (d *doneScheduledSpeechAssistant) OnSpeechScheduled(ctx context.Context, speech *SpeechHandle) {
	speech.MarkDone()
}

type onEnterSayAgent struct {
	*Agent
	session *AgentSession
}

func (a *onEnterSayAgent) OnEnter() {
	_, _ = a.session.Say(context.Background(), "hello from on enter")
}

type onEnterGenerateReplyAgent struct {
	*Agent
	session *AgentSession
}

func (a *onEnterGenerateReplyAgent) OnEnter() {
	_, _ = a.session.GenerateReply(context.Background(), "hello from on enter")
}

type fakeCloseableSessionAssistant struct {
	fakeSessionAssistant
	closed   int
	closeErr error
}

func (f *fakeCloseableSessionAssistant) Close() error {
	f.closed++
	return f.closeErr
}

type fakeInterruptingSessionAssistant struct {
	fakeSessionAssistant
	interrupts int
}

func (f *fakeInterruptingSessionAssistant) Interrupt() error {
	f.interrupts++
	return nil
}

type fakeVideoSessionAssistant struct {
	fakeSessionAssistant
	videoFrames int
}

func (f *fakeVideoSessionAssistant) OnVideoFrame(ctx context.Context, frame *images.VideoFrame) {
	f.videoFrames++
}

func TestAgentSessionStartStartsConfiguredAvatar(t *testing.T) {
	baseAgent := NewAgent("test")
	baseAgent.VAD = &fakePipelineVAD{}
	baseAgent.STT = &fakePipelineSTT{}
	baseAgent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	baseAgent.TTS = &fakePipelineTTS{}
	avatar := &fakeAvatarProvider{}
	baseAgent.Avatar = avatar

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{})

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}

	if avatar.startCalls != 1 {
		t.Fatalf("avatar startCalls = %d, want 1", avatar.startCalls)
	}
}

func TestAgentSessionStartReturnsAvatarStartError(t *testing.T) {
	errAvatar := errors.New("avatar start failed")
	baseAgent := NewAgent("test")
	baseAgent.VAD = &fakePipelineVAD{}
	baseAgent.STT = &fakePipelineSTT{}
	baseAgent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	baseAgent.TTS = &fakePipelineTTS{}
	baseAgent.Avatar = &fakeAvatarProvider{startErr: errAvatar}

	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{})

	err := session.Start(context.Background())

	if !errors.Is(err, errAvatar) {
		t.Fatalf("Start error = %v, want avatar error", err)
	}
	if session.started {
		t.Fatal("session started after avatar start error")
	}
}

func TestAgentSessionBackgroundAudioLifecycleWithoutRoom(t *testing.T) {
	player := NewBackgroundAudioPlayer(nil, nil)
	baseAgent := NewAgent("test")
	baseAgent.VAD = &fakePipelineVAD{}
	baseAgent.STT = &fakePipelineSTT{}
	baseAgent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	baseAgent.TTS = &fakePipelineTTS{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{
		BackgroundAudio: player,
	})

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	if player.mixerTaskCancel != nil {
		t.Fatal("background audio started without a room")
	}

	session.UpdateAgentState(AgentStateSpeaking)
	if got := player.targetVolume; got != 0.2 {
		t.Fatalf("targetVolume after speaking = %v, want ducked volume 0.2", got)
	}

	if err := session.Stop(context.Background()); err != nil {
		t.Fatalf("Stop error = %v", err)
	}
}

func TestNewAgentSessionInitializesUserStateListening(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	if got := session.UserState(); got != UserStateListening {
		t.Fatalf("UserState() = %q, want %q", got, UserStateListening)
	}
}

func TestNewAgentSessionInitializesAgentStateInitializing(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	if got := session.AgentState(); got != AgentStateInitializing {
		t.Fatalf("AgentState() = %q, want %q", got, AgentStateInitializing)
	}
}

func TestNewAgentSessionInitializesUsageCollector(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	if session.MetricsCollector == nil {
		t.Fatal("MetricsCollector = nil, want default usage collector")
	}
}

func TestAgentSessionUserdataRequiresValue(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	value, err := session.Userdata()

	if value != nil {
		t.Fatalf("Userdata value = %#v, want nil when unset", value)
	}
	if !errors.Is(err, ErrAgentSessionUserdataNotSet) {
		t.Fatalf("Userdata error = %v, want ErrAgentSessionUserdataNotSet", err)
	}
	if got, want := err.Error(), "AgentSession userdata is not set"; got != want {
		t.Fatalf("Userdata error text = %q, want %q", got, want)
	}
}

func TestAgentSessionUserdataCanBeSet(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	want := map[string]string{"customer_id": "cust_123"}

	session.SetUserdata(want)
	value, err := session.Userdata()

	if err != nil {
		t.Fatalf("Userdata error = %v, want nil", err)
	}
	got, ok := value.(map[string]string)
	if !ok {
		t.Fatalf("Userdata value = %T, want map[string]string", value)
	}
	if got["customer_id"] != want["customer_id"] {
		t.Fatalf("Userdata customer_id = %q, want %q", got["customer_id"], want["customer_id"])
	}
}

func TestNewAgentSessionAppliesReferenceOptionDefaults(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	opts := session.Options
	if !opts.AllowInterruptions {
		t.Fatal("AllowInterruptions = false, want default true")
	}
	if !opts.DiscardAudioIfUninterruptible {
		t.Fatal("DiscardAudioIfUninterruptible = false, want default true")
	}
	if opts.MinInterruptionDuration != 0.5 {
		t.Fatalf("MinInterruptionDuration = %v, want 0.5", opts.MinInterruptionDuration)
	}
	if opts.MinInterruptionWords != 0 {
		t.Fatalf("MinInterruptionWords = %d, want 0", opts.MinInterruptionWords)
	}
	if opts.MinEndpointingDelay != 0.5 {
		t.Fatalf("MinEndpointingDelay = %v, want 0.5", opts.MinEndpointingDelay)
	}
	if opts.MaxEndpointingDelay != 3.0 {
		t.Fatalf("MaxEndpointingDelay = %v, want 3.0", opts.MaxEndpointingDelay)
	}
	if opts.FalseInterruptionTimeout != 2.0 {
		t.Fatalf("FalseInterruptionTimeout = %v, want 2.0", opts.FalseInterruptionTimeout)
	}
	if !opts.ResumeFalseInterruption {
		t.Fatal("ResumeFalseInterruption = false, want default true")
	}
	if opts.BackchannelBoundaryStart != 1.0 {
		t.Fatalf("BackchannelBoundaryStart = %v, want 1.0", opts.BackchannelBoundaryStart)
	}
	if opts.BackchannelBoundaryEnd != 1.0 {
		t.Fatalf("BackchannelBoundaryEnd = %v, want 1.0", opts.BackchannelBoundaryEnd)
	}
	if opts.UserAwayTimeout != 15.0 {
		t.Fatalf("UserAwayTimeout = %v, want 15.0", opts.UserAwayTimeout)
	}
	if !opts.PreemptiveGeneration {
		t.Fatal("PreemptiveGeneration = false, want default true")
	}
	if opts.PreemptiveGenerationPreemptiveTTS {
		t.Fatal("PreemptiveGenerationPreemptiveTTS = true, want default false")
	}
	if opts.PreemptiveGenerationMaxSpeechDuration != 10.0 {
		t.Fatalf("PreemptiveGenerationMaxSpeechDuration = %v, want 10.0", opts.PreemptiveGenerationMaxSpeechDuration)
	}
	if opts.PreemptiveGenerationMaxRetries != 3 {
		t.Fatalf("PreemptiveGenerationMaxRetries = %d, want 3", opts.PreemptiveGenerationMaxRetries)
	}
	if opts.AECWarmupDuration != 3.0 {
		t.Fatalf("AECWarmupDuration = %v, want 3.0", opts.AECWarmupDuration)
	}
	if opts.SessionCloseTranscriptTimeout != 2.0 {
		t.Fatalf("SessionCloseTranscriptTimeout = %v, want 2.0", opts.SessionCloseTranscriptTimeout)
	}
	if !opts.TTSTextTransformsSet {
		t.Fatal("TTSTextTransformsSet = false, want default transform list marked set")
	}
	if want := []string{"filter_markdown", "filter_emoji"}; !reflect.DeepEqual(opts.TTSTextTransforms, want) {
		t.Fatalf("TTSTextTransforms = %#v, want %#v", opts.TTSTextTransforms, want)
	}
}

func TestNewAgentSessionPreservesExplicitFalseTurnOptions(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{
		ResumeFalseInterruption:    false,
		ResumeFalseInterruptionSet: true,
		PreemptiveGeneration:       false,
		PreemptiveGenerationSet:    true,
	})

	if session.Options.ResumeFalseInterruption {
		t.Fatal("ResumeFalseInterruption = true, want explicit false")
	}
	if session.Options.PreemptiveGeneration {
		t.Fatal("PreemptiveGeneration = true, want explicit false")
	}
}

func TestNewAgentSessionPreservesExplicitPreemptiveGenerationOptions(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{
		PreemptiveGenerationPreemptiveTTS:        true,
		PreemptiveGenerationPreemptiveTTSSet:     true,
		PreemptiveGenerationMaxSpeechDuration:    4.5,
		PreemptiveGenerationMaxSpeechDurationSet: true,
		PreemptiveGenerationMaxRetries:           7,
		PreemptiveGenerationMaxRetriesSet:        true,
	})

	if !session.Options.PreemptiveGenerationPreemptiveTTS {
		t.Fatal("PreemptiveGenerationPreemptiveTTS = false, want explicit true")
	}
	if session.Options.PreemptiveGenerationMaxSpeechDuration != 4.5 {
		t.Fatalf("PreemptiveGenerationMaxSpeechDuration = %v, want 4.5", session.Options.PreemptiveGenerationMaxSpeechDuration)
	}
	if session.Options.PreemptiveGenerationMaxRetries != 7 {
		t.Fatalf("PreemptiveGenerationMaxRetries = %d, want 7", session.Options.PreemptiveGenerationMaxRetries)
	}
}

func TestNewAgentSessionPreservesExplicitFalseAllowInterruptions(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		AllowInterruptions:    false,
		AllowInterruptionsSet: true,
	})

	if session.Options.AllowInterruptions {
		t.Fatal("AllowInterruptions = true, want explicit false")
	}
	activity := NewAgentActivity(agent, session)
	if activity.AllowInterruptions() {
		t.Fatal("AgentActivity.AllowInterruptions() = true, want explicit session false")
	}
	if activity.InterruptionEnabled() {
		t.Fatal("AgentActivity.InterruptionEnabled() = true, want false when interruptions are disabled")
	}
}

func TestNewAgentSessionPreservesExplicitFalseDiscardAudioIfUninterruptible(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		DiscardAudioIfUninterruptible:    false,
		DiscardAudioIfUninterruptibleSet: true,
	})

	if session.Options.DiscardAudioIfUninterruptible {
		t.Fatal("DiscardAudioIfUninterruptible = true, want explicit false")
	}
	session.activity = NewAgentActivity(agent, session)
	session.activity.currentSpeech = NewSpeechHandle(false, DefaultInputDetails())
	if session.shouldSilenceInputAudio() {
		t.Fatal("shouldSilenceInputAudio() = true, want false when discard audio is explicitly disabled")
	}
}

func TestNewAgentSessionPreservesExplicitZeroMaxToolSteps(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{
		MaxToolSteps:    0,
		MaxToolStepsSet: true,
	})

	if session.Options.MaxToolSteps != 0 {
		t.Fatalf("MaxToolSteps = %d, want explicit zero", session.Options.MaxToolSteps)
	}
}

func TestNewAgentSessionPreservesExplicitZeroMinInterruptionDuration(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		MinInterruptionDuration:    0,
		MinInterruptionDurationSet: true,
	})

	if session.Options.MinInterruptionDuration != 0 {
		t.Fatalf("MinInterruptionDuration = %v, want explicit zero preserved", session.Options.MinInterruptionDuration)
	}
	activity := NewAgentActivity(agent, session)
	if got := activity.minInterruptionDuration(); got != 0 {
		t.Fatalf("AgentActivity.minInterruptionDuration() = %v, want explicit zero", got)
	}
}

func TestNewAgentSessionPreservesExplicitZeroFalseInterruptionTimeout(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{
		FalseInterruptionTimeout:    0,
		FalseInterruptionTimeoutSet: true,
	})

	if session.Options.FalseInterruptionTimeout != 0 {
		t.Fatalf("FalseInterruptionTimeout = %v, want explicit zero preserved", session.Options.FalseInterruptionTimeout)
	}
}

func TestNewAgentSessionPreservesExplicitZeroBackchannelBoundaryEnd(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{
		BackchannelBoundaryStart:    0,
		BackchannelBoundaryStartSet: true,
		BackchannelBoundaryEnd:      0,
		BackchannelBoundaryEndSet:   true,
	})

	if session.Options.BackchannelBoundaryStart != 0 {
		t.Fatalf("BackchannelBoundaryStart = %v, want explicit zero preserved", session.Options.BackchannelBoundaryStart)
	}
	if session.Options.BackchannelBoundaryEnd != 0 {
		t.Fatalf("BackchannelBoundaryEnd = %v, want explicit zero preserved", session.Options.BackchannelBoundaryEnd)
	}
}

func TestNewAgentSessionPreservesExplicitZeroMinEndpointingDelay(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{
		MinEndpointingDelay:    0,
		MinEndpointingDelaySet: true,
	})

	if session.Options.MinEndpointingDelay != 0 {
		t.Fatalf("MinEndpointingDelay = %v, want explicit zero preserved", session.Options.MinEndpointingDelay)
	}
	if session.Options.Endpointing == nil {
		t.Fatal("Endpointing = nil, want default endpointing policy")
	}
	if got := session.Options.Endpointing.MinDelay(); got != 0 {
		t.Fatalf("Endpointing.MinDelay() = %v, want explicit zero", got)
	}
}

func TestNewAgentSessionPreservesExplicitZeroMaxEndpointingDelay(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{
		MaxEndpointingDelay:    0,
		MaxEndpointingDelaySet: true,
	})

	if session.Options.MaxEndpointingDelay != 0 {
		t.Fatalf("MaxEndpointingDelay = %v, want explicit zero preserved", session.Options.MaxEndpointingDelay)
	}
	if session.Options.Endpointing == nil {
		t.Fatal("Endpointing = nil, want default endpointing policy")
	}
	if got := session.Options.Endpointing.MaxDelay(); got != 0 {
		t.Fatalf("Endpointing.MaxDelay() = %v, want explicit zero", got)
	}
}

func TestNewAgentSessionPreservesExplicitZeroSessionCloseTranscriptTimeout(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{
		SessionCloseTranscriptTimeout:    0,
		SessionCloseTranscriptTimeoutSet: true,
	})

	if session.Options.SessionCloseTranscriptTimeout != 0 {
		t.Fatalf("SessionCloseTranscriptTimeout = %v, want explicit zero preserved", session.Options.SessionCloseTranscriptTimeout)
	}
}

func TestNewAgentSessionPreservesExplicitZeroAECWarmupDuration(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{
		AECWarmupDuration:    0,
		AECWarmupDurationSet: true,
	})

	if session.Options.AECWarmupDuration != 0 {
		t.Fatalf("AECWarmupDuration = %v, want explicit zero preserved", session.Options.AECWarmupDuration)
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

func TestAgentSessionSayReturnsScheduledSpeechHandle(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.Say(context.Background(), "hello")

	if err != nil {
		t.Fatalf("Say error = %v, want nil", err)
	}
	if handle == nil {
		t.Fatal("Say handle = nil, want speech handle")
	}
	if !handle.IsScheduled() {
		t.Fatal("Say returned unscheduled handle")
	}
	if !handle.AllowInterruptions {
		t.Fatal("handle.AllowInterruptions = false, want session default true")
	}
	if got, want := handle.InputDetails.Modality, "text"; got != want {
		t.Fatalf("handle.InputDetails.Modality = %q, want %q", got, want)
	}
}

func TestAgentSessionSayUsesAgentAllowInterruptionsDefault(t *testing.T) {
	agent := NewAgent("test")
	agent.AllowInterruptions = true
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Options.AllowInterruptions = false
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.Say(context.Background(), "hello")

	if err != nil {
		t.Fatalf("Say error = %v, want nil", err)
	}
	if !handle.AllowInterruptions {
		t.Fatal("handle.AllowInterruptions = false, want agent default true")
	}
}

func TestAgentSessionSayAgentAllowInterruptionsCanDisableSessionDefault(t *testing.T) {
	agent := NewAgent("test")
	agent.AllowInterruptions = false
	agent.AllowInterruptionsSet = true
	session := NewAgentSession(agent, nil, AgentSessionOptions{AllowInterruptions: true})
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.Say(context.Background(), "hello")

	if err != nil {
		t.Fatalf("Say error = %v, want nil", err)
	}
	if handle.AllowInterruptions {
		t.Fatal("handle.AllowInterruptions = true, want agent default false")
	}
}

func TestAgentSessionSayEmitsSpeechCreatedEvent(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	before := time.Now()

	handle, err := session.Say(context.Background(), "hello")

	if err != nil {
		t.Fatalf("Say error = %v, want nil", err)
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
			t.Fatal("UserInitiated = false, want true for Say")
		}
		if ev.Source != "say" {
			t.Fatalf("Source = %q, want say", ev.Source)
		}
		if ev.CreatedAt.Before(before) || ev.CreatedAt.IsZero() {
			t.Fatalf("CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
		}
	case <-time.After(time.Second):
		t.Fatal("SpeechCreatedEvents did not receive say speech")
	}
}

func TestAgentSessionSayAddsAssistantTextToChatContextByDefault(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)

	if _, err := session.Say(context.Background(), "hello from agent"); err != nil {
		t.Fatalf("Say error = %v, want nil", err)
	}

	if len(session.ChatCtx.Items) != 1 {
		t.Fatalf("ChatCtx.Items length = %d, want 1", len(session.ChatCtx.Items))
	}
	msg, ok := session.ChatCtx.Items[0].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("ChatCtx item type = %T, want *llm.ChatMessage", session.ChatCtx.Items[0])
	}
	if msg.Role != llm.ChatRoleAssistant || msg.TextContent() != "hello from agent" {
		t.Fatalf("ChatCtx message = %#v, want assistant message with text", msg)
	}
	select {
	case ev := <-session.ConversationItemAddedEvents():
		if ev.Item != msg {
			t.Fatalf("ConversationItemAdded item = %#v, want committed assistant message", ev.Item)
		}
	case <-time.After(time.Second):
		t.Fatal("ConversationItemAddedEvents did not receive say text")
	}
}

func TestAgentSessionSayOptionsOverrideInterruptionsAndChatContext(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	allowInterruptions := false
	addToChatContext := false

	handle, err := session.SayWithOptions(context.Background(), SayOptions{
		Text:               "private aside",
		AllowInterruptions: &allowInterruptions,
		AddToChatContext:   &addToChatContext,
	})

	if err != nil {
		t.Fatalf("SayWithOptions error = %v, want nil", err)
	}
	if handle.AllowInterruptions {
		t.Fatal("handle.AllowInterruptions = true, want per-call false override")
	}
	if len(session.ChatCtx.Items) != 0 {
		t.Fatalf("ChatCtx.Items length = %d, want 0 when AddToChatContext is false", len(session.ChatCtx.Items))
	}
	select {
	case ev := <-session.ConversationItemAddedEvents():
		t.Fatalf("ConversationItemAdded event = %#v, want none when AddToChatContext is false", ev)
	default:
	}
}

func TestAgentSessionSayWatchesActiveRunState(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	result := NewRunResult(session.ChatCtx)
	session.runState = result

	handle, err := session.SayWithOptions(context.Background(), SayOptions{
		Text: "hello",
	})
	if err != nil {
		t.Fatalf("SayWithOptions error = %v, want nil", err)
	}
	if result.Done() {
		t.Fatal("run result marked done before watched say speech completed")
	}

	handle.MarkDone()

	if !result.Done() {
		t.Fatal("run result not marked done after say speech completed")
	}
}

func TestAgentSessionGenerateReplyWatchesActiveRunState(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	result := NewRunResult(session.ChatCtx)
	session.runState = result

	handle, err := session.GenerateReply(context.Background(), "hello")
	if err != nil {
		t.Fatalf("GenerateReply error = %v, want nil", err)
	}
	if result.Done() {
		t.Fatal("run result marked done before watched generated speech completed")
	}

	handle.MarkDone()

	if !result.Done() {
		t.Fatal("run result not marked done after generated speech completed")
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

func TestAgentSessionRecordsEmittedEvents(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	cause := errors.New("provider failed")

	session.EmitError(ErrorEvent{Error: cause, Source: "llm"})

	events := session.RecordedEvents()
	if len(events) != 1 {
		t.Fatalf("RecordedEvents length = %d, want 1", len(events))
	}
	ev, ok := events[0].(*ErrorEvent)
	if !ok {
		t.Fatalf("RecordedEvents[0] = %T, want *ErrorEvent", events[0])
	}
	if !errors.Is(ev.Error, cause) || ev.Source != "llm" {
		t.Fatalf("recorded error event = %#v, want original error/source", ev)
	}
}

func TestAgentSessionOnReceivesRecordedEmittedEvents(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	cause := errors.New("provider failed")
	received := make(chan Event, 1)

	session.On("error", func(ev Event) {
		if len(session.RecordedEvents()) != 1 {
			t.Fatalf("RecordedEvents length during callback = %d, want 1", len(session.RecordedEvents()))
		}
		received <- ev
	})

	session.EmitError(ErrorEvent{Error: cause, Source: "llm"})

	select {
	case ev := <-received:
		errEvent, ok := ev.(*ErrorEvent)
		if !ok {
			t.Fatalf("listener event = %T, want *ErrorEvent", ev)
		}
		if !errors.Is(errEvent.Error, cause) || errEvent.Source != "llm" {
			t.Fatalf("listener error event = %#v, want original error/source", errEvent)
		}
	case <-time.After(time.Second):
		t.Fatal("listener did not receive emitted event")
	}
}

func TestAgentSessionEventListenerPanicDoesNotBlockOtherListeners(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	received := make(chan Event, 1)

	session.On("error", func(Event) {
		panic("listener failed")
	})
	session.On("error", func(ev Event) {
		received <- ev
	})

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("EmitError panic = %v, want listener panic isolated", recovered)
		}
		select {
		case ev := <-received:
			if _, ok := ev.(*ErrorEvent); !ok {
				t.Fatalf("remaining listener event = %T, want *ErrorEvent", ev)
			}
		default:
			t.Fatal("remaining listener was not called after listener panic")
		}
	}()

	session.EmitError(ErrorEvent{Error: errors.New("provider failed"), Source: "llm"})
}

func TestAgentSessionOnReturnsUnsubscribe(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	received := make(chan Event, 1)

	unsubscribe := session.On("error", func(ev Event) {
		received <- ev
	})
	unsubscribe()

	session.EmitError(ErrorEvent{Error: errors.New("provider failed"), Source: "llm"})

	select {
	case ev := <-received:
		t.Fatalf("unsubscribed listener received event: %#v", ev)
	default:
	}
}

func TestAgentSessionOffRemovesMatchingListener(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	removed := make(chan Event, 1)
	kept := make(chan Event, 1)

	callback := func(ev Event) {
		removed <- ev
	}
	session.On("error", callback)
	session.On("error", func(ev Event) {
		kept <- ev
	})

	session.Off("error", callback)
	session.EmitError(ErrorEvent{Error: errors.New("provider failed"), Source: "llm"})

	select {
	case ev := <-removed:
		t.Fatalf("removed listener received event: %#v", ev)
	default:
	}
	select {
	case ev := <-kept:
		if _, ok := ev.(*ErrorEvent); !ok {
			t.Fatalf("kept listener event = %T, want *ErrorEvent", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("remaining listener did not receive emitted event")
	}
}

func TestAgentSessionCloseListenerPanicDoesNotBlockOtherListeners(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.started = true
	received := make(chan Event, 1)

	session.On("close", func(Event) {
		panic("close listener failed")
	})
	session.On("close", func(ev Event) {
		received <- ev
	})

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("CloseSoon panic = %v, want close listener panic isolated", recovered)
		}
		select {
		case ev := <-received:
			if _, ok := ev.(*CloseEvent); !ok {
				t.Fatalf("remaining close listener event = %T, want *CloseEvent", ev)
			}
		default:
			t.Fatal("remaining close listener was not called after listener panic")
		}
	}()

	session.CloseSoon(CloseReasonUserInitiated)
}

func TestAgentSessionOffUsesCallbackIdentity(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	first := make(chan Event, 1)
	second := make(chan Event, 1)
	makeCallback := func(ch chan<- Event) func(Event) {
		return func(ev Event) {
			ch <- ev
		}
	}
	firstCallback := makeCallback(first)
	secondCallback := makeCallback(second)

	session.On("error", firstCallback)
	session.On("error", secondCallback)
	session.Off("error", secondCallback)

	session.EmitError(ErrorEvent{Error: errors.New("provider failed"), Source: "llm"})

	select {
	case ev := <-first:
		if _, ok := ev.(*ErrorEvent); !ok {
			t.Fatalf("first listener event = %T, want *ErrorEvent", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("first listener did not receive emitted event")
	}
	select {
	case ev := <-second:
		t.Fatalf("removed second listener received event: %#v", ev)
	default:
	}
}

func TestAgentSessionOnceReceivesOnlyFirstMatchingEvent(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	first := errors.New("first failure")
	second := errors.New("second failure")
	received := make(chan Event, 2)

	session.Once("error", func(ev Event) {
		received <- ev
	})

	session.EmitError(ErrorEvent{Error: first, Source: "llm"})
	session.EmitError(ErrorEvent{Error: second, Source: "tts"})

	select {
	case ev := <-received:
		errEvent, ok := ev.(*ErrorEvent)
		if !ok {
			t.Fatalf("listener event = %T, want *ErrorEvent", ev)
		}
		if !errors.Is(errEvent.Error, first) || errEvent.Source != "llm" {
			t.Fatalf("listener error event = %#v, want first error/source", errEvent)
		}
	case <-time.After(time.Second):
		t.Fatal("one-shot listener did not receive first emitted event")
	}
	select {
	case ev := <-received:
		t.Fatalf("one-shot listener received second event: %#v", ev)
	default:
	}
}

func TestAgentSessionOnceReturnsUnsubscribe(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	received := make(chan Event, 1)

	unsubscribe := session.Once("error", func(ev Event) {
		received <- ev
	})
	unsubscribe()

	session.EmitError(ErrorEvent{Error: errors.New("provider failed"), Source: "llm"})

	select {
	case ev := <-received:
		t.Fatalf("unsubscribed one-shot listener received event: %#v", ev)
	default:
	}
}

func TestAgentSessionCloseSoonClearsEventListeners(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.started = true

	closeEvents := make(chan Event, 1)
	errorEvents := make(chan Event, 1)
	session.On("close", func(ev Event) {
		closeEvents <- ev
	})
	session.On("error", func(ev Event) {
		errorEvents <- ev
	})

	session.CloseSoon(CloseReasonUserInitiated)

	select {
	case ev := <-closeEvents:
		closeEvent, ok := ev.(*CloseEvent)
		if !ok {
			t.Fatalf("close listener event = %T, want *CloseEvent", ev)
		}
		if closeEvent.Reason != CloseReasonUserInitiated {
			t.Fatalf("close reason = %q, want %q", closeEvent.Reason, CloseReasonUserInitiated)
		}
	case <-time.After(time.Second):
		t.Fatal("close listener did not receive close event")
	}

	session.EmitError(ErrorEvent{Error: errors.New("after close"), Source: "llm"})

	select {
	case ev := <-errorEvents:
		t.Fatalf("closed-session listener received later event: %#v", ev)
	default:
	}
}

func TestAgentSessionCloseSoonEmitsCloseAfterCleanup(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	session.started = true
	current := NewSpeechHandle(true, DefaultInputDetails())
	session.activity.currentSpeech = current
	session.activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "closing turn", Confidence: 0.9}},
	})

	done := make(chan struct{}, 1)
	go func() {
		session.CloseSoon(CloseReasonUserInitiated)
		done <- struct{}{}
	}()

	closeEvents := session.CloseEvents()
	time.Sleep(20 * time.Millisecond)
	select {
	case ev := <-closeEvents:
		t.Fatalf("CloseSoon emitted close before cleanup completed: %#v", ev)
	case <-done:
		t.Fatal("CloseSoon returned before active speech cleanup completed")
	default:
	}

	current.MarkDone()
	select {
	case <-done:
	case <-testTimeout():
		t.Fatal("CloseSoon did not finish after speech completed")
	}
	select {
	case ev := <-closeEvents:
		if ev.Reason != CloseReasonUserInitiated {
			t.Fatalf("close reason = %q, want user_initiated", ev.Reason)
		}
	default:
		t.Fatal("CloseSoon did not emit close after cleanup completed")
	}
}

func TestAgentSessionRecordedEventsReturnsCopy(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.EmitError(ErrorEvent{Error: errors.New("failed"), Source: "llm"})

	events := session.RecordedEvents()
	events[0] = nil

	if session.RecordedEvents()[0] == nil {
		t.Fatal("RecordedEvents returned mutable backing storage")
	}
}

func TestAgentSessionRecordsStateChangedEvents(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	session.UpdateAgentState(AgentStateThinking)
	session.UpdateUserState(UserStateSpeaking)

	events := session.RecordedEvents()
	if len(events) != 2 {
		t.Fatalf("RecordedEvents length = %d, want 2", len(events))
	}
	if ev, ok := events[0].(*AgentStateChangedEvent); !ok || ev.NewState != AgentStateThinking {
		t.Fatalf("RecordedEvents[0] = %#v, want agent state changed to thinking", events[0])
	}
	if ev, ok := events[1].(*UserStateChangedEvent); !ok || ev.NewState != UserStateSpeaking {
		t.Fatalf("RecordedEvents[1] = %#v, want user state changed to speaking", events[1])
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

func TestAgentSessionEmitUserTurnExceededDispatchesAgentHook(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)

	session.EmitUserTurnExceeded(UserTurnExceededEvent{Transcript: "extended user turn"})

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatal("user turn exceeded hook did not schedule cut-in speech")
		case <-ticker.C:
			session.activity.queueMu.Lock()
			var handle *SpeechHandle
			if len(session.activity.speechQueue) > 0 {
				handle = session.activity.speechQueue[0].speech
			}
			session.activity.queueMu.Unlock()
			if handle == nil {
				continue
			}
			if handle.AllowInterruptions {
				t.Fatal("handle.AllowInterruptions = true, want false for exceeded-turn cut-in")
			}
			if handle.Generation.ToolChoice != "none" {
				t.Fatalf("ToolChoice = %#v, want none", handle.Generation.ToolChoice)
			}
			return
		}
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

func TestAgentSessionGenerateReplyOptionsAcceptUserMessage(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	userMessage := &llm.ChatMessage{
		ID:        "custom_user",
		Role:      llm.ChatRoleUser,
		Content:   []llm.ChatContent{{Text: "custom input"}},
		CreatedAt: time.Now(),
	}

	handle, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserMessage:   userMessage,
		InputModality: "text",
	})

	if err != nil {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil", err)
	}
	if handle.Generation.UserMessage != userMessage {
		t.Fatalf("handle.Generation.UserMessage = %#v, want supplied user message", handle.Generation.UserMessage)
	}
	if len(session.ChatCtx.Items) != 1 || session.ChatCtx.Items[0] != userMessage {
		t.Fatalf("ChatCtx.Items = %#v, want supplied user message", session.ChatCtx.Items)
	}
	select {
	case ev := <-session.ConversationItemAddedEvents():
		if ev.Item != userMessage {
			t.Fatalf("ConversationItemAdded item = %#v, want supplied user message", ev.Item)
		}
	case <-time.After(time.Second):
		t.Fatal("ConversationItemAddedEvents did not receive supplied user message")
	}
}

func TestAgentSessionGenerateReplyOptionsCanCreateUnscheduledSpeech(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	speechEvents := session.SpeechCreatedEvents()
	conversationEvents := session.ConversationItemAddedEvents()
	scheduleSpeech := false
	userMessage := &llm.ChatMessage{
		ID:        "preemptive_user",
		Role:      llm.ChatRoleUser,
		Content:   []llm.ChatContent{{Text: "preemptive input"}},
		CreatedAt: time.Now(),
	}

	handle, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserMessage:    userMessage,
		InputModality:  "audio",
		ScheduleSpeech: &scheduleSpeech,
	})

	if err != nil {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil", err)
	}
	if handle == nil {
		t.Fatal("GenerateReplyWithOptions handle = nil, want unscheduled speech handle")
	}
	if handle.IsScheduled() {
		t.Fatal("speech handle was scheduled, want preemptive unscheduled handle")
	}
	if handle.Generation.UserMessage != userMessage {
		t.Fatalf("handle.Generation.UserMessage = %#v, want supplied user message", handle.Generation.UserMessage)
	}
	if len(session.ChatCtx.Items) != 0 {
		t.Fatalf("session ChatCtx items = %#v, want no committed user message before scheduling", session.ChatCtx.Items)
	}
	select {
	case ev := <-speechEvents:
		if ev.SpeechHandle != handle || !ev.UserInitiated || ev.Source != "generate_reply" {
			t.Fatalf("SpeechCreated event = %#v, want unscheduled generate_reply handle", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("SpeechCreatedEvents did not receive unscheduled generate reply")
	}
	select {
	case ev := <-conversationEvents:
		t.Fatalf("ConversationItemAdded event = %#v, want no chat commit before scheduling", ev)
	default:
	}
}

func TestAgentSessionGenerateReplyRejectsMissingLLM(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	session.Assistant = NewPipelineAgent(nil, nil, nil, nil, session.ChatCtx)
	speechEvents := session.SpeechCreatedEvents()

	handle, err := session.GenerateReply(context.Background(), "hello")

	if handle != nil {
		t.Fatalf("GenerateReply handle = %#v, want nil without LLM", handle)
	}
	if err == nil {
		t.Fatal("GenerateReply error = nil, want missing LLM error")
	}
	if got, want := err.Error(), "trying to generate reply without an LLM model"; got != want {
		t.Fatalf("GenerateReply error = %q, want %q", got, want)
	}
	select {
	case ev := <-speechEvents:
		t.Fatalf("SpeechCreated event = %#v, want no speech without LLM", ev)
	default:
	}
}

func TestAgentSessionGenerateReplyOptionsPreferUserMessageOverString(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	userMessage := &llm.ChatMessage{
		ID:        "custom_user",
		Role:      llm.ChatRoleUser,
		Content:   []llm.ChatContent{{Text: "custom input"}},
		CreatedAt: time.Now(),
	}

	_, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserInput:     "string input",
		UserMessage:   userMessage,
		InputModality: "text",
	})

	if err != nil {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil", err)
	}
	if len(session.ChatCtx.Items) != 1 || session.ChatCtx.Items[0] != userMessage {
		t.Fatalf("ChatCtx.Items = %#v, want only supplied user message", session.ChatCtx.Items)
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

func TestAgentSessionGenerateReplyOptionsPreserveInstructions(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserInput:     "hello",
		Instructions:  "answer briefly",
		InputModality: "text",
	})

	if err != nil {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil", err)
	}
	if handle.Generation.Instructions == nil {
		t.Fatal("handle.Generation.Instructions = nil, want per-call instructions")
	}
	if got := handle.Generation.Instructions.AsModality("text").String(); got != "answer briefly" {
		t.Fatalf("handle.Generation.Instructions text = %q, want answer briefly", got)
	}
}

func TestAgentSessionGenerateReplyOptionsPreserveInstructionVariants(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	instructions := llm.NewInstructions("speak plainly", "write tersely")

	handle, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserInput:           "hello",
		InstructionVariants: instructions,
		InputModality:       "audio",
	})

	if err != nil {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil", err)
	}
	if handle.Generation.Instructions == nil {
		t.Fatal("handle.Generation.Instructions = nil, want per-call instruction variants")
	}
	if got := handle.Generation.Instructions.AsModality("audio").String(); got != "speak plainly" {
		t.Fatalf("handle.Generation.Instructions audio = %q, want speak plainly", got)
	}
	if got := handle.Generation.Instructions.AsModality("text").String(); got != "write tersely" {
		t.Fatalf("handle.Generation.Instructions text = %q, want write tersely", got)
	}
}

func TestAgentSessionGenerateReplyOptionsPreserveToolChoice(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserInput:     "hello",
		ToolChoice:    "none",
		InputModality: "text",
	})

	if err != nil {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil", err)
	}
	if handle.Generation.ToolChoice != "none" {
		t.Fatalf("handle.Generation.ToolChoice = %#v, want none", handle.Generation.ToolChoice)
	}
}

func TestAgentSessionUpdateOptionsToolChoiceDefaultsFutureReplies(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	toolChoice := llm.ToolChoice("auto")
	if err := session.UpdateOptions(AgentSessionUpdateOptions{ToolChoice: &toolChoice}); err != nil {
		t.Fatalf("UpdateOptions error = %v, want nil", err)
	}

	handle, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserInput:     "hello",
		InputModality: "text",
	})

	if err != nil {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil", err)
	}
	if handle.Generation.ToolChoice != "auto" {
		t.Fatalf("handle.Generation.ToolChoice = %#v, want auto", handle.Generation.ToolChoice)
	}
}

func TestAgentSessionGenerateReplyFromFunctionToolDefaultsToolChoiceNone(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	ctx := WithRunContext(context.Background(), NewRunContext(session, nil, &llm.FunctionCall{
		CallID: "call_lookup",
		Name:   "lookup",
	}))

	handle, err := session.GenerateReplyWithOptions(ctx, GenerateReplyOptions{
		UserInput:     "continue",
		InputModality: "text",
	})

	if err != nil {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil", err)
	}
	if handle.Generation.ToolChoice != "none" {
		t.Fatalf("handle.Generation.ToolChoice = %#v, want none inside function tool context", handle.Generation.ToolChoice)
	}
}

func TestAgentSessionGenerateReplyOptionsPreserveTools(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup"}}
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserInput:     "hello",
		Tools:         []string{"lookup"},
		InputModality: "text",
	})

	if err != nil {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil", err)
	}
	if !stringSlicesEqual(handle.Generation.Tools, []string{"lookup"}) {
		t.Fatalf("handle.Generation.Tools = %q, want [lookup]", handle.Generation.Tools)
	}
}

func TestAgentSessionGenerateReplyOptionsAcceptAgentTools(t *testing.T) {
	agent := NewAgent("test")
	agent.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup"}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserInput:     "hello",
		Tools:         []string{"lookup"},
		InputModality: "text",
	})

	if err != nil {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil for agent tool selector", err)
	}
	if !stringSlicesEqual(handle.Generation.Tools, []string{"lookup"}) {
		t.Fatalf("handle.Generation.Tools = %q, want [lookup]", handle.Generation.Tools)
	}
}

func TestAgentSessionGenerateReplyOptionsAcceptMCPServerTools(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.SetMCPServers([]llm.MCPServer{
		&fakeSessionMCPServer{tools: []llm.Tool{&fakeGenerationTool{name: "lookup"}}},
	})
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserInput:     "hello",
		Tools:         []string{"lookup"},
		InputModality: "text",
	})

	if err != nil {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil for MCP server tool selector", err)
	}
	if !stringSlicesEqual(handle.Generation.Tools, []string{"lookup"}) {
		t.Fatalf("handle.Generation.Tools = %q, want [lookup]", handle.Generation.Tools)
	}
}

func TestAgentSessionGenerateReplyOptionsPreserveChatContext(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	replyCtx := llm.NewChatContext()
	replyCtx.Append(&llm.ChatMessage{
		ID:      "custom_user",
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "custom context"}},
	})

	handle, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserInput:     "hello",
		ChatCtx:       replyCtx,
		InputModality: "text",
	})

	if err != nil {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil", err)
	}
	if handle.Generation.ChatCtx == nil {
		t.Fatal("handle.Generation.ChatCtx = nil, want per-call chat context")
	}
	if handle.Generation.ChatCtx == replyCtx {
		t.Fatal("handle.Generation.ChatCtx aliases input context, want copy")
	}
	if len(handle.Generation.ChatCtx.Items) != 1 || handle.Generation.ChatCtx.Items[0].GetID() != "custom_user" {
		t.Fatalf("handle.Generation.ChatCtx.Items = %#v, want copied custom context", handle.Generation.ChatCtx.Items)
	}
}

func TestAgentSessionGenerateReplyOptionsRejectUnknownTools(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup"}}
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserInput:     "hello",
		Tools:         []string{"missing"},
		InputModality: "text",
	})

	if handle != nil {
		t.Fatalf("GenerateReplyWithOptions handle = %#v, want nil", handle)
	}
	if err == nil {
		t.Fatal("GenerateReplyWithOptions error = nil, want unknown tool error")
	}
	if got, want := err.Error(), "tool 'missing' not found in agent's registered tools. Available tools: ['lookup']"; got != want {
		t.Fatalf("GenerateReplyWithOptions error text = %q, want %q", got, want)
	}
}

func TestAgentSessionGenerateReplyOptionsRejectsNilRegisteredTool(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Tools = []llm.Tool{nil}
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserInput:     "hello",
		Tools:         []string{"lookup"},
		InputModality: "text",
	})

	if handle != nil {
		t.Fatalf("GenerateReplyWithOptions handle = %#v, want nil", handle)
	}
	if err == nil || !strings.Contains(err.Error(), "nil tool") {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil tool error", err)
	}
}

func TestAgentSessionGenerateReplyOptionsRejectsTypedNilRegisteredTool(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	var nilTool *fakeGenerationTool
	session.Tools = []llm.Tool{nilTool}
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserInput:     "hello",
		Tools:         []string{"lookup"},
		InputModality: "text",
	})

	if handle != nil {
		t.Fatalf("GenerateReplyWithOptions handle = %#v, want nil", handle)
	}
	if err == nil || !strings.Contains(err.Error(), "nil tool") {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil tool error", err)
	}
}

func TestAgentSessionGenerateReplyOptionsRejectsNilAgentTool(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Tools = []llm.Tool{&fakeGenerationTool{name: "session_lookup"}}
	agent.Tools = []llm.Tool{nil}
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserInput:     "hello",
		Tools:         []string{"session_lookup"},
		InputModality: "text",
	})

	if handle != nil {
		t.Fatalf("GenerateReplyWithOptions handle = %#v, want nil", handle)
	}
	if err == nil || !strings.Contains(err.Error(), "nil tool") {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil tool error", err)
	}
}

func TestAgentSessionGenerateReplyOptionsRejectsTypedNilAgentTool(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Tools = []llm.Tool{&fakeGenerationTool{name: "session_lookup"}}
	var nilTool *fakeGenerationTool
	agent.Tools = []llm.Tool{nilTool}
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserInput:     "hello",
		Tools:         []string{"session_lookup"},
		InputModality: "text",
	})

	if handle != nil {
		t.Fatalf("GenerateReplyWithOptions handle = %#v, want nil", handle)
	}
	if err == nil || !strings.Contains(err.Error(), "nil tool") {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil tool error", err)
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

func TestAgentSessionFunctionToolsExecutedRecordsActiveRunItems(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	result := NewRunResult(session.ChatCtx)
	session.runState = result
	call := &llm.FunctionCall{
		ID:        "call_item_1",
		CallID:    "call_lookup",
		Name:      "lookup",
		Arguments: `{}`,
		CreatedAt: time.Now(),
	}
	output := &llm.FunctionCallOutput{
		ID:        "output_item_1",
		CallID:    "call_lookup",
		Name:      "lookup",
		Output:    "tool result",
		CreatedAt: call.CreatedAt.Add(time.Millisecond),
	}
	ev, err := NewFunctionToolsExecutedEvent([]*llm.FunctionCall{call}, []*llm.FunctionCallOutput{output})
	if err != nil {
		t.Fatalf("NewFunctionToolsExecutedEvent error = %v, want nil", err)
	}

	session.EmitFunctionToolsExecuted(*ev)

	events := result.Events()
	if len(events) != 2 {
		t.Fatalf("RunResult events length = %d, want function call and output", len(events))
	}
	if callEvent, ok := events[0].(*FunctionCallEvent); !ok || callEvent.Item != call {
		t.Fatalf("events[0] = %#v, want recorded function call", events[0])
	}
	if outputEvent, ok := events[1].(*FunctionCallOutputEvent); !ok || outputEvent.Item != output {
		t.Fatalf("events[1] = %#v, want recorded function call output", events[1])
	}
}

func TestAgentSessionRunWithOptionsPreservesUserInputAndOutputType(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)

	result, err := session.RunWithOptions(context.Background(), RunOptions{
		UserInput:     "collect name",
		InputModality: "text",
		OutputType:    reflect.TypeOf(""),
	})

	if err != nil {
		t.Fatalf("RunWithOptions error = %v, want nil", err)
	}
	if got := result.UserInput(); got != "collect name" {
		t.Fatalf("UserInput = %q, want collect name", got)
	}
	result.SetFinalOutput(42)
	result.MarkDone()
	output, err := result.FinalOutput()
	if !errors.Is(err, ErrRunResultFinalOutputType) {
		t.Fatalf("FinalOutput error = %v, want ErrRunResultFinalOutputType", err)
	}
	if output != nil {
		t.Fatalf("FinalOutput output = %#v, want nil on type mismatch", output)
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
	if got, want := err.Error(), "nested runs are not supported"; got != want {
		t.Fatalf("second Run error text = %q, want %q", got, want)
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
	if got, want := err.Error(), "AgentSession isn't running"; got != want {
		t.Fatalf("GenerateReply error text = %q, want %q", got, want)
	}
}

func TestAgentSessionSayAndGenerateReplyReportClosingState(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.started = true
	session.closing = true

	sayHandle, sayErr := session.Say(context.Background(), "closing")
	if sayHandle != nil {
		t.Fatalf("Say handle = %#v, want nil while closing", sayHandle)
	}
	if sayErr == nil {
		t.Fatal("Say error = nil, want reference closing error")
	}
	if got, want := sayErr.Error(), "AgentSession is closing, cannot use say()"; got != want {
		t.Fatalf("Say error text = %q, want %q", got, want)
	}

	replyHandle, replyErr := session.GenerateReply(context.Background(), "closing")
	if replyHandle != nil {
		t.Fatalf("GenerateReply handle = %#v, want nil while closing", replyHandle)
	}
	if replyErr == nil {
		t.Fatal("GenerateReply error = nil, want reference closing error")
	}
	if got, want := replyErr.Error(), "AgentSession is closing, cannot use generate_reply()"; got != want {
		t.Fatalf("GenerateReply error text = %q, want %q", got, want)
	}
}

func TestAgentSessionCurrentAgentRequiresConfiguredAgent(t *testing.T) {
	session := &AgentSession{}

	current, err := session.CurrentAgent()

	if current != nil {
		t.Fatalf("CurrentAgent = %#v, want nil when session has no agent", current)
	}
	if !errors.Is(err, ErrAgentSessionNotRunning) {
		t.Fatalf("CurrentAgent error = %v, want ErrAgentSessionNotRunning", err)
	}
	if got, want := err.Error(), "VoiceAgent isn't running"; got != want {
		t.Fatalf("CurrentAgent error text = %q, want %q", got, want)
	}
}

func TestAgentSessionCurrentAgentReturnsConfiguredAgentBeforeStart(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	current, err := session.CurrentAgent()

	if err != nil {
		t.Fatalf("CurrentAgent error = %v, want nil before start when agent is configured", err)
	}
	if current != agent {
		t.Fatalf("CurrentAgent = %#v, want configured agent %#v", current, agent)
	}
}

func TestAgentSessionCurrentAgentReturnsRunningAgent(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.started = true

	current, err := session.CurrentAgent()

	if err != nil {
		t.Fatalf("CurrentAgent error = %v, want nil", err)
	}
	if current != agent {
		t.Fatalf("CurrentAgent = %#v, want session agent %#v", current, agent)
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

func TestAgentSessionCloseSoonInterruptsActiveSpeechBeforeClosing(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	session.started = true
	current := NewSpeechHandle(true, DefaultInputDetails())
	session.activity.currentSpeech = current
	closeEvents := session.CloseEvents()

	done := make(chan struct{}, 1)
	go func() {
		session.CloseSoon(CloseReasonParticipantDisconnected)
		done <- struct{}{}
	}()

	waitForInterrupted(t, current)

	select {
	case <-closeEvents:
		t.Fatal("CloseSoon emitted close event before interrupted speech completed")
	case <-done:
		t.Fatal("CloseSoon returned before interrupted speech completed")
	default:
	}

	current.MarkDone()

	select {
	case <-done:
	case <-testTimeout():
		t.Fatal("CloseSoon did not return after interrupted speech completed")
	}

	select {
	case ev := <-closeEvents:
		if ev.Reason != CloseReasonParticipantDisconnected {
			t.Fatalf("CloseEvent.Reason = %q, want participant_disconnected", ev.Reason)
		}
	default:
		t.Fatal("CloseSoon did not emit close event")
	}
}

func TestAgentSessionCloseSoonClearsAECWarmupBeforeCleanup(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	session.started = true
	session.aecWarmupTimer = time.NewTimer(time.Hour)
	defer session.aecWarmupTimer.Stop()
	current := NewSpeechHandle(true, DefaultInputDetails())
	session.activity.currentSpeech = current

	done := make(chan struct{}, 1)
	go func() {
		session.CloseSoon(CloseReasonParticipantDisconnected)
		done <- struct{}{}
	}()

	waitForInterrupted(t, current)
	if session.shouldSilenceInputAudio() {
		t.Fatal("shouldSilenceInputAudio() = true during close, want AEC warmup cleared before cleanup")
	}

	current.MarkDone()

	select {
	case <-done:
	case <-testTimeout():
		t.Fatal("CloseSoon did not return after interrupted speech completed")
	}
}

func TestAgentSessionCloseSoonCancelsUserAwayTimerBeforeCleanup(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	session.started = true
	session.userAwayTimer = time.NewTimer(time.Hour)
	defer session.userAwayTimer.Stop()
	current := NewSpeechHandle(true, DefaultInputDetails())
	session.activity.currentSpeech = current

	done := make(chan struct{}, 1)
	go func() {
		session.CloseSoon(CloseReasonParticipantDisconnected)
		done <- struct{}{}
	}()

	waitForInterrupted(t, current)
	session.mu.Lock()
	timer := session.userAwayTimer
	session.mu.Unlock()
	if timer != nil {
		t.Fatal("userAwayTimer still active during close, want canceled before cleanup")
	}

	current.MarkDone()

	select {
	case <-done:
	case <-testTimeout():
		t.Fatal("CloseSoon did not return after interrupted speech completed")
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

func TestAgentSessionShutdownClosesMCPServers(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	session.started = true
	mcpServer := &fakeSessionMCPServer{}
	session.SetMCPServers([]llm.MCPServer{mcpServer})

	session.Shutdown(false)

	if mcpServer.closed != 1 {
		t.Fatalf("MCP server closed = %d, want 1", mcpServer.closed)
	}
}

func TestAgentSessionShutdownDrainsByDefaultBeforeClosing(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	session.started = true
	current := NewSpeechHandle(true, DefaultInputDetails())
	session.activity.currentSpeech = current

	done := make(chan struct{}, 1)
	go func() {
		session.Shutdown()
		done <- struct{}{}
	}()

	closeEvents := session.CloseEvents()
	waitForDraining(t, session.activity)
	select {
	case <-closeEvents:
		t.Fatal("Shutdown emitted close event before active speech drained")
	case <-done:
		t.Fatal("Shutdown returned before active speech drained")
	case <-time.After(20 * time.Millisecond):
	}

	current.MarkDone()

	select {
	case <-done:
	case <-testTimeout():
		t.Fatal("Shutdown did not return after active speech completed")
	}
	select {
	case ev := <-closeEvents:
		if ev.Reason != CloseReasonUserInitiated {
			t.Fatalf("CloseEvent.Reason = %q, want user_initiated", ev.Reason)
		}
	default:
		t.Fatal("Shutdown did not emit close event after draining")
	}
	if session.activity != nil {
		t.Fatalf("session.activity = %#v, want nil after drained shutdown", session.activity)
	}
}

func TestAgentSessionShutdownCanSkipDrain(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	session.started = true
	current := NewSpeechHandle(true, DefaultInputDetails())
	session.activity.currentSpeech = current

	closeEvents := session.CloseEvents()
	done := make(chan struct{}, 1)
	go func() {
		session.Shutdown(false)
		done <- struct{}{}
	}()

	waitForInterrupted(t, current)

	select {
	case <-closeEvents:
		t.Fatal("Shutdown(false) emitted close event before interrupted speech completed")
	case <-done:
		t.Fatal("Shutdown(false) returned before interrupted speech completed")
	default:
	}

	current.MarkDone()

	select {
	case <-done:
	case <-testTimeout():
		t.Fatal("Shutdown(false) did not return after interrupted speech completed")
	}

	select {
	case ev := <-closeEvents:
		if ev.Reason != CloseReasonUserInitiated {
			t.Fatalf("CloseEvent.Reason = %q, want user_initiated", ev.Reason)
		}
	default:
		t.Fatal("Shutdown(false) did not emit close event")
	}
	if session.activity != nil {
		t.Fatalf("session.activity = %#v, want nil after non-draining shutdown", session.activity)
	}
}

func TestAgentSessionShutdownSkipDrainInterruptsRealtimeOnce(t *testing.T) {
	agent := NewAgent("test")
	assistant := &fakeInterruptingSessionAssistant{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Assistant = assistant
	session.activity = NewAgentActivity(agent, session)
	session.started = true

	session.Shutdown(false)

	if assistant.interrupts != 1 {
		t.Fatalf("realtime interrupts = %d, want 1", assistant.interrupts)
	}
}

func TestAgentSessionShutdownDoesNotCloseUnstartedSession(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	session.Shutdown()

	select {
	case ev := <-session.CloseEvents():
		t.Fatalf("unexpected close event for unstarted session: %#v", ev)
	default:
	}
}

func TestAgentSessionStopResetsSessionStates(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	session.started = true
	session.UpdateUserState(UserStateSpeaking)
	session.UpdateAgentState(AgentStateThinking)

	if err := session.Stop(context.Background()); err != nil {
		t.Fatalf("Stop error = %v, want nil", err)
	}

	if got := session.UserState(); got != UserStateListening {
		t.Fatalf("UserState() after Stop = %q, want %q", got, UserStateListening)
	}
	if got := session.AgentState(); got != AgentStateInitializing {
		t.Fatalf("AgentState() after Stop = %q, want %q", got, AgentStateInitializing)
	}
}

func TestAgentSessionStopResetsProviderErrorCounts(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	session.started = true
	session.llmErrorCount = 2
	session.ttsErrorCount = 2

	if err := session.Stop(context.Background()); err != nil {
		t.Fatalf("Stop error = %v, want nil", err)
	}

	if session.llmErrorCount != 0 {
		t.Fatalf("llmErrorCount after Stop = %d, want 0", session.llmErrorCount)
	}
	if session.ttsErrorCount != 0 {
		t.Fatalf("ttsErrorCount after Stop = %d, want 0", session.ttsErrorCount)
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

func TestAgentSessionStopClosesCloseableAssistant(t *testing.T) {
	agent := NewAgent("test")
	assistant := &fakeCloseableSessionAssistant{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Assistant = assistant
	session.activity = NewAgentActivity(agent, session)
	session.started = true

	if err := session.Stop(context.Background()); err != nil {
		t.Fatalf("Stop error = %v, want nil", err)
	}
	if assistant.closed != 1 {
		t.Fatalf("assistant closed = %d, want 1", assistant.closed)
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

func TestAgentSessionWaitForInactiveRetargetsDuringAgentHandoff(t *testing.T) {
	initial := &trackingAgent{Agent: NewAgent("initial")}
	next := &onEnterSayAgent{Agent: NewAgent("next")}
	session := NewAgentSession(initial, nil, AgentSessionOptions{})
	next.session = session
	session.activity = NewAgentActivity(initial, session)
	session.Assistant = &fakeSessionAssistant{}
	session.started = true
	oldSpeech := NewSpeechHandle(true, DefaultInputDetails())
	session.activity.currentSpeech = oldSpeech

	done := make(chan error, 1)
	go func() {
		done <- session.WaitForInactive(context.Background())
	}()

	select {
	case err := <-done:
		t.Fatalf("WaitForInactive returned before previous activity speech completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	session.UpdateAgent(next)
	oldSpeech.MarkDone()

	select {
	case err := <-done:
		t.Fatalf("WaitForInactive returned while replacement activity speech was still active: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	current := session.CurrentSpeech()
	if current == nil {
		t.Fatal("replacement activity did not schedule speech")
	}
	current.MarkDone()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitForInactive error = %v, want nil", err)
		}
	case <-testTimeout():
		t.Fatal("WaitForInactive did not return after replacement speech completed")
	}
}

func TestAgentSessionWaitForInactiveWaitsForClaimedUserTurn(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	release := make(chan struct{})
	claimed := make(chan struct{})
	claimDone := make(chan error, 1)

	go func() {
		claimDone <- session.ClaimUserTurn(context.Background(), func(context.Context) error {
			close(claimed)
			<-release
			return nil
		})
	}()
	<-claimed

	done := make(chan error, 1)
	go func() {
		done <- session.WaitForInactive(context.Background())
	}()

	select {
	case err := <-done:
		t.Fatalf("WaitForInactive returned before claimed user turn released: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-claimDone:
		if err != nil {
			t.Fatalf("ClaimUserTurn error = %v", err)
		}
	case <-testTimeout():
		t.Fatal("ClaimUserTurn did not release")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitForInactive error = %v, want nil", err)
		}
	case <-testTimeout():
		t.Fatal("WaitForInactive did not return after claimed user turn released")
	}
}

func TestAgentSessionWaitForInactiveWaitsForUserSpeech(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	activity.speaking = true

	done := make(chan error, 1)
	go func() {
		done <- session.WaitForInactive(context.Background())
	}()

	select {
	case err := <-done:
		t.Fatalf("WaitForInactive returned while user speech was active: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	activity.OnEndOfSpeech(nil)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitForInactive error = %v, want nil", err)
		}
	case <-testTimeout():
		t.Fatal("WaitForInactive did not return after user speech ended")
	}
}

func TestAgentSessionWaitForInactiveAndHoldBlocksOtherWaiters(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	held := make(chan struct{})
	release := make(chan struct{})
	holdDone := make(chan error, 1)

	go func() {
		holdDone <- session.WaitForInactiveAndHold(context.Background(), func(ctx context.Context) error {
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
		t.Fatalf("WaitForInactive returned while another caller held idle: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-holdDone:
		if err != nil {
			t.Fatalf("WaitForInactiveAndHold error = %v", err)
		}
	case <-testTimeout():
		t.Fatal("WaitForInactiveAndHold did not release")
	}
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("WaitForInactive error = %v, want nil after hold release", err)
		}
	case <-testTimeout():
		t.Fatal("WaitForInactive did not return after hold release")
	}
}

func TestAgentSessionDrainRequiresRunningActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	err := session.Drain(context.Background())

	if !errors.Is(err, ErrAgentSessionNotRunning) {
		t.Fatalf("Drain error = %v, want ErrAgentSessionNotRunning", err)
	}
	if got, want := err.Error(), "AgentSession isn't running"; got != want {
		t.Fatalf("Drain error text = %q, want %q", got, want)
	}
}

func TestAgentSessionDrainDelegatesToActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	session.activity.currentSpeech = current

	done := make(chan error, 1)
	go func() {
		done <- session.Drain(context.Background())
	}()

	waitForDraining(t, session.activity)
	current.MarkDone()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Drain error = %v, want nil", err)
		}
	case <-testTimeout():
		t.Fatal("Drain did not return after current speech completed")
	}
	if !session.activity.schedulingPaused {
		t.Fatal("schedulingPaused = false after Drain, want true")
	}
}

func TestAgentSessionInterruptRequiresRunningActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	err := session.Interrupt(false)

	if !errors.Is(err, ErrAgentSessionNotRunning) {
		t.Fatalf("Interrupt error = %v, want ErrAgentSessionNotRunning", err)
	}
	if got, want := err.Error(), "AgentSession isn't running"; got != want {
		t.Fatalf("Interrupt error text = %q, want %q", got, want)
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

func TestAgentSessionClearUserTurnRequiresRunningActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	err := session.ClearUserTurn()

	if !errors.Is(err, ErrAgentSessionNotRunning) {
		t.Fatalf("ClearUserTurn error = %v, want ErrAgentSessionNotRunning", err)
	}
	if got, want := err.Error(), "AgentSession isn't running"; got != want {
		t.Fatalf("ClearUserTurn error text = %q, want %q", got, want)
	}
}

func TestAgentSessionCommitUserTurnRequiresRunningActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	_, err := session.CommitUserTurn(context.Background(), CommitUserTurnOptions{})

	if !errors.Is(err, ErrAgentSessionNotRunning) {
		t.Fatalf("CommitUserTurn error = %v, want ErrAgentSessionNotRunning", err)
	}
	if got, want := err.Error(), "AgentSession isn't running"; got != want {
		t.Fatalf("CommitUserTurn error text = %q, want %q", got, want)
	}
}

func TestAgentSessionCommitUserTurnDelegatesToActivity(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	session.activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "manual session turn", Confidence: 0.9}},
	})

	transcript, err := session.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "manual session turn" {
		t.Fatalf("CommitUserTurn transcript = %q, want manual session turn", transcript)
	}
	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "manual session turn" {
			t.Fatalf("turn message text = %q, want manual session turn", msg.TextContent())
		}
		if msg.TranscriptConfidence == nil || *msg.TranscriptConfidence != 0.9 {
			t.Fatalf("turn confidence = %v, want 0.9", msg.TranscriptConfidence)
		}
	case <-testTimeout():
		t.Fatal("OnUserTurnCompleted was not called")
	}
}

func TestAgentSessionStartForwardsTTSMetricsThroughActivity(t *testing.T) {
	ttsSource := &fakePipelineTTS{}
	agent := NewAgent("test")
	agent.TTS = ttsSource
	agent.LLM = &fakeGenerationLLM{}
	agent.STT = &fakePipelineSTT{}
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Assistant = &fakeSessionAssistant{}

	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("Start error = %v, want nil", err)
	}

	metrics := &telemetry.TTSMetrics{RequestID: "tts_req", InputTokens: 2}
	ttsSource.EmitMetricsCollected(metrics)

	select {
	case ev := <-session.MetricsCollectedEvents():
		if ev.Metrics != metrics {
			t.Fatalf("MetricsCollectedEvent metrics = %#v, want original TTS metrics", ev.Metrics)
		}
	case <-time.After(time.Second):
		t.Fatal("MetricsCollectedEvents did not receive TTS metrics")
	}
}

func TestAgentSessionStartForwardsLLMMetricsThroughActivity(t *testing.T) {
	llmSource := &fakeGenerationLLM{}
	agent := NewAgent("test")
	agent.LLM = llmSource
	agent.STT = &fakePipelineSTT{}
	agent.VAD = &fakePipelineVAD{}
	agent.TTS = &fakePipelineTTS{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Assistant = &fakeSessionAssistant{}

	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("Start error = %v, want nil", err)
	}

	metrics := &telemetry.LLMMetrics{RequestID: "llm_req", PromptTokens: 5}
	llmSource.EmitMetricsCollected(metrics)

	select {
	case ev := <-session.MetricsCollectedEvents():
		if ev.Metrics != metrics {
			t.Fatalf("MetricsCollectedEvent metrics = %#v, want original LLM metrics", ev.Metrics)
		}
	case <-time.After(time.Second):
		t.Fatal("MetricsCollectedEvents did not receive LLM metrics")
	}
}

func TestAgentSessionStartForwardsVADMetricsThroughActivity(t *testing.T) {
	vadSource := &fakePipelineVAD{}
	agent := NewAgent("test")
	agent.VAD = vadSource
	agent.LLM = &fakeGenerationLLM{}
	agent.STT = &fakePipelineSTT{}
	agent.TTS = &fakePipelineTTS{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Assistant = &fakeSessionAssistant{}

	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("Start error = %v, want nil", err)
	}

	metrics := &telemetry.VADMetrics{Label: "vad"}
	vadSource.EmitMetricsCollected(metrics)

	select {
	case ev := <-session.MetricsCollectedEvents():
		if ev.Metrics != metrics {
			t.Fatalf("MetricsCollectedEvent metrics = %#v, want original VAD metrics", ev.Metrics)
		}
	case <-time.After(time.Second):
		t.Fatal("MetricsCollectedEvents did not receive VAD metrics")
	}
}

func TestAgentSessionStopUnsubscribesVADMetricsFromActivity(t *testing.T) {
	vadSource := &fakePipelineVAD{}
	agent := NewAgent("test")
	agent.VAD = vadSource
	agent.LLM = &fakeGenerationLLM{}
	agent.STT = &fakePipelineSTT{}
	agent.TTS = &fakePipelineTTS{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Assistant = &fakeSessionAssistant{}

	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("Start error = %v, want nil", err)
	}
	if err := session.Stop(context.Background()); err != nil {
		t.Fatalf("Stop error = %v, want nil", err)
	}

	vadSource.EmitMetricsCollected(&telemetry.VADMetrics{Label: "late-vad"})

	select {
	case ev := <-session.MetricsCollectedEvents():
		t.Fatalf("MetricsCollectedEvents received VAD metrics after Stop: %#v", ev.Metrics)
	default:
	}
}

func TestAgentSessionStartForwardsSTTMetricsThroughActivity(t *testing.T) {
	sttSource := &fakePipelineSTT{}
	agent := NewAgent("test")
	agent.STT = sttSource
	agent.LLM = &fakeGenerationLLM{}
	agent.VAD = &fakePipelineVAD{}
	agent.TTS = &fakePipelineTTS{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Assistant = &fakeSessionAssistant{}

	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("Start error = %v, want nil", err)
	}

	metrics := &telemetry.STTMetrics{RequestID: "stt_req", InputTokens: 3}
	sttSource.EmitMetricsCollected(metrics)

	select {
	case ev := <-session.MetricsCollectedEvents():
		if ev.Metrics != metrics {
			t.Fatalf("MetricsCollectedEvent metrics = %#v, want original STT metrics", ev.Metrics)
		}
	case <-time.After(time.Second):
		t.Fatal("MetricsCollectedEvents did not receive STT metrics")
	}
}

func TestAgentSessionStartForwardsTTSErrorsThroughActivity(t *testing.T) {
	ttsSource := &fakePipelineTTS{}
	agent := NewAgent("test")
	agent.TTS = ttsSource
	agent.LLM = &fakeGenerationLLM{}
	agent.STT = &fakePipelineSTT{}
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Assistant = &fakeSessionAssistant{}

	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("Start error = %v, want nil", err)
	}

	cause := errors.New("tts failed")
	ttsSource.EmitError(tts.TTSError{Label: "fake", Err: cause, Recoverable: true})

	select {
	case ev := <-session.ErrorEvents():
		var ttsErr tts.TTSError
		if !errors.As(ev.Error, &ttsErr) {
			t.Fatalf("Error = %T, want tts.TTSError", ev.Error)
		}
		if !errors.Is(ev.Error, cause) {
			t.Fatalf("Error = %v, want cause %v", ev.Error, cause)
		}
		if ev.Source != ttsSource {
			t.Fatalf("Source = %#v, want TTS source", ev.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive TTS error")
	}
}

func TestAgentSessionStartForwardsLLMErrorsThroughActivity(t *testing.T) {
	llmSource := &fakeGenerationLLM{}
	agent := NewAgent("test")
	agent.LLM = llmSource
	agent.STT = &fakePipelineSTT{}
	agent.VAD = &fakePipelineVAD{}
	agent.TTS = &fakePipelineTTS{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Assistant = &fakeSessionAssistant{}

	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("Start error = %v, want nil", err)
	}

	cause := errors.New("llm failed")
	llmSource.EmitError(llm.NewLLMError("fake", cause, true))

	select {
	case ev := <-session.ErrorEvents():
		var llmErr *llm.LLMError
		if !errors.As(ev.Error, &llmErr) {
			t.Fatalf("Error = %T, want *llm.LLMError", ev.Error)
		}
		if !errors.Is(ev.Error, cause) {
			t.Fatalf("Error = %v, want cause %v", ev.Error, cause)
		}
		if ev.Source != llmSource {
			t.Fatalf("Source = %#v, want LLM source", ev.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive LLM error")
	}
}

func TestAgentSessionStartForwardsSTTErrorsThroughActivity(t *testing.T) {
	sttSource := &fakePipelineSTT{}
	agent := NewAgent("test")
	agent.STT = sttSource
	agent.LLM = &fakeGenerationLLM{}
	agent.VAD = &fakePipelineVAD{}
	agent.TTS = &fakePipelineTTS{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Assistant = &fakeSessionAssistant{}

	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("Start error = %v, want nil", err)
	}

	cause := errors.New("stt failed")
	sttSource.EmitError(stt.NewSTTError("fake", cause, true))

	select {
	case ev := <-session.ErrorEvents():
		var sttErr *stt.STTError
		if !errors.As(ev.Error, &sttErr) {
			t.Fatalf("Error = %T, want *stt.STTError", ev.Error)
		}
		if !errors.Is(ev.Error, cause) {
			t.Fatalf("Error = %v, want cause %v", ev.Error, cause)
		}
		if ev.Source != sttSource {
			t.Fatalf("Source = %#v, want STT source", ev.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive STT error")
	}
}

func TestAgentSessionClosesAfterUnrecoverableTTSErrorThreshold(t *testing.T) {
	ttsSource := &fakePipelineTTS{}
	agent := NewAgent("test")
	agent.TTS = ttsSource
	agent.LLM = &fakeGenerationLLM{}
	agent.STT = &fakePipelineSTT{}
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{MaxUnrecoverableErrors: 1})
	session.Assistant = &fakeSessionAssistant{}

	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("Start error = %v, want nil", err)
	}

	cause := errors.New("tts failed")
	closeEvents := session.CloseEvents()
	ttsSource.EmitError(tts.TTSError{Label: "fake", Err: cause, Recoverable: false})

	select {
	case ev := <-session.ErrorEvents():
		if !errors.Is(ev.Error, cause) {
			t.Fatalf("Error = %v, want cause %v", ev.Error, cause)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive TTS error")
	}

	select {
	case ev := <-closeEvents:
		t.Fatalf("CloseEvents received early close with reason %q", ev.Reason)
	default:
	}

	ttsSource.EmitError(tts.TTSError{Label: "fake", Err: cause, Recoverable: false})

	select {
	case ev := <-closeEvents:
		if ev.Reason != CloseReasonError {
			t.Fatalf("CloseEvent.Reason = %q, want error", ev.Reason)
		}
		if !errors.Is(ev.Error, cause) {
			t.Fatalf("CloseEvent.Error = %v, want cause %v", ev.Error, cause)
		}
	case <-time.After(time.Second):
		t.Fatal("CloseEvents did not receive error close")
	}
}

func TestAgentSessionSpeakingResetsUnrecoverableProviderErrorCounts(t *testing.T) {
	agent := NewAgent("test")
	agent.TTS = &fakePipelineTTS{}
	agent.LLM = &fakeGenerationLLM{}
	agent.STT = &fakePipelineSTT{}
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{MaxUnrecoverableErrors: 1})
	session.Assistant = &fakeSessionAssistant{}

	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("Start error = %v, want nil", err)
	}

	cause := errors.New("llm failed")
	session.EmitError(*NewErrorEvent(&llm.LLMError{Err: cause, Recoverable: false}, agent.LLM))

	select {
	case <-session.ErrorEvents():
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive LLM error")
	}

	select {
	case ev := <-session.CloseEvents():
		t.Fatalf("CloseEvents received early close with reason %q", ev.Reason)
	default:
	}

	session.UpdateAgentState(AgentStateSpeaking)
	session.EmitError(*NewErrorEvent(&llm.LLMError{Err: cause, Recoverable: false}, agent.LLM))

	select {
	case ev := <-session.CloseEvents():
		t.Fatalf("CloseEvents received close after speaking reset with reason %q", ev.Reason)
	default:
	}
}

func TestAgentSessionPreservesExplicitZeroMaxUnrecoverableErrors(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		MaxUnrecoverableErrors:    0,
		MaxUnrecoverableErrorsSet: true,
	})

	if session.Options.MaxUnrecoverableErrors != 0 {
		t.Fatalf("MaxUnrecoverableErrors = %d, want explicit zero preserved", session.Options.MaxUnrecoverableErrors)
	}
}

func TestAgentSessionExplicitZeroMaxUnrecoverableErrorsClosesOnFirstError(t *testing.T) {
	agent := NewAgent("test")
	agent.TTS = &fakePipelineTTS{}
	agent.LLM = &fakeGenerationLLM{}
	agent.STT = &fakePipelineSTT{}
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		MaxUnrecoverableErrors:    0,
		MaxUnrecoverableErrorsSet: true,
	})
	session.Assistant = &fakeSessionAssistant{}

	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("Start error = %v, want nil", err)
	}

	cause := errors.New("tts failed")
	closeEvents := session.CloseEvents()
	session.EmitError(*NewErrorEvent(tts.TTSError{Label: "fake", Err: cause, Recoverable: false}, agent.TTS))

	select {
	case ev := <-closeEvents:
		if ev.Reason != CloseReasonError {
			t.Fatalf("CloseEvent.Reason = %q, want error", ev.Reason)
		}
		if !errors.Is(ev.Error, cause) {
			t.Fatalf("CloseEvent.Error = %v, want cause %v", ev.Error, cause)
		}
	case <-time.After(time.Second):
		t.Fatal("CloseEvents did not receive error close")
	}
}

func TestAgentSessionCloseSoonCommitsPendingUserTurn(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{SessionCloseTranscriptTimeout: 0.25})
	session.activity = NewAgentActivity(agent, session)
	session.started = true
	session.activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "closing turn", Confidence: 0.9}},
	})

	session.CloseSoon(CloseReasonUserInitiated)

	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "closing turn" {
			t.Fatalf("turn message text = %q, want closing turn", msg.TextContent())
		}
	case <-testTimeout():
		t.Fatal("OnUserTurnCompleted was not called before close")
	}
}

func TestAgentSessionCloseSoonDoesNotCommitRealtimeAudioOrReply(t *testing.T) {
	agent := NewAgent("test")
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{SessionCloseTranscriptTimeout: 0.25})
	assistant := &recordingRealtimeCommitAssistant{}
	session.Assistant = assistant
	session.activity = NewAgentActivity(agent, session)
	session.started = true

	events := session.SpeechCreatedEvents()
	session.CloseSoon(CloseReasonUserInitiated)

	if assistant.commits != 0 {
		t.Fatalf("CommitAudio calls = %d, want 0 on close", assistant.commits)
	}
	select {
	case ev := <-events:
		t.Fatalf("unexpected SpeechCreated event on close: %#v", ev)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestAgentSessionStopCancelsActiveAgentTask(t *testing.T) {
	task := NewAgentTask[string]("collect data")
	task.ID = "collect_data"
	session := NewAgentSession(task, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(task, session)
	session.started = true

	if err := session.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v, want nil", err)
	}

	type waitResult struct {
		value any
		err   error
	}
	done := make(chan waitResult, 1)
	go func() {
		got, err := task.WaitAny(context.Background())
		done <- waitResult{value: got, err: err}
	}()

	var result waitResult
	select {
	case result = <-done:
	case <-testTimeout():
		t.Fatal("WaitAny() did not return after Stop cancelled the active AgentTask")
	}
	if result.value != nil {
		t.Fatalf("WaitAny() result = %#v, want nil", result.value)
	}
	var toolErr llm.ToolError
	if !errors.As(result.err, &toolErr) {
		t.Fatalf("WaitAny() error = %T %v, want llm.ToolError", result.err, result.err)
	}
	if toolErr.Message != "AgentTask collect_data is cancelled" {
		t.Fatalf("ToolError message = %q, want cancellation message", toolErr.Message)
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

func TestAgentSessionUpdateAgentReplacesSessionTools(t *testing.T) {
	initial := &trackingAgent{Agent: NewAgent("initial")}
	initialTool := &fakeGenerationTool{name: "initial_lookup"}
	initial.Tools = []llm.Tool{initialTool}
	next := &trackingAgent{Agent: NewAgent("next")}
	nextTool := &fakeGenerationTool{name: "next_lookup"}
	next.Tools = []llm.Tool{nextTool}
	session := NewAgentSession(initial, nil, AgentSessionOptions{})
	if len(session.Tools) != 1 || session.Tools[0] != initialTool {
		t.Fatalf("initial session.Tools = %#v, want initial agent tool", session.Tools)
	}

	session.UpdateAgent(next)

	if len(session.Tools) != 1 || session.Tools[0] != nextTool {
		t.Fatalf("session.Tools after UpdateAgent = %#v, want next agent tool", session.Tools)
	}
}

func TestAgentSessionUpdateAgentBeforeStartUsesNextRealtimeModel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	initial := &trackingAgent{Agent: NewAgent("initial")}
	next := &trackingAgent{Agent: NewAgent("next")}
	nextRealtime := &fakeRealtimeModel{session: &fakeRealtimeSession{}}
	next.RealtimeModel = nextRealtime
	session := NewAgentSession(initial, nil, AgentSessionOptions{})
	session.VAD = &fakePipelineVAD{}
	session.STT = &fakePipelineSTT{}
	session.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	session.TTS = &fakePipelineTTS{}

	session.UpdateAgent(next)
	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v, want nil", err)
	}
	defer session.Stop(context.Background())

	if session.RealtimeModel != nextRealtime {
		t.Fatalf("session.RealtimeModel = %#v, want next realtime model", session.RealtimeModel)
	}
	if _, ok := session.Assistant.(*MultimodalAgent); !ok {
		t.Fatalf("Assistant = %T, want *MultimodalAgent", session.Assistant)
	}
}

func TestAgentSessionStartRejectsRealtimeTurnDetectionWithDisabledInterruptions(t *testing.T) {
	agent := NewAgent("test")
	agent.RealtimeModel = &fakeRealtimeModel{
		session:      &fakeRealtimeSession{},
		capabilities: llm.RealtimeCapabilities{TurnDetection: true},
	}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		AllowInterruptions:    false,
		AllowInterruptionsSet: true,
	})

	err := session.Start(context.Background())
	if err == nil {
		t.Fatal("Start error = nil, want reference realtime turn detection interruption error")
	}
	want := "the RealtimeModel uses a server-side turn detection, allow_interruptions cannot be False, disable turn_detection in the RealtimeModel and use VAD on the AgentSession instead"
	if got := err.Error(); got != want {
		t.Fatalf("Start error = %q, want %q", got, want)
	}
	if session.started {
		t.Fatal("session.started = true, want start rejected before activity starts")
	}
}

func TestAgentSessionUpdateAgentWhileRunningRefreshesMultimodalRealtimeModel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	initial := &trackingAgent{Agent: NewAgent("initial")}
	initialRealtimeSession := &fakeRealtimeSession{}
	initialRealtime := &fakeRealtimeModel{session: initialRealtimeSession}
	initial.RealtimeModel = initialRealtime
	next := &trackingAgent{Agent: NewAgent("next")}
	nextRealtimeSession := &fakeRealtimeSession{}
	nextRealtime := &fakeRealtimeModel{session: nextRealtimeSession}
	next.RealtimeModel = nextRealtime
	session := NewAgentSession(initial, nil, AgentSessionOptions{})

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v, want nil", err)
	}
	defer session.Stop(context.Background())
	assistant, ok := session.Assistant.(*MultimodalAgent)
	if !ok {
		t.Fatalf("Assistant = %T, want *MultimodalAgent", session.Assistant)
	}
	if assistant.model != initialRealtime {
		t.Fatalf("assistant model before handoff = %#v, want initial realtime model", assistant.model)
	}

	session.UpdateAgent(next)

	if session.RealtimeModel != nextRealtime {
		t.Fatalf("session.RealtimeModel = %#v, want next realtime model", session.RealtimeModel)
	}
	if assistant.model != nextRealtime {
		t.Fatalf("assistant model after handoff = %#v, want next realtime model", assistant.model)
	}
	if assistant.rtSession != nextRealtimeSession {
		t.Fatalf("assistant realtime session after handoff = %#v, want next model session", assistant.rtSession)
	}
	if initialRealtimeSession.closed != 1 {
		t.Fatalf("initial realtime session closed = %d, want 1", initialRealtimeSession.closed)
	}
}

func TestAgentSessionUpdateAgentRejectsRealtimeTurnDetectionWithDisabledInterruptions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	initial := &trackingAgent{Agent: NewAgent("initial")}
	next := &trackingAgent{Agent: NewAgent("next")}
	next.RealtimeModel = &fakeRealtimeModel{
		session:      &fakeRealtimeSession{},
		capabilities: llm.RealtimeCapabilities{TurnDetection: true},
	}
	session := NewAgentSession(initial, nil, AgentSessionOptions{
		AllowInterruptions:    false,
		AllowInterruptionsSet: true,
	})

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v, want nil", err)
	}
	defer session.Stop(context.Background())
	initialActivity := session.activity
	events := session.ErrorEvents()

	session.UpdateAgent(next)

	if session.activity != initialActivity {
		t.Fatalf("session.activity changed to %#v, want invalid handoff to preserve current activity", session.activity)
	}
	if session.Agent != initial {
		t.Fatalf("session.Agent = %#v, want invalid handoff to preserve current agent", session.Agent)
	}
	select {
	case ev := <-events:
		want := "the RealtimeModel uses a server-side turn detection, allow_interruptions cannot be False, disable turn_detection in the RealtimeModel and use VAD on the AgentSession instead"
		if ev.Error == nil || ev.Error.Error() != want {
			t.Fatalf("error event = %#v, want realtime turn detection interruption error", ev)
		}
	case <-testTimeout():
		t.Fatal("ErrorEvents did not receive invalid realtime handoff error")
	}
}

func TestAgentSessionUpdateAgentWhileRunningClearsReusedRealtimeSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	initial := &trackingAgent{Agent: NewAgent("initial")}
	rtSession := &fakeRealtimeSession{}
	realtime := &fakeRealtimeModel{session: rtSession}
	initial.RealtimeModel = realtime
	next := &trackingAgent{Agent: NewAgent("next")}
	next.RealtimeModel = realtime
	session := NewAgentSession(initial, nil, AgentSessionOptions{})

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v, want nil", err)
	}
	defer session.Stop(context.Background())
	assistant, ok := session.Assistant.(*MultimodalAgent)
	if !ok {
		t.Fatalf("Assistant = %T, want *MultimodalAgent", session.Assistant)
	}

	session.UpdateAgent(next)

	if session.RealtimeModel != realtime {
		t.Fatalf("session.RealtimeModel = %#v, want reused realtime model", session.RealtimeModel)
	}
	if assistant.model != realtime {
		t.Fatalf("assistant model after handoff = %#v, want reused realtime model", assistant.model)
	}
	if assistant.rtSession != rtSession {
		t.Fatalf("assistant realtime session after handoff = %#v, want reused session", assistant.rtSession)
	}
	if rtSession.closed != 0 {
		t.Fatalf("reused realtime session closed = %d, want 0", rtSession.closed)
	}
	if rtSession.interrupted != 1 {
		t.Fatalf("reused realtime session interrupts = %d, want 1", rtSession.interrupted)
	}
	if rtSession.cleared != 1 {
		t.Fatalf("reused realtime session clears = %d, want 1", rtSession.cleared)
	}
}

func TestAgentSessionUpdateAgentWhileRunningRefreshesMutableReusedRealtimeSessionConfig(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	initial := &trackingAgent{Agent: NewAgent("initial instructions")}
	initial.Tools = []llm.Tool{&fakeGenerationTool{name: "initial_tool"}}
	rtSession := &fakeRealtimeSession{}
	realtime := &fakeRealtimeModel{
		session: rtSession,
		capabilities: llm.RealtimeCapabilities{
			MutableInstructions: true,
			MutableChatContext:  true,
			MutableTools:        true,
		},
	}
	initial.RealtimeModel = realtime
	next := &trackingAgent{Agent: NewAgent("next instructions")}
	next.Tools = []llm.Tool{&fakeGenerationTool{name: "next_tool"}}
	next.RealtimeModel = realtime
	session := NewAgentSession(initial, nil, AgentSessionOptions{})

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v, want nil", err)
	}
	defer session.Stop(context.Background())

	session.UpdateAgent(next)

	if rtSession.instructions != "next instructions" {
		t.Fatalf("realtime instructions = %q, want next instructions", rtSession.instructions)
	}
	if rtSession.instructionUpdates != 2 {
		t.Fatalf("realtime instruction updates = %d, want 2", rtSession.instructionUpdates)
	}
	if rtSession.chatContextUpdates != 2 {
		t.Fatalf("realtime chat context updates = %d, want 2", rtSession.chatContextUpdates)
	}
	gotTools := toolNames(rtSession.tools)
	if !strings.Contains(strings.Join(gotTools, ","), "next_tool") {
		t.Fatalf("updated realtime tools = %#v, want next_tool present", gotTools)
	}
	if strings.Contains(strings.Join(gotTools, ","), "initial_tool") {
		t.Fatalf("updated realtime tools = %#v, want initial_tool removed", gotTools)
	}
	if rtSession.toolUpdates != 2 {
		t.Fatalf("realtime tool updates = %d, want 2", rtSession.toolUpdates)
	}
	if rtSession.interrupted != 1 {
		t.Fatalf("reused realtime session interrupts = %d, want 1", rtSession.interrupted)
	}
	if rtSession.cleared != 1 {
		t.Fatalf("reused realtime session clears = %d, want 1", rtSession.cleared)
	}
}

func TestAgentSessionUpdateAgentEmitsErrorWhenRealtimeModelRefreshFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	initial := &trackingAgent{Agent: NewAgent("initial")}
	initialRealtime := &fakeRealtimeModel{session: &fakeRealtimeSession{}}
	initial.RealtimeModel = initialRealtime
	next := &trackingAgent{Agent: NewAgent("next")}
	cause := errors.New("realtime session failed")
	nextRealtime := &fakeRealtimeModel{sessionErr: cause}
	next.RealtimeModel = nextRealtime
	session := NewAgentSession(initial, nil, AgentSessionOptions{})

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v, want nil", err)
	}
	defer session.Stop(context.Background())

	session.UpdateAgent(next)

	select {
	case ev := <-session.ErrorEvents():
		if !errors.Is(ev.Error, cause) {
			t.Fatalf("Error = %v, want %v", ev.Error, cause)
		}
		if ev.Source != nextRealtime {
			t.Fatalf("Source = %#v, want next realtime model", ev.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive realtime model refresh error")
	}
}

func TestAgentSessionUpdateAgentWhileRunningSwitchesPipelineToMultimodal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	initial := &trackingAgent{Agent: NewAgent("initial")}
	initial.VAD = &fakePipelineVAD{}
	initial.STT = &fakePipelineSTT{}
	initial.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	initial.TTS = &fakePipelineTTS{}
	next := &trackingAgent{Agent: NewAgent("next")}
	nextRealtimeSession := &fakeRealtimeSession{}
	nextRealtime := &fakeRealtimeModel{session: nextRealtimeSession}
	next.RealtimeModel = nextRealtime
	session := NewAgentSession(initial, nil, AgentSessionOptions{})

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v, want nil", err)
	}
	defer session.Stop(context.Background())
	if _, ok := session.Assistant.(*PipelineAgent); !ok {
		t.Fatalf("Assistant before handoff = %T, want *PipelineAgent", session.Assistant)
	}

	session.UpdateAgent(next)

	assistant, ok := session.Assistant.(*MultimodalAgent)
	if !ok {
		t.Fatalf("Assistant after handoff = %T, want *MultimodalAgent", session.Assistant)
	}
	if assistant.model != nextRealtime {
		t.Fatalf("assistant model after handoff = %#v, want next realtime model", assistant.model)
	}
	if assistant.rtSession != nextRealtimeSession {
		t.Fatalf("assistant realtime session after handoff = %#v, want next model session", assistant.rtSession)
	}
}

func TestAgentSessionUpdateAgentEmitsErrorWhenReplacementAssistantStartFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	initial := &trackingAgent{Agent: NewAgent("initial")}
	initial.VAD = &fakePipelineVAD{}
	initial.STT = &fakePipelineSTT{}
	initial.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	initial.TTS = &fakePipelineTTS{}
	next := &trackingAgent{Agent: NewAgent("next")}
	cause := errors.New("replacement realtime start failed")
	next.RealtimeModel = &fakeRealtimeModel{sessionErr: cause}
	session := NewAgentSession(initial, nil, AgentSessionOptions{})

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v, want nil", err)
	}
	defer session.Stop(context.Background())

	session.UpdateAgent(next)

	assistant, ok := session.Assistant.(*MultimodalAgent)
	if !ok {
		t.Fatalf("Assistant after handoff = %T, want *MultimodalAgent", session.Assistant)
	}
	select {
	case ev := <-session.ErrorEvents():
		if !errors.Is(ev.Error, cause) {
			t.Fatalf("Error = %v, want %v", ev.Error, cause)
		}
		if ev.Source != assistant {
			t.Fatalf("Source = %#v, want replacement assistant", ev.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive replacement assistant start error")
	}
}

func TestAgentSessionUpdateAgentEmitsErrorWhenPreviousAssistantCloseFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	initial := &trackingAgent{Agent: NewAgent("initial")}
	next := &trackingAgent{Agent: NewAgent("next")}
	nextRealtime := &fakeRealtimeModel{session: &fakeRealtimeSession{}}
	next.RealtimeModel = nextRealtime
	cause := errors.New("previous assistant close failed")
	previous := &fakeCloseableSessionAssistant{closeErr: cause}
	session := NewAgentSession(initial, nil, AgentSessionOptions{})
	session.Assistant = previous

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v, want nil", err)
	}
	defer session.Stop(context.Background())

	session.UpdateAgent(next)

	select {
	case ev := <-session.ErrorEvents():
		if !errors.Is(ev.Error, cause) {
			t.Fatalf("Error = %v, want %v", ev.Error, cause)
		}
		if ev.Source != previous {
			t.Fatalf("Source = %#v, want previous assistant", ev.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive previous assistant close error")
	}
	if previous.closed != 1 {
		t.Fatalf("previous assistant closed = %d, want 1", previous.closed)
	}
}

func TestAgentSessionUpdateAgentWhileRunningSwitchesMultimodalToPipeline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	initial := &trackingAgent{Agent: NewAgent("initial")}
	initialRealtimeSession := &fakeRealtimeSession{}
	initial.RealtimeModel = &fakeRealtimeModel{session: initialRealtimeSession}
	next := &trackingAgent{Agent: NewAgent("next")}
	nextVAD := &fakePipelineVAD{}
	nextSTT := &fakePipelineSTT{}
	nextLLM := &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	nextTTS := &fakePipelineTTS{}
	next.VAD = nextVAD
	next.STT = nextSTT
	next.LLM = nextLLM
	next.TTS = nextTTS
	session := NewAgentSession(initial, nil, AgentSessionOptions{})

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v, want nil", err)
	}
	defer session.Stop(context.Background())
	if _, ok := session.Assistant.(*MultimodalAgent); !ok {
		t.Fatalf("Assistant before handoff = %T, want *MultimodalAgent", session.Assistant)
	}

	session.UpdateAgent(next)

	assistant, ok := session.Assistant.(*PipelineAgent)
	if !ok {
		t.Fatalf("Assistant after handoff = %T, want *PipelineAgent", session.Assistant)
	}
	if session.RealtimeModel != nil {
		t.Fatalf("session.RealtimeModel = %#v, want nil after pipeline handoff", session.RealtimeModel)
	}
	if assistant.vad != nextVAD {
		t.Fatalf("pipeline.vad = %#v, want next VAD", assistant.vad)
	}
	if assistant.stt != nextSTT {
		t.Fatalf("pipeline.stt = %#v, want next STT", assistant.stt)
	}
	if assistant.LLM != nextLLM {
		t.Fatalf("pipeline.LLM = %#v, want next LLM", assistant.LLM)
	}
	if assistant.tts != nextTTS {
		t.Fatalf("pipeline.tts = %#v, want next TTS", assistant.tts)
	}
	if initialRealtimeSession.closed != 1 {
		t.Fatalf("initial realtime session closed = %d, want 1", initialRealtimeSession.closed)
	}
}

func TestAgentSessionUpdateAgentPreservesSessionComponentsWhenNextAgentOmitsThem(t *testing.T) {
	initial := &trackingAgent{Agent: NewAgent("initial")}
	next := &trackingAgent{Agent: NewAgent("next")}
	session := NewAgentSession(initial, nil, AgentSessionOptions{})
	sessionSTT := &fakePipelineSTT{}
	sessionVAD := &fakePipelineVAD{}
	sessionLLM := &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	sessionTTS := &fakePipelineTTS{}
	session.STT = sessionSTT
	session.VAD = sessionVAD
	session.LLM = sessionLLM
	session.TTS = sessionTTS

	session.UpdateAgent(next)

	if session.STT != sessionSTT {
		t.Fatalf("session.STT = %#v, want preserved session STT", session.STT)
	}
	if session.VAD != sessionVAD {
		t.Fatalf("session.VAD = %#v, want preserved session VAD", session.VAD)
	}
	if session.LLM != sessionLLM {
		t.Fatalf("session.LLM = %#v, want preserved session LLM", session.LLM)
	}
	if session.TTS != sessionTTS {
		t.Fatalf("session.TTS = %#v, want preserved session TTS", session.TTS)
	}
}

func TestAgentSessionUpdateAgentUsesNextAgentComponentsWhenProvided(t *testing.T) {
	initial := &trackingAgent{Agent: NewAgent("initial")}
	next := &trackingAgent{Agent: NewAgent("next")}
	nextSTT := &fakePipelineSTT{}
	nextVAD := &fakePipelineVAD{}
	nextLLM := &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	nextTTS := &fakePipelineTTS{}
	next.STT = nextSTT
	next.VAD = nextVAD
	next.LLM = nextLLM
	next.TTS = nextTTS
	session := NewAgentSession(initial, nil, AgentSessionOptions{})
	session.STT = &fakePipelineSTT{}
	session.VAD = &fakePipelineVAD{}
	session.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	session.TTS = &fakePipelineTTS{}

	session.UpdateAgent(next)

	if session.STT != nextSTT {
		t.Fatalf("session.STT = %#v, want next agent STT", session.STT)
	}
	if session.VAD != nextVAD {
		t.Fatalf("session.VAD = %#v, want next agent VAD", session.VAD)
	}
	if session.LLM != nextLLM {
		t.Fatalf("session.LLM = %#v, want next agent LLM", session.LLM)
	}
	if session.TTS != nextTTS {
		t.Fatalf("session.TTS = %#v, want next agent TTS", session.TTS)
	}
}

func TestAgentSessionUpdateAgentRefreshesPipelineAssistantComponents(t *testing.T) {
	initial := &trackingAgent{Agent: NewAgent("initial")}
	initial.VAD = &fakePipelineVAD{}
	initial.STT = &fakePipelineSTT{}
	initial.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	initial.TTS = &fakePipelineTTS{}
	next := &trackingAgent{Agent: NewAgent("next")}
	nextVAD := &fakePipelineVAD{}
	nextSTT := &fakePipelineSTT{}
	nextLLM := &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	nextTTS := &fakePipelineTTS{}
	next.VAD = nextVAD
	next.STT = nextSTT
	next.LLM = nextLLM
	next.TTS = nextTTS
	session := NewAgentSession(initial, nil, AgentSessionOptions{})
	pipeline := NewPipelineAgent(initial.VAD, initial.STT, initial.LLM, initial.TTS, session.ChatCtx)
	session.Assistant = pipeline
	session.activity = NewAgentActivity(initial, session)
	session.started = true

	session.UpdateAgent(next)

	if pipeline.vad != nextVAD {
		t.Fatalf("pipeline.vad = %#v, want next agent VAD", pipeline.vad)
	}
	if pipeline.stt != nextSTT {
		t.Fatalf("pipeline.stt = %#v, want next agent STT", pipeline.stt)
	}
	if pipeline.LLM != nextLLM {
		t.Fatalf("pipeline.LLM = %#v, want next agent LLM", pipeline.LLM)
	}
	if pipeline.tts != nextTTS {
		t.Fatalf("pipeline.tts = %#v, want next agent TTS", pipeline.tts)
	}
}

func TestAgentSessionUpdateAgentWhileRunningStartsNewActivity(t *testing.T) {
	initial := &trackingAgent{Agent: NewAgent("initial")}
	initial.ID = "agent_initial"
	next := &trackingAgent{Agent: NewAgent("next")}
	next.ID = "agent_next"
	session := NewAgentSession(initial, nil, AgentSessionOptions{})
	oldActivity := NewAgentActivity(initial, session)
	session.activity = oldActivity
	session.started = true
	result := NewRunResult(session.ChatCtx)
	session.runState = result

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
	var handoff *llm.AgentHandoff
	for _, item := range session.ChatCtx.Items {
		if candidate, ok := item.(*llm.AgentHandoff); ok {
			handoff = candidate
			break
		}
	}
	if handoff == nil {
		t.Fatalf("session ChatCtx items = %#v, want agent handoff item", session.ChatCtx.Items)
	}
	if handoff.OldAgentID == nil || *handoff.OldAgentID != "agent_initial" || handoff.NewAgentID != "agent_next" {
		t.Fatalf("handoff = %#v, want initial to next", handoff)
	}
	events := result.Events()
	if len(events) != 1 {
		t.Fatalf("RunResult events length = %d, want handoff event", len(events))
	}
	handoffEvent, ok := events[0].(*AgentHandoffEvent)
	if !ok {
		t.Fatalf("events[0] = %T, want *AgentHandoffEvent", events[0])
	}
	if handoffEvent.Item != handoff || handoffEvent.OldAgent != initial.Agent || handoffEvent.NewAgent != next.Agent {
		t.Fatalf("handoff event = %#v, want recorded session handoff", handoffEvent)
	}
}

func TestAgentSessionUpdateAgentBlocksRealtimeGenerationOnPreviousActivity(t *testing.T) {
	initial := &trackingAgent{Agent: NewAgent("initial")}
	next := &trackingAgent{Agent: NewAgent("next")}
	session := NewAgentSession(initial, nil, AgentSessionOptions{})
	oldActivity := NewAgentActivity(initial, session)
	session.activity = oldActivity
	session.started = true

	session.UpdateAgent(next)

	handle, err := oldActivity.OnGenerationCreated(llm.GenerationCreatedEvent{
		ResponseID:    "response_1",
		UserInitiated: false,
	})
	if !errors.Is(err, ErrSpeechSchedulingPaused) {
		t.Fatalf("OnGenerationCreated error = %v, want ErrSpeechSchedulingPaused", err)
	}
	if handle != nil {
		t.Fatalf("OnGenerationCreated handle = %#v, want nil after UpdateAgent", handle)
	}
	select {
	case ev := <-session.SpeechCreatedEvents():
		t.Fatalf("unexpected SpeechCreated event from previous activity: %#v", ev)
	default:
	}
}

func TestAgentSessionUpdateAgentBlocksPreviousActivityDuringReplacementStart(t *testing.T) {
	initial := &trackingAgent{Agent: NewAgent("initial")}
	initial.VAD = &fakePipelineVAD{}
	initial.STT = &fakePipelineSTT{}
	initial.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	initial.TTS = &fakePipelineTTS{}
	next := &trackingAgent{Agent: NewAgent("next")}
	sessionStarted := make(chan struct{})
	releaseSession := make(chan struct{})
	next.RealtimeModel = &fakeRealtimeModel{
		session:        &fakeRealtimeSession{},
		sessionStarted: sessionStarted,
		sessionRelease: releaseSession,
	}
	session := NewAgentSession(initial, nil, AgentSessionOptions{})
	oldActivity := NewAgentActivity(initial, session)
	session.activity = oldActivity
	session.Assistant = &PipelineAgent{}
	session.started = true

	done := make(chan struct{})
	go func() {
		session.UpdateAgent(next)
		close(done)
	}()
	<-sessionStarted

	handle, err := oldActivity.OnGenerationCreated(llm.GenerationCreatedEvent{
		ResponseID:    "response_1",
		UserInitiated: false,
	})
	if !errors.Is(err, ErrSpeechSchedulingPaused) {
		t.Fatalf("OnGenerationCreated during replacement start error = %v, want ErrSpeechSchedulingPaused", err)
	}
	if handle != nil {
		t.Fatalf("OnGenerationCreated handle = %#v, want nil while replacement starts", handle)
	}
	select {
	case ev := <-session.SpeechCreatedEvents():
		t.Fatalf("unexpected SpeechCreated event from previous activity during replacement start: %#v", ev)
	default:
	}

	close(releaseSession)
	select {
	case <-done:
	case <-testTimeout():
		t.Fatal("UpdateAgent did not finish after releasing replacement session")
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

func TestAgentStateChangedEventMarshalJSONMatchesReferencePayload(t *testing.T) {
	ev := &AgentStateChangedEvent{
		OldState:  AgentStateInitializing,
		NewState:  AgentStateListening,
		CreatedAt: time.Unix(22, 250_000_000),
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal AgentStateChangedEvent returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal marshaled AgentStateChangedEvent returned error: %v", err)
	}
	if payload["type"] != "agent_state_changed" {
		t.Fatalf("type = %#v, want agent_state_changed", payload["type"])
	}
	if payload["old_state"] != string(AgentStateInitializing) || payload["new_state"] != string(AgentStateListening) {
		t.Fatalf("state payload = %#v, want reference old_state/new_state", payload)
	}
	if payload["created_at"] != 22.25 {
		t.Fatalf("created_at = %#v, want 22.25", payload["created_at"])
	}
	if _, ok := payload["OldState"]; ok {
		t.Fatalf("payload used Go field names: %#v", payload)
	}
}

func TestUserStateChangedEventMarshalJSONMatchesReferencePayload(t *testing.T) {
	ev := &UserStateChangedEvent{
		OldState:  UserStateListening,
		NewState:  UserStateSpeaking,
		CreatedAt: time.Unix(23, 500_000_000),
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal UserStateChangedEvent returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal marshaled UserStateChangedEvent returned error: %v", err)
	}
	if payload["type"] != "user_state_changed" {
		t.Fatalf("type = %#v, want user_state_changed", payload["type"])
	}
	if payload["old_state"] != string(UserStateListening) || payload["new_state"] != string(UserStateSpeaking) {
		t.Fatalf("state payload = %#v, want reference old_state/new_state", payload)
	}
	if payload["created_at"] != 23.5 {
		t.Fatalf("created_at = %#v, want 23.5", payload["created_at"])
	}
	if _, ok := payload["OldState"]; ok {
		t.Fatalf("payload used Go field names: %#v", payload)
	}
}

func TestAgentSessionMarksUserAwayAfterIdleTimeout(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{UserAwayTimeout: 0.01})

	session.UpdateAgentState(AgentStateListening)

	select {
	case ev := <-session.UserStateChangedCh:
		if ev.OldState != UserStateListening || ev.NewState != UserStateAway {
			t.Fatalf("event states = %q -> %q, want listening -> away", ev.OldState, ev.NewState)
		}
	case <-time.After(time.Second):
		t.Fatal("UserStateChangedCh did not receive away event")
	}
	if got := session.UserState(); got != UserStateAway {
		t.Fatalf("UserState() = %q, want away", got)
	}
}

func TestAgentSessionExplicitZeroUserAwayTimeoutMarksAwayImmediately(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		UserAwayTimeout:    0,
		UserAwayTimeoutSet: true,
	})

	session.UpdateAgentState(AgentStateListening)

	select {
	case ev := <-session.UserStateChangedCh:
		if ev.OldState != UserStateListening || ev.NewState != UserStateAway {
			t.Fatalf("event states = %q -> %q, want listening -> away", ev.OldState, ev.NewState)
		}
	case <-time.After(time.Second):
		t.Fatal("UserStateChangedCh did not receive immediate away event")
	}
	if got := session.UserState(); got != UserStateAway {
		t.Fatalf("UserState() = %q, want away", got)
	}
}

func TestAgentSessionCancelsUserAwayTimerWhenUserSpeaks(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{UserAwayTimeout: 0.02})

	session.UpdateAgentState(AgentStateListening)
	session.UpdateUserState(UserStateSpeaking)

	select {
	case ev := <-session.UserStateChangedCh:
		if ev.NewState != UserStateSpeaking {
			t.Fatalf("first user state event = %q, want speaking", ev.NewState)
		}
	case <-time.After(time.Second):
		t.Fatal("UserStateChangedCh did not receive speaking event")
	}
	select {
	case ev := <-session.UserStateChangedCh:
		t.Fatalf("unexpected user state event after speaking = %q -> %q", ev.OldState, ev.NewState)
	case <-time.After(60 * time.Millisecond):
	}
	if got := session.UserState(); got != UserStateSpeaking {
		t.Fatalf("UserState() = %q, want speaking", got)
	}
}

func TestAgentSessionUserAwayTimerWaitsForGate(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{UserAwayTimeout: 0.01})
	gated := true
	session.SetUserAwayTimerGate(func() bool { return gated })

	session.UpdateAgentState(AgentStateListening)

	select {
	case ev := <-session.UserStateChangedCh:
		t.Fatalf("unexpected user state event while user-away gate closed = %q -> %q", ev.OldState, ev.NewState)
	case <-time.After(40 * time.Millisecond):
	}
	if got := session.UserState(); got != UserStateListening {
		t.Fatalf("UserState() = %q, want listening while user-away gate is closed", got)
	}

	gated = false
	session.RefreshUserAwayTimer()

	select {
	case ev := <-session.UserStateChangedCh:
		if ev.OldState != UserStateListening || ev.NewState != UserStateAway {
			t.Fatalf("event states = %q -> %q, want listening -> away after gate opens", ev.OldState, ev.NewState)
		}
	case <-time.After(time.Second):
		t.Fatal("UserStateChangedCh did not receive away event after gate opened")
	}
}

func TestAgentSessionClaimUserTurnPinsUserStateUntilRelease(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	err := session.ClaimUserTurn(context.Background(), func(context.Context) error {
		if got := session.UserState(); got != UserStateSpeaking {
			t.Fatalf("UserState() while claimed = %q, want %q", got, UserStateSpeaking)
		}
		session.UpdateUserState(UserStateListening)
		if got := session.UserState(); got != UserStateSpeaking {
			t.Fatalf("UserState() after listening update while claimed = %q, want %q", got, UserStateSpeaking)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ClaimUserTurn error = %v", err)
	}
	if got := session.UserState(); got != UserStateListening {
		t.Fatalf("UserState() after claim release = %q, want %q", got, UserStateListening)
	}
}

func TestAgentSessionClaimUserTurnReleaseDerivesStateFromActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	session.UpdateUserState(UserStateSpeaking)

	err := session.ClaimUserTurn(context.Background(), func(context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("ClaimUserTurn error = %v", err)
	}
	if got := session.UserState(); got != UserStateListening {
		t.Fatalf("UserState() after claim release = %q, want %q when activity is silent", got, UserStateListening)
	}
}

func TestAgentSessionCanDisableUserAwayTimeout(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		UserAwayTimeout:        0.01,
		DisableUserAwayTimeout: true,
	})

	session.UpdateAgentState(AgentStateListening)

	select {
	case ev := <-session.UserStateChangedCh:
		t.Fatalf("unexpected user state event with user-away timeout disabled = %q -> %q", ev.OldState, ev.NewState)
	case <-time.After(40 * time.Millisecond):
	}
	if got := session.UserState(); got != UserStateListening {
		t.Fatalf("UserState() = %q, want listening", got)
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
	recorder := &recordingLogger{}
	oldLogger := logutil.Logger
	logutil.SetLogger(recorder)
	t.Cleanup(func() { logutil.SetLogger(oldLogger) })
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
		if ev.Usage.LLMInputTokens() != 7 || ev.Usage.LLMOutputTokens() != 11 {
			t.Fatalf("usage event summary = %#v, want input=7 output=11", ev.Usage)
		}
		if ev.CreatedAt.Before(before) || ev.CreatedAt.IsZero() {
			t.Fatalf("usage event CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
		}
	case <-time.After(time.Second):
		t.Fatal("SessionUsageUpdatedEvents did not receive event")
	}
	if !recorder.hasInfo("LLM metrics") {
		t.Fatalf("logged info messages = %#v, want LLM metrics log", recorder.infoMessages)
	}
	if got := recorder.infoValue("LLM metrics", "type"); got != "llm_metrics" {
		t.Fatalf("logged LLM metrics type = %#v, want llm_metrics", got)
	}
}

func TestAgentSessionUsageUpdatedEventCarriesModelUsage(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	metrics := &telemetry.LLMMetrics{
		PromptTokens:     7,
		CompletionTokens: 11,
		Metadata:         &telemetry.Metadata{ModelProvider: "openai", ModelName: "gpt-4o"},
	}

	session.EmitMetricsCollected(metrics)

	select {
	case ev := <-session.SessionUsageUpdatedEvents():
		if len(ev.Usage.ModelUsage) != 1 {
			t.Fatalf("usage event model usage = %#v, want one entry", ev.Usage.ModelUsage)
		}
		llmUsage, ok := ev.Usage.ModelUsage[0].(*telemetry.LLMModelUsage)
		if !ok {
			t.Fatalf("usage event model usage type = %T, want *telemetry.LLMModelUsage", ev.Usage.ModelUsage[0])
		}
		if llmUsage.Provider != "openai" || llmUsage.Model != "gpt-4o" {
			t.Fatalf("usage provider/model = %q/%q, want openai/gpt-4o", llmUsage.Provider, llmUsage.Model)
		}
		if llmUsage.InputTokens != 7 || llmUsage.OutputTokens != 11 {
			t.Fatalf("usage tokens = input %d output %d, want 7/11", llmUsage.InputTokens, llmUsage.OutputTokens)
		}
	case <-time.After(time.Second):
		t.Fatal("SessionUsageUpdatedEvents did not receive event")
	}
}

func TestAgentSessionUsageReturnsCollectedSummary(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	session.EmitMetricsCollected(&telemetry.LLMMetrics{
		PromptTokens:     3,
		CompletionTokens: 5,
	})

	usage := session.Usage()
	if usage.LLMPromptTokens != 3 || usage.LLMCompletionTokens != 5 {
		t.Fatalf("Usage = %#v, want prompt=3 completion=5", usage)
	}
}

type recordingLogger struct {
	infoMessages  []string
	warnMessages  []string
	errorMessages []string
	infoFields    map[string]map[string]any
}

func (l *recordingLogger) Debugw(msg string, keysAndValues ...any) {}
func (l *recordingLogger) Infow(msg string, keysAndValues ...any) {
	l.infoMessages = append(l.infoMessages, msg)
	if l.infoFields == nil {
		l.infoFields = make(map[string]map[string]any)
	}
	fields := make(map[string]any)
	for i := 0; i+1 < len(keysAndValues); i += 2 {
		key, ok := keysAndValues[i].(string)
		if !ok {
			continue
		}
		fields[key] = keysAndValues[i+1]
	}
	l.infoFields[msg] = fields
}
func (l *recordingLogger) Warnw(msg string, err error, keysAndValues ...any) {
	l.warnMessages = append(l.warnMessages, msg)
}
func (l *recordingLogger) Errorw(msg string, err error, keysAndValues ...any) {
	l.errorMessages = append(l.errorMessages, msg)
}
func (l *recordingLogger) WithValues(keysAndValues ...any) livekitlogger.Logger {
	return l
}
func (l *recordingLogger) WithUnlikelyValues(keysAndValues ...any) livekitlogger.UnlikelyLogger {
	return livekitlogger.GetDiscardLogger().WithUnlikelyValues(keysAndValues...)
}
func (l *recordingLogger) WithName(name string) livekitlogger.Logger {
	return l
}
func (l *recordingLogger) WithComponent(component string) livekitlogger.Logger {
	return l
}
func (l *recordingLogger) WithCallDepth(depth int) livekitlogger.Logger {
	return l
}
func (l *recordingLogger) WithItemSampler() livekitlogger.Logger {
	return l
}
func (l *recordingLogger) WithoutSampler() livekitlogger.Logger {
	return l
}
func (l *recordingLogger) WithDeferredValues() (livekitlogger.Logger, livekitlogger.DeferredFieldResolver) {
	return livekitlogger.GetDiscardLogger().WithDeferredValues()
}

func (l *recordingLogger) hasInfo(msg string) bool {
	for _, logged := range l.infoMessages {
		if logged == msg {
			return true
		}
	}
	return false
}

func (l *recordingLogger) hasError(msg string) bool {
	for _, logged := range l.errorMessages {
		if logged == msg {
			return true
		}
	}
	return false
}

func (l *recordingLogger) infoValue(msg, key string) any {
	if l.infoFields == nil {
		return nil
	}
	return l.infoFields[msg][key]
}

type fakeAvatarProvider struct {
	startCalls int
	startErr   error
	state      AvatarState
}

func (f *fakeAvatarProvider) Start(ctx context.Context) error {
	f.startCalls++
	return f.startErr
}

func (f *fakeAvatarProvider) UpdateState(state AvatarState) error {
	f.state = state
	return nil
}

type fakeSessionMCPServer struct {
	id     string
	tools  []llm.Tool
	closed int
}

func (f *fakeSessionMCPServer) Initialize(context.Context) error { return nil }

func (f *fakeSessionMCPServer) Initialized() bool { return true }

func (f *fakeSessionMCPServer) InvalidateCache() {}

func (f *fakeSessionMCPServer) ListTools(context.Context) ([]llm.Tool, error) {
	return f.tools, nil
}

func (f *fakeSessionMCPServer) Close() error {
	f.closed++
	return nil
}
