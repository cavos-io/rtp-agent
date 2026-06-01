package agent

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/cavos-io/conversation-worker/core/llm"
)

func TestRunResultRecordsSupportedItemsInChronologicalOrder(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())
	late := time.Now()
	early := late.Add(-time.Second)
	message := &llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleAssistant, CreatedAt: late}
	functionCall := &llm.FunctionCall{ID: "fnc_1", CallID: "call_1", Name: "lookup", CreatedAt: early}
	functionOutput := &llm.FunctionCallOutput{ID: "out_1", CallID: "call_1", Name: "lookup", CreatedAt: late.Add(time.Second)}

	result.RecordItem(message)
	result.RecordItem(functionOutput)
	result.RecordItem(functionCall)

	events := result.Events()
	if len(events) != 3 {
		t.Fatalf("Events length = %d, want 3", len(events))
	}
	if ev, ok := events[0].(*FunctionCallEvent); !ok || ev.Item != functionCall || ev.GetType() != "function_call" {
		t.Fatalf("events[0] = %#v, want function call event for early call", events[0])
	}
	if ev, ok := events[1].(*ChatMessageEvent); !ok || ev.Item != message || ev.GetType() != "message" {
		t.Fatalf("events[1] = %#v, want message event", events[1])
	}
	if ev, ok := events[2].(*FunctionCallOutputEvent); !ok || ev.Item != functionOutput || ev.GetType() != "function_call_output" {
		t.Fatalf("events[2] = %#v, want function output event", events[2])
	}
}

func TestRunResultRecordItemIgnoresUnsupportedItems(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())

	result.RecordItem(&llm.MetricsReport{CreatedAt: time.Now()})

	if len(result.Events()) != 0 {
		t.Fatalf("Events length = %d, want 0 for unsupported metrics report", len(result.Events()))
	}
}

func TestRunResultRecordsAgentHandoff(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())
	handoff := &llm.AgentHandoff{ID: "handoff_1", NewAgentID: "agent_2", CreatedAt: time.Now()}
	oldAgent := NewAgent("old")
	newAgent := NewAgent("new")

	result.RecordAgentHandoff(handoff, oldAgent, newAgent)

	events := result.Events()
	if len(events) != 1 {
		t.Fatalf("Events length = %d, want 1", len(events))
	}
	ev, ok := events[0].(*AgentHandoffEvent)
	if !ok {
		t.Fatalf("events[0] = %T, want *AgentHandoffEvent", events[0])
	}
	if ev.GetType() != "agent_handoff" || ev.Item != handoff || ev.OldAgent != oldAgent || ev.NewAgent != newAgent {
		t.Fatalf("handoff event = %#v, want original handoff and agents", ev)
	}
}

func TestRunResultEventsReturnsCopy(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())
	message := &llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleAssistant, CreatedAt: time.Now()}
	result.RecordItem(message)

	events := result.Events()
	events[0] = nil

	if result.Events()[0] == nil {
		t.Fatal("Events returned mutable backing storage")
	}
}

func TestRunResultFinalOutputRequiresDone(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())

	_, err := result.FinalOutput()

	if !errors.Is(err, ErrRunResultNotDone) {
		t.Fatalf("FinalOutput error = %v, want ErrRunResultNotDone", err)
	}
}

func TestRunResultFinalOutputRequiresOutput(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())
	result.MarkDone()

	_, err := result.FinalOutput()

	if !errors.Is(err, ErrRunResultNoFinalOutput) {
		t.Fatalf("FinalOutput error = %v, want ErrRunResultNoFinalOutput", err)
	}
}

func TestRunResultFinalOutputReturnsValueAfterDone(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())
	result.SetFinalOutput("done")
	result.MarkDone()

	output, err := result.FinalOutput()

	if err != nil {
		t.Fatalf("FinalOutput error = %v, want nil", err)
	}
	if output != "done" {
		t.Fatalf("FinalOutput = %#v, want done", output)
	}
	if !result.Done() {
		t.Fatal("Done() = false, want true after MarkDone")
	}
}

func TestRunResultWatchSpeechHandleRecordsAddedItems(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())
	speech := NewSpeechHandle(true, DefaultInputDetails())
	message := &llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleAssistant, CreatedAt: time.Now()}

	result.WatchSpeechHandle(speech)
	speech.AddChatItems(message)

	events := result.Events()
	if len(events) != 1 {
		t.Fatalf("Events length = %d, want 1", len(events))
	}
	ev, ok := events[0].(*ChatMessageEvent)
	if !ok || ev.Item != message {
		t.Fatalf("event = %#v, want message event for added item", events[0])
	}
}

