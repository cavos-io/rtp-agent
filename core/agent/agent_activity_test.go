package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/library/tokenize"
)

func TestAgentActivityScheduleSpeechProcessesHighestPriorityFirst(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	low := NewSpeechHandle(true, DefaultInputDetails())
	high := NewSpeechHandle(true, DefaultInputDetails())
	normal := NewSpeechHandle(true, DefaultInputDetails())

	if err := activity.ScheduleSpeech(low, SpeechPriorityLow, false); err != nil {
		t.Fatalf("ScheduleSpeech low error = %v, want nil", err)
	}
	if err := activity.ScheduleSpeech(high, SpeechPriorityHigh, false); err != nil {
		t.Fatalf("ScheduleSpeech high error = %v, want nil", err)
	}
	if err := activity.ScheduleSpeech(normal, SpeechPriorityNormal, false); err != nil {
		t.Fatalf("ScheduleSpeech normal error = %v, want nil", err)
	}

	activity.processQueue()

	if activity.currentSpeech != high {
		t.Fatalf("currentSpeech = %p, want high priority speech %p", activity.currentSpeech, high)
	}
	activity.currentSpeech.MarkDone()
	waitForNoCurrentSpeech(t, activity)
	activity.processQueue()
	if activity.currentSpeech != normal {
		t.Fatalf("currentSpeech = %p, want normal priority speech %p", activity.currentSpeech, normal)
	}
	activity.currentSpeech.MarkDone()
	waitForNoCurrentSpeech(t, activity)
	activity.processQueue()
	if activity.currentSpeech != low {
		t.Fatalf("currentSpeech = %p, want low priority speech %p", activity.currentSpeech, low)
	}
}

func TestAgentActivityScheduleSpeechPreservesFIFOWithinPriority(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	first := NewSpeechHandle(true, DefaultInputDetails())
	second := NewSpeechHandle(true, DefaultInputDetails())

	if err := activity.ScheduleSpeech(first, SpeechPriorityNormal, false); err != nil {
		t.Fatalf("ScheduleSpeech first error = %v, want nil", err)
	}
	if err := activity.ScheduleSpeech(second, SpeechPriorityNormal, false); err != nil {
		t.Fatalf("ScheduleSpeech second error = %v, want nil", err)
	}

	activity.processQueue()

	if activity.currentSpeech != first {
		t.Fatalf("currentSpeech = %p, want first speech %p", activity.currentSpeech, first)
	}
	activity.currentSpeech.MarkDone()
	waitForNoCurrentSpeech(t, activity)
	activity.processQueue()
	if activity.currentSpeech != second {
		t.Fatalf("currentSpeech = %p, want second speech %p", activity.currentSpeech, second)
	}
}

func TestAgentActivityRespectsMinConsecutiveSpeechDelay(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		MinConsecutiveSpeechDelay: 0.08,
	})
	assistant := &recordingScheduledSpeechAssistant{scheduledCh: make(chan *SpeechHandle, 10)}
	session.Assistant = assistant
	activity := NewAgentActivity(agent, session)

	first := NewSpeechHandle(true, DefaultInputDetails())
	second := NewSpeechHandle(true, DefaultInputDetails())
	if err := activity.ScheduleSpeech(first, SpeechPriorityNormal, false); err != nil {
		t.Fatalf("ScheduleSpeech first error = %v, want nil", err)
	}
	activity.processQueue()
	if got := receiveScheduledSpeech(t, assistant); got != first {
		t.Fatalf("scheduled speech = %p, want first %p", got, first)
	}
	first.MarkDone()
	waitForNoCurrentSpeech(t, activity)

	if err := activity.ScheduleSpeech(second, SpeechPriorityNormal, false); err != nil {
		t.Fatalf("ScheduleSpeech second error = %v, want nil", err)
	}
	activity.processQueue()

	select {
	case got := <-assistant.scheduledCh:
		t.Fatalf("scheduled speech = %p before min consecutive delay elapsed, want none", got)
	case <-time.After(20 * time.Millisecond):
	}

	if got := receiveScheduledSpeech(t, assistant); got != second {
		t.Fatalf("scheduled speech = %p, want second %p", got, second)
	}
	second.MarkDone()
}

func TestAgentActivityScheduleSpeechRejectsNonForcedSpeechWhilePaused(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	activity.schedulingPaused = true
	speech := NewSpeechHandle(true, DefaultInputDetails())

	err := activity.ScheduleSpeech(speech, SpeechPriorityNormal, false)

	if !errors.Is(err, ErrSpeechSchedulingPaused) {
		t.Fatalf("ScheduleSpeech error = %v, want ErrSpeechSchedulingPaused", err)
	}
	if !speech.IsInterrupted() {
		t.Fatal("speech was not interrupted after rejected schedule")
	}
	if speech.IsScheduled() {
		t.Fatal("speech was marked scheduled after rejected schedule")
	}
}

func TestAgentActivitySchedulingPausedReportsState(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	if activity.SchedulingPaused() {
		t.Fatal("SchedulingPaused() = true, want false")
	}
	activity.schedulingPaused = true
	if !activity.SchedulingPaused() {
		t.Fatal("SchedulingPaused() = false after pause, want true")
	}
}

func TestAgentActivityCurrentSpeechReportsActiveSpeech(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	if got := activity.CurrentSpeech(); got != nil {
		t.Fatalf("CurrentSpeech() = %#v, want nil before speech is active", got)
	}

	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	if got := activity.CurrentSpeech(); got != current {
		t.Fatalf("CurrentSpeech() = %#v, want active speech %#v", got, current)
	}
}

