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
	"github.com/cavos-io/rtp-agent/library/tokenize"
	"github.com/cavos-io/rtp-agent/library/utils/images"
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
	TTSStreamPacer                *tts.SentenceStreamPacerOptions
	BackgroundAudio               *BackgroundAudioPlayer
	WordTokenizer                 tokenize.WordTokenizer
	PreemptiveGeneration          bool
	AECWarmupDuration             float64
	TurnDetection                 TurnDetectionMode
	IVRDetection                  bool
	IVRSilenceDuration            time.Duration
	VideoSampler                  *VoiceActivityVideoSampler
}

type AgentSessionUpdateOptions struct {
	MinEndpointingDelay *float64
	MaxEndpointingDelay *float64
	TurnDetection       *TurnDetectionMode
}

var (
	ErrAgentSessionNotRunning     = errors.New("agent session is not running")
	ErrAgentSessionNestedRun      = errors.New("nested agent session runs are not supported")
	ErrAgentSessionUserdataNotSet = errors.New("agent session userdata is not set")
)

type GenerateReplyOptions struct {
	UserInput          string
	UserMessage        *llm.ChatMessage
	Instructions       string
	ToolChoice         llm.ToolChoice
	Tools              []string
	ChatCtx            *llm.ChatContext
	AllowInterruptions *bool
	InputModality      string
}

type SayOptions struct {
	Text               string
	AllowInterruptions *bool
	AddToChatContext   *bool
}

type RunOptions struct {
	UserInput          string
	AllowInterruptions *bool
	InputModality      string
	OutputType         reflect.Type
}

type CommitUserTurnOptions struct {
	TranscriptTimeout time.Duration
	STTFlushDuration  time.Duration
	SkipReply         bool
}

type SessionAssistant interface {
	Start(ctx context.Context, s *AgentSession) error
	OnAudioFrame(ctx context.Context, frame *model.AudioFrame)
	SetPublishAudio(func(frame *model.AudioFrame) error)
}

type videoSessionAssistant interface {
	OnVideoFrame(ctx context.Context, frame *images.VideoFrame)
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
	Assistant SessionAssistant
	Room      *lksdk.Room

	MetricsCollector *telemetry.UsageCollector

	UserState  UserState
	AgentState AgentState

	mu             sync.Mutex
	activity       *AgentActivity
	started        bool
	runState       *RunResult
	userAwayTimer  *time.Timer
	userdata       any
	userdataSet    bool
	recordedEvents []Event
	ivrActivity    *IVRActivity
	videoSampler   *VoiceActivityVideoSampler

	// Event channels
	AgentStateChangedCh chan AgentStateChangedEvent
	UserStateChangedCh  chan UserStateChangedEvent
	userInputCh         chan UserInputTranscribedEvent
	agentOutputCh       chan AgentOutputTranscribedEvent
	speechCreatedCh     chan SpeechCreatedEvent
	falseInterruptionCh chan AgentFalseInterruptionEvent
	userTurnExceededCh  chan UserTurnExceededEvent
	overlappingSpeechCh chan OverlappingSpeechEvent
	conversationItemCh  chan ConversationItemAddedEvent
	functionToolsCh     chan FunctionToolsExecutedEvent
	metricsCollectedCh  chan MetricsCollectedEvent
	sessionUsageCh      chan SessionUsageUpdatedEvent
	errorCh             chan ErrorEvent
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

func (s *AgentSession) OnVideoFrame(ctx context.Context, frame *images.VideoFrame) {
	if frame == nil {
		return
	}
	s.mu.Lock()
	assistant := s.Assistant
	sampler := s.videoSampler
	s.mu.Unlock()

	if sampler != nil && !sampler.OnVideoFrame(ctx, frame) {
		return
	}
	if videoAssistant, ok := assistant.(videoSessionAssistant); ok {
		videoAssistant.OnVideoFrame(ctx, frame)
	}
}

func (s *AgentSession) CurrentSpeech() *SpeechHandle {
	s.mu.Lock()
	activity := s.activity
	s.mu.Unlock()
	if activity == nil {
		return nil
	}

	activity.queueMu.Lock()
	defer activity.queueMu.Unlock()
	return activity.currentSpeech
}

func (s *AgentSession) Userdata() (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.userdataSet {
		return nil, ErrAgentSessionUserdataNotSet
	}
	return s.userdata, nil
}

func (s *AgentSession) SetUserdata(value any) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.userdata = value
	s.userdataSet = true
}

