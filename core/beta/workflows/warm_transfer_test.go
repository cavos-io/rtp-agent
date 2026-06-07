package workflows

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/beta"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/livekit/protocol/livekit"
)

func TestNewWarmTransferTaskBuildsInstructionsAndTools(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.Insert(&llm.ChatMessage{Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "I need billing help"}}})
	chatCtx.Insert(&llm.ChatMessage{Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "I can transfer you"}}})

	task := newWarmTransferTaskForTest(t, "+15550100", "trunk_123", chatCtx, "\nUse a concise handoff.")

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

func TestNewWarmTransferTaskInstructionPartsCustomizePersonaAndExtra(t *testing.T) {
	customPersona := "You brief a licensed support specialist before joining the caller."
	task, err := NewWarmTransferTaskWithOptions(WarmTransferOptions{
		TargetPhone: "+15550100",
		TrunkID:     "trunk_123",
		Instructions: &beta.InstructionParts{
			Persona: &customPersona,
			Extra:   "Mention the caller prefers SMS follow-up.",
		},
	})
	if err != nil {
		t.Fatalf("NewWarmTransferTaskWithOptions() error = %v", err)
	}

	if !strings.Contains(task.Instructions, customPersona) {
		t.Fatalf("Instructions = %q, want custom persona", task.Instructions)
	}
	if strings.Contains(task.Instructions, "reaching out to a human agent for help") {
		t.Fatalf("Instructions = %q, want default persona replaced", task.Instructions)
	}
	if !strings.Contains(task.Instructions, "Mention the caller prefers SMS follow-up.") {
		t.Fatalf("Instructions = %q, want extra instructions appended", task.Instructions)
	}
}

func TestNewWarmTransferTaskInstructionPartsCanRemovePersona(t *testing.T) {
	emptyPersona := ""
	task, err := NewWarmTransferTaskWithOptions(WarmTransferOptions{
		TargetPhone:       "+15550100",
		TrunkID:           "trunk_123",
		Instructions:      &beta.InstructionParts{Persona: &emptyPersona},
		ChatContext:       nil,
		ExtraInstructions: "",
	})
	if err != nil {
		t.Fatalf("NewWarmTransferTaskWithOptions() error = %v", err)
	}

	if strings.Contains(task.Instructions, "reaching out to a human agent for help") {
		t.Fatalf("Instructions = %q, want default persona removed", task.Instructions)
	}
	if !strings.Contains(task.Instructions, "connect_to_caller") {
		t.Fatalf("Instructions = %q, want transfer guidance preserved", task.Instructions)
	}
}

func TestNewWarmTransferTaskUsesExplicitSIPNumberOption(t *testing.T) {
	t.Setenv("LIVEKIT_SIP_NUMBER", "+15550000")

	task, err := NewWarmTransferTaskWithOptions(WarmTransferOptions{
		TargetPhone: "+15550100",
		TrunkID:     "trunk_123",
		SipNumber:   "+15550999",
	})
	if err != nil {
		t.Fatalf("NewWarmTransferTaskWithOptions() error = %v", err)
	}

	if task.SipNumber != "+15550999" {
		t.Fatalf("SipNumber = %q, want explicit option", task.SipNumber)
	}
}

func TestNewWarmTransferTaskAllowsExplicitSIPConnectionWithoutTrunk(t *testing.T) {
	t.Setenv("LIVEKIT_SIP_OUTBOUND_TRUNK", "")

	connection := &livekit.SIPOutboundConfig{
		Hostname:     "sip.example.com",
		AuthUsername: "agent",
		AuthPassword: "secret",
	}
	task, err := NewWarmTransferTaskWithOptions(WarmTransferOptions{
		TargetPhone:   "+15550100",
		SipConnection: connection,
	})
	if err != nil {
		t.Fatalf("NewWarmTransferTaskWithOptions() error = %v", err)
	}

	if task.SipTrunkID != "" {
		t.Fatalf("SipTrunkID = %q, want empty when explicit SIP connection is used", task.SipTrunkID)
	}
	if task.SipConnection != connection {
		t.Fatalf("SipConnection = %#v, want explicit connection", task.SipConnection)
	}
}