func TestAgentActivityToolsCombinesSessionAndAgentTools(t *testing.T) {
	agentTool := &agentTestTool{id: "agent", name: "agent"}
	sessionTool := &agentTestTool{id: "session", name: "session"}
	agent := NewAgent("test")
	agent.Tools = []llm.Tool{agentTool}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Tools = []llm.Tool{sessionTool}
	activity := NewAgentActivity(agent, session)

	got := activity.Tools()
	if len(got) != 2 || got[0] != sessionTool || got[1] != agentTool {
		t.Fatalf("Tools() = %#v, want session tool then agent tool", got)
	}

	got[0] = agentTool
	if session.Tools[0] != sessionTool {
		t.Fatal("mutating Tools() result changed session tools")
	}
}

func TestAgentActivityMinConsecutiveSpeechDelayUsesAgentOverride(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{MinConsecutiveSpeechDelay: 0.25})
	activity := NewAgentActivity(agent, session)

	if got := activity.MinConsecutiveSpeechDelay(); got != 250*time.Millisecond {
		t.Fatalf("MinConsecutiveSpeechDelay() = %v, want 250ms session default", got)
	}

	agent.MinConsecutiveSpeechDelay = 0.75
	if got := activity.MinConsecutiveSpeechDelay(); got != 750*time.Millisecond {
		t.Fatalf("MinConsecutiveSpeechDelay() = %v, want 750ms agent override", got)
	}
}

func TestAgentActivityOnPipelineReplyDoneReturnsToListeningWhenInactive(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	session.UpdateAgentState(AgentStateSpeaking)
	current.MarkDone()

	activity.OnPipelineReplyDone(current)

	if got := session.AgentState(); got != AgentStateListening {
		t.Fatalf("AgentState() = %q, want %q", got, AgentStateListening)
	}
}

func TestAgentActivityUseTTSAlignedTranscriptUsesAgentOverride(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{UseTTSAlignedTranscript: true})
	activity := NewAgentActivity(agent, session)

	if !activity.UseTTSAlignedTranscript() {
		t.Fatal("UseTTSAlignedTranscript() = false, want session default true")
	}

	agent.UseTTSAlignedTranscript = false
	agent.UseTTSAlignedTranscriptSet = true
	if activity.UseTTSAlignedTranscript() {
		t.Fatal("UseTTSAlignedTranscript() = true, want explicit agent override false")
	}

	agent.UseTTSAlignedTranscript = true
	session.Options.UseTTSAlignedTranscript = false
	if !activity.UseTTSAlignedTranscript() {
		t.Fatal("UseTTSAlignedTranscript() = false, want explicit agent override true")
	}
}

func TestAgentActivityAllowInterruptionsUsesAgentOverride(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	if !activity.AllowInterruptions() {
		t.Fatal("AllowInterruptions() = false, want session default true")
	}

	agent.AllowInterruptions = false
	agent.AllowInterruptionsSet = true
	if activity.AllowInterruptions() {
		t.Fatal("AllowInterruptions() = true, want explicit agent override false")
	}

	agent.AllowInterruptions = true
	if !activity.AllowInterruptions() {
		t.Fatal("AllowInterruptions() = false, want explicit agent override true")
	}
}

func TestAgentActivityInterruptionEnabledRequiresDetectionModeAndAllowance(t *testing.T) {
	agent := NewAgent("test")
	agent.STT = &fakePipelineSTT{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{TurnDetection: TurnDetectionModeSTT})
	activity := NewAgentActivity(agent, session)

	if !activity.InterruptionEnabled() {
		t.Fatal("InterruptionEnabled() = false, want true with STT turn detection and interruptions allowed")
	}

	agent.AllowInterruptions = false
	agent.AllowInterruptionsSet = true
	if activity.InterruptionEnabled() {
		t.Fatal("InterruptionEnabled() = true, want false when agent disables interruptions")
	}

	agent.AllowInterruptions = true
	session.Options.TurnDetection = TurnDetectionModeManual
	if activity.InterruptionEnabled() {
		t.Fatal("InterruptionEnabled() = true, want false for manual turn detection")
	}

	session.Options.TurnDetection = TurnDetectionModeRealtimeLLM
	if activity.InterruptionEnabled() {
		t.Fatal("InterruptionEnabled() = true, want false for realtime LLM turn detection")
	}
}

func TestAgentActivityScheduleSpeechAllowsForcedSpeechWhilePaused(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	activity.schedulingPaused = true
	speech := NewSpeechHandle(true, DefaultInputDetails())

	if err := activity.ScheduleSpeech(speech, SpeechPriorityNormal, true); err != nil {
		t.Fatalf("ScheduleSpeech forced error = %v, want nil", err)
	}

	if !speech.IsScheduled() {
		t.Fatal("forced speech was not marked scheduled")
	}
	if speech.IsInterrupted() {
		t.Fatal("forced speech was interrupted")
	}
}

func TestAgentActivityInterruptInterruptsCurrentAndQueuedSpeech(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	current := NewSpeechHandle(true, DefaultInputDetails())
	queued := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	if err := activity.ScheduleSpeech(queued, SpeechPriorityNormal, false); err != nil {
		t.Fatalf("ScheduleSpeech queued error = %v, want nil", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- activity.Interrupt(false)
	}()

	waitForInterrupted(t, current)
	waitForInterrupted(t, queued)

	select {
	case err := <-done:
		t.Fatalf("Interrupt returned before speech handles were done: %v", err)
	case <-time.After(10 * time.Millisecond):
	}

	current.MarkDone()
	select {
	case err := <-done:
		t.Fatalf("Interrupt returned before queued speech was done: %v", err)
	case <-time.After(10 * time.Millisecond):
	}

	queued.MarkDone()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Interrupt error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Interrupt did not return after all interrupted speech handles were done")
	}
}

func TestAgentActivityInterruptReturnsImmediatelyWhenNoSpeech(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	done := make(chan error, 1)
	go func() {
		done <- activity.Interrupt(false)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Interrupt error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Interrupt did not return with no active or queued speech")
	}
}

