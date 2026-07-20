package warmtransfer

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cavos-io/rtp-agent/app"
	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/beta/workflows"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/interface/worker"
	"github.com/cavos-io/rtp-agent/library/logger"
)

const SupportInstructions = `
# Personality

You are friendly and helpful, with a welcoming personality
You're naturally curious, empathetic, and intuitive, always aiming to deeply understand the user's intent by actively listening.

# Environment

You are engaged in a live, spoken dialogue over the phone.
There are no other ways of communication with the user (no chat, text, visual, etc)

# Tone

Your responses are warm, measured, and supportive, typically 1-2 sentences to maintain a comfortable pace.
You speak with gentle, thoughtful pacing, using pauses (marked by "...") when appropriate to let emotional moments breathe.
You naturally include subtle conversational elements like "Hmm," "I see," and occasional rephrasing to sound authentic.
You actively acknowledge feelings ("That sounds really difficult...") and check in regularly ("How does that resonate with you?").
You vary your tone to match the user's emotional state, becoming calmer and more deliberate when they express distress.

# Identity

You are a customer support agent for LiveKit.

# Transferring to a human

In some cases, the user may ask to speak to a human agent. This could happen when you are unable to answer their question.
When such is requested, you would always confirm with the user before initiating the transfer.
`

const SummaryInstructions = `
Introduce the conversation from your perspective as the AI assistant who participated in this call:

WHO you're talking to (name, role, company if mentioned)
WHY they contacted you (goal, problem, request)
WHY a human agent is requested or needed at this point
Brief summary in 100-200 characters from a first-person perspective`

func ConfigFromEnv() app.AppConfig {
	_ = loadWarmTransferDotEnv(".env")
	cfg := app.DefaultConfigFromEnv()
	cfg.Instructions = SupportInstructions
	cfg.LLMProvider = "livekit"
	cfg.LLMModel = "openai/gpt-4.1-mini"
	cfg.STTProvider = "livekit"
	cfg.STTModel = "deepgram/nova-3"
	cfg.STTLanguage = "en"
	cfg.VADProvider = "silero"
	cfg.TTSProvider = "livekit"
	cfg.TTSModel = "cartesia/sonic-3"
	cfg.TTSVoice = "9626c31c-bec5-4cca-baa8-f8ba9e84c8bc"
	cfg.WorkflowTask = "warm_transfer"
	cfg.WorkflowWarmTransferSipCallTo = firstNonEmpty(
		cfg.WorkflowWarmTransferSipCallTo,
		os.Getenv("LIVEKIT_SUPERVISOR_PHONE_NUMBER"),
	)
	cfg.WorkflowWarmTransferSipTrunkID = firstNonEmpty(
		cfg.WorkflowWarmTransferSipTrunkID,
		os.Getenv("LIVEKIT_SIP_OUTBOUND_TRUNK"),
	)
	cfg.WorkflowWarmTransferSipNumber = firstNonEmpty(
		cfg.WorkflowWarmTransferSipNumber,
		os.Getenv("LIVEKIT_SIP_NUMBER"),
	)
	cfg.WorkflowWarmTransferExtraInstructions = firstNonEmpty(
		cfg.WorkflowWarmTransferExtraInstructions,
		SummaryInstructions,
	)
	cfg.WorkerOptions.AgentName = firstNonEmpty(cfg.WorkerOptions.AgentName, "sip-inbound")
	cfg.WorkerOptions.WorkerType = worker.WorkerTypeRoom
	return cfg
}

func NewApp(cfg app.AppConfig) (*app.App, error) {
	if strings.TrimSpace(cfg.WorkflowTask) == "" {
		cfg.WorkflowTask = "warm_transfer"
	}
	if strings.TrimSpace(cfg.WorkflowWarmTransferExtraInstructions) == "" {
		cfg.WorkflowWarmTransferExtraInstructions = SummaryInstructions
	}
	rtpApp, err := app.Init(cfg)
	if err != nil {
		return nil, err
	}
	if rtpApp.Session == nil {
		_ = rtpApp.Close(context.Background())
		return nil, fmt.Errorf("agent session is not configured")
	}
	rtpApp.RoomOptions.DeleteRoomOnClose = false
	rtpApp.Session.Options = mergeWarmTransferSessionOptions(rtpApp.Session.Options)
	supportAgent := NewSupportAgent(rtpApp.Session, cfg)
	if rtpApp.Agent != nil {
		copyRuntime(supportAgent.Agent, rtpApp.Agent)
	}
	rtpApp.Session.UpdateAgent(supportAgent)
	rtpApp.Agent = supportAgent.Agent
	return rtpApp, nil
}

func mergeWarmTransferSessionOptions(existing agent.AgentSessionOptions) agent.AgentSessionOptions {
	existing.PreemptiveGeneration = true
	return existing
}

type SupportAgent struct {
	*agent.Agent
	session *agent.AgentSession
	config  app.AppConfig
}

func NewSupportAgent(session *agent.AgentSession, cfg app.AppConfig) *SupportAgent {
	base := agent.NewAgent(SupportInstructions)
	support := &SupportAgent{
		Agent:   base,
		session: session,
		config:  cfg,
	}
	base.Tools = []llm.Tool{&transferToHumanTool{support: support}}
	return support
}

func (a *SupportAgent) OnEnter() {
	session := a.session
	if activity := a.GetActivity(); activity != nil && activity.Session != nil {
		session = activity.Session
	}
	if session == nil {
		return
	}
	_, _ = session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{})
}

