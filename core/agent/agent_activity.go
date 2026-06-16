package agent

import (
	"context"
	"errors"
	"fmt"
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

var ErrSpeechSchedulingPaused = errors.New("speech scheduling is paused")

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
	SkipReply            bool
	NewTranscript        string
	Language             string
	TranscriptConfidence float64
	EndOfTurnDelay       float64
	TranscriptionDelay   float64
	StartedSpeakingAt    *float64
	StoppedSpeakingAt    *float64
	AudioFrames          []*model.AudioFrame
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

	sttEOSReceived bool
	speaking       bool

	providerUnsubscribes []func()
	registeredTools      []llm.Tool

	userTurnMu                   sync.Mutex
	userTurnUpdatedCh            chan struct{}
	pendingInterimTranscript     string
	pendingInterimLanguage       string
	pendingInterimSpeakerID      string
	userTurnCompletionMu         sync.Mutex
	pendingUserTranscript        string
	pendingUserLanguage          string
	pendingTranscriptConfidence  float64
	pendingUserTranscriptPresent bool

	ctx    context.Context
	cancel context.CancelFunc

	eouMu     sync.Mutex
	eouCancel context.CancelFunc

	userTurnExceededMu     sync.Mutex
	userTurnExceededLocked bool

	userAudioMu     sync.Mutex
	userAudioFrames []*model.AudioFrame
}

func NewAgentActivity(agentIntf AgentInterface, session *AgentSession) *AgentActivity {
	ctx, cancel := context.WithCancel(context.Background())
	activity := &AgentActivity{
		AgentIntf:         agentIntf,
		Agent:             agentIntf.GetAgent(),
		Session:           session,
		speechQueue:       make([]scheduledSpeech, 0),
		queueUpdatedCh:    make(chan struct{}, 1),
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
	a.queueMu.Lock()
	interrupted := make([]*SpeechHandle, 0, len(a.speechQueue)+1)
	if a.currentSpeech != nil {
		if err := a.currentSpeech.Interrupt(force); err != nil {
			a.queueMu.Unlock()
			return err
		}
		interrupted = append(interrupted, a.currentSpeech)
	}
	for _, queued := range a.speechQueue {
		if err := queued.speech.Interrupt(force); err != nil {
			a.queueMu.Unlock()
			return err
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
				return err
			}
		}
	}

	for _, speech := range interrupted {
		if err := speech.Wait(a.ctx); err != nil {
			return err
		}
	}

	return nil
}

