package agent

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/core/llm"
	"github.com/cavos-io/conversation-worker/core/stt"
	"github.com/cavos-io/conversation-worker/core/tts"
	"github.com/cavos-io/conversation-worker/core/vad"
	"github.com/cavos-io/conversation-worker/library/logger"
	"github.com/cavos-io/conversation-worker/library/telemetry"
	"github.com/cavos-io/conversation-worker/model"
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

var ErrAgentSessionNotRunning = errors.New("agent session is not running")

type GenerateReplyOptions struct {
	UserInput          string
	AllowInterruptions *bool
	InputModality      string
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

	mu       sync.Mutex
	activity *AgentActivity
	started  bool

	// Event channels
	AgentStateChangedCh chan AgentStateChangedEvent
	UserStateChangedCh  chan UserStateChangedEvent
	sipDTMFCh           chan SipDTMFEvent
}

type SipDTMFEvent struct {
	Digit          string
	Code           uint32
	SenderIdentity string
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
		sipDTMFCh:           make(chan SipDTMFEvent, 10),
	}
}

func (s *AgentSession) SipDTMFEvents() <-chan SipDTMFEvent {
	return s.sipDTMFEvents()
}

func (s *AgentSession) EmitSipDTMF(ev SipDTMFEvent) {
	ch := s.sipDTMFEvents()
	select {
	case ch <- ev:
	default:
	}
}

func (s *AgentSession) sipDTMFEvents() chan SipDTMFEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sipDTMFCh == nil {
		s.sipDTMFCh = make(chan SipDTMFEvent, 10)
	}
	return s.sipDTMFCh
}

func (s *AgentSession) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return nil
	}

	if s.VAD == nil {
		s.VAD = vad.NewSimpleVAD(0.05)
	}

	if s.Assistant == nil {
		s.Assistant = NewPipelineAgent(s.VAD, s.STT, s.LLM, s.TTS, s.ChatCtx)
	}

	if err := s.Assistant.Start(ctx, s); err != nil {
		return err
	}

	s.activity = NewAgentActivity(s.Agent, s)
	s.activity.Start()

	// Trigger periodic usage metrics reporting
	if s.MetricsCollector != nil {
		go s.reportUsageLoop(ctx)
	}

	s.started = true
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

func (s *AgentSession) GenerateReply(ctx context.Context, userInput string) (*SpeechHandle, error) {
	return s.GenerateReplyWithOptions(ctx, GenerateReplyOptions{
		UserInput:     userInput,
		InputModality: "text",
	})
}

func (s *AgentSession) GenerateReplyWithOptions(ctx context.Context, opts GenerateReplyOptions) (*SpeechHandle, error) {
	s.mu.Lock()
	activity := s.activity
	s.mu.Unlock()

	if activity == nil {
		return nil, ErrAgentSessionNotRunning
	}

	// Trigger the pipeline
	logger.Logger.Infow("Generating reply", "userInput", opts.UserInput)

	allowInterruptions := s.Options.AllowInterruptions
	if opts.AllowInterruptions != nil {
		allowInterruptions = *opts.AllowInterruptions
	}
	inputModality := opts.InputModality
	if inputModality == "" {
		inputModality = "text"
	}
	handle := NewSpeechHandle(allowInterruptions, InputDetails{Modality: inputModality})

	// Add user message to ChatContext if provided
	if opts.UserInput != "" {
		s.ChatCtx.Append(&llm.ChatMessage{
			Role: llm.ChatRoleUser,
			Content: []llm.ChatContent{
				{Text: opts.UserInput},
			},
			CreatedAt: time.Now(),
		})
	}

	// Schedule the speech
	if err := activity.ScheduleSpeech(handle, SpeechPriorityNormal, false); err != nil {
		return nil, err
	}
	return handle, nil
}

func (s *AgentSession) Interrupt(force bool) error {
	s.mu.Lock()
	activity := s.activity
	s.mu.Unlock()

	if activity == nil {
		return ErrAgentSessionNotRunning
	}

	return activity.Interrupt(force)
}

func (s *AgentSession) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started {
		return nil
	}

	s.activity.Stop()
	s.activity = nil
	s.started = false
	return nil
}
