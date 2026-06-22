package agent

import (
	"context"
	"encoding/json"
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
	"github.com/google/uuid"
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

type idleHoldContextKey struct{}
type drainIdleContextKey struct{}

type AgentSessionOptions struct {
	AllowInterruptions                       bool
	AllowInterruptionsSet                    bool
	DiscardAudioIfUninterruptible            bool
	DiscardAudioIfUninterruptibleSet         bool
	MinInterruptionDuration                  float64
	MinInterruptionDurationSet               bool
	MinInterruptionWords                     int
	MinEndpointingDelay                      float64
	MinEndpointingDelaySet                   bool
	MaxEndpointingDelay                      float64
	MaxEndpointingDelaySet                   bool
	EndpointingMode                          string
	EndpointingAlpha                         float64
	Endpointing                              Endpointing
	MaxToolSteps                             int
	MaxToolStepsSet                          bool
	UserAwayTimeout                          float64
	UserAwayTimeoutSet                       bool
	DisableUserAwayTimeout                   bool
	UserTurnLimitMaxWords                    int
	UserTurnLimitMaxDuration                 float64
	FalseInterruptionTimeout                 float64
	FalseInterruptionTimeoutSet              bool
	ResumeFalseInterruption                  bool
	ResumeFalseInterruptionSet               bool
	BackchannelBoundaryStart                 float64
	BackchannelBoundaryStartSet              bool
	BackchannelBoundaryEnd                   float64
	BackchannelBoundaryEndSet                bool
	MinConsecutiveSpeechDelay                float64
	UseTTSAlignedTranscript                  bool
	TTSStreamPacer                           *tts.SentenceStreamPacerOptions
	TTSTextReplacements                      map[string]string
	TTSTextTransforms                        []string
	TTSTextTransformsSet                     bool
	DisableTTSTextTransforms                 bool
	LLMParallelToolCalls                     *bool
	LLMExtraParams                           map[string]any
	LLMResponseFormat                        map[string]any
	BackgroundAudio                          *BackgroundAudioPlayer
	WordTokenizer                            tokenize.WordTokenizer
	PreemptiveGeneration                     bool
	PreemptiveGenerationSet                  bool
	PreemptiveGenerationPreemptiveTTS        bool
	PreemptiveGenerationPreemptiveTTSSet     bool
	PreemptiveGenerationMaxSpeechDuration    float64
	PreemptiveGenerationMaxSpeechDurationSet bool
	PreemptiveGenerationMaxRetries           int
	PreemptiveGenerationMaxRetriesSet        bool
	AudioInputHook                           AudioInputHook
	AECWarmupDuration                        float64
	AECWarmupDurationSet                     bool
	SessionCloseTranscriptTimeout            float64
	SessionCloseTranscriptTimeoutSet         bool
	TurnDetection                            TurnDetectionMode
	IVRDetection                             bool
	IVRSilenceDuration                       time.Duration
	VideoSampler                             *VoiceActivityVideoSampler
	ToolChoice                               llm.ToolChoice
	MaxUnrecoverableErrors                   int
	MaxUnrecoverableErrorsSet                bool
	MockTools                                map[string]MockToolFunc
}

type AgentSessionUpdateOptions struct {
	MinEndpointingDelay *float64
	MaxEndpointingDelay *float64
	EndpointingAlpha    *float64
	EndpointingMode     *string
	Endpointing         Endpointing
	TurnDetection       *TurnDetectionMode
	ToolChoice          *llm.ToolChoice
}

var (
	ErrAgentSessionNotRunning                     = errors.New("AgentSession isn't running")
	ErrAgentSessionNestedRun                      = errors.New("nested runs are not supported")
	ErrAgentSessionUserdataNotSet                 = errors.New("AgentSession userdata is not set")
	ErrAgentSessionJobContextNotSet               = errors.New("agent session job context is not set")
	errGenerateReplyMissingLLM                    = errors.New("trying to generate reply without an LLM model")
	errSayMissingTTSWithAudioOutput               = errors.New("trying to generate speech from text without a TTS model or a RealtimeSession that supports say(); add a TTS model to AgentSession to enable say()")
	errAgentSessionClosingSay                     = agentSessionClosingError("AgentSession is closing, cannot use say()")
	errAgentSessionClosingReply                   = agentSessionClosingError("AgentSession is closing, cannot use generate_reply()")
	errRealtimeTurnDetectionInterruptionsDisabled = errors.New("the RealtimeModel uses a server-side turn detection, allow_interruptions cannot be False, disable turn_detection in the RealtimeModel and use VAD on the AgentSession instead")
)

type agentSessionClosingError string

func (e agentSessionClosingError) Error() string {
	return string(e)
}

func (agentSessionClosingError) Is(target error) bool {
	return target == ErrAgentSessionNotRunning
}

type voiceAgentNotRunningError struct{}

func (voiceAgentNotRunningError) Error() string {
	return "VoiceAgent isn't running"
}

func (voiceAgentNotRunningError) Is(target error) bool {
	return target == ErrAgentSessionNotRunning
}

type GenerateReplyOptions struct {
	UserInput           string
	UserMessage         *llm.ChatMessage
	Instructions        string
	InstructionVariants *llm.Instructions
	ToolChoice          llm.ToolChoice
	Tools               []string
	ChatCtx             *llm.ChatContext
	AllowInterruptions  *bool
	InputModality       string
	ScheduleSpeech      *bool
	UserInitiated       *bool
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

type StartOptions struct {
	CaptureRun bool
	OutputType reflect.Type
}

type CommitUserTurnOptions struct {
	TranscriptTimeout time.Duration
	STTFlushDuration  time.Duration
	SkipReply         bool
	SkipRealtimeAudio bool
}

type SessionAssistant interface {
	Start(ctx context.Context, s *AgentSession) error
	OnAudioFrame(ctx context.Context, frame *model.AudioFrame)
	SetPublishAudio(func(ctx context.Context, frame *model.AudioFrame) error)
}

type AudioOutputController interface {
	CanPauseAudioOutput() bool
	PauseAudioOutput()
	ResumeAudioOutput()
}

type AudioPlaybackController interface {
	ClearBuffer()
	WaitForPlayout(context.Context) (AudioPlaybackResult, error)
}

type AudioPlaybackResult struct {
	PlaybackPosition          time.Duration
	Interrupted               bool
	SynchronizedTranscript    string
	HasSynchronizedTranscript bool
}

type closeableSessionAssistant interface {
	Close() error
}

type sessionToolsetCloser interface {
	Close() error
}

type scheduledSpeechAssistant interface {
	OnSpeechScheduled(ctx context.Context, speech *SpeechHandle)
}

type preemptiveSpeechAssistant interface {
	OnSpeechPreemptive(ctx context.Context, speech *SpeechHandle)
}

type nativeSayAssistant interface {
	SupportsNativeSay() bool
}

type realtimeCapabilitiesAssistant interface {
	RealtimeCapabilities() llm.RealtimeCapabilities
}

type videoSessionAssistant interface {
	OnVideoFrame(ctx context.Context, frame *images.VideoFrame)
}

type componentUpdatingAssistant interface {
	UpdateComponents(vad.VAD, stt.STT, llm.LLM, tts.TTS)
}

type realtimeModelUpdatingAssistant interface {
	UpdateRealtimeModel(context.Context, llm.RealtimeModel) error
}

type realtimeOptionsUpdatingAssistant interface {
	UpdateOptions(context.Context, llm.RealtimeSessionOptions) error
}

type AgentSession struct {
	Options AgentSessionOptions

	ChatCtx       *llm.ChatContext
	Agent         AgentInterface
	STT           stt.STT
	VAD           vad.VAD
	LLM           llm.LLM
	TTS           tts.TTS
	RealtimeModel llm.RealtimeModel
	Tools         []llm.Tool
	Assistant     SessionAssistant
	Room          *lksdk.Room

	MetricsCollector    *telemetry.UsageCollector
	ModelUsageCollector *telemetry.ModelUsageCollector

	userState  UserState
	agentState AgentState

	mu             sync.Mutex
	activity       *AgentActivity
	started        bool
	starting       bool
	startDone      chan struct{}
	closing        bool
	runCtx         context.Context
	runState       *RunResult
	onEnterDepth   int
	userTurnClaims int
	userTurnDone   chan struct{}
	idleHolds      int
	idleDone       chan struct{}
	userAwayTimer  *time.Timer
	userAwayGate   func() bool
	aecWarmupTimer *time.Timer
	aecWarmupDone  bool
	userdata       any
	userdataSet    bool
	jobContext     any
	jobContextSet  bool
	// UserTranscriptFilter, when non-nil, is applied before user transcript
	// events are recorded or broadcast to RoomIO subscribers.
	UserTranscriptFilter    func(string) string
	mcpServers              []llm.MCPServer
	recordedEvents          []Event
	eventListeners          map[string][]agentEventListener
	nextListenerID          uint64
	ivrActivity             *IVRActivity
	videoSampler            *VoiceActivityVideoSampler
	audioOutputController   AudioOutputController
	audioPlaybackController AudioPlaybackController
	toolExecutionRegistry   activeToolRegistry

	// Event channels
	AgentStateChangedCh  chan AgentStateChangedEvent
	UserStateChangedCh   chan UserStateChangedEvent
	agentStateSubs       []chan AgentStateChangedEvent
	userStateSubs        []chan UserStateChangedEvent
	userInputSubs        []chan UserInputTranscribedEvent
	agentOutputSubs      []chan AgentOutputTranscribedEvent
	agentReasoningSubs   []chan AgentReasoningTranscribedEvent
	speechCreatedCh      chan SpeechCreatedEvent
	speechCreatedSubd    bool
	speechCreatedSubs    []chan SpeechCreatedEvent
	falseInterruptionCh  chan AgentFalseInterruptionEvent
	falseInterruptSubd   bool
	falseInterruptSubs   []chan AgentFalseInterruptionEvent
	userTurnExceededCh   chan UserTurnExceededEvent
	userTurnExceededSubd bool
	userTurnExceededSubs []chan UserTurnExceededEvent
	overlappingSpeechCh  chan OverlappingSpeechEvent
	overlappingSubd      bool
	overlappingSubs      []chan OverlappingSpeechEvent
	conversationItemCh   chan ConversationItemAddedEvent
	conversationItemSubd bool
	conversationItemSubs []chan ConversationItemAddedEvent
	functionToolsCh      chan FunctionToolsExecutedEvent
	functionToolsSubd    bool
	functionToolsSubs    []chan FunctionToolsExecutedEvent
	metricsCollectedCh   chan MetricsCollectedEvent
	metricsChSubscribed  bool
	metricsSubs          []chan MetricsCollectedEvent
	sessionUsageCh       chan SessionUsageUpdatedEvent
	sessionUsageChSubbed bool
	sessionUsageSubs     []chan SessionUsageUpdatedEvent
	errorCh              chan ErrorEvent
	errorChSubscribed    bool
	errorSubs            []chan ErrorEvent
	sipDTMFCh            chan SipDTMFEvent
	sipDTMFSubd          bool
	sipDTMFSubs          []chan SipDTMFEvent
	closeCh              chan CloseEvent
	closeChSubscribed    bool
	closeSubs            []chan CloseEvent

	llmErrorCount int
	ttsErrorCount int
}

type agentEventListener struct {
	id              uint64
	callback        func(Event)
	callbackPointer uintptr
	once            bool
}

type SipDTMFEvent struct {
	Digit          string
	Code           uint32
	SenderIdentity string
}

func (s *AgentSession) OnAudioFrame(ctx context.Context, frame *model.AudioFrame) {
	s.mu.Lock()
	assistant := s.Assistant
	activity := s.activity
	s.mu.Unlock()

	if activity != nil {
		activity.RecordUserAudioFrame(frame)
	}
	if assistant != nil {
		assistant.OnAudioFrame(ctx, frame)
	}
}

func (s *AgentSession) OnAudioEnabledChanged(enabled bool) {
	if enabled {
		return
	}

	s.mu.Lock()
	userState := s.userState
	activity := s.activity
	s.mu.Unlock()

	if userState != UserStateSpeaking {
		return
	}
	if activity != nil {
		activity.OnEndOfSpeech(nil)
		return
	}
	s.UpdateUserState(UserStateListening)
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

	return activity.CurrentSpeech()
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

func (s *AgentSession) ClaimUserTurn(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return nil
	}

	s.mu.Lock()
	s.userTurnClaims++
	first := s.userTurnClaims == 1
	if first {
		s.userTurnDone = make(chan struct{})
	}
	s.mu.Unlock()

	if first {
		s.UpdateUserState(UserStateSpeaking)
	}
	defer s.releaseUserTurnClaim()

	return fn(ctx)
}

func (s *AgentSession) releaseUserTurnClaim() {
	s.mu.Lock()
	if s.userTurnClaims > 0 {
		s.userTurnClaims--
	}
	remaining := s.userTurnClaims
	activity := s.activity
	done := s.userTurnDone
	if remaining == 0 && done != nil {
		close(done)
		s.userTurnDone = nil
	}
	s.mu.Unlock()

	if remaining > 0 {
		return
	}

	if activity != nil && activity.isUserSpeaking() {
		s.UpdateUserState(UserStateSpeaking)
		return
	}
	s.UpdateUserState(UserStateListening)
}

func (s *AgentSession) JobContext() (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.jobContextSet {
		return nil, ErrAgentSessionJobContextNotSet
	}
	return s.jobContext, nil
}

func (s *AgentSession) SetJobContext(value any) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.jobContext = value
	s.jobContextSet = true
}

func (s *AgentSession) History() *llm.ChatContext {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ChatCtx == nil {
		return llm.NewChatContext()
	}
	return s.ChatCtx
}

func (s *AgentSession) SessionOptions() AgentSessionOptions {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Options
}

func (s *AgentSession) TurnDetection() TurnDetectionMode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Options.TurnDetection
}

