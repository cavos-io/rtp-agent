package agent

import (
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
