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
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/library/tokenize"
)

var ErrSpeechSchedulingPaused = errors.New("speech scheduling is paused")

const agentInstructionsMessageID = "lk.agent_task.instructions"

type instructionUpdatingAssistant interface {
	UpdateInstructions(context.Context, string) error
}

type toolUpdatingAssistant interface {
	UpdateTools(context.Context) error
}

type chatContextUpdatingAssistant interface {
	UpdateChatContext(context.Context, *llm.ChatContext) error
}

type EndOfTurnInfo struct {
	SkipReply            bool
	NewTranscript        string
	TranscriptConfidence float64
	EndOfTurnDelay       float64
	TranscriptionDelay   float64
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
	lastSpeechDone time.Time
	queueMu        sync.Mutex
	queueUpdatedCh chan struct{}

	schedulingPaused   bool
	schedulingDraining bool

	sttEOSReceived bool
	speaking       bool

	userTurnMu                   sync.Mutex
	userTurnUpdatedCh            chan struct{}
	pendingInterimTranscript     string
	pendingInterimLanguage       string
	pendingInterimSpeakerID      string
	userTurnCompletionMu         sync.Mutex
	pendingUserTranscript        string
	pendingTranscriptConfidence  float64
	pendingUserTranscriptPresent bool

	ctx    context.Context
	cancel context.CancelFunc

	eouMu     sync.Mutex
	eouCancel context.CancelFunc

	userTurnExceededMu     sync.Mutex
	userTurnExceededLocked bool
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
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case ev := <-a.Session.AgentStateChangedCh:
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
	if a.Session != nil {
		if updater, ok := a.Session.Assistant.(instructionUpdatingAssistant); ok {
			return updater.UpdateInstructions(ctx, instructions)
		}
	}
	return nil
}

