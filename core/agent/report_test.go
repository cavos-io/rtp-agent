package agent

import (
	"errors"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestNewSessionReportUsesSessionRecordedEventsAndChatHistory(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{AllowInterruptions: true})
	session.ChatCtx.Append(&llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleUser})
	cause := errors.New("provider failed")
	session.EmitError(ErrorEvent{Error: cause, Source: "llm"})

	report := NewSessionReport(session)

	if report.Options.AllowInterruptions != session.Options.AllowInterruptions {
		t.Fatalf("report AllowInterruptions = %v, want %v", report.Options.AllowInterruptions, session.Options.AllowInterruptions)
	}
	if len(report.Events) != 1 {
		t.Fatalf("report Events length = %d, want 1", len(report.Events))
	}
	ev, ok := report.Events[0].(*ErrorEvent)
	if !ok {
		t.Fatalf("report Events[0] = %T, want *ErrorEvent", report.Events[0])
	}
	if !errors.Is(ev.Error, cause) || ev.Source != "llm" {
		t.Fatalf("report error event = %#v, want original error/source", ev)
	}
	if report.ChatHistory == nil {
		t.Fatal("report ChatHistory = nil, want copied chat context")
	}
	if len(report.ChatHistory.Items) != 1 || report.ChatHistory.Items[0].GetID() != "msg_1" {
		t.Fatalf("report ChatHistory items = %#v, want copied session chat history", report.ChatHistory.Items)
	}
}

func TestNewSessionReportCopiesSessionChatHistory(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.ChatCtx.Append(&llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleUser})

	report := NewSessionReport(session)
	session.ChatCtx.Append(&llm.ChatMessage{ID: "msg_2", Role: llm.ChatRoleAssistant})

	if len(report.ChatHistory.Items) != 1 {
		t.Fatalf("report ChatHistory length = %d, want copy unaffected by later session mutations", len(report.ChatHistory.Items))
	}
}

func TestNewSessionReportFromNilSessionReturnsEmptyReport(t *testing.T) {
	report := NewSessionReport(nil)

	if report == nil {
		t.Fatal("report = nil, want empty report")
	}
	if report.ChatHistory == nil {
		t.Fatal("report ChatHistory = nil, want empty chat context")
	}
	if len(report.Events) != 0 {
		t.Fatalf("report Events length = %d, want 0", len(report.Events))
	}
}