func (a *AgentActivity) WaitForInactive(ctx context.Context) error {
	for {
		a.processQueue()
		active := a.activeSpeechHandles()
		if len(active) == 0 {
			return nil
		}
		for _, speech := range active {
			if err := speech.Wait(ctx); err != nil {
				return err
			}
			a.processQueue()
		}
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
	a.userTurnExceededLocked = true
	a.userTurnExceededMu.Unlock()

	go func() {
		defer func() {
			a.userTurnExceededMu.Lock()
			a.userTurnExceededLocked = false
			a.userTurnExceededMu.Unlock()
		}()

		shouldRun, err := a.waitForUserTurnExceededCallback(a.ctx)
		if err != nil {
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
	if toolChoice == nil {
		return nil
	}
	return updater.UpdateOptions(context.Background(), llm.RealtimeSessionOptions{
		ToolChoice:    toolChoice,
		ToolChoiceSet: true,
	})
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

	err := a.WaitForInactive(ctx)

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
	a.speaking = true
	a.sttEOSReceived = false
	a.clearUserAudioFrames()
	if a.Session != nil {
		a.Session.UpdateUserState(UserStateSpeaking)
	}
	if endpointing := a.endpointing(); endpointing != nil {
		startedAt := vadEventTimestamp(ev)
		overlapping := a.Session != nil && a.Session.AgentStateValue() == AgentStateSpeaking
		endpointing.OnStartOfSpeech(startedAt, overlapping)
	}
	logger.Logger.Infow("Start of speech detected")

	// Cancel pending EOU detection
	a.eouMu.Lock()
	if a.eouCancel != nil {
		a.eouCancel()
		a.eouCancel = nil
	}
	a.eouMu.Unlock()
}

func (a *AgentActivity) OnEndOfSpeech(ev *vad.VADEvent) {
	a.speaking = false
	if a.Session != nil {
		a.Session.UpdateUserState(UserStateListening)
	}
	if endpointing := a.endpointing(); endpointing != nil {
		endpointing.OnEndOfSpeech(vadEventTimestamp(ev), false)
	}
	logger.Logger.Infow("End of speech detected")

	if a.vadBasedTurnDetection() {
		// Trigger EOU detection
		a.runEOUDetection(a.pendingFinalEndOfTurnInfo())
	}
}

func (a *AgentActivity) OnVADInferenceDone(ev *vad.VADEvent) {
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
	a.interruptByAudioActivity("VAD inference", "speech_duration", ev.SpeechDuration)
}

func (a *AgentActivity) OnInterimTranscript(ev *stt.SpeechEvent) {
	if a.Session == nil {
		return
	}
	if a.realtimeUserTranscriptionEnabled() {
		return
	}
	transcript := ""
	language := ""
	speakerID := ""
	if ev != nil && len(ev.Alternatives) > 0 {
		transcript = ev.Alternatives[0].Text
		language = ev.Alternatives[0].Language
		speakerID = ev.Alternatives[0].SpeakerID
	}
	a.userTurnMu.Lock()
	a.pendingInterimTranscript = transcript
	a.pendingInterimLanguage = language
	a.pendingInterimSpeakerID = speakerID
	a.userTurnMu.Unlock()

	a.Session.EmitUserInputTranscribed(UserInputTranscribedEvent{
		Language:   language,
		Transcript: transcript,
		IsFinal:    false,
		SpeakerID:  speakerID,
	})
	turnDetection := a.turnDetectionMode()
	if transcript != "" && turnDetection != TurnDetectionModeManual && turnDetection != TurnDetectionModeRealtimeLLM {
		a.interruptByAudioActivity("interim transcript", "transcript", transcript)
	}
}

func (a *AgentActivity) OnFinalTranscript(ev *stt.SpeechEvent) {
	if a.realtimeUserTranscriptionEnabled() {
		return
	}
	a.sttEOSReceived = true
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
	if rejectsZeroConfidenceTranscript(transcript, confidence) {
		logger.Logger.Warnw("skipping zero-confidence final transcript", nil, "transcript", transcript)
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

	a.userTurnMu.Lock()
	a.pendingUserTranscript = transcript
	a.pendingUserLanguage = language
	a.pendingTranscriptConfidence = confidence
	a.pendingUserTranscriptPresent = true
	a.pendingInterimTranscript = ""
	a.pendingInterimLanguage = ""
	a.pendingInterimSpeakerID = ""
	a.userTurnMu.Unlock()
	a.notifyUserTurnUpdated()

	turnDetection := a.turnDetectionMode()
	if turnDetection != TurnDetectionModeManual && turnDetection != TurnDetectionModeRealtimeLLM {
		a.interruptByAudioActivity("final transcript", "transcript", transcript)
	}
	if turnDetection == TurnDetectionModeSTT {
		a.runEOUDetection(EndOfTurnInfo{
			NewTranscript:        transcript,
			Language:             language,
			TranscriptConfidence: confidence,
			AudioFrames:          a.userAudioSnapshot(),
		})
	} else if a.vadBasedTurnDetection() && !a.speaking {
		a.runEOUDetection(EndOfTurnInfo{
			NewTranscript:        transcript,
			Language:             language,
			TranscriptConfidence: confidence,
			AudioFrames:          a.userAudioSnapshot(),
		})
	}
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

func (a *AgentActivity) interruptByAudioActivity(reason string, key string, value any) {
	if a == nil {
		return
	}
	if a.Session != nil && a.Session.aecWarmupActive() {
		return
	}
	go func() {
		if err := a.Interrupt(false); err != nil {
			logger.Logger.Warnw("failed to interrupt speech for "+reason, err, key, value)
		}
	}()
}

func (a *AgentActivity) ClearUserTurn() {
	a.eouMu.Lock()
	if a.eouCancel != nil {
		a.eouCancel()
		a.eouCancel = nil
	}
	a.eouMu.Unlock()

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
	}

	a.sttEOSReceived = false
	a.speaking = false
}

func (a *AgentActivity) CommitUserTurn(ctx context.Context, opts CommitUserTurnOptions) (string, error) {
	a.eouMu.Lock()
	if a.eouCancel != nil {
		a.eouCancel()
		a.eouCancel = nil
	}
	a.eouMu.Unlock()

	if ctx == nil {
		ctx = a.ctx
	}
	if a.Session != nil {
		a.Session.mu.Lock()
		assistant := a.Session.Assistant
		activity := a.Session.activity
		a.Session.mu.Unlock()
		if committer, ok := assistant.(realtimeAudioCommitter); ok {
			if err := committer.CommitAudio(); err != nil {
				return "", err
			}
			if !opts.SkipReply && activity == a {
				if _, err := a.Session.GenerateReplyWithOptions(ctx, GenerateReplyOptions{}); err != nil {
					return "", err
				}
			}
			opts.SkipReply = true
		}
	}
	if opts.TranscriptTimeout > 0 {
		deadline := time.NewTimer(opts.TranscriptTimeout)
		defer deadline.Stop()
		for {
			a.userTurnMu.Lock()
			present := a.pendingUserTranscriptPresent
			hasInterim := a.pendingInterimTranscript != ""
			ch := a.userTurnUpdatedCh
			a.userTurnMu.Unlock()
			if present || !hasInterim {
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

collect:
	a.userTurnMu.Lock()
	transcript := a.pendingUserTranscript
	language := a.pendingUserLanguage
	confidence := a.pendingTranscriptConfidence
	present := a.pendingUserTranscriptPresent
	fallbackLanguage := ""
	fallbackSpeakerID := ""
	fallbackFinal := false
	if !present && a.pendingInterimTranscript != "" {
		transcript = a.pendingInterimTranscript
		confidence = 1
		fallbackLanguage = a.pendingInterimLanguage
		fallbackSpeakerID = a.pendingInterimSpeakerID
		present = true
		fallbackFinal = true
	}
	a.pendingUserTranscript = ""
	a.pendingUserLanguage = ""
	a.pendingTranscriptConfidence = 0
	a.pendingUserTranscriptPresent = false
	a.pendingInterimTranscript = ""
	a.pendingInterimLanguage = ""
	a.pendingInterimSpeakerID = ""
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
			Transcript: transcript,
			IsFinal:    true,
			SpeakerID:  fallbackSpeakerID,
		})
	}
	if _, err := a.completeUserTurn(ctx, EndOfTurnInfo{
		SkipReply:            opts.SkipReply,
		NewTranscript:        transcript,
		Language:             firstNonEmpty(language, fallbackLanguage),
		TranscriptConfidence: confidence,
		AudioFrames:          a.userAudioSnapshot(),
	}); err != nil {
		return transcript, err
	}
	return transcript, nil
}

func (a *AgentActivity) completeUserTurn(ctx context.Context, info EndOfTurnInfo) (*SpeechHandle, error) {
	a.userTurnCompletionMu.Lock()
	defer a.userTurnCompletionMu.Unlock()

	if rejectsZeroConfidenceTranscript(info.NewTranscript, info.TranscriptConfidence) {
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
	if info.SkipReply {
		a.commitUserMessage(newMsg)
		return nil, nil
	}
	a.queueMu.Lock()
	currentSpeech := a.currentSpeech
	schedulingPaused := a.schedulingPaused || a.schedulingDraining
	a.queueMu.Unlock()
	if currentSpeech != nil && !currentSpeech.AllowInterruptions && !currentSpeech.IsInterrupted() && !currentSpeech.IsDone() {
		logger.Logger.Warnw("skipping reply to user input, current speech generation cannot be interrupted", nil, "userInput", info.NewTranscript)
		return nil, nil
	}
	if a.shouldSkipShortInterruption(currentSpeech, info.NewTranscript) {
		return nil, nil
	}
	if currentSpeech != nil && !currentSpeech.IsInterrupted() && !currentSpeech.IsDone() {
		if err := currentSpeech.Interrupt(false); err != nil {
			return nil, err
		}
		if err := currentSpeech.Wait(ctx); err != nil {
			return nil, err
		}
	}
	if schedulingPaused {
		logger.Logger.Warnw("skipping on_user_turn_completed, speech scheduling is paused", nil, "userInput", info.NewTranscript)
		return nil, nil
	}

	chatCtx := a.RetrieveChatCtx().Copy()
	hookStart := time.Now()
	if err := a.AgentIntf.OnUserTurnCompleted(ctx, chatCtx, newMsg); err != nil {
		var stopResponse llm.StopResponse
		if errors.As(err, &stopResponse) {
			return nil, nil
		}
		logger.Logger.Errorw("error occurred during on_user_turn_completed", err)
		return nil, nil
	}
	hookDelay := time.Since(hookStart).Seconds()
	newMsg.Metrics = metricsReportFromEndOfTurn(info, hookDelay)
	if a.Agent.LLM == nil || a.Session == nil {
		return nil, nil
	}
	a.queueMu.Lock()
	schedulingPaused = a.schedulingPaused || a.schedulingDraining
	a.queueMu.Unlock()
	if schedulingPaused {
		logger.Logger.Warnw("skipping reply to user input, speech scheduling is paused", nil, "userInput", info.NewTranscript)
		return nil, nil
	}
	handle, err := a.Session.GenerateReplyWithOptions(ctx, GenerateReplyOptions{
		UserMessage:   newMsg,
		ChatCtx:       chatCtx,
		InputModality: "audio",
	})
	if err != nil {
		return nil, err
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
	return handle, nil
}

func (a *AgentActivity) shouldSkipShortInterruption(currentSpeech *SpeechHandle, transcript string) bool {
	if currentSpeech == nil || !currentSpeech.AllowInterruptions || currentSpeech.IsInterrupted() || currentSpeech.IsDone() {
		return false
	}
	if !a.InterruptionEnabled() {
		return false
	}
	if a.Session == nil || a.Session.Options.MinInterruptionWords <= 0 {
		return false
	}
	if a.Agent.STT == nil && a.Session.STT == nil {
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
	a.pendingUserTranscriptPresent = false
	a.pendingInterimTranscript = ""
	a.pendingInterimLanguage = ""
	a.pendingInterimSpeakerID = ""
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
	return EndOfTurnInfo{
		NewTranscript:        a.pendingUserTranscript,
		Language:             a.pendingUserLanguage,
		TranscriptConfidence: a.pendingTranscriptConfidence,
		AudioFrames:          a.userAudioSnapshot(),
	}
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
	switch TurnDetectionMode(mode) {
	case TurnDetectionModeSTT:
		if (a.Agent == nil || a.Agent.STT == nil) && (a.Session == nil || a.Session.STT == nil) {
			logger.Logger.Warnw("turn_detection is set to stt, but no STT model is provided", nil)
			return ""
		}
	case TurnDetectionModeVAD:
		if (a.Agent == nil || a.Agent.VAD == nil) && (a.Session == nil || a.Session.VAD == nil) {
			logger.Logger.Warnw("turn_detection is set to vad, but no VAD model is provided", nil)
			return ""
		}
	}
	return TurnDetectionMode(mode)
}

func (a *AgentActivity) hasVADModel() bool {
	return a != nil && ((a.Agent != nil && a.Agent.VAD != nil) || (a.Session != nil && a.Session.VAD != nil))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (a *AgentActivity) runEOUDetection(info EndOfTurnInfo) {
	a.eouMu.Lock()
	if a.eouCancel != nil {
		a.eouCancel()
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.eouCancel = cancel
	a.eouMu.Unlock()

	go func() {
		defer cancel()

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

		timer := time.NewTimer(time.Duration(endpointingDelay * float64(time.Second)))
		defer timer.Stop()

		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			a.clearPendingUserTurn()
			if strings.TrimSpace(info.NewTranscript) == "" {
				return
			}
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

func vadEventTimestamp(ev *vad.VADEvent) float64 {
	if ev == nil {
		return float64(time.Now().UnixNano()) / float64(time.Second)
	}
	return ev.Timestamp
}
