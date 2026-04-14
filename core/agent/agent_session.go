package agent

import (
	"context"
	"fmt"
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
	SpeakingRate                  float64
	LinkedParticipant             lksdk.Participant
}

type AgentSession struct {
	Options AgentSessionOptions

	ChatCtx   *llm.ChatContext
	Agent     AgentInterface
	STT       stt.STT
	VAD       vad.VAD
	LLM       llm.LLM
	TTS       tts.TTS
	Tools     []interface{}
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
	closing  bool

	// Event channels
	AgentStateChangedCh chan AgentStateChangedEvent
	UserStateChangedCh  chan UserStateChangedEvent

	clientEvents *ClientEventsDispatcher
}

func NewAgentSession(agent AgentInterface, room *lksdk.Room, opts AgentSessionOptions) *AgentSession {
	baseAgent := agent.GetAgent()
	chatCtx := baseAgent.ChatCtx
	if chatCtx == nil {
		chatCtx = llm.NewChatContext()
		baseAgent.ChatCtx = chatCtx
	}

	tools := make([]interface{}, len(baseAgent.Tools))
	copy(tools, baseAgent.Tools)

	timeline := NewEventTimeline()

	if opts.SpeakingRate <= 0 {
		opts.SpeakingRate = 3.83
	}

	s := &AgentSession{
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

	if chatCtx != nil {
		chatCtx.OnItemAdded = func(item llm.ChatItem) {
			if s.Timeline != nil {
				s.Timeline.AddEvent(&ConversationItemAddedEvent{
					Item:      item,
					CreatedAt: time.Now(),
				})
			}
		}
	}

	if room != nil {
		s.clientEvents = NewClientEventsDispatcher(room, s)
	}

	return s
}

func (s *AgentSession) SetAudioOutput(out AudioOutput) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Output.Audio = out
	if out != nil {
		out.OnPlaybackStarted(func(ev PlaybackStartedEvent) {
			s.UpdateAgentState(AgentStateSpeaking)
		})
		out.OnPlaybackFinished(func(ev PlaybackFinishedEvent) {
			if !ev.Interrupted {
				s.UpdateAgentState(AgentStateListening)
			}
		})
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

	s.UpdateAgentState(AgentStateListening)

	return nil
}

func (s *AgentSession) Close() error {
	s.mu.Lock()
	if !s.started || s.closing {
		s.mu.Unlock()
		return nil
	}
	s.closing = true
	s.started = false
	activity := s.Activity
	assistant := s.Assistant
	s.mu.Unlock()

	if activity != nil {
		activity.Stop()
	}

	if assistant != nil {
		assistant.Stop()
	}

	if s.Timeline != nil {
		s.Timeline.AddEvent(&CloseEvent{
			Reason:    CloseReasonUserInitiated,
			CreatedAt: time.Now(),
		})
	}
	s.UpdateAgentState(AgentStateIdle)

	return nil
}

func (s *AgentSession) UpdateAgent(agent AgentInterface) error {
	s.mu.Lock()
	if !s.started || s.closing {
		s.mu.Unlock()
		return fmt.Errorf("session not started or closing")
	}
	oldActivity := s.Activity
	
	// Create and start new activity
	s.Agent = agent
	newActivity := NewAgentActivity(s.Agent, s)
	s.Activity = newActivity
	s.mu.Unlock()

	if oldActivity != nil {
		oldActivity.Stop()
	}
	newActivity.Start()

	return nil
}

func (s *AgentSession) ClearUserTurn() {
	s.mu.Lock()
	activity := s.Activity
	s.mu.Unlock()
	if activity != nil {
		activity.ClearUserTurn()
	}
}

func (s *AgentSession) CommitUserTurn() {
	s.mu.Lock()
	activity := s.Activity
	s.mu.Unlock()
	if activity != nil {
		activity.CommitUserTurn()
	}
}

func (s *AgentSession) Pause() error {
	s.mu.Lock()
	activity := s.Activity
	s.mu.Unlock()
	if activity != nil {
		return activity.Pause()
	}
	return nil
}

func (s *AgentSession) Resume() error {
	s.mu.Lock()
	activity := s.Activity
	s.mu.Unlock()
	if activity != nil {
		return activity.Resume()
	}
	return nil
}

func (s *AgentSession) UpdateOptions(opts AgentSessionOptions) {
	s.mu.Lock()
	s.Options = opts
	activity := s.Activity
	s.mu.Unlock()
	if activity != nil {
		activity.UpdateOptions(opts)
	}
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
				
				if s.Timeline != nil {
					s.Timeline.AddEvent(&MetricsCollectedEvent{
						Metrics:   &summary,
						CreatedAt: time.Now(),
					})
				}
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
			s.Timeline.AddEvent(&AgentStateChangedEvent{
				OldState:  oldState,
				NewState:  state,
				CreatedAt: time.Now(),
			})
		}
		
		if s.clientEvents != nil {
			s.clientEvents.DispatchAgentState(state)
		}

		select {
		case s.AgentStateChangedCh <- AgentStateChangedEvent{
			OldState:  oldState,
			NewState:  state,
			CreatedAt: time.Now(),
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
			s.Timeline.AddEvent(&UserStateChangedEvent{
				OldState:  oldState,
				NewState:  state,
				CreatedAt: time.Now(),
			})
		}
		
		if s.clientEvents != nil {
			s.clientEvents.DispatchUserState(state)
		}

		select {
		case s.UserStateChangedCh <- UserStateChangedEvent{
			OldState:  oldState,
			NewState:  state,
			CreatedAt: time.Now(),
		}:
		default:
			// Channel full, ignore
		}
	}
}