func (s *AgentSession) RecordedEvents() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()

	events := make([]Event, len(s.recordedEvents))
	copy(events, s.recordedEvents)
	return events
}

func (s *AgentSession) recordEvent(ev Event) {
	if ev == nil {
		return
	}
	s.mu.Lock()
	s.recordedEvents = append(s.recordedEvents, ev)
	s.mu.Unlock()
}

func (s *AgentSession) CurrentAgent() (AgentInterface, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.Agent == nil {
		return nil, ErrAgentSessionNotRunning
	}
	return s.Agent, nil
}

func (s *AgentSession) WaitForInactive(ctx context.Context) error {
	s.mu.Lock()
	activity := s.activity
	s.mu.Unlock()
	if activity == nil {
		return nil
	}
	return activity.WaitForInactive(ctx)
}

func (s *AgentSession) Drain(ctx context.Context) error {
	s.mu.Lock()
	activity := s.activity
	s.mu.Unlock()
	if activity == nil {
		return ErrAgentSessionNotRunning
	}
	return activity.Drain(ctx)
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

func (s *AgentSession) AgentStateChangedEvents() <-chan AgentStateChangedEvent {
	return s.agentStateChangedEvents()
}

func (s *AgentSession) agentStateChangedEvents() chan AgentStateChangedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.AgentStateChangedCh == nil {
		s.AgentStateChangedCh = make(chan AgentStateChangedEvent, 10)
	}
	return s.AgentStateChangedCh
}

func (s *AgentSession) UserStateChangedEvents() <-chan UserStateChangedEvent {
	return s.userStateChangedEvents()
}

func (s *AgentSession) userStateChangedEvents() chan UserStateChangedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.UserStateChangedCh == nil {
		s.UserStateChangedCh = make(chan UserStateChangedEvent, 10)
	}
	return s.UserStateChangedCh
}

func NewAgentSession(agent AgentInterface, room *lksdk.Room, opts AgentSessionOptions) *AgentSession {
	opts = withAgentSessionOptionDefaults(opts)
	baseAgent := agent.GetAgent()
	session := &AgentSession{
		Agent:               agent,
		Room:                room,
		STT:                 baseAgent.STT,
		VAD:                 baseAgent.VAD,
		LLM:                 baseAgent.LLM,
		TTS:                 baseAgent.TTS,
		Options:             opts,
		ChatCtx:             llm.NewChatContext(),
		Tools:               make([]llm.Tool, 0),
		MetricsCollector:    telemetry.NewUsageCollector(),
		UserState:           UserStateListening,
		AgentState:          AgentStateInitializing,
		AgentStateChangedCh: make(chan AgentStateChangedEvent, 10),
		UserStateChangedCh:  make(chan UserStateChangedEvent, 10),
		userInputCh:         make(chan UserInputTranscribedEvent, 10),
		speechCreatedCh:     make(chan SpeechCreatedEvent, 10),
		falseInterruptionCh: make(chan AgentFalseInterruptionEvent, 10),
		userTurnExceededCh:  make(chan UserTurnExceededEvent, 10),
		overlappingSpeechCh: make(chan OverlappingSpeechEvent, 10),
		conversationItemCh:  make(chan ConversationItemAddedEvent, 10),
		functionToolsCh:     make(chan FunctionToolsExecutedEvent, 10),
		metricsCollectedCh:  make(chan MetricsCollectedEvent, 10),
		sessionUsageCh:      make(chan SessionUsageUpdatedEvent, 10),
		errorCh:             make(chan ErrorEvent, 10),
		sipDTMFCh:           make(chan SipDTMFEvent, 10),
	}
	if opts.VideoSampler != nil {
		session.videoSampler = opts.VideoSampler
	} else {
		session.videoSampler = NewVoiceActivityVideoSampler(session, 1.0, images.EncodeOptions{})
	}
	return session
}

