package agent

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
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

func TestSessionReportToDictSkipsMetricsCollectedEvents(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{AllowInterruptions: true})
	session.ChatCtx.Append(&llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleUser})
	session.EmitMetricsCollected(&telemetry.LLMMetrics{RequestID: "metrics_req", PromptTokens: 7})
	session.EmitError(ErrorEvent{Error: errors.New("failed"), Source: "llm"})

	data := NewSessionReport(session).ToDict()

	events, ok := data["events"].([]map[string]any)
	if !ok {
		t.Fatalf("events = %T, want []map[string]any", data["events"])
	}
	for _, event := range events {
		if event["type"] == "metrics_collected" {
			t.Fatalf("events contained metrics_collected: %#v", events)
		}
	}
	if len(events) == 0 {
		t.Fatal("events length = 0, want non-metric events preserved")
	}
	options, ok := data["options"].(map[string]any)
	if !ok {
		t.Fatalf("options = %T, want map[string]any", data["options"])
	}
	if options["allow_interruptions"] != true {
		t.Fatalf("allow_interruptions = %#v, want true", options["allow_interruptions"])
	}
	if _, ok := options["min_endpointing_delay"]; !ok {
		t.Fatalf("options missing min_endpointing_delay: %#v", options)
	}
	chatHistory, ok := data["chat_history"].(map[string]any)
	if !ok {
		t.Fatalf("chat_history = %T, want map[string]any", data["chat_history"])
	}
	items, ok := chatHistory["items"].([]map[string]any)
	if !ok || len(items) != 1 {
		t.Fatalf("chat_history items = %#v, want one serialized item", chatHistory["items"])
	}
	if _, ok := items[0]["created_at"]; !ok {
		t.Fatalf("chat_history item missing created_at: %#v", items[0])
	}
	usage, ok := data["usage"].([]map[string]any)
	if !ok || len(usage) != 1 {
		t.Fatalf("usage = %#v, want one usage summary", data["usage"])
	}
	if usage[0]["llm_prompt_tokens"] != 7 {
		t.Fatalf("usage llm_prompt_tokens = %#v, want 7", usage[0]["llm_prompt_tokens"])
	}
	if _, ok := usage[0]["llm_completion_tokens"]; ok {
		t.Fatalf("usage llm_completion_tokens present with zero value: %#v", usage[0])
	}
	if data["sdk_version"] != defaultSessionReportSDKVersion {
		t.Fatalf("sdk_version = %#v, want default report SDK version", data["sdk_version"])
	}

	encoded, err := json.Marshal(NewSessionReport(session))
	if err != nil {
		t.Fatalf("Marshal session report: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal session report JSON: %v", err)
	}
	encodedEvents, ok := decoded["events"].([]any)
	if !ok {
		t.Fatalf("encoded events = %T, want []any", decoded["events"])
	}
	for _, event := range encodedEvents {
		eventMap, ok := event.(map[string]any)
		if !ok {
			t.Fatalf("encoded event = %T, want map[string]any", event)
		}
		if eventMap["type"] == "metrics_collected" {
			t.Fatalf("encoded events contained metrics_collected: %#v", encodedEvents)
		}
	}
}
