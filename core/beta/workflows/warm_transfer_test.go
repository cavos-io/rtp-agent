package workflows

import (
	"context"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

func TestNewWarmTransferTaskBuildsInstructionsAndTools(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.Insert(&llm.ChatMessage{Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "I need billing help"}}})
	chatCtx.Insert(&llm.ChatMessage{Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "I can transfer you"}}})

	task := NewWarmTransferTask("+15550100", "trunk_123", chatCtx, "\nUse a concise handoff.")

	if task.TargetPhoneNumber != "+15550100" || task.SipTrunkID != "trunk_123" {
		t.Fatalf("task SIP fields = %#v/%#v, want target and trunk", task.TargetPhoneNumber, task.SipTrunkID)
	}
	if !strings.Contains(task.Instructions, "Caller: I need billing help") {
		t.Fatalf("instructions missing caller history: %q", task.Instructions)
	}
	if !strings.Contains(task.Instructions, "Assistant: I can transfer you") {
		t.Fatalf("instructions missing assistant history: %q", task.Instructions)
	}
	if !strings.Contains(task.Instructions, "Use a concise handoff.") {
		t.Fatalf("instructions missing extra guidance: %q", task.Instructions)
	}
	if len(task.Tools) != 3 {
		t.Fatalf("tools length = %d, want 3", len(task.Tools))
	}
}

func TestWarmTransferLifecycleCleansHumanAgentSession(t *testing.T) {
	task := NewWarmTransferTask("+15550100", "trunk_123", nil, "")
	task.humanAgentSess = agent.NewAgentSession(agent.NewAgent("human"), nil, agent.AgentSessionOptions{})

	task.OnEnter()
	if task.backgroundAudio == nil {
		t.Fatal("backgroundAudio is nil after OnEnter")
	}

	task.OnExit()
	if task.humanAgentSess != nil {
		t.Fatalf("humanAgentSess = %#v, want cleared on exit", task.humanAgentSess)
	}
}

func TestConnectToCallerCompletesWarmTransfer(t *testing.T) {
	task := NewWarmTransferTask("+15550100", "trunk_123", nil, "")
	task.callerRoom = &lksdk.Room{}
	task.humanAgentSess = agent.NewAgentSession(agent.NewAgent("human"), nil, agent.AgentSessionOptions{})

	if err := task.ConnectToCaller(); err != nil {
		t.Fatalf("ConnectToCaller returned error: %v", err)
	}

	result, err := task.WaitAny(context.Background())
	if err != nil {
		t.Fatalf("WaitAny error = %v, want result", err)
	}
	transfer, ok := result.(*WarmTransferResult)
	if !ok || transfer.HumanAgentIdentity != "human-agent-sip" {
		t.Fatalf("result = %#v, want warm transfer result", result)
	}
}

func TestWarmTransferToolsCompleteAndFailTask(t *testing.T) {
	connectTask := NewWarmTransferTask("+15550100", "trunk_123", nil, "")
	connect := &connectToCallerTool{task: connectTask}
	if connect.ID() != "connect_to_caller" || connect.Name() != "connect_to_caller" || connect.Description() == "" {
		t.Fatalf("connect tool metadata is incomplete")
	}
	if params := connect.Parameters(); params["type"] != "object" {
		t.Fatalf("connect parameters = %#v, want object schema", params)
	}
	if out, err := connect.Execute(context.Background(), `{}`); err != nil || out != "Connected to caller." {
		t.Fatalf("connect Execute = %q/%v, want connected output", out, err)
	}

	declineTask := NewWarmTransferTask("+15550100", "trunk_123", nil, "")
	decline := &declineTransferTool{task: declineTask}
	if decline.ID() != "decline_transfer" || decline.Name() != "decline_transfer" || decline.Description() == "" {
		t.Fatalf("decline tool metadata is incomplete")
	}
	if params := decline.Parameters(); params["type"] != "object" {
		t.Fatalf("decline parameters = %#v, want object schema", params)
	}
	if out, err := decline.Execute(context.Background(), `{"reason":"busy"}`); err != nil || out != "Transfer declined." {
		t.Fatalf("decline Execute = %q/%v, want declined output", out, err)
	}
	if _, err := declineTask.WaitAny(context.Background()); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("decline task error = %v, want busy reason", err)
	}
	if _, err := decline.Execute(context.Background(), `{`); err == nil {
		t.Fatal("decline Execute with invalid JSON returned nil error")
	}

	voicemailTask := NewWarmTransferTask("+15550100", "trunk_123", nil, "")
	voicemail := &voicemailDetectedTool{task: voicemailTask}
	if voicemail.ID() != "voicemail_detected" || voicemail.Name() != "voicemail_detected" || voicemail.Description() == "" {
		t.Fatalf("voicemail tool metadata is incomplete")
	}
	if params := voicemail.Parameters(); params["type"] != "object" {
		t.Fatalf("voicemail parameters = %#v, want object schema", params)
	}
	if out, err := voicemail.Execute(context.Background(), `{}`); err != nil || out != "Voicemail detected." {
		t.Fatalf("voicemail Execute = %q/%v, want voicemail output", out, err)
	}
	if _, err := voicemailTask.WaitAny(context.Background()); err == nil || !strings.Contains(err.Error(), "voicemail") {
		t.Fatalf("voicemail task error = %v, want voicemail error", err)
	}
}
