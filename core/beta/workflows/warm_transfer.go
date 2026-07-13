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

## Conversation history with caller
%s
## End of conversation history with caller

Once the human agent has confirmed, you should call the tool ` + "`connect_to_caller`" + ` to connect them to the caller.

You are talking to the human agent now, start by giving them a summary of the conversation so far, and answer any questions they might have.`

type WarmTransferResult struct {
	HumanAgentIdentity string
}

type WarmTransferOptions struct {
	AgentOptions
	TargetPhone       string
	TargetPhoneNumber string
	TrunkID           string
	TrunkIDSet        bool
	SipConnection     *livekit.SIPOutboundConfig
	SipNumber         string
	SipNumberSet      bool
	SipHeaders        map[string]string
	Dtmf              string
	RingingTimeout    time.Duration
	RingingTimeoutSet bool
	HoldAudio         interface{}
	DisableHoldAudio  bool
	ChatContext       *llm.ChatContext
	ExtraInstructions string
	Instructions      *beta.InstructionParts
	Tools             []llm.Tool
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
	RingingTimeoutSet bool
	HoldAudio         interface{}

	callerRoom            *lksdk.Room
	humanAgentSess        *agent.AgentSession
	humanAgentReady       bool
	humanAgentRoom        string
	humanAgentIdentity    string
	humanAgentCloseCancel context.CancelFunc
	callerCloseCancel     context.CancelFunc

	backgroundAudio          warmTransferBackgroundAudio
	holdAudioHandle          warmTransferHoldAudioHandle
	newBackgroundAudioPlayer func(interface{}) warmTransferBackgroundAudio

	mu                            sync.Mutex
	callerInputAudioStateSet      bool
	originalCallerInputAudioMuted bool
	callerAudioOutputPaused       bool
	callerRoomCleanupArmed        bool
	callerRoomCleanupDone         bool
}

type warmTransferHoldAudioHandle interface {
	Stop()
}

type warmTransferBackgroundAudio interface {
	Start(room *lksdk.Room, agentSession *agent.AgentSession) error
	Play(audio interface{}, loop bool) warmTransferHoldAudioHandle
	Close() error
}

type warmTransferAgentBackgroundAudio struct {
	player *agent.BackgroundAudioPlayer
}

func newWarmTransferBackgroundAudio(holdAudio interface{}) warmTransferBackgroundAudio {
	return warmTransferAgentBackgroundAudio{player: agent.NewBackgroundAudioPlayer(nil, nil)}
}

func (p warmTransferAgentBackgroundAudio) Start(room *lksdk.Room, agentSession *agent.AgentSession) error {
	return p.player.Start(room, agentSession)
}

func (p warmTransferAgentBackgroundAudio) Play(audio interface{}, loop bool) warmTransferHoldAudioHandle {
	return p.player.Play(audio, loop)
}

func (p warmTransferAgentBackgroundAudio) Close() error {
	return p.player.Close()
}

type warmTransferJobContext interface {
	RoomInfo() *livekit.Room
	CreateSIPParticipant(ctx context.Context, req *livekit.CreateSIPParticipantRequest) (*livekit.SIPParticipantInfo, error)
	MoveParticipant(ctx context.Context, room string, identity string, destinationRoom string) error
	DeleteRoom(ctx context.Context, roomName string) (*livekit.DeleteRoomResponse, error)
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
	if targetPhone == "" {
		targetPhone = strings.TrimSpace(opts.TargetPhoneNumber)
	}
	trunkId := strings.TrimSpace(opts.TrunkID)
	if targetPhone == "" {
		return nil, fmt.Errorf("`sip_call_to` must be set")
	}
	if trunkId == "" && !opts.TrunkIDSet {
		if opts.SipConnection == nil {
			trunkId = strings.TrimSpace(os.Getenv("LIVEKIT_SIP_OUTBOUND_TRUNK"))
		}
	}
	if trunkId == "" && !opts.TrunkIDSet && opts.SipConnection == nil {
		return nil, fmt.Errorf("`LIVEKIT_SIP_OUTBOUND_TRUNK` environment variable, `sip_trunk_id`, or `sip_connection` must be set")
	}

	prevConvo := ""
	if opts.ChatContext != nil {
		for _, msg := range opts.ChatContext.Items {
			if m, ok := msg.(*llm.ChatMessage); ok && (m.Role == llm.ChatRoleUser || m.Role == llm.ChatRoleAssistant) {
				text := m.TextContent()
				if text == "" {
					continue
				}
				role := "Caller"
				if m.Role == llm.ChatRoleAssistant {
					role = "Assistant"
				}
				prevConvo += fmt.Sprintf("%s: %s\n", role, text)
			}
		}
	}

	instructions := fmt.Sprintf(WarmTransferBaseInstructions, prevConvo)
	if opts.Instructions != nil {
		instructions = applyInstructionParts(instructions, warmTransferPersona, opts.Instructions)
	} else if extra := strings.TrimSpace(opts.ExtraInstructions); extra != "" {
		instructions = strings.TrimRight(instructions, "\n") + "\n\n" + extra
	}

	sipNumber := strings.TrimSpace(opts.SipNumber)
	if sipNumber == "" && !opts.SipNumberSet {
		sipNumber = os.Getenv("LIVEKIT_SIP_NUMBER")
	}
	var holdAudio interface{} = agent.AudioConfig{
		Source: agent.HoldMusic,
		Volume: 0.8,
	}
	if opts.DisableHoldAudio {
		holdAudio = nil
	} else if opts.HoldAudio != nil {
		holdAudio = opts.HoldAudio
	}

	t := &WarmTransferTask{
		AgentTask:                *agent.NewAgentTask[*WarmTransferResult](instructions),
		TargetPhoneNumber:        targetPhone,
		SipTrunkID:               trunkId,
		SipConnection:            opts.SipConnection,
		humanAgentIdentity:       "human-agent-sip",
		SipNumber:                sipNumber,
		SipHeaders:               opts.SipHeaders,
		Dtmf:                     opts.Dtmf,
		RingingTimeout:           opts.RingingTimeout,
		RingingTimeoutSet:        opts.RingingTimeoutSet,
		HoldAudio:                holdAudio,
		newBackgroundAudioPlayer: newWarmTransferBackgroundAudio,
	}

	t.Agent.Tools = append(append([]llm.Tool{}, opts.Tools...),
		&connectToCallerTool{task: t},
		&declineTransferTool{task: t},
		&voicemailDetectedTool{task: t},
	)
	applyAgentOptions(&t.Agent, opts.AgentOptions)

	return t, nil
}

func (t *WarmTransferTask) OnEnter() {
	t.mu.Lock()
	defer t.mu.Unlock()

	logger.Logger.Infow("Entering warm transfer task, dialing human agent", "target", t.TargetPhoneNumber)
	var session *agent.AgentSession
	if activity := t.GetActivity(); activity != nil && activity.Session != nil {
		session = activity.Session
		t.callerRoom = session.Room
	}
	t.muteCallerInputAudioLocked()
	t.pauseCallerAudioOutputLocked()
	t.watchHumanAgentSessionCloseLocked()

	if t.HoldAudio != nil {
		factory := t.newBackgroundAudioPlayer
		if factory == nil {
			factory = newWarmTransferBackgroundAudio
		}
		t.backgroundAudio = factory(t.HoldAudio)
		if t.callerRoom != nil {
			if err := t.backgroundAudio.Start(t.callerRoom, session); err != nil {
				logger.Logger.Warnw("could not start warm transfer hold audio", err)
			} else {
				t.holdAudioHandle = t.backgroundAudio.Play(t.HoldAudio, true)
			}
		}
	}

	jobCtx, err := t.jobContext()
	if err != nil {
		logger.Logger.Warnw("could not dial human agent", err)
		t.setResultLocked(nil, llm.NewToolError("could not dial human agent"))
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
	if t.RingingTimeout > 0 || t.RingingTimeoutSet {
		req.RingingTimeout = durationpb.New(t.RingingTimeout)
	}
	_, err = jobCtx.CreateSIPParticipant(context.Background(), req)
	if err != nil {
		logger.Logger.Warnw("could not dial human agent", err)
		t.setResultLocked(nil, llm.NewToolError("could not dial human agent"))
		return
	}
	t.humanAgentReady = true
	t.humanAgentRoom = req.RoomName
}

func (t *WarmTransferTask) OnExit() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.restoreCallerInputAudioLocked()
	t.restoreCallerAudioOutputLocked()
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
	if t.humanAgentCloseCancel != nil {
		t.humanAgentCloseCancel()
		t.humanAgentCloseCancel = nil
	}
	t.humanAgentReady = false
	t.humanAgentRoom = ""
}

func (t *WarmTransferTask) setResultLocked(result *WarmTransferResult, err error) {
	if t.Done() {
		return
	}
	t.cleanupResultResourcesLocked()
	t.restoreCallerInputAudioLocked()
	t.restoreCallerAudioOutputLocked()
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

func (t *WarmTransferTask) onHumanAgentRoomClosed(reason string) {
	t.setResult(nil, llm.NewToolError(fmt.Sprintf("room closed: %s", reason)))
}

func (t *WarmTransferTask) watchHumanAgentSessionCloseLocked() {
	if t.humanAgentSess == nil || t.humanAgentCloseCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	closeEvents := t.humanAgentSess.CloseEvents()
	t.humanAgentCloseCancel = cancel
	go func() {
		select {
		case <-ctx.Done():
			return
		case ev := <-closeEvents:
			t.onHumanAgentRoomClosed(string(ev.Reason))
		}
	}()
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
	if t.humanAgentSess == nil && !t.humanAgentReady {
		return fmt.Errorf("human agent is not ready")
	}
	destinationRoom := t.callerRoomName(jobCtx)
	humanAgentRoom := t.humanAgentRoomName(destinationRoom)
	if err := jobCtx.MoveParticipant(context.Background(), humanAgentRoom, t.humanAgentIdentity, destinationRoom); err != nil {
		return err
	}
	if humanAgentRoom != "" {
		if _, err := jobCtx.DeleteRoom(context.Background(), humanAgentRoom); err != nil {
			logger.Logger.Warnw("could not delete warm transfer human-agent room", err, "room", humanAgentRoom)
		}
	}
	t.callerRoomCleanupArmed = true
	t.callerRoomCleanupDone = false
	t.watchCallerSessionCloseLocked()

	t.setResultLocked(&WarmTransferResult{HumanAgentIdentity: t.humanAgentIdentity}, nil)
	return nil
}

func (t *WarmTransferTask) onCallerParticipantDisconnected(identity string, kind livekit.ParticipantInfo_Kind) {
	if kind != livekit.ParticipantInfo_CONNECTOR &&
		kind != livekit.ParticipantInfo_SIP &&
		kind != livekit.ParticipantInfo_STANDARD {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.callerRoomCleanupArmed || t.callerRoomCleanupDone {
		return
	}
	jobCtx, err := t.jobContext()
	if err != nil {
		logger.Logger.Warnw("could not delete warm transfer caller room after participant disconnect", err, "participant", identity)
		return
	}
	roomName := t.callerRoomName(jobCtx)
	if roomName == "" {
		return
	}
	if _, err := jobCtx.DeleteRoom(context.Background(), roomName); err != nil {
		logger.Logger.Warnw("could not delete warm transfer caller room", err, "room", roomName, "participant", identity)
		return
	}
	t.callerRoomCleanupDone = true
	if t.callerCloseCancel != nil {
		t.callerCloseCancel()
		t.callerCloseCancel = nil
	}
}

func (t *WarmTransferTask) watchCallerSessionCloseLocked() {
	session := t.callerSessionLocked()
	if session == nil || t.callerCloseCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	closeEvents := session.CloseEvents()
	t.callerCloseCancel = cancel
	go func() {
		select {
		case <-ctx.Done():
			return
		case ev := <-closeEvents:
			if ev.Reason == agent.CloseReasonParticipantDisconnected {
				t.onCallerParticipantDisconnected("", livekit.ParticipantInfo_STANDARD)
			}
		}
	}()
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
	if t.humanAgentRoom != "" {
		return t.humanAgentRoom
	}
	if callerRoomName != "" {
		return callerRoomName + "-human-agent"
	}
	return ""
}

func (t *WarmTransferTask) callerSessionLocked() *agent.AgentSession {
	activity := t.Agent.GetActivity()
	if activity == nil {
		return nil
	}
	return activity.Session
}

func (t *WarmTransferTask) muteCallerInputAudioLocked() {
	session := t.callerSessionLocked()
	if session == nil {
		return
	}
	if !t.callerInputAudioStateSet {
		t.originalCallerInputAudioMuted = session.InputAudioMuted()
		t.callerInputAudioStateSet = true
	}
	session.SetInputAudioMuted(true)
}

func (t *WarmTransferTask) restoreCallerInputAudioLocked() {
	if !t.callerInputAudioStateSet {
		return
	}
	session := t.callerSessionLocked()
	if session != nil {
		session.SetInputAudioMuted(t.originalCallerInputAudioMuted)
	}
	t.callerInputAudioStateSet = false
}

func (t *WarmTransferTask) pauseCallerAudioOutputLocked() {
	if t.callerAudioOutputPaused {
		return
	}
	session := t.callerSessionLocked()
	if session == nil {
		return
	}
	controller := session.AudioOutputController()
	if controller == nil || !controller.CanPauseAudioOutput() {
		return
	}
	controller.PauseAudioOutput()
	t.callerAudioOutputPaused = true
}

func (t *WarmTransferTask) restoreCallerAudioOutputLocked() {
	if !t.callerAudioOutputPaused {
		return
	}
	session := t.callerSessionLocked()
	if session != nil {
		if controller := session.AudioOutputController(); controller != nil {
			controller.ResumeAudioOutput()
		}
	}
	t.callerAudioOutputPaused = false
}

type connectToCallerTool struct {
	task *WarmTransferTask
}

func (t *connectToCallerTool) ID() string   { return "connect_to_caller" }
func (t *connectToCallerTool) Name() string { return "connect_to_caller" }
func (t *connectToCallerTool) ToolFlags() llm.ToolFlag {
	return llm.ToolFlagIgnoreOnEnter
}
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
	return "", nil
}

type declineTransferTool struct {
	task *WarmTransferTask
}

func (t *declineTransferTool) ID() string   { return "decline_transfer" }
func (t *declineTransferTool) Name() string { return "decline_transfer" }
func (t *declineTransferTool) ToolFlags() llm.ToolFlag {
	return llm.ToolFlagIgnoreOnEnter
}
func (t *declineTransferTool) Description() string {
	return "Handles the case when the human agent explicitly declines to connect to the caller."
}
func (t *declineTransferTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reason": map[string]any{"type": "string", "description": "A short explanation of why the human agent declined to connect to the caller"},
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
	return "", nil
}

type voicemailDetectedTool struct {
	task *WarmTransferTask
}

func (t *voicemailDetectedTool) ID() string   { return "voicemail_detected" }
func (t *voicemailDetectedTool) Name() string { return "voicemail_detected" }
func (t *voicemailDetectedTool) ToolFlags() llm.ToolFlag {
	return llm.ToolFlagIgnoreOnEnter
}
func (t *voicemailDetectedTool) Description() string {
	return "Called when the call reaches voicemail. Use this tool AFTER you hear the voicemail greeting"
}
func (t *voicemailDetectedTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *voicemailDetectedTool) Execute(ctx context.Context, args string) (string, error) {
	t.task.setResult(nil, llm.NewToolError("voicemail detected"))
	return "", nil
}
