package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

const WarmTransferBaseInstructions = `# Identity

You are an agent that is reaching out to a human agent for help. There has been a previous conversation
between you and a caller, the conversation history is included below.

# Goal

Your main goal is to give the human agent sufficient context about why the caller had called in,
so that the human agent could gain sufficient knowledge to help the caller directly.

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

type WarmTransferTask struct {
	agent.AgentTask[*WarmTransferResult]
	TargetPhoneNumber string
	SipTrunkID        string
	SipNumber         string
	SipHeaders        map[string]string
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
	MoveParticipant(ctx context.Context, room string, identity string, destinationRoom string) error
}

func NewWarmTransferTask(targetPhone string, trunkId string, chatCtx *llm.ChatContext, extraInstructions string) *WarmTransferTask {
	prevConvo := ""
	if chatCtx != nil {
		for _, msg := range chatCtx.Items {
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

	instructions := fmt.Sprintf(WarmTransferBaseInstructions, prevConvo) + extraInstructions

	t := &WarmTransferTask{
		AgentTask:          *agent.NewAgentTask[*WarmTransferResult](instructions),
		TargetPhoneNumber:  targetPhone,
		SipTrunkID:         trunkId,
		humanAgentIdentity: "human-agent-sip",
		SipNumber:          os.Getenv("LIVEKIT_SIP_NUMBER"),
	}

	if t.SipTrunkID == "" {
		t.SipTrunkID = os.Getenv("LIVEKIT_SIP_OUTBOUND_TRUNK")
	}

	t.Agent.Tools = []llm.Tool{
		&connectToCallerTool{task: t},
		&declineTransferTool{task: t},
		&voicemailDetectedTool{task: t},
	}

	return t
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

	// We'll need the room from the session to start background audio
	// This part is tricky without a fully linked session/activity
}

func (t *WarmTransferTask) OnExit() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.holdAudioHandle != nil {
		t.holdAudioHandle.Stop()
	}
	if t.backgroundAudio != nil {
		t.backgroundAudio.Close()
	}
	if t.humanAgentSess != nil {
		t.humanAgentSess.Shutdown(false)
		t.humanAgentSess = nil
	}
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

	t.Complete(&WarmTransferResult{HumanAgentIdentity: t.humanAgentIdentity})
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

	t.task.Fail(fmt.Errorf("human agent declined to connect: %s", params.Reason))
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
	t.task.Fail(fmt.Errorf("voicemail detected"))
	return "Voicemail detected.", nil
}