func withAgentSessionOptionDefaults(opts AgentSessionOptions) AgentSessionOptions {
	opts.AllowInterruptions = true
	opts.DiscardAudioIfUninterruptible = true
	if opts.MinInterruptionDuration == 0 {
		opts.MinInterruptionDuration = 0.5
	}
	if opts.MinEndpointingDelay == 0 {
		opts.MinEndpointingDelay = 0.5
	}
	if opts.MaxEndpointingDelay == 0 {
		opts.MaxEndpointingDelay = 3.0
	}
	if opts.MaxToolSteps == 0 {
		opts.MaxToolSteps = 3
	}
	if opts.UserAwayTimeout == 0 {
		opts.UserAwayTimeout = 15.0
	}
	if opts.FalseInterruptionTimeout == 0 {
		opts.FalseInterruptionTimeout = 2.0
	}
	opts.ResumeFalseInterruption = true
	opts.PreemptiveGeneration = true
	if opts.AECWarmupDuration == 0 {
		opts.AECWarmupDuration = 3.0
	}
	return opts
}

func (s *AgentSession) UpdateOptions(opts AgentSessionUpdateOptions) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if opts.MinEndpointingDelay != nil {
		s.Options.MinEndpointingDelay = *opts.MinEndpointingDelay
	}
	if opts.MaxEndpointingDelay != nil {
		s.Options.MaxEndpointingDelay = *opts.MaxEndpointingDelay
	}
	if opts.TurnDetection != nil {
		s.Options.TurnDetection = *opts.TurnDetection
	}
}

func (s *AgentSession) UserInputTranscribedEvents() <-chan UserInputTranscribedEvent {
	return s.userInputTranscribedEvents()
}

func (s *AgentSession) EmitUserInputTranscribed(ev UserInputTranscribedEvent) {
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now()
	}
	s.recordEvent(&ev)
	s.mu.Lock()
	userState := s.UserState
	s.mu.Unlock()
	if ev.IsFinal && userState == UserStateAway {
		s.UpdateUserState(UserStateListening)
	}
	ch := s.userInputTranscribedEvents()
	select {
	case ch <- ev:
	default:
	}
}

func (s *AgentSession) userInputTranscribedEvents() chan UserInputTranscribedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.userInputCh == nil {
		s.userInputCh = make(chan UserInputTranscribedEvent, 10)
	}
	return s.userInputCh
}

func (s *AgentSession) AgentOutputTranscribedEvents() <-chan AgentOutputTranscribedEvent {
	return s.agentOutputTranscribedEvents()
}

func (s *AgentSession) EmitAgentOutputTranscribed(ev AgentOutputTranscribedEvent) {
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now()
	}
	s.recordEvent(&ev)
	ch := s.agentOutputTranscribedEvents()
	select {
	case ch <- ev:
	default:
	}
}

func (s *AgentSession) agentOutputTranscribedEvents() chan AgentOutputTranscribedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.agentOutputCh == nil {
		s.agentOutputCh = make(chan AgentOutputTranscribedEvent, 10)
	}
	return s.agentOutputCh
}

func (s *AgentSession) SpeechCreatedEvents() <-chan SpeechCreatedEvent {
	return s.speechCreatedEvents()
}

func (s *AgentSession) EmitSpeechCreated(ev SpeechCreatedEvent) {
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now()
	}
	s.recordEvent(&ev)
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

func (s *AgentSession) AgentFalseInterruptionEvents() <-chan AgentFalseInterruptionEvent {
	return s.agentFalseInterruptionEvents()
}

func (s *AgentSession) EmitAgentFalseInterruption(ev AgentFalseInterruptionEvent) {
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = NewAgentFalseInterruptionEvent(ev.Resumed).CreatedAt
	}
	s.recordEvent(&ev)
	ch := s.agentFalseInterruptionEvents()
	select {
	case ch <- ev:
	default:
	}
}

