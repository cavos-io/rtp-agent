package agent

import (
	"errors"
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