func TestAgentActivityInterruptForceBypassesDisallowedInterruptions(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(false, DefaultInputDetails())
	activity.currentSpeech = current

	done := make(chan error, 1)
	go func() {
		done <- activity.Interrupt(true)
	}()

	waitForInterrupted(t, current)
	current.MarkDone()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Interrupt(force=true) error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Interrupt(force=true) did not return after speech was done")
	}
}

func TestAgentActivityInterruptReturnsDisallowedInterruptionError(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(false, DefaultInputDetails())
	activity.currentSpeech = current

	err := activity.Interrupt(false)

	if !errors.Is(err, ErrSpeechInterruptionsDisabled) {
		t.Fatalf("Interrupt error = %v, want ErrSpeechInterruptionsDisabled", err)
	}
	if current.IsInterrupted() {
		t.Fatal("current speech was interrupted despite disallowing interruptions")
	}
}

func TestAgentActivityDrainRejectsNewSpeechWhileQueuedSpeechFinishes(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	activity.Start()
	defer activity.Stop()

	current := NewSpeechHandle(true, DefaultInputDetails())
	queued := NewSpeechHandle(true, DefaultInputDetails())
	if err := activity.ScheduleSpeech(current, SpeechPriorityHigh, false); err != nil {
		t.Fatalf("ScheduleSpeech current error = %v, want nil", err)
	}
	if err := activity.ScheduleSpeech(queued, SpeechPriorityNormal, false); err != nil {
		t.Fatalf("ScheduleSpeech queued error = %v, want nil", err)
	}
	waitForCurrentSpeech(t, activity, current)

	done := make(chan error, 1)
	go func() {
		done <- activity.Drain(context.Background())
	}()

	waitForDraining(t, activity)
	rejected := NewSpeechHandle(true, DefaultInputDetails())
	err := activity.ScheduleSpeech(rejected, SpeechPriorityNormal, false)
	if !errors.Is(err, ErrSpeechSchedulingPaused) {
		t.Fatalf("ScheduleSpeech during drain error = %v, want ErrSpeechSchedulingPaused", err)
	}
	if !rejected.IsInterrupted() {
		t.Fatal("speech rejected during drain was not interrupted")
	}

	current.MarkDone()
	waitForCurrentSpeech(t, activity, queued)
	select {
	case err := <-done:
		t.Fatalf("Drain returned before queued speech completed: %v", err)
	case <-time.After(10 * time.Millisecond):
	}

	queued.MarkDone()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Drain error = %v, want nil", err)
		}
	case <-testTimeout():
		t.Fatal("Drain did not return after queued speech completed")
	}
	if !activity.schedulingPaused {
		t.Fatal("schedulingPaused = false after Drain, want true")
	}
}

func TestAgentActivityStartRecordsInitialConfiguration(t *testing.T) {
	agent := NewAgent("be helpful")
	agent.Tools = []llm.Tool{&agentTestTool{id: "lookup", name: "lookup"}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	activity.Start()
	defer activity.Stop()

	if len(agent.ChatCtx.Items) == 0 {
		t.Fatal("agent chat context has no initial items, want instructions and config")
	}
	msg, ok := agent.ChatCtx.Items[0].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("first agent chat item = %T, want instructions message", agent.ChatCtx.Items[0])
	}
	if msg.ID != agentInstructionsMessageID || msg.Role != llm.ChatRoleSystem || msg.TextContent() != "be helpful" {
		t.Fatalf("instructions message = %#v, want system message with initial instructions", msg)
	}

	config, ok := agent.ChatCtx.Items[len(agent.ChatCtx.Items)-1].(*llm.AgentConfigUpdate)
	if !ok {
		t.Fatalf("last agent chat item = %T, want config update", agent.ChatCtx.Items[len(agent.ChatCtx.Items)-1])
	}
	if config.Instructions == nil || *config.Instructions != "be helpful" {
		t.Fatalf("config instructions = %v, want be helpful", config.Instructions)
	}
	if !stringSlicesEqual(config.ToolsAdded, []string{"lookup"}) {
		t.Fatalf("config tools added = %q, want [lookup]", config.ToolsAdded)
	}
	if len(session.ChatCtx.Items) != 1 || session.ChatCtx.Items[0] != config {
		t.Fatalf("session chat context = %#v, want shared initial config update", session.ChatCtx.Items)
	}
}

func TestAgentActivityStartSkipsEmptyInitialConfiguration(t *testing.T) {
	agent := NewAgent("")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	activity.Start()
	defer activity.Stop()

	if len(agent.ChatCtx.Items) != 0 {
		t.Fatalf("agent chat context items = %#v, want no initial config for empty agent", agent.ChatCtx.Items)
	}
	if len(session.ChatCtx.Items) != 0 {
		t.Fatalf("session chat context items = %#v, want no initial config for empty agent", session.ChatCtx.Items)
	}
}

func TestAgentActivityRetrieveChatCtxReturnsReadOnlySnapshot(t *testing.T) {
	agent := NewAgent("")
	agent.ChatCtx.Append(&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser})
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	got := activity.RetrieveChatCtx()
	if got == nil {
		t.Fatal("RetrieveChatCtx returned nil, want chat context")
	}
	if !got.Readonly() {
		t.Fatal("RetrieveChatCtx returned mutable context, want read-only snapshot")
	}
	if got == agent.ChatCtx {
		t.Fatal("RetrieveChatCtx returned agent-owned context, want snapshot")
	}
	if len(got.Items) != 1 || got.Items[0].GetID() != "user" {
		t.Fatalf("RetrieveChatCtx items = %#v, want existing agent message", got.Items)
	}

	agent.ChatCtx = nil
	if empty := activity.RetrieveChatCtx(); empty == nil || !empty.Readonly() || len(empty.Items) != 0 {
		t.Fatalf("RetrieveChatCtx with nil agent context = %#v, want empty read-only context", empty)
	}
}