func (s *AgentSession) agentFalseInterruptionEvents() chan AgentFalseInterruptionEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.falseInterruptionCh == nil {
		s.falseInterruptionCh = make(chan AgentFalseInterruptionEvent, 10)
	}
	return s.falseInterruptionCh
}

func (s *AgentSession) UserTurnExceededEvents() <-chan UserTurnExceededEvent {
	return s.userTurnExceededEvents()
}

func (s *AgentSession) EmitUserTurnExceeded(ev UserTurnExceededEvent) {
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = NewUserTurnExceededEvent(
			ev.Transcript,
			ev.AccumulatedTranscript,
			ev.AccumulatedWordCount,
			ev.Duration,
		).CreatedAt
	}
	s.recordEvent(&ev)
	s.mu.Lock()
	activity := s.activity
	s.mu.Unlock()
	ch := s.userTurnExceededEvents()
	select {
	case ch <- ev:
	default:
	}
	if activity != nil {
		activity.OnUserTurnExceeded(ev)
	}
}

func (s *AgentSession) userTurnExceededEvents() chan UserTurnExceededEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.userTurnExceededCh == nil {
		s.userTurnExceededCh = make(chan UserTurnExceededEvent, 10)
	}
	return s.userTurnExceededCh
}

func (s *AgentSession) OverlappingSpeechEvents() <-chan OverlappingSpeechEvent {
	return s.overlappingSpeechEvents()
}

func (s *AgentSession) EmitOverlappingSpeech(ev OverlappingSpeechEvent) {
	now := time.Now()
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = now
	}
	if ev.DetectedAt.IsZero() {
		ev.DetectedAt = now
	}
	s.recordEvent(&ev)
	ch := s.overlappingSpeechEvents()
	select {
	case ch <- ev:
	default:
	}
}

func (s *AgentSession) overlappingSpeechEvents() chan OverlappingSpeechEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.overlappingSpeechCh == nil {
		s.overlappingSpeechCh = make(chan OverlappingSpeechEvent, 10)
	}
	return s.overlappingSpeechCh
}

func (s *AgentSession) ConversationItemAddedEvents() <-chan ConversationItemAddedEvent {
	return s.conversationItemAddedEvents()
}

func (s *AgentSession) EmitConversationItemAdded(item llm.ChatItem) {
	if item == nil {
		return
	}
	s.insertChatItem(item)
	ev := ConversationItemAddedEvent{Item: item, CreatedAt: time.Now()}
	s.recordEvent(&ev)
	ch := s.conversationItemAddedEvents()
	select {
	case ch <- ev:
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
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now()
	}
	for _, call := range ev.FunctionCalls {
		s.insertChatItem(call)
	}
	for _, output := range ev.FunctionCallOutputs {
		if output != nil {
			s.insertChatItem(output)
		}
	}
	s.recordEvent(&ev)
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

func (s *AgentSession) MetricsCollectedEvents() <-chan MetricsCollectedEvent {
	return s.metricsCollectedEvents()
}

func (s *AgentSession) EmitMetricsCollected(metrics telemetry.AgentMetrics) {
	if metrics == nil {
		return
	}
	var usage telemetry.UsageSummary
	if s.MetricsCollector != nil {
		s.MetricsCollector.Collect(metrics)
		usage = s.MetricsCollector.GetSummary()
	}
	ch := s.metricsCollectedEvents()
	ev := MetricsCollectedEvent{
		Metrics:   metrics,
		CreatedAt: time.Now(),
	}
	s.recordEvent(&ev)
	select {
	case ch <- ev:
	default:
	}
	if s.MetricsCollector != nil {
		s.EmitSessionUsageUpdated(SessionUsageUpdatedEvent{Usage: usage})
	}
}

func (s *AgentSession) Usage() telemetry.UsageSummary {
	if s.MetricsCollector == nil {
		return telemetry.UsageSummary{}
	}
	return s.MetricsCollector.GetSummary()
}

func (s *AgentSession) metricsCollectedEvents() chan MetricsCollectedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.metricsCollectedCh == nil {
		s.metricsCollectedCh = make(chan MetricsCollectedEvent, 10)
	}
	return s.metricsCollectedCh
}

