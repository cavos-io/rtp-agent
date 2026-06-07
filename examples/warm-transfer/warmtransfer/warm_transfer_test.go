package warmtransfer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/beta/workflows"
	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestWarmTransferConfigMapsReferenceEnvironment(t *testing.T) {
	t.Setenv("LIVEKIT_API_KEY", "test-key")
	t.Setenv("LIVEKIT_API_SECRET", "test-secret")
	t.Setenv("LIVEKIT_SUPERVISOR_PHONE_NUMBER", "+12003004000")
	t.Setenv("LIVEKIT_SIP_OUTBOUND_TRUNK", "ST_abcxyz")
	t.Setenv("LIVEKIT_SIP_NUMBER", "+15005006000")

	cfg := ConfigFromEnv()

	if cfg.WorkflowTask != "warm_transfer" {
		t.Fatalf("WorkflowTask = %q, want warm_transfer", cfg.WorkflowTask)
	}
	if cfg.WorkflowWarmTransferSipCallTo != "+12003004000" {
		t.Fatalf("WorkflowWarmTransferSipCallTo = %q, want supervisor phone", cfg.WorkflowWarmTransferSipCallTo)
	}
	if cfg.WorkflowWarmTransferSipTrunkID != "ST_abcxyz" {
		t.Fatalf("WorkflowWarmTransferSipTrunkID = %q, want reference trunk env", cfg.WorkflowWarmTransferSipTrunkID)
	}
	if cfg.WorkflowWarmTransferSipNumber != "+15005006000" {
		t.Fatalf("WorkflowWarmTransferSipNumber = %q, want caller ID number", cfg.WorkflowWarmTransferSipNumber)
	}
	if cfg.WorkerOptions.AgentName != "sip-inbound" {
		t.Fatalf("AgentName = %q, want sip-inbound", cfg.WorkerOptions.AgentName)
	}
}

func TestWarmTransferConfigMatchesReferenceProvidersAndPrompts(t *testing.T) {
	t.Setenv("LIVEKIT_API_KEY", "test-key")
	t.Setenv("LIVEKIT_API_SECRET", "test-secret")
	t.Setenv("LIVEKIT_SUPERVISOR_PHONE_NUMBER", "+12003004000")
	t.Setenv("LIVEKIT_SIP_OUTBOUND_TRUNK", "ST_abcxyz")

	cfg := ConfigFromEnv()

	if cfg.LLMProvider != "livekit" || cfg.LLMModel != "openai/gpt-4.1-mini" {
		t.Fatalf("LLM = %q/%q, want LiveKit openai/gpt-4.1-mini", cfg.LLMProvider, cfg.LLMModel)
	}
	if cfg.STTProvider != "livekit" || cfg.STTModel != "deepgram/nova-3" || cfg.STTLanguage != "en" {
		t.Fatalf("STT = %q/%q:%q, want LiveKit deepgram/nova-3:en", cfg.STTProvider, cfg.STTModel, cfg.STTLanguage)
	}
	if cfg.TTSProvider != "livekit" || cfg.TTSModel != "cartesia/sonic-3" || cfg.TTSVoice != "9626c31c-bec5-4cca-baa8-f8ba9e84c8bc" {
		t.Fatalf("TTS = %q/%q:%q, want LiveKit Cartesia voice", cfg.TTSProvider, cfg.TTSModel, cfg.TTSVoice)
	}
	if cfg.VADProvider != "silero" {
		t.Fatalf("VADProvider = %q, want silero", cfg.VADProvider)
	}
	for _, want := range []string{
		"friendly and helpful",
		"live, spoken dialogue over the phone",
		"You are a customer support agent for LiveKit.",
		"always confirm with the user before initiating the transfer",
	} {
		if !strings.Contains(cfg.Instructions, want) {
			t.Fatalf("Instructions = %q, want reference fragment %q", cfg.Instructions, want)
		}
	}
	for _, want := range []string{
		"Introduce the conversation from your perspective",
		"WHY a human agent is requested or needed",
		"Brief summary in 100-200 characters",
	} {
		if !strings.Contains(cfg.WorkflowWarmTransferExtraInstructions, want) {
			t.Fatalf("WorkflowWarmTransferExtraInstructions = %q, want %q", cfg.WorkflowWarmTransferExtraInstructions, want)
		}
	}
}