func (s *AgentSession) UserStateValue() UserState {
	return s.UserState()
}

func (s *AgentSession) AgentStateValue() AgentState {
	return s.AgentState()
}

func (s *AgentSession) SetAudioOutputController(controller AudioOutputController) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.audioOutputController = controller
	s.mu.Unlock()
}

func (s *AgentSession) AudioOutputController() AudioOutputController {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.audioOutputController
}

func (s *AgentSession) SetAudioPlaybackController(controller AudioPlaybackController) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.audioPlaybackController = controller
	s.mu.Unlock()
}

func (s *AgentSession) AudioPlaybackController() AudioPlaybackController {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.audioPlaybackController
}

// SetUserAwayTimerGate installs a runtime gate for user-away timer arming.
// The callback returns true while the timer should be blocked.
func (s *AgentSession) SetUserAwayTimerGate(gate func() bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.userAwayGate = gate
	s.mu.Unlock()
	s.updateUserAwayTimer()
}

func (s *AgentSession) RefreshUserAwayTimer() {
	if s == nil {
		return
	}
	s.updateUserAwayTimer()
}

func (s *AgentSession) UserState() UserState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.userState
}

func (s *AgentSession) AgentState() AgentState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agentState
}

func (s *AgentSession) isClosing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closing
}

func (s *AgentSession) SetMCPServers(servers []llm.MCPServer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mcpServers = append([]llm.MCPServer(nil), servers...)
}

func (s *AgentSession) MCPServers() []llm.MCPServer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mcpServers
}

func (s *AgentSession) RecordedEvents() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()

	events := make([]Event, len(s.recordedEvents))
	copy(events, s.recordedEvents)
	return events
}

func (s *AgentSession) On(eventType string, callback func(Event)) func() {
	if s == nil || eventType == "" || callback == nil {
		return func() {}
	}
	if eventType == "metrics_collected" {
		logger.Logger.Warnw("metrics_collected is deprecated. Use session_usage_updated for usage tracking and ChatMessage.metrics for per-turn latency.", nil)
	}
	s.mu.Lock()
	if s.eventListeners == nil {
		s.eventListeners = make(map[string][]agentEventListener)
	}
	s.nextListenerID++
	id := s.nextListenerID
	s.eventListeners[eventType] = append(s.eventListeners[eventType], agentEventListener{
		id:              id,
		callback:        callback,
		callbackPointer: reflect.ValueOf(callback).Pointer(),
	})
	s.mu.Unlock()
	return func() {
		s.removeEventListener(eventType, id)
	}
}

func (s *AgentSession) Off(eventType string, callback func(Event)) {
	if s == nil || eventType == "" || callback == nil {
		return
	}
	s.removeEventListenerByCallback(eventType, reflect.ValueOf(callback).Pointer())
}

func (s *AgentSession) Once(eventType string, callback func(Event)) func() {
	if s == nil || eventType == "" || callback == nil {
		return func() {}
	}
	s.mu.Lock()
	if s.eventListeners == nil {
		s.eventListeners = make(map[string][]agentEventListener)
	}
	s.nextListenerID++
	id := s.nextListenerID
	s.eventListeners[eventType] = append(s.eventListeners[eventType], agentEventListener{
		id:              id,
		callback:        callback,
		callbackPointer: reflect.ValueOf(callback).Pointer(),
		once:            true,
	})
	s.mu.Unlock()
	return func() {
		s.removeEventListener(eventType, id)
	}
}

func (s *AgentSession) removeEventListener(eventType string, id uint64) {
	if s == nil || eventType == "" || id == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	listeners := s.eventListeners[eventType]
	for i, listener := range listeners {
		if listener.id != id {
			continue
		}
		listeners = append(listeners[:i], listeners[i+1:]...)
		if len(listeners) == 0 {
			delete(s.eventListeners, eventType)
		} else {
			s.eventListeners[eventType] = listeners
		}
		return
	}
}

func (s *AgentSession) removeEventListenerByCallback(eventType string, callbackPointer uintptr) {
	if s == nil || eventType == "" || callbackPointer == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	listeners := s.eventListeners[eventType]
	for i, listener := range listeners {
		if listener.callbackPointer != callbackPointer {
			continue
		}
		listeners = append(listeners[:i], listeners[i+1:]...)
		if len(listeners) == 0 {
			delete(s.eventListeners, eventType)
		} else {
			s.eventListeners[eventType] = listeners
		}
		return
	}
}

func (s *AgentSession) clearEventListenersLocked() {
	s.eventListeners = nil
}

func (s *AgentSession) recordEvent(ev Event) {
	if ev == nil {
		return
	}
	s.mu.Lock()
	s.recordedEvents = append(s.recordedEvents, ev)
	eventType := ev.GetType()
	registered := s.eventListeners[eventType]
	listeners := make([]func(Event), 0, len(registered))
	if len(registered) > 0 {
		remaining := registered[:0]
		for _, listener := range registered {
			listeners = append(listeners, listener.callback)
			if !listener.once {
				remaining = append(remaining, listener)
			}
		}
		if len(remaining) == 0 {
			delete(s.eventListeners, eventType)
		} else {
			s.eventListeners[eventType] = remaining
		}
	}
	s.mu.Unlock()
	for _, listener := range listeners {
		callAgentSessionListener(eventType, listener, ev)
	}
}

func (s *AgentSession) CurrentAgent() (AgentInterface, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Agent == nil {
		return nil, voiceAgentNotRunningError{}
	}
	return s.Agent, nil
}