func (a *AgentActivity) UpdateTools(ctx context.Context, tools []llm.Tool) error {
	oldToolCtx := llm.EmptyToolContext()
	if err := oldToolCtx.UpdateTools(agentToolsAsInterfaces(a.Agent.Tools)); err != nil {
		return err
	}
	dedupedTools := dedupeAgentToolsByID(tools)
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
	excludeInvalid := true
	if len(excludeInvalidFunctionCalls) > 0 {
		excludeInvalid = excludeInvalidFunctionCalls[0]
	}
	if chatCtx == nil {
		a.Agent.ChatCtx = llm.NewChatContext()
		if err := updateAgentInstructionsMessage(a.Agent.ChatCtx, a.Agent.Instructions, true); err != nil {
			return err
		}
		return a.updateRealtimeChatContext(ctx)
	}
	if !excludeInvalid {
		a.Agent.ChatCtx = chatCtx.Copy()
		if err := updateAgentInstructionsMessage(a.Agent.ChatCtx, a.Agent.Instructions, true); err != nil {
			return err
		}
		return a.updateRealtimeChatContext(ctx)
	}
	a.Agent.ChatCtx = chatCtx.Copy(llm.ChatContextCopyOptions{
		Tools: a.chatContextTools(),
	})
	if err := updateAgentInstructionsMessage(a.Agent.ChatCtx, a.Agent.Instructions, true); err != nil {
		return err
	}
	if err := a.updateRealtimeChatContext(ctx); err != nil {
		return err
	}
	return nil
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
	defer a.queueMu.Unlock()

	if a.currentSpeech != nil && a.currentSpeech.IsDone() {
		a.currentSpeech = nil
	}
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
	delay := a.minConsecutiveSpeechDelay()
	if delay > 0 && !a.lastSpeechDone.IsZero() {
		delay -= time.Since(a.lastSpeechDone)
	}
	if a.Session != nil {
		if assistant, ok := a.Session.Assistant.(scheduledSpeechAssistant); ok {
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
	}

	// Run speech completion asynchronously
	go func() {
		// Wait for generation to finish or be interrupted
		<-speech.doneCh

		a.queueMu.Lock()
		a.currentSpeech = nil
		a.lastSpeechDone = time.Now()
		a.queueMu.Unlock()

		// Trigger next
		select {
		case a.queueUpdatedCh <- struct{}{}:
		default:
		}
	}()
}

func (a *AgentActivity) minConsecutiveSpeechDelay() time.Duration {
	if a.Agent != nil && a.Agent.MinConsecutiveSpeechDelay > 0 {
		return time.Duration(a.Agent.MinConsecutiveSpeechDelay * float64(time.Second))
	}
	if a.Session != nil && a.Session.Options.MinConsecutiveSpeechDelay > 0 {
		return time.Duration(a.Session.Options.MinConsecutiveSpeechDelay * float64(time.Second))
	}
	return 0
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
	a.schedulingPaused = true
	a.schedulingDraining = false
	a.queueMu.Unlock()

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
	if endpointing := a.endpointing(); endpointing != nil {
		endpointing.OnEndOfSpeech(vadEventTimestamp(ev), false)
	}
	logger.Logger.Infow("End of speech detected")

	if a.turnDetectionMode() == TurnDetectionModeVAD {
		// Trigger EOU detection
		a.runEOUDetection(EndOfTurnInfo{})
	}
}

func (a *AgentActivity) OnInterimTranscript(ev *stt.SpeechEvent) {
	if a.Session == nil {
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
}

func (a *AgentActivity) OnFinalTranscript(ev *stt.SpeechEvent) {
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
	a.pendingTranscriptConfidence = confidence
	a.pendingUserTranscriptPresent = true
	a.pendingInterimTranscript = ""
	a.pendingInterimLanguage = ""
	a.pendingInterimSpeakerID = ""
	a.userTurnMu.Unlock()
	a.notifyUserTurnUpdated()

	if a.turnDetectionMode() == TurnDetectionModeSTT {
		a.runEOUDetection(EndOfTurnInfo{
			NewTranscript:        transcript,
			TranscriptConfidence: confidence,
		})
	}
}

func (a *AgentActivity) ClearUserTurn() {
	a.eouMu.Lock()
	if a.eouCancel != nil {
		a.eouCancel()
		a.eouCancel = nil
	}
	a.eouMu.Unlock()

	a.clearPendingUserTurn()

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
	confidence := a.pendingTranscriptConfidence
	present := a.pendingUserTranscriptPresent
	fallbackLanguage := ""
	fallbackSpeakerID := ""
	fallbackFinal := false
	if !present && a.pendingInterimTranscript != "" {
		transcript = a.pendingInterimTranscript
		fallbackLanguage = a.pendingInterimLanguage
		fallbackSpeakerID = a.pendingInterimSpeakerID
		present = true
		fallbackFinal = true
	}
	a.pendingUserTranscript = ""
	a.pendingTranscriptConfidence = 0
	a.pendingUserTranscriptPresent = false
	a.pendingInterimTranscript = ""
	a.pendingInterimLanguage = ""
	a.pendingInterimSpeakerID = ""
	a.userTurnMu.Unlock()

	if !present || transcript == "" {
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
		TranscriptConfidence: confidence,
	}); err != nil {
		return transcript, err
	}
	return transcript, nil
}

func (a *AgentActivity) completeUserTurn(ctx context.Context, info EndOfTurnInfo) (*SpeechHandle, error) {
	a.userTurnCompletionMu.Lock()
	defer a.userTurnCompletionMu.Unlock()

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

	chatCtx := llm.NewChatContext()
	if a.Agent.ChatCtx != nil {
		chatCtx = a.Agent.ChatCtx.Copy()
	}
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
	if a.Session == nil || a.Session.Options.MinInterruptionWords <= 0 {
		return false
	}
	if a.turnDetectionMode() == TurnDetectionModeManual {
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
			a.clearPendingUserTurn()
			if _, err := a.completeUserTurn(a.ctx, info); err != nil {
				logger.Logger.Errorw("user turn completion failed", err)
			}
		}
	}()
}

func (a *AgentActivity) minEndpointingDelay() float64 {
	if a.Agent.MinEndpointingDelay > 0 {
		return a.Agent.MinEndpointingDelay
	}
	if endpointing := a.endpointing(); endpointing != nil {
		return endpointing.MinDelay()
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
	if endpointing := a.endpointing(); endpointing != nil {
		return endpointing.MaxDelay()
	}
	if a.Session != nil && a.Session.Options.MaxEndpointingDelay > 0 {
		return a.Session.Options.MaxEndpointingDelay
	}
	return 3.0
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