func TestAgentActivityUsesSessionMinEndpointingDelay(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeSTT
	session := NewAgentSession(agent, nil, AgentSessionOptions{MinEndpointingDelay: 0.01})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	activity.runEOUDetection(EndOfTurnInfo{NewTranscript: "hello"})

	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "hello" {
			t.Fatalf("turn message text = %q, want hello", msg.TextContent())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnUserTurnCompleted was not called after session min endpointing delay")
	}
}

func TestAgentActivityUsesSessionMaxEndpointingDelay(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeSTT
	agent.TurnDetector = turnDetectorFunc(func(context.Context, *llm.ChatContext) (float64, error) {
		return 0.1, nil
	})
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		MinEndpointingDelay: 0.01,
		MaxEndpointingDelay: 0.02,
	})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	activity.runEOUDetection(EndOfTurnInfo{NewTranscript: "still talking"})

	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "still talking" {
			t.Fatalf("turn message text = %q, want still talking", msg.TextContent())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnUserTurnCompleted was not called after session max endpointing delay")
	}
}

func TestAgentSessionUpdateOptionsAffectsActiveEndpointingDelay(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeSTT
	session := NewAgentSession(agent, nil, AgentSessionOptions{MinEndpointingDelay: 1})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	minDelay := 0.01
	session.UpdateOptions(AgentSessionUpdateOptions{MinEndpointingDelay: &minDelay})

	activity.runEOUDetection(EndOfTurnInfo{NewTranscript: "updated delay"})

	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "updated delay" {
			t.Fatalf("turn message text = %q, want updated delay", msg.TextContent())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnUserTurnCompleted was not called after updated session min endpointing delay")
	}
}

func TestAgentSessionUpdateOptionsAffectsActiveTurnDetection(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.STT = &fakePipelineSTT{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{MinEndpointingDelay: 0.01})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "ignored before update"}},
	})
	select {
	case msg := <-agent.turns:
		t.Fatalf("OnUserTurnCompleted called before session turn detection update with %q", msg.TextContent())
	case <-time.After(20 * time.Millisecond):
	}

	turnDetection := TurnDetectionModeSTT
	session.UpdateOptions(AgentSessionUpdateOptions{TurnDetection: &turnDetection})

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "after update", Confidence: 0.9}},
	})

	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "after update" {
			t.Fatalf("turn message text = %q, want after update", msg.TextContent())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnUserTurnCompleted was not called after session turn detection update")
	}
}

func TestAgentActivityIgnoresSTTTurnDetectionWithoutSTT(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeSTT
	session := NewAgentSession(agent, nil, AgentSessionOptions{MinEndpointingDelay: 0.01})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "missing stt should not complete", Confidence: 0.9}},
	})

	select {
	case msg := <-agent.turns:
		t.Fatalf("OnUserTurnCompleted called without STT configured with %q", msg.TextContent())
	case <-time.After(20 * time.Millisecond):
	}
}

func TestAgentActivityIgnoresVADTurnDetectionWithoutVAD(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeVAD
	session := NewAgentSession(agent, nil, AgentSessionOptions{MinEndpointingDelay: 0.01})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	activity.OnEndOfSpeech(nil)

	select {
	case msg := <-agent.turns:
		t.Fatalf("OnUserTurnCompleted called without VAD configured with %q", msg.TextContent())
	case <-time.After(20 * time.Millisecond):
	}
}

func TestAgentActivityOnFinalTranscriptEmitsUserInputTranscribed(t *testing.T) {
	agent := NewAgent("test")
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Language:  "en",
			Text:      "final transcript",
			SpeakerID: "speaker-1",
		}},
	})

	select {
	case ev := <-session.UserInputTranscribedEvents():
		if ev.GetType() != "user_input_transcribed" {
			t.Fatalf("event type = %q, want user_input_transcribed", ev.GetType())
		}
		if ev.Transcript != "final transcript" || !ev.IsFinal {
			t.Fatalf("event transcript/final = %q/%v, want final transcript/true", ev.Transcript, ev.IsFinal)
		}
		if ev.Language != "en" || ev.SpeakerID != "speaker-1" {
			t.Fatalf("event language/speaker = %q/%q, want en/speaker-1", ev.Language, ev.SpeakerID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("UserInputTranscribedEvents did not receive final transcript")
	}
}

func TestAgentActivityOnInterimTranscriptEmitsUserInputTranscribed(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	activity.OnInterimTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Language:  "en",
			Text:      "interim transcript",
			SpeakerID: "speaker-1",
		}},
	})

	select {
	case ev := <-session.UserInputTranscribedEvents():
		if ev.Transcript != "interim transcript" || ev.IsFinal {
			t.Fatalf("event transcript/final = %q/%v, want interim transcript/false", ev.Transcript, ev.IsFinal)
		}
		if ev.Language != "en" || ev.SpeakerID != "speaker-1" {
			t.Fatalf("event language/speaker = %q/%q, want en/speaker-1", ev.Language, ev.SpeakerID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("UserInputTranscribedEvents did not receive interim transcript")
	}
}

func TestAgentActivityClearUserTurnDropsPendingManualTranscript(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "discard me", Confidence: 0.8}},
	})
	activity.ClearUserTurn()

	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "" {
		t.Fatalf("CommitUserTurn transcript = %q, want empty after ClearUserTurn", transcript)
	}
	select {
	case msg := <-agent.turns:
		t.Fatalf("OnUserTurnCompleted called after cleared turn with %q", msg.TextContent())
	case <-time.After(20 * time.Millisecond):
	}
}

func TestAgentActivityCommitUserTurnCompletesPendingManualTranscript(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "manual turn", Confidence: 0.9}},
	})

	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "manual turn" {
		t.Fatalf("CommitUserTurn transcript = %q, want manual turn", transcript)
	}
	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "manual turn" {
			t.Fatalf("turn message text = %q, want manual turn", msg.TextContent())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnUserTurnCompleted was not called for manual commit")
	}
}

