package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	beta "github.com/cavos-io/rtp-agent/core/beta"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

const warmTransferPersona = `# Identity

You are an agent that is reaching out to a human agent for help. There has been a previous conversation
between you and a caller, the conversation history is included below.

# Goal

Your main goal is to give the human agent sufficient context about why the caller had called in,
so that the human agent could gain sufficient knowledge to help the caller directly.
`

const WarmTransferBaseInstructions = warmTransferPersona + `
# Context

In the conversation, user refers to the human agent, caller refers to the person who's transcript is included.
Remember, you are not speaking to the caller right now, you are speaking to the human agent.

Once the human agent has confirmed, you should call the tool connect_to_caller to connect them to the caller.

Start by giving them a summary of the conversation so far, and answer any questions they might have.

## Conversation history with caller
%s
## End of conversation history with caller

You are talking to the human agent now,
give a brief introduction of the conversation so far, and ask if they want to connect to the caller.`

type WarmTransferResult struct {
	HumanAgentIdentity string
}

type WarmTransferOptions struct {
	TargetPhone       string
	TrunkID           string
	SipConnection     *livekit.SIPOutboundConfig
	SipNumber         string
	ChatContext       *llm.ChatContext
	ExtraInstructions string
	Instructions      *beta.InstructionParts
}

type WarmTransferTask struct {
	agent.AgentTask[*WarmTransferResult]
	TargetPhoneNumber string
	SipTrunkID        string
	SipConnection     *livekit.SIPOutboundConfig
	SipNumber         string
	SipHeaders        map[string]string
	Dtmf              string
	RingingTimeout    time.Duration
	HoldAudio         interface{}

	callerRoom         *lksdk.Room
	humanAgentSess     *agent.AgentSession
	humanAgentIdentity string

	backgroundAudio *agent.BackgroundAudioPlayer
	holdAudioHandle *agent.PlayHandle

	mu sync.Mutex
}

type warmTransferJobContext interface {
	RoomInfo() *livekit.Room
	CreateSIPParticipant(ctx context.Context, req *livekit.CreateSIPParticipantRequest) (*livekit.SIPParticipantInfo, error)
	MoveParticipant(ctx context.Context, room string, identity string, destinationRoom string) error
}

func NewWarmTransferTask(targetPhone string, trunkId string, chatCtx *llm.ChatContext, extraInstructions string) (*WarmTransferTask, error) {
	return NewWarmTransferTaskWithOptions(WarmTransferOptions{
		TargetPhone:       targetPhone,
		TrunkID:           trunkId,
		ChatContext:       chatCtx,
		ExtraInstructions: extraInstructions,
	})
}

func NewWarmTransferTaskWithOptions(opts WarmTransferOptions) (*WarmTransferTask, error) {
	targetPhone := strings.TrimSpace(opts.TargetPhone)
	trunkId := strings.TrimSpace(opts.TrunkID)
	if targetPhone == "" {
		return nil, fmt.Errorf("`sip_call_to` must be set")
	}
	if trunkId == "" {
		if opts.SipConnection == nil {
			trunkId = strings.TrimSpace(os.Getenv("LIVEKIT_SIP_OUTBOUND_TRUNK"))
		}
	}
	if trunkId == "" && opts.SipConnection == nil {
		return nil, fmt.Errorf("`LIVEKIT_SIP_OUTBOUND_TRUNK` environment variable, `sip_trunk_id`, or `sip_connection` must be set")
	}

	prevConvo := ""
	if opts.ChatContext != nil {
		for _, msg := range opts.ChatContext.Items {
			if m, ok := msg.(*llm.ChatMessage); ok && (m.Role == llm.ChatRoleUser || m.Role == llm.ChatRoleAssistant) {
				role := "Caller"
				if m.Role == llm.ChatRoleAssistant {
					role := "Assistant"
					prevConvo += fmt.Sprintf("%s: %s\n", role, m.TextContent())
				} else {
					prevConvo += fmt.Sprintf("%s: %s\n", role, m.TextContent())
				}
			}
		}
	}

	instructions := fmt.Sprintf(WarmTransferBaseInstructions, prevConvo)
	if opts.Instructions != nil {
		instructions = applyInstructionParts(instructions, warmTransferPersona, opts.Instructions)
	} else {
		instructions += opts.ExtraInstructions
	}

	sipNumber := strings.TrimSpace(opts.SipNumber)
	if sipNumber == "" {
		sipNumber = os.Getenv("LIVEKIT_SIP_NUMBER")
	}

	t := &WarmTransferTask{
		AgentTask:          *agent.NewAgentTask[*WarmTransferResult](instructions),
		TargetPhoneNumber:  targetPhone,
		SipTrunkID:         trunkId,
		SipConnection:      opts.SipConnection,
		humanAgentIdentity: "human-agent-sip",
		SipNumber:          sipNumber,
	}

	t.Agent.Tools = []llm.Tool{
		&connectToCallerTool{task: t},
		&declineTransferTool{task: t},
		&voicemailDetectedTool{task: t},
	}

	return t, nil
}

