package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
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

type turnCompletedAgent struct {
	*Agent
	turns chan *llm.ChatMessage
}

func (a *turnCompletedAgent) OnUserTurnCompleted(ctx context.Context, chatCtx *llm.ChatContext, newMsg *llm.ChatMessage) error {
	a.turns <- newMsg
	return nil
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