func TestWarmTransferConfigPreservesExplicitRTPAgentOverrides(t *testing.T) {
	t.Setenv("LIVEKIT_API_KEY", "test-key")
	t.Setenv("LIVEKIT_API_SECRET", "test-secret")
	t.Setenv("LIVEKIT_SUPERVISOR_PHONE_NUMBER", "+12003004000")
	t.Setenv("LIVEKIT_SIP_OUTBOUND_TRUNK", "ST_abcxyz")
	t.Setenv("LIVEKIT_SIP_NUMBER", "+15005006000")
	t.Setenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_SIP_CALL_TO", "+19990001111")
	t.Setenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_SIP_TRUNK_ID", "ST_override")
	t.Setenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_SIP_NUMBER", "+18880001111")
	t.Setenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_EXTRA_INSTRUCTIONS", "custom handoff summary")

	cfg := ConfigFromEnv()

	if cfg.WorkflowWarmTransferSipCallTo != "+19990001111" {
		t.Fatalf("WorkflowWarmTransferSipCallTo = %q, want explicit RTP override", cfg.WorkflowWarmTransferSipCallTo)
	}
	if cfg.WorkflowWarmTransferSipTrunkID != "ST_override" {
		t.Fatalf("WorkflowWarmTransferSipTrunkID = %q, want explicit RTP override", cfg.WorkflowWarmTransferSipTrunkID)
	}
	if cfg.WorkflowWarmTransferSipNumber != "+18880001111" {
		t.Fatalf("WorkflowWarmTransferSipNumber = %q, want explicit RTP override", cfg.WorkflowWarmTransferSipNumber)
	}
	if cfg.WorkflowWarmTransferExtraInstructions != "custom handoff summary" {
		t.Fatalf("WorkflowWarmTransferExtraInstructions = %q, want explicit RTP override", cfg.WorkflowWarmTransferExtraInstructions)
	}
}

func TestWarmTransferSupportAgentMatchesReferenceToolContract(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("base"), nil, agent.AgentSessionOptions{})
	support := NewSupportAgent(session, ConfigFromEnv())

	if support.Instructions != SupportInstructions {
		t.Fatalf("Instructions = %q, want support instructions", support.Instructions)
	}
	if len(support.Tools) != 1 {
		t.Fatalf("len(Tools) = %d, want transfer_to_human tool", len(support.Tools))
	}
	tool := support.Tools[0]
	if tool.Name() != "transfer_to_human" || tool.ID() != "transfer_to_human" {
		t.Fatalf("tool = %s/%s, want transfer_to_human", tool.ID(), tool.Name())
	}
	for _, want := range []string{
		"user asks to speak to a human agent",
		"put the user on",
		"hold while the supervisor is connected",
		"Do not start transfer",
		"until the user has confirmed",
	} {
		if !strings.Contains(tool.Description(), want) {
			t.Fatalf("Description() = %q, want reference fragment %q", tool.Description(), want)
		}
	}
	if got := tool.Parameters(); got["type"] != "object" {
		t.Fatalf("Parameters() = %#v, want empty object schema", got)
	}
}

func TestWarmTransferTaskFromConfigPassesReferenceOptions(t *testing.T) {
	timeout := 25.0
	cfg := ConfigFromEnv()
	cfg.WorkflowWarmTransferSipCallTo = "+12003004000"
	cfg.WorkflowWarmTransferSipTrunkID = "ST_abcxyz"
	cfg.WorkflowWarmTransferSipNumber = "+15005006000"
	cfg.WorkflowWarmTransferSipHeaders = map[string]string{"X-LiveKit": "warm-transfer"}
	cfg.WorkflowWarmTransferDTMF = "wwww1234#"
	cfg.WorkflowWarmTransferRingingTimeout = &timeout
	cfg.WorkflowWarmTransferExtraInstructions = SummaryInstructions
	chatCtx := llm.NewChatContext()
	chatCtx.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleUser, Text: "I need help"})

	task, err := warmTransferTaskFromConfig(cfg, chatCtx)
	if err != nil {
		t.Fatalf("warmTransferTaskFromConfig() error = %v", err)
	}

	if task.TargetPhoneNumber != "+12003004000" {
		t.Fatalf("TargetPhoneNumber = %q, want supervisor phone", task.TargetPhoneNumber)
	}
	if task.SipTrunkID != "ST_abcxyz" {
		t.Fatalf("SipTrunkID = %q, want trunk", task.SipTrunkID)
	}
	if task.SipNumber != "+15005006000" {
		t.Fatalf("SipNumber = %q, want caller ID", task.SipNumber)
	}
	if task.SipHeaders["X-LiveKit"] != "warm-transfer" {
		t.Fatalf("SipHeaders = %#v, want reference header passthrough", task.SipHeaders)
	}
	if task.Dtmf != "wwww1234#" {
		t.Fatalf("Dtmf = %q, want reference DTMF", task.Dtmf)
	}
	if task.RingingTimeout.String() != "25s" {
		t.Fatalf("RingingTimeout = %v, want 25s", task.RingingTimeout)
	}
	if !strings.Contains(task.Instructions, "Brief summary in 100-200 characters") {
		t.Fatalf("Instructions = %q, want summary instructions", task.Instructions)
	}
	if !strings.Contains(task.Instructions, "Caller: I need help") {
		t.Fatalf("Instructions = %q, want caller chat context summary", task.Instructions)
	}
}

