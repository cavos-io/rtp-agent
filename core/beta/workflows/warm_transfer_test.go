package workflows

import (
	"context"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/livekit/protocol/livekit"
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

func TestWarmTransferOnEnterDialsHumanAgentSIPParticipant(t *testing.T) {
	task := NewWarmTransferTask("+15550100", "trunk_123", nil, "")
	task.SipNumber = "+15550999"
	task.SipHeaders = map[string]string{"X-Trace": "trace-a"}
	jobCtx := &fakeWarmTransferJobContext{room: &livekit.Room{Name: "caller-room"}}
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	session.SetJobContext(jobCtx)
	task.Agent.Start(session, task)
	defer task.Agent.GetActivity().Stop()

	task.OnEnter()

	if jobCtx.createSIPRequest == nil {
		t.Fatal("OnEnter did not create SIP participant")
	}
	if jobCtx.createSIPRequest.RoomName != "caller-room-human-agent" {
		t.Fatalf("CreateSIPParticipant RoomName = %q, want caller-room-human-agent", jobCtx.createSIPRequest.RoomName)
	}
	if jobCtx.createSIPRequest.ParticipantIdentity != "human-agent-sip" {
		t.Fatalf("CreateSIPParticipant ParticipantIdentity = %q, want human-agent-sip", jobCtx.createSIPRequest.ParticipantIdentity)
	}
	if jobCtx.createSIPRequest.SipTrunkId != "trunk_123" {
		t.Fatalf("CreateSIPParticipant SipTrunkId = %q, want trunk_123", jobCtx.createSIPRequest.SipTrunkId)
	}
	if jobCtx.createSIPRequest.SipCallTo != "+15550100" {
		t.Fatalf("CreateSIPParticipant SipCallTo = %q, want +15550100", jobCtx.createSIPRequest.SipCallTo)
	}
	if !jobCtx.createSIPRequest.WaitUntilAnswered {
		t.Fatal("CreateSIPParticipant WaitUntilAnswered = false, want true")
	}
	if jobCtx.createSIPRequest.SipNumber != "+15550999" {
		t.Fatalf("CreateSIPParticipant SipNumber = %q, want +15550999", jobCtx.createSIPRequest.SipNumber)
	}
	if jobCtx.createSIPRequest.Headers["X-Trace"] != "trace-a" {
		t.Fatalf("CreateSIPParticipant Headers = %#v, want X-Trace", jobCtx.createSIPRequest.Headers)
	}
}

func TestConnectToCallerCompletesWarmTransfer(t *testing.T) {
	task := NewWarmTransferTask("+15550100", "trunk_123", nil, "")
	task.humanAgentSess = agent.NewAgentSession(agent.NewAgent("human"), nil, agent.AgentSessionOptions{})
	jobCtx := &fakeWarmTransferJobContext{room: &livekit.Room{Name: "caller-room"}}
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	session.SetJobContext(jobCtx)
	task.Agent.Start(session, task)
	defer task.Agent.GetActivity().Stop()

	if err := task.ConnectToCaller(); err != nil {
		t.Fatalf("ConnectToCaller returned error: %v", err)
	}

	if jobCtx.moveRequest == nil {
		t.Fatal("ConnectToCaller did not move human participant")
	}
	if jobCtx.moveRequest.Room != "caller-room-human-agent" {
		t.Fatalf("MoveParticipant room = %q, want caller-room-human-agent", jobCtx.moveRequest.Room)
	}
	if jobCtx.moveRequest.Identity != "human-agent-sip" {
		t.Fatalf("MoveParticipant identity = %q, want human-agent-sip", jobCtx.moveRequest.Identity)
	}
	if jobCtx.moveRequest.DestinationRoom != "caller-room" {
		t.Fatalf("MoveParticipant destination = %q, want caller-room", jobCtx.moveRequest.DestinationRoom)
	}

	result, err := task.WaitAny(context.Background())
	if err != nil {
		t.Fatalf("WaitAny error = %v, want result", err)
	}
	transfer, ok := result.(*WarmTransferResult)
	if !ok || transfer.HumanAgentIdentity != "human-agent-sip" {
		t.Fatalf("result = %#v, want warm transfer result", result)
	}
	if task.humanAgentSess != nil {
		t.Fatalf("humanAgentSess = %#v, want cleared after connect result", task.humanAgentSess)
	}
}

type fakeWarmTransferJobContext struct {
	room             *livekit.Room
	moveRequest      *livekit.MoveParticipantRequest
	createSIPRequest *livekit.CreateSIPParticipantRequest
}

func (f *fakeWarmTransferJobContext) RoomInfo() *livekit.Room {
	return f.room
}

func (f *fakeWarmTransferJobContext) MoveParticipant(_ context.Context, room string, identity string, destinationRoom string) error {
	f.moveRequest = &livekit.MoveParticipantRequest{
		Room:            room,
		Identity:        identity,
		DestinationRoom: destinationRoom,
	}
	return nil
}

func (f *fakeWarmTransferJobContext) CreateSIPParticipant(_ context.Context, req *livekit.CreateSIPParticipantRequest) (*livekit.SIPParticipantInfo, error) {
	f.createSIPRequest = req
	return &livekit.SIPParticipantInfo{}, nil
}

func TestWarmTransferToolsCompleteAndFailTask(t *testing.T) {
	connectTask := NewWarmTransferTask("+15550100", "trunk_123", nil, "")
	connectTask.humanAgentSess = agent.NewAgentSession(agent.NewAgent("human"), nil, agent.AgentSessionOptions{})
	connectJobCtx := &fakeWarmTransferJobContext{room: &livekit.Room{Name: "caller-room"}}
	connectSession := agent.NewAgentSession(connectTask, nil, agent.AgentSessionOptions{})
	connectSession.SetJobContext(connectJobCtx)
	connectTask.Agent.Start(connectSession, connectTask)
	defer connectTask.Agent.GetActivity().Stop()
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
	if connectJobCtx.moveRequest == nil {
		t.Fatal("connect Execute did not move participant")
	}
	if connectTask.humanAgentSess != nil {
		t.Fatalf("humanAgentSess = %#v, want cleared after connect tool result", connectTask.humanAgentSess)
	}

	declineTask := NewWarmTransferTask("+15550100", "trunk_123", nil, "")
	declineTask.humanAgentSess = agent.NewAgentSession(agent.NewAgent("human"), nil, agent.AgentSessionOptions{})
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
	if declineTask.humanAgentSess != nil {
		t.Fatalf("humanAgentSess = %#v, want cleared after decline result", declineTask.humanAgentSess)
	}
	if _, err := decline.Execute(context.Background(), `{`); err == nil {
		t.Fatal("decline Execute with invalid JSON returned nil error")
	}

	voicemailTask := NewWarmTransferTask("+15550100", "trunk_123", nil, "")
	voicemailTask.humanAgentSess = agent.NewAgentSession(agent.NewAgent("human"), nil, agent.AgentSessionOptions{})
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
	if voicemailTask.humanAgentSess != nil {
		t.Fatalf("humanAgentSess = %#v, want cleared after voicemail result", voicemailTask.humanAgentSess)
	}
}
