package agent

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/logger"
)

var ErrSpeechSchedulingPaused = errors.New("speech scheduling is paused")

const agentInstructionsMessageID = "lk.agent_task.instructions"

type EndOfTurnInfo struct {
	SkipReply            bool
	NewTranscript        string
	TranscriptConfidence float64
	StartedSpeakingAt    *float64
	StoppedSpeakingAt    *float64
}

// AgentActivity handles the internal event loops, I/O processing, and
// speech generation queue for an Agent.
type AgentActivity struct {
	AgentIntf AgentInterface
	Agent     *Agent
	Session   *AgentSession

	currentSpeech  *SpeechHandle
	speechQueue    []scheduledSpeech
	nextSpeechSeq  uint64
	queueMu        sync.Mutex
	queueUpdatedCh chan struct{}

	schedulingPaused bool

	sttEOSReceived bool
	speaking       bool

	ctx    context.Context
	cancel context.CancelFunc

	eouMu     sync.Mutex
	eouCancel context.CancelFunc
}

func NewAgentActivity(agentIntf AgentInterface, session *AgentSession) *AgentActivity {
	ctx, cancel := context.WithCancel(context.Background())
	activity := &AgentActivity{
		AgentIntf:      agentIntf,
		Agent:          agentIntf.GetAgent(),
		Session:        session,
		speechQueue:    make([]scheduledSpeech, 0),
		queueUpdatedCh: make(chan struct{}, 1),
		ctx:            ctx,
		cancel:         cancel,
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
	_ = a.recordInitialConfiguration()
	a.AgentIntf.OnEnter()
	go a.schedulingTask()
}

func (a *AgentActivity) Stop() {
	a.AgentIntf.OnExit()
	a.cancel()
	if a.Agent.activity == a {
		a.Agent.activity = nil
	}
}

func (a *AgentActivity) recordInitialConfiguration() error {
	if a.Agent.ChatCtx == nil {
		a.Agent.ChatCtx = llm.NewChatContext()
	}
	if a.Session != nil && a.Session.ChatCtx == nil {
		a.Session.ChatCtx = llm.NewChatContext()
	}

	if err := updateAgentInstructionsMessage(a.Agent.ChatCtx, a.Agent.Instructions, a.Agent.Instructions != ""); err != nil {
		return err
	}

	toolNames := sortedAgentToolNames(a.chatContextTools())
	if a.Agent.Instructions == "" && len(toolNames) == 0 {
		return nil
	}

	configUpdate := &llm.AgentConfigUpdate{
		Instructions: stringPtrIfNotEmpty(a.Agent.Instructions),
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

	for _, speech := range interrupted {
		if err := speech.Wait(a.ctx); err != nil {
			return err
		}
	}

	return nil
}

func (a *AgentActivity) WaitForInactive(ctx context.Context) error {
	for {
		active := a.activeSpeechHandles()
		if len(active) == 0 {
			return nil
		}
		for _, speech := range active {
			if err := speech.Wait(ctx); err != nil {
				return err
			}
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

func (a *AgentActivity) UpdateInstructions(ctx context.Context, instructions string) error {
	a.Agent.Instructions = instructions
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
	if err := updateAgentInstructionsMessage(a.Agent.ChatCtx, instructions, true); err != nil {
		return err
	}
	return nil
}

func (a *AgentActivity) UpdateTools(ctx context.Context, tools []llm.Tool) error {
	oldToolNames := agentToolNameSet(a.Agent.Tools)
	newToolNames := agentToolNameSet(tools)
	toolsAdded, toolsRemoved := agentToolDiff(oldToolNames, newToolNames)

	a.Agent.Tools = dedupeAgentToolsByID(tools)
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
	return a.UpdateChatContext(ctx, a.Agent.ChatCtx)
}

func (a *AgentActivity) UpdateChatContext(ctx context.Context, chatCtx *llm.ChatContext, excludeInvalidFunctionCalls ...bool) error {
	excludeInvalid := true
	if len(excludeInvalidFunctionCalls) > 0 {
		excludeInvalid = excludeInvalidFunctionCalls[0]
	}
	if chatCtx == nil {
		a.Agent.ChatCtx = llm.NewChatContext()
		return updateAgentInstructionsMessage(a.Agent.ChatCtx, a.Agent.Instructions, true)
	}
	if !excludeInvalid {
		a.Agent.ChatCtx = chatCtx.Copy()
		return updateAgentInstructionsMessage(a.Agent.ChatCtx, a.Agent.Instructions, true)
	}
	a.Agent.ChatCtx = chatCtx.Copy(llm.ChatContextCopyOptions{
		Tools: a.chatContextTools(),
	})
	if err := updateAgentInstructionsMessage(a.Agent.ChatCtx, a.Agent.Instructions, true); err != nil {
		return err
	}
	return nil
}

func (a *AgentActivity) chatContextTools() []interface{} {
	tools := make([]interface{}, 0, len(a.Agent.Tools))
	if a.Session != nil {
		for _, tool := range a.Session.Tools {
			tools = append(tools, tool)
		}
	}
	for _, tool := range a.Agent.Tools {
		tools = append(tools, tool)
	}
	return tools
}

func agentToolNameSet(tools []llm.Tool) map[string]struct{} {
	names := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		names[tool.Name()] = struct{}{}
	}
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

func stringPtrIfNotEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func updateAgentInstructionsMessage(chatCtx *llm.ChatContext, instructions string, addIfMissing bool) error {
	if chatCtx == nil {
		return nil
	}
	idx := chatCtx.IndexByID(agentInstructionsMessageID)
	if idx != nil {
		existing, ok := chatCtx.Items[*idx].(*llm.ChatMessage)
		if !ok {
			return errors.New("expected instructions chat item to be a message")
		}
		chatCtx.Items[*idx] = &llm.ChatMessage{
			ID:        agentInstructionsMessageID,
			Role:      llm.ChatRoleSystem,
			Content:   []llm.ChatContent{{Text: instructions}},
			CreatedAt: existing.CreatedAt,
		}
		return nil
	}
	if addIfMissing {
		msg := &llm.ChatMessage{
			ID:      agentInstructionsMessageID,
			Role:    llm.ChatRoleSystem,
			Content: []llm.ChatContent{{Text: instructions}},
		}
		chatCtx.Items = append([]llm.ChatItem{msg}, chatCtx.Items...)
	}
	return nil
}

func (a *AgentActivity) ScheduleSpeech(speech *SpeechHandle, priority int, force bool) error {
	a.queueMu.Lock()
	defer a.queueMu.Unlock()

	if a.schedulingPaused && !force {
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
	defer a.queueMu.Unlock()

	if len(a.speechQueue) == 0 || a.schedulingPaused || a.currentSpeech != nil {
		return
	}

	nextIdx := a.nextSpeechIndexLocked()
	speech := a.speechQueue[nextIdx].speech
	a.speechQueue = append(a.speechQueue[:nextIdx], a.speechQueue[nextIdx+1:]...)

	if speech.IsDone() {
		return
	}

	a.currentSpeech = speech

	// Run speech completion asynchronously
	go func() {
		// Wait for generation to finish or be interrupted
		<-speech.doneCh

		a.queueMu.Lock()
		a.currentSpeech = nil
		a.queueMu.Unlock()

		// Trigger next
		select {
		case a.queueUpdatedCh <- struct{}{}:
		default:
		}
	}()
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

// Event callbacks from RecognitionHooks
func (a *AgentActivity) OnStartOfSpeech(ev *vad.VADEvent) {
	a.speaking = true
	a.sttEOSReceived = false
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
	logger.Logger.Infow("End of speech detected")

	if a.turnDetectionMode() == TurnDetectionModeVAD {
		// Trigger EOU detection
		a.runEOUDetection(EndOfTurnInfo{})
	}
}

func (a *AgentActivity) OnFinalTranscript(ev *stt.SpeechEvent) {
	a.sttEOSReceived = true
	if a.turnDetectionMode() == TurnDetectionModeSTT {
		transcript := ""
		confidence := 0.0
		if len(ev.Alternatives) > 0 {
			transcript = ev.Alternatives[0].Text
			confidence = ev.Alternatives[0].Confidence
		}
		a.runEOUDetection(EndOfTurnInfo{
			NewTranscript:        transcript,
			TranscriptConfidence: confidence,
		})
	}
}

func (a *AgentActivity) turnDetectionMode() TurnDetectionMode {
	if a.Agent.TurnDetection != "" {
		return a.Agent.TurnDetection
	}
	if a.Session != nil {
		return a.Session.Options.TurnDetection
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

		if a.Agent.TurnDetector != nil && info.NewTranscript != "" {
			// Predict end of turn
			chatCtx := a.Agent.ChatCtx.Copy()
			chatCtx.Append(&llm.ChatMessage{
				Role:    llm.ChatRoleUser,
				Content: []llm.ChatContent{{Text: info.NewTranscript}},
			})

			prob, err := a.Agent.TurnDetector.PredictEndOfTurn(ctx, chatCtx)
			if err == nil {
				logger.Logger.Infow("EOU prediction", "probability", prob)
				// Apply probability threshold logic
				if prob < 0.5 {
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
			// EOU detected
			logger.Logger.Infow("EOU detected, completing user turn")
			newMsg := &llm.ChatMessage{
				Role:    llm.ChatRoleUser,
				Content: []llm.ChatContent{{Text: info.NewTranscript}},
			}
			a.AgentIntf.OnUserTurnCompleted(a.ctx, a.Agent.ChatCtx, newMsg)
		}
	}()
}

func (a *AgentActivity) minEndpointingDelay() float64 {
	if a.Agent.MinEndpointingDelay > 0 {
		return a.Agent.MinEndpointingDelay
	}
	if a.Session != nil && a.Session.Options.MinEndpointingDelay > 0 {
		return a.Session.Options.MinEndpointingDelay
	}
	return 0.5
}

func (a *AgentActivity) maxEndpointingDelay() float64 {
	if a.Agent.MaxEndpointingDelay > 0 {
		return a.Agent.MaxEndpointingDelay
	}
	if a.Session != nil && a.Session.Options.MaxEndpointingDelay > 0 {
		return a.Session.Options.MaxEndpointingDelay
	}
	return 3.0
}
