package agent

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
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
)

var (
	ErrSpeechSchedulingPaused = errors.New("speech scheduling is paused")
	errAgentActivityClosed    = errors.New("agent activity closed")
)

const agentInstructionsMessageID = "lk.agent_task.instructions"
const audioTurnDetectorWindowSeconds = 8.0

type instructionUpdatingAssistant interface {
	UpdateInstructions(context.Context, string) error
}

type toolUpdatingAssistant interface {
	UpdateTools(context.Context) error
}

type chatContextUpdatingAssistant interface {
	UpdateChatContext(context.Context, *llm.ChatContext) error
}

type realtimeAudioCommitter interface {
	CommitAudio() error
}

type realtimeAudioClearer interface {
	ClearAudio() error
}

type realtimeInterrupter interface {
	Interrupt() error
}

type llmMetricsCollector interface {
	OnMetricsCollected(llm.LLMMetricsHandler) func()
}

type llmErrorCollector interface {
	OnError(llm.LLMErrorHandler) func()
}

type ttsMetricsCollector interface {
	OnMetricsCollected(tts.TTSMetricsHandler) func()
}

type sttMetricsCollector interface {
	OnMetricsCollected(stt.STTMetricsHandler) func()
}

type sttErrorCollector interface {
	OnError(stt.STTErrorHandler) func()
}

type ttsErrorCollector interface {
	OnError(tts.TTSErrorHandler) func()
}

type EndOfTurnInfo struct {
	SkipReply              bool
	ReplyAlreadyGenerated  bool
	GenerateReplyAfterHook bool
	NewTranscript          string
	Language               string
	TranscriptConfidence   float64
	EndOfTurnDelay         float64
	TranscriptionDelay     float64
	StartedSpeakingAt      *float64
	StoppedSpeakingAt      *float64
	AudioFrames            []*model.AudioFrame
}

// AgentActivity handles the internal event loops, I/O processing, and
// speech generation queue for an Agent.
type AgentActivity struct {
	AgentIntf        AgentInterface
	Agent            *Agent
	Session          *AgentSession
	agentStateMu     sync.Mutex
	agentStateEvents <-chan AgentStateChangedEvent

	currentSpeech  *SpeechHandle
	speechQueue    []scheduledSpeech
	nextSpeechSeq  uint64
	lastSpeechDone time.Time
	queueMu        sync.Mutex
	queueUpdatedCh chan struct{}

	schedulingPaused   bool
	schedulingDraining bool
	schedulingStarted  bool
	startedCh          chan struct{}
	startedOnce        sync.Once

	sttEOSReceived       bool
	speaking             bool
	speakingMu           sync.RWMutex
	interruptionDetected bool
	overlapSpeechEnded   bool
	manualTurnCommitted  bool

	providerUnsubscribes []func()
	registeredTools      []llm.Tool

	userTurnMu                       sync.Mutex
	userTurnUpdatedCh                chan struct{}
	pendingInterimTranscript         string
	pendingInterimLanguage           string
	pendingInterimSpeakerID          string
	pendingPreflightTranscript       string
	pendingPreflightConfidence       float64
	userTurnCompletionMu             sync.Mutex
	userTurnCompletionSeq            uint64
	commitUserTurnMu                 sync.Mutex
	commitUserTurnCancel             context.CancelFunc
	commitUserTurnSeq                uint64
	commitUserTurnActive             int
	userTurnHookActive               int
	pendingUserTranscript            string
	pendingUserLanguage              string
	pendingTranscriptConfidence      float64
	pendingTranscriptConfidenceSum   float64
	pendingTranscriptConfidenceCount int
	pendingUserTranscriptPresent     bool
	lastFinalTranscriptTime          time.Time
	lastUserLanguage                 string
	pendingStartedSpeakingAt         *float64
	pendingStoppedSpeakingAt         *float64
	pendingTranscriptionDelay        float64
	userTurnLimitStartedAt           time.Time
	userTurnLimitTranscript          string
	userTurnLimitWordCount           int

	ctx    context.Context
	cancel context.CancelFunc

	eouMu     sync.Mutex
	eouCancel context.CancelFunc
	eouDone   chan struct{}

	userTurnExceededMu     sync.Mutex
	userTurnExceededLocked bool

	userAudioMu     sync.Mutex
	userAudioFrames []*model.AudioFrame

	preemptiveMu              sync.Mutex
	preemptiveGeneration      *preemptiveGeneration
	preemptiveGenerationCount int
	userSpeechStartedAt       time.Time
	userSpeechStoppedAt       time.Time
	ignoreUserTranscriptUntil time.Time
	heldSTTEvents             []*stt.SpeechEvent
	holdSTTWhileAgentSpeaking bool

	backchannelBoundaryMu    sync.Mutex
	backchannelBoundaryUntil time.Time
	audioActivityDisabled    bool

	falseInterruptionMu    sync.Mutex
	pausedSpeech           *pausedSpeechInfo
	falseInterruptionTimer *time.Timer

	userTurnExceededCancel context.CancelFunc
	userTurnExceededSeq    uint64
}

func NewAgentActivity(agentIntf AgentInterface, session *AgentSession) *AgentActivity {
	ctx, cancel := context.WithCancel(context.Background())
	activity := &AgentActivity{
		AgentIntf:         agentIntf,
		Agent:             agentIntf.GetAgent(),
		Session:           session,
		speechQueue:       make([]scheduledSpeech, 0),
		queueUpdatedCh:    make(chan struct{}, 1),
		startedCh:         make(chan struct{}),
		userTurnUpdatedCh: make(chan struct{}, 1),
		ctx:               ctx,
		cancel:            cancel,
	}
	activity.Agent.activity = activity
	return activity
}

type scheduledSpeech struct {
	speech   *SpeechHandle
	priority int
	seq      uint64
}

type pausedSpeechInfo struct {
	handle     *SpeechHandle
	agentState AgentState
	timeout    time.Duration
}

type inputTranscriptionFlusher interface {
	FlushInputTranscription(context.Context, time.Duration) error
}

type inputTranscriptionClearer interface {
	ClearInputTranscription() error
}

type preemptiveGeneration struct {
	speech     *SpeechHandle
	userMsg    *llm.ChatMessage
	transcript string
	chatCtx    *llm.ChatContext
	toolsKey   string
	toolChoice llm.ToolChoice
	createdAt  time.Time
}

func (a *AgentActivity) Start() {
	if err := a.recordInitialConfiguration(); err != nil {
		logger.Logger.Errorw("failed to record initial agent configuration", err)
	}
	if a.Session != nil && a.Session.LLM != nil {
		if collector, ok := a.Session.LLM.(llmMetricsCollector); ok {
			unsubscribe := collector.OnMetricsCollected(func(metrics *telemetry.LLMMetrics) {
				a.OnMetricsCollected(metrics)
			})
			a.providerUnsubscribes = append(a.providerUnsubscribes, unsubscribe)
		}
		if collector, ok := a.Session.LLM.(llmErrorCollector); ok {
			unsubscribe := collector.OnError(func(err *llm.LLMError) {
				a.OnError(err, a.Session.LLM)
			})
			a.providerUnsubscribes = append(a.providerUnsubscribes, unsubscribe)
		}
	}
	if a.Session != nil && a.Session.TTS != nil {
		if collector, ok := a.Session.TTS.(ttsMetricsCollector); ok {
			unsubscribe := collector.OnMetricsCollected(func(metrics *telemetry.TTSMetrics) {
				a.OnMetricsCollected(metrics)
			})
			a.providerUnsubscribes = append(a.providerUnsubscribes, unsubscribe)
		}
		if collector, ok := a.Session.TTS.(ttsErrorCollector); ok {
			unsubscribe := collector.OnError(func(err tts.TTSError) {
				a.OnError(err, a.Session.TTS)
			})
			a.providerUnsubscribes = append(a.providerUnsubscribes, unsubscribe)
		}
	}
	if a.Session != nil && a.Session.STT != nil {
		if collector, ok := a.Session.STT.(sttMetricsCollector); ok {
			unsubscribe := collector.OnMetricsCollected(func(metrics *telemetry.STTMetrics) {
				a.OnMetricsCollected(metrics)
			})
			a.providerUnsubscribes = append(a.providerUnsubscribes, unsubscribe)
		}
		if collector, ok := a.Session.STT.(sttErrorCollector); ok {
			unsubscribe := collector.OnError(func(err *stt.STTError) {
				a.OnError(err, a.Session.STT)
			})
			a.providerUnsubscribes = append(a.providerUnsubscribes, unsubscribe)
		}
	}
	if a.Session != nil && a.Session.VAD != nil {
		unsubscribe := a.Session.VAD.OnMetricsCollected(func(metrics *telemetry.VADMetrics) {
			a.OnMetricsCollected(metrics)
		})
		a.providerUnsubscribes = append(a.providerUnsubscribes, unsubscribe)
	}
	if a.Session != nil {
		if pipeline, ok := a.Session.Assistant.(*PipelineAgent); ok {
			if pipeline.stt != nil && !sameProviderInstance(pipeline.stt, a.Session.STT) {
				if collector, ok := pipeline.stt.(sttMetricsCollector); ok {
					unsubscribe := collector.OnMetricsCollected(func(metrics *telemetry.STTMetrics) {
						a.OnMetricsCollected(metrics)
					})
					a.providerUnsubscribes = append(a.providerUnsubscribes, unsubscribe)
				}
				if collector, ok := pipeline.stt.(sttErrorCollector); ok {
					sttSource := pipeline.stt
					unsubscribe := collector.OnError(func(err *stt.STTError) {
						a.OnError(err, sttSource)
					})
					a.providerUnsubscribes = append(a.providerUnsubscribes, unsubscribe)
				}
			}
			if pipeline.vad != nil && !sameProviderInstance(pipeline.vad, a.Session.VAD) {
				unsubscribe := pipeline.vad.OnMetricsCollected(func(metrics *telemetry.VADMetrics) {
					a.OnMetricsCollected(metrics)
				})
				a.providerUnsubscribes = append(a.providerUnsubscribes, unsubscribe)
			}
		}
	}
	if a.Session != nil {
		a.Session.mu.Lock()
		a.Session.onEnterDepth++
		a.Session.mu.Unlock()
	}
	func() {
		defer func() {
			if a.Session != nil {
				a.Session.mu.Lock()
				if a.Session.onEnterDepth > 0 {
					a.Session.onEnterDepth--
				}
				a.Session.mu.Unlock()
			}
		}()
		a.AgentIntf.OnEnter()
	}()
	a.queueMu.Lock()
	a.schedulingStarted = true
	a.queueMu.Unlock()
	go a.schedulingTask()
	a.startedOnce.Do(func() { close(a.startedCh) })
}

func (a *AgentActivity) Stop() {
	a.AgentIntf.OnExit()
	for _, unsubscribe := range a.providerUnsubscribes {
		unsubscribe()
	}
	a.providerUnsubscribes = nil
	a.cancel()
	a.queueMu.Lock()
	a.schedulingPaused = true
	a.schedulingDraining = false
	a.schedulingStarted = false
	a.queueMu.Unlock()
	select {
	case a.queueUpdatedCh <- struct{}{}:
	default:
	}
	if a.Agent.activity == a {
		a.Agent.activity = nil
	}
}

func sameProviderInstance(left, right any) bool {
	if left == nil || right == nil {
		return false
	}
	leftValue := reflect.ValueOf(left)
	rightValue := reflect.ValueOf(right)
	if !leftValue.IsValid() || !rightValue.IsValid() || leftValue.Type() != rightValue.Type() {
		return false
	}
	if !leftValue.Type().Comparable() {
		return false
	}
	return leftValue.Interface() == rightValue.Interface()
}

func (a *AgentActivity) SchedulingPaused() bool {
	if a == nil {
		return false
	}
	a.queueMu.Lock()
	defer a.queueMu.Unlock()
	return a.schedulingPaused
}

func (a *AgentActivity) PauseScheduling() {
	if a == nil {
		return
	}
	a.queueMu.Lock()
	a.schedulingPaused = true
	a.queueMu.Unlock()

	select {
	case a.queueUpdatedCh <- struct{}{}:
	default:
	}
}

func (a *AgentActivity) ResumeScheduling() {
	if a == nil {
		return
	}
	a.queueMu.Lock()
	a.schedulingPaused = false
	a.schedulingDraining = false
	a.queueMu.Unlock()

	select {
	case a.queueUpdatedCh <- struct{}{}:
	default:
	}
}

func (a *AgentActivity) CurrentSpeech() *SpeechHandle {
	if a == nil {
		return nil
	}
	a.queueMu.Lock()
	defer a.queueMu.Unlock()
	return a.currentSpeech
}

func (a *AgentActivity) uninterruptibleSpeechActive() bool {
	if a == nil {
		return false
	}
	a.queueMu.Lock()
	defer a.queueMu.Unlock()
	return a.currentSpeech != nil &&
		!a.currentSpeech.IsDone() &&
		!a.currentSpeech.IsInterrupted() &&
		!a.currentSpeech.AllowInterruptions
}

func (a *AgentActivity) Tools() []interface{} {
	if a == nil || a.Agent == nil {
		return nil
	}
	if len(a.registeredTools) > 0 {
		tools := make([]interface{}, 0, len(a.registeredTools))
		for _, tool := range a.registeredTools {
			tools = append(tools, tool)
		}
		return tools
	}
	capacity := len(a.Agent.Tools)
	if a.Session != nil {
		capacity += len(a.Session.Tools)
	}
	tools := make([]interface{}, 0, capacity)
	if a.Session != nil {
		for _, tool := range a.Session.Tools {
			tools = append(tools, tool)
		}
	}
	for _, tool := range a.Agent.Tools {
		tools = append(tools, tool)
	}
	if hasCancellableTool(tools) {
		tools = append(tools, cancelTaskTool{}, getRunningTasksTool{})
	}
	return tools
}

func (a *AgentActivity) AllowInterruptions() bool {
	if a == nil {
		return false
	}
	if a.Session != nil {
		return a.Session.defaultAllowInterruptions()
	}
	if a.Agent != nil && (a.Agent.AllowInterruptionsSet || a.Agent.AllowInterruptions) {
		return a.Agent.AllowInterruptions
	}
	return false
}

func (a *AgentActivity) InterruptionEnabled() bool {
	if a == nil || !a.AllowInterruptions() {
		return false
	}
	switch a.turnDetectionMode() {
	case TurnDetectionModeManual, TurnDetectionModeRealtimeLLM:
		return false
	case "":
		return a.hasVADModel()
	default:
		return true
	}
}