func (s *AgentSession) WaitForInactive(ctx context.Context) error {
	for {
		s.mu.Lock()
		activity := s.activity
		s.mu.Unlock()

		if activity != nil {
			if err := activity.WaitForInactive(ctx); err != nil {
				if errors.Is(err, errAgentActivityClosed) {
					s.mu.Lock()
					currentActivity := s.activity
					s.mu.Unlock()
					if currentActivity != activity {
						continue
					}
				}
				return err
			}
			s.mu.Lock()
			currentActivity := s.activity
			s.mu.Unlock()
			if currentActivity != activity {
				continue
			}
		}
		s.mu.Lock()
		claims := s.userTurnClaims
		done := s.userTurnDone
		holds := s.idleHolds
		idleDone := s.idleDone
		s.mu.Unlock()
		if claims == 0 {
			if holds == 0 || ctx.Value(idleHoldContextKey{}) == true {
				return nil
			}
			select {
			case <-idleDone:
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *AgentSession) WaitForInactiveAndHold(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.WaitForInactive(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	s.idleHolds++
	if s.idleHolds == 1 {
		s.idleDone = make(chan struct{})
	}
	s.mu.Unlock()

	defer s.releaseIdleHold()
	return fn(context.WithValue(ctx, idleHoldContextKey{}, true))
}

func (s *AgentSession) releaseIdleHold() {
	s.mu.Lock()
	if s.idleHolds > 0 {
		s.idleHolds--
	}
	done := s.idleDone
	if s.idleHolds == 0 && done != nil {
		close(done)
		s.idleDone = nil
	}
	s.mu.Unlock()
}

func (s *AgentSession) Drain(ctx context.Context) error {
	s.mu.Lock()
	activity := s.activity
	s.mu.Unlock()
	if activity == nil {
		return ErrAgentSessionNotRunning
	}
	if err := activity.Drain(ctx); err != nil {
		return err
	}
	return s.toolExecutionRegistry.drain(ctx)
}

type AgentStateChangedEvent struct {
	OldState  AgentState
	NewState  AgentState
	CreatedAt time.Time
}

func (e *AgentStateChangedEvent) GetType() string { return "agent_state_changed" }

func (e *AgentStateChangedEvent) MarshalJSON() ([]byte, error) {
	if e == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(map[string]any{
		"type":       e.GetType(),
		"old_state":  e.OldState,
		"new_state":  e.NewState,
		"created_at": timeToUnixSeconds(e.CreatedAt),
	})
}

type UserStateChangedEvent struct {
	OldState  UserState
	NewState  UserState
	CreatedAt time.Time
}

func (e *UserStateChangedEvent) GetType() string { return "user_state_changed" }

func (e *UserStateChangedEvent) MarshalJSON() ([]byte, error) {
	if e == nil {
		return json.Marshal(nil)
	}
	return json.Marshal(map[string]any{
		"type":       e.GetType(),
		"old_state":  e.OldState,
		"new_state":  e.NewState,
		"created_at": timeToUnixSeconds(e.CreatedAt),
	})
}

func (s *AgentSession) AgentStateChangedEvents() <-chan AgentStateChangedEvent {
	return s.agentStateChangedEvents()
}

func (s *AgentSession) agentStateChangedEvents() chan AgentStateChangedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch := make(chan AgentStateChangedEvent, 10)
	s.agentStateSubs = append(s.agentStateSubs, ch)
	return ch
}

func (s *AgentSession) agentStateChangedSubscribers() (chan AgentStateChangedEvent, []chan AgentStateChangedEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.AgentStateChangedCh == nil {
		s.AgentStateChangedCh = make(chan AgentStateChangedEvent, 10)
	}
	return s.AgentStateChangedCh, append([]chan AgentStateChangedEvent(nil), s.agentStateSubs...)
}

func (s *AgentSession) UserStateChangedEvents() <-chan UserStateChangedEvent {
	return s.userStateChangedEvents()
}

func (s *AgentSession) userStateChangedEvents() chan UserStateChangedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch := make(chan UserStateChangedEvent, 10)
	s.userStateSubs = append(s.userStateSubs, ch)
	return ch
}

func (s *AgentSession) userStateChangedSubscribers() (chan UserStateChangedEvent, []chan UserStateChangedEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.UserStateChangedCh == nil {
		s.UserStateChangedCh = make(chan UserStateChangedEvent, 10)
	}
	return s.UserStateChangedCh, append([]chan UserStateChangedEvent(nil), s.userStateSubs...)
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
		RealtimeModel:       baseAgent.RealtimeModel,
		TTS:                 baseAgent.TTS,
		Options:             opts,
		ChatCtx:             llm.NewChatContext(),
		Tools:               copySessionTools(baseAgent.Tools),
		MetricsCollector:    telemetry.NewUsageCollector(),
		ModelUsageCollector: telemetry.NewModelUsageCollector(),
		userState:           UserStateListening,
		agentState:          AgentStateInitializing,
		AgentStateChangedCh: make(chan AgentStateChangedEvent, 10),
		UserStateChangedCh:  make(chan UserStateChangedEvent, 10),
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
	if !opts.AllowInterruptionsSet {
		opts.AllowInterruptions = true
	}
	if !opts.DiscardAudioIfUninterruptibleSet {
		opts.DiscardAudioIfUninterruptible = true
	}
	if !opts.MinInterruptionDurationSet && opts.MinInterruptionDuration == 0 {
		opts.MinInterruptionDuration = 0.5
	}
	if !opts.MinEndpointingDelaySet && opts.MinEndpointingDelay == 0 {
		opts.MinEndpointingDelay = 0.5
	}
	if !opts.MaxEndpointingDelaySet && opts.MaxEndpointingDelay == 0 {
		opts.MaxEndpointingDelay = 3.0
	}
	if opts.Endpointing == nil {
		opts.Endpointing = CreateEndpointing(opts.EndpointingMode, opts.MinEndpointingDelay, opts.MaxEndpointingDelay, opts.EndpointingAlpha)
	}
	if !opts.MaxToolStepsSet && opts.MaxToolSteps == 0 {
		opts.MaxToolSteps = 3
	}
	if !opts.DisableUserAwayTimeout && !opts.UserAwayTimeoutSet && opts.UserAwayTimeout == 0 {
		opts.UserAwayTimeout = 15.0
	}
	if !opts.FalseInterruptionTimeoutSet && opts.FalseInterruptionTimeout == 0 {
		opts.FalseInterruptionTimeout = 2.0
	}
	if !opts.ResumeFalseInterruptionSet {
		opts.ResumeFalseInterruption = true
	}
	if !opts.BackchannelBoundaryStartSet && opts.BackchannelBoundaryStart == 0 {
		opts.BackchannelBoundaryStart = 1.0
	}
	if !opts.BackchannelBoundaryEndSet && opts.BackchannelBoundaryEnd == 0 {
		opts.BackchannelBoundaryEnd = 1.0
	}
	if !opts.PreemptiveGenerationSet {
		opts.PreemptiveGeneration = true
	}
	if !opts.PreemptiveGenerationMaxSpeechDurationSet && opts.PreemptiveGenerationMaxSpeechDuration == 0 {
		opts.PreemptiveGenerationMaxSpeechDuration = 10.0
	}
	if !opts.PreemptiveGenerationMaxRetriesSet && opts.PreemptiveGenerationMaxRetries == 0 {
		opts.PreemptiveGenerationMaxRetries = 3
	}
	if !opts.DisableTTSTextTransforms && !opts.TTSTextTransformsSet {
		opts.TTSTextTransforms = []string{"filter_markdown", "filter_emoji"}
		opts.TTSTextTransformsSet = true
	}
	if !opts.AECWarmupDurationSet && opts.AECWarmupDuration == 0 {
		opts.AECWarmupDuration = 3.0
	}
	if !opts.SessionCloseTranscriptTimeoutSet && opts.SessionCloseTranscriptTimeout == 0 {
		opts.SessionCloseTranscriptTimeout = 2.0
	}
	if !opts.MaxUnrecoverableErrorsSet && opts.MaxUnrecoverableErrors == 0 {
		opts.MaxUnrecoverableErrors = 3
	}
	return opts
}

func (s *AgentSession) UpdateOptions(opts AgentSessionUpdateOptions) error {
	s.mu.Lock()
	oldTurnDetection := s.Options.TurnDetection

	if opts.MinEndpointingDelay != nil {
		s.Options.MinEndpointingDelay = *opts.MinEndpointingDelay
	}
	if opts.MaxEndpointingDelay != nil {
		s.Options.MaxEndpointingDelay = *opts.MaxEndpointingDelay
	}
	if opts.EndpointingAlpha != nil {
		s.Options.EndpointingAlpha = *opts.EndpointingAlpha
	}
	if opts.EndpointingMode != nil {
		s.Options.EndpointingMode = *opts.EndpointingMode
		s.Options.Endpointing = CreateEndpointing(s.Options.EndpointingMode, s.Options.MinEndpointingDelay, s.Options.MaxEndpointingDelay, s.Options.EndpointingAlpha)
	} else if s.Options.Endpointing != nil {
		s.Options.Endpointing.UpdateOptions(opts.MinEndpointingDelay, opts.MaxEndpointingDelay)
	}
	if opts.Endpointing != nil {
		s.Options.Endpointing = opts.Endpointing
	}
	if opts.EndpointingAlpha != nil {
		updateEndpointingAlpha(s.Options.Endpointing, *opts.EndpointingAlpha)
	}
	if opts.TurnDetection != nil {
		s.Options.TurnDetection = *opts.TurnDetection
	}
	if opts.ToolChoice != nil {
		s.Options.ToolChoice = *opts.ToolChoice
	}
	assistant := s.Assistant
	activity := s.activity
	s.mu.Unlock()

	if activity != nil && opts.TurnDetection != nil && (oldTurnDetection == TurnDetectionModeManual || *opts.TurnDetection == TurnDetectionModeManual) {
		activity.cancelFalseInterruptionTimer()
		activity.cancelPendingEOUDetection()
	}
	if activity != nil {
		return activity.UpdateOptions(opts)
	}
	if opts.ToolChoice == nil {
		return nil
	}
	updater, ok := assistant.(realtimeOptionsUpdatingAssistant)
	if !ok {
		return nil
	}
	return updater.UpdateOptions(context.Background(), llm.RealtimeSessionOptions{
		ToolChoice:    *opts.ToolChoice,
		ToolChoiceSet: true,
	})
}

type endpointingAlphaUpdater interface {
	UpdateAlpha(alpha float64)
}

func updateEndpointingAlpha(endpointing Endpointing, alpha float64) {
	updater, ok := endpointing.(endpointingAlphaUpdater)
	if !ok {
		return
	}
	updater.UpdateAlpha(alpha)
}

func (s *AgentSession) EnsureAssistant() SessionAssistant {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ensureAssistantLocked()
}

func (s *AgentSession) ensureAssistantLocked() SessionAssistant {
	if s.Assistant != nil {
		return s.Assistant
	}
	if s.RealtimeModel != nil {
		s.Assistant = NewMultimodalAgent(s.RealtimeModel, s.ChatCtx)
		return s.Assistant
	}
	pipeline := NewPipelineAgent(s.VAD, s.STT, s.LLM, s.TTS, s.ChatCtx)
	pipeline.SetAudioInputHook(s.Options.AudioInputHook)
	s.Assistant = pipeline
	return s.Assistant
}

func (s *AgentSession) replacementAssistantLocked(current SessionAssistant) SessionAssistant {
	if s.RealtimeModel != nil {
		if _, ok := current.(*MultimodalAgent); ok {
			return nil
		}
		return NewMultimodalAgent(s.RealtimeModel, s.ChatCtx)
	}
	if _, ok := current.(*MultimodalAgent); !ok {
		return nil
	}
	pipeline := NewPipelineAgent(s.VAD, s.STT, s.LLM, s.TTS, s.ChatCtx)
	pipeline.ttsStreamPacer = s.Options.TTSStreamPacer
	pipeline.SetAudioInputHook(s.Options.AudioInputHook)
	return pipeline
}

func (s *AgentSession) UserInputTranscribedEvents() <-chan UserInputTranscribedEvent {
	return s.userInputTranscribedEvents()
}

func (s *AgentSession) EmitUserInputTranscribed(ev UserInputTranscribedEvent) {
	if s.UserTranscriptFilter != nil {
		ev.Transcript = s.UserTranscriptFilter(ev.Transcript)
	}
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now()
	}
	s.mu.Lock()
	userState := s.userState
	s.mu.Unlock()
	if ev.IsFinal && userState == UserStateAway {
		s.UpdateUserState(UserStateListening)
	}
	s.recordEvent(&ev)
	for _, ch := range s.userInputTranscribedSubscribers() {
		if ev.IsFinal {
			ch <- ev
		} else {
			select {
			case ch <- ev:
			default:
			}
		}
	}
}

func (s *AgentSession) userInputTranscribedEvents() chan UserInputTranscribedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch := make(chan UserInputTranscribedEvent, 10)
	s.userInputSubs = append(s.userInputSubs, ch)
	return ch
}