func TestAgentActivityCommitUserTurnFallsBackToInterimTranscriptAfterTimeout(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	activity.OnInterimTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Text:       "interim fallback",
			Language:   "en",
			Confidence: 0.4,
			SpeakerID:  "speaker-1",
		}},
	})
	<-session.UserInputTranscribedEvents()

	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{
		TranscriptTimeout: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "interim fallback" {
		t.Fatalf("CommitUserTurn transcript = %q, want interim fallback", transcript)
	}
	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "interim fallback" {
			t.Fatalf("turn message text = %q, want interim fallback", msg.TextContent())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnUserTurnCompleted was not called for interim fallback")
	}
	select {
	case ev := <-session.UserInputTranscribedEvents():
		if !ev.IsFinal || ev.Transcript != "interim fallback" || ev.Language != "en" || ev.SpeakerID != "speaker-1" {
			t.Fatalf("fallback final event = %#v, want final interim fallback/en/speaker-1", ev)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("UserInputTranscribedEvents did not receive fallback final transcript")
	}
}

func TestAgentActivityCommitUserTurnGeneratesReplyWhenLLMConfigured(t *testing.T) {
	agent := NewAgent("test")
	agent.TurnDetection = TurnDetectionModeManual
	agent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "reply to me", Confidence: 0.9}},
	})

	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "reply to me" {
		t.Fatalf("CommitUserTurn transcript = %q, want reply to me", transcript)
	}
	select {
	case ev := <-session.SpeechCreatedEvents():
		if ev.Source != "generate_reply" {
			t.Fatalf("SpeechCreated source = %q, want generate_reply", ev.Source)
		}
		if ev.SpeechHandle.Generation.UserMessage == nil || ev.SpeechHandle.Generation.UserMessage.TextContent() != "reply to me" {
			t.Fatalf("generation user message = %#v, want committed transcript", ev.SpeechHandle.Generation.UserMessage)
		}
		if ev.SpeechHandle.InputDetails.Modality != "audio" {
			t.Fatalf("generation modality = %q, want audio", ev.SpeechHandle.InputDetails.Modality)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("CommitUserTurn did not generate a reply")
	}
}

func TestAgentActivityCompleteUserTurnEmitsEOUMetricsForGeneratedReply(t *testing.T) {
	agent := NewAgent("test")
	agent.TurnDetection = TurnDetectionModeManual
	agent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity

	_, err := activity.completeUserTurn(context.Background(), EndOfTurnInfo{
		NewTranscript:        "metrics turn",
		TranscriptConfidence: 0.9,
		EndOfTurnDelay:       0.12,
		TranscriptionDelay:   0.34,
	})
	if err != nil {
		t.Fatalf("completeUserTurn error = %v, want nil", err)
	}

	var speechID string
	select {
	case ev := <-session.SpeechCreatedEvents():
		speechID = ev.SpeechHandle.ID
	case <-time.After(100 * time.Millisecond):
		t.Fatal("completeUserTurn did not generate a reply")
	}

	select {
	case ev := <-session.MetricsCollectedEvents():
		metrics, ok := ev.Metrics.(*telemetry.EOUMetrics)
		if !ok {
			t.Fatalf("metrics = %T, want *telemetry.EOUMetrics", ev.Metrics)
		}
		if metrics.SpeechID != speechID {
			t.Fatalf("EOUMetrics SpeechID = %q, want generated speech %q", metrics.SpeechID, speechID)
		}
		if metrics.EndOfUtteranceDelay != 0.12 {
			t.Fatalf("EOUMetrics EndOfUtteranceDelay = %v, want 0.12", metrics.EndOfUtteranceDelay)
		}
		if metrics.TranscriptionDelay != 0.34 {
			t.Fatalf("EOUMetrics TranscriptionDelay = %v, want 0.34", metrics.TranscriptionDelay)
		}
		if metrics.OnUserTurnCompletedDelay < 0 {
			t.Fatalf("EOUMetrics OnUserTurnCompletedDelay = %v, want non-negative", metrics.OnUserTurnCompletedDelay)
		}
		if metrics.Metadata == nil {
			t.Fatal("EOUMetrics Metadata = nil, want turn detection metadata")
		}
		if metrics.Metadata.ModelName != "unknown" || metrics.Metadata.ModelProvider != "manual" {
			t.Fatalf("EOUMetrics Metadata = %#v, want unknown/manual", metrics.Metadata)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("MetricsCollectedEvents did not receive EOU metrics")
	}
}

func TestAgentActivityCompleteUserTurnAddsMetricsToGeneratedUserMessage(t *testing.T) {
	agent := NewAgent("test")
	agent.TurnDetection = TurnDetectionModeManual
	agent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity

	started := 1.25
	stopped := 2.5
	_, err := activity.completeUserTurn(context.Background(), EndOfTurnInfo{
		NewTranscript:        "message metrics",
		TranscriptConfidence: 0.9,
		EndOfTurnDelay:       0.12,
		TranscriptionDelay:   0.34,
		StartedSpeakingAt:    &started,
		StoppedSpeakingAt:    &stopped,
	})
	if err != nil {
		t.Fatalf("completeUserTurn error = %v, want nil", err)
	}

	select {
	case ev := <-session.SpeechCreatedEvents():
		msg := ev.SpeechHandle.Generation.UserMessage
		if msg == nil {
			t.Fatal("generation user message = nil, want committed user turn")
		}
		if msg.Metrics["started_speaking_at"] != started {
			t.Fatalf("user message started_speaking_at = %#v, want %v", msg.Metrics["started_speaking_at"], started)
		}
		if msg.Metrics["stopped_speaking_at"] != stopped {
			t.Fatalf("user message stopped_speaking_at = %#v, want %v", msg.Metrics["stopped_speaking_at"], stopped)
		}
		if msg.Metrics["end_of_turn_delay"] != 0.12 {
			t.Fatalf("user message end_of_turn_delay = %#v, want 0.12", msg.Metrics["end_of_turn_delay"])
		}
		if msg.Metrics["transcription_delay"] != 0.34 {
			t.Fatalf("user message transcription_delay = %#v, want 0.34", msg.Metrics["transcription_delay"])
		}
		hookDelay, ok := msg.Metrics["on_user_turn_completed_delay"].(float64)
		if !ok || hookDelay < 0 {
			t.Fatalf("user message on_user_turn_completed_delay = %#v, want non-negative float64", msg.Metrics["on_user_turn_completed_delay"])
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("completeUserTurn did not generate a reply")
	}
}

func TestAgentActivityCommitUserTurnSkipsWhenCurrentSpeechCannotBeInterrupted(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeManual
	agent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity
	activity.currentSpeech = NewSpeechHandle(false, DefaultInputDetails())

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "do not interrupt"}},
	})

	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "do not interrupt" {
		t.Fatalf("CommitUserTurn transcript = %q, want do not interrupt", transcript)
	}
	select {
	case msg := <-agent.turns:
		t.Fatalf("OnUserTurnCompleted called for non-interruptible current speech with %q", msg.TextContent())
	case ev := <-session.SpeechCreatedEvents():
		t.Fatalf("reply generated for non-interruptible current speech: %#v", ev)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestAgentActivityCommitUserTurnInterruptsCurrentSpeechBeforeReply(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeManual
	agent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "interrupt and reply"}},
	})

	done := make(chan error, 1)
	go func() {
		_, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
		done <- err
	}()

	waitForInterrupted(t, current)
	select {
	case err := <-done:
		t.Fatalf("CommitUserTurn returned before current speech completed: %v", err)
	case msg := <-agent.turns:
		t.Fatalf("OnUserTurnCompleted called before current speech completed with %q", msg.TextContent())
	case ev := <-session.SpeechCreatedEvents():
		t.Fatalf("reply generated before current speech completed: %#v", ev)
	case <-time.After(20 * time.Millisecond):
	}

	current.MarkDone()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("CommitUserTurn error = %v, want nil", err)
		}
	case <-testTimeout():
		t.Fatal("CommitUserTurn did not return after current speech completed")
	}
	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "interrupt and reply" {
			t.Fatalf("OnUserTurnCompleted message = %q, want interrupt and reply", msg.TextContent())
		}
	default:
		t.Fatal("OnUserTurnCompleted was not called after current speech completed")
	}
	select {
	case ev := <-session.SpeechCreatedEvents():
		if ev.SpeechHandle.Generation.UserMessage == nil || ev.SpeechHandle.Generation.UserMessage.TextContent() != "interrupt and reply" {
			t.Fatalf("reply user message = %#v, want committed user turn", ev.SpeechHandle.Generation.UserMessage)
		}
	default:
		t.Fatal("reply was not generated after current speech completed")
	}
}