func (s *AgentSession) SessionUsageUpdatedEvents() <-chan SessionUsageUpdatedEvent {
	return s.sessionUsageUpdatedEvents()
}

func (s *AgentSession) EmitSessionUsageUpdated(ev SessionUsageUpdatedEvent) {
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now()
	}
	s.recordEvent(&ev)
	ch := s.sessionUsageUpdatedEvents()
	select {
	case ch <- ev:
	default:
	}
}

func (s *AgentSession) sessionUsageUpdatedEvents() chan SessionUsageUpdatedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sessionUsageCh == nil {
		s.sessionUsageCh = make(chan SessionUsageUpdatedEvent, 10)
	}
	return s.sessionUsageCh
}

func (s *AgentSession) ErrorEvents() <-chan ErrorEvent {
	return s.errorEvents()
}

func (s *AgentSession) EmitError(ev ErrorEvent) {
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = NewErrorEvent(ev.Error, ev.Source).CreatedAt
	}
	s.recordEvent(&ev)
	ch := s.errorEvents()
	select {
	case ch <- ev:
	default:
	}
}

func (s *AgentSession) errorEvents() chan ErrorEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.errorCh == nil {
		s.errorCh = make(chan ErrorEvent, 10)
	}
	return s.errorCh
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
	s.mu.Lock()
	started := s.started
	activity := s.activity
	agent := s.Agent
	s.mu.Unlock()
	if !started && activity == nil && agent != nil {
		return
	}

	ev := CloseEvent{Reason: reason, CreatedAt: time.Now()}
	s.recordEvent(&ev)
	ch := s.closeEvents()
	select {
	case ch <- ev:
	default:
	}
	_ = s.Stop(context.Background())
}