func (s *AgentSession) userInputTranscribedSubscribers() []chan UserInputTranscribedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]chan UserInputTranscribedEvent(nil), s.userInputSubs...)
}

func (s *AgentSession) AgentOutputTranscribedEvents() <-chan AgentOutputTranscribedEvent {
	return s.agentOutputTranscribedEvents()
}

func (s *AgentSession) EmitAgentOutputTranscribed(ev AgentOutputTranscribedEvent) {
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now()
	}
	s.recordEvent(&ev)
	for _, ch := range s.agentOutputTranscribedSubscribers() {
		if ev.IsFinal {
			ch <- ev
		} else {
			select {
			case ch <- ev:
			default:
			}
		}
	}
}

func (s *AgentSession) agentOutputTranscribedEvents() chan AgentOutputTranscribedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch := make(chan AgentOutputTranscribedEvent, 10)
	s.agentOutputSubs = append(s.agentOutputSubs, ch)
	return ch
}

func (s *AgentSession) agentOutputTranscribedSubscribers() []chan AgentOutputTranscribedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]chan AgentOutputTranscribedEvent(nil), s.agentOutputSubs...)
}

func (s *AgentSession) AgentReasoningTranscribedEvents() <-chan AgentReasoningTranscribedEvent {
	return s.agentReasoningTranscribedEvents()
}

func (s *AgentSession) EmitAgentReasoningTranscribed(ev AgentReasoningTranscribedEvent) {
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now()
	}
	s.recordEvent(&ev)
	for _, ch := range s.agentReasoningTranscribedSubscribers() {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (s *AgentSession) agentReasoningTranscribedEvents() chan AgentReasoningTranscribedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch := make(chan AgentReasoningTranscribedEvent, 10)
	s.agentReasoningSubs = append(s.agentReasoningSubs, ch)
	return ch
}

func (s *AgentSession) agentReasoningTranscribedSubscribers() []chan AgentReasoningTranscribedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]chan AgentReasoningTranscribedEvent(nil), s.agentReasoningSubs...)
}

func (s *AgentSession) SpeechCreatedEvents() <-chan SpeechCreatedEvent {
	return s.speechCreatedEvents()
}

func (s *AgentSession) EmitSpeechCreated(ev SpeechCreatedEvent) {
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now()
	}
	s.recordEvent(&ev)
	primary, primarySubscribed, subscribers := s.speechCreatedSubscribers()
	if primarySubscribed {
		primary <- ev
	} else if primary != nil {
		select {
		case primary <- ev:
		default:
		}
	}
	for _, ch := range subscribers {
		ch <- ev
	}
}

func (s *AgentSession) speechCreatedEvents() chan SpeechCreatedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.speechCreatedCh == nil {
		s.speechCreatedCh = make(chan SpeechCreatedEvent, 10)
	}
	if !s.speechCreatedSubd {
		s.speechCreatedSubd = true
		return s.speechCreatedCh
	}
	ch := make(chan SpeechCreatedEvent, 10)
	s.speechCreatedSubs = append(s.speechCreatedSubs, ch)
	return ch
}

func (s *AgentSession) speechCreatedSubscribers() (chan SpeechCreatedEvent, bool, []chan SpeechCreatedEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.speechCreatedCh == nil {
		s.speechCreatedCh = make(chan SpeechCreatedEvent, 10)
	}
	return s.speechCreatedCh, s.speechCreatedSubd, append([]chan SpeechCreatedEvent(nil), s.speechCreatedSubs...)
}

func (s *AgentSession) AgentFalseInterruptionEvents() <-chan AgentFalseInterruptionEvent {
	return s.agentFalseInterruptionEvents()
}

func (s *AgentSession) EmitAgentFalseInterruption(ev AgentFalseInterruptionEvent) {
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = NewAgentFalseInterruptionEvent(ev.Resumed).CreatedAt
	}
	s.recordEvent(&ev)
	primary, primarySubscribed, subscribers := s.agentFalseInterruptionSubscribers()
	if primarySubscribed {
		primary <- ev
	} else if primary != nil {
		select {
		case primary <- ev:
		default:
		}
	}
	for _, ch := range subscribers {
		ch <- ev
	}
}

func (s *AgentSession) agentFalseInterruptionEvents() chan AgentFalseInterruptionEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.falseInterruptionCh == nil {
		s.falseInterruptionCh = make(chan AgentFalseInterruptionEvent, 10)
	}
	if !s.falseInterruptSubd {
		s.falseInterruptSubd = true
		return s.falseInterruptionCh
	}
	ch := make(chan AgentFalseInterruptionEvent, 10)
	s.falseInterruptSubs = append(s.falseInterruptSubs, ch)
	return ch
}

func (s *AgentSession) agentFalseInterruptionSubscribers() (chan AgentFalseInterruptionEvent, bool, []chan AgentFalseInterruptionEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.falseInterruptionCh == nil {
		s.falseInterruptionCh = make(chan AgentFalseInterruptionEvent, 10)
	}
	return s.falseInterruptionCh, s.falseInterruptSubd, append([]chan AgentFalseInterruptionEvent(nil), s.falseInterruptSubs...)
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
	primary, primarySubscribed, subscribers := s.userTurnExceededSubscribers()
	if primarySubscribed {
		primary <- ev
	} else if primary != nil {
		select {
		case primary <- ev:
		default:
		}
	}
	for _, ch := range subscribers {
		ch <- ev
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
	if !s.userTurnExceededSubd {
		s.userTurnExceededSubd = true
		return s.userTurnExceededCh
	}
	ch := make(chan UserTurnExceededEvent, 10)
	s.userTurnExceededSubs = append(s.userTurnExceededSubs, ch)
	return ch
}

func (s *AgentSession) userTurnExceededSubscribers() (chan UserTurnExceededEvent, bool, []chan UserTurnExceededEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.userTurnExceededCh == nil {
		s.userTurnExceededCh = make(chan UserTurnExceededEvent, 10)
	}
	return s.userTurnExceededCh, s.userTurnExceededSubd, append([]chan UserTurnExceededEvent(nil), s.userTurnExceededSubs...)
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
	primary, primarySubscribed, subscribers := s.overlappingSpeechSubscribers()
	if primarySubscribed {
		primary <- ev
	} else if primary != nil {
		select {
		case primary <- ev:
		default:
		}
	}
	for _, ch := range subscribers {
		ch <- ev
	}
}

func (s *AgentSession) overlappingSpeechEvents() chan OverlappingSpeechEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.overlappingSpeechCh == nil {
		s.overlappingSpeechCh = make(chan OverlappingSpeechEvent, 10)
	}
	if !s.overlappingSubd {
		s.overlappingSubd = true
		return s.overlappingSpeechCh
	}
	ch := make(chan OverlappingSpeechEvent, 10)
	s.overlappingSubs = append(s.overlappingSubs, ch)
	return ch
}