func TestRunResultWatchSpeechHandleMarksDoneWhenSpeechDone(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())
	first := NewSpeechHandle(true, DefaultInputDetails())
	second := NewSpeechHandle(true, DefaultInputDetails())

	result.WatchSpeechHandle(first)
	result.WatchSpeechHandle(second)
	first.MarkDone()
	if result.Done() {
		t.Fatal("RunResult marked done before all watched speech handles finished")
	}

	second.MarkDone()

	if !result.Done() {
		t.Fatal("RunResult did not mark done after all watched speech handles finished")
	}
}

func TestRunResultFinalOutputUsesLastCompletedSpeechHandle(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())
	first := NewSpeechHandle(true, DefaultInputDetails())
	second := NewSpeechHandle(true, DefaultInputDetails())
	first.SetRunFinalOutput("first")
	second.SetRunFinalOutput("second")

	result.WatchSpeechHandle(first)
	result.WatchSpeechHandle(second)

	first.MarkDone()
	second.MarkDone()

	output, err := result.FinalOutput()
	if err != nil {
		t.Fatalf("FinalOutput error = %v, want nil", err)
	}
	if output != "second" {
		t.Fatalf("FinalOutput = %#v, want second", output)
	}
}

func TestRunResultFinalOutputReturnsSpeechFinalOutputError(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())
	speech := NewSpeechHandle(true, DefaultInputDetails())
	finalErr := errors.New("final output failed")
	speech.SetRunFinalOutput(finalErr)

	result.WatchSpeechHandle(speech)
	speech.MarkDone()

	output, err := result.FinalOutput()
	if !errors.Is(err, finalErr) {
		t.Fatalf("FinalOutput error = %v, want finalErr", err)
	}
	if output != nil {
		t.Fatalf("FinalOutput output = %#v, want nil when final output is error", output)
	}
}

func TestRunResultFinalOutputRejectsUnexpectedType(t *testing.T) {
	result := NewRunResultWithOutputType(llm.NewChatContext(), reflect.TypeOf(""))
	speech := NewSpeechHandle(true, DefaultInputDetails())
	speech.SetRunFinalOutput(42)

	result.WatchSpeechHandle(speech)
	speech.MarkDone()

	output, err := result.FinalOutput()
	if !errors.Is(err, ErrRunResultFinalOutputType) {
		t.Fatalf("FinalOutput error = %v, want ErrRunResultFinalOutputType", err)
	}
	if output != nil {
		t.Fatalf("FinalOutput output = %#v, want nil on type mismatch", output)
	}
}

func TestRunResultFinalOutputAcceptsExpectedType(t *testing.T) {
	result := NewRunResultWithOutputType(llm.NewChatContext(), reflect.TypeOf(""))
	speech := NewSpeechHandle(true, DefaultInputDetails())
	speech.SetRunFinalOutput("done")

	result.WatchSpeechHandle(speech)
	speech.MarkDone()

	output, err := result.FinalOutput()
	if err != nil {
		t.Fatalf("FinalOutput error = %v, want nil", err)
	}
	if output != "done" {
		t.Fatalf("FinalOutput = %#v, want done", output)
	}
}

func TestRunResultUnwatchSpeechHandleRemovesCallbacks(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())
	speech := NewSpeechHandle(true, DefaultInputDetails())
	message := &llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleAssistant, CreatedAt: time.Now()}

	if ok := result.WatchSpeechHandle(speech); !ok {
		t.Fatal("WatchSpeechHandle returned false, want true for first watch")
	}
	if ok := result.UnwatchSpeechHandle(speech); !ok {
		t.Fatal("UnwatchSpeechHandle returned false, want true for watched handle")
	}

	speech.AddChatItems(message)
	speech.MarkDone()

	if len(result.Events()) != 0 {
		t.Fatalf("Events length = %d, want 0 after unwatch", len(result.Events()))
	}
	if result.Done() {
		t.Fatal("RunResult marked done after unwatched speech completed")
	}
}

func TestRunAssertUsesRecordedEventsForMessages(t *testing.T) {
	chatCtx := llm.NewChatContext()
	result := NewRunResult(chatCtx)
	message := &llm.ChatMessage{
		ID:        "msg_1",
		Role:      llm.ChatRoleAssistant,
		Content:   []llm.ChatContent{{Text: "hello from recorded events"}},
		CreatedAt: time.Now(),
	}

	result.RecordItem(message)

	if err := result.Expect.ContainsMessage(llm.ChatRoleAssistant, "recorded events").HasError(); err != nil {
		t.Fatalf("ContainsMessage returned error = %v, want nil for recorded event", err)
	}
}