func (s *AgentSession) Shutdown(drain ...bool) {
	shouldDrain := true
	if len(drain) > 0 {
		shouldDrain = drain[0]
	}
	if shouldDrain {
		if err := s.Drain(context.Background()); err != nil && !errors.Is(err, ErrAgentSessionNotRunning) {
			s.EmitError(ErrorEvent{Error: err, CreatedAt: time.Now()})
		}
	}
	s.CloseSoon(CloseReasonUserInitiated)
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
	if s.started {
		s.mu.Unlock()
		return nil
	}

	if s.VAD == nil {
		s.VAD = vad.NewSimpleVAD(0.05)
	}

	if s.Assistant == nil {
		s.Assistant = NewPipelineAgent(s.VAD, s.STT, s.LLM, s.TTS, s.ChatCtx)
	}
	assistant := s.Assistant
	if pipeline, ok := assistant.(*PipelineAgent); ok {
		pipeline.ttsStreamPacer = s.Options.TTSStreamPacer
	}
	agent := s.Agent
	avatar := agent.GetAgent().Avatar
	backgroundAudio := s.Options.BackgroundAudio
	room := s.Room
	hasMetricsCollector := s.MetricsCollector != nil
	s.mu.Unlock()

	s.UpdateAgentState(AgentStateInitializing)

	if backgroundAudio != nil && room != nil {
		if err := backgroundAudio.Start(room, s); err != nil {
			return err
		}
	}
	if avatar != nil {
		if err := avatar.Start(ctx); err != nil {
			if backgroundAudio != nil {
				_ = backgroundAudio.Close()
			}
			return err
		}
	}
	if err := assistant.Start(ctx, s); err != nil {
		if backgroundAudio != nil {
			_ = backgroundAudio.Close()
		}
		return err
	}

	activity := NewAgentActivity(agent, s)
	s.mu.Lock()
	s.activity = activity
	s.started = true
	s.mu.Unlock()

	activity.Start()
	if s.Options.IVRDetection {
		ivrActivity := NewIVRActivity(s)
		s.mu.Lock()
		s.ivrActivity = ivrActivity
		s.mu.Unlock()
		ivrActivity.Start()
	}

	// Trigger periodic usage metrics reporting
	if hasMetricsCollector {
		go s.reportUsageLoop(ctx)
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
	backgroundAudio := s.Options.BackgroundAudio
	s.mu.Unlock()

	if oldState != state {
		if backgroundAudio != nil {
			backgroundAudio.AgentStateChanged(state)
		}
		s.updateUserAwayTimer()
	}

	if oldState != state {
		logger.Logger.Debugw("Agent state changed", "old", oldState, "new", state)
		ev := AgentStateChangedEvent{
			OldState:  oldState,
			NewState:  state,
			CreatedAt: time.Now(),
		}
		s.recordEvent(&ev)
		select {
		case s.AgentStateChangedCh <- ev:
		default:
			// Channel full, ignore
		}
	}
}

func (s *AgentSession) UpdateUserState(state UserState) {
	s.mu.Lock()
	oldState := s.UserState
	s.UserState = state
	videoSampler := s.videoSampler
	s.mu.Unlock()

	if videoSampler != nil {
		videoSampler.SetSpeaking(state == UserStateSpeaking)
	}

	if oldState != state {
		s.updateUserAwayTimer()
	}

	if oldState != state {
		logger.Logger.Debugw("User state changed", "old", oldState, "new", state)
		ev := UserStateChangedEvent{
			OldState:  oldState,
			NewState:  state,
			CreatedAt: time.Now(),
		}
		s.recordEvent(&ev)
		select {
		case s.UserStateChangedCh <- ev:
		default:
			// Channel full, ignore
		}
	}
}

func (s *AgentSession) updateUserAwayTimer() {
	s.mu.Lock()
	if s.userAwayTimer != nil {
		s.userAwayTimer.Stop()
		s.userAwayTimer = nil
	}
	shouldStart := s.Options.UserAwayTimeout > 0 &&
		s.AgentState == AgentStateListening &&
		s.UserState == UserStateListening
	timeout := s.Options.UserAwayTimeout
	s.mu.Unlock()

	if !shouldStart {
		return
	}

	timer := time.AfterFunc(time.Duration(timeout*float64(time.Second)), func() {
		s.UpdateUserState(UserStateAway)
	})

	s.mu.Lock()
	if s.AgentState == AgentStateListening && s.UserState == UserStateListening && s.userAwayTimer == nil {
		s.userAwayTimer = timer
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	timer.Stop()
}

func (s *AgentSession) GenerateReply(ctx context.Context, userInput string) (*SpeechHandle, error) {
	return s.GenerateReplyWithOptions(ctx, GenerateReplyOptions{
		UserInput:     userInput,
		InputModality: "text",
	})
}

func (s *AgentSession) Say(ctx context.Context, text string) (*SpeechHandle, error) {
	return s.SayWithOptions(ctx, SayOptions{
		Text: text,
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
	result := newRunResultFromOptions(s.ChatCtx, opts.UserInput, opts.OutputType)
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
	if len(opts.Tools) > 0 {
		if _, err := resolveToolsByID(s.Tools, opts.Tools); err != nil {
			return nil, err
		}
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
	if opts.Instructions != "" {
		handle.Generation.Instructions = llm.NewInstructions(opts.Instructions)
	}
	if opts.ToolChoice != nil {
		handle.Generation.ToolChoice = opts.ToolChoice
	}
	if len(opts.Tools) > 0 {
		handle.Generation.Tools = append([]string(nil), opts.Tools...)
	}
	if opts.ChatCtx != nil {
		handle.Generation.ChatCtx = opts.ChatCtx.Copy()
	}
	if opts.UserMessage != nil {
		handle.Generation.UserMessage = opts.UserMessage
	}
	s.EmitSpeechCreated(SpeechCreatedEvent{
		UserInitiated: true,
		Source:        "generate_reply",
		SpeechHandle:  handle,
	})

	var userMessage *llm.ChatMessage
	if opts.UserMessage != nil {
		userMessage = opts.UserMessage
	} else if opts.UserInput != "" {
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
	s.watchActiveRunSpeechHandle(handle)
	return handle, nil
}

func (s *AgentSession) SayWithOptions(ctx context.Context, opts SayOptions) (*SpeechHandle, error) {
	s.mu.Lock()
	activity := s.activity
	s.mu.Unlock()

	if activity == nil {
		return nil, ErrAgentSessionNotRunning
	}

	logger.Logger.Infow("Saying text", "text", opts.Text)

	allowInterruptions := s.Options.AllowInterruptions
	if opts.AllowInterruptions != nil {
		allowInterruptions = *opts.AllowInterruptions
	}
	addToChatContext := true
	if opts.AddToChatContext != nil {
		addToChatContext = *opts.AddToChatContext
	}

	handle := NewSpeechHandle(allowInterruptions, InputDetails{Modality: "text"})
	s.EmitSpeechCreated(SpeechCreatedEvent{
		UserInitiated: true,
		Source:        "say",
		SpeechHandle:  handle,
	})

	var assistantMessage *llm.ChatMessage
	if addToChatContext && opts.Text != "" {
		assistantMessage = &llm.ChatMessage{
			Role: llm.ChatRoleAssistant,
			Content: []llm.ChatContent{
				{Text: opts.Text},
			},
			CreatedAt: time.Now(),
		}
	}

	if err := activity.ScheduleSpeech(handle, SpeechPriorityNormal, false); err != nil {
		return nil, err
	}
	if assistantMessage != nil {
		s.EmitConversationItemAdded(assistantMessage)
		handle.AddChatItems(assistantMessage)
	}
	s.watchActiveRunSpeechHandle(handle)
	return handle, nil
}

func (s *AgentSession) watchActiveRunSpeechHandle(handle *SpeechHandle) {
	s.mu.Lock()
	runState := s.runState
	s.mu.Unlock()
	if runState != nil {
		runState.WatchSpeechHandle(handle)
	}
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

func (s *AgentSession) ClearUserTurn() error {
	s.mu.Lock()
	activity := s.activity
	s.mu.Unlock()

	if activity == nil {
		return ErrAgentSessionNotRunning
	}

	activity.ClearUserTurn()
	return nil
}

func (s *AgentSession) CommitUserTurn(ctx context.Context, opts CommitUserTurnOptions) (string, error) {
	s.mu.Lock()
	activity := s.activity
	s.mu.Unlock()

	if activity == nil {
		return "", ErrAgentSessionNotRunning
	}

	return activity.CommitUserTurn(ctx, opts)
}

func (s *AgentSession) UpdateAgent(agent AgentInterface) {
	if agent == nil {
		return
	}
	baseAgent := agent.GetAgent()

	s.mu.Lock()
	oldActivity := s.activity
	started := s.started
	s.Agent = agent
	s.STT = baseAgent.STT
	s.VAD = baseAgent.VAD
	s.LLM = baseAgent.LLM
	s.TTS = baseAgent.TTS
	if !started {
		s.mu.Unlock()
		return
	}

	newActivity := NewAgentActivity(agent, s)
	s.activity = newActivity
	s.mu.Unlock()

	if oldActivity != nil {
		oldActivity.Stop()
	}
	newActivity.Start()
}

func (s *AgentSession) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}

	activity := s.activity
	ivrActivity := s.ivrActivity
	s.activity = nil
	s.ivrActivity = nil
	s.started = false
	s.UserState = UserStateListening
	s.AgentState = AgentStateInitializing
	if s.userAwayTimer != nil {
		s.userAwayTimer.Stop()
		s.userAwayTimer = nil
	}
	backgroundAudio := s.Options.BackgroundAudio
	s.mu.Unlock()

	if ivrActivity != nil {
		ivrActivity.Stop()
	}
	if activity != nil {
		activity.Stop()
	}
	if backgroundAudio != nil {
		_ = backgroundAudio.Close()
	}
	return nil
}