func (s *AgentSession) overlappingSpeechSubscribers() (chan OverlappingSpeechEvent, bool, []chan OverlappingSpeechEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.overlappingSpeechCh == nil {
		s.overlappingSpeechCh = make(chan OverlappingSpeechEvent, 10)
	}
	return s.overlappingSpeechCh, s.overlappingSubd, append([]chan OverlappingSpeechEvent(nil), s.overlappingSubs...)
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
	primary, primarySubscribed, subscribers := s.conversationItemAddedSubscribers()
	if primarySubscribed {
		primary <- ev
	} else if primary != nil {
		select {
		case primary <- ev:
		default:
		}
	}
	for _, ch := range subscribers {
		ch <- ev
	}
}

func (s *AgentSession) conversationItemAddedEvents() chan ConversationItemAddedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conversationItemCh == nil {
		s.conversationItemCh = make(chan ConversationItemAddedEvent, 10)
	}
	if !s.conversationItemSubd {
		s.conversationItemSubd = true
		return s.conversationItemCh
	}
	ch := make(chan ConversationItemAddedEvent, 10)
	s.conversationItemSubs = append(s.conversationItemSubs, ch)
	return ch
}

func (s *AgentSession) conversationItemAddedSubscribers() (chan ConversationItemAddedEvent, bool, []chan ConversationItemAddedEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conversationItemCh == nil {
		s.conversationItemCh = make(chan ConversationItemAddedEvent, 10)
	}
	return s.conversationItemCh, s.conversationItemSubd, append([]chan ConversationItemAddedEvent(nil), s.conversationItemSubs...)
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

func (s *AgentSession) EmitFunctionToolsExecuted(ev FunctionToolsExecutedEvent) FunctionToolsExecutedEvent {
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now()
	}
	s.mu.Lock()
	runState := s.runState
	s.mu.Unlock()
	for i, call := range ev.FunctionCalls {
		if i >= len(ev.FunctionCallOutputs) || ev.FunctionCallOutputs[i] == nil {
			continue
		}
		s.insertChatItem(call)
		if runState != nil {
			runState.RecordItem(call)
		}
	}
	for _, output := range ev.FunctionCallOutputs {
		if output != nil {
			s.insertChatItem(output)
			if runState != nil {
				runState.RecordItem(output)
			}
		}
	}
	s.recordEvent(&ev)
	primary, primarySubscribed, subscribers := s.functionToolsExecutedSubscribers()
	if primarySubscribed {
		primary <- ev
	} else if primary != nil {
		select {
		case primary <- ev:
		default:
		}
	}
	for _, ch := range subscribers {
		ch <- ev
	}
	return ev
}

func (s *AgentSession) functionToolsExecutedEvents() chan FunctionToolsExecutedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.functionToolsCh == nil {
		s.functionToolsCh = make(chan FunctionToolsExecutedEvent, 10)
	}
	if !s.functionToolsSubd {
		s.functionToolsSubd = true
		return s.functionToolsCh
	}
	ch := make(chan FunctionToolsExecutedEvent, 10)
	s.functionToolsSubs = append(s.functionToolsSubs, ch)
	return ch
}

func (s *AgentSession) functionToolsExecutedSubscribers() (chan FunctionToolsExecutedEvent, bool, []chan FunctionToolsExecutedEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.functionToolsCh == nil {
		s.functionToolsCh = make(chan FunctionToolsExecutedEvent, 10)
	}
	return s.functionToolsCh, s.functionToolsSubd, append([]chan FunctionToolsExecutedEvent(nil), s.functionToolsSubs...)
}

func (s *AgentSession) MetricsCollectedEvents() <-chan MetricsCollectedEvent {
	return s.metricsCollectedEvents()
}

func (s *AgentSession) EmitMetricsCollected(metrics telemetry.AgentMetrics) {
	if metrics == nil {
		return
	}
	telemetry.LogMetrics(metrics)
	telemetry.CollectOTelUsage(metrics)
	if s.MetricsCollector != nil {
		s.MetricsCollector.Collect(metrics)
	}
	if s.ModelUsageCollector != nil {
		s.ModelUsageCollector.Collect(metrics)
	}
	ev := MetricsCollectedEvent{
		Metrics:   metrics,
		CreatedAt: time.Now(),
	}
	s.recordEvent(&ev)
	primary, primarySubscribed, subscribers := s.metricsCollectedSubscribers()
	if primarySubscribed {
		primary <- ev
	} else if primary != nil {
		select {
		case primary <- ev:
		default:
		}
	}
	for _, ch := range subscribers {
		ch <- ev
	}
	if s.MetricsCollector != nil {
		s.EmitSessionUsageUpdated(SessionUsageUpdatedEvent{Usage: s.ModelUsage()})
	}
}

func (s *AgentSession) Usage() telemetry.UsageSummary {
	if s.MetricsCollector == nil {
		return telemetry.UsageSummary{}
	}
	return s.MetricsCollector.GetSummary()
}

func (s *AgentSession) ModelUsage() telemetry.AgentSessionUsage {
	if s.ModelUsageCollector == nil {
		return telemetry.AgentSessionUsage{}
	}
	return s.ModelUsageCollector.Usage()
}

func (s *AgentSession) metricsCollectedEvents() chan MetricsCollectedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.metricsCollectedCh == nil {
		s.metricsCollectedCh = make(chan MetricsCollectedEvent, 10)
	}
	if !s.metricsChSubscribed {
		s.metricsChSubscribed = true
		return s.metricsCollectedCh
	}
	ch := make(chan MetricsCollectedEvent, 10)
	s.metricsSubs = append(s.metricsSubs, ch)
	return ch
}

func (s *AgentSession) metricsCollectedSubscribers() (chan MetricsCollectedEvent, bool, []chan MetricsCollectedEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.metricsCollectedCh == nil {
		s.metricsCollectedCh = make(chan MetricsCollectedEvent, 10)
	}
	return s.metricsCollectedCh, s.metricsChSubscribed, append([]chan MetricsCollectedEvent(nil), s.metricsSubs...)
}

func (s *AgentSession) SessionUsageUpdatedEvents() <-chan SessionUsageUpdatedEvent {
	return s.sessionUsageUpdatedEvents()
}

func (s *AgentSession) EmitSessionUsageUpdated(ev SessionUsageUpdatedEvent) {
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now()
	}
	s.recordEvent(&ev)
	primary, primarySubscribed, subscribers := s.sessionUsageUpdatedSubscribers()
	if primarySubscribed {
		primary <- ev
	} else if primary != nil {
		select {
		case primary <- ev:
		default:
		}
	}
	for _, ch := range subscribers {
		ch <- ev
	}
}

func (s *AgentSession) sessionUsageUpdatedEvents() chan SessionUsageUpdatedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sessionUsageCh == nil {
		s.sessionUsageCh = make(chan SessionUsageUpdatedEvent, 10)
	}
	if !s.sessionUsageChSubbed {
		s.sessionUsageChSubbed = true
		return s.sessionUsageCh
	}
	ch := make(chan SessionUsageUpdatedEvent, 10)
	s.sessionUsageSubs = append(s.sessionUsageSubs, ch)
	return ch
}

func (s *AgentSession) sessionUsageUpdatedSubscribers() (chan SessionUsageUpdatedEvent, bool, []chan SessionUsageUpdatedEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sessionUsageCh == nil {
		s.sessionUsageCh = make(chan SessionUsageUpdatedEvent, 10)
	}
	return s.sessionUsageCh, s.sessionUsageChSubbed, append([]chan SessionUsageUpdatedEvent(nil), s.sessionUsageSubs...)
}

func (s *AgentSession) ErrorEvents() <-chan ErrorEvent {
	return s.errorEvents()
}

func (s *AgentSession) EmitError(ev ErrorEvent) {
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = NewErrorEvent(ev.Error, ev.Source).CreatedAt
	}
	s.recordEvent(&ev)
	primary, primarySubscribed, subscribers := s.errorSubscribers()
	if primarySubscribed {
		primary <- ev
	} else if primary != nil {
		select {
		case primary <- ev:
		default:
		}
	}
	for _, ch := range subscribers {
		ch <- ev
	}
	s.closeOnUnrecoverableError(ev.Error)
}

func (s *AgentSession) closeOnUnrecoverableError(err error) {
	if s == nil || err == nil {
		return
	}
	shouldClose := false
	var llmErr *llm.LLMError
	var sttErr *stt.STTError
	var ttsErr tts.TTSError
	var realtimeErr *llm.RealtimeModelError
	switch {
	case errors.As(err, &llmErr):
		if llmErr.Recoverable {
			return
		}
		s.mu.Lock()
		s.llmErrorCount++
		shouldClose = s.llmErrorCount > s.Options.MaxUnrecoverableErrors
		s.mu.Unlock()
	case errors.As(err, &ttsErr):
		if ttsErr.Recoverable {
			return
		}
		s.mu.Lock()
		s.ttsErrorCount++
		shouldClose = s.ttsErrorCount > s.Options.MaxUnrecoverableErrors
		s.mu.Unlock()
	case errors.As(err, &sttErr):
		shouldClose = !sttErr.Recoverable
	case errors.As(err, &realtimeErr):
		shouldClose = !realtimeErr.Recoverable
	}
	if shouldClose {
		s.closeSoon(CloseReasonError, err)
	}
}

func (s *AgentSession) startAECWarmupLocked() {
	if s == nil || s.Options.AECWarmupDuration <= 0 || s.aecWarmupDone || s.aecWarmupTimer != nil {
		return
	}
	timeout := time.Duration(s.Options.AECWarmupDuration * float64(time.Second))
	s.aecWarmupTimer = time.AfterFunc(timeout, func() {
		s.mu.Lock()
		s.aecWarmupTimer = nil
		s.aecWarmupDone = true
		s.mu.Unlock()
	})
}

