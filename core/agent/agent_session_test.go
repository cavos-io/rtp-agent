package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/cavos-io/conversation-worker/core/llm"
)

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
}