type transferToHumanTool struct {
	support *SupportAgent
}

func (t *transferToHumanTool) ID() string   { return "transfer_to_human" }
func (t *transferToHumanTool) Name() string { return "transfer_to_human" }
func (t *transferToHumanTool) Description() string {
	return `Called when the user asks to speak to a human agent. This will put the user on
hold while the supervisor is connected.

Ensure that the user has confirmed that they wanted to be transferred. Do not start transfer
until the user has confirmed.`
}
func (t *transferToHumanTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *transferToHumanTool) Execute(ctx context.Context, args string) (string, error) {
	if t.support == nil || t.support.session == nil {
		return "", llm.NewToolError("failed to transfer to supervisor: agent session is not available")
	}
	session := t.support.session
	allowInterruptions := false
	hold, err := session.SayWithOptions(ctx, agent.SayOptions{
		Text:               "Please hold while I connect you to a human agent.",
		AllowInterruptions: &allowInterruptions,
	})
	if err != nil {
		return "", llm.NewToolError(fmt.Sprintf("failed to transfer to supervisor with error: %v", err))
	}
	if hold != nil {
		if err := hold.Wait(ctx); err != nil {
			return "", llm.NewToolError(fmt.Sprintf("failed to transfer to supervisor with error: %v", err))
		}
	}

	task, err := warmTransferTaskFromConfig(t.support.config, t.support.ChatCtx)
	if err != nil {
		return "", llm.NewToolError(fmt.Sprintf("failed to transfer to supervisor with error: %v", err))
	}
	copyRuntime(task.GetAgent(), t.support.Agent)
	session.UpdateAgent(task)
	result, err := task.Wait(ctx)
	if err != nil {
		if _, ok := err.(llm.ToolError); ok {
			return "", err
		}
		return "", llm.NewToolError(fmt.Sprintf("failed to transfer to supervisor with error: %v", err))
	}

	logger.Logger.Infow("transfer to supervisor successful", "supervisor_identity", result.HumanAgentIdentity)
	final, err := session.SayWithOptions(ctx, agent.SayOptions{
		Text:               "you are on the line with my supervisor. I'll be hanging up now.",
		AllowInterruptions: &allowInterruptions,
	})
	if err != nil {
		return "", llm.NewToolError(fmt.Sprintf("failed to transfer to supervisor with error: %v", err))
	}
	if final != nil {
		if err := final.Wait(ctx); err != nil {
			return "", llm.NewToolError(fmt.Sprintf("failed to transfer to supervisor with error: %v", err))
		}
	}
	session.Shutdown()
	return "Transfer completed.", nil
}

func warmTransferTaskFromConfig(cfg app.AppConfig, chatCtx *llm.ChatContext) (*workflows.WarmTransferTask, error) {
	task, err := workflows.NewWarmTransferTaskWithOptions(workflows.WarmTransferOptions{
		TargetPhone:       cfg.WorkflowWarmTransferSipCallTo,
		TrunkID:           cfg.WorkflowWarmTransferSipTrunkID,
		SipConnection:     cfg.WorkflowWarmTransferSipConnection,
		SipNumber:         cfg.WorkflowWarmTransferSipNumber,
		DisableHoldAudio:  cfg.WorkflowWarmTransferDisableHoldAudio,
		ChatContext:       chatCtx,
		ExtraInstructions: cfg.WorkflowWarmTransferExtraInstructions,
	})
	if err != nil {
		return nil, err
	}
	if len(cfg.WorkflowWarmTransferSipHeaders) > 0 {
		task.SipHeaders = cfg.WorkflowWarmTransferSipHeaders
	}
	task.Dtmf = strings.TrimSpace(cfg.WorkflowWarmTransferDTMF)
	if cfg.WorkflowWarmTransferRingingTimeout != nil {
		task.RingingTimeout = time.Duration(*cfg.WorkflowWarmTransferRingingTimeout * float64(time.Second))
	}
	return task, nil
}

func copyRuntime(dst *agent.Agent, src *agent.Agent) {
	if dst == nil || src == nil {
		return
	}
	dst.ChatCtx = src.ChatCtx
	dst.TurnDetection = src.TurnDetection
	dst.TurnDetector = src.TurnDetector
	dst.Avatar = src.Avatar
	dst.STT = src.STT
	dst.VAD = src.VAD
	dst.LLM = src.LLM
	dst.RealtimeModel = src.RealtimeModel
	dst.TTS = src.TTS
	dst.AllowInterruptions = src.AllowInterruptions
	dst.AllowInterruptionsSet = src.AllowInterruptionsSet
	dst.MinConsecutiveSpeechDelay = src.MinConsecutiveSpeechDelay
	dst.UseTTSAlignedTranscript = src.UseTTSAlignedTranscript
	dst.UseTTSAlignedTranscriptSet = src.UseTTSAlignedTranscriptSet
	dst.MinEndpointingDelay = src.MinEndpointingDelay
	dst.MaxEndpointingDelay = src.MaxEndpointingDelay
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func loadWarmTransferDotEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := parseWarmTransferDotEnvLine(scanner.Text())
		if !ok {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func parseWarmTransferDotEnvLine(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
	key, value, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	if key == "" || strings.ContainsAny(key, " \t") {
		return "", "", false
	}
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		quote := value[0]
		if (quote == '\'' || quote == '"') && value[len(value)-1] == quote {
			value = value[1 : len(value)-1]
		}
	}
	return key, value, true
}