func (s *AgentSession) clearAECWarmupLocked() {
	if s.aecWarmupTimer != nil {
		s.aecWarmupTimer.Stop()
		s.aecWarmupTimer = nil
	}
	s.aecWarmupDone = true
}

func (s *AgentSession) cancelUserAwayTimerLocked() {
	if s.userAwayTimer != nil {
		s.userAwayTimer.Stop()
		s.userAwayTimer = nil
	}
}

func (s *AgentSession) aecWarmupActive() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.aecWarmupTimer != nil
}

func (s *AgentSession) shouldSilenceInputAudio() bool {
	if s == nil {
		return false
	}
	if s.aecWarmupActive() {
		return true
	}
	s.mu.Lock()
	activity := s.activity
	discard := s.Options.DiscardAudioIfUninterruptible
	s.mu.Unlock()
	return discard && activity.uninterruptibleSpeechActive()
}

func (s *AgentSession) errorEvents() chan ErrorEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.errorCh == nil {
		s.errorCh = make(chan ErrorEvent, 10)
	}
	if !s.errorChSubscribed {
		s.errorChSubscribed = true
		return s.errorCh
	}
	ch := make(chan ErrorEvent, 10)
	s.errorSubs = append(s.errorSubs, ch)
	return ch
}

func (s *AgentSession) errorSubscribers() (chan ErrorEvent, bool, []chan ErrorEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.errorCh == nil {
		s.errorCh = make(chan ErrorEvent, 10)
	}
	return s.errorCh, s.errorChSubscribed, append([]chan ErrorEvent(nil), s.errorSubs...)
}

func (s *AgentSession) SipDTMFEvents() <-chan SipDTMFEvent {
	return s.sipDTMFEvents()
}

func (s *AgentSession) EmitSipDTMF(ev SipDTMFEvent) {
	primary, primarySubscribed, subscribers := s.sipDTMFSubscribers()
	if primarySubscribed {
		primary <- ev
	} else if primary != nil {
		select {
		case primary <- ev:
		default:
		}
	}
	for _, ch := range subscribers {
		ch <- ev
	}
}

func (s *AgentSession) sipDTMFEvents() chan SipDTMFEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sipDTMFCh == nil {
		s.sipDTMFCh = make(chan SipDTMFEvent, 10)
	}
	if !s.sipDTMFSubd {
		s.sipDTMFSubd = true
		return s.sipDTMFCh
	}
	ch := make(chan SipDTMFEvent, 10)
	s.sipDTMFSubs = append(s.sipDTMFSubs, ch)
	return ch
}

func (s *AgentSession) sipDTMFSubscribers() (chan SipDTMFEvent, bool, []chan SipDTMFEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sipDTMFCh == nil {
		s.sipDTMFCh = make(chan SipDTMFEvent, 10)
	}
	return s.sipDTMFCh, s.sipDTMFSubd, append([]chan SipDTMFEvent(nil), s.sipDTMFSubs...)
}

func (s *AgentSession) CloseEvents() <-chan CloseEvent {
	return s.closeEvents()
}

func (s *AgentSession) CloseSoon(reason CloseReason) {
	s.closeSoon(reason, nil)
}

func (s *AgentSession) closeSoon(reason CloseReason, err error) {
	s.mu.Lock()
	started := s.started
	activity := s.activity
	agent := s.Agent
	if !started && activity == nil && agent != nil {
		s.mu.Unlock()
		return
	}
	if s.closing {
		s.mu.Unlock()
		return
	}
	s.closing = true
	s.cancelUserAwayTimerLocked()
	s.clearAECWarmupLocked()
	closeEventListeners := s.closeEventListenersLocked()
	closePrimary, closePrimarySubscribed, closeSubscribers := s.closeSubscribersLocked()
	s.mu.Unlock()

	if activity != nil {
		if err := activity.Interrupt(true); err != nil {
			s.EmitError(ErrorEvent{Error: err, CreatedAt: time.Now()})
		}
	}
	_ = s.stop(context.Background(), reason != CloseReasonError)

	ev := &CloseEvent{Reason: reason, Error: err, CreatedAt: time.Now()}
	s.appendRecordedEvent(ev)
	for _, listener := range closeEventListeners {
		callAgentSessionListener("close", listener, ev)
	}
	if closePrimarySubscribed {
		closePrimary <- *ev
	} else if closePrimary != nil {
		select {
		case closePrimary <- *ev:
		default:
		}
	}
	for _, ch := range closeSubscribers {
		ch <- *ev
	}
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
	if !s.closeChSubscribed {
		s.closeChSubscribed = true
		return s.closeCh
	}
	ch := make(chan CloseEvent, 10)
	s.closeSubs = append(s.closeSubs, ch)
	return ch
}

func (s *AgentSession) closeSubscribersLocked() (chan CloseEvent, bool, []chan CloseEvent) {
	if s.closeCh == nil {
		s.closeCh = make(chan CloseEvent, 10)
	}
	return s.closeCh, s.closeChSubscribed, append([]chan CloseEvent(nil), s.closeSubs...)
}

func (s *AgentSession) closeEventListenersLocked() []func(Event) {
	registered := s.eventListeners["close"]
	listeners := make([]func(Event), 0, len(registered))
	for _, listener := range registered {
		listeners = append(listeners, listener.callback)
	}
	return listeners
}

func callAgentSessionListener(eventType string, listener func(Event), ev Event) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Logger.Warnw("failed to emit event "+eventType, panicAsError(recovered))
		}
	}()
	listener(ev)
}

func (s *AgentSession) appendRecordedEvent(ev Event) {
	if ev == nil {
		return
	}
	s.mu.Lock()
	s.recordedEvents = append(s.recordedEvents, ev)
	s.mu.Unlock()
}

func (s *AgentSession) Start(ctx context.Context) error {
	_, err := s.StartWithOptions(ctx, StartOptions{})
	return err
}

func (s *AgentSession) StartWithOptions(ctx context.Context, opts StartOptions) (*RunResult, error) {
	var startDone chan struct{}
	for {
		s.mu.Lock()
		if s.started {
			s.mu.Unlock()
			return nil, nil
		}
		if s.starting {
			done := s.startDone
			s.mu.Unlock()
			if done != nil {
				<-done
			}
			continue
		}
		if opts.CaptureRun && s.runState != nil && !s.runState.Done() {
			s.mu.Unlock()
			return nil, ErrAgentSessionNestedRun
		}

		startDone = make(chan struct{})
		s.starting = true
		s.startDone = startDone
		break
	}

	if s.VAD == nil {
		s.VAD = vad.NewSimpleVAD(0.05)
	}

	assistant := s.ensureAssistantLocked()
	if pipeline, ok := assistant.(*PipelineAgent); ok {
		pipeline.ttsStreamPacer = s.Options.TTSStreamPacer
	}
	agent := s.Agent
	avatar := agent.GetAgent().Avatar
	backgroundAudio := s.Options.BackgroundAudio
	room := s.Room
	hasMetricsCollector := s.MetricsCollector != nil
	var runState *RunResult
	if opts.CaptureRun {
		runState = newRunResultFromOptions(s.ChatCtx, "", opts.OutputType)
		s.runState = runState
	}
	s.mu.Unlock()

	if err := s.validateRealtimeTurnDetectionInterruptions(assistant); err != nil {
		s.clearRunStateIfCurrent(runState)
		s.clearStartingIfCurrent(startDone)
		return nil, err
	}

	s.UpdateAgentState(AgentStateInitializing)

	if backgroundAudio != nil && room != nil {
		if err := backgroundAudio.Start(room, s); err != nil {
			s.clearRunStateIfCurrent(runState)
			s.clearStartingIfCurrent(startDone)
			return nil, err
		}
	}
	var unsubscribeAvatarMetrics func()
	if avatar != nil {
		if metricsSource, ok := avatar.(AvatarMetricsSource); ok {
			unsubscribeAvatarMetrics = metricsSource.OnMetricsCollected(func(metrics *telemetry.AvatarMetrics) {
				s.EmitMetricsCollected(metrics)
			})
		}
		if err := avatar.Start(ctx); err != nil {
			if unsubscribeAvatarMetrics != nil {
				unsubscribeAvatarMetrics()
			}
			if backgroundAudio != nil {
				_ = backgroundAudio.Close()
			}
			s.clearRunStateIfCurrent(runState)
			s.clearStartingIfCurrent(startDone)
			return nil, err
		}
	}
	if err := assistant.Start(ctx, s); err != nil {
		if unsubscribeAvatarMetrics != nil {
			unsubscribeAvatarMetrics()
		}
		if backgroundAudio != nil {
			_ = backgroundAudio.Close()
		}
		s.clearRunStateIfCurrent(runState)
		s.clearStartingIfCurrent(startDone)
		return nil, err
	}

	activity := NewAgentActivity(agent, s)
	s.mu.Lock()
	s.activity = activity
	s.started = true
	s.closing = false
	s.runCtx = ctx
	if s.startDone == startDone {
		s.starting = false
		close(startDone)
		s.startDone = nil
	}
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

	if runState != nil {
		if err := runState.Wait(ctx); err != nil {
			s.clearRunStateIfCurrent(runState)
			return nil, err
		}
	}

	return runState, nil
}

func (s *AgentSession) clearRunStateIfCurrent(runState *RunResult) {
	if runState == nil {
		return
	}
	s.mu.Lock()
	if s.runState == runState {
		s.runState = nil
	}
	s.mu.Unlock()
}

