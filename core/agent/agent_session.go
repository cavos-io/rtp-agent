package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/core/llm"
	"github.com/cavos-io/conversation-worker/core/stt"
	"github.com/cavos-io/conversation-worker/core/tts"
	"github.com/cavos-io/conversation-worker/core/vad"
	"github.com/cavos-io/conversation-worker/library/logger"
	"github.com/cavos-io/conversation-worker/library/telemetry"
	"github.com/cavos-io/conversation-worker/model"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

// sendChatToPlayground tries multiple approaches to get a message
// into the Playground chat panel. We send all three approaches
// simultaneously to find out which one the Playground accepts.
func sendChatToPlayground(room *lksdk.Room, msgID string, text string) {
	topic := "lk.chat"

	// Approach A: text stream (SendText) — required by @livekit/components-react >= 2.x
	room.LocalParticipant.SendText(text, lksdk.StreamTextOptions{
		Topic:    topic,
		StreamId: &msgID,
	})

	// Approach B: UserDataPacket + topic "lk.chat" + LiveKit JSON format
	payload, _ := json.Marshal(map[string]interface{}{
		"id":        msgID,
		"message":   text,
		"timestamp": time.Now().UnixMilli(),
	})
	pkt := &lksdk.UserDataPacket{Payload: payload, Topic: topic}
	_ = room.LocalParticipant.PublishDataPacket(pkt, lksdk.WithDataPublishReliable(true))
}

type UserState string
type AgentState string

const (
	UserStateSpeaking  UserState = "speaking"
	UserStateListening UserState = "listening"
	UserStateAway      UserState = "away"

	AgentStateInitializing AgentState = "initializing"
	AgentStateIdle         AgentState = "idle"
	AgentStateListening    AgentState = "listening"
	AgentStateThinking     AgentState = "thinking"
	AgentStateSpeaking     AgentState = "speaking"
)

type AgentSessionOptions struct {
	AllowInterruptions            bool
	DiscardAudioIfUninterruptible bool
	MinInterruptionDuration       float64
	MinInterruptionWords          int
	MinEndpointingDelay           float64
	MaxEndpointingDelay           float64
	MaxToolSteps                  int
	UserAwayTimeout               float64
	FalseInterruptionTimeout      float64
	ResumeFalseInterruption       bool
	MinConsecutiveSpeechDelay     float64
	UseTTSAlignedTranscript       bool
	PreemptiveGeneration          bool
	AECWarmupDuration             float64
}

type AgentSession struct {
	Options AgentSessionOptions

	ChatCtx   *llm.ChatContext
	Agent     AgentInterface
	STT       stt.STT
	VAD       vad.VAD
	LLM       llm.LLM
	TTS       tts.TTS
	Tools     []llm.Tool
	Assistant *PipelineAgent
	Room      *lksdk.Room

	MetricsCollector *telemetry.UsageCollector

	UserState  UserState
	AgentState AgentState

	// Transcript attribution — set by RoomIO when tracks are established.
	RemoteUserIdentity string
	RemoteTrackSID     string
	AgentTrackSID      string

	mu       sync.Mutex
	activity *AgentActivity
	started  bool

	// Event channels
	AgentStateChangedCh chan AgentStateChangedEvent
	UserStateChangedCh  chan UserStateChangedEvent
}

// transcriptionPacket wraps livekit.Transcription to implement the DataPacket interface.
type transcriptionPacket struct {
	t *livekit.Transcription
}

func (p *transcriptionPacket) ToProto() *livekit.DataPacket {
	return &livekit.DataPacket{
		Value: &livekit.DataPacket_Transcription{Transcription: p.t},
	}
}

func (s *AgentSession) OnAudioFrame(ctx context.Context, frame *model.AudioFrame) {
	s.mu.Lock()
	assistant := s.Assistant
	s.mu.Unlock()

	if assistant != nil {
		assistant.OnAudioFrame(ctx, frame)
	}
}

type AgentStateChangedEvent struct {
	OldState AgentState
	NewState AgentState
}