func TestNewWarmTransferTaskRejectsMissingSIPConfig(t *testing.T) {
	t.Setenv("LIVEKIT_SIP_OUTBOUND_TRUNK", "")

	if _, err := NewWarmTransferTask("", "trunk_123", nil, ""); err == nil {
		t.Fatal("NewWarmTransferTask() error = nil, want missing sip_call_to error")
	}
	if _, err := NewWarmTransferTask("+15550100", "", nil, ""); err == nil {
		t.Fatal("NewWarmTransferTask() error = nil, want missing outbound trunk error")
	}
}

func TestWarmTransferLifecycleCleansHumanAgentSession(t *testing.T) {
	task := newWarmTransferTaskForTest(t, "+15550100", "trunk_123", nil, "")
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
	task := newWarmTransferTaskForTest(t, "+15550100", "trunk_123", nil, "")
	task.SipNumber = "+15550999"
	task.SipHeaders = map[string]string{"X-Trace": "trace-a"}
	task.Dtmf = "ww1234#"
	task.RingingTimeout = 7 * time.Second
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
	if jobCtx.createSIPRequest.Dtmf != "ww1234#" {
		t.Fatalf("CreateSIPParticipant Dtmf = %q, want ww1234#", jobCtx.createSIPRequest.Dtmf)
	}
	if jobCtx.createSIPRequest.RingingTimeout == nil || jobCtx.createSIPRequest.RingingTimeout.AsDuration() != 7*time.Second {
		t.Fatalf("CreateSIPParticipant RingingTimeout = %v, want 7s", jobCtx.createSIPRequest.RingingTimeout)
	}
}

func TestWarmTransferOnEnterUsesExplicitSIPConnection(t *testing.T) {
	t.Setenv("LIVEKIT_SIP_OUTBOUND_TRUNK", "trunk-env")

	connection := &livekit.SIPOutboundConfig{
		Hostname:           "sip.example.com",
		DestinationCountry: "US",
		AuthUsername:       "agent",
		AuthPassword:       "secret",
	}
	task, err := NewWarmTransferTaskWithOptions(WarmTransferOptions{
		TargetPhone:   "+15550100",
		SipConnection: connection,
	})
	if err != nil {
		t.Fatalf("NewWarmTransferTaskWithOptions() error = %v", err)
	}
	jobCtx := &fakeWarmTransferJobContext{room: &livekit.Room{Name: "caller-room"}}
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	session.SetJobContext(jobCtx)
	task.Agent.Start(session, task)
	defer task.Agent.GetActivity().Stop()

	task.OnEnter()

	if jobCtx.createSIPRequest == nil {
		t.Fatal("OnEnter did not create SIP participant")
	}
	if jobCtx.createSIPRequest.SipTrunkId != "" {
		t.Fatalf("CreateSIPParticipant SipTrunkId = %q, want empty with explicit SIP connection", jobCtx.createSIPRequest.SipTrunkId)
	}
	if jobCtx.createSIPRequest.Trunk == nil {
		t.Fatal("CreateSIPParticipant Trunk = nil, want explicit SIP connection")
	}
	if jobCtx.createSIPRequest.Trunk == connection {
		t.Fatal("CreateSIPParticipant Trunk aliases input connection, want copied SIP connection")
	}
	if jobCtx.createSIPRequest.Trunk.GetHostname() != "sip.example.com" ||
		jobCtx.createSIPRequest.Trunk.GetDestinationCountry() != "US" ||
		jobCtx.createSIPRequest.Trunk.GetAuthUsername() != "agent" ||
		jobCtx.createSIPRequest.Trunk.GetAuthPassword() != "secret" {
		t.Fatalf("CreateSIPParticipant Trunk = %#v, want explicit SIP connection copied", jobCtx.createSIPRequest.Trunk)
	}
}

func TestConnectToCallerCompletesWarmTransfer(t *testing.T) {
	task := newWarmTransferTaskForTest(t, "+15550100", "trunk_123", nil, "")
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
	connectTask := newWarmTransferTaskForTest(t, "+15550100", "trunk_123", nil, "")
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

	declineTask := newWarmTransferTaskForTest(t, "+15550100", "trunk_123", nil, "")
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

	voicemailTask := newWarmTransferTaskForTest(t, "+15550100", "trunk_123", nil, "")
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

func newWarmTransferTaskForTest(t *testing.T, targetPhone string, trunkID string, chatCtx *llm.ChatContext, extraInstructions string) *WarmTransferTask {
	t.Helper()

	task, err := NewWarmTransferTask(targetPhone, trunkID, chatCtx, extraInstructions)
	if err != nil {
		t.Fatalf("NewWarmTransferTask() error = %v", err)
	}
	return task
}