func TestRunAssertUsesRecordedEventsForMessageRole(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())
	message := &llm.ChatMessage{
		ID:        "msg_1",
		Role:      llm.ChatRoleAssistant,
		Content:   []llm.ChatContent{{Text: "any content"}},
		CreatedAt: time.Now(),
	}

	result.RecordItem(message)

	if err := result.Expect.ContainsMessageRole(llm.ChatRoleAssistant).HasError(); err != nil {
		t.Fatalf("ContainsMessageRole returned error = %v, want nil for recorded role", err)
	}
}

func TestRunAssertReportsMissingMessageRole(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())
	message := &llm.ChatMessage{
		ID:        "msg_1",
		Role:      llm.ChatRoleAssistant,
		Content:   []llm.ChatContent{{Text: "any content"}},
		CreatedAt: time.Now(),
	}

	result.RecordItem(message)

	err := result.Expect.ContainsMessageRole(llm.ChatRoleUser).HasError()
	if err == nil {
		t.Fatal("ContainsMessageRole error = nil, want missing role error")
	}
}

func TestRunAssertUsesRecordedEventsForFunctionCalls(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())
	functionCall := &llm.FunctionCall{
		ID:        "fnc_1",
		CallID:    "call_1",
		Name:      "lookup",
		CreatedAt: time.Now(),
	}

	result.RecordItem(functionCall)

	if err := result.Expect.IsFunctionCall("lookup").HasError(); err != nil {
		t.Fatalf("IsFunctionCall returned error = %v, want nil for recorded event", err)
	}
}

func TestRunAssertUsesRecordedEventsForFunctionCallArguments(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())
	functionCall := &llm.FunctionCall{
		ID:        "fnc_1",
		CallID:    "call_1",
		Name:      "lookup",
		Arguments: `{"city":"Jakarta","limit":3,"includeClosed":false}`,
		CreatedAt: time.Now(),
	}

	result.RecordItem(functionCall)

	err := result.Expect.IsFunctionCallWithArguments("lookup", map[string]any{
		"city":          "Jakarta",
		"limit":         float64(3),
		"includeClosed": false,
	}).HasError()
	if err != nil {
		t.Fatalf("IsFunctionCallWithArguments returned error = %v, want nil for matching arguments", err)
	}
}

func TestRunAssertReportsFunctionCallArgumentMismatch(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())
	functionCall := &llm.FunctionCall{
		ID:        "fnc_1",
		CallID:    "call_1",
		Name:      "lookup",
		Arguments: `{"city":"Jakarta"}`,
		CreatedAt: time.Now(),
	}

	result.RecordItem(functionCall)

	err := result.Expect.IsFunctionCallWithArguments("lookup", map[string]any{
		"city": "Bandung",
	}).HasError()
	if err == nil {
		t.Fatal("IsFunctionCallWithArguments error = nil, want mismatch error")
	}
}

func TestRunAssertUsesRecordedEventsForFunctionCallOutputs(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())
	output := &llm.FunctionCallOutput{
		ID:        "out_1",
		CallID:    "call_1",
		Name:      "lookup",
		Output:    "done",
		IsError:   true,
		CreatedAt: time.Now(),
	}

	result.RecordItem(output)

	if err := result.Expect.ContainsFunctionCallOutput("done", true).HasError(); err != nil {
		t.Fatalf("ContainsFunctionCallOutput returned error = %v, want nil for recorded event", err)
	}
}

func TestRunAssertUsesRecordedEventsForAgentHandoffs(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())
	handoff := &llm.AgentHandoff{ID: "handoff_1", NewAgentID: "agent_2", CreatedAt: time.Now()}
	oldAgent := NewAgent("old")
	newAgent := NewAgent("new")

	result.RecordAgentHandoff(handoff, oldAgent, newAgent)

	if err := result.Expect.ContainsAgentHandoff(newAgent).HasError(); err != nil {
		t.Fatalf("ContainsAgentHandoff returned error = %v, want nil for recorded event", err)
	}
}

func TestRunAssertNoMoreEventsPassesAfterSkippingRecordedEvents(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())
	result.RecordItem(&llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleAssistant, CreatedAt: time.Now()})
	result.RecordItem(&llm.FunctionCall{ID: "fnc_1", CallID: "call_1", Name: "lookup", CreatedAt: time.Now().Add(time.Millisecond)})

	if err := result.Expect.SkipNext(2).NoMoreEvents().HasError(); err != nil {
		t.Fatalf("NoMoreEvents returned error = %v, want nil after skipping all events", err)
	}
}