func (s *AgentSession) clearStartingIfCurrent(startDone chan struct{}) {
	if startDone == nil {
		return
	}
	s.mu.Lock()
	if s.startDone == startDone {
		s.starting = false
		close(startDone)
		s.startDone = nil
	}
	s.mu.Unlock()
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
	var flushHeldSTTActivity *AgentActivity
	s.mu.Lock()
	oldState := s.agentState
	if oldState == state {
		s.mu.Unlock()
		return
	}
	s.agentState = state
	backgroundAudio := s.Options.BackgroundAudio
	endpointing := s.Options.Endpointing
	activity := s.activity
	var avatar AvatarProvider
	if s.Agent != nil && s.Agent.GetAgent() != nil {
		avatar = s.Agent.GetAgent().Avatar
	}
	if state == AgentStateSpeaking {
		s.llmErrorCount = 0
		s.ttsErrorCount = 0
		s.startAECWarmupLocked()
	}
	nowTime := time.Now()
	now := float64(nowTime.UnixNano()) / float64(time.Second)
	if state == AgentStateSpeaking {
		if endpointing != nil {
			endpointing.OnStartOfAgentSpeech(now)
		}
		if activity != nil {
			activity.armBackchannelBoundary(nowTime)
		}
	} else if oldState == AgentStateSpeaking {
		if endpointing != nil {
			endpointing.OnEndOfAgentSpeech(now)
		}
		if activity != nil {
			activity.onAgentSpeechEnded(nowTime)
			flushHeldSTTActivity = activity
		}
	}
	s.mu.Unlock()

	if flushHeldSTTActivity != nil {
		flushHeldSTTActivity.flushHeldSTTEvents()
	}

	if backgroundAudio != nil {
		backgroundAudio.AgentStateChanged(state)
	}
	if avatar != nil {
		if err := avatar.UpdateState(avatarStateForAgentState(state)); err != nil {
			logger.Logger.Warnw("avatar state update failed", err, "state", state)
		}
	}
	s.updateUserAwayTimer()

	logger.Logger.Debugw("Agent state changed", "old", oldState, "new", state)
	ev := AgentStateChangedEvent{
		OldState:  oldState,
		NewState:  state,
		CreatedAt: time.Now(),
	}
	s.recordEvent(&ev)
	primary, subscribers := s.agentStateChangedSubscribers()
	if primary != nil {
		select {
		case primary <- ev:
		default:
		}
	}
	for _, ch := range subscribers {
		ch <- ev
	}
}

func (s *AgentSession) UpdateUserState(state UserState) {
	s.updateUserStateAt(state, time.Time{})
}

func (s *AgentSession) updateUserStateAt(state UserState, createdAt time.Time) {
	s.mu.Lock()
	if s.userTurnClaims > 0 && state != UserStateSpeaking {
		s.mu.Unlock()
		return
	}
	oldState := s.userState
	s.userState = state
	videoSampler := s.videoSampler
	s.mu.Unlock()

	if videoSampler != nil {
		videoSampler.SetSpeaking(state == UserStateSpeaking)
	}

	if oldState != state {
		s.updateUserAwayTimer()
	}

	if oldState != state {
		if createdAt.IsZero() {
			createdAt = time.Now()
		}
		logger.Logger.Debugw("User state changed", "old", oldState, "new", state)
		ev := UserStateChangedEvent{
			OldState:  oldState,
			NewState:  state,
			CreatedAt: createdAt,
		}
		s.recordEvent(&ev)
		primary, subscribers := s.userStateChangedSubscribers()
		if primary != nil {
			select {
			case primary <- ev:
			default:
			}
		}
		for _, ch := range subscribers {
			ch <- ev
		}
	}
}