type UserStateChangedEvent struct {
	OldState UserState
	NewState UserState
}

func NewAgentSession(agent AgentInterface, room *lksdk.Room, opts AgentSessionOptions) *AgentSession {
	baseAgent := agent.GetAgent()
	return &AgentSession{
		Agent:               agent,
		Room:                room,
		STT:                 baseAgent.STT,
		VAD:                 baseAgent.VAD,
		LLM:                 baseAgent.LLM,
		TTS:                 baseAgent.TTS,
		Options:             opts,
		ChatCtx:             llm.NewChatContext(),
		Tools:               make([]llm.Tool, 0),
		AgentStateChangedCh: make(chan AgentStateChangedEvent, 10),
		UserStateChangedCh:  make(chan UserStateChangedEvent, 10),
	}
}

func (s *AgentSession) Start(ctx context.Context) error {
	s.mu.Lock()

	if s.started {
		s.mu.Unlock()
		return nil
	}

	if s.VAD == nil {
		s.VAD = vad.NewSimpleVAD(0.002)
	}

	if s.Assistant == nil {
		s.Assistant = NewPipelineAgent(s.VAD, s.STT, s.LLM, s.TTS, s.ChatCtx)
	}

	if err := s.Assistant.Start(ctx, s); err != nil {
		s.mu.Unlock()
		return err
	}

	s.activity = NewAgentActivity(s.Agent, s)
	s.activity.Start()

	// Trigger periodic usage metrics reporting
	if s.MetricsCollector != nil {
		go s.reportUsageLoop(ctx)
	}

	s.started = true
	s.mu.Unlock()

	// UpdateAgentState acquires s.mu, so call AFTER releasing
	s.UpdateAgentState(AgentStateListening)

	return nil
}

func (s *AgentSession) reportUsageLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.MetricsCollector != nil && s.ChatCtx != nil {
				summary := s.MetricsCollector.GetSummary()
				s.ChatCtx.Append(&llm.MetricsReport{Usage: summary})
			}
		}
	}
}

func (s *AgentSession) UpdateAgentState(state AgentState) {
	s.mu.Lock()
	oldState := s.AgentState
	s.AgentState = state
	room := s.Room
	s.mu.Unlock()

	if oldState != state {
		logger.Logger.Debugw("Agent state changed", "old", oldState, "new", state)

		// Publish state to Playground via LiveKit participant attributes.
		// The Playground reads "lk.agent.state" to display agent status and
		// resolve "Waiting for agent audio track…".
		if room != nil && room.LocalParticipant != nil {
			room.LocalParticipant.SetAttributes(map[string]string{
				"lk.agent.state": string(state),
			})
		}

		select {
		case s.AgentStateChangedCh <- AgentStateChangedEvent{
			OldState: oldState,
			NewState: state,
		}:
		default:
			// Channel full, ignore
		}
	}
}

func (s *AgentSession) UpdateUserState(state UserState) {
	s.mu.Lock()
	oldState := s.UserState
	s.UserState = state
	s.mu.Unlock()

	if oldState != state {
		logger.Logger.Debugw("User state changed", "old", oldState, "new", state)
		select {
		case s.UserStateChangedCh <- UserStateChangedEvent{
			OldState: oldState,
			NewState: state,
		}:
		default:
			// Channel full, ignore
		}
	}
}

func (s *AgentSession) GenerateReply(ctx context.Context, userInput string) error {
	s.mu.Lock()
	activity := s.activity
	s.mu.Unlock()

	if activity == nil {
		return nil
	}

	// Trigger the pipeline
	logger.Logger.Infow("Generating reply", "userInput", userInput)

	// Create a speech handle
	handle := NewSpeechHandle(s.Options.AllowInterruptions, DefaultInputDetails())

	// Add user message to ChatContext if provided
	if userInput != "" {
		s.ChatCtx.Append(&llm.ChatMessage{
			Role: llm.ChatRoleUser,
			Content: []llm.ChatContent{
				{Text: userInput},
			},
			CreatedAt: time.Now(),
		})
	}

	// Schedule the speech
	return activity.ScheduleSpeech(handle, SpeechPriorityNormal, false)
}