func TestRunAssertNoMoreEventsReportsRemainingEvents(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())
	result.RecordItem(&llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleAssistant, CreatedAt: time.Now()})

	err := result.Expect.NoMoreEvents().HasError()

	if err == nil {
		t.Fatal("NoMoreEvents error = nil, want error when events remain")
	}
}

func TestRunAssertSkipNextReportsOutOfRange(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())

	err := result.Expect.SkipNext(1).HasError()

	if err == nil {
		t.Fatal("SkipNext error = nil, want error when skipping past available events")
	}
}

func TestRunAssertNextEventAdvancesToNextRecordedEvent(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())
	result.RecordItem(&llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleAssistant, CreatedAt: time.Now()})

	if err := result.Expect.NextEvent().NoMoreEvents().HasError(); err != nil {
		t.Fatalf("NextEvent returned error = %v, want nil after advancing past one event", err)
	}
}

func TestRunAssertNextEventFiltersByType(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())
	now := time.Now()
	result.RecordItem(&llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleAssistant, CreatedAt: now})
	result.RecordItem(&llm.FunctionCall{ID: "fnc_1", CallID: "call_1", Name: "lookup", CreatedAt: now.Add(time.Millisecond)})

	if err := result.Expect.NextEvent("function_call").NoMoreEvents().HasError(); err != nil {
		t.Fatalf("NextEvent returned error = %v, want nil after finding function_call", err)
	}
}

func TestRunAssertNextEventReportsMissingType(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())
	result.RecordItem(&llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleAssistant, CreatedAt: time.Now()})

	err := result.Expect.NextEvent("function_call").HasError()

	if err == nil {
		t.Fatal("NextEvent error = nil, want error when type is not found")
	}
}

func TestRunAssertSkipNextEventIfConsumesMatchingCurrentEvent(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())
	result.RecordItem(&llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleAssistant, CreatedAt: time.Now()})

	if ok := result.Expect.SkipNextEventIf("message"); !ok {
		t.Fatal("SkipNextEventIf returned false, want true for matching current event")
	}
	if err := result.Expect.NoMoreEvents().HasError(); err != nil {
		t.Fatalf("NoMoreEvents returned error = %v, want nil after consuming matching event", err)
	}
}

func TestRunAssertSkipNextEventIfLeavesNonMatchingCurrentEvent(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())
	result.RecordItem(&llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleAssistant, CreatedAt: time.Now()})

	if ok := result.Expect.SkipNextEventIf("function_call"); ok {
		t.Fatal("SkipNextEventIf returned true, want false for non-matching current event")
	}
	if err := result.Expect.NextEvent("message").NoMoreEvents().HasError(); err != nil {
		t.Fatalf("NextEvent returned error = %v, want message to remain after non-match", err)
	}
}

func TestRunAssertSkipNextEventIfConsumesMatchingMessageRole(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())
	result.RecordItem(&llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleAssistant, CreatedAt: time.Now()})

	if ok := result.Expect.SkipNextEventIf("message", RunEventCriteria{Role: llm.ChatRoleAssistant}); !ok {
		t.Fatal("SkipNextEventIf returned false, want true for matching message role")
	}
	if err := result.Expect.NoMoreEvents().HasError(); err != nil {
		t.Fatalf("NoMoreEvents returned error = %v, want nil after consuming matching role", err)
	}
}

func TestRunAssertSkipNextEventIfLeavesNonMatchingFunctionCallName(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())
	result.RecordItem(&llm.FunctionCall{
		ID:        "fnc_1",
		CallID:    "call_1",
		Name:      "lookup",
		CreatedAt: time.Now(),
	})

	if ok := result.Expect.SkipNextEventIf("function_call", RunEventCriteria{Name: "search"}); ok {
		t.Fatal("SkipNextEventIf returned true, want false for non-matching function call name")
	}
	if err := result.Expect.NextEvent("function_call").NoMoreEvents().HasError(); err != nil {
		t.Fatalf("NextEvent returned error = %v, want function call to remain after non-match", err)
	}
}

func TestRunAssertSkipNextEventIfReturnsFalseWhenNoEventsRemain(t *testing.T) {
	result := NewRunResult(llm.NewChatContext())

	if ok := result.Expect.SkipNextEventIf("message"); ok {
		t.Fatal("SkipNextEventIf returned true, want false when no events remain")
	}
}
