package agent

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/telemetry"
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

var (
	ErrAgentSessionNotRunning = errors.New("agent session is not running")
	ErrAgentSessionNestedRun  = errors.New("nested agent session runs are not supported")
)

type GenerateReplyOptions struct {
	UserInput          string
	AllowInterruptions *bool
	InputModality      string
}

type RunOptions struct {
	UserInput          string
	AllowInterruptions *bool
	InputModality      string
	OutputType         reflect.Type
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
	runState *RunResult

	// Event channels
	AgentStateChangedCh chan AgentStateChangedEvent
	UserStateChangedCh  chan UserStateChangedEvent
	speechCreatedCh     chan SpeechCreatedEvent
	conversationItemCh  chan ConversationItemAddedEvent
	functionToolsCh     chan FunctionToolsExecutedEvent
	sipDTMFCh           chan SipDTMFEvent
	closeCh             chan CloseEvent
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
	OldState  AgentState
	NewState  AgentState
	CreatedAt time.Time
}

func (e *AgentStateChangedEvent) GetType() string { return "agent_state_changed" }

type UserStateChangedEvent struct {
	OldState  UserState
	NewState  UserState
	CreatedAt time.Time
}

func (e *UserStateChangedEvent) GetType() string { return "user_state_changed" }

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
		speechCreatedCh:     make(chan SpeechCreatedEvent, 10),
		conversationItemCh:  make(chan ConversationItemAddedEvent, 10),
		functionToolsCh:     make(chan FunctionToolsExecutedEvent, 10),
		sipDTMFCh:           make(chan SipDTMFEvent, 10),
	}
}

func (s *AgentSession) SpeechCreatedEvents() <-chan SpeechCreatedEvent {
	return s.speechCreatedEvents()
}

func (s *AgentSession) EmitSpeechCreated(ev SpeechCreatedEvent) {
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now()
	}
	ch := s.speechCreatedEvents()
	select {
	case ch <- ev:
	default:
	}
}

func (s *AgentSession) speechCreatedEvents() chan SpeechCreatedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.speechCreatedCh == nil {
		s.speechCreatedCh = make(chan SpeechCreatedEvent, 10)
	}
	return s.speechCreatedCh
}

func (s *AgentSession) ConversationItemAddedEvents() <-chan ConversationItemAddedEvent {
	return s.conversationItemAddedEvents()
}

func (s *AgentSession) EmitConversationItemAdded(item llm.ChatItem) {
	if item == nil {
		return
	}
	s.insertChatItem(item)
	ch := s.conversationItemAddedEvents()
	select {
	case ch <- ConversationItemAddedEvent{Item: item, CreatedAt: time.Now()}:
	default:
	}
}

func (s *AgentSession) conversationItemAddedEvents() chan ConversationItemAddedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conversationItemCh == nil {
		s.conversationItemCh = make(chan ConversationItemAddedEvent, 10)
	}
	return s.conversationItemCh
}

func (s *AgentSession) insertChatItem(item llm.ChatItem) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ChatCtx == nil {
		s.ChatCtx = llm.NewChatContext()
	}
	for _, existing := range s.ChatCtx.Items {
		if existing == item {
			return
		}
		if item.GetID() != "" && existing.GetID() == item.GetID() && existing.GetType() == item.GetType() {
			return
		}
	}
	s.ChatCtx.Insert(item)
}

func (s *AgentSession) FunctionToolsExecutedEvents() <-chan FunctionToolsExecutedEvent {
	return s.functionToolsExecutedEvents()
}

func (s *AgentSession) EmitFunctionToolsExecuted(ev FunctionToolsExecutedEvent) {
	for _, call := range ev.FunctionCalls {
		s.insertChatItem(call)
	}
	for _, output := range ev.FunctionCallOutputs {
		if output != nil {
			s.insertChatItem(output)
		}
	}
	ch := s.functionToolsExecutedEvents()
	select {
	case ch <- ev:
	default:
	}
}

func (s *AgentSession) functionToolsExecutedEvents() chan FunctionToolsExecutedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.functionToolsCh == nil {
		s.functionToolsCh = make(chan FunctionToolsExecutedEvent, 10)
	}
	return s.functionToolsCh
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

func (s *AgentSession) CloseEvents() <-chan CloseEvent {
	return s.closeEvents()
}

func (s *AgentSession) CloseSoon(reason CloseReason) {
	ch := s.closeEvents()
	select {
	case ch <- CloseEvent{Reason: reason, CreatedAt: time.Now()}:
	default:
	}
	_ = s.Stop(context.Background())
}

func (s *AgentSession) closeEvents() chan CloseEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closeCh == nil {
		s.closeCh = make(chan CloseEvent, 10)
	}
	return s.closeCh
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

func (s *AgentSession) GenerateReply(ctx context.Context, userInput string) (*SpeechHandle, error) {
	return s.GenerateReplyWithOptions(ctx, GenerateReplyOptions{
		UserInput:     userInput,
		InputModality: "text",
	})
}

func (s *AgentSession) Run(ctx context.Context, userInput string) (*RunResult, error) {
	return s.RunWithOptions(ctx, RunOptions{
		UserInput:     userInput,
		InputModality: "text",
	})
}

func (s *AgentSession) RunWithOptions(ctx context.Context, opts RunOptions) (*RunResult, error) {
	s.mu.Lock()
	if s.runState != nil && !s.runState.Done() {
		s.mu.Unlock()
		return nil, ErrAgentSessionNestedRun
	}
	result := newRunResult(s.ChatCtx, opts.UserInput, opts.OutputType)
	s.runState = result
	s.mu.Unlock()

	handle, err := s.GenerateReplyWithOptions(ctx, GenerateReplyOptions{
		UserInput:          opts.UserInput,
		AllowInterruptions: opts.AllowInterruptions,
		InputModality:      opts.InputModality,
	})
	if err != nil {
		s.mu.Lock()
		if s.runState == result {
			s.runState = nil
		}
		s.mu.Unlock()
		return nil, err
	}

	result.WatchSpeechHandle(handle)
	return result, nil
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
	s.EmitSpeechCreated(SpeechCreatedEvent{
		UserInitiated: true,
		Source:        "generate_reply",
		SpeechHandle:  handle,
	})

	var userMessage *llm.ChatMessage
	if opts.UserInput != "" {
		userMessage = &llm.ChatMessage{
			Role: llm.ChatRoleUser,
			Content: []llm.ChatContent{
				{Text: opts.UserInput},
			},
			CreatedAt: time.Now(),
		}
	}

	// Schedule the speech
	if err := activity.ScheduleSpeech(handle, SpeechPriorityNormal, false); err != nil {
		return nil, err
	}
	if userMessage != nil {
		s.EmitConversationItemAdded(userMessage)
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

	if s.activity != nil {
		s.activity.Stop()
	}
	s.activity = nil
	s.started = false
	return nil
}