func (a *AgentActivity) EndpointingOpts() EndpointingOptions {
	opts := EndpointingOptions{
		MinDelay: 0.5,
		MaxDelay: 3.0,
	}
	if a == nil {
		return opts
	}
	if a.Session != nil {
		opts.Mode = a.Session.Options.EndpointingMode
		if a.Session.Options.MinEndpointingDelay > 0 {
			opts.MinDelay = a.Session.Options.MinEndpointingDelay
		}
		if a.Session.Options.MaxEndpointingDelay > 0 {
			opts.MaxDelay = a.Session.Options.MaxEndpointingDelay
		}
		if endpointing := a.endpointing(); endpointing != nil {
			opts.MinDelay = endpointing.MinDelay()
			opts.MaxDelay = endpointing.MaxDelay()
		}
	}
	if a.Agent != nil {
		if a.Agent.MinEndpointingDelay > 0 {
			opts.MinDelay = a.Agent.MinEndpointingDelay
		}
		if a.Agent.MaxEndpointingDelay > 0 {
			opts.MaxDelay = a.Agent.MaxEndpointingDelay
		}
	}
	return opts
}

func (a *AgentActivity) recordInitialConfiguration() error {
	if a.Agent.ChatCtx == nil {
		a.Agent.ChatCtx = llm.NewChatContext()
	}
	if a.Session != nil && a.Session.ChatCtx == nil {
		a.Session.ChatCtx = llm.NewChatContext()
	}

	instructions := agentInstructionVariants(a.Agent)
	if err := updateAgentInstructionsMessage(a.Agent.ChatCtx, instructions, instructions != nil); err != nil {
		return err
	}

	tools := a.Tools()
	if a.Session != nil {
		registeredTools, err := sessionRegisteredTools(context.Background(), a.Session)
		if err != nil {
			return err
		}
		a.registeredTools = append([]llm.Tool(nil), registeredTools...)
		tools = agentToolsAsInterfaces(registeredTools)
	}
	toolNames := sortedAgentFunctionToolNames(tools)
	if instructions == nil && len(toolNames) == 0 {
		return nil
	}

	instructionText := agentInstructionsText(a.Agent)
	configUpdate := &llm.AgentConfigUpdate{
		Instructions: stringPtrIfNotEmpty(instructionText),
		ToolsAdded:   toolNames,
		CreatedAt:    time.Now(),
	}
	a.Agent.ChatCtx.Insert(configUpdate)
	if a.Session != nil {
		a.Session.ChatCtx.Insert(configUpdate)
	}
	return nil
}

func (a *AgentActivity) Interrupt(force bool) error {
	return a.interrupt(force, true)
}

func (a *AgentActivity) interrupt(force bool, cancelPreemptive bool) error {
	interrupted, err := a.interruptHandles(force, cancelPreemptive)
	if err != nil {
		return err
	}

	for _, speech := range interrupted {
		if err := speech.Wait(a.ctx); err != nil {
			return err
		}
	}

	return nil
}

func (a *AgentActivity) interruptHandles(force bool, cancelPreemptive bool) ([]*SpeechHandle, error) {
	if cancelPreemptive {
		a.cancelPreemptiveGeneration()
	}

	a.queueMu.Lock()
	interrupted := make([]*SpeechHandle, 0, len(a.speechQueue)+1)
	if a.currentSpeech != nil {
		if err := a.currentSpeech.Interrupt(force); err != nil {
			a.queueMu.Unlock()
			return nil, err
		}
		interrupted = append(interrupted, a.currentSpeech)
	}
	for _, queued := range a.speechQueue {
		if err := queued.speech.Interrupt(force); err != nil {
			a.queueMu.Unlock()
			return nil, err
		}
		interrupted = append(interrupted, queued.speech)
	}
	a.queueMu.Unlock()

	if a.Session != nil {
		a.Session.mu.Lock()
		assistant := a.Session.Assistant
		a.Session.mu.Unlock()
		if interrupter, ok := assistant.(realtimeInterrupter); ok {
			if err := interrupter.Interrupt(); err != nil {
				return nil, err
			}
		}
	}

	return interrupted, nil
}