func GenerateTypedReply[T any](ctx context.Context, s *AgentSession, userInput string) (*RunResult[T], error) {
	s.mu.Lock()
	activity := s.Activity
	s.mu.Unlock()

	if activity == nil {
		return nil, fmt.Errorf("agent activity not started")
	}

	// Trigger the pipeline
	logger.Logger.Infow("Generating reply", "userInput", userInput)

	// Create a speech handle
	handle := NewSpeechHandle(s.Options.AllowInterruptions, DefaultInputDetails())
	
	participantID := ""
	if s.Room != nil && s.Room.LocalParticipant != nil {
		participantID = s.Room.LocalParticipant.Identity()
	}

	if s.Timeline != nil {
		s.Timeline.AddEvent(&SpeechCreatedEvent{
			UserInitiated: true,
			Source:        "generate_reply",
			SpeechHandle:  handle,
			ParticipantID: participantID,
			CreatedAt:     time.Now(),
		})
	}
	
	// Create run result and watch the new handle
	runResult := NewRunResult[T](s.ChatCtx)
	runResult.WatchHandle(ctx, handle)
	handle.RunResult = runResult

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
	err := activity.ScheduleSpeech(handle, SpeechPriorityNormal, false)
	if err != nil {
		return nil, err
	}

	return runResult, nil
}

func (s *AgentSession) GenerateReply(ctx context.Context, userInput string) (*RunResult[any], error) {
	return GenerateTypedReply[any](ctx, s, userInput)
}

func (s *AgentSession) TimelineSnapshot() []TimelineEvent {
	if s == nil || s.Timeline == nil {
		return nil
	}
	return s.Timeline.Snapshot()
}

func (s *AgentSession) Interrupt(ctx context.Context) error {
	s.mu.Lock()
	activity := s.Activity
	s.mu.Unlock()

	if activity != nil {
		return activity.Interrupt(true)
	}
	return nil
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
		s.Timeline.AddEvent(&CloseEvent{
			Reason:    CloseReasonJobShutdown,
			CreatedAt: time.Now(),
		})
	}

	s.mu.Lock()
	if s.Activity == activity {
		s.Activity = nil
	}
	s.mu.Unlock()

	return nil
}