func TestAgentActivityCompleteUserTurnWaitsForPreviousHook(t *testing.T) {
	agent := &blockingTurnAgent{
		Agent:   NewAgent("test"),
		started: make(chan *llm.ChatMessage, 2),
		release: make(chan struct{}),
	}
	agent.TurnDetection = TurnDetectionModeManual
	agent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity

	firstDone := make(chan error, 1)
	go func() {
		_, err := activity.completeUserTurn(context.Background(), EndOfTurnInfo{
			NewTranscript:        "first turn",
			TranscriptConfidence: 0.9,
		})
		firstDone <- err
	}()
	select {
	case msg := <-agent.started:
		if msg.TextContent() != "first turn" {
			t.Fatalf("first hook message = %q, want first turn", msg.TextContent())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("first user turn hook did not start")
	}

	secondDone := make(chan error, 1)
	go func() {
		_, err := activity.completeUserTurn(context.Background(), EndOfTurnInfo{
			NewTranscript:        "second turn",
			TranscriptConfidence: 0.9,
		})
		secondDone <- err
	}()
	select {
	case msg := <-agent.started:
		close(agent.release)
		t.Fatalf("second hook started before first completed with %q", msg.TextContent())
	case <-time.After(20 * time.Millisecond):
	}

	close(agent.release)
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first completeUserTurn error = %v, want nil", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("first completeUserTurn did not finish after release")
	}
	select {
	case msg := <-agent.started:
		if msg.TextContent() != "second turn" {
			t.Fatalf("second hook message = %q, want second turn", msg.TextContent())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("second user turn hook did not start after first completed")
	}
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second completeUserTurn error = %v, want nil", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("second completeUserTurn did not finish")
	}
}

func TestAgentActivityCompleteUserTurnSkipsShortInterruptions(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeSTT
	agent.STT = &fakePipelineSTT{}
	agent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{MinInterruptionWords: 2})
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current

	done := make(chan error, 1)
	go func() {
		_, err := activity.completeUserTurn(context.Background(), EndOfTurnInfo{
			NewTranscript:        "hi",
			TranscriptConfidence: 0.9,
		})
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("completeUserTurn error = %v, want nil", err)
		}
	case <-time.After(20 * time.Millisecond):
		if current.IsInterrupted() {
			current.MarkDone()
			<-done
			t.Fatal("current speech was interrupted for transcript below MinInterruptionWords")
		}
		t.Fatal("completeUserTurn did not return for transcript below MinInterruptionWords")
	}
	if current.IsInterrupted() {
		t.Fatal("current speech interrupted for transcript below MinInterruptionWords")
	}
	select {
	case msg := <-agent.turns:
		t.Fatalf("OnUserTurnCompleted called for short interruption with %q", msg.TextContent())
	case ev := <-session.SpeechCreatedEvents():
		t.Fatalf("reply generated for short interruption: %#v", ev)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestAgentActivityShortInterruptionUsesConfiguredWordTokenizer(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeSTT
	agent.STT = &fakePipelineSTT{}
	agent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		MinInterruptionWords: 2,
		WordTokenizer:        fixedWordTokenizer{tokens: []string{"single"}},
	})
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current

	done := make(chan error, 1)
	go func() {
		_, err := activity.completeUserTurn(context.Background(), EndOfTurnInfo{
			NewTranscript:        "two words",
			TranscriptConfidence: 0.9,
		})
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("completeUserTurn error = %v, want nil", err)
		}
	case <-time.After(20 * time.Millisecond):
		if current.IsInterrupted() {
			current.MarkDone()
			<-done
			t.Fatal("current speech was interrupted despite tokenizer reporting one word")
		}
		t.Fatal("completeUserTurn did not return for tokenizer-short interruption")
	}
	if current.IsInterrupted() {
		t.Fatal("current speech interrupted despite tokenizer reporting one word")
	}
}