func (a *AgentActivity) WaitForInactive(ctx context.Context) error {
	for {
		a.processQueue()
		active := a.activeSpeechHandles()
		if len(active) == 0 {
			waitedForStart, err := a.waitForStartIfNeeded(ctx)
			if err != nil {
				return err
			}
			if waitedForStart {
				continue
			}
			if done, ok := a.pendingEOUDetection(); ok {
				select {
				case <-done:
				case <-a.ctx.Done():
					return errAgentActivityClosed
				case <-ctx.Done():
					return ctx.Err()
				}
				continue
			}
			if a.pendingUserTurnCompletion() {
				select {
				case <-a.userTurnUpdated():
				case <-a.ctx.Done():
					return errAgentActivityClosed
				case <-ctx.Done():
					return ctx.Err()
				}
				continue
			}
			if !a.isUserSpeaking() {
				return nil
			}
			select {
			case <-a.userTurnUpdated():
			case <-a.ctx.Done():
				return errAgentActivityClosed
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}
		for _, speech := range active {
			select {
			case <-speech.doneCh:
			case <-a.ctx.Done():
				return errAgentActivityClosed
			case <-ctx.Done():
				return ctx.Err()
			}
			a.processQueue()
		}
	}
}

func (a *AgentActivity) waitForStartIfNeeded(ctx context.Context) (bool, error) {
	if a == nil || a.hasStarted() || a.Session == nil {
		return false, nil
	}
	if ctx.Value(drainIdleContextKey{}) == true {
		return false, nil
	}
	a.Session.mu.Lock()
	started := a.Session.started && !a.Session.closing && a.Session.activity == a
	a.Session.mu.Unlock()
	if !started {
		return false, nil
	}
	select {
	case <-a.startedCh:
		return true, nil
	case <-a.ctx.Done():
		return false, errAgentActivityClosed
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (a *AgentActivity) hasStarted() bool {
	if a == nil {
		return true
	}
	select {
	case <-a.startedCh:
		return true
	default:
		return false
	}
}

func (a *AgentActivity) activeSpeechHandles() []*SpeechHandle {
	a.queueMu.Lock()
	defer a.queueMu.Unlock()

	active := make([]*SpeechHandle, 0, len(a.speechQueue)+1)
	if a.currentSpeech != nil && !a.currentSpeech.IsDone() {
		active = append(active, a.currentSpeech)
	}
	for _, queued := range a.speechQueue {
		if !queued.speech.IsDone() {
			active = append(active, queued.speech)
		}
	}
	return active
}

func (a *AgentActivity) userTurnUpdated() <-chan struct{} {
	if a == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	a.userTurnMu.Lock()
	defer a.userTurnMu.Unlock()
	return a.userTurnUpdatedCh
}

func (a *AgentActivity) pendingEOUDetection() (<-chan struct{}, bool) {
	if a == nil {
		return nil, false
	}
	a.eouMu.Lock()
	defer a.eouMu.Unlock()
	if a.eouDone == nil {
		return nil, false
	}
	return a.eouDone, true
}

func (a *AgentActivity) pendingUserTurnCompletion() bool {
	if a == nil {
		return false
	}
	a.commitUserTurnMu.Lock()
	defer a.commitUserTurnMu.Unlock()
	return a.commitUserTurnActive > 0
}

func (a *AgentActivity) OnUserTurnExceeded(ev UserTurnExceededEvent) {
	a.queueMu.Lock()
	schedulingPaused := a.schedulingPaused
	a.queueMu.Unlock()
	if schedulingPaused {
		logger.Logger.Warnw("skipping user turn exceeded, speech scheduling is paused", nil, "num_words", ev.AccumulatedWordCount, "duration", ev.Duration)
		return
	}

	a.userTurnExceededMu.Lock()
	if a.userTurnExceededLocked {
		a.userTurnExceededMu.Unlock()
		return
	}
	if a.userTurnExceededCancel != nil {
		a.userTurnExceededCancel()
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.userTurnExceededSeq++
	seq := a.userTurnExceededSeq
	a.userTurnExceededCancel = cancel
	a.userTurnExceededMu.Unlock()

	go func() {
		defer func() {
			a.userTurnExceededMu.Lock()
			if a.userTurnExceededSeq == seq {
				a.userTurnExceededCancel = nil
			}
			a.userTurnExceededMu.Unlock()
		}()

		shouldRun, err := a.waitForUserTurnExceededCallback(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			logger.Logger.Errorw("user turn exceeded wait failed", err)
			return
		}
		if !shouldRun {
			return
		}
		a.queueMu.Lock()
		schedulingPaused = a.schedulingPaused || a.schedulingDraining
		a.queueMu.Unlock()
		if schedulingPaused {
			return
		}
		a.userTurnExceededMu.Lock()
		if a.userTurnExceededSeq != seq || a.userTurnExceededLocked {
			a.userTurnExceededMu.Unlock()
			return
		}
		a.userTurnExceededLocked = true
		a.userTurnExceededCancel = nil
		a.userTurnExceededMu.Unlock()
		defer func() {
			a.userTurnExceededMu.Lock()
			a.userTurnExceededLocked = false
			a.userTurnExceededMu.Unlock()
		}()

		if err := a.AgentIntf.OnUserTurnExceeded(a.ctx, ev); err != nil {
			logger.Logger.Errorw("error in OnUserTurnExceeded callback", err)
		}
	}()
}

func (a *AgentActivity) waitForUserTurnExceededCallback(ctx context.Context) (bool, error) {
	if a.Session == nil {
		return true, nil
	}
	if a.Session.AgentStateValue() == AgentStateSpeaking {
		return false, nil
	}
	if len(a.activeSpeechHandles()) == 0 {
		return true, nil
	}

	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	agentStateEvents := a.sessionAgentStateChangedEvents()
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case ev := <-agentStateEvents:
			if ev.NewState == AgentStateSpeaking {
				return false, nil
			}
		case <-ticker.C:
			if a.Session.AgentStateValue() == AgentStateSpeaking {
				return false, nil
			}
			if len(a.activeSpeechHandles()) == 0 {
				return true, nil
			}
		}
	}
}

func (a *AgentActivity) sessionAgentStateChangedEvents() <-chan AgentStateChangedEvent {
	a.agentStateMu.Lock()
	defer a.agentStateMu.Unlock()

	if a.agentStateEvents == nil && a.Session != nil {
		a.agentStateEvents = a.Session.AgentStateChangedEvents()
	}
	return a.agentStateEvents
}

func (a *AgentActivity) UpdateInstructions(ctx context.Context, instructions string) error {
	a.Agent.Instructions = instructions
	a.Agent.InstructionVariants = nil
	configUpdate := &llm.AgentConfigUpdate{
		Instructions: &instructions,
		CreatedAt:    time.Now(),
	}
	if a.Agent.ChatCtx == nil {
		a.Agent.ChatCtx = llm.NewChatContext()
	}
	a.Agent.ChatCtx.Insert(configUpdate)
	if a.Session != nil {
		if a.Session.ChatCtx == nil {
			a.Session.ChatCtx = llm.NewChatContext()
		}
		a.Session.ChatCtx.Insert(configUpdate)
	}
	if err := updateAgentInstructionsMessage(a.Agent.ChatCtx, llm.NewInstructions(instructions), true); err != nil {
		return err
	}
	if a.Session != nil {
		if updater, ok := a.Session.Assistant.(instructionUpdatingAssistant); ok {
			return updater.UpdateInstructions(ctx, instructions)
		}
	}
	return nil
}

func (a *AgentActivity) UpdateTools(ctx context.Context, tools []llm.Tool) error {
	for idx, tool := range a.Agent.Tools {
		if isNilAgentTool(tool) {
			return fmt.Errorf("existing agent tool at index %d: nil tool", idx)
		}
	}
	oldToolCtx := llm.EmptyToolContext()
	if err := oldToolCtx.UpdateTools(agentToolsAsInterfaces(a.Agent.Tools)); err != nil {
		return err
	}
	dedupedTools, err := dedupeAgentToolsByID(tools)
	if err != nil {
		return err
	}
	newToolCtx := llm.EmptyToolContext()
	if err := newToolCtx.UpdateTools(agentToolsAsInterfaces(dedupedTools)); err != nil {
		return err
	}
	oldToolNames := agentToolNameSet(a.Agent.Tools)
	newToolNames := agentToolNameSet(dedupedTools)
	toolsAdded, toolsRemoved := agentToolDiff(oldToolNames, newToolNames)
	if !oldToolCtx.Equal(newToolCtx) && len(toolsAdded) == 0 && len(toolsRemoved) == 0 {
		replaced := agentToolNamesIntersection(oldToolNames, newToolNames)
		toolsAdded = replaced
		toolsRemoved = replaced
	}

	a.Agent.Tools = dedupedTools
	if len(toolsAdded) > 0 || len(toolsRemoved) > 0 {
		configUpdate := &llm.AgentConfigUpdate{
			ToolsAdded:   toolsAdded,
			ToolsRemoved: toolsRemoved,
			CreatedAt:    time.Now(),
		}
		if a.Agent.ChatCtx == nil {
			a.Agent.ChatCtx = llm.NewChatContext()
		}
		a.Agent.ChatCtx.Insert(configUpdate)
		if a.Session != nil {
			if a.Session.ChatCtx == nil {
				a.Session.ChatCtx = llm.NewChatContext()
			}
			a.Session.ChatCtx.Insert(configUpdate)
		}
	}
	if a.Session != nil {
		if updater, ok := a.Session.Assistant.(toolUpdatingAssistant); ok {
			if err := updater.UpdateTools(ctx); err != nil {
				return err
			}
		}
	}
	return a.UpdateChatContext(ctx, a.Agent.ChatCtx)
}

func (a *AgentActivity) UpdateChatContext(ctx context.Context, chatCtx *llm.ChatContext, excludeInvalidFunctionCalls ...bool) error {
	return a.UpdateChatCtx(ctx, chatCtx, excludeInvalidFunctionCalls...)
}

func (a *AgentActivity) UpdateChatCtx(ctx context.Context, chatCtx *llm.ChatContext, excludeInvalidFunctionCalls ...bool) error {
	excludeInvalid := true
	if len(excludeInvalidFunctionCalls) > 0 {
		excludeInvalid = excludeInvalidFunctionCalls[0]
	}
	if chatCtx == nil {
		a.Agent.ChatCtx = llm.NewChatContext()
		if err := updateAgentInstructionsMessage(a.Agent.ChatCtx, agentInstructionVariants(a.Agent), true); err != nil {
			return err
		}
		return a.updateRealtimeChatContext(ctx)
	}
	if !excludeInvalid {
		a.Agent.ChatCtx = chatCtx.Copy()
		if err := updateAgentInstructionsMessage(a.Agent.ChatCtx, agentInstructionVariants(a.Agent), true); err != nil {
			return err
		}
		return a.updateRealtimeChatContext(ctx)
	}
	tools := a.Tools()
	if a.Session != nil {
		registeredTools, err := sessionRegisteredTools(ctx, a.Session)
		if err != nil {
			return err
		}
		a.registeredTools = append([]llm.Tool(nil), registeredTools...)
		tools = agentToolsAsInterfaces(registeredTools)
	} else if a.Agent != nil {
		for idx, tool := range a.Agent.Tools {
			if isNilAgentTool(tool) {
				return fmt.Errorf("agent tool at index %d: nil tool", idx)
			}
		}
	}
	a.Agent.ChatCtx = chatCtx.Copy(llm.ChatContextCopyOptions{
		Tools: tools,
	})
	if err := updateAgentInstructionsMessage(a.Agent.ChatCtx, agentInstructionVariants(a.Agent), true); err != nil {
		return err
	}
	if err := a.updateRealtimeChatContext(ctx); err != nil {
		return err
	}
	return nil
}

func (a *AgentActivity) UpdateOptions(opts AgentSessionUpdateOptions) error {
	if a == nil || a.Session == nil {
		return nil
	}
	if opts.TurnDetection != nil && *opts.TurnDetection == TurnDetectionModeManual {
		a.cancelPendingEOUDetection()
	}
	updater, ok := a.Session.Assistant.(realtimeOptionsUpdatingAssistant)
	if !ok {
		return nil
	}
	var toolChoice llm.ToolChoice
	if opts.ToolChoice != nil {
		toolChoice = *opts.ToolChoice
	} else {
		toolChoice = a.Session.Options.ToolChoice
	}
	return updater.UpdateOptions(context.Background(), llm.RealtimeSessionOptions{
		ToolChoice:    toolChoice,
		ToolChoiceSet: true,
	})
}

func (a *AgentActivity) cancelPendingEOUDetection() {
	if a == nil {
		return
	}
	a.eouMu.Lock()
	if a.eouCancel != nil {
		a.eouCancel()
		a.eouCancel = nil
	}
	a.eouMu.Unlock()
	a.manualTurnCommitted = false
	a.notifyUserTurnUpdated()
}

func (a *AgentActivity) updateRealtimeChatContext(ctx context.Context) error {
	if a.Session == nil {
		return nil
	}
	updater, ok := a.Session.Assistant.(chatContextUpdatingAssistant)
	if !ok {
		return nil
	}
	chatCtx := a.Agent.ChatContext()
	removeAgentInstructionsMessage(chatCtx)
	return updater.UpdateChatContext(ctx, chatCtx)
}

func (a *AgentActivity) RetrieveChatCtx() *llm.ChatContext {
	if a == nil || a.Agent == nil {
		return llm.NewChatContext().ReadOnly()
	}
	return a.Agent.ChatContext()
}

func agentToolNameSet(tools []llm.Tool) map[string]struct{} {
	names := make(map[string]struct{}, len(tools))
	var addToolNames func([]llm.Tool)
	addToolNames = func(items []llm.Tool) {
		for _, tool := range items {
			if toolset, ok := tool.(llm.Toolset); ok {
				addToolNames(toolset.Tools())
				continue
			}
			if _, providerOnly := tool.(llm.ProviderTool); providerOnly {
				continue
			}
			if name := tool.Name(); name != "" {
				names[name] = struct{}{}
			}
		}
	}
	addToolNames(tools)
	return names
}

func agentToolDiff(oldToolNames map[string]struct{}, newToolNames map[string]struct{}) ([]string, []string) {
	added := make([]string, 0)
	for name := range newToolNames {
		if _, ok := oldToolNames[name]; !ok {
			added = append(added, name)
		}
	}
	removed := make([]string, 0)
	for name := range oldToolNames {
		if _, ok := newToolNames[name]; !ok {
			removed = append(removed, name)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

func agentToolNamesIntersection(left map[string]struct{}, right map[string]struct{}) []string {
	names := make([]string, 0)
	for name := range left {
		if _, ok := right[name]; ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func sortedAgentToolNames(tools []interface{}) []string {
	if len(tools) == 0 {
		return nil
	}
	names := make([]string, 0, len(tools))
	seen := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		t, ok := tool.(llm.Tool)
		if !ok {
			continue
		}
		name := t.Name()
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	return names
}

func sortedAgentFunctionToolNames(tools []interface{}) []string {
	if len(tools) == 0 {
		return nil
	}
	names := make([]string, 0, len(tools))
	seen := make(map[string]struct{}, len(tools))
	var addToolNames func([]interface{})
	addToolNames = func(items []interface{}) {
		for _, item := range items {
			if toolset, ok := item.(llm.Toolset); ok {
				addToolNames(agentToolsAsInterfaces(toolset.Tools()))
				continue
			}
			tool, ok := item.(llm.Tool)
			if !ok {
				continue
			}
			if _, providerOnly := tool.(llm.ProviderTool); providerOnly {
				continue
			}
			name := tool.Name()
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}
	addToolNames(tools)
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	return names
}

func stringPtrIfNotEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func agentInstructionVariants(agent *Agent) *llm.Instructions {
	if agent == nil {
		return nil
	}
	if agent.InstructionVariants != nil {
		return agent.InstructionVariants
	}
	if agent.Instructions == "" {
		return nil
	}
	return llm.NewInstructions(agent.Instructions)
}

func agentInstructionsText(agent *Agent) string {
	instructions := agentInstructionVariants(agent)
	if instructions == nil {
		return ""
	}
	return instructions.String()
}

func updateAgentInstructionsMessage(chatCtx *llm.ChatContext, instructions *llm.Instructions, addIfMissing bool) error {
	if chatCtx == nil {
		return nil
	}
	if instructions == nil && !addIfMissing {
		return nil
	}
	content := llm.ChatContent{}
	if instructions != nil {
		content.Instructions = instructions
	}
	idx := chatCtx.IndexByID(agentInstructionsMessageID)
	if idx != nil {
		existing, ok := chatCtx.Items[*idx].(*llm.ChatMessage)
		if !ok {
			return errors.New("expected the instructions inside the chat_ctx to be of type 'message'")
		}
		createdAt := existing.CreatedAt
		if createdAt.IsZero() {
			createdAt = time.Now()
		}
		chatCtx.Items[*idx] = &llm.ChatMessage{
			ID:        agentInstructionsMessageID,
			Role:      llm.ChatRoleSystem,
			Content:   []llm.ChatContent{content},
			CreatedAt: createdAt,
		}
		return nil
	}
	if addIfMissing {
		msg := &llm.ChatMessage{
			ID:        agentInstructionsMessageID,
			Role:      llm.ChatRoleSystem,
			Content:   []llm.ChatContent{content},
			CreatedAt: time.Now(),
		}
		chatCtx.Items = append([]llm.ChatItem{msg}, chatCtx.Items...)
	}
	return nil
}

func applyAgentInstructionsModality(chatCtx *llm.ChatContext, modality string) {
	if chatCtx == nil || modality == "" {
		return
	}
	idx := chatCtx.IndexByID(agentInstructionsMessageID)
	if idx == nil {
		return
	}
	msg, ok := chatCtx.Items[*idx].(*llm.ChatMessage)
	if !ok {
		return
	}
	content := make([]llm.ChatContent, len(msg.Content))
	changed := false
	for i, part := range msg.Content {
		if part.Instructions != nil {
			part.Instructions = part.Instructions.AsModality(modality)
			changed = true
		}
		content[i] = part
	}
	if !changed {
		return
	}
	createdAt := msg.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	chatCtx.Items[*idx] = &llm.ChatMessage{
		ID:          msg.ID,
		Role:        msg.Role,
		Content:     content,
		Interrupted: msg.Interrupted,
		CreatedAt:   createdAt,
		Extra:       msg.Extra,
		Metrics:     msg.Metrics,
	}
}

func removeAgentInstructionsMessage(chatCtx *llm.ChatContext) {
	if chatCtx == nil {
		return
	}
	for {
		idx := chatCtx.IndexByID(agentInstructionsMessageID)
		if idx == nil {
			return
		}
		chatCtx.Items = append(chatCtx.Items[:*idx], chatCtx.Items[*idx+1:]...)
	}
}

func (a *AgentActivity) ScheduleSpeech(speech *SpeechHandle, priority int, force bool) error {
	a.queueMu.Lock()
	defer a.queueMu.Unlock()

	if (a.schedulingPaused || a.schedulingDraining) && !force {
		_ = speech.Interrupt(true)
		return ErrSpeechSchedulingPaused
	}

	speech.Priority = priority

	a.speechQueue = append(a.speechQueue, scheduledSpeech{
		speech:   speech,
		priority: priority,
		seq:      a.nextSpeechSeq,
	})
	a.nextSpeechSeq++

	// Notify the scheduling loop
	select {
	case a.queueUpdatedCh <- struct{}{}:
	default:
	}

	speech.MarkScheduled()
	return nil
}

func (a *AgentActivity) schedulingTask() {
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-a.queueUpdatedCh:
			a.processQueue()
		}
	}
}

func (a *AgentActivity) processQueue() {
	a.queueMu.Lock()

	if a.currentSpeech != nil && a.currentSpeech.IsDone() {
		a.currentSpeech = nil
	}
	if len(a.speechQueue) == 0 || a.schedulingPaused || a.currentSpeech != nil {
		a.queueMu.Unlock()
		return
	}

	nextIdx := a.nextSpeechIndexLocked()
	speech := a.speechQueue[nextIdx].speech
	a.speechQueue = append(a.speechQueue[:nextIdx], a.speechQueue[nextIdx+1:]...)

	if speech.IsDone() {
		a.queueMu.Unlock()
		return
	}

	a.currentSpeech = speech
	delay := a.MinConsecutiveSpeechDelay()
	if delay > 0 && !a.lastSpeechDone.IsZero() {
		delay -= time.Since(a.lastSpeechDone)
	}
	var assistant scheduledSpeechAssistant
	if a.Session != nil {
		assistant, _ = a.Session.Assistant.(scheduledSpeechAssistant)
	}
	a.queueMu.Unlock()

	speech.AddDoneCallback(a.OnPipelineReplyDone)
	if assistant != nil {
		go func() {
			if delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-timer.C:
				case <-a.ctx.Done():
					timer.Stop()
					return
				case <-speech.doneCh:
					timer.Stop()
					return
				}
			}
			if speech.IsDone() {
				return
			}
			assistant.OnSpeechScheduled(a.ctx, speech)
		}()
	}

	// Run speech completion asynchronously
	go func() {
		// Wait for generation to finish or be interrupted
		<-speech.doneCh

		a.queueMu.Lock()
		if a.currentSpeech == speech {
			a.currentSpeech = nil
		}
		a.lastSpeechDone = time.Now()
		a.queueMu.Unlock()

		// Trigger next
		select {
		case a.queueUpdatedCh <- struct{}{}:
		default:
		}
	}()
}

func (a *AgentActivity) MinConsecutiveSpeechDelay() time.Duration {
	if a.Agent != nil && a.Agent.MinConsecutiveSpeechDelay > 0 {
		return time.Duration(a.Agent.MinConsecutiveSpeechDelay * float64(time.Second))
	}
	if a.Session != nil && a.Session.Options.MinConsecutiveSpeechDelay > 0 {
		return time.Duration(a.Session.Options.MinConsecutiveSpeechDelay * float64(time.Second))
	}
	return 0
}

func (a *AgentActivity) UseTTSAlignedTranscript() bool {
	if a == nil {
		return false
	}
	enabled := false
	if a.Session != nil {
		enabled = a.Session.Options.UseTTSAlignedTranscript
	}
	if a.Agent != nil && (a.Agent.UseTTSAlignedTranscriptSet || a.Agent.UseTTSAlignedTranscript) {
		enabled = a.Agent.UseTTSAlignedTranscript
	}
	return enabled
}

func (a *AgentActivity) OnPipelineReplyDone(speech *SpeechHandle) {
	if a == nil || a.Session == nil {
		return
	}
	a.queueMu.Lock()
	if speech != nil && a.currentSpeech == speech && speech.IsDone() {
		a.currentSpeech = nil
	}
	inactive := len(a.speechQueue) == 0 && (a.currentSpeech == nil || a.currentSpeech.IsDone())
	started := a.schedulingStarted
	a.queueMu.Unlock()
	a.resumeFinishedPausedSpeech(speech)
	if inactive {
		a.Session.UpdateAgentState(AgentStateListening)
	}
	if started {
		go a.processQueue()
	}
}

func (a *AgentActivity) OnInputSpeechStarted() {
	if a == nil || a.Session == nil {
		return
	}
	if !a.hasVADModel() {
		a.Session.UpdateUserState(UserStateSpeaking)
	}
	go func() {
		if err := a.Interrupt(false); err != nil {
			logger.Logger.Errorw("realtime input speech started but current speech is not interruptable", err)
		}
	}()
}

func (a *AgentActivity) OnInputSpeechStopped(ev llm.InputSpeechStoppedEvent) {
	if a == nil || a.Session == nil {
		return
	}
	if !a.hasVADModel() {
		a.Session.UpdateUserState(UserStateListening)
	}
	if ev.UserTranscriptionEnabled {
		a.Session.EmitUserInputTranscribed(UserInputTranscribedEvent{
			Transcript: "",
			IsFinal:    false,
		})
	}
}

func (a *AgentActivity) OnInputAudioTranscriptionCompleted(ev llm.InputTranscriptionCompleted) {
	if a == nil || a.Session == nil {
		return
	}
	a.Session.EmitUserInputTranscribed(UserInputTranscribedEvent{
		Transcript: ev.Transcript,
		IsFinal:    ev.IsFinal,
	})
	if !ev.IsFinal {
		return
	}

	msg := &llm.ChatMessage{
		ID:   ev.ItemID,
		Role: llm.ChatRoleUser,
		Content: []llm.ChatContent{
			{Text: ev.Transcript},
		},
		CreatedAt: time.Now(),
	}
	if a.Agent != nil && a.Agent.ChatCtx != nil {
		_ = a.Agent.ChatCtx.UpsertItem(msg, llm.ChatContextUpsertOptions{AllowTypeMismatch: true})
	}
	a.Session.EmitConversationItemAdded(msg)
}

func (a *AgentActivity) OnRemoteItemAdded(ev llm.RemoteItemAddedEvent) {
	if a == nil || a.Agent == nil || a.Agent.ChatCtx == nil || ev.Item == nil {
		return
	}
	item := ev.Item
	if item.GetID() != "" && a.Agent.ChatCtx.GetByID(item.GetID()) != nil {
		return
	}
	lastItemID := ""
	if len(a.Agent.ChatCtx.Items) > 0 {
		lastItemID = a.Agent.ChatCtx.Items[len(a.Agent.ChatCtx.Items)-1].GetID()
	}
	if ev.PreviousItemID == "" || ev.PreviousItemID == lastItemID {
		a.Agent.ChatCtx.Items = append(a.Agent.ChatCtx.Items, item)
	}
}

func (a *AgentActivity) OnMetricsCollected(metrics telemetry.AgentMetrics) {
	if a == nil || a.Session == nil {
		return
	}
	a.queueMu.Lock()
	currentSpeech := a.currentSpeech
	a.queueMu.Unlock()
	if currentSpeech != nil {
		switch m := metrics.(type) {
		case *telemetry.LLMMetrics:
			m.SpeechID = currentSpeech.ID
		case *telemetry.TTSMetrics:
			m.SpeechID = currentSpeech.ID
		}
	}
	a.Session.EmitMetricsCollected(metrics)
}

func (a *AgentActivity) OnError(err error, source any) {
	if a == nil || a.Session == nil || err == nil {
		return
	}
	if errors.Is(err, context.Canceled) {
		return
	}
	a.Session.EmitError(ErrorEvent{Error: err, Source: source})
}

func (a *AgentActivity) OnGenerationCreated(ev llm.GenerationCreatedEvent, configure ...func(*SpeechHandle)) (*SpeechHandle, error) {
	if a == nil || a.Session == nil || ev.UserInitiated {
		return nil, nil
	}

	a.queueMu.Lock()
	schedulingBlocked := a.schedulingPaused || a.schedulingDraining
	a.queueMu.Unlock()
	if schedulingBlocked {
		return nil, ErrSpeechSchedulingPaused
	}

	handle := NewSpeechHandle(a.AllowInterruptions(), DefaultInputDetails())
	handle.Generation.RealtimeGeneration = &ev
	a.Session.EmitSpeechCreated(SpeechCreatedEvent{
		UserInitiated: false,
		Source:        "generate_reply",
		SpeechHandle:  handle,
	})
	for _, configureHandle := range configure {
		if configureHandle != nil {
			configureHandle(handle)
		}
	}
	if err := a.ScheduleSpeech(handle, SpeechPriorityNormal, false); err != nil {
		return handle, err
	}
	return handle, nil
}

func (a *AgentActivity) Drain(ctx context.Context) error {
	if ctx == nil {
		ctx = a.ctx
	}

	a.queueMu.Lock()
	if a.schedulingPaused {
		a.queueMu.Unlock()
		return nil
	}
	a.schedulingDraining = true
	a.queueMu.Unlock()

	select {
	case a.queueUpdatedCh <- struct{}{}:
	default:
	}

	err := a.WaitForInactive(context.WithValue(ctx, drainIdleContextKey{}, true))

	a.queueMu.Lock()
	a.schedulingDraining = false
	a.queueMu.Unlock()
	a.PauseScheduling()

	return err
}

func (a *AgentActivity) nextSpeechIndexLocked() int {
	best := 0
	for i := 1; i < len(a.speechQueue); i++ {
		current := a.speechQueue[i]
		candidate := a.speechQueue[best]
		if current.priority > candidate.priority || (current.priority == candidate.priority && current.seq < candidate.seq) {
			best = i
		}
	}
	return best
}

// Event callbacks from the active audio pipeline.
func (a *AgentActivity) OnStartOfSpeech(ev *vad.VADEvent) {
	a.onStartOfSpeech(ev, nil)
}

func (a *AgentActivity) OnSTTStartOfSpeech(ev *stt.SpeechEvent) {
	var startedAt *float64
	if ev != nil {
		startedAt = ev.SpeechStartTime
	}
	a.onStartOfSpeech(nil, startedAt)
}

func (a *AgentActivity) onStartOfSpeech(ev *vad.VADEvent, sttStartedAt *float64) {
	wasSpeaking := a.setSpeaking(true)
	a.sttEOSReceived = false
	a.manualTurnCommitted = false
	a.interruptionDetected = false
	a.overlapSpeechEnded = false
	if !wasSpeaking {
		if sttStartedAt != nil {
			a.userSpeechStartedAt = unixSecondsToTime(*sttStartedAt)
		} else if ev != nil {
			a.userSpeechStartedAt = vadSpeechStartedAt(ev)
		} else {
			a.userSpeechStartedAt = time.Now()
		}
		a.clearUserAudioFrames()
	}
	a.userSpeechStoppedAt = time.Time{}
	a.clearHeldUserTranscriptWindow()
	if a.Session != nil {
		a.Session.updateUserStateAt(UserStateSpeaking, a.userSpeechStartedAt)
	}
	a.notifyUserTurnUpdated()
	if endpointing := a.endpointing(); endpointing != nil {
		startedAt := vadSpeechStartTimestamp(ev)
		if sttStartedAt != nil {
			startedAt = *sttStartedAt
		}
		overlapping := a.Session != nil && a.Session.AgentStateValue() == AgentStateSpeaking
		endpointing.OnStartOfSpeech(startedAt, overlapping)
	}
	logger.Logger.Infow("Start of speech detected")

	// Cancel pending EOU detection
	a.cancelPendingEOUDetection()

	a.cancelFalseInterruptionTimer()
	if a.pauseThinkingSpeechForFalseInterruption() {
		return
	}
}

func (a *AgentActivity) OnEndOfSpeech(ev *vad.VADEvent) {
	wasSpeaking := a.setSpeaking(false)
	a.userSpeechStoppedAt = vadSpeechStoppedAt(ev)
	if ev == nil {
		a.sttEOSReceived = true
	}
	if a.Session != nil {
		a.Session.updateUserStateAt(UserStateListening, a.userSpeechStoppedAt)
	}
	a.notifyUserTurnUpdated()
	if endpointing := a.endpointing(); endpointing != nil && wasSpeaking {
		shouldIgnore := a.overlapSpeechEnded && !a.interruptionDetected
		endpointing.OnEndOfSpeech(vadSpeechEndTimestamp(ev), shouldIgnore)
	}
	a.overlapSpeechEnded = false
	logger.Logger.Infow("End of speech detected")

	turnDetection := a.turnDetectionMode()
	if a.vadBasedTurnDetection() || (turnDetection == TurnDetectionModeSTT && a.pendingFinalTranscriptPresent()) {
		// Trigger EOU detection
		a.runEOUDetection(a.pendingFinalEndOfTurnInfo())
	}
	a.startFalseInterruptionTimer()
}

func (a *AgentActivity) OnOverlapSpeechEnded(ev OverlappingSpeechEvent) {
	if a == nil || a.Session == nil {
		return
	}
	if !ev.IsInterruption && a.backchannelBoundaryActive(time.Now()) {
		return
	}
	a.overlapSpeechEnded = true
	a.interruptionDetected = ev.IsInterruption
	a.Session.EmitOverlappingSpeech(ev)
}

func (a *AgentActivity) OnInterruption(ev OverlappingSpeechEvent) {
	if a == nil {
		return
	}
	a.overlapSpeechEnded = true
	a.interruptionDetected = true
	a.restoreAudioActivityInterruption()
	ignoreUntil := overlappingSpeechIgnoreUntil(ev)
	a.interruptByAudioActivity("overlapping speech", "detected_at", ev.DetectedAt, ignoreUntil)
	a.onAgentSpeechEnded(ignoreUntil)
	a.flushHeldSTTEvents()
}

func (a *AgentActivity) OnVADInferenceDone(ev *vad.VADEvent) {
	a.updateVADInferenceTiming(ev)
	turnDetection := a.turnDetectionMode()
	if turnDetection == TurnDetectionModeManual || turnDetection == TurnDetectionModeRealtimeLLM {
		return
	}
	if ev == nil || ev.SpeechDuration < a.minInterruptionDuration() {
		return
	}
	if turnDetection == TurnDetectionModeSTT && a.sttEOSReceived && ev.RawAccumulatedSilence > 0 {
		return
	}
	if a.shortInterruptionTranscript(a.currentInterruptionTranscript()) {
		return
	}
	a.interruptByAudioActivity("VAD inference", "speech_duration", ev.SpeechDuration, time.Time{})
}

func (a *AgentActivity) updateVADInferenceTiming(ev *vad.VADEvent) {
	if a == nil || ev == nil || ev.RawAccumulatedSpeech <= 0 {
		return
	}
	now := time.Now()
	a.userSpeechStoppedAt = now
	if a.userSpeechStartedAt.IsZero() {
		delay := time.Duration(ev.RawAccumulatedSpeech * float64(time.Second))
		a.userSpeechStartedAt = now.Add(-delay)
	}
}

func (a *AgentActivity) OnInterimTranscript(ev *stt.SpeechEvent) {
	if a.Session == nil {
		return
	}
	if a.realtimeUserTranscriptionEnabled() {
		return
	}
	evType := stt.SpeechEventInterimTranscript
	if ev != nil {
		evType = ev.Type
	}
	if a.shouldIgnoreManualCommittedSTT(evType) {
		return
	}
	if a.holdSTTEventWhileAgentSpeaking(ev) {
		return
	}
	transcript := ""
	language := ""
	speakerID := ""
	confidence := 0.0
	if ev != nil && len(ev.Alternatives) > 0 {
		transcript = ev.Alternatives[0].Text
		language = ev.Alternatives[0].Language
		speakerID = ev.Alternatives[0].SpeakerID
		confidence = ev.Alternatives[0].Confidence
	}
	if a.shouldDropInterimTranscriptBeforeAgentSpeechEnd(ev) {
		logger.Logger.Debugw("dropping stale interim transcript before agent speech end", "transcript", transcript)
		return
	}
	a.userTurnMu.Lock()
	preflightTranscript := ""
	preflightConfidence := 0.0
	if ev != nil && ev.Type == stt.SpeechEventPreflightTranscript && transcript != "" {
		language = referenceTranscriptLanguage(a.lastUserLanguage, language, transcript)
		a.lastUserLanguage = language
		preflightTranscript = strings.TrimSpace(strings.Join([]string{a.pendingUserTranscript, transcript}, " "))
		preflightConfidence = confidence
		if a.pendingTranscriptConfidenceCount > 0 {
			preflightConfidence = (a.pendingTranscriptConfidenceSum + confidence) / float64(a.pendingTranscriptConfidenceCount+1)
		}
		a.lastFinalTranscriptTime = time.Now()
	}
	a.pendingInterimTranscript = transcript
	a.pendingInterimLanguage = language
	a.pendingInterimSpeakerID = speakerID
	a.pendingPreflightTranscript = preflightTranscript
	a.pendingPreflightConfidence = preflightConfidence
	a.userTurnMu.Unlock()

	a.Session.EmitUserInputTranscribed(UserInputTranscribedEvent{
		Language:   language,
		Transcript: transcript,
		IsFinal:    false,
		SpeakerID:  speakerID,
	})
	turnDetection := a.turnDetectionMode()
	if transcript != "" && turnDetection != TurnDetectionModeManual && turnDetection != TurnDetectionModeRealtimeLLM {
		if !a.shortInterruptionTranscript(transcript) {
			a.interruptByAudioActivity("interim transcript", "transcript", transcript, time.Time{})
			if !a.isUserSpeaking() {
				a.startFalseInterruptionTimer()
			}
		}
	}
	if preflightTranscript != "" {
		a.maybeStartPreemptiveGeneration(preflightTranscript, preflightConfidence)
	}
}

func (a *AgentActivity) OnFinalTranscript(ev *stt.SpeechEvent) {
	if a.realtimeUserTranscriptionEnabled() {
		return
	}
	if a.shouldIgnoreManualCommittedSTT(stt.SpeechEventFinalTranscript) {
		return
	}
	if a.holdSTTEventWhileAgentSpeaking(ev) {
		return
	}
	transcript := ""
	confidence := 0.0
	language := ""
	speakerID := ""
	if len(ev.Alternatives) > 0 {
		transcript = ev.Alternatives[0].Text
		confidence = ev.Alternatives[0].Confidence
		language = ev.Alternatives[0].Language
		speakerID = ev.Alternatives[0].SpeakerID
	}
	if transcript == "" {
		return
	}
	if a.shouldDropFinalTranscriptBeforeAgentSpeechEnd(ev) {
		logger.Logger.Debugw("dropping stale final transcript before agent speech end", "transcript", transcript)
		return
	}
	if a.Session != nil {
		a.Session.EmitUserInputTranscribed(UserInputTranscribedEvent{
			Language:   language,
			Transcript: transcript,
			IsFinal:    true,
			SpeakerID:  speakerID,
		})
	}
	if rejectsZeroConfidenceTranscript(transcript, confidence) {
		logger.Logger.Warnw("skipping zero-confidence final transcript", nil, "transcript", transcript)
		return
	}

	startedSpeakingAt, stoppedSpeakingAt, transcriptionDelay := a.finalTranscriptTiming(ev)

	a.userTurnMu.Lock()
	pendingTranscript := strings.TrimSpace(strings.Join([]string{a.pendingUserTranscript, transcript}, " "))
	matchesPreflightTranscript := a.pendingPreflightTranscript != "" && pendingTranscript == a.pendingPreflightTranscript
	confidenceSum := a.pendingTranscriptConfidenceSum + confidence
	confidenceCount := a.pendingTranscriptConfidenceCount + 1
	language = referenceTranscriptLanguage(a.lastUserLanguage, language, transcript)
	a.pendingUserTranscript = pendingTranscript
	a.pendingUserLanguage = language
	a.lastUserLanguage = language
	a.pendingTranscriptConfidenceSum = confidenceSum
	a.pendingTranscriptConfidenceCount = confidenceCount
	a.pendingTranscriptConfidence = confidenceSum / float64(confidenceCount)
	a.pendingUserTranscriptPresent = true
	a.lastFinalTranscriptTime = time.Now()
	a.pendingStartedSpeakingAt = startedSpeakingAt
	a.pendingStoppedSpeakingAt = stoppedSpeakingAt
	a.pendingTranscriptionDelay = transcriptionDelay
	a.pendingInterimTranscript = ""
	a.pendingInterimLanguage = ""
	a.pendingInterimSpeakerID = ""
	a.pendingPreflightTranscript = ""
	a.pendingPreflightConfidence = 0
	a.userTurnMu.Unlock()
	a.notifyUserTurnUpdated()
	a.checkUserTurnLimit(transcript)

	turnDetection := a.turnDetectionMode()
	if turnDetection != TurnDetectionModeManual && turnDetection != TurnDetectionModeRealtimeLLM {
		if !a.shortInterruptionTranscript(transcript) {
			a.interruptByAudioActivity("final transcript", "transcript", transcript, time.Time{})
			if !a.isUserSpeaking() {
				a.startFalseInterruptionTimer()
			}
		}
	}
	if !matchesPreflightTranscript {
		a.maybeStartPreemptiveGeneration(pendingTranscript, confidenceSum/float64(confidenceCount))
	}
	if a.vadBasedTurnDetection() && !a.isUserSpeaking() {
		a.runEOUDetection(EndOfTurnInfo{
			NewTranscript:        transcript,
			Language:             language,
			TranscriptConfidence: confidence,
			TranscriptionDelay:   transcriptionDelay,
			StartedSpeakingAt:    startedSpeakingAt,
			StoppedSpeakingAt:    stoppedSpeakingAt,
			AudioFrames:          a.userAudioSnapshot(),
		})
	}
}

func (a *AgentActivity) holdUserTranscriptsUntil(ignoreUntil time.Time) {
	if a == nil || ignoreUntil.IsZero() {
		return
	}
	if cooldown := a.heldTranscriptEndCooldown(); cooldown > 0 {
		ignoreUntil = ignoreUntil.Add(-cooldown)
	}
	a.userTurnMu.Lock()
	if a.ignoreUserTranscriptUntil.IsZero() || ignoreUntil.Before(a.ignoreUserTranscriptUntil) {
		a.ignoreUserTranscriptUntil = ignoreUntil
	}
	a.userTurnMu.Unlock()
}

func (a *AgentActivity) heldTranscriptEndCooldown() time.Duration {
	if a == nil || a.Session == nil {
		return 0
	}
	return time.Duration(a.Session.Options.BackchannelBoundaryEnd * float64(time.Second))
}

func (a *AgentActivity) armBackchannelBoundary(startedAt time.Time) {
	if a == nil || a.Session == nil {
		return
	}
	a.resetUserTurnLimitTracker()
	duration := time.Duration(a.Session.Options.BackchannelBoundaryStart * float64(time.Second))
	a.backchannelBoundaryMu.Lock()
	a.audioActivityDisabled = false
	if duration > 0 {
		a.backchannelBoundaryUntil = startedAt.Add(duration)
	} else {
		a.backchannelBoundaryUntil = time.Time{}
		a.audioActivityDisabled = true
	}
	a.backchannelBoundaryMu.Unlock()
	a.userTurnMu.Lock()
	a.holdSTTWhileAgentSpeaking = a.InterruptionEnabled()
	a.userTurnMu.Unlock()
}

func (a *AgentActivity) cancelBackchannelBoundary() {
	if a == nil {
		return
	}
	a.backchannelBoundaryMu.Lock()
	a.backchannelBoundaryUntil = time.Time{}
	a.audioActivityDisabled = false
	a.backchannelBoundaryMu.Unlock()
}

func (a *AgentActivity) onAgentSpeechEnded(endedAt time.Time) {
	if a == nil {
		return
	}
	a.cancelBackchannelBoundary()
	if a.InterruptionEnabled() {
		a.holdUserTranscriptsUntil(endedAt)
	}
	a.userTurnMu.Lock()
	a.holdSTTWhileAgentSpeaking = false
	a.userTurnMu.Unlock()
}

func (a *AgentActivity) audioActivityInterruptionDisabled(now time.Time) bool {
	if a == nil || a.Session == nil {
		return false
	}
	a.backchannelBoundaryMu.Lock()
	if a.audioActivityDisabled {
		a.backchannelBoundaryMu.Unlock()
		return a.Session.AgentState() == AgentStateSpeaking
	}
	until := a.backchannelBoundaryUntil
	if until.IsZero() {
		a.backchannelBoundaryMu.Unlock()
		return false
	}
	if now.Before(until) {
		a.backchannelBoundaryMu.Unlock()
		return false
	}
	a.backchannelBoundaryUntil = time.Time{}
	a.backchannelBoundaryMu.Unlock()

	if a.Session.AgentState() != AgentStateSpeaking {
		return false
	}
	a.backchannelBoundaryMu.Lock()
	a.audioActivityDisabled = true
	a.backchannelBoundaryMu.Unlock()
	return true
}

func (a *AgentActivity) restoreAudioActivityInterruption() {
	if a == nil {
		return
	}
	a.backchannelBoundaryMu.Lock()
	a.backchannelBoundaryUntil = time.Time{}
	a.audioActivityDisabled = false
	a.backchannelBoundaryMu.Unlock()
}

func (a *AgentActivity) backchannelBoundaryActive(now time.Time) bool {
	if a == nil {
		return false
	}
	a.backchannelBoundaryMu.Lock()
	until := a.backchannelBoundaryUntil
	if until.IsZero() || !now.Before(until) {
		a.backchannelBoundaryUntil = time.Time{}
		a.backchannelBoundaryMu.Unlock()
		return false
	}
	a.backchannelBoundaryMu.Unlock()
	return true
}

func overlappingSpeechIgnoreUntil(ev OverlappingSpeechEvent) time.Time {
	if ev.OverlapStartedAt != nil && !ev.OverlapStartedAt.IsZero() {
		return *ev.OverlapStartedAt
	}
	return ev.DetectedAt
}

func (a *AgentActivity) clearHeldUserTranscriptWindow() {
	if a == nil {
		return
	}
	a.userTurnMu.Lock()
	a.ignoreUserTranscriptUntil = time.Time{}
	a.heldSTTEvents = nil
	a.holdSTTWhileAgentSpeaking = false
	a.userTurnMu.Unlock()
}

func (a *AgentActivity) holdSTTEventWhileAgentSpeaking(ev *stt.SpeechEvent) bool {
	if a == nil || a.Session == nil || ev == nil || a.Session.AgentStateValue() != AgentStateSpeaking {
		return false
	}
	switch ev.Type {
	case "", stt.SpeechEventStartOfSpeech, stt.SpeechEventInterimTranscript, stt.SpeechEventPreflightTranscript, stt.SpeechEventFinalTranscript, stt.SpeechEventEndOfSpeech:
	default:
		return false
	}
	a.userTurnMu.Lock()
	if !a.holdSTTWhileAgentSpeaking {
		a.userTurnMu.Unlock()
		return false
	}
	a.heldSTTEvents = append(a.heldSTTEvents, cloneSpeechEvent(ev))
	a.userTurnMu.Unlock()
	return true
}

func (a *AgentActivity) flushHeldSTTEvents() {
	if a == nil {
		return
	}
	a.userTurnMu.Lock()
	events := a.heldSTTEvents
	a.heldSTTEvents = nil
	a.userTurnMu.Unlock()
	for _, ev := range events {
		if ev == nil {
			continue
		}
		switch ev.Type {
		case stt.SpeechEventStartOfSpeech:
			a.OnSTTStartOfSpeech(ev)
		case stt.SpeechEventInterimTranscript, stt.SpeechEventPreflightTranscript:
			a.OnInterimTranscript(ev)
		case stt.SpeechEventEndOfSpeech:
			a.OnEndOfSpeech(nil)
		default:
			a.OnFinalTranscript(ev)
		}
	}
}

func cloneSpeechEvent(ev *stt.SpeechEvent) *stt.SpeechEvent {
	if ev == nil {
		return nil
	}
	clone := *ev
	if ev.Alternatives != nil {
		clone.Alternatives = append([]stt.SpeechData(nil), ev.Alternatives...)
	}
	return &clone
}

func (a *AgentActivity) shouldDropFinalTranscriptBeforeAgentSpeechEnd(ev *stt.SpeechEvent) bool {
	if a == nil || ev == nil || len(ev.Alternatives) == 0 || a.userSpeechStartedAt.IsZero() {
		return false
	}
	alternative := ev.Alternatives[0]
	if alternative.EndTime <= 0 || alternative.StartTime == alternative.EndTime {
		return false
	}
	a.userTurnMu.Lock()
	ignoreUntil := a.ignoreUserTranscriptUntil
	if ignoreUntil.IsZero() {
		a.userTurnMu.Unlock()
		return false
	}
	transcriptEnd := a.userSpeechStartedAt.Add(time.Duration(alternative.EndTime * float64(time.Second)))
	if transcriptEnd.Before(ignoreUntil) {
		a.userTurnMu.Unlock()
		return true
	}
	a.ignoreUserTranscriptUntil = time.Time{}
	a.userTurnMu.Unlock()
	return false
}

func (a *AgentActivity) shouldDropInterimTranscriptBeforeAgentSpeechEnd(ev *stt.SpeechEvent) bool {
	if a == nil || ev == nil || len(ev.Alternatives) == 0 || a.userSpeechStartedAt.IsZero() {
		return false
	}
	alternative := ev.Alternatives[0]
	if alternative.StartTime <= 0 || alternative.StartTime == alternative.EndTime {
		return false
	}
	a.userTurnMu.Lock()
	ignoreUntil := a.ignoreUserTranscriptUntil
	if ignoreUntil.IsZero() {
		a.userTurnMu.Unlock()
		return false
	}
	transcriptStart := a.userSpeechStartedAt.Add(time.Duration(alternative.StartTime * float64(time.Second)))
	if transcriptStart.Before(ignoreUntil) {
		a.userTurnMu.Unlock()
		return true
	}
	a.ignoreUserTranscriptUntil = time.Time{}
	a.userTurnMu.Unlock()
	return false
}

func (a *AgentActivity) finalTranscriptTiming(ev *stt.SpeechEvent) (*float64, *float64, float64) {
	if a == nil || ev == nil || a.userSpeechStartedAt.IsZero() || len(ev.Alternatives) == 0 {
		return nil, nil, 0
	}
	started := timeToUnixSeconds(a.userSpeechStartedAt)
	if ev.Alternatives[0].EndTime <= 0 {
		return &started, nil, 0
	}
	stopped := started + ev.Alternatives[0].EndTime
	transcriptionDelay := timeToUnixSeconds(time.Now()) - stopped
	if transcriptionDelay < 0 {
		transcriptionDelay = 0
	}
	return &started, &stopped, transcriptionDelay
}

func (a *AgentActivity) RecordUserAudioFrame(frame *model.AudioFrame) {
	if a == nil || frame == nil {
		return
	}
	copyFrame := copyAudioFrame(frame)
	a.userAudioMu.Lock()
	a.userAudioFrames = append(a.userAudioFrames, copyFrame)
	a.trimUserAudioFramesLocked()
	a.userAudioMu.Unlock()
}

func (a *AgentActivity) clearUserAudioFrames() {
	a.userAudioMu.Lock()
	a.userAudioFrames = nil
	a.userAudioMu.Unlock()
}

func (a *AgentActivity) userAudioSnapshot() []*model.AudioFrame {
	a.userAudioMu.Lock()
	defer a.userAudioMu.Unlock()
	frames := make([]*model.AudioFrame, len(a.userAudioFrames))
	for i, frame := range a.userAudioFrames {
		frames[i] = copyAudioFrame(frame)
	}
	return frames
}

func (a *AgentActivity) trimUserAudioFramesLocked() {
	total := 0.0
	start := len(a.userAudioFrames)
	for start > 0 {
		frame := a.userAudioFrames[start-1]
		total += audioFrameDurationSeconds(frame)
		start--
		if total >= audioTurnDetectorWindowSeconds {
			break
		}
	}
	if start > 0 {
		a.userAudioFrames = append([]*model.AudioFrame(nil), a.userAudioFrames[start:]...)
	}
}

func copyAudioFrame(frame *model.AudioFrame) *model.AudioFrame {
	if frame == nil {
		return nil
	}
	copyFrame := *frame
	copyFrame.Data = append([]byte(nil), frame.Data...)
	return &copyFrame
}

func audioFrameDurationSeconds(frame *model.AudioFrame) float64 {
	if frame == nil || frame.SampleRate == 0 {
		return 0
	}
	return float64(frame.SamplesPerChannel) / float64(frame.SampleRate)
}

func (a *AgentActivity) realtimeUserTranscriptionEnabled() bool {
	if a == nil || a.Session == nil {
		return false
	}
	a.Session.mu.Lock()
	assistant := a.Session.Assistant
	a.Session.mu.Unlock()
	capabilities, ok := assistant.(realtimeCapabilitiesAssistant)
	return ok && capabilities.RealtimeCapabilities().UserTranscription
}

func (a *AgentActivity) interruptByAudioActivity(reason string, key string, value any, ignoreUserTranscriptUntil time.Time) {
	if a == nil {
		return
	}
	if a.Session != nil && a.Session.aecWarmupActive() {
		return
	}
	if a.audioActivityInterruptionDisabled(time.Now()) {
		return
	}
	if a.pauseSpeechForFalseInterruption(ignoreUserTranscriptUntil) {
		return
	}
	if _, err := a.interruptHandles(false, false); err != nil {
		logger.Logger.Warnw("failed to interrupt speech for "+reason, err, key, value)
	}
}

func (a *AgentActivity) pauseSpeechForFalseInterruption(ignoreUserTranscriptUntil time.Time) bool {
	if a == nil || a.Session == nil {
		return false
	}
	opts := a.Session.Options
	if !a.pauseCurrentSpeechForFalseInterruption(time.Duration(opts.FalseInterruptionTimeout*float64(time.Second)), true, false) {
		return false
	}
	if ignoreUserTranscriptUntil.IsZero() {
		ignoreUserTranscriptUntil = time.Now()
	}
	a.holdUserTranscriptsUntil(ignoreUserTranscriptUntil)
	return true
}

func (a *AgentActivity) pauseThinkingSpeechForFalseInterruption() bool {
	if a == nil || a.Session == nil || a.Session.AgentState() == AgentStateSpeaking {
		return false
	}
	return a.pauseCurrentSpeechForFalseInterruption(0, false, true)
}

func (a *AgentActivity) pauseCurrentSpeechForFalseInterruption(timeout time.Duration, updateAgentState bool, skipIfPaused bool) bool {
	if a == nil || a.Session == nil {
		return false
	}
	controller := a.Session.AudioOutputController()
	if controller == nil || !controller.CanPauseAudioOutput() {
		return false
	}
	opts := a.Session.Options
	if !opts.ResumeFalseInterruption || opts.FalseInterruptionTimeout < 0 {
		return false
	}
	a.queueMu.Lock()
	current := a.currentSpeech
	if current == nil || current.IsInterrupted() || current.IsDone() || !current.AllowInterruptions {
		a.queueMu.Unlock()
		return false
	}
	a.queueMu.Unlock()

	a.falseInterruptionMu.Lock()
	if skipIfPaused && a.pausedSpeech != nil && a.pausedSpeech.handle == current {
		a.falseInterruptionMu.Unlock()
		return false
	}
	if a.falseInterruptionTimer != nil {
		a.falseInterruptionTimer.Stop()
		a.falseInterruptionTimer = nil
	}
	if a.pausedSpeech != nil && a.pausedSpeech.handle == current {
		a.pausedSpeech.timeout = timeout
	} else {
		a.pausedSpeech = &pausedSpeechInfo{
			handle:     current,
			agentState: a.Session.AgentState(),
			timeout:    timeout,
		}
	}
	a.falseInterruptionMu.Unlock()

	controller.PauseAudioOutput()
	if updateAgentState {
		a.Session.UpdateAgentState(AgentStateListening)
	}
	return true
}

func (a *AgentActivity) startFalseInterruptionTimer() {
	if a == nil {
		return
	}
	a.falseInterruptionMu.Lock()
	paused := a.pausedSpeech
	if paused == nil {
		a.falseInterruptionMu.Unlock()
		return
	}
	if a.falseInterruptionTimer != nil {
		a.falseInterruptionTimer.Stop()
	}
	timeout := paused.timeout
	a.falseInterruptionTimer = time.AfterFunc(timeout, a.resumeFalseInterruption)
	a.falseInterruptionMu.Unlock()
}

func (a *AgentActivity) cancelFalseInterruptionTimer() {
	if a == nil {
		return
	}
	a.falseInterruptionMu.Lock()
	if a.falseInterruptionTimer != nil {
		a.falseInterruptionTimer.Stop()
		a.falseInterruptionTimer = nil
	}
	a.falseInterruptionMu.Unlock()
}

func (a *AgentActivity) resumeFalseInterruption() {
	if a == nil || a.Session == nil {
		return
	}
	a.falseInterruptionMu.Lock()
	paused := a.pausedSpeech
	a.pausedSpeech = nil
	a.falseInterruptionTimer = nil
	a.falseInterruptionMu.Unlock()
	if paused == nil {
		return
	}

	resumed := false
	controller := a.Session.AudioOutputController()
	a.queueMu.Lock()
	current := a.currentSpeech
	a.queueMu.Unlock()
	if current == paused.handle && !paused.handle.IsDone() && controller != nil && controller.CanPauseAudioOutput() && a.Session.Options.ResumeFalseInterruption {
		a.Session.UpdateAgentState(paused.agentState)
		controller.ResumeAudioOutput()
		resumed = true
	}
	a.Session.EmitAgentFalseInterruption(AgentFalseInterruptionEvent{Resumed: resumed})
}

func (a *AgentActivity) cancelPausedFalseInterruption(interrupt bool) *pausedSpeechInfo {
	if a == nil {
		return nil
	}
	a.falseInterruptionMu.Lock()
	paused := a.pausedSpeech
	a.pausedSpeech = nil
	if a.falseInterruptionTimer != nil {
		a.falseInterruptionTimer.Stop()
		a.falseInterruptionTimer = nil
	}
	a.falseInterruptionMu.Unlock()
	if paused == nil {
		return nil
	}
	if interrupt && !paused.handle.IsInterrupted() && paused.handle.AllowInterruptions {
		_ = paused.handle.Interrupt(false)
	}
	return paused
}

func (a *AgentActivity) resumeCanceledFalseInterruption(paused *pausedSpeechInfo) {
	if a == nil || a.Session == nil || paused == nil {
		return
	}
	controller := a.Session.AudioOutputController()
	if controller != nil && a.Session.Options.ResumeFalseInterruption {
		controller.ResumeAudioOutput()
	}
}

func (a *AgentActivity) resumeFinishedPausedSpeech(speech *SpeechHandle) {
	if a == nil || a.Session == nil || speech == nil {
		return
	}
	a.falseInterruptionMu.Lock()
	paused := a.pausedSpeech
	if paused == nil || paused.handle != speech {
		a.falseInterruptionMu.Unlock()
		return
	}
	a.pausedSpeech = nil
	if a.falseInterruptionTimer != nil {
		a.falseInterruptionTimer.Stop()
		a.falseInterruptionTimer = nil
	}
	a.falseInterruptionMu.Unlock()
	a.resumeCanceledFalseInterruption(paused)
}

func (a *AgentActivity) ClearUserTurn() {
	a.cancelPendingEOUDetection()

	a.clearPendingUserTurn()

	if a.Session != nil {
		a.Session.mu.Lock()
		assistant := a.Session.Assistant
		a.Session.mu.Unlock()
		if clearer, ok := assistant.(realtimeAudioClearer); ok {
			if err := clearer.ClearAudio(); err != nil {
				logger.Logger.Warnw("failed to clear realtime audio", err)
			}
		}
		if clearer, ok := assistant.(inputTranscriptionClearer); ok {
			if err := clearer.ClearInputTranscription(); err != nil {
				logger.Logger.Warnw("failed to clear input transcription", err)
			}
		}
	}

	a.sttEOSReceived = false
	a.setSpeaking(false)
	a.notifyUserTurnUpdated()
	a.manualTurnCommitted = false
	a.userSpeechStartedAt = time.Time{}
	a.userSpeechStoppedAt = time.Time{}
	a.resetUserTurnLimitTracker()
	a.cancelPreemptiveGeneration()
}

func (a *AgentActivity) isUserSpeaking() bool {
	if a == nil {
		return false
	}
	a.speakingMu.RLock()
	defer a.speakingMu.RUnlock()
	return a.speaking
}

func (a *AgentActivity) setSpeaking(speaking bool) bool {
	a.speakingMu.Lock()
	wasSpeaking := a.speaking
	a.speaking = speaking
	a.speakingMu.Unlock()
	return wasSpeaking
}

func (a *AgentActivity) CommitUserTurn(ctx context.Context, opts CommitUserTurnOptions) (string, error) {
	a.cancelPendingEOUDetection()

	if ctx == nil {
		ctx = a.ctx
	}
	if opts.TranscriptTimeout == 0 {
		opts.TranscriptTimeout = 2 * time.Second
	}
	ctx, cancelCommit := context.WithCancel(ctx)
	a.commitUserTurnMu.Lock()
	if a.commitUserTurnCancel != nil && a.userTurnHookActive == 0 {
		a.commitUserTurnCancel()
	}
	a.commitUserTurnSeq++
	commitSeq := a.commitUserTurnSeq
	a.commitUserTurnCancel = cancelCommit
	a.commitUserTurnActive++
	a.commitUserTurnMu.Unlock()
	a.notifyUserTurnUpdated()
	defer func() {
		a.commitUserTurnMu.Lock()
		if a.commitUserTurnSeq == commitSeq {
			a.commitUserTurnCancel = nil
		}
		a.commitUserTurnActive--
		a.commitUserTurnMu.Unlock()
		cancelCommit()
		a.notifyUserTurnUpdated()
	}()
	replyAlreadyGenerated := false
	generateReplyAfterHook := false
	pendingTranscriptBeforeRealtime := a.pendingFinalTranscriptPresent()
	if a.Session != nil && !opts.SkipRealtimeAudio {
		a.Session.mu.Lock()
		assistant := a.Session.Assistant
		activity := a.Session.activity
		a.Session.mu.Unlock()
		if realtimeTurnDetectionEnabled(assistant) {
			opts.SkipReply = true
		} else if committer, ok := assistant.(realtimeAudioCommitter); ok {
			if err := committer.CommitAudio(); err != nil {
				return "", err
			}
			if !opts.SkipReply && activity == a {
				if pendingTranscriptBeforeRealtime {
					generateReplyAfterHook = true
				} else {
					if _, err := a.Session.GenerateReplyWithOptions(ctx, GenerateReplyOptions{}); err != nil {
						return "", err
					}
					replyAlreadyGenerated = true
				}
			}
			if !replyAlreadyGenerated && !generateReplyAfterHook {
				opts.SkipReply = true
			}
		}
	}
	if opts.TranscriptTimeout > 0 {
		a.userTurnMu.Lock()
		present := a.pendingUserTranscriptPresent
		lastFinalTranscriptTime := a.lastFinalTranscriptTime
		hasInterim := a.pendingInterimTranscript != ""
		a.userTurnMu.Unlock()
		needNewFinal := present && !lastFinalTranscriptTime.IsZero() && time.Since(lastFinalTranscriptTime) > 500*time.Millisecond
		if !present || needNewFinal {
			flusher, ok := a.inputTranscriptionFlusher()
			if ok {
				flushDuration := opts.STTFlushDuration
				if flushDuration == 0 {
					flushDuration = 2 * time.Second
				}
				if err := flusher.FlushInputTranscription(ctx, flushDuration); err != nil {
					return "", err
				}
			}
			if ok || hasInterim {
				deadline := time.NewTimer(opts.TranscriptTimeout)
				defer deadline.Stop()
				for {
					a.userTurnMu.Lock()
					present := a.pendingUserTranscriptPresent
					currentFinalTime := a.lastFinalTranscriptTime
					ch := a.userTurnUpdatedCh
					a.userTurnMu.Unlock()
					if present && (!needNewFinal || currentFinalTime.After(lastFinalTranscriptTime)) {
						break
					}
					select {
					case <-ch:
					case <-deadline.C:
						goto collect
					case <-ctx.Done():
						return "", ctx.Err()
					}
				}
			}
		}
	}

collect:
	a.userTurnMu.Lock()
	transcript := a.pendingUserTranscript
	language := a.pendingUserLanguage
	confidence := a.pendingTranscriptConfidence
	present := a.pendingUserTranscriptPresent
	preflightTranscript := a.pendingPreflightTranscript
	preflightConfidence := a.pendingPreflightConfidence
	interimTranscript := a.pendingInterimTranscript
	interimLanguage := a.pendingInterimLanguage
	interimSpeakerID := a.pendingInterimSpeakerID
	fallbackLanguage := ""
	fallbackSpeakerID := ""
	fallbackTranscript := ""
	fallbackFinal := false
	if preflightTranscript != "" {
		transcript = preflightTranscript
		confidence = preflightConfidence
		fallbackLanguage = interimLanguage
		fallbackSpeakerID = interimSpeakerID
		fallbackTranscript = interimTranscript
		present = true
		fallbackFinal = interimTranscript != ""
	}
	if preflightTranscript == "" && interimTranscript != "" {
		transcript = strings.TrimSpace(strings.Join([]string{transcript, interimTranscript}, " "))
		if !present {
			confidence = 1
		}
		fallbackLanguage = interimLanguage
		fallbackSpeakerID = interimSpeakerID
		fallbackTranscript = interimTranscript
		present = true
		fallbackFinal = true
	}
	a.pendingUserTranscript = ""
	a.pendingUserLanguage = ""
	a.pendingTranscriptConfidence = 0
	a.pendingTranscriptConfidenceSum = 0
	a.pendingTranscriptConfidenceCount = 0
	a.pendingUserTranscriptPresent = false
	a.lastFinalTranscriptTime = time.Time{}
	a.pendingStartedSpeakingAt = nil
	a.pendingStoppedSpeakingAt = nil
	a.pendingTranscriptionDelay = 0
	a.pendingInterimTranscript = ""
	a.pendingInterimLanguage = ""
	a.pendingInterimSpeakerID = ""
	a.pendingPreflightTranscript = ""
	a.pendingPreflightConfidence = 0
	a.userTurnMu.Unlock()

	if !present || transcript == "" {
		return "", nil
	}
	if !fallbackFinal && rejectsZeroConfidenceTranscript(transcript, confidence) {
		return "", nil
	}

	if fallbackFinal && a.Session != nil {
		a.Session.EmitUserInputTranscribed(UserInputTranscribedEvent{
			Language:   fallbackLanguage,
			Transcript: fallbackTranscript,
			IsFinal:    true,
			SpeakerID:  fallbackSpeakerID,
		})
	}
	if a.turnDetectionMode() == TurnDetectionModeManual {
		a.manualTurnCommitted = true
	}
	if _, err := a.completeUserTurn(ctx, EndOfTurnInfo{
		SkipReply:              opts.SkipReply,
		ReplyAlreadyGenerated:  replyAlreadyGenerated,
		GenerateReplyAfterHook: generateReplyAfterHook,
		NewTranscript:          transcript,
		Language:               firstNonEmpty(language, fallbackLanguage),
		TranscriptConfidence:   confidence,
		AudioFrames:            a.userAudioSnapshot(),
	}); err != nil {
		return transcript, err
	}
	return transcript, nil
}

func (a *AgentActivity) shouldIgnoreManualCommittedSTT(evType stt.SpeechEventType) bool {
	if a == nil || a.turnDetectionMode() != TurnDetectionModeManual || !a.manualTurnCommitted {
		return false
	}
	if evType == stt.SpeechEventInterimTranscript {
		return true
	}
	a.commitUserTurnMu.Lock()
	activeHook := a.userTurnHookActive > 0
	a.commitUserTurnMu.Unlock()
	return !activeHook
}

func (a *AgentActivity) inputTranscriptionFlusher() (inputTranscriptionFlusher, bool) {
	if a == nil || a.Session == nil {
		return nil, false
	}
	a.Session.mu.Lock()
	assistant := a.Session.Assistant
	a.Session.mu.Unlock()
	flusher, ok := assistant.(inputTranscriptionFlusher)
	return flusher, ok
}

func (a *AgentActivity) completeUserTurn(ctx context.Context, info EndOfTurnInfo) (*SpeechHandle, error) {
	turnSeq := a.nextUserTurnCompletionSeq()
	a.userTurnCompletionMu.Lock()
	defer a.userTurnCompletionMu.Unlock()

	if rejectsZeroConfidenceTranscript(info.NewTranscript, info.TranscriptConfidence) {
		a.cancelPreemptiveGeneration()
		logger.Logger.Warnw("skipping zero-confidence user turn", nil, "transcript", info.NewTranscript)
		return nil, nil
	}
	confidence := info.TranscriptConfidence
	newMsg := &llm.ChatMessage{
		Role:                 llm.ChatRoleUser,
		Content:              []llm.ChatContent{{Text: info.NewTranscript}},
		TranscriptConfidence: &confidence,
		CreatedAt:            time.Now(),
	}
	if a.realtimeServerTurnDetectionEnabled() {
		a.cancelPreemptiveGeneration()
		a.resetPreemptiveGenerationCount()
		return nil, nil
	}
	if info.SkipReply {
		a.cancelPreemptiveGeneration()
		a.resetPreemptiveGenerationCount()
		a.commitUserMessage(newMsg)
		return nil, nil
	}
	a.queueMu.Lock()
	currentSpeech := a.currentSpeech
	schedulingPaused := a.schedulingPaused || a.schedulingDraining
	a.queueMu.Unlock()
	if currentSpeech != nil && !currentSpeech.AllowInterruptions && !currentSpeech.IsInterrupted() && !currentSpeech.IsDone() {
		a.cancelPreemptiveGeneration()
		a.resetPreemptiveGenerationCount()
		logger.Logger.Warnw("skipping reply to user input, current speech generation cannot be interrupted", nil, "userInput", info.NewTranscript)
		return nil, nil
	}
	if a.shouldSkipShortInterruption(currentSpeech, info.NewTranscript) {
		a.cancelPreemptiveGeneration()
		return nil, nil
	}
	if schedulingPaused {
		a.cancelPreemptiveGeneration()
		logger.Logger.Warnw("skipping on_user_turn_completed, speech scheduling is paused", nil, "userInput", info.NewTranscript)
		if a.Session != nil && a.Session.isClosing() {
			newMsg.Metrics = metricsReportFromEndOfTurn(info, 0)
			a.commitUserMessage(newMsg)
		}
		return nil, nil
	}
	a.resetPreemptiveGenerationCount()
	if currentSpeech != nil && !currentSpeech.IsInterrupted() && !currentSpeech.IsDone() {
		paused := a.cancelPausedFalseInterruption(false)
		if err := currentSpeech.Interrupt(false); err != nil {
			return nil, err
		}
		if err := currentSpeech.Wait(ctx); err != nil {
			return nil, err
		}
		if a.Session != nil {
			a.Session.mu.Lock()
			assistant := a.Session.Assistant
			a.Session.mu.Unlock()
			if interrupter, ok := assistant.(realtimeInterrupter); ok {
				if err := interrupter.Interrupt(); err != nil {
					return nil, err
				}
			}
		}
		a.resumeCanceledFalseInterruption(paused)
	}

	chatCtx := a.RetrieveChatCtx().Copy()
	hookStart := time.Now()
	a.commitUserTurnMu.Lock()
	a.userTurnHookActive++
	a.commitUserTurnMu.Unlock()
	err := a.AgentIntf.OnUserTurnCompleted(ctx, chatCtx, newMsg)
	a.commitUserTurnMu.Lock()
	a.userTurnHookActive--
	a.commitUserTurnMu.Unlock()
	a.notifyUserTurnUpdated()
	if err != nil {
		var stopResponse llm.StopResponse
		if errors.As(err, &stopResponse) {
			a.cancelPreemptiveGeneration()
			return nil, nil
		}
		a.cancelPreemptiveGeneration()
		logger.Logger.Errorw("error occurred during on_user_turn_completed", err)
		return nil, nil
	}
	hookDelay := time.Since(hookStart).Seconds()
	newMsg.Metrics = metricsReportFromEndOfTurn(info, hookDelay)
	if info.ReplyAlreadyGenerated {
		a.cancelPreemptiveGeneration()
		return nil, nil
	}
	if info.GenerateReplyAfterHook {
		a.cancelPreemptiveGeneration()
		if a.Session == nil {
			return nil, nil
		}
		userInitiated := false
		handle, err := a.Session.GenerateReplyWithOptions(ctx, GenerateReplyOptions{
			UserMessage:   newMsg,
			ChatCtx:       chatCtx,
			InputModality: "audio",
			UserInitiated: &userInitiated,
		})
		if err != nil {
			return nil, err
		}
		a.interruptObsoleteUserTurnReply(turnSeq, handle)
		a.emitEOUMetrics(handle, info, hookDelay)
		return handle, nil
	}
	if a.Agent.LLM == nil || a.Session == nil {
		a.cancelPreemptiveGeneration()
		return nil, nil
	}
	a.queueMu.Lock()
	schedulingPaused = a.schedulingPaused || a.schedulingDraining
	a.queueMu.Unlock()
	if schedulingPaused {
		a.cancelPreemptiveGeneration()
		logger.Logger.Warnw("skipping reply to user input, speech scheduling is paused", nil, "userInput", info.NewTranscript)
		if a.Session != nil && a.Session.isClosing() {
			a.commitUserMessage(newMsg)
		}
		return nil, nil
	}
	handle, err := a.usePreemptiveGenerationIfMatching(chatCtx, newMsg)
	if err != nil {
		return nil, err
	}
	if handle == nil {
		userInitiated := false
		handle, err = a.Session.GenerateReplyWithOptions(ctx, GenerateReplyOptions{
			UserMessage:   newMsg,
			ChatCtx:       chatCtx,
			InputModality: "audio",
			UserInitiated: &userInitiated,
		})
		if err != nil {
			return nil, err
		}
	}
	a.interruptObsoleteUserTurnReply(turnSeq, handle)
	a.emitEOUMetrics(handle, info, hookDelay)
	return handle, nil
}

func (a *AgentActivity) nextUserTurnCompletionSeq() uint64 {
	a.commitUserTurnMu.Lock()
	defer a.commitUserTurnMu.Unlock()
	a.userTurnCompletionSeq++
	return a.userTurnCompletionSeq
}

func (a *AgentActivity) interruptObsoleteUserTurnReply(turnSeq uint64, handle *SpeechHandle) {
	if a == nil || handle == nil {
		return
	}
	a.commitUserTurnMu.Lock()
	obsolete := a.userTurnCompletionSeq != turnSeq
	a.commitUserTurnMu.Unlock()
	if obsolete {
		_ = handle.Interrupt(false)
	}
}

func (a *AgentActivity) emitEOUMetrics(handle *SpeechHandle, info EndOfTurnInfo, hookDelay float64) {
	if a == nil || a.Session == nil || handle == nil {
		return
	}
	mode := a.turnDetectionMode()
	metadata := (*telemetry.Metadata)(nil)
	if mode != "" {
		metadata = &telemetry.Metadata{
			ModelName:     "unknown",
			ModelProvider: string(mode),
		}
	}
	a.Session.EmitMetricsCollected(&telemetry.EOUMetrics{
		Timestamp:                time.Now(),
		EndOfUtteranceDelay:      info.EndOfTurnDelay,
		TranscriptionDelay:       info.TranscriptionDelay,
		OnUserTurnCompletedDelay: hookDelay,
		SpeechID:                 handle.ID,
		Metadata:                 metadata,
	})
}

func (a *AgentActivity) shouldSkipShortInterruption(currentSpeech *SpeechHandle, transcript string) bool {
	if currentSpeech == nil || !currentSpeech.AllowInterruptions || currentSpeech.IsInterrupted() || currentSpeech.IsDone() {
		return false
	}
	if !a.InterruptionEnabled() {
		return false
	}
	return a.shortInterruptionTranscript(transcript)
}

func (a *AgentActivity) shortInterruptionTranscript(transcript string) bool {
	if a.Session == nil || a.Session.Options.MinInterruptionWords <= 0 {
		return false
	}
	if !a.hasSTTModel() {
		return false
	}
	var wordCount int
	if a.Session.Options.WordTokenizer != nil {
		wordCount = len(a.Session.Options.WordTokenizer.Tokenize(transcript, ""))
	} else {
		wordCount = len(tokenize.SplitWords(transcript, true, true, false))
	}
	return wordCount < a.Session.Options.MinInterruptionWords
}

func (a *AgentActivity) realtimeServerTurnDetectionEnabled() bool {
	if a == nil || a.Session == nil {
		return false
	}
	a.Session.mu.Lock()
	assistant := a.Session.Assistant
	a.Session.mu.Unlock()
	return realtimeTurnDetectionEnabled(assistant)
}

func (a *AgentActivity) currentInterruptionTranscript() string {
	if a == nil {
		return ""
	}
	a.userTurnMu.Lock()
	defer a.userTurnMu.Unlock()
	if a.pendingInterimTranscript != "" {
		return a.pendingInterimTranscript
	}
	return a.pendingUserTranscript
}

func (a *AgentActivity) checkUserTurnLimit(transcript string) {
	if a == nil || a.Session == nil || transcript == "" {
		return
	}
	maxWords := a.Session.Options.UserTurnLimitMaxWords
	maxDuration := a.Session.Options.UserTurnLimitMaxDuration
	if maxWords <= 0 && maxDuration <= 0 {
		return
	}

	now := time.Now()
	a.userTurnMu.Lock()
	if a.userTurnLimitStartedAt.IsZero() {
		if !a.userSpeechStartedAt.IsZero() {
			a.userTurnLimitStartedAt = a.userSpeechStartedAt
		} else {
			a.userTurnLimitStartedAt = now
		}
	}
	wordCount := a.userTurnLimitWordCount + a.countUserTurnWordsLocked(transcript)
	accumulated := strings.TrimSpace(strings.TrimSpace(a.userTurnLimitTranscript) + " " + strings.TrimSpace(transcript))
	a.userTurnLimitWordCount = wordCount
	a.userTurnLimitTranscript = accumulated
	duration := now.Sub(a.userTurnLimitStartedAt)
	wordsExceeded := maxWords > 0 && wordCount >= maxWords
	durationExceeded := maxDuration > 0 && duration.Seconds() >= maxDuration
	a.userTurnMu.Unlock()

	if !wordsExceeded && !durationExceeded {
		return
	}
	a.Session.EmitUserTurnExceeded(UserTurnExceededEvent{
		Transcript:            transcript,
		AccumulatedTranscript: accumulated,
		AccumulatedWordCount:  wordCount,
		Duration:              duration,
	})
}

func (a *AgentActivity) countUserTurnWordsLocked(transcript string) int {
	if a.Session != nil && a.Session.Options.WordTokenizer != nil {
		return len(a.Session.Options.WordTokenizer.Tokenize(transcript, ""))
	}
	return len(tokenize.SplitWords(transcript, true, true, false))
}

func (a *AgentActivity) resetUserTurnLimitTracker() {
	if a == nil {
		return
	}
	a.userTurnMu.Lock()
	a.userTurnLimitStartedAt = time.Time{}
	a.userTurnLimitTranscript = ""
	a.userTurnLimitWordCount = 0
	a.userTurnMu.Unlock()
}

func (a *AgentActivity) maybeStartPreemptiveGeneration(transcript string, confidence float64) {
	if a == nil || a.Agent == nil || a.Session == nil || transcript == "" {
		return
	}
	opts := a.Session.Options
	mode := a.turnDetectionMode()
	if !opts.PreemptiveGeneration || a.Agent.LLM == nil || mode == TurnDetectionModeManual || mode == TurnDetectionModeRealtimeLLM {
		return
	}
	a.queueMu.Lock()
	schedulingPaused := a.schedulingPaused || a.schedulingDraining
	currentSpeech := a.currentSpeech
	a.queueMu.Unlock()
	if schedulingPaused || (currentSpeech != nil && !currentSpeech.IsInterrupted()) {
		return
	}
	a.cancelPreemptiveGeneration()
	if opts.PreemptiveGenerationMaxSpeechDuration > 0 && !a.userSpeechStartedAt.IsZero() &&
		time.Since(a.userSpeechStartedAt).Seconds() > opts.PreemptiveGenerationMaxSpeechDuration {
		return
	}

	a.preemptiveMu.Lock()
	if a.preemptiveGenerationCount >= opts.PreemptiveGenerationMaxRetries {
		a.preemptiveMu.Unlock()
		return
	}
	a.preemptiveGenerationCount++
	a.preemptiveMu.Unlock()

	msg := &llm.ChatMessage{
		Role:                 llm.ChatRoleUser,
		Content:              []llm.ChatContent{{Text: transcript}},
		TranscriptConfidence: &confidence,
		CreatedAt:            time.Now(),
	}
	chatCtx := a.RetrieveChatCtx().Copy()
	scheduleSpeech := false
	userInitiated := false
	handle, err := a.Session.GenerateReplyWithOptions(a.ctx, GenerateReplyOptions{
		UserMessage:    msg,
		ChatCtx:        chatCtx,
		InputModality:  "audio",
		ScheduleSpeech: &scheduleSpeech,
		UserInitiated:  &userInitiated,
	})
	if err != nil {
		logger.Logger.Warnw("failed to start preemptive generation", err, "transcript", transcript)
		return
	}

	a.preemptiveMu.Lock()
	a.preemptiveGeneration = &preemptiveGeneration{
		speech:     handle,
		userMsg:    msg,
		transcript: transcript,
		chatCtx:    chatCtx.Copy(),
		toolsKey:   a.currentToolsKey(),
		toolChoice: a.currentToolChoice(),
		createdAt:  time.Now(),
	}
	a.preemptiveMu.Unlock()
	if assistant, ok := a.Session.Assistant.(preemptiveSpeechAssistant); ok {
		go assistant.OnSpeechPreemptive(a.ctx, handle)
	}
}

func (a *AgentActivity) usePreemptiveGenerationIfMatching(chatCtx *llm.ChatContext, newMsg *llm.ChatMessage) (*SpeechHandle, error) {
	if a == nil || a.Session == nil || newMsg == nil {
		return nil, nil
	}
	a.preemptiveMu.Lock()
	preemptive := a.preemptiveGeneration
	a.preemptiveGeneration = nil
	a.preemptiveMu.Unlock()
	if preemptive == nil {
		return nil, nil
	}
	matches := preemptive.transcript == newMsg.TextContent() &&
		preemptive.chatCtx.IsEquivalent(chatCtx) &&
		preemptive.toolsKey == a.currentToolsKey() &&
		reflect.DeepEqual(preemptive.toolChoice, a.currentToolChoice())
	if !matches {
		_ = preemptive.speech.Interrupt(true)
		return nil, nil
	}
	preemptive.userMsg.Metrics = newMsg.Metrics
	if err := a.ScheduleSpeech(preemptive.speech, SpeechPriorityNormal, false); err != nil {
		return nil, err
	}
	a.Session.EmitConversationItemAdded(preemptive.userMsg)
	a.Session.watchActiveRunSpeechHandle(preemptive.speech)
	logger.Logger.Debugw("using preemptive generation", "preemptiveLeadTime", time.Since(preemptive.createdAt).Seconds())
	return preemptive.speech, nil
}

func (a *AgentActivity) cancelPreemptiveGeneration() {
	if a == nil {
		return
	}
	a.preemptiveMu.Lock()
	preemptive := a.preemptiveGeneration
	a.preemptiveGeneration = nil
	a.preemptiveMu.Unlock()
	if preemptive != nil {
		_ = preemptive.speech.Interrupt(true)
	}
}

func (a *AgentActivity) resetPreemptiveGenerationCount() {
	if a == nil {
		return
	}
	a.preemptiveMu.Lock()
	a.preemptiveGenerationCount = 0
	a.preemptiveMu.Unlock()
}

func (a *AgentActivity) currentToolsKey() string {
	if a == nil {
		return ""
	}
	tools := a.Tools()
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if named, ok := tool.(interface{ Name() string }); ok && named.Name() != "" {
			names = append(names, named.Name())
		}
	}
	sort.Strings(names)
	return strings.Join(names, "\x00")
}

func (a *AgentActivity) currentToolChoice() llm.ToolChoice {
	if a == nil || a.Session == nil {
		return nil
	}
	return a.Session.Options.ToolChoice
}

func metricsReportFromEndOfTurn(info EndOfTurnInfo, onUserTurnCompletedDelay float64) map[string]any {
	metrics := make(map[string]any)
	if info.StartedSpeakingAt != nil {
		metrics["started_speaking_at"] = *info.StartedSpeakingAt
	}
	if info.StoppedSpeakingAt != nil {
		metrics["stopped_speaking_at"] = *info.StoppedSpeakingAt
	}
	metrics["transcription_delay"] = info.TranscriptionDelay
	metrics["end_of_turn_delay"] = info.EndOfTurnDelay
	metrics["on_user_turn_completed_delay"] = onUserTurnCompletedDelay
	return metrics
}

func rejectsZeroConfidenceTranscript(transcript string, confidence float64) bool {
	return strings.TrimSpace(transcript) != "" && confidence <= 0
}

func (a *AgentActivity) commitUserMessage(msg *llm.ChatMessage) {
	if msg == nil || msg.TextContent() == "" {
		return
	}
	if a.Agent.ChatCtx == nil {
		a.Agent.ChatCtx = llm.NewChatContext()
	}
	a.Agent.ChatCtx.Append(msg)
	if a.Session != nil {
		a.Session.EmitConversationItemAdded(msg)
	}
}

func (a *AgentActivity) clearPendingUserTurn() {
	a.userTurnMu.Lock()
	defer a.userTurnMu.Unlock()

	a.pendingUserTranscript = ""
	a.pendingUserLanguage = ""
	a.pendingTranscriptConfidence = 0
	a.pendingTranscriptConfidenceSum = 0
	a.pendingTranscriptConfidenceCount = 0
	a.pendingUserTranscriptPresent = false
	a.lastFinalTranscriptTime = time.Time{}
	a.pendingStartedSpeakingAt = nil
	a.pendingStoppedSpeakingAt = nil
	a.pendingTranscriptionDelay = 0
	a.pendingInterimTranscript = ""
	a.pendingInterimLanguage = ""
	a.pendingInterimSpeakerID = ""
	a.pendingPreflightTranscript = ""
	a.pendingPreflightConfidence = 0
}

func (a *AgentActivity) notifyUserTurnUpdated() {
	a.userTurnMu.Lock()
	ch := a.userTurnUpdatedCh
	a.userTurnMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (a *AgentActivity) pendingFinalEndOfTurnInfo() EndOfTurnInfo {
	a.userTurnMu.Lock()
	defer a.userTurnMu.Unlock()
	if !a.pendingUserTranscriptPresent {
		return EndOfTurnInfo{}
	}
	info := EndOfTurnInfo{
		NewTranscript:        a.pendingUserTranscript,
		Language:             a.pendingUserLanguage,
		TranscriptConfidence: a.pendingTranscriptConfidence,
		TranscriptionDelay:   a.pendingTranscriptionDelay,
		StartedSpeakingAt:    a.pendingStartedSpeakingAt,
		StoppedSpeakingAt:    a.pendingStoppedSpeakingAt,
		AudioFrames:          a.userAudioSnapshot(),
	}
	if info.StartedSpeakingAt == nil && !a.userSpeechStartedAt.IsZero() {
		started := timeToUnixSeconds(a.userSpeechStartedAt)
		info.StartedSpeakingAt = &started
	}
	if info.StoppedSpeakingAt == nil && !a.userSpeechStoppedAt.IsZero() {
		stopped := timeToUnixSeconds(a.userSpeechStoppedAt)
		info.StoppedSpeakingAt = &stopped
	}
	return info
}

func (a *AgentActivity) pendingFinalTranscriptPresent() bool {
	if a == nil {
		return false
	}
	a.userTurnMu.Lock()
	defer a.userTurnMu.Unlock()
	return a.pendingUserTranscriptPresent
}

func (a *AgentActivity) vadBasedTurnDetection() bool {
	if a == nil {
		return false
	}
	mode := a.turnDetectionMode()
	return mode == TurnDetectionModeVAD || (mode == "" && a.hasVADModel())
}

func (a *AgentActivity) turnDetectionMode() TurnDetectionMode {
	mode := ""
	if a.Agent.TurnDetection != "" {
		mode = string(a.Agent.TurnDetection)
	} else if a.Session != nil {
		mode = string(a.Session.Options.TurnDetection)
	}
	if realtime, turnDetection := a.realtimeTurnDetectionCapabilities(); realtime && TurnDetectionMode(mode) != TurnDetectionModeRealtimeLLM {
		if turnDetection && mode != "" {
			logger.Logger.Warnw("turn_detection is set to a local mode, but realtime server turn detection is enabled", nil)
			return ""
		}
		if !turnDetection && (mode == "" || TurnDetectionMode(mode) == TurnDetectionModeSTT) {
			if a.hasVADModel() {
				return TurnDetectionModeVAD
			}
			if TurnDetectionMode(mode) == TurnDetectionModeSTT {
				logger.Logger.Warnw("turn_detection is set to stt, but realtime model local STT turn detection is ignored", nil)
				return ""
			}
		}
	}
	switch TurnDetectionMode(mode) {
	case TurnDetectionModeSTT:
		if !a.hasSTTModel() {
			logger.Logger.Warnw("turn_detection is set to stt, but no STT model is provided", nil)
			return ""
		}
	case TurnDetectionModeVAD:
		if !a.hasVADModel() {
			logger.Logger.Warnw("turn_detection is set to vad, but no VAD model is provided", nil)
			return ""
		}
	case TurnDetectionModeRealtimeLLM:
		if realtime, turnDetection := a.realtimeTurnDetectionCapabilities(); !realtime || !turnDetection {
			logger.Logger.Warnw("turn_detection is set to realtime_llm, but no realtime model with turn detection is provided", nil)
			if realtime && a.hasVADModel() {
				return TurnDetectionModeVAD
			}
			return ""
		}
	}
	return TurnDetectionMode(mode)
}

func (a *AgentActivity) hasVADModel() bool {
	if a == nil {
		return false
	}
	if (a.Agent != nil && a.Agent.VAD != nil) || (a.Session != nil && a.Session.VAD != nil) {
		return true
	}
	if a.Session == nil {
		return false
	}
	assistant := a.Session.Assistant
	if pipeline, ok := assistant.(*PipelineAgent); ok {
		return pipeline.vad != nil
	}
	return false
}

func (a *AgentActivity) hasSTTModel() bool {
	if a == nil {
		return false
	}
	if (a.Agent != nil && a.Agent.STT != nil) || (a.Session != nil && a.Session.STT != nil) {
		return true
	}
	if a.Session == nil {
		return false
	}
	assistant := a.Session.Assistant
	if pipeline, ok := assistant.(*PipelineAgent); ok {
		return pipeline.stt != nil
	}
	return false
}

func (a *AgentActivity) realtimeTurnDetectionCapabilities() (bool, bool) {
	if a == nil {
		return false, false
	}
	if a.Agent != nil && a.Agent.RealtimeModel != nil {
		return true, a.Agent.RealtimeModel.Capabilities().TurnDetection
	}
	if a.Session != nil {
		assistant := a.Session.Assistant
		if capabilities, ok := assistant.(realtimeCapabilitiesAssistant); ok {
			return true, capabilities.RealtimeCapabilities().TurnDetection
		}
	}
	return false, false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func referenceTranscriptLanguage(current, language, transcript string) string {
	if current == "" || (language != "" && len(transcript) > 5) {
		return language
	}
	return current
}

func (a *AgentActivity) runEOUDetection(info EndOfTurnInfo) {
	if a.hasSTTModel() && strings.TrimSpace(info.NewTranscript) == "" && a.turnDetectionMode() != TurnDetectionModeManual {
		return
	}

	a.eouMu.Lock()
	if a.eouCancel != nil {
		a.eouCancel()
	}
	ctx, cancel := context.WithCancel(a.ctx)
	done := make(chan struct{})
	a.eouCancel = cancel
	a.eouDone = done
	a.eouMu.Unlock()
	a.notifyUserTurnUpdated()

	go func() {
		defer func() {
			cancel()
			a.eouMu.Lock()
			if a.eouDone == done {
				a.eouCancel = nil
				a.eouDone = nil
			}
			a.eouMu.Unlock()
			close(done)
			a.notifyUserTurnUpdated()
		}()

		endpointingDelay := a.minEndpointingDelay()

		if a.Agent.AudioTurnDetector != nil && len(info.AudioFrames) > 0 {
			prob, err := a.Agent.AudioTurnDetector.PredictEndOfTurnAudio(ctx, info.AudioFrames)
			if err == nil {
				logger.Logger.Infow("Audio EOU prediction", "probability", prob)
				if prob < 0.5 {
					endpointingDelay = a.maxEndpointingDelay()
				}
			} else {
				logger.Logger.Errorw("Audio EOU prediction failed", err)
			}
		} else if a.Agent.TurnDetector != nil && info.NewTranscript != "" {
			// Predict end of turn
			chatCtx := a.RetrieveChatCtx().Copy()
			chatCtx.Append(&llm.ChatMessage{
				Role:    llm.ChatRoleUser,
				Content: []llm.ChatContent{{Text: info.NewTranscript}},
			})

			prob, err := a.Agent.TurnDetector.PredictEndOfTurn(ctx, chatCtx)
			if err == nil {
				logger.Logger.Infow("EOU prediction", "probability", prob)
				// Apply probability threshold logic
				if prob < a.turnDetectorThreshold(info.Language) {
					endpointingDelay = a.maxEndpointingDelay()
				}
			} else {
				logger.Logger.Errorw("EOU prediction failed", err)
			}
		}

		if info.StoppedSpeakingAt != nil {
			endpointingDelay += *info.StoppedSpeakingAt - timeToUnixSeconds(time.Now())
		}
		timer := time.NewTimer(time.Duration(endpointingDelay * float64(time.Second)))
		defer timer.Stop()

		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if strings.TrimSpace(info.NewTranscript) == "" {
				a.clearPendingUserTurn()
				return
			}
			a.queueMu.Lock()
			currentSpeech := a.currentSpeech
			a.queueMu.Unlock()
			if a.shouldSkipShortInterruption(currentSpeech, info.NewTranscript) {
				a.cancelPreemptiveGeneration()
				return
			}
			a.clearPendingUserTurn()
			if _, err := a.completeUserTurn(a.ctx, info); err != nil {
				logger.Logger.Errorw("user turn completion failed", err)
				return
			}
		}
	}()
}

func (a *AgentActivity) turnDetectorThreshold(language string) float64 {
	if a == nil || a.Agent == nil || a.Agent.TurnDetector == nil || language == "" {
		return 0.5
	}
	detector, ok := a.Agent.TurnDetector.(turnDetectorThreshold)
	if !ok {
		return 0.5
	}
	threshold, ok := detector.UnlikelyThreshold(language)
	if !ok {
		return 0.5
	}
	return threshold
}

func (a *AgentActivity) minEndpointingDelay() float64 {
	return a.EndpointingOpts().MinDelay
}

func (a *AgentActivity) maxEndpointingDelay() float64 {
	return a.EndpointingOpts().MaxDelay
}

func (a *AgentActivity) minInterruptionDuration() float64 {
	if a != nil && a.Session != nil {
		if a.Session.Options.MinInterruptionDurationSet || a.Session.Options.MinInterruptionDuration > 0 {
			return a.Session.Options.MinInterruptionDuration
		}
	}
	return 0.5
}

func (a *AgentActivity) endpointing() Endpointing {
	if a.Session == nil {
		return nil
	}
	return a.Session.Options.Endpointing
}

func vadSpeechStartTimestamp(ev *vad.VADEvent) float64 {
	if ev == nil {
		return float64(time.Now().UnixNano()) / float64(time.Second)
	}
	return max(ev.Timestamp-ev.SpeechDuration-ev.InferenceDuration, 0)
}

func vadSpeechEndTimestamp(ev *vad.VADEvent) float64 {
	if ev == nil {
		return float64(time.Now().UnixNano()) / float64(time.Second)
	}
	return max(ev.Timestamp-ev.SilenceDuration-ev.InferenceDuration, 0)
}

func vadSpeechStoppedAt(ev *vad.VADEvent) time.Time {
	stoppedAt := time.Now()
	if ev == nil {
		return stoppedAt
	}
	delay := time.Duration((ev.SilenceDuration + ev.InferenceDuration) * float64(time.Second))
	return stoppedAt.Add(-delay)
}

func vadSpeechStartedAt(ev *vad.VADEvent) time.Time {
	startedAt := time.Now()
	if ev == nil {
		return startedAt
	}
	delay := time.Duration((ev.SpeechDuration + ev.InferenceDuration) * float64(time.Second))
	return startedAt.Add(-delay)
}