func (t *WarmTransferTask) OnEnter() {
	t.mu.Lock()
	defer t.mu.Unlock()

	logger.Logger.Infow("Entering warm transfer task, dialing human agent", "target", t.TargetPhoneNumber)
	if activity := t.GetActivity(); activity != nil && activity.Session != nil {
		t.callerRoom = activity.Session.Room
	}

	// In a full implementation, we would start background audio and dial SIP
	// self.background_audio = BackgroundAudioPlayer()
	// self.hold_audio = AudioConfig(BuiltinAudioClip.HOLD_MUSIC, volume=0.8)

	t.backgroundAudio = agent.NewBackgroundAudioPlayer(agent.AudioConfig{
		Source: agent.HoldMusic,
		Volume: 0.8,
	}, nil)

	jobCtx, err := t.jobContext()
	if err != nil {
		t.Fail(err)
		return
	}
	callerRoomName := t.callerRoomName(jobCtx)
	req := &livekit.CreateSIPParticipantRequest{
		RoomName:            t.humanAgentRoomName(callerRoomName),
		ParticipantIdentity: t.humanAgentIdentity,
		SipTrunkId:          t.SipTrunkID,
		SipCallTo:           t.TargetPhoneNumber,
		WaitUntilAnswered:   true,
		SipNumber:           t.SipNumber,
		Headers:             t.SipHeaders,
		Dtmf:                t.Dtmf,
	}
	if t.SipConnection != nil {
		req.Trunk = proto.Clone(t.SipConnection).(*livekit.SIPOutboundConfig)
	}
	if t.RingingTimeout > 0 {
		req.RingingTimeout = durationpb.New(t.RingingTimeout)
	}
	_, err = jobCtx.CreateSIPParticipant(context.Background(), req)
	if err != nil {
		t.Fail(err)
	}
}

func (t *WarmTransferTask) OnExit() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.cleanupResultResourcesLocked()
}

func (t *WarmTransferTask) cleanupResultResourcesLocked() {
	if t.holdAudioHandle != nil {
		t.holdAudioHandle.Stop()
		t.holdAudioHandle = nil
	}
	if t.backgroundAudio != nil {
		t.backgroundAudio.Close()
		t.backgroundAudio = nil
	}
	if t.humanAgentSess != nil {
		t.humanAgentSess.Shutdown(false)
		t.humanAgentSess = nil
	}
}

func (t *WarmTransferTask) setResultLocked(result *WarmTransferResult, err error) {
	if t.Done() {
		return
	}
	t.cleanupResultResourcesLocked()
	if err != nil {
		_ = t.Fail(err)
		return
	}
	_ = t.Complete(result)
}

func (t *WarmTransferTask) setResult(result *WarmTransferResult, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.setResultLocked(result, err)
}