func TestAgentActivityCommitUserTurnSkipsWhenSchedulingPaused(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeManual
	agent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity
	activity.schedulingPaused = true

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "paused turn"}},
	})

	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "paused turn" {
		t.Fatalf("CommitUserTurn transcript = %q, want paused turn", transcript)
	}
	select {
	case msg := <-agent.turns:
		t.Fatalf("OnUserTurnCompleted called while scheduling paused with %q", msg.TextContent())
	case ev := <-session.SpeechCreatedEvents():
		t.Fatalf("reply generated while scheduling paused: %#v", ev)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestAgentActivityCommitUserTurnSkipsReplyWhenHookPausesScheduling(t *testing.T) {
	agent := &pausingTurnAgent{
		Agent: NewAgent("test"),
		turns: make(chan *llm.ChatMessage, 1),
	}
	agent.TurnDetection = TurnDetectionModeManual
	agent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "pause after hook"}},
	})

	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "pause after hook" {
		t.Fatalf("CommitUserTurn transcript = %q, want pause after hook", transcript)
	}
	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "pause after hook" {
			t.Fatalf("OnUserTurnCompleted message = %q, want pause after hook", msg.TextContent())
		}
	default:
		t.Fatal("OnUserTurnCompleted was not called")
	}
	select {
	case ev := <-session.SpeechCreatedEvents():
		t.Fatalf("reply generated after hook paused scheduling: %#v", ev)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestAgentActivityCommitUserTurnStopResponseSkipsReply(t *testing.T) {
	agent := &stopResponseTurnAgent{
		Agent: NewAgent("test"),
		turns: make(chan *llm.ChatMessage, 1),
	}
	agent.TurnDetection = TurnDetectionModeManual
	agent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "stop response"}},
	})

	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil for StopResponse", err)
	}
	if transcript != "stop response" {
		t.Fatalf("CommitUserTurn transcript = %q, want stop response", transcript)
	}
	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "stop response" {
			t.Fatalf("OnUserTurnCompleted message = %q, want stop response", msg.TextContent())
		}
	default:
		t.Fatal("OnUserTurnCompleted was not called")
	}
	select {
	case ev := <-session.SpeechCreatedEvents():
		t.Fatalf("reply generated after StopResponse: %#v", ev)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestAgentActivityCommitUserTurnHookErrorSkipsReply(t *testing.T) {
	agent := &errorTurnAgent{
		Agent: NewAgent("test"),
		turns: make(chan *llm.ChatMessage, 1),
		err:   errors.New("hook failed"),
	}
	agent.TurnDetection = TurnDetectionModeManual
	agent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "hook error"}},
	})

	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil for hook error", err)
	}
	if transcript != "hook error" {
		t.Fatalf("CommitUserTurn transcript = %q, want hook error", transcript)
	}
	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "hook error" {
			t.Fatalf("OnUserTurnCompleted message = %q, want hook error", msg.TextContent())
		}
	default:
		t.Fatal("OnUserTurnCompleted was not called")
	}
	select {
	case ev := <-session.SpeechCreatedEvents():
		t.Fatalf("reply generated after hook error: %#v", ev)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestAgentActivityCommitUserTurnSkipsReplyWhenLLMMissing(t *testing.T) {
	agent := NewAgent("test")
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "no llm"}},
	})

	if _, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{}); err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	select {
	case ev := <-session.SpeechCreatedEvents():
		t.Fatalf("unexpected SpeechCreated event without LLM: %#v", ev)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestAgentActivityAutomaticTurnCompletionConsumesPendingTranscript(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 2)}
	agent.TurnDetection = TurnDetectionModeSTT
	agent.STT = &fakePipelineSTT{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{MinEndpointingDelay: 0.01})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "automatic turn", Confidence: 0.9}},
	})

	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "automatic turn" {
			t.Fatalf("turn message text = %q, want automatic turn", msg.TextContent())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnUserTurnCompleted was not called for automatic STT turn")
	}

	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "" {
		t.Fatalf("CommitUserTurn transcript = %q, want empty after automatic completion", transcript)
	}
	select {
	case msg := <-agent.turns:
		t.Fatalf("CommitUserTurn duplicated completed turn with %q", msg.TextContent())
	case <-time.After(20 * time.Millisecond):
	}
}

func TestAgentActivityCommitUserTurnSkipReplyAddsUserMessageWithoutCallback(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "store only", Confidence: 0.7}},
	})

	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{SkipReply: true})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "store only" {
		t.Fatalf("CommitUserTurn transcript = %q, want store only", transcript)
	}
	select {
	case msg := <-agent.turns:
		t.Fatalf("OnUserTurnCompleted called despite SkipReply with %q", msg.TextContent())
	case <-time.After(20 * time.Millisecond):
	}
	if len(agent.ChatCtx.Items) != 1 {
		t.Fatalf("agent chat context has %d items, want committed user message", len(agent.ChatCtx.Items))
	}
	msg, ok := agent.ChatCtx.Items[0].(*llm.ChatMessage)
	if !ok || msg.Role != llm.ChatRoleUser || msg.TextContent() != "store only" {
		t.Fatalf("committed chat item = %#v, want user message", agent.ChatCtx.Items[0])
	}
}

