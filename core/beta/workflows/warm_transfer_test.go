package workflows

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/beta"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
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

func TestNewWarmTransferTaskPreservesReferenceExtraTools(t *testing.T) {
	opts := WarmTransferOptions{
		TargetPhone: "+15550100",
		TrunkID:     "trunk_123",
	}
	field := reflect.ValueOf(&opts).Elem().FieldByName("Tools")
	if !field.IsValid() {
		t.Fatal("WarmTransferOptions.Tools missing; want reference tools constructor option")
	}
	field.Set(reflect.ValueOf([]llm.Tool{referenceWarmTransferExtraTool{id: "handoff_help"}}))

	task, err := NewWarmTransferTaskWithOptions(opts)
	if err != nil {
		t.Fatalf("NewWarmTransferTaskWithOptions() error = %v", err)
	}

	if len(task.Agent.Tools) < 4 {
		t.Fatalf("tools len = %d, want extra tool before warm-transfer control tools", len(task.Agent.Tools))
	}
	if got := task.Agent.Tools[0].Name(); got != "handoff_help" {
		t.Fatalf("tools[0] = %q, want caller-provided tool preserved first", got)
	}
	if got := task.Agent.Tools[1].Name(); got != "connect_to_caller" {
		t.Fatalf("tools[1] = %q, want connect_to_caller after caller tools", got)
	}
}

func TestNewWarmTransferTaskSkipsEmptyConversationHistoryMessages(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.Insert(&llm.ChatMessage{Role: llm.ChatRoleUser})
	chatCtx.Insert(&llm.ChatMessage{Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: ""}}})
	chatCtx.Insert(&llm.ChatMessage{Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "Need help with billing"}}})

	task := newWarmTransferTaskForTest(t, "+15550100", "trunk_123", chatCtx, "")

	if strings.Contains(task.Instructions, "Caller: \n") || strings.Contains(task.Instructions, "Assistant: \n") {
		t.Fatalf("Instructions = %q, want empty chat messages omitted from conversation history", task.Instructions)
	}
	if !strings.Contains(task.Instructions, "Caller: Need help with billing\n") {
		t.Fatalf("Instructions = %q, want non-empty caller history preserved", task.Instructions)
	}
}

func TestNewWarmTransferTaskUsesReferenceHandoffPromptOrder(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.Insert(&llm.ChatMessage{Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "My claim was denied"}}})

	task := newWarmTransferTaskForTest(t, "+15550100", "trunk_123", chatCtx, "")

	historyIndex := strings.Index(task.Instructions, "## Conversation history with caller")
	callerIndex := strings.Index(task.Instructions, "Caller: My claim was denied\n")
	endHistoryIndex := strings.Index(task.Instructions, "## End of conversation history with caller")
	connectIndex := strings.Index(task.Instructions, "Once the human agent has confirmed, you should call the tool `connect_to_caller` to connect them to the caller.")
	summaryIndex := strings.Index(task.Instructions, "You are talking to the human agent now, start by giving them a summary of the conversation so far, and answer any questions they might have.")
	if historyIndex < 0 || callerIndex < 0 || endHistoryIndex < 0 || connectIndex < 0 || summaryIndex < 0 {
		t.Fatalf("Instructions = %q, want reference handoff prompt sections", task.Instructions)
	}
	if !(historyIndex < callerIndex && callerIndex < endHistoryIndex && endHistoryIndex < connectIndex && connectIndex < summaryIndex) {
		t.Fatalf("handoff prompt order history=%d caller=%d end=%d connect=%d summary=%d, want history before connect before summary", historyIndex, callerIndex, endHistoryIndex, connectIndex, summaryIndex)
	}
}