func (t *WarmTransferTask) ConnectToCaller() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	logger.Logger.Debugw(
		"Connecting human agent to caller",
		"caller_room_ready", t.callerRoom != nil,
		"human_agent_session_ready", t.humanAgentSess != nil,
	)

	jobCtx, err := t.jobContext()
	if err != nil {
		return err
	}
	destinationRoom := t.callerRoomName(jobCtx)
	humanAgentRoom := t.humanAgentRoomName(destinationRoom)
	if err := jobCtx.MoveParticipant(context.Background(), humanAgentRoom, t.humanAgentIdentity, destinationRoom); err != nil {
		return err
	}

	t.setResultLocked(&WarmTransferResult{HumanAgentIdentity: t.humanAgentIdentity}, nil)
	return nil
}

func (t *WarmTransferTask) jobContext() (warmTransferJobContext, error) {
	activity := t.Agent.GetActivity()
	if activity == nil || activity.Session == nil {
		return nil, fmt.Errorf("warm transfer job context requires an active session")
	}
	value, err := activity.Session.JobContext()
	if err != nil {
		return nil, err
	}
	jobCtx, ok := value.(warmTransferJobContext)
	if !ok {
		return nil, fmt.Errorf("job context does not support participant moves")
	}
	return jobCtx, nil
}

func (t *WarmTransferTask) callerRoomName(jobCtx warmTransferJobContext) string {
	if t.callerRoom != nil && t.callerRoom.Name() != "" {
		return t.callerRoom.Name()
	}
	if jobCtx != nil {
		if room := jobCtx.RoomInfo(); room != nil {
			return room.GetName()
		}
	}
	return ""
}

func (t *WarmTransferTask) humanAgentRoomName(callerRoomName string) string {
	if t.humanAgentSess != nil && t.humanAgentSess.Room != nil && t.humanAgentSess.Room.Name() != "" {
		return t.humanAgentSess.Room.Name()
	}
	if callerRoomName != "" {
		return callerRoomName + "-human-agent"
	}
	return ""
}

type connectToCallerTool struct {
	task *WarmTransferTask
}

func (t *connectToCallerTool) ID() string   { return "connect_to_caller" }
func (t *connectToCallerTool) Name() string { return "connect_to_caller" }
func (t *connectToCallerTool) Description() string {
	return "Called when the human agent wants to connect to the caller."
}
func (t *connectToCallerTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *connectToCallerTool) Execute(ctx context.Context, args string) (string, error) {
	err := t.task.ConnectToCaller()
	if err != nil {
		return "", err
	}
	return "Connected to caller.", nil
}

type declineTransferTool struct {
	task *WarmTransferTask
}

func (t *declineTransferTool) ID() string   { return "decline_transfer" }
func (t *declineTransferTool) Name() string { return "decline_transfer" }
func (t *declineTransferTool) Description() string {
	return "Handles the case when the human agent explicitly declines to connect to the caller."
}
func (t *declineTransferTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reason": map[string]any{"type": "string", "description": "A short explanation of why the human agent declined"},
		},
		"required": []string{"reason"},
	}
}

func (t *declineTransferTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}

	t.task.setResult(nil, llm.NewToolError(fmt.Sprintf("human agent declined to connect: %s", params.Reason)))
	return "Transfer declined.", nil
}

type voicemailDetectedTool struct {
	task *WarmTransferTask
}

func (t *voicemailDetectedTool) ID() string   { return "voicemail_detected" }
func (t *voicemailDetectedTool) Name() string { return "voicemail_detected" }
func (t *voicemailDetectedTool) Description() string {
	return "Called when the call reaches voicemail. Use this tool AFTER you hear the voicemail greeting"
}
func (t *voicemailDetectedTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *voicemailDetectedTool) Execute(ctx context.Context, args string) (string, error) {
	t.task.setResult(nil, llm.NewToolError("voicemail detected"))
	return "Voicemail detected.", nil
}