func (s *AgentSession) updateUserAwayTimer() {
	s.mu.Lock()
	if s.userAwayTimer != nil {
		s.userAwayTimer.Stop()
		s.userAwayTimer = nil
	}
	shouldStart := !s.Options.DisableUserAwayTimeout &&
		s.Options.UserAwayTimeout >= 0 &&
		s.agentState == AgentStateListening &&
		s.userState == UserStateListening
	timeout := s.Options.UserAwayTimeout
	gate := s.userAwayGate
	s.mu.Unlock()

	if !shouldStart {
		return
	}
	if gate != nil && gate() {
		return
	}

	timer := time.AfterFunc(time.Duration(timeout*float64(time.Second)), func() {
		s.markUserAwayIfStillIdle()
	})

	s.mu.Lock()
	if s.agentState == AgentStateListening && s.userState == UserStateListening && s.userAwayTimer == nil {
		s.userAwayTimer = timer
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	timer.Stop()
}

func (s *AgentSession) markUserAwayIfStillIdle() {
	s.mu.Lock()
	shouldMarkAway := s.agentState == AgentStateListening && s.userState == UserStateListening
	s.mu.Unlock()
	if shouldMarkAway {
		s.UpdateUserState(UserStateAway)
	}
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
	assistant := s.Assistant
	closing := s.closing
	onEnterActive := s.onEnterDepth > 0
	s.mu.Unlock()

	if activity == nil {
		if closing {
			return nil, errAgentSessionClosingReply
		}
		return nil, ErrAgentSessionNotRunning
	}
	if pipeline, ok := assistant.(*PipelineAgent); ok && pipeline.LLM == nil {
		return nil, errGenerateReplyMissingLLM
	}
	if len(opts.Tools) > 0 {
		registeredTools, err := sessionRegisteredTools(ctx, s)
		if err != nil {
			return nil, err
		}
		if _, err := resolveToolsByID(registeredTools, opts.Tools); err != nil {
			return nil, err
		}
	}

	// Trigger the pipeline
	loggedUserInput := opts.UserInput
	if loggedUserInput == "" && opts.UserMessage != nil {
		loggedUserInput = opts.UserMessage.TextContent()
	}
	logger.Logger.Infow("Generating reply", "userInput", loggedUserInput)

	allowInterruptions := s.defaultAllowInterruptions()
	if opts.AllowInterruptions != nil {
		allowInterruptions = *opts.AllowInterruptions
	}
	if realtimeTurnDetectionEnabled(assistant) && opts.AllowInterruptions != nil && !*opts.AllowInterruptions {
		allowInterruptions = s.defaultAllowInterruptions()
	}
	inputModality := opts.InputModality
	if inputModality == "" {
		inputModality = "text"
	}
	handle := NewSpeechHandle(allowInterruptions, InputDetails{Modality: inputModality})
	if opts.InstructionVariants != nil {
		handle.Generation.Instructions = opts.InstructionVariants
	} else if opts.Instructions != "" {
		handle.Generation.Instructions = llm.NewInstructions(opts.Instructions)
	}
	if opts.ToolChoice != nil {
		handle.Generation.ToolChoice = opts.ToolChoice
	} else if runCtx := GetRunContext(ctx); runCtx != nil && runCtx.FunctionCall != nil {
		handle.Generation.ToolChoice = "none"
	} else if s.Options.ToolChoice != nil {
		handle.Generation.ToolChoice = s.Options.ToolChoice
	}
	if onEnterActive {
		handle.Generation.IgnoreOnEnterTools = true
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
	userInitiated := true
	if opts.UserInitiated != nil {
		userInitiated = *opts.UserInitiated
	}
	s.EmitSpeechCreated(SpeechCreatedEvent{
		UserInitiated: userInitiated,
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
	if userMessage != nil && handle.Generation.UserMessage == nil {
		handle.Generation.UserMessage = userMessage
	}

	scheduleSpeech := true
	if opts.ScheduleSpeech != nil {
		scheduleSpeech = *opts.ScheduleSpeech
	}
	if !scheduleSpeech {
		return handle, nil
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
	assistant := s.Assistant
	closing := s.closing
	s.mu.Unlock()

	if activity == nil {
		if closing {
			return nil, errAgentSessionClosingSay
		}
		return nil, ErrAgentSessionNotRunning
	}
	if s.AudioOutputController() != nil && !s.hasActiveTTS(assistant) {
		nativeSay, ok := assistant.(nativeSayAssistant)
		if !ok || !nativeSay.SupportsNativeSay() {
			return nil, errSayMissingTTSWithAudioOutput
		}
	}

	logger.Logger.Infow("Saying text", "text", opts.Text)

	allowInterruptions := s.defaultAllowInterruptions()
	if opts.AllowInterruptions != nil {
		allowInterruptions = *opts.AllowInterruptions
	}
	if realtimeTurnDetectionEnabled(assistant) && opts.AllowInterruptions != nil && !*opts.AllowInterruptions {
		allowInterruptions = s.defaultAllowInterruptions()
	}
	addToChatContext := true
	if opts.AddToChatContext != nil {
		addToChatContext = *opts.AddToChatContext
	}
	if nativeSay, ok := assistant.(nativeSayAssistant); ok && nativeSay.SupportsNativeSay() {
		addToChatContext = true
	}

	handle := NewSpeechHandle(allowInterruptions, InputDetails{Modality: "text"})
	handle.Generation.Text = opts.Text
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
		handle.Generation.AssistantMessage = assistantMessage
	}

	if err := activity.ScheduleSpeech(handle, SpeechPriorityNormal, false); err != nil {
		return nil, err
	}
	s.watchActiveRunSpeechHandle(handle)
	return handle, nil
}

func realtimeTurnDetectionEnabled(assistant any) bool {
	if capabilities, ok := assistant.(realtimeCapabilitiesAssistant); ok {
		return capabilities.RealtimeCapabilities().TurnDetection
	}
	return false
}

func (s *AgentSession) hasActiveTTS(assistant any) bool {
	if s == nil {
		return false
	}
	if s.TTS != nil {
		return true
	}
	if pipeline, ok := assistant.(*PipelineAgent); ok {
		return pipeline.tts != nil
	}
	return false
}

func realtimeModelTurnDetectionEnabled(model llm.RealtimeModel) bool {
	return model != nil && model.Capabilities().TurnDetection
}

func (s *AgentSession) validateRealtimeTurnDetectionInterruptions(assistant any) error {
	if realtimeTurnDetectionEnabled(assistant) && !s.defaultAllowInterruptions() {
		return errRealtimeTurnDetectionInterruptionsDisabled
	}
	return nil
}

func (s *AgentSession) defaultAllowInterruptions() bool {
	return s.defaultAllowInterruptionsForAgent(s.agentConfig())
}

func (s *AgentSession) defaultAllowInterruptionsForAgent(agent *Agent) bool {
	allowInterruptions := s.Options.AllowInterruptions
	if agent == nil {
		return allowInterruptions
	}
	if agent.AllowInterruptionsSet || agent.AllowInterruptions {
		return agent.AllowInterruptions
	}
	return allowInterruptions
}

func (s *AgentSession) agentConfig() *Agent {
	if s.Agent == nil {
		return nil
	}
	return s.Agent.GetAgent()
}

func (s *AgentSession) watchActiveRunSpeechHandle(handle *SpeechHandle) bool {
	s.mu.Lock()
	runState := s.runState
	s.mu.Unlock()
	if runState != nil {
		return runState.WatchSpeechHandle(handle)
	}
	return false
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
	if s.started && s.invalidRealtimeTurnDetectionHandoffLocked(baseAgent) {
		s.mu.Unlock()
		s.EmitError(ErrorEvent{Error: errRealtimeTurnDetectionInterruptionsDisabled, Source: agent})
		return
	}
	oldActivity := s.activity
	started := s.started
	oldTools := copySessionTools(s.Tools)
	s.Agent = agent
	s.Tools = copySessionTools(baseAgent.Tools)
	s.updateAgentComponentsLocked(baseAgent)
	assistant := s.Assistant
	sessionVAD := s.VAD
	sessionSTT := s.STT
	sessionLLM := s.LLM
	sessionTTS := s.TTS
	sessionRealtimeModel := s.RealtimeModel
	if !started {
		s.mu.Unlock()
		return
	}
	runCtx := s.runCtx
	previousAssistant := assistant
	replacementAssistant := s.replacementAssistantLocked(assistant)
	if replacementAssistant != nil {
		s.Assistant = replacementAssistant
		assistant = replacementAssistant
	}
	oldAgent := (*Agent)(nil)
	if oldActivity != nil {
		oldAgent = oldActivity.Agent
		oldActivity.PauseScheduling()
	}
	runState := s.runState

	newActivity := NewAgentActivity(agent, s)
	s.activity = newActivity
	s.mu.Unlock()

	if replacementAssistant != nil {
		if runCtx == nil {
			runCtx = context.Background()
		}
		if err := replacementAssistant.Start(runCtx, s); err != nil {
			logger.Logger.Errorw("failed to start replacement assistant", err)
			s.EmitError(ErrorEvent{Error: err, Source: replacementAssistant})
		} else if closer, ok := previousAssistant.(closeableSessionAssistant); ok {
			if err := closer.Close(); err != nil {
				logger.Logger.Errorw("failed to close replaced assistant", err)
				s.EmitError(ErrorEvent{Error: err, Source: previousAssistant})
			}
		}
	} else if updater, ok := assistant.(componentUpdatingAssistant); ok {
		updater.UpdateComponents(sessionVAD, sessionSTT, sessionLLM, sessionTTS)
	}
	if replacementAssistant == nil {
		if updater, ok := assistant.(realtimeModelUpdatingAssistant); ok {
			if err := updater.UpdateRealtimeModel(context.Background(), sessionRealtimeModel); err != nil {
				logger.Logger.Errorw("failed to update realtime model on assistant", err)
				s.EmitError(ErrorEvent{Error: err, Source: sessionRealtimeModel})
			}
		}
	}
	if oldActivity != nil {
		oldActivity.Stop()
		if err := closeSessionToolsets(oldTools); err != nil {
			logger.Logger.Errorw("failed to close previous agent toolsets", err)
			s.EmitError(ErrorEvent{Error: err, Source: oldAgent})
		}
	}
	handoff := newAgentHandoff(oldAgent, baseAgent)
	if runState != nil {
		runState.RecordAgentHandoff(handoff, oldAgent, baseAgent)
	}
	s.EmitConversationItemAdded(handoff)
	newActivity.Start()
}

func (s *AgentSession) invalidRealtimeTurnDetectionHandoffLocked(agent *Agent) bool {
	if s.defaultAllowInterruptionsForAgent(agent) {
		return false
	}
	realtimeModel := s.RealtimeModel
	if agent != nil && agent.RealtimeModel != nil {
		realtimeModel = agent.RealtimeModel
	}
	return realtimeModelTurnDetectionEnabled(realtimeModel)
}

func copySessionTools(tools []llm.Tool) []llm.Tool {
	if len(tools) == 0 {
		return []llm.Tool{}
	}
	return append([]llm.Tool(nil), tools...)
}

func (s *AgentSession) updateAgentComponentsLocked(agent *Agent) {
	if agent.STT != nil {
		s.STT = agent.STT
	}
	if agent.VAD != nil {
		s.VAD = agent.VAD
	}
	if agent.LLM != nil {
		s.LLM = agent.LLM
		s.RealtimeModel = nil
	}
	if agent.RealtimeModel != nil {
		s.RealtimeModel = agent.RealtimeModel
	}
	if agent.TTS != nil {
		s.TTS = agent.TTS
	}
}

func newAgentHandoff(oldAgent *Agent, newAgent *Agent) *llm.AgentHandoff {
	var oldAgentID *string
	if oldID := agentHandoffID(oldAgent); oldID != "" {
		oldAgentID = &oldID
	}
	return &llm.AgentHandoff{
		ID:         "handoff_" + uuid.NewString()[:12],
		OldAgentID: oldAgentID,
		NewAgentID: agentHandoffID(newAgent),
		CreatedAt:  time.Now(),
	}
}

func agentHandoffID(agent *Agent) string {
	if agent == nil {
		return ""
	}
	if agent.ID != "" {
		return agent.ID
	}
	return agentInstructionsText(agent)
}

func (s *AgentSession) Stop(ctx context.Context) error {
	return s.stop(ctx, true)
}

func (s *AgentSession) stop(ctx context.Context, commitPendingUserTurn bool) error {
	s.mu.Lock()
	if !s.started {
		s.clearEventListenersLocked()
		s.mu.Unlock()
		return nil
	}

	activity := s.activity
	ivrActivity := s.ivrActivity
	assistant := s.Assistant
	s.activity = nil
	s.ivrActivity = nil
	s.started = false
	s.clearEventListenersLocked()
	s.runCtx = nil
	s.userState = UserStateListening
	s.agentState = AgentStateInitializing
	if s.userTurnClaims > 0 && s.userTurnDone != nil {
		close(s.userTurnDone)
	}
	s.userTurnClaims = 0
	s.userTurnDone = nil
	if s.idleHolds > 0 && s.idleDone != nil {
		close(s.idleDone)
	}
	s.idleHolds = 0
	s.idleDone = nil
	s.llmErrorCount = 0
	s.ttsErrorCount = 0
	s.cancelUserAwayTimerLocked()
	s.clearAECWarmupLocked()
	backgroundAudio := s.Options.BackgroundAudio
	mcpServers := append([]llm.MCPServer(nil), s.mcpServers...)
	sessionTools := copySessionTools(s.Tools)
	s.mu.Unlock()

	if ivrActivity != nil {
		ivrActivity.Stop()
	}
	var stopErr error
	if activity != nil {
		if commitPendingUserTurn {
			commitCtx := ctx
			parentCtx := ctx
			var cancel context.CancelFunc
			if commitCtx == nil {
				commitCtx = context.Background()
				parentCtx = commitCtx
			}
			if timeout := s.Options.SessionCloseTranscriptTimeout; timeout > 0 {
				commitCtx, cancel = context.WithTimeout(commitCtx, time.Duration(timeout*float64(time.Second)))
			}
			if _, err := activity.CommitUserTurn(commitCtx, CommitUserTurnOptions{
				TranscriptTimeout: time.Duration(s.Options.SessionCloseTranscriptTimeout * float64(time.Second)),
				SkipRealtimeAudio: true,
			}); err != nil && !(cancel != nil && errors.Is(err, context.DeadlineExceeded) && parentCtx.Err() == nil) {
				stopErr = err
			}
			if cancel != nil {
				cancel()
			}
		}
		if task, ok := activity.AgentIntf.(interface{ Cancel() }); ok {
			task.Cancel()
		}
		if err := s.toolExecutionRegistry.drain(ctx); err != nil && stopErr == nil {
			stopErr = err
		}
		activity.Stop()
	}
	if closer, ok := assistant.(closeableSessionAssistant); ok {
		if err := closer.Close(); err != nil && stopErr == nil {
			stopErr = err
		}
	}
	for _, server := range mcpServers {
		if server == nil {
			continue
		}
		if err := server.Close(); err != nil && stopErr == nil {
			stopErr = err
		}
	}
	if err := closeSessionToolsets(sessionTools); err != nil && stopErr == nil {
		stopErr = err
	}
	s.flushOTelTurnMetrics()
	if backgroundAudio != nil {
		_ = backgroundAudio.Close()
	}
	return stopErr
}

func closeSessionToolsets(tools []llm.Tool) error {
	var errs []error
	for _, tool := range tools {
		if err := closeSessionToolsetValue(tool); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func closeSessionToolsetValue(tool llm.Tool) error {
	toolset, ok := tool.(llm.Toolset)
	if !ok {
		return nil
	}
	if closeable, ok := toolset.(sessionToolsetCloser); ok {
		return closeable.Close()
	}
	var errs []error
	for _, child := range toolset.Tools() {
		if err := closeSessionToolsetValue(child); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *AgentSession) flushOTelTurnMetrics() {
	if s == nil || s.ChatCtx == nil {
		return
	}
	for _, item := range s.ChatCtx.Items {
		msg, ok := item.(*llm.ChatMessage)
		if !ok || len(msg.Metrics) == 0 {
			continue
		}
		telemetry.RecordOTelTurnMetrics(msg.Metrics)
	}
}