func (s *AgentSession) SetRemoteUserIdentity(identity string) {
	s.mu.Lock()
	s.RemoteUserIdentity = identity
	s.mu.Unlock()
}

func (s *AgentSession) SetRemoteTrackSID(sid string) {
	s.mu.Lock()
	s.RemoteTrackSID = sid
	s.mu.Unlock()
}

func (s *AgentSession) SetAgentTrackSID(sid string) {
	s.mu.Lock()
	s.AgentTrackSID = sid
	s.mu.Unlock()
}

func (s *AgentSession) GetAgentTrackSID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.AgentTrackSID
}

// PublishUserTranscript publishes the user's STT transcript to the Playground.
// Sends both a ChatMessage (chat panel) and a Transcription packet (transcript overlay).
func (s *AgentSession) PublishUserTranscript(text string) {
	if text == "" {
		return
	}
	s.mu.Lock()
	room := s.Room
	userIdentity := s.RemoteUserIdentity
	userTrackSID := s.RemoteTrackSID
	s.mu.Unlock()

	if room == nil || room.LocalParticipant == nil {
		return
	}

	now := time.Now()
	sendChatToPlayground(room, fmt.Sprintf("usr-%d", now.UnixNano()), text)

	// Transcription packet — displayed as real-time subtitle overlay.
	nowMs := uint64(now.UnixMilli())
	tpkt := &transcriptionPacket{t: &livekit.Transcription{
		TranscribedParticipantIdentity: userIdentity,
		TrackId:                        userTrackSID,
		Segments: []*livekit.TranscriptionSegment{{
			Id:        fmt.Sprintf("seg-usr-%d", now.UnixNano()),
			Text:      text,
			StartTime: nowMs,
			EndTime:   nowMs,
			Final:     true,
		}},
	}}
	if err := room.LocalParticipant.PublishDataPacket(tpkt, lksdk.WithDataPublishReliable(true)); err != nil {
		logger.Logger.Warnw("Failed to publish user transcription", err)
	} else {
		fmt.Printf("💬 [Transcript] User %q: %q\n", userIdentity, text)
	}
}

// PublishAgentTranscript publishes the agent's LLM response to the Playground.
// Sends both a ChatMessage (chat panel) and a Transcription packet (transcript overlay).
func (s *AgentSession) PublishAgentTranscript(text string) {
	if text == "" {
		return
	}
	s.mu.Lock()
	room := s.Room
	agentTrackSID := s.AgentTrackSID
	s.mu.Unlock()

	if room == nil || room.LocalParticipant == nil {
		return
	}

	agentIdentity := room.LocalParticipant.Identity()
	now := time.Now()
	sendChatToPlayground(room, fmt.Sprintf("agt-%d", now.UnixNano()), text)

	// Transcription packet — displayed as real-time subtitle overlay.
	nowMs := uint64(now.UnixMilli())
	tpkt := &transcriptionPacket{t: &livekit.Transcription{
		TranscribedParticipantIdentity: agentIdentity,
		TrackId:                        agentTrackSID,
		Segments: []*livekit.TranscriptionSegment{{
			Id:        fmt.Sprintf("seg-agt-%d", now.UnixNano()),
			Text:      text,
			StartTime: nowMs,
			EndTime:   nowMs,
			Final:     true,
		}},
	}}
	if err := room.LocalParticipant.PublishDataPacket(tpkt, lksdk.WithDataPublishReliable(true)); err != nil {
		logger.Logger.Warnw("Failed to publish agent transcription", err)
	} else {
		fmt.Printf("💬 [Transcript] Agent: %q\n", text)
	}
}

func (s *AgentSession) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started {
		return nil
	}

	s.activity.Stop()
	s.activity = nil

	if s.Assistant != nil {
		s.Assistant.cancel()
	}

	s.started = false
	return nil
}