func TestNewWarmTransferTaskSeparatesExtraInstructions(t *testing.T) {
	task := newWarmTransferTaskForTest(t, "+15550100", "trunk_123", nil, "Keep the summary under two sentences.")

	want := "answer any questions they might have.\n\nKeep the summary under two sentences."
	if !strings.Contains(task.Instructions, want) {
		t.Fatalf("Instructions = %q, want extra instructions separated by blank line", task.Instructions)
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

func TestNewWarmTransferTaskPreservesExplicitEmptySIPNumber(t *testing.T) {
	t.Setenv("LIVEKIT_SIP_NUMBER", "+15550000")

	opts := WarmTransferOptions{
		TargetPhone: "+15550100",
		TrunkID:     "trunk_123",
		SipNumber:   "",
	}
	field := reflect.ValueOf(&opts).Elem().FieldByName("SipNumberSet")
	if !field.IsValid() {
		t.Fatal("WarmTransferOptions.SipNumberSet missing; want reference explicit empty sip_number support")
	}
	field.SetBool(true)

	task, err := NewWarmTransferTaskWithOptions(opts)
	if err != nil {
		t.Fatalf("NewWarmTransferTaskWithOptions() error = %v", err)
	}
	if task.SipNumber != "" {
		t.Fatalf("SipNumber = %q, want explicit empty option without env fallback", task.SipNumber)
	}
}

func TestNewWarmTransferTaskPreservesExplicitEmptySIPTrunkID(t *testing.T) {
	t.Setenv("LIVEKIT_SIP_OUTBOUND_TRUNK", "trunk_env")

	opts := WarmTransferOptions{
		TargetPhone: "+15550100",
		TrunkID:     "",
	}
	field := reflect.ValueOf(&opts).Elem().FieldByName("TrunkIDSet")
	if !field.IsValid() {
		t.Fatal("WarmTransferOptions.TrunkIDSet missing; want reference explicit empty sip_trunk_id support")
	}
	field.SetBool(true)

	task, err := NewWarmTransferTaskWithOptions(opts)
	if err != nil {
		t.Fatalf("NewWarmTransferTaskWithOptions() error = %v", err)
	}
	if task.SipTrunkID != "" {
		t.Fatalf("SipTrunkID = %q, want explicit empty option without env fallback", task.SipTrunkID)
	}
}

func TestNewWarmTransferTaskUsesReferenceTargetPhoneNumberAlias(t *testing.T) {
	opts := WarmTransferOptions{TrunkID: "trunk_123"}
	field := reflect.ValueOf(&opts).Elem().FieldByName("TargetPhoneNumber")
	if !field.IsValid() {
		t.Fatal("WarmTransferOptions.TargetPhoneNumber missing; want deprecated reference alias")
	}
	field.SetString("+15550100")

	task, err := NewWarmTransferTaskWithOptions(opts)
	if err != nil {
		t.Fatalf("NewWarmTransferTaskWithOptions() error = %v", err)
	}
	if task.TargetPhoneNumber != "+15550100" {
		t.Fatalf("TargetPhoneNumber = %q, want deprecated alias value", task.TargetPhoneNumber)
	}
}

func TestNewWarmTransferTaskUsesReferenceSIPRequestOptions(t *testing.T) {
	opts := WarmTransferOptions{
		TargetPhone: "+15550100",
		TrunkID:     "trunk_123",
	}
	values := reflect.ValueOf(&opts).Elem()
	for name, value := range map[string]any{
		"SipHeaders":     map[string]string{"X-Trace": "trace-a"},
		"Dtmf":           "ww1234#",
		"RingingTimeout": 7 * time.Second,
	} {
		field := values.FieldByName(name)
		if !field.IsValid() {
			t.Fatalf("WarmTransferOptions.%s missing; want reference constructor option", name)
		}
		field.Set(reflect.ValueOf(value))
	}

	task, err := NewWarmTransferTaskWithOptions(opts)
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
	if jobCtx.createSIPRequest.Headers["X-Trace"] != "trace-a" {
		t.Fatalf("CreateSIPParticipant Headers = %#v, want constructor headers", jobCtx.createSIPRequest.Headers)
	}
	if jobCtx.createSIPRequest.Dtmf != "ww1234#" {
		t.Fatalf("CreateSIPParticipant Dtmf = %q, want constructor dtmf", jobCtx.createSIPRequest.Dtmf)
	}
	if jobCtx.createSIPRequest.RingingTimeout == nil || jobCtx.createSIPRequest.RingingTimeout.AsDuration() != 7*time.Second {
		t.Fatalf("CreateSIPParticipant RingingTimeout = %v, want constructor timeout 7s", jobCtx.createSIPRequest.RingingTimeout)
	}
}

func TestNewWarmTransferTaskPreservesExplicitZeroRingingTimeout(t *testing.T) {
	opts := WarmTransferOptions{
		TargetPhone:    "+15550100",
		TrunkID:        "trunk_123",
		RingingTimeout: 0,
	}
	field := reflect.ValueOf(&opts).Elem().FieldByName("RingingTimeoutSet")
	if !field.IsValid() {
		t.Fatal("WarmTransferOptions.RingingTimeoutSet missing; want reference explicit zero ringing_timeout support")
	}
	field.SetBool(true)

	task, err := NewWarmTransferTaskWithOptions(opts)
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
	if jobCtx.createSIPRequest.RingingTimeout == nil {
		t.Fatal("CreateSIPParticipant RingingTimeout = nil, want explicit zero duration")
	}
	if got := jobCtx.createSIPRequest.RingingTimeout.AsDuration(); got != 0 {
		t.Fatalf("CreateSIPParticipant RingingTimeout = %v, want explicit zero duration", got)
	}
}

func TestNewWarmTransferTaskUsesReferenceDefaultHoldAudio(t *testing.T) {
	task := newWarmTransferTaskForTest(t, "+15550100", "trunk_123", nil, "")

	hold, ok := task.HoldAudio.(agent.AudioConfig)
	if !ok {
		t.Fatalf("HoldAudio = %T, want agent.AudioConfig", task.HoldAudio)
	}
	if hold.Source != agent.HoldMusic {
		t.Fatalf("HoldAudio.Source = %#v, want HoldMusic", hold.Source)
	}
	if hold.Volume != 0.8 {
		t.Fatalf("HoldAudio.Volume = %v, want 0.8", hold.Volume)
	}
}

func TestNewWarmTransferTaskAllowsCustomHoldAudio(t *testing.T) {
	custom := agent.AudioConfig{
		Source: "custom-hold.ogg",
		Volume: 0.4,
	}
	task, err := NewWarmTransferTaskWithOptions(WarmTransferOptions{
		TargetPhone: "+15550100",
		TrunkID:     "trunk_123",
		HoldAudio:   custom,
	})
	if err != nil {
		t.Fatalf("NewWarmTransferTaskWithOptions() error = %v", err)
	}

	if task.HoldAudio != custom {
		t.Fatalf("HoldAudio = %#v, want custom hold audio", task.HoldAudio)
	}
}

func TestNewWarmTransferTaskCanDisableHoldAudio(t *testing.T) {
	task, err := NewWarmTransferTaskWithOptions(WarmTransferOptions{
		TargetPhone:      "+15550100",
		TrunkID:          "trunk_123",
		DisableHoldAudio: true,
	})
	if err != nil {
		t.Fatalf("NewWarmTransferTaskWithOptions() error = %v", err)
	}

	if task.HoldAudio != nil {
		t.Fatalf("HoldAudio = %#v, want nil when hold audio is disabled", task.HoldAudio)
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
	jobCtx := &fakeWarmTransferJobContext{room: &livekit.Room{Name: "caller-room"}}
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	session.SetJobContext(jobCtx)
	task.Agent.Start(session, task)
	defer task.Agent.GetActivity().Stop()

	if task.backgroundAudio == nil {
		t.Fatal("backgroundAudio is nil after OnEnter")
	}

	task.OnExit()
	if task.humanAgentSess != nil {
		t.Fatalf("humanAgentSess = %#v, want cleared on exit", task.humanAgentSess)
	}
}

func TestWarmTransferOnEnterSkipsBackgroundAudioWhenHoldAudioDisabled(t *testing.T) {
	task, err := NewWarmTransferTaskWithOptions(WarmTransferOptions{
		TargetPhone:      "+15550100",
		TrunkID:          "trunk_123",
		DisableHoldAudio: true,
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

	if task.backgroundAudio != nil {
		t.Fatalf("backgroundAudio = %#v, want nil when hold audio is disabled", task.backgroundAudio)
	}
}

func TestWarmTransferOnEnterStartsReferenceHoldAudio(t *testing.T) {
	holdAudio := agent.AudioConfig{Source: "hold.wav", Volume: 0.5}
	task, err := NewWarmTransferTaskWithOptions(WarmTransferOptions{
		TargetPhone: "+15550100",
		TrunkID:     "trunk_123",
		HoldAudio:   holdAudio,
	})
	if err != nil {
		t.Fatalf("NewWarmTransferTaskWithOptions() error = %v", err)
	}
	player := &fakeWarmTransferBackgroundAudio{handle: &fakeWarmTransferHoldAudioHandle{}}
	task.newBackgroundAudioPlayer = func(audio interface{}) warmTransferBackgroundAudio {
		if !reflect.DeepEqual(audio, holdAudio) {
			t.Fatalf("newBackgroundAudioPlayer audio = %#v, want hold audio", audio)
		}
		return player
	}
	jobCtx := &fakeWarmTransferJobContext{room: &livekit.Room{Name: "caller-room"}}
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	room := &lksdk.Room{}
	session.Room = room
	session.SetJobContext(jobCtx)
	task.Agent.Start(session, task)
	defer task.Agent.GetActivity().Stop()

	if player.startRoom != room {
		t.Fatalf("hold audio Start room = %#v, want caller room", player.startRoom)
	}
	if player.startSession != session {
		t.Fatalf("hold audio Start session = %#v, want caller session", player.startSession)
	}
	if !reflect.DeepEqual(player.playAudio, holdAudio) {
		t.Fatalf("hold audio Play audio = %#v, want hold audio", player.playAudio)
	}
	if !player.playLoop {
		t.Fatal("hold audio Play loop = false, want true")
	}
	if task.holdAudioHandle != player.handle {
		t.Fatalf("holdAudioHandle = %#v, want play handle", task.holdAudioHandle)
	}

	task.OnExit()
	if !player.handle.stopped {
		t.Fatal("hold audio handle stopped = false after exit")
	}
	if !player.closed {
		t.Fatal("background audio closed = false after exit")
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

func TestWarmTransferOnEnterReadiesHumanAgentForConnect(t *testing.T) {
	task := newWarmTransferTaskForTest(t, "+15550100", "trunk_123", nil, "")
	jobCtx := &fakeWarmTransferJobContext{room: &livekit.Room{Name: "caller-room"}}
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	session.SetJobContext(jobCtx)
	task.Agent.Start(session, task)
	defer task.Agent.GetActivity().Stop()

	if err := task.ConnectToCaller(); err != nil {
		t.Fatalf("ConnectToCaller after answered dial returned error: %v", err)
	}
	if jobCtx.moveRequest == nil {
		t.Fatal("ConnectToCaller did not move answered human participant")
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
}

func TestWarmTransferOnEnterDialFailureCleansResources(t *testing.T) {
	task := newWarmTransferTaskForTest(t, "+15550100", "trunk_123", nil, "")
	task.humanAgentSess = agent.NewAgentSession(agent.NewAgent("human"), nil, agent.AgentSessionOptions{})
	jobCtx := &fakeWarmTransferJobContext{
		room:      &livekit.Room{Name: "caller-room"},
		createErr: errors.New("sip unavailable"),
	}
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	session.SetJobContext(jobCtx)
	task.Agent.Start(session, task)
	defer task.Agent.GetActivity().Stop()

	if _, err := task.WaitAny(context.Background()); err == nil {
		t.Fatal("WaitAny() error = nil, want warm transfer ToolError")
	} else {
		var toolErr llm.ToolError
		if !errors.As(err, &toolErr) {
			t.Fatalf("WaitAny() error = %T %v, want ToolError", err, err)
		}
		if toolErr.Message != "could not dial human agent" {
			t.Fatalf("ToolError.Message = %q, want reference dial failure", toolErr.Message)
		}
	}
	if task.backgroundAudio != nil {
		t.Fatalf("backgroundAudio = %#v, want cleared after failed dial", task.backgroundAudio)
	}
	if task.humanAgentSess != nil {
		t.Fatalf("humanAgentSess = %#v, want cleared after failed dial", task.humanAgentSess)
	}
}

func TestWarmTransferHumanAgentRoomCloseFailsAndRestoresCaller(t *testing.T) {
	task := newWarmTransferTaskForTest(t, "+15550100", "trunk_123", nil, "")
	player := &fakeWarmTransferBackgroundAudio{handle: &fakeWarmTransferHoldAudioHandle{}}
	task.newBackgroundAudioPlayer = func(audio interface{}) warmTransferBackgroundAudio {
		return player
	}
	humanSession := agent.NewAgentSession(agent.NewAgent("human"), nil, agent.AgentSessionOptions{})
	task.humanAgentSess = humanSession
	jobCtx := &fakeWarmTransferJobContext{room: &livekit.Room{Name: "caller-room"}}
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	session.Room = &lksdk.Room{}
	audioOutput := &fakeWarmTransferAudioOutputController{canPause: true}
	session.SetAudioOutputController(audioOutput)
	session.SetJobContext(jobCtx)
	task.Agent.Start(session, task)
	defer task.Agent.GetActivity().Stop()

	if !session.InputAudioMuted() {
		t.Fatal("caller input audio muted = false after OnEnter, want muted while human-agent room is active")
	}

	humanSession.EmitEvent(&agent.CloseEvent{Reason: agent.CloseReasonUserInitiated})

	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := task.WaitAny(waitCtx); err == nil {
		t.Fatal("WaitAny() error = nil, want room closed ToolError")
	} else {
		var toolErr llm.ToolError
		if !errors.As(err, &toolErr) {
			t.Fatalf("WaitAny() error = %T %v, want ToolError", err, err)
		}
		if toolErr.Message != "room closed: user_initiated" {
			t.Fatalf("ToolError.Message = %q, want room closed reason", toolErr.Message)
		}
	}
	if session.InputAudioMuted() {
		t.Fatal("caller input audio muted = true after human-agent room close, want restored")
	}
	if audioOutput.resumeCount != 1 {
		t.Fatalf("ResumeAudioOutput calls = %d, want 1 after human-agent room close", audioOutput.resumeCount)
	}
	if !player.handle.stopped {
		t.Fatal("hold audio stopped = false after human-agent room close")
	}
	if task.backgroundAudio != nil {
		t.Fatalf("backgroundAudio = %#v, want cleared after human-agent room close", task.backgroundAudio)
	}
	if task.humanAgentReady {
		t.Fatal("humanAgentReady = true after human-agent room close, want false")
	}
}

func TestWarmTransferOnEnterMutesCallerInputAudioAndRestoresOnResult(t *testing.T) {
	task := newWarmTransferTaskForTest(t, "+15550100", "trunk_123", nil, "")
	jobCtx := &fakeWarmTransferJobContext{room: &livekit.Room{Name: "caller-room"}}
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	session.SetJobContext(jobCtx)
	task.Agent.Start(session, task)
	defer task.Agent.GetActivity().Stop()

	task.OnEnter()

	if !session.InputAudioMuted() {
		t.Fatal("caller input audio muted = false after OnEnter, want true while warm transfer holds caller")
	}

	decline := &declineTransferTool{task: task}
	if _, err := decline.Execute(context.Background(), `{"reason":"busy"}`); err != nil {
		t.Fatalf("decline Execute error = %v", err)
	}
	if session.InputAudioMuted() {
		t.Fatal("caller input audio muted = true after result, want restored")
	}
}

func TestWarmTransferOnEnterPausesCallerAudioOutputAndRestoresOnResult(t *testing.T) {
	task := newWarmTransferTaskForTest(t, "+15550100", "trunk_123", nil, "")
	jobCtx := &fakeWarmTransferJobContext{room: &livekit.Room{Name: "caller-room"}}
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	audioOutput := &fakeWarmTransferAudioOutputController{canPause: true}
	session.SetAudioOutputController(audioOutput)
	session.SetJobContext(jobCtx)
	task.Agent.Start(session, task)
	defer task.Agent.GetActivity().Stop()

	if audioOutput.pauseCount != 1 {
		t.Fatalf("PauseAudioOutput calls = %d, want 1 while warm transfer briefs human agent", audioOutput.pauseCount)
	}

	decline := &declineTransferTool{task: task}
	if _, err := decline.Execute(context.Background(), `{"reason":"busy"}`); err != nil {
		t.Fatalf("decline Execute error = %v", err)
	}
	if audioOutput.resumeCount != 1 {
		t.Fatalf("ResumeAudioOutput calls = %d, want 1 after warm transfer result", audioOutput.resumeCount)
	}
}

func TestWarmTransferPreservesPreexistingCallerInputAudioMute(t *testing.T) {
	task := newWarmTransferTaskForTest(t, "+15550100", "trunk_123", nil, "")
	jobCtx := &fakeWarmTransferJobContext{room: &livekit.Room{Name: "caller-room"}}
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	session.SetInputAudioMuted(true)
	session.SetJobContext(jobCtx)
	task.Agent.Start(session, task)
	defer task.Agent.GetActivity().Stop()

	task.OnEnter()

	decline := &declineTransferTool{task: task}
	if _, err := decline.Execute(context.Background(), `{"reason":"busy"}`); err != nil {
		t.Fatalf("decline Execute error = %v", err)
	}
	if !session.InputAudioMuted() {
		t.Fatal("caller input audio muted = false after result, want original muted state preserved")
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

func TestConnectToCallerDeletesTemporaryHumanAgentRoom(t *testing.T) {
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

	if jobCtx.deleteRoomName != "caller-room-human-agent" {
		t.Fatalf("DeleteRoom room = %q, want caller-room-human-agent", jobCtx.deleteRoomName)
	}
}

func TestWarmTransferCallerParticipantDisconnectDeletesCallerRoomOnce(t *testing.T) {
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
	jobCtx.deleteRoomName = ""
	jobCtx.deleteRoomCalls = 0

	session.EmitEvent(&agent.CloseEvent{Reason: agent.CloseReasonParticipantDisconnected})
	session.EmitEvent(&agent.CloseEvent{Reason: agent.CloseReasonParticipantDisconnected})

	waitForWarmTransferDeleteRoom(t, jobCtx, "caller-room")
	if calls := warmTransferDeleteRoomCalls(jobCtx); calls != 1 {
		t.Fatalf("DeleteRoom calls after caller-room disconnect = %d, want 1", calls)
	}
}

func TestWarmTransferCallerParticipantDisconnectIgnoresNonDefaultKinds(t *testing.T) {
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
	jobCtx.deleteRoomName = ""
	jobCtx.deleteRoomCalls = 0

	task.onCallerParticipantDisconnected("agent-a", livekit.ParticipantInfo_AGENT)

	if jobCtx.deleteRoomName != "" {
		t.Fatalf("DeleteRoom room = %q, want no caller-room delete for non-default participant kind", jobCtx.deleteRoomName)
	}
	if jobCtx.deleteRoomCalls != 0 {
		t.Fatalf("DeleteRoom calls = %d, want 0 for non-default participant kind", jobCtx.deleteRoomCalls)
	}
}

func TestConnectToCallerRequiresHumanAgentReady(t *testing.T) {
	task := newWarmTransferTaskForTest(t, "+15550100", "trunk_123", nil, "")
	jobCtx := &fakeWarmTransferJobContext{room: &livekit.Room{Name: "caller-room"}}
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	session.SetJobContext(jobCtx)
	task.Agent.Start(session, task)
	defer task.Agent.GetActivity().Stop()
	task.humanAgentReady = false
	task.humanAgentRoom = ""
	jobCtx.moveRequest = nil

	err := task.ConnectToCaller()
	if err == nil {
		t.Fatal("ConnectToCaller() error = nil, want missing human-agent readiness error")
	}
	if jobCtx.moveRequest != nil {
		t.Fatalf("MoveParticipant request = %#v, want no move before human-agent leg is ready", jobCtx.moveRequest)
	}
	if task.Done() {
		t.Fatal("task Done() = true, want pending after premature connect request")
	}
}

type fakeWarmTransferJobContext struct {
	mu               sync.Mutex
	room             *livekit.Room
	moveRequest      *livekit.MoveParticipantRequest
	deleteRoomName   string
	deleteRoomCalls  int
	createSIPRequest *livekit.CreateSIPParticipantRequest
	createErr        error
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

func (f *fakeWarmTransferJobContext) DeleteRoom(_ context.Context, roomName string) (*livekit.DeleteRoomResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.deleteRoomName = roomName
	f.deleteRoomCalls++
	return &livekit.DeleteRoomResponse{}, nil
}

func (f *fakeWarmTransferJobContext) CreateSIPParticipant(_ context.Context, req *livekit.CreateSIPParticipantRequest) (*livekit.SIPParticipantInfo, error) {
	f.createSIPRequest = req
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &livekit.SIPParticipantInfo{}, nil
}

type fakeWarmTransferBackgroundAudio struct {
	startRoom    *lksdk.Room
	startSession *agent.AgentSession
	playAudio    interface{}
	playLoop     bool
	handle       *fakeWarmTransferHoldAudioHandle
	closed       bool
}

func (f *fakeWarmTransferBackgroundAudio) Start(room *lksdk.Room, session *agent.AgentSession) error {
	f.startRoom = room
	f.startSession = session
	return nil
}

func (f *fakeWarmTransferBackgroundAudio) Play(audio interface{}, loop bool) warmTransferHoldAudioHandle {
	f.playAudio = audio
	f.playLoop = loop
	return f.handle
}

func (f *fakeWarmTransferBackgroundAudio) Close() error {
	f.closed = true
	return nil
}

type fakeWarmTransferHoldAudioHandle struct {
	stopped bool
}

func (f *fakeWarmTransferHoldAudioHandle) Stop() {
	f.stopped = true
}

type fakeWarmTransferAudioOutputController struct {
	canPause    bool
	pauseCount  int
	resumeCount int
}

func (f *fakeWarmTransferAudioOutputController) CanPauseAudioOutput() bool {
	return f.canPause
}

func (f *fakeWarmTransferAudioOutputController) PauseAudioOutput() {
	f.pauseCount++
}

func (f *fakeWarmTransferAudioOutputController) ResumeAudioOutput() {
	f.resumeCount++
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
	// Reference control tools return None after setting the task result.
	if out, err := connect.Execute(context.Background(), `{}`); err != nil || out != "" {
		t.Fatalf("connect Execute = %q/%v, want empty output", out, err)
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
	declineProps, ok := decline.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("decline properties = %#v, want map", decline.Parameters()["properties"])
	}
	reasonSchema, ok := declineProps["reason"].(map[string]any)
	if !ok {
		t.Fatalf("decline reason schema = %#v, want map", declineProps["reason"])
	}
	wantReasonDescription := "A short explanation of why the human agent declined to connect to the caller"
	if got := reasonSchema["description"]; got != wantReasonDescription {
		t.Fatalf("decline reason description = %#v, want %q", got, wantReasonDescription)
	}
	if out, err := decline.Execute(context.Background(), `{"reason":"busy"}`); err != nil || out != "" {
		t.Fatalf("decline Execute = %q/%v, want empty output", out, err)
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
	if out, err := voicemail.Execute(context.Background(), `{}`); err != nil || out != "" {
		t.Fatalf("voicemail Execute = %q/%v, want empty output", out, err)
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