func TestWarmTransferAppKeepsRoomOpenForSupervisor(t *testing.T) {
	t.Setenv("LIVEKIT_API_KEY", "test-key")
	t.Setenv("LIVEKIT_API_SECRET", "test-secret")
	t.Setenv("LIVEKIT_SUPERVISOR_PHONE_NUMBER", "+12003004000")
	t.Setenv("LIVEKIT_SIP_OUTBOUND_TRUNK", "ST_abcxyz")

	rtpApp, err := NewApp(ConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	defer rtpApp.Close(context.Background())

	if rtpApp.RoomOptions.DeleteRoomOnClose {
		t.Fatal("DeleteRoomOnClose = true, want reference delete_room_on_close=false")
	}
	if _, ok := rtpApp.Session.Agent.(*SupportAgent); !ok {
		t.Fatalf("Session.Agent = %T, want *SupportAgent", rtpApp.Session.Agent)
	}
	if _, ok := rtpApp.Session.Agent.(*workflows.WarmTransferTask); ok {
		t.Fatalf("Session.Agent = %T, want support agent wrapper, not direct workflow task", rtpApp.Session.Agent)
	}
}

func TestWarmTransferConfigLoadsDotEnvLikeReferenceExample(t *testing.T) {
	t.Setenv("LIVEKIT_API_KEY", "test-key")
	t.Setenv("LIVEKIT_API_SECRET", "test-secret")
	for _, key := range []string{
		"LIVEKIT_SUPERVISOR_PHONE_NUMBER",
		"LIVEKIT_SIP_OUTBOUND_TRUNK",
		"LIVEKIT_SIP_NUMBER",
		"RTP_AGENT_WORKFLOW_WARM_TRANSFER_DTMF",
	} {
		previous, hadPrevious := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("Unsetenv(%s): %v", key, err)
		}
		t.Cleanup(func() {
			if hadPrevious {
				_ = os.Setenv(key, previous)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}

	tmp := t.TempDir()
	dotenv := strings.Join([]string{
		"LIVEKIT_SUPERVISOR_PHONE_NUMBER=+12003004000",
		"LIVEKIT_SIP_OUTBOUND_TRUNK=ST_abcxyz",
		"LIVEKIT_SIP_NUMBER='+15005006000'",
		"RTP_AGENT_WORKFLOW_WARM_TRANSFER_DTMF=wwww1234#",
	}, "\n")
	if err := os.WriteFile(filepath.Join(tmp, ".env"), []byte(dotenv), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})

	cfg := ConfigFromEnv()

	if cfg.WorkflowWarmTransferSipCallTo != "+12003004000" {
		t.Fatalf("WorkflowWarmTransferSipCallTo = %q, want .env supervisor phone", cfg.WorkflowWarmTransferSipCallTo)
	}
	if cfg.WorkflowWarmTransferSipTrunkID != "ST_abcxyz" {
		t.Fatalf("WorkflowWarmTransferSipTrunkID = %q, want .env trunk", cfg.WorkflowWarmTransferSipTrunkID)
	}
	if cfg.WorkflowWarmTransferSipNumber != "+15005006000" {
		t.Fatalf("WorkflowWarmTransferSipNumber = %q, want unquoted .env caller ID", cfg.WorkflowWarmTransferSipNumber)
	}
	if cfg.WorkflowWarmTransferDTMF != "wwww1234#" {
		t.Fatalf("WorkflowWarmTransferDTMF = %q, want .env DTMF", cfg.WorkflowWarmTransferDTMF)
	}
}
