package agent

import (
	"context"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/core/llm"
	"github.com/cavos-io/conversation-worker/core/stt"
	"github.com/cavos-io/conversation-worker/core/tts"
	"github.com/cavos-io/conversation-worker/core/vad"
	"github.com/cavos-io/conversation-worker/library/logger"
	"github.com/cavos-io/conversation-worker/library/telemetry"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

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

	Input  AgentInput
	Output AgentOutput

	MetricsCollector *telemetry.UsageCollector
	Timeline         *EventTimeline

	UserState  UserState
	AgentState AgentState

	mu       sync.Mutex
	Activity *AgentActivity
	started  bool

	// Event channels
	AgentStateChangedCh chan AgentStateChangedEvent
	UserStateChangedCh  chan UserStateChangedEvent
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
	chatCtx := baseAgent.ChatCtx
	if chatCtx == nil {
		chatCtx = llm.NewChatContext()
		baseAgent.ChatCtx = chatCtx
	}

	tools := make([]llm.Tool, len(baseAgent.Tools))
	copy(tools, baseAgent.Tools)

	timeline := NewEventTimeline()
	timeline.Add("session_created", map[string]any{
		"has_room": room != nil,
	})

	return &AgentSession{
		Agent:               agent,
		Room:                room,
		STT:                 baseAgent.STT,
		VAD:                 baseAgent.VAD,
		LLM:                 baseAgent.LLM,
		TTS:                 baseAgent.TTS,
		Options:             opts,
		ChatCtx:             chatCtx,
		Tools:               tools,
		Timeline:            timeline,
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
		s.VAD = vad.NewSimpleVAD(0.01)
	}

	// AudioRecognition consumes session.STT directly. Ensure it is stream-capable.
	if s.STT != nil && !s.STT.Capabilities().Streaming {
		s.STT = stt.NewStreamAdapter(s.STT, s.VAD)
	}

	// Keep base agent fields aligned with effective session components.
	if base := s.Agent.GetAgent(); base != nil {
		base.VAD = s.VAD
		base.STT = s.STT
	}

	s.Activity = NewAgentActivity(s.Agent, s)
	s.Activity.Start()

	if s.Assistant == nil {
		s.Assistant = NewPipelineAgent(s.VAD, s.STT, s.LLM, s.TTS, s.ChatCtx)
	}

	if err := s.Assistant.Start(ctx, s); err != nil {
		s.Activity.Stop()
		s.Activity = nil
		s.mu.Unlock()
		return err
	}

	// Trigger periodic usage metrics reporting
	if s.MetricsCollector != nil {
		go s.reportUsageLoop(ctx)
	}

	s.started = true
	s.mu.Unlock()

	if s.Timeline != nil {
		s.Timeline.Add("session_started", nil)
	}
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
	s.mu.Unlock()

	if oldState != state {
		logger.Logger.Debugw("Agent state changed", "old", oldState, "new", state)
		if s.Timeline != nil {
			s.Timeline.Add("agent_state_changed", map[string]any{
				"old": string(oldState),
				"new": string(state),
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
		if s.Timeline != nil {
			s.Timeline.Add("user_state_changed", map[string]any{
				"old": string(oldState),
				"new": string(state),
			})
		}
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
	activity := s.Activity
	s.mu.Unlock()

	if activity == nil {
		return nil
	}

	// Trigger the pipeline
	logger.Logger.Infow("Generating reply", "userInput", userInput)
	if s.Timeline != nil {
		s.Timeline.Add("reply_requested", map[string]any{
			"has_user_input": userInput != "",
		})
	}

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

func (s *AgentSession) TimelineSnapshot() []TimelineEvent {
	if s == nil || s.Timeline == nil {
		return nil
	}
	return s.Timeline.Snapshot()
}

func (s *AgentSession) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	s.started = false
	activity := s.Activity
	assistant := s.Assistant
	s.mu.Unlock()

	if activity != nil {
		activity.PauseScheduling()
		if err := activity.Drain(ctx); err != nil && err != context.Canceled && err != context.DeadlineExceeded {
			logger.Logger.Errorw("failed draining agent activity", err)
		}
		activity.Stop()
	}

	if assistant != nil {
		assistant.Stop()
	}

	if s.Timeline != nil {
		s.Timeline.Add("session_stopped", nil)
	}

	s.mu.Lock()
	if s.Activity == activity {
		s.Activity = nil
	}
	s.mu.Unlock()

	return nil
}
