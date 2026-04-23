package agent

import (
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestSessionReport(t *testing.T) {
	report := NewSessionReport()
	
	report.JobID = "job-123"
	report.RoomID = "room-456"
	report.Room = "test-room"
	
	// Test AddEvent
	report.AddEvent("some raw event")
	if len(report.Events) != 1 {
		t.Errorf("Expected 1 event, got %d", len(report.Events))
	}
	
	// Test SetTimeline
	timeline := []*AgentEvent{
		{
			Type:      "user_state_changed",
			Timestamp: float64(time.Now().UnixNano()) / 1e9,
			UserStateChanged: &UserStateChangedEvent{
				OldState: UserStateListening,
				NewState: UserStateSpeaking,
			},
		},
	}
	report.SetTimeline(timeline)
	if len(report.Timeline) != 1 {
		t.Errorf("Expected 1 timeline event, got %d", len(report.Timeline))
	}
	
	// Test SetChatHistory
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.ChatMessage{
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "hello"}},
	})
	report.SetChatHistory(chatCtx)
	if len(report.ChatHistory.Items) != 1 {
		t.Errorf("Expected 1 chat item, got %d", len(report.ChatHistory.Items))
	}
	
	// Test ToDict
	dict := report.ToDict()
	if dict["job_id"] != "job-123" {
		t.Errorf("Expected job_id job-123, got %v", dict["job_id"])
	}
	
	events := dict["events"].([]map[string]any)
	if len(events) != 1 {
		t.Errorf("Expected 1 mapped event, got %d", len(events))
	}
}