func TestAgentActivityUserTurnExceededSkipsWhenAgentStartsSpeaking(t *testing.T) {
	agent := &countingExceededAgent{Agent: NewAgent("test"), calls: make(chan UserTurnExceededEvent, 1)}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity
	activity.currentSpeech = NewSpeechHandle(true, DefaultInputDetails())

	activity.OnUserTurnExceeded(UserTurnExceededEvent{Transcript: "still speaking"})
	session.UpdateAgentState(AgentStateSpeaking)

	select {
	case ev := <-agent.calls:
		t.Fatalf("OnUserTurnExceeded called after agent started speaking: %#v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

type turnCompletedAgent struct {
	*Agent
	turns chan *llm.ChatMessage
}

func (a *turnCompletedAgent) OnUserTurnCompleted(ctx context.Context, chatCtx *llm.ChatContext, newMsg *llm.ChatMessage) error {
	a.turns <- newMsg
	return nil
}

type pausingTurnAgent struct {
	*Agent
	turns chan *llm.ChatMessage
}

func (a *pausingTurnAgent) OnUserTurnCompleted(ctx context.Context, chatCtx *llm.ChatContext, newMsg *llm.ChatMessage) error {
	a.turns <- newMsg
	a.activity.schedulingPaused = true
	return nil
}

type stopResponseTurnAgent struct {
	*Agent
	turns chan *llm.ChatMessage
}

func (a *stopResponseTurnAgent) OnUserTurnCompleted(ctx context.Context, chatCtx *llm.ChatContext, newMsg *llm.ChatMessage) error {
	a.turns <- newMsg
	return llm.StopResponse{}
}

type errorTurnAgent struct {
	*Agent
	turns chan *llm.ChatMessage
	err   error
}

func (a *errorTurnAgent) OnUserTurnCompleted(ctx context.Context, chatCtx *llm.ChatContext, newMsg *llm.ChatMessage) error {
	a.turns <- newMsg
	return a.err
}

type countingExceededAgent struct {
	*Agent
	calls chan UserTurnExceededEvent
}

func (a *countingExceededAgent) OnUserTurnExceeded(ctx context.Context, ev UserTurnExceededEvent) error {
	a.calls <- ev
	return nil
}

type blockingTurnAgent struct {
	*Agent
	started chan *llm.ChatMessage
	release chan struct{}
}

func (a *blockingTurnAgent) OnUserTurnCompleted(ctx context.Context, chatCtx *llm.ChatContext, newMsg *llm.ChatMessage) error {
	a.started <- newMsg
	select {
	case <-a.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type turnDetectorFunc func(context.Context, *llm.ChatContext) (float64, error)

func (f turnDetectorFunc) PredictEndOfTurn(ctx context.Context, chatCtx *llm.ChatContext) (float64, error) {
	return f(ctx, chatCtx)
}

func waitForNoCurrentSpeech(t *testing.T, activity *AgentActivity) {
	t.Helper()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			t.Fatal("current speech was not cleared after MarkDone")
		case <-ticker.C:
			activity.queueMu.Lock()
			cleared := activity.currentSpeech == nil
			activity.queueMu.Unlock()
			if cleared {
				return
			}
		}
	}
}

func waitForCurrentSpeech(t *testing.T, activity *AgentActivity, want *SpeechHandle) {
	t.Helper()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			t.Fatalf("current speech did not become %p", want)
		case <-ticker.C:
			activity.queueMu.Lock()
			got := activity.currentSpeech
			activity.queueMu.Unlock()
			if got == want {
				return
			}
		}
	}
}

func waitForDraining(t *testing.T, activity *AgentActivity) {
	t.Helper()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			t.Fatal("activity did not enter draining state")
		case <-ticker.C:
			activity.queueMu.Lock()
			draining := activity.schedulingDraining
			activity.queueMu.Unlock()
			if draining {
				return
			}
		}
	}
}

func waitForInterrupted(t *testing.T, speech *SpeechHandle) {
	t.Helper()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			t.Fatal("speech was not interrupted")
		case <-ticker.C:
			if speech.IsInterrupted() {
				return
			}
		}
	}
}

type recordingScheduledSpeechAssistant struct {
	scheduledCh chan *SpeechHandle
}

func (r *recordingScheduledSpeechAssistant) Start(context.Context, *AgentSession) error {
	return nil
}

func (r *recordingScheduledSpeechAssistant) OnAudioFrame(context.Context, *model.AudioFrame) {
}

func (r *recordingScheduledSpeechAssistant) SetPublishAudio(func(frame *model.AudioFrame) error) {
}

func (r *recordingScheduledSpeechAssistant) OnSpeechScheduled(ctx context.Context, speech *SpeechHandle) {
	speech.AuthorizeGeneration()
	select {
	case r.scheduledCh <- speech:
	case <-ctx.Done():
	}
}

func receiveScheduledSpeech(t *testing.T, assistant *recordingScheduledSpeechAssistant) *SpeechHandle {
	t.Helper()

	select {
	case speech := <-assistant.scheduledCh:
		return speech
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for scheduled speech")
		return nil
	}
}

type fixedWordTokenizer struct {
	tokens []string
}

func (f fixedWordTokenizer) Tokenize(string, string) []string {
	return append([]string(nil), f.tokens...)
}

func (f fixedWordTokenizer) Stream(string) tokenize.WordStream {
	return tokenize.NewBufferedTokenStream(func(string) []string {
		return append([]string(nil), f.tokens...)
	}, 1, 1)
}

func (f fixedWordTokenizer) FormatWords(words []string) string {
	return strings.Join(words, " ")
}
