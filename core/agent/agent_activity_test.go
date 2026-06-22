package agent

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/core/vad"
	logutil "github.com/cavos-io/rtp-agent/library/logger"
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

func TestAgentActivitySpeechDoneClearsPausedFalseInterruption(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection:               TurnDetectionModeVAD,
		MinInterruptionDuration:     0.05,
		FalseInterruptionTimeout:    10,
		FalseInterruptionTimeoutSet: true,
		ResumeFalseInterruption:     true,
		ResumeFalseInterruptionSet:  true,
	})
	audioOutput := &recordingAudioOutputController{canPause: true}
	session.SetAudioOutputController(audioOutput)
	activity := NewAgentActivity(agent, session)
	speech := NewSpeechHandle(true, DefaultInputDetails())

	if err := activity.ScheduleSpeech(speech, SpeechPriorityNormal, false); err != nil {
		t.Fatalf("ScheduleSpeech error = %v, want nil", err)
	}
	activity.processQueue()
	if activity.currentSpeech != speech {
		t.Fatalf("currentSpeech = %p, want scheduled speech %p", activity.currentSpeech, speech)
	}
	session.agentState = AgentStateSpeaking
	activity.OnVADInferenceDone(&vad.VADEvent{
		Type:                  vad.VADEventInferenceDone,
		SpeechDuration:        0.06,
		Speaking:              true,
		RawAccumulatedSilence: 0,
	})
	if audioOutput.pauseCount != 1 {
		t.Fatalf("PauseAudioOutput calls = %d, want 1", audioOutput.pauseCount)
	}

	speech.MarkDone()
	waitForNoCurrentSpeech(t, activity)

	if audioOutput.resumeCount != 1 {
		t.Fatalf("ResumeAudioOutput calls = %d, want 1 after paused speech finished", audioOutput.resumeCount)
	}
	select {
	case ev := <-session.AgentFalseInterruptionEvents():
		t.Fatalf("unexpected false interruption event after paused speech finished: %#v", ev)
	default:
	}
}

func TestAgentActivityProcessQueueDoesNotDeadlockWhenSpeechCompletesDuringDoneCallbackRegistration(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	speech := NewSpeechHandle(true, DefaultInputDetails())
	activity.speechQueue = append(activity.speechQueue, scheduledSpeech{
		speech:   speech,
		priority: SpeechPriorityNormal,
	})

	speech.mu.Lock()
	done := make(chan struct{})
	go func() {
		activity.processQueue()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("processQueue returned before done callback registration was released")
	case <-time.After(10 * time.Millisecond):
	}
	close(speech.doneCh)
	speech.mu.Unlock()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("processQueue deadlocked when done callback ran during registration")
	}
	waitForNoCurrentSpeech(t, activity)
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
	activity.PauseScheduling()
	if !activity.SchedulingPaused() {
		t.Fatal("SchedulingPaused() = false after pause, want true")
	}
}

func TestAgentActivityResumeSchedulingWakesQueuedForcedSpeech(t *testing.T) {
	agent := NewAgent("test")
	assistant := &recordingScheduledSpeechAssistant{scheduledCh: make(chan *SpeechHandle, 1)}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Assistant = assistant
	activity := NewAgentActivity(agent, session)
	activity.Start()
	defer activity.Stop()
	activity.PauseScheduling()
	speech := NewSpeechHandle(true, DefaultInputDetails())

	if err := activity.ScheduleSpeech(speech, SpeechPriorityNormal, true); err != nil {
		t.Fatalf("ScheduleSpeech forced error = %v, want nil", err)
	}
	activity.ResumeScheduling()

	select {
	case got := <-assistant.scheduledCh:
		if got != speech {
			t.Fatalf("scheduled speech = %p, want %p", got, speech)
		}
	case <-time.After(time.Second):
		t.Fatal("ResumeScheduling did not wake queued forced speech")
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

func TestAgentActivityToolsAddsCancellationHelpersForCancellableTools(t *testing.T) {
	agentTool := &agentTestTool{id: "lookup", name: "lookup", flags: llm.ToolFlagCancellable}
	agent := NewAgent("test")
	agent.Tools = []llm.Tool{agentTool}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	got := sortedAgentToolNames(activity.Tools())
	want := []string{"lk_agents_cancel_task", "lk_agents_get_running_tasks", "lookup"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Tools() names = %#v, want reference cancellation helpers %#v", got, want)
	}
}

func TestAgentActivityToolsAddsCancellationHelpersForNestedCancellableToolsets(t *testing.T) {
	cancellableTool := &agentTestTool{id: "lookup", name: "lookup", flags: llm.ToolFlagCancellable}
	innerToolset := &nestedAgentToolset{
		agentTestTool: agentTestTool{id: "inner", name: "inner"},
		tools:         []llm.Tool{cancellableTool},
	}
	outerToolset := &nestedAgentToolset{
		agentTestTool: agentTestTool{id: "outer", name: "outer"},
		tools:         []llm.Tool{innerToolset},
	}
	agent := NewAgent("test")
	agent.Tools = []llm.Tool{outerToolset}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	got := sortedAgentToolNames(activity.Tools())
	want := []string{"lk_agents_cancel_task", "lk_agents_get_running_tasks", "outer"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Tools() names = %#v, want reference recursive cancellation helpers %#v", got, want)
	}
}

func TestAgentActivityToolsOmitCancellationHelpersWithoutCancellableTools(t *testing.T) {
	agentTool := &agentTestTool{id: "lookup", name: "lookup"}
	agent := NewAgent("test")
	agent.Tools = []llm.Tool{agentTool}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	got := sortedAgentToolNames(activity.Tools())
	want := []string{"lookup"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Tools() names = %#v, want no reference cancellation helpers %#v", got, want)
	}
}

func TestAgentActivityToolsIncludesStartedMCPTools(t *testing.T) {
	mcpTool := &agentTestTool{id: "lookup", name: "lookup"}
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.SetMCPServers([]llm.MCPServer{&fakeActivityMCPServer{tools: []llm.Tool{mcpTool}}})
	activity := NewAgentActivity(agent, session)

	activity.Start()
	defer activity.Stop()

	got := sortedAgentToolNames(activity.Tools())
	want := []string{"lookup"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Tools() names after Start = %#v, want MCP tools %#v", got, want)
	}
}

func TestAgentActivityUpdateChatCtxPreservesMCPToolItems(t *testing.T) {
	mcpTool := &agentTestTool{id: "lookup", name: "lookup"}
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.SetMCPServers([]llm.MCPServer{&fakeActivityMCPServer{tools: []llm.Tool{mcpTool}}})
	activity := NewAgentActivity(agent, session)
	chatCtx := llm.NewChatContext()
	chatCtx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "look this up"}}},
		&llm.FunctionCall{ID: "lookup-call", CallID: "call_lookup", Name: "lookup", Arguments: "{}"},
		&llm.FunctionCallOutput{ID: "lookup-output", CallID: "call_lookup", Name: "lookup", Output: "found"},
	}

	if err := activity.UpdateChatCtx(context.Background(), chatCtx); err != nil {
		t.Fatalf("UpdateChatCtx() error = %v", err)
	}

	if got, want := agentActivityChatItemIDs(agent.ChatCtx.Items), "lk.agent_task.instructions,user,lookup-call,lookup-output"; got != want {
		t.Fatalf("agent chat context item IDs = %q, want %q", got, want)
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

func TestAgentActivityUpdateOptionsForwardsRealtimeToolChoice(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	assistant := &recordingOptionsAssistant{}
	session.Assistant = assistant
	activity := NewAgentActivity(agent, session)
	toolChoice := llm.ToolChoice("none")

	if err := activity.UpdateOptions(AgentSessionUpdateOptions{ToolChoice: &toolChoice}); err != nil {
		t.Fatalf("UpdateOptions error = %v, want nil", err)
	}

	if assistant.options.ToolChoice != toolChoice {
		t.Fatalf("realtime ToolChoice = %#v, want %#v", assistant.options.ToolChoice, toolChoice)
	}
	if !assistant.options.ToolChoiceSet {
		t.Fatal("realtime ToolChoiceSet = false, want true for explicit tool choice update")
	}
}

func TestAgentActivityUpdateOptionsClearsRealtimeToolChoice(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Options.ToolChoice = llm.ToolChoice("required")
	assistant := &recordingOptionsAssistant{}
	session.Assistant = assistant
	activity := NewAgentActivity(agent, session)
	var toolChoice llm.ToolChoice

	if err := activity.UpdateOptions(AgentSessionUpdateOptions{ToolChoice: &toolChoice}); err != nil {
		t.Fatalf("UpdateOptions error = %v, want nil", err)
	}

	if assistant.options.ToolChoice != nil {
		t.Fatalf("realtime ToolChoice = %#v, want nil clear", assistant.options.ToolChoice)
	}
	if !assistant.options.ToolChoiceSet {
		t.Fatal("realtime ToolChoiceSet = false, want true for explicit nil tool choice update")
	}
}

func TestAgentActivityUpdateOptionsRefreshesRealtimeStoredToolChoice(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	toolChoice := llm.ToolChoice("auto")
	session.Options.ToolChoice = toolChoice
	assistant := &recordingOptionsAssistant{}
	session.Assistant = assistant
	activity := NewAgentActivity(agent, session)
	minDelay := 0.2

	if err := activity.UpdateOptions(AgentSessionUpdateOptions{MinEndpointingDelay: &minDelay}); err != nil {
		t.Fatalf("UpdateOptions error = %v, want nil", err)
	}

	if assistant.options.ToolChoice != toolChoice {
		t.Fatalf("realtime ToolChoice = %#v, want stored %#v", assistant.options.ToolChoice, toolChoice)
	}
	if !assistant.options.ToolChoiceSet {
		t.Fatal("realtime ToolChoiceSet = false, want true for stored tool choice refresh")
	}
}

func TestAgentActivityUpdateOptionsRefreshesRealtimeNilToolChoice(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	assistant := &recordingOptionsAssistant{}
	session.Assistant = assistant
	activity := NewAgentActivity(agent, session)
	minDelay := 0.2

	if err := activity.UpdateOptions(AgentSessionUpdateOptions{MinEndpointingDelay: &minDelay}); err != nil {
		t.Fatalf("UpdateOptions error = %v, want nil", err)
	}

	if assistant.options.ToolChoice != nil {
		t.Fatalf("realtime ToolChoice = %#v, want nil refresh", assistant.options.ToolChoice)
	}
	if !assistant.options.ToolChoiceSet {
		t.Fatal("realtime ToolChoiceSet = false, want true for nil tool choice refresh")
	}
}

func TestAgentActivityRealtimeInputSpeechCallbacksUpdateUserState(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	activity.OnInputSpeechStarted()
	if got := session.UserState(); got != UserStateSpeaking {
		t.Fatalf("UserState() after speech started = %q, want %q", got, UserStateSpeaking)
	}

	activity.OnInputSpeechStopped(llm.InputSpeechStoppedEvent{})
	if got := session.UserState(); got != UserStateListening {
		t.Fatalf("UserState() after speech stopped = %q, want %q", got, UserStateListening)
	}
}

func TestAgentActivityRealtimeInputSpeechStartedKeepsVADOwnedUserState(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current

	activity.OnInputSpeechStarted()

	if got := session.UserState(); got != UserStateListening {
		t.Fatalf("UserState() after realtime speech started with VAD = %q, want %q", got, UserStateListening)
	}
	waitForInterrupted(t, current)
	current.MarkDone()
}

func TestAgentActivityRealtimeInputSpeechStoppedKeepsVADOwnedUserState(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.UpdateUserState(UserStateSpeaking)
	activity := NewAgentActivity(agent, session)
	userTranscriptEvents := session.UserInputTranscribedEvents()

	activity.OnInputSpeechStopped(llm.InputSpeechStoppedEvent{UserTranscriptionEnabled: true})

	if got := session.UserState(); got != UserStateSpeaking {
		t.Fatalf("UserState() after realtime speech stopped with VAD = %q, want %q", got, UserStateSpeaking)
	}
	select {
	case ev := <-userTranscriptEvents:
		if ev.Transcript != "" || ev.IsFinal {
			t.Fatalf("UserInputTranscribedEvent = %#v, want empty interim transcript", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("UserInputTranscribedEvents did not receive empty interim transcript")
	}
}

func TestAgentActivityVADSpeechCallbacksUpdateUserState(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	activity.OnStartOfSpeech(&vad.VADEvent{Type: vad.VADEventStartOfSpeech})
	if got := session.UserState(); got != UserStateSpeaking {
		t.Fatalf("UserState() after VAD speech started = %q, want %q", got, UserStateSpeaking)
	}

	activity.OnEndOfSpeech(&vad.VADEvent{Type: vad.VADEventEndOfSpeech})
	if got := session.UserState(); got != UserStateListening {
		t.Fatalf("UserState() after VAD speech ended = %q, want %q", got, UserStateListening)
	}
}

func TestAgentActivityOnEndOfSpeechSkipsEndpointingWhenNotSpeaking(t *testing.T) {
	endpointing := &recordingActivityEndpointing{}
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{Endpointing: endpointing})
	activity := NewAgentActivity(agent, session)

	activity.OnEndOfSpeech(&vad.VADEvent{Type: vad.VADEventEndOfSpeech, Timestamp: 2.5})

	if endpointing.endCount != 0 {
		t.Fatalf("OnEndOfSpeech calls = %d, want 0 for stale end-of-speech", endpointing.endCount)
	}
}

func TestAgentActivityOnEndOfSpeechReportsActualSpeechEndTime(t *testing.T) {
	endpointing := &recordingActivityEndpointing{}
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{Endpointing: endpointing})
	activity := NewAgentActivity(agent, session)

	activity.OnStartOfSpeech(&vad.VADEvent{Type: vad.VADEventStartOfSpeech, Timestamp: 1.0})
	select {
	case <-session.UserStateChangedCh:
	case <-testTimeout():
		t.Fatal("UserStateChangedCh did not receive start-of-speech event")
	}
	beforeEnd := time.Now()
	activity.OnEndOfSpeech(&vad.VADEvent{
		Type:              vad.VADEventEndOfSpeech,
		Timestamp:         3.0,
		SilenceDuration:   0.4,
		InferenceDuration: 0.1,
	})

	if endpointing.endCount != 1 {
		t.Fatalf("OnEndOfSpeech calls = %d, want 1", endpointing.endCount)
	}
	if endpointing.lastEnd != 2.5 {
		t.Fatalf("OnEndOfSpeech endedAt = %v, want 2.5", endpointing.lastEnd)
	}
	var ev UserStateChangedEvent
	select {
	case ev = <-session.UserStateChangedCh:
	case <-testTimeout():
		t.Fatal("UserStateChangedCh did not receive end-of-speech event")
	}
	if ev.NewState != UserStateListening {
		t.Fatalf("user state = %q, want listening", ev.NewState)
	}
	if ev.CreatedAt.After(beforeEnd.Add(-450 * time.Millisecond)) {
		t.Fatalf("user state CreatedAt = %v, want VAD-adjusted speech end before callback", ev.CreatedAt.Sub(beforeEnd))
	}
	if ev.CreatedAt.Before(beforeEnd.Add(-700 * time.Millisecond)) {
		t.Fatalf("user state CreatedAt = %v, want close to VAD-adjusted end", ev.CreatedAt.Sub(beforeEnd))
	}
}

func TestAgentActivityOnStartOfSpeechReportsActualSpeechStartTime(t *testing.T) {
	endpointing := &recordingActivityEndpointing{}
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{Endpointing: endpointing})
	activity := NewAgentActivity(agent, session)

	beforeStart := time.Now()
	activity.OnStartOfSpeech(&vad.VADEvent{
		Type:              vad.VADEventStartOfSpeech,
		Timestamp:         3.0,
		SpeechDuration:    0.4,
		InferenceDuration: 0.1,
	})

	if endpointing.startCount != 1 {
		t.Fatalf("OnStartOfSpeech calls = %d, want 1", endpointing.startCount)
	}
	if endpointing.lastStart != 2.5 {
		t.Fatalf("OnStartOfSpeech startedAt = %v, want 2.5", endpointing.lastStart)
	}
	if activity.userSpeechStartedAt.After(beforeStart.Add(-450 * time.Millisecond)) {
		t.Fatalf("userSpeechStartedAt = %v, want at least 450ms before OnStartOfSpeech", activity.userSpeechStartedAt.Sub(beforeStart))
	}
	if activity.userSpeechStartedAt.Before(beforeStart.Add(-700 * time.Millisecond)) {
		t.Fatalf("userSpeechStartedAt = %v, want close to VAD-adjusted start", activity.userSpeechStartedAt.Sub(beforeStart))
	}
	select {
	case ev := <-session.UserStateChangedCh:
		if ev.CreatedAt.After(beforeStart.Add(-450 * time.Millisecond)) {
			t.Fatalf("user state CreatedAt = %v, want VAD-adjusted speech start before callback", ev.CreatedAt.Sub(beforeStart))
		}
		if ev.CreatedAt.Before(beforeStart.Add(-700 * time.Millisecond)) {
			t.Fatalf("user state CreatedAt = %v, want close to VAD-adjusted start", ev.CreatedAt.Sub(beforeStart))
		}
	case <-testTimeout():
		t.Fatal("UserStateChangedCh did not receive start-of-speech event")
	}
}

func TestAgentActivityPendingFinalKeepsFirstSpeechStartAcrossVADBursts(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	activity.OnStartOfSpeech(&vad.VADEvent{
		Type:              vad.VADEventStartOfSpeech,
		SpeechDuration:    0.5,
		InferenceDuration: 0.01,
	})
	firstBurstStart := activity.userSpeechStartedAt
	activity.OnEndOfSpeech(&vad.VADEvent{Type: vad.VADEventEndOfSpeech, SilenceDuration: 0.05})

	time.Sleep(20 * time.Millisecond)
	activity.OnStartOfSpeech(&vad.VADEvent{Type: vad.VADEventStartOfSpeech})
	if !activity.userSpeechStartedAt.After(firstBurstStart) {
		t.Fatalf("second burst userSpeechStartedAt = %v, want per-burst start after first %v", activity.userSpeechStartedAt, firstBurstStart)
	}
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "multi burst turn", Confidence: 0.9}},
	})

	info := activity.pendingFinalEndOfTurnInfo()
	if info.StartedSpeakingAt == nil {
		t.Fatal("StartedSpeakingAt = nil, want first burst start")
	}
	got := unixSecondsToTime(*info.StartedSpeakingAt)
	if got.Sub(firstBurstStart) > 10*time.Millisecond || firstBurstStart.Sub(got) > 10*time.Millisecond {
		t.Fatalf("StartedSpeakingAt = %v, want first burst start %v", got, firstBurstStart)
	}
}

func TestAgentActivityOnStartOfSpeechPausesThinkingSpeech(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		ResumeFalseInterruption:    true,
		ResumeFalseInterruptionSet: true,
	})
	audioOutput := &recordingAudioOutputController{canPause: true}
	session.SetAudioOutputController(audioOutput)
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	session.agentState = AgentStateListening
	falseInterruptions := session.AgentFalseInterruptionEvents()

	activity.OnStartOfSpeech(&vad.VADEvent{Type: vad.VADEventStartOfSpeech})

	if audioOutput.pauseCount != 1 {
		t.Fatalf("PauseAudioOutput calls = %d, want 1", audioOutput.pauseCount)
	}
	if current.IsInterrupted() {
		t.Fatal("current speech was interrupted instead of paused while agent was thinking")
	}

	activity.OnEndOfSpeech(&vad.VADEvent{Type: vad.VADEventEndOfSpeech})

	select {
	case ev := <-falseInterruptions:
		if !ev.Resumed {
			t.Fatalf("AgentFalseInterruptionEvent.Resumed = false, want true")
		}
	case <-time.After(time.Second):
		t.Fatal("AgentFalseInterruptionEvents did not receive resumed event")
	}
	if audioOutput.resumeCount != 1 {
		t.Fatalf("ResumeAudioOutput calls = %d, want 1", audioOutput.resumeCount)
	}
	current.MarkDone()
}

func TestAgentActivityOnStartOfSpeechCancelsPendingFalseInterruptionResume(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection:               TurnDetectionModeVAD,
		MinInterruptionDuration:     0.05,
		FalseInterruptionTimeout:    0.02,
		FalseInterruptionTimeoutSet: true,
		ResumeFalseInterruption:     true,
		ResumeFalseInterruptionSet:  true,
	})
	audioOutput := &recordingAudioOutputController{canPause: true}
	session.SetAudioOutputController(audioOutput)
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	session.agentState = AgentStateSpeaking
	falseInterruptions := session.AgentFalseInterruptionEvents()

	activity.OnVADInferenceDone(&vad.VADEvent{
		Type:                  vad.VADEventInferenceDone,
		SpeechDuration:        0.06,
		Speaking:              true,
		RawAccumulatedSilence: 0,
	})
	activity.OnEndOfSpeech(&vad.VADEvent{Type: vad.VADEventEndOfSpeech})
	activity.OnStartOfSpeech(&vad.VADEvent{Type: vad.VADEventStartOfSpeech})

	select {
	case ev := <-falseInterruptions:
		t.Fatalf("false interruption resumed while user started speaking again: %#v", ev)
	case <-time.After(50 * time.Millisecond):
	}
	if audioOutput.resumeCount != 0 {
		t.Fatalf("ResumeAudioOutput calls = %d, want 0 while user is speaking again", audioOutput.resumeCount)
	}

	activity.OnEndOfSpeech(&vad.VADEvent{Type: vad.VADEventEndOfSpeech})

	select {
	case ev := <-falseInterruptions:
		if !ev.Resumed {
			t.Fatalf("AgentFalseInterruptionEvent.Resumed = false, want true")
		}
	case <-time.After(time.Second):
		t.Fatal("AgentFalseInterruptionEvents did not receive resumed event after second speech end")
	}
	if audioOutput.resumeCount != 1 {
		t.Fatalf("ResumeAudioOutput calls = %d, want 1 after second speech end", audioOutput.resumeCount)
	}
	current.MarkDone()
}

func TestAgentSessionUpdateOptionsToManualCancelsFalseInterruptionTimer(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection: TurnDetectionModeVAD,
	})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	activity.falseInterruptionTimer = time.AfterFunc(time.Hour, func() {})
	defer activity.cancelFalseInterruptionTimer()

	manual := TurnDetectionModeManual
	if err := session.UpdateOptions(AgentSessionUpdateOptions{TurnDetection: &manual}); err != nil {
		t.Fatalf("UpdateOptions error = %v, want nil", err)
	}

	if activity.falseInterruptionTimer != nil {
		t.Fatal("falseInterruptionTimer still armed after switching to manual turn detection")
	}
}

func TestAgentSessionUpdateOptionsToManualCancelsPendingEOU(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeSTT
	agent.STT = &fakePipelineSTT{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		MinEndpointingDelay: 0.05,
		TurnDetection:       TurnDetectionModeSTT,
	})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	defer activity.Stop()

	activity.runEOUDetection(EndOfTurnInfo{NewTranscript: "pending automatic", TranscriptConfidence: 0.9})
	manual := TurnDetectionModeManual
	if err := session.UpdateOptions(AgentSessionUpdateOptions{TurnDetection: &manual}); err != nil {
		t.Fatalf("UpdateOptions error = %v, want nil", err)
	}

	select {
	case msg := <-agent.turns:
		t.Fatalf("OnUserTurnCompleted called after switching to manual with %q", msg.TextContent())
	case <-time.After(100 * time.Millisecond):
	}
}

func TestAgentUpdateTurnDetectionWhileRunningCancelsPendingEOU(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeSTT
	agent.STT = &fakePipelineSTT{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		MinEndpointingDelay: 0.05,
		TurnDetection:       TurnDetectionModeSTT,
	})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	defer activity.Stop()

	activity.runEOUDetection(EndOfTurnInfo{NewTranscript: "pending automatic", TranscriptConfidence: 0.9})
	if err := agent.UpdateTurnDetection(context.Background(), TurnDetectionModeManual); err != nil {
		t.Fatalf("UpdateTurnDetection error = %v, want nil", err)
	}

	if got := agent.TurnDetection; got != TurnDetectionModeManual {
		t.Fatalf("agent TurnDetection = %q, want %q", got, TurnDetectionModeManual)
	}
	select {
	case msg := <-agent.turns:
		t.Fatalf("OnUserTurnCompleted called after agent turn_detection switched to manual with %q", msg.TextContent())
	case <-time.After(100 * time.Millisecond):
	}
}

func TestAgentActivityDuplicateStartOfSpeechKeepsActiveAudioFrames(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	activity.OnStartOfSpeech(&vad.VADEvent{Type: vad.VADEventStartOfSpeech})
	activity.RecordUserAudioFrame(&model.AudioFrame{
		Data:              []byte{1, 2},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	})

	activity.OnStartOfSpeech(&vad.VADEvent{Type: vad.VADEventStartOfSpeech})

	frames := activity.userAudioSnapshot()
	if len(frames) != 1 {
		t.Fatalf("user audio frames after duplicate start = %d, want 1", len(frames))
	}
}

func TestAgentActivityOverlapSpeechEndedEmitsAndMarksInterruption(t *testing.T) {
	endpointing := &recordingActivityEndpointing{}
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{Endpointing: endpointing})
	activity := NewAgentActivity(agent, session)
	events := session.OverlappingSpeechEvents()
	detectedAt := time.Unix(40, 250_000_000)

	activity.OnStartOfSpeech(&vad.VADEvent{Type: vad.VADEventStartOfSpeech, Timestamp: 1.0})
	activity.OnOverlapSpeechEnded(OverlappingSpeechEvent{
		IsInterruption: true,
		DetectedAt:     detectedAt,
	})

	select {
	case ev := <-events:
		if !ev.IsInterruption || !ev.DetectedAt.Equal(detectedAt) {
			t.Fatalf("OverlappingSpeechEvent = %#v, want interruption event with detector timestamp", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("OverlappingSpeechEvents did not receive overlap event")
	}

	activity.OnEndOfSpeech(&vad.VADEvent{Type: vad.VADEventEndOfSpeech, Timestamp: 1.5})

	if endpointing.endCount != 1 {
		t.Fatalf("OnEndOfSpeech calls = %d, want 1", endpointing.endCount)
	}
	if endpointing.lastShouldIgnore {
		t.Fatal("OnEndOfSpeech shouldIgnore = true, want false for overlap classified as interruption")
	}
}

func TestAgentActivityOverlapSpeechEndedIgnoresFalseInterruptionForEndpointing(t *testing.T) {
	endpointing := &recordingActivityEndpointing{}
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{Endpointing: endpointing})
	session.UpdateAgentState(AgentStateSpeaking)
	activity := NewAgentActivity(agent, session)

	activity.OnStartOfSpeech(&vad.VADEvent{Type: vad.VADEventStartOfSpeech, Timestamp: 1.0})
	activity.OnOverlapSpeechEnded(OverlappingSpeechEvent{IsInterruption: false})
	activity.OnEndOfSpeech(&vad.VADEvent{Type: vad.VADEventEndOfSpeech, Timestamp: 1.5})

	if endpointing.endCount != 1 {
		t.Fatalf("OnEndOfSpeech calls = %d, want 1", endpointing.endCount)
	}
	if !endpointing.lastShouldIgnore {
		t.Fatal("OnEndOfSpeech shouldIgnore = false, want true for overlap classified as non-interruption while agent was speaking")
	}
}

func TestAgentActivitySuppressesBackchannelOverlapDuringStartBoundary(t *testing.T) {
	endpointing := &recordingActivityEndpointing{}
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{Endpointing: endpointing})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	events := session.OverlappingSpeechEvents()

	session.UpdateAgentState(AgentStateSpeaking)
	activity.OnStartOfSpeech(&vad.VADEvent{Type: vad.VADEventStartOfSpeech, Timestamp: 1.0})
	activity.OnOverlapSpeechEnded(OverlappingSpeechEvent{IsInterruption: false})
	activity.OnEndOfSpeech(&vad.VADEvent{Type: vad.VADEventEndOfSpeech, Timestamp: 1.5})

	select {
	case ev := <-events:
		t.Fatalf("unexpected backchannel overlap event during start boundary: %#v", ev)
	default:
	}
	if endpointing.endCount != 1 {
		t.Fatalf("OnEndOfSpeech calls = %d, want 1", endpointing.endCount)
	}
	if endpointing.lastShouldIgnore {
		t.Fatal("OnEndOfSpeech shouldIgnore = true, want false for suppressed backchannel overlap")
	}

	detectedAt := time.Unix(50, 0)
	activity.OnOverlapSpeechEnded(OverlappingSpeechEvent{
		IsInterruption: true,
		DetectedAt:     detectedAt,
	})
	select {
	case ev := <-events:
		if !ev.IsInterruption || !ev.DetectedAt.Equal(detectedAt) {
			t.Fatalf("interruption overlap event = %#v, want true interruption during boundary", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("OverlappingSpeechEvents did not receive true interruption during start boundary")
	}
}

func TestAgentActivityOnInterruptionCutsCurrentSpeech(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection:           TurnDetectionModeVAD,
		MinInterruptionDuration: 0.05,
	})
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current

	activity.OnInterruption(OverlappingSpeechEvent{
		IsInterruption: true,
		DetectedAt:     time.Now(),
	})

	waitForInterrupted(t, current)
	current.MarkDone()
}

func TestAgentActivityOnInterruptionPauseUsesOverlapTimestampForHeldTranscripts(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection:               TurnDetectionModeVAD,
		MinInterruptionDuration:     0.05,
		FalseInterruptionTimeout:    0.01,
		FalseInterruptionTimeoutSet: true,
		ResumeFalseInterruption:     true,
		ResumeFalseInterruptionSet:  true,
	})
	session.SetAudioOutputController(&recordingAudioOutputController{canPause: true})
	session.agentState = AgentStateSpeaking
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	defer current.MarkDone()
	overlapStartedAt := time.Unix(200, 250_000_000)
	detectedAt := overlapStartedAt.Add(750 * time.Millisecond)

	activity.OnInterruption(OverlappingSpeechEvent{
		IsInterruption:   true,
		DetectedAt:       detectedAt,
		OverlapStartedAt: &overlapStartedAt,
	})

	wantIgnoreUntil := overlapStartedAt.Add(-time.Second)
	if !activity.ignoreUserTranscriptUntil.Equal(wantIgnoreUntil) {
		t.Fatalf("ignoreUserTranscriptUntil = %v, want overlap start minus cooldown %v", activity.ignoreUserTranscriptUntil, wantIgnoreUntil)
	}
}

func TestAgentActivityOnInterruptionFlushesHeldSTTWithOverlapCutoff(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection:              TurnDetectionModeVAD,
		BackchannelBoundaryEnd:     0,
		BackchannelBoundaryEndSet:  true,
		MinInterruptionDuration:    0.05,
		MinInterruptionDurationSet: true,
	})
	session.agentState = AgentStateSpeaking
	activity := NewAgentActivity(agent, session)
	activity.holdSTTWhileAgentSpeaking = true
	activity.userSpeechStartedAt = time.Unix(100, 0)
	userTranscriptEvents := session.UserInputTranscribedEvents()
	overlapStartedAt := activity.userSpeechStartedAt.Add(2 * time.Second)

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{{
			Text:       "assistant overlap",
			EndTime:    0.5,
			Confidence: 0.9,
		}},
	})
	if len(activity.heldSTTEvents) != 1 {
		t.Fatalf("held STT events = %d, want 1 buffered while agent speaking", len(activity.heldSTTEvents))
	}

	activity.OnInterruption(OverlappingSpeechEvent{
		IsInterruption:   true,
		DetectedAt:       overlapStartedAt.Add(250 * time.Millisecond),
		OverlapStartedAt: &overlapStartedAt,
	})

	if len(activity.heldSTTEvents) != 0 {
		t.Fatalf("held STT events = %d, want flushed after interruption", len(activity.heldSTTEvents))
	}
	select {
	case ev := <-userTranscriptEvents:
		t.Fatalf("unexpected stale held transcript after interruption flush: %#v", ev)
	default:
	}
}

func TestAgentActivityHoldsPreflightTranscriptWhileAgentSpeaking(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection:              TurnDetectionModeVAD,
		MinInterruptionDuration:    0.05,
		MinInterruptionDurationSet: true,
	})
	session.agentState = AgentStateSpeaking
	activity := NewAgentActivity(agent, session)
	activity.holdSTTWhileAgentSpeaking = true
	userTranscriptEvents := session.UserInputTranscribedEvents()

	activity.OnInterimTranscript(&stt.SpeechEvent{
		Type: stt.SpeechEventPreflightTranscript,
		Alternatives: []stt.SpeechData{{
			Language:   "id",
			Text:       "halo dari user",
			Confidence: 0.8,
		}},
	})

	if len(activity.heldSTTEvents) != 1 {
		t.Fatalf("held STT events = %d, want preflight transcript buffered while agent speaking", len(activity.heldSTTEvents))
	}
	select {
	case ev := <-userTranscriptEvents:
		t.Fatalf("preflight transcript emitted before hold released: %#v", ev)
	default:
	}

	session.agentState = AgentStateListening
	activity.flushHeldSTTEvents()

	if len(activity.heldSTTEvents) != 0 {
		t.Fatalf("held STT events = %d, want flushed", len(activity.heldSTTEvents))
	}
	select {
	case ev := <-userTranscriptEvents:
		if ev.Transcript != "halo dari user" || ev.IsFinal {
			t.Fatalf("flushed preflight event = %#v, want non-final transcript", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("held preflight transcript was not emitted after flush")
	}
}

func TestAgentActivityHoldsSTTStartOfSpeechWhileAgentSpeaking(t *testing.T) {
	endpointing := &recordingActivityEndpointing{}
	agent := NewAgent("test")
	agent.STT = &fakePipelineSTT{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection: TurnDetectionModeSTT,
		Endpointing:   endpointing,
	})
	session.agentState = AgentStateSpeaking
	activity := NewAgentActivity(agent, session)
	activity.holdSTTWhileAgentSpeaking = true
	startedAt := 123.4

	held := activity.holdSTTEventWhileAgentSpeaking(&stt.SpeechEvent{
		Type:            stt.SpeechEventStartOfSpeech,
		SpeechStartTime: &startedAt,
	})

	if !held {
		t.Fatal("STT start_of_speech was not held while agent speaking")
	}
	if len(activity.heldSTTEvents) != 1 {
		t.Fatalf("held STT events = %d, want start_of_speech buffered while agent speaking", len(activity.heldSTTEvents))
	}
	if endpointing.startCount != 0 {
		t.Fatalf("endpointing start count = %d, want held start_of_speech delayed", endpointing.startCount)
	}

	session.agentState = AgentStateListening
	activity.flushHeldSTTEvents()

	if endpointing.startCount != 1 {
		t.Fatalf("endpointing start count after flush = %d, want 1", endpointing.startCount)
	}
	if endpointing.lastStart != startedAt {
		t.Fatalf("endpointing start = %v, want %v", endpointing.lastStart, startedAt)
	}
}

func TestAgentActivityOnVADInferenceDoneInterruptsCurrentSpeech(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection:           TurnDetectionModeVAD,
		MinInterruptionDuration: 0.05,
	})
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current

	activity.OnVADInferenceDone(&vad.VADEvent{
		Type:                  vad.VADEventInferenceDone,
		SpeechDuration:        0.06,
		Speaking:              true,
		RawAccumulatedSilence: 0,
	})

	waitForInterrupted(t, current)
	current.MarkDone()
}

func TestAgentActivityVADInferenceRawSpeechSeedsTurnTiming(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	beforeInference := time.Now()
	activity.OnVADInferenceDone(&vad.VADEvent{
		Type:                 vad.VADEventInferenceDone,
		SpeechDuration:       0.1,
		RawAccumulatedSpeech: 0.3,
	})

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Text:       "vad timed",
			Confidence: 0.9,
		}},
	})

	info := activity.pendingFinalEndOfTurnInfo()
	if info.StartedSpeakingAt == nil {
		t.Fatal("StartedSpeakingAt = nil, want raw VAD speech start")
	}
	started := unixSecondsToTime(*info.StartedSpeakingAt)
	if started.After(beforeInference.Add(-250 * time.Millisecond)) {
		t.Fatalf("StartedSpeakingAt = %v, want at least 250ms before inference", started.Sub(beforeInference))
	}
	if started.Before(beforeInference.Add(-500 * time.Millisecond)) {
		t.Fatalf("StartedSpeakingAt = %v, want close to raw VAD speech start", started.Sub(beforeInference))
	}
	if info.StoppedSpeakingAt == nil {
		t.Fatal("StoppedSpeakingAt = nil, want raw VAD last-speaking time")
	}
	stopped := unixSecondsToTime(*info.StoppedSpeakingAt)
	if stopped.Before(beforeInference.Add(-50*time.Millisecond)) || stopped.After(time.Now().Add(50*time.Millisecond)) {
		t.Fatalf("StoppedSpeakingAt = %v, want near VAD inference time", stopped.Sub(beforeInference))
	}
}

func TestAgentActivityOnVADInferenceDonePausesFalseInterruption(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection:               TurnDetectionModeVAD,
		MinInterruptionDuration:     0.05,
		FalseInterruptionTimeout:    0.01,
		FalseInterruptionTimeoutSet: true,
		ResumeFalseInterruption:     true,
		ResumeFalseInterruptionSet:  true,
	})
	audioOutput := &recordingAudioOutputController{canPause: true}
	session.SetAudioOutputController(audioOutput)
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	session.agentState = AgentStateSpeaking
	falseInterruptions := session.AgentFalseInterruptionEvents()

	activity.OnVADInferenceDone(&vad.VADEvent{
		Type:                  vad.VADEventInferenceDone,
		SpeechDuration:        0.06,
		Speaking:              true,
		RawAccumulatedSilence: 0,
	})

	if audioOutput.pauseCount != 1 {
		t.Fatalf("PauseAudioOutput calls = %d, want 1", audioOutput.pauseCount)
	}
	if current.IsInterrupted() {
		t.Fatal("current speech was interrupted instead of paused for resumable false interruption")
	}
	if got := session.AgentState(); got != AgentStateListening {
		t.Fatalf("AgentState() after pause = %q, want %q", got, AgentStateListening)
	}

	activity.OnEndOfSpeech(&vad.VADEvent{Type: vad.VADEventEndOfSpeech})

	select {
	case ev := <-falseInterruptions:
		if !ev.Resumed {
			t.Fatalf("AgentFalseInterruptionEvent.Resumed = false, want true")
		}
	case <-time.After(time.Second):
		t.Fatal("AgentFalseInterruptionEvents did not receive resumed event")
	}
	if audioOutput.resumeCount != 1 {
		t.Fatalf("ResumeAudioOutput calls = %d, want 1", audioOutput.resumeCount)
	}
	if got := session.AgentState(); got != AgentStateSpeaking {
		t.Fatalf("AgentState() after false interruption resume = %q, want %q", got, AgentStateSpeaking)
	}
	current.MarkDone()
}

func TestAgentActivityVADInferenceDoneIgnoresAfterBackchannelBoundaryExpires(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection:           TurnDetectionModeVAD,
		MinInterruptionDuration: 0.05,
	})
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	defer current.MarkDone()

	activity.OnVADInferenceDone(&vad.VADEvent{
		Type:                  vad.VADEventInferenceDone,
		SpeechDuration:        0.06,
		Speaking:              true,
		RawAccumulatedSilence: 0,
	})
	waitForInterrupted(t, current)
	current.MarkDone()

	current = NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	defer current.MarkDone()
	session.agentState = AgentStateSpeaking
	activity.armBackchannelBoundary(time.Now().Add(-2 * time.Second))
	activity.OnVADInferenceDone(&vad.VADEvent{
		Type:                  vad.VADEventInferenceDone,
		SpeechDuration:        0.06,
		Speaking:              true,
		RawAccumulatedSilence: 0,
	})

	select {
	case <-current.interruptCh:
		t.Fatal("current speech was interrupted by VAD after backchannel boundary expired")
	case <-time.After(100 * time.Millisecond):
	}

	activity.OnInterruption(OverlappingSpeechEvent{
		IsInterruption: true,
		DetectedAt:     time.Now(),
	})
	waitForInterrupted(t, current)
}

func TestAgentActivityAudioInterruptionAppliesBeforeReturning(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection:           TurnDetectionModeVAD,
		MinInterruptionDuration: 0.05,
	})
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	defer current.MarkDone()

	activity.queueMu.Lock()
	returned := make(chan struct{})
	go func() {
		activity.OnVADInferenceDone(&vad.VADEvent{
			Type:                  vad.VADEventInferenceDone,
			SpeechDuration:        0.06,
			Speaking:              true,
			RawAccumulatedSilence: 0,
		})
		close(returned)
	}()

	select {
	case <-returned:
		activity.queueMu.Unlock()
		t.Fatal("OnVADInferenceDone returned before applying interruption")
	case <-time.After(20 * time.Millisecond):
	}

	activity.queueMu.Unlock()
	select {
	case <-returned:
	case <-testTimeout():
		t.Fatal("OnVADInferenceDone did not return after queue lock released")
	}
	if !current.IsInterrupted() {
		t.Fatal("current speech was not interrupted before OnVADInferenceDone returned")
	}
}

func TestAgentActivityVADInferenceDoneIgnoresWithoutBackchannelBoundaryStart(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection:               TurnDetectionModeVAD,
		MinInterruptionDuration:     0.05,
		BackchannelBoundaryStart:    0,
		BackchannelBoundaryStartSet: true,
	})
	activity := NewAgentActivity(agent, session)

	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	activity.OnVADInferenceDone(&vad.VADEvent{
		Type:                  vad.VADEventInferenceDone,
		SpeechDuration:        0.06,
		Speaking:              true,
		RawAccumulatedSilence: 0,
	})
	waitForInterrupted(t, current)
	current.MarkDone()

	current = NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	defer current.MarkDone()
	session.agentState = AgentStateSpeaking
	activity.armBackchannelBoundary(time.Now())
	activity.OnVADInferenceDone(&vad.VADEvent{
		Type:                  vad.VADEventInferenceDone,
		SpeechDuration:        0.06,
		Speaking:              true,
		RawAccumulatedSilence: 0,
	})

	select {
	case <-current.interruptCh:
		t.Fatal("current speech was interrupted by VAD without an active backchannel boundary")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestAgentActivityOnVADInferenceDoneRespectsMinInterruptionWordsWithoutTranscript(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	agent.STT = &fakePipelineSTT{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection:           TurnDetectionModeVAD,
		MinInterruptionDuration: 0.05,
		MinInterruptionWords:    2,
	})
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current

	activity.OnVADInferenceDone(&vad.VADEvent{
		Type:                  vad.VADEventInferenceDone,
		SpeechDuration:        0.06,
		Speaking:              true,
		RawAccumulatedSilence: 0,
	})

	select {
	case <-current.interruptCh:
		t.Fatal("current speech was interrupted by VAD before transcript reached MinInterruptionWords")
	case <-time.After(20 * time.Millisecond):
	}
	current.MarkDone()
}

func TestAgentActivityOnVADInferenceDoneInterruptsAfterMinInterruptionWordsTranscript(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	agent.STT = &fakePipelineSTT{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection:           TurnDetectionModeVAD,
		MinInterruptionDuration: 0.05,
		MinInterruptionWords:    2,
	})
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	activity.OnInterimTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "wait now"}},
	})

	activity.OnVADInferenceDone(&vad.VADEvent{
		Type:                  vad.VADEventInferenceDone,
		SpeechDuration:        0.06,
		Speaking:              true,
		RawAccumulatedSilence: 0,
	})

	waitForInterrupted(t, current)
	current.MarkDone()
}

func TestAgentActivityOnVADInferenceDoneIgnoresAECWarmup(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection:           TurnDetectionModeVAD,
		MinInterruptionDuration: 0.05,
		AECWarmupDuration:       0.02,
	})
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current

	session.UpdateAgentState(AgentStateSpeaking)
	activity.OnVADInferenceDone(&vad.VADEvent{
		Type:                  vad.VADEventInferenceDone,
		SpeechDuration:        0.06,
		Speaking:              true,
		RawAccumulatedSilence: 0,
	})

	select {
	case <-current.interruptCh:
		t.Fatal("speech was interrupted during AEC warmup")
	case <-time.After(10 * time.Millisecond):
	}

	waitForAECWarmupInactive(t, session)
	activity.OnVADInferenceDone(&vad.VADEvent{
		Type:                  vad.VADEventInferenceDone,
		SpeechDuration:        0.06,
		Speaking:              true,
		RawAccumulatedSilence: 0,
	})

	waitForInterrupted(t, current)
	current.MarkDone()
}

func TestAgentActivityOnVADInferenceDoneIgnoresManualTurnDetection(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection:           TurnDetectionModeManual,
		MinInterruptionDuration: 0.05,
	})
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current

	activity.OnVADInferenceDone(&vad.VADEvent{
		Type:                  vad.VADEventInferenceDone,
		SpeechDuration:        0.06,
		Speaking:              true,
		RawAccumulatedSilence: 0,
	})

	if current.IsInterrupted() {
		t.Fatal("current speech was interrupted for manual turn detection")
	}
	current.MarkDone()
}

func TestAgentActivityUserTurnExceededWaitDoesNotConsumeLegacyAgentStateChannel(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	activity.currentSpeech = NewSpeechHandle(true, DefaultInputDetails())
	sentinel := AgentStateChangedEvent{
		OldState: AgentState("sentinel"),
		NewState: AgentStateSpeaking,
	}
	session.AgentStateChangedCh <- sentinel

	result := make(chan bool, 1)
	errs := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		shouldRun, err := activity.waitForUserTurnExceededCallback(ctx)
		result <- shouldRun
		errs <- err
	}()

	var shouldRun bool
	select {
	case shouldRun = <-result:
	case <-time.After(20 * time.Millisecond):
		session.UpdateAgentState(AgentStateSpeaking)
		select {
		case shouldRun = <-result:
		case <-time.After(time.Second):
			t.Fatal("waitForUserTurnExceededCallback did not return after agent started speaking")
		}
	}
	if shouldRun {
		t.Fatal("waitForUserTurnExceededCallback() = true, want false when agent starts speaking")
	}
	if err := <-errs; err != nil {
		t.Fatalf("waitForUserTurnExceededCallback() error = %v, want nil", err)
	}
	select {
	case ev := <-session.AgentStateChangedCh:
		if ev.OldState != sentinel.OldState || ev.NewState != sentinel.NewState {
			t.Fatalf("legacy agent state event = %#v, want sentinel event %#v", ev, sentinel)
		}
	default:
		t.Fatal("waitForUserTurnExceededCallback consumed the legacy agent state channel event")
	}
}

func TestAgentActivityFinalTranscriptEmitsUserTurnExceededAtMaxWords(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{UserTurnLimitMaxWords: 3})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	defer activity.Stop()
	events := session.UserTurnExceededEvents()

	activity.OnStartOfSpeech(&vad.VADEvent{Type: vad.VADEventStartOfSpeech})
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "one two", Confidence: 0.9}},
	})
	select {
	case ev := <-events:
		t.Fatalf("UserTurnExceeded emitted early: %#v", ev)
	default:
	}

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "three", Confidence: 0.9}},
	})
	select {
	case ev := <-events:
		if ev.Transcript != "three" || ev.AccumulatedTranscript != "one two three" || ev.AccumulatedWordCount != 3 {
			t.Fatalf("UserTurnExceededEvent = %#v, want latest three with accumulated one two three/3", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("UserTurnExceededEvents did not receive word-limit event")
	}

	session.UpdateAgentState(AgentStateSpeaking)
	session.UpdateAgentState(AgentStateListening)
	activity.OnStartOfSpeech(&vad.VADEvent{Type: vad.VADEventStartOfSpeech})
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "one two", Confidence: 0.9}},
	})
	select {
	case ev := <-events:
		t.Fatalf("UserTurnExceeded emitted after agent speech reset: %#v", ev)
	default:
	}
}

func TestAgentActivityRealtimeInputSpeechStoppedEmitsInterimTranscriptWhenEnabled(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	userTranscriptEvents := session.UserInputTranscribedEvents()

	activity.OnInputSpeechStopped(llm.InputSpeechStoppedEvent{UserTranscriptionEnabled: true})

	select {
	case ev := <-userTranscriptEvents:
		if ev.Transcript != "" || ev.IsFinal {
			t.Fatalf("UserInputTranscribedEvent = %#v, want empty interim transcript", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("UserInputTranscribedEvents did not receive empty interim transcript")
	}
}

func TestAgentActivityInputAudioTranscriptionCompletedCommitsFinalMessage(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	userTranscriptEvents := session.UserInputTranscribedEvents()

	activity.OnInputAudioTranscriptionCompleted(llm.InputTranscriptionCompleted{
		ItemID:     "item_user_1",
		Transcript: "hello realtime",
		IsFinal:    true,
	})

	transcriptEvent := receiveUserInputTranscribedEvent(t, userTranscriptEvents)
	if transcriptEvent.Transcript != "hello realtime" || !transcriptEvent.IsFinal {
		t.Fatalf("UserInputTranscribedEvent = %#v, want final hello realtime", transcriptEvent)
	}

	select {
	case ev := <-session.ConversationItemAddedEvents():
		msg, ok := ev.Item.(*llm.ChatMessage)
		if !ok {
			t.Fatalf("ConversationItemAdded item = %T, want *llm.ChatMessage", ev.Item)
		}
		if msg.ID != "item_user_1" || msg.Role != llm.ChatRoleUser || msg.TextContent() != "hello realtime" {
			t.Fatalf("message = %#v, want committed user message with realtime transcript", msg)
		}
		if agent.ChatCtx.GetByID("item_user_1") != msg {
			t.Fatalf("agent chat context item = %#v, want committed message", agent.ChatCtx.GetByID("item_user_1"))
		}
		if session.ChatCtx.GetByID("item_user_1") != msg {
			t.Fatalf("session chat context item = %#v, want committed message", session.ChatCtx.GetByID("item_user_1"))
		}
	case <-time.After(time.Second):
		t.Fatal("ConversationItemAddedEvents did not receive realtime user message")
	}
}

func TestAgentActivityInputAudioTranscriptionCompletedSkipsInterimMessage(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	userTranscriptEvents := session.UserInputTranscribedEvents()

	activity.OnInputAudioTranscriptionCompleted(llm.InputTranscriptionCompleted{
		ItemID:     "item_user_1",
		Transcript: "hello",
		IsFinal:    false,
	})

	transcriptEvent := receiveUserInputTranscribedEvent(t, userTranscriptEvents)
	if transcriptEvent.Transcript != "hello" || transcriptEvent.IsFinal {
		t.Fatalf("UserInputTranscribedEvent = %#v, want interim hello", transcriptEvent)
	}
	select {
	case ev := <-session.ConversationItemAddedEvents():
		t.Fatalf("unexpected conversation item for interim transcript: %#v", ev)
	default:
	}
	if agent.ChatCtx.GetByID("item_user_1") != nil {
		t.Fatalf("agent chat context item = %#v, want none", agent.ChatCtx.GetByID("item_user_1"))
	}
	if session.ChatCtx.GetByID("item_user_1") != nil {
		t.Fatalf("session chat context item = %#v, want none", session.ChatCtx.GetByID("item_user_1"))
	}
}

func TestAgentActivityRemoteItemAddedAppendsServerPlaceholder(t *testing.T) {
	existing := &llm.ChatMessage{
		ID:        "item_user_1",
		Role:      llm.ChatRoleUser,
		Content:   []llm.ChatContent{{Text: "hello"}},
		CreatedAt: time.Now(),
	}
	remote := &llm.ChatMessage{
		ID:        "item_assistant_1",
		Role:      llm.ChatRoleAssistant,
		Content:   []llm.ChatContent{{Text: "hi"}},
		CreatedAt: existing.CreatedAt.Add(time.Second),
	}
	agent := NewAgent("test")
	agent.ChatCtx.Insert(existing)
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	activity.OnRemoteItemAdded(llm.RemoteItemAddedEvent{
		PreviousItemID: "item_user_1",
		Item:           remote,
	})

	if len(agent.ChatCtx.Items) != 2 || agent.ChatCtx.Items[1] != remote {
		t.Fatalf("agent chat context items = %#v, want remote item appended after previous item", agent.ChatCtx.Items)
	}
	select {
	case ev := <-session.ConversationItemAddedEvents():
		t.Fatalf("unexpected conversation item event for remote placeholder: %#v", ev)
	default:
	}
}

func TestAgentActivityRemoteItemAddedSkipsDuplicatePlaceholder(t *testing.T) {
	remote := &llm.ChatMessage{
		ID:        "item_assistant_1",
		Role:      llm.ChatRoleAssistant,
		Content:   []llm.ChatContent{{Text: "hi"}},
		CreatedAt: time.Now(),
	}
	agent := NewAgent("test")
	agent.ChatCtx.Insert(remote)
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	activity.OnRemoteItemAdded(llm.RemoteItemAddedEvent{
		Item: remote,
	})

	if len(agent.ChatCtx.Items) != 1 {
		t.Fatalf("agent chat context items = %#v, want duplicate remote item skipped", agent.ChatCtx.Items)
	}
}

func TestAgentActivityMetricsCollectedEmitsMetricsAndUsage(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	metrics := &telemetry.RealtimeModelMetrics{
		RequestID:    "req_1",
		InputTokens:  3,
		OutputTokens: 5,
		TotalTokens:  8,
	}

	activity.OnMetricsCollected(metrics)

	select {
	case ev := <-session.MetricsCollectedEvents():
		if ev.Metrics != metrics {
			t.Fatalf("MetricsCollectedEvent metrics = %#v, want original metrics", ev.Metrics)
		}
	case <-time.After(time.Second):
		t.Fatal("MetricsCollectedEvents did not receive realtime metrics")
	}
	select {
	case ev := <-session.SessionUsageUpdatedEvents():
		if ev.Usage.LLMInputTokens() != 3 || ev.Usage.LLMOutputTokens() != 5 {
			t.Fatalf("SessionUsageUpdatedEvent usage = %#v, want realtime token usage", ev.Usage)
		}
	case <-time.After(time.Second):
		t.Fatal("SessionUsageUpdatedEvents did not receive realtime usage")
	}
}

func TestAgentActivityMetricsCollectedAddsCurrentSpeechID(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	llmMetrics := &telemetry.LLMMetrics{RequestID: "llm_req"}
	ttsMetrics := &telemetry.TTSMetrics{RequestID: "tts_req"}

	activity.OnMetricsCollected(llmMetrics)
	activity.OnMetricsCollected(ttsMetrics)

	if llmMetrics.SpeechID != current.ID {
		t.Fatalf("LLMMetrics SpeechID = %q, want current speech %q", llmMetrics.SpeechID, current.ID)
	}
	if ttsMetrics.SpeechID != current.ID {
		t.Fatalf("TTSMetrics SpeechID = %q, want current speech %q", ttsMetrics.SpeechID, current.ID)
	}
}

func TestAgentActivityErrorEmitsSessionErrorEvent(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	cause := errors.New("realtime failed")
	source := &fakeRealtimeModel{label: "test.RealtimeModel"}

	activity.OnError(cause, source)

	select {
	case ev := <-session.ErrorEvents():
		if !errors.Is(ev.Error, cause) {
			t.Fatalf("Error = %v, want %v", ev.Error, cause)
		}
		if ev.Source != source {
			t.Fatalf("Source = %#v, want realtime source", ev.Source)
		}
		if ev.CreatedAt.IsZero() {
			t.Fatal("CreatedAt is zero")
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive activity error")
	}
}

func TestAgentActivityGenerationCreatedSkipsSpeechWhenSchedulingPaused(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	activity.PauseScheduling()

	handle, err := activity.OnGenerationCreated(llm.GenerationCreatedEvent{
		ResponseID:    "response_1",
		UserInitiated: false,
	})

	if !errors.Is(err, ErrSpeechSchedulingPaused) {
		t.Fatalf("OnGenerationCreated error = %v, want ErrSpeechSchedulingPaused", err)
	}
	if handle != nil {
		t.Fatalf("OnGenerationCreated handle = %#v, want nil when scheduling paused", handle)
	}
	select {
	case ev := <-session.SpeechCreatedEvents():
		t.Fatalf("unexpected SpeechCreated event while scheduling paused: %#v", ev)
	default:
	}
}

func TestAgentActivityGenerationCreatedEmitsAndSchedulesSpeech(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{AllowInterruptions: true})
	activity := NewAgentActivity(agent, session)
	generation := llm.GenerationCreatedEvent{
		ResponseID:    "response_1",
		UserInitiated: false,
	}

	handle, err := activity.OnGenerationCreated(generation)
	if err != nil {
		t.Fatalf("OnGenerationCreated error = %v, want nil", err)
	}
	if handle == nil {
		t.Fatal("OnGenerationCreated handle = nil, want speech handle")
	}
	if handle.Generation.RealtimeGeneration == nil || handle.Generation.RealtimeGeneration.ResponseID != "response_1" {
		t.Fatalf("RealtimeGeneration = %#v, want response_1", handle.Generation.RealtimeGeneration)
	}

	select {
	case ev := <-session.SpeechCreatedEvents():
		if ev.SpeechHandle != handle || ev.UserInitiated || ev.Source != "generate_reply" {
			t.Fatalf("SpeechCreatedEvent = %#v, want server generate_reply handle", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("SpeechCreatedEvents did not receive realtime generation")
	}
	scheduleCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handle.WaitForScheduled(scheduleCtx); err != nil {
		t.Fatalf("speech handle was not scheduled: %v", err)
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

func TestAgentActivityRealtimeLLMTurnDetectionRequiresRealtimeModel(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{TurnDetection: TurnDetectionModeRealtimeLLM})
	activity := NewAgentActivity(agent, session)

	if got := activity.turnDetectionMode(); got != "" {
		t.Fatalf("turnDetectionMode() = %q, want reference fallback when no realtime model exists", got)
	}
	if !activity.vadBasedTurnDetection() {
		t.Fatal("vadBasedTurnDetection() = false, want VAD fallback after ignored realtime_llm mode")
	}
}

func TestAgentActivityRealtimeLLMTurnDetectionUsesRealtimeCapabilities(t *testing.T) {
	t.Run("server turn detection", func(t *testing.T) {
		agent := NewAgent("test")
		agent.RealtimeModel = &fakeRealtimeModel{capabilities: llm.RealtimeCapabilities{TurnDetection: true}}
		session := NewAgentSession(agent, nil, AgentSessionOptions{TurnDetection: TurnDetectionModeRealtimeLLM})
		activity := NewAgentActivity(agent, session)

		if got := activity.turnDetectionMode(); got != TurnDetectionModeRealtimeLLM {
			t.Fatalf("turnDetectionMode() = %q, want realtime_llm when realtime model supports turn detection", got)
		}
	})

	t.Run("realtime without server turn detection falls back to vad", func(t *testing.T) {
		agent := NewAgent("test")
		agent.VAD = &fakePipelineVAD{}
		agent.RealtimeModel = &fakeRealtimeModel{capabilities: llm.RealtimeCapabilities{TurnDetection: false}}
		session := NewAgentSession(agent, nil, AgentSessionOptions{TurnDetection: TurnDetectionModeRealtimeLLM})
		activity := NewAgentActivity(agent, session)

		if got := activity.turnDetectionMode(); got != TurnDetectionModeVAD {
			t.Fatalf("turnDetectionMode() = %q, want vad fallback when realtime model lacks turn detection", got)
		}
	})
}

func TestAgentActivityRealtimeModelIgnoresLocalTurnDetection(t *testing.T) {
	t.Run("server turn detection ignores stt", func(t *testing.T) {
		agent := NewAgent("test")
		agent.STT = &fakePipelineSTT{}
		agent.RealtimeModel = &fakeRealtimeModel{capabilities: llm.RealtimeCapabilities{TurnDetection: true}}
		session := NewAgentSession(agent, nil, AgentSessionOptions{TurnDetection: TurnDetectionModeSTT})
		activity := NewAgentActivity(agent, session)

		if got := activity.turnDetectionMode(); got != "" {
			t.Fatalf("turnDetectionMode() = %q, want local STT ignored while realtime server turn detection is enabled", got)
		}
	})

	t.Run("realtime without server turn detection falls back from stt to vad", func(t *testing.T) {
		agent := NewAgent("test")
		agent.STT = &fakePipelineSTT{}
		agent.VAD = &fakePipelineVAD{}
		agent.RealtimeModel = &fakeRealtimeModel{capabilities: llm.RealtimeCapabilities{TurnDetection: false}}
		session := NewAgentSession(agent, nil, AgentSessionOptions{TurnDetection: TurnDetectionModeSTT})
		activity := NewAgentActivity(agent, session)

		if got := activity.turnDetectionMode(); got != TurnDetectionModeVAD {
			t.Fatalf("turnDetectionMode() = %q, want VAD fallback when realtime model lacks server turn detection", got)
		}
	})

	t.Run("realtime without server turn detection ignores stt when vad missing", func(t *testing.T) {
		agent := NewAgent("test")
		agent.STT = &fakePipelineSTT{}
		agent.RealtimeModel = &fakeRealtimeModel{capabilities: llm.RealtimeCapabilities{TurnDetection: false}}
		session := NewAgentSession(agent, nil, AgentSessionOptions{TurnDetection: TurnDetectionModeSTT})
		activity := NewAgentActivity(agent, session)

		if got := activity.turnDetectionMode(); got != "" {
			t.Fatalf("turnDetectionMode() = %q, want no local STT mode for realtime model without VAD fallback", got)
		}
	})

	t.Run("server turn detection ignores vad", func(t *testing.T) {
		agent := NewAgent("test")
		agent.VAD = &fakePipelineVAD{}
		agent.RealtimeModel = &fakeRealtimeModel{capabilities: llm.RealtimeCapabilities{TurnDetection: true}}
		session := NewAgentSession(agent, nil, AgentSessionOptions{TurnDetection: TurnDetectionModeVAD})
		activity := NewAgentActivity(agent, session)

		if got := activity.turnDetectionMode(); got != "" {
			t.Fatalf("turnDetectionMode() = %q, want local VAD ignored while realtime server turn detection is enabled", got)
		}
	})
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

func TestAgentActivityInterruptCancelsPreemptiveGeneration(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	speech := NewSpeechHandle(true, DefaultInputDetails())
	activity.preemptiveGeneration = &preemptiveGeneration{
		speech:     speech,
		transcript: "hello",
		chatCtx:    llm.NewChatContext(),
		createdAt:  time.Now(),
	}

	if err := activity.Interrupt(false); err != nil {
		t.Fatalf("Interrupt(false) error = %v, want nil", err)
	}

	if activity.preemptiveGeneration != nil {
		t.Fatal("preemptiveGeneration still set after interrupt, want cleared")
	}
	if !speech.IsInterrupted() {
		t.Fatal("preemptive speech was not interrupted")
	}
}

func TestAgentActivityInterruptInterruptsRealtimeSession(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	assistant := &recordingRealtimeCommitAssistant{}
	session.Assistant = assistant
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	if err := activity.Interrupt(false); err != nil {
		t.Fatalf("Interrupt(false) error = %v, want nil", err)
	}
	if assistant.interrupts != 1 {
		t.Fatalf("realtime Interrupt calls = %d, want 1", assistant.interrupts)
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
	if msg.CreatedAt.IsZero() {
		t.Fatal("instructions message CreatedAt is zero, want reference default timestamp")
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

func TestAgentActivityStartRecordsInitialMCPTools(t *testing.T) {
	agent := NewAgent("")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.SetMCPServers([]llm.MCPServer{
		&fakeActivityMCPServer{tools: []llm.Tool{&agentTestTool{id: "lookup", name: "lookup"}}},
	})
	activity := NewAgentActivity(agent, session)

	activity.Start()
	defer activity.Stop()

	if len(agent.ChatCtx.Items) == 0 {
		t.Fatal("agent chat context has no initial items, want MCP tool config")
	}
	config, ok := agent.ChatCtx.Items[len(agent.ChatCtx.Items)-1].(*llm.AgentConfigUpdate)
	if !ok {
		t.Fatalf("last agent chat item = %T, want config update", agent.ChatCtx.Items[len(agent.ChatCtx.Items)-1])
	}
	if !stringSlicesEqual(config.ToolsAdded, []string{"lookup"}) {
		t.Fatalf("config tools added = %q, want [lookup]", config.ToolsAdded)
	}
}

func TestAgentActivityStartRecordsFlattenedToolsetFunctionNames(t *testing.T) {
	lookup := &agentTestTool{id: "lookup", name: "lookup"}
	agent := NewAgent("")
	agent.Tools = []llm.Tool{&nestedAgentToolset{
		agentTestTool: agentTestTool{id: "wrapper", name: "wrapper"},
		tools:         []llm.Tool{lookup},
	}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	activity.Start()
	defer activity.Stop()

	if len(agent.ChatCtx.Items) == 0 {
		t.Fatal("agent chat context has no initial items, want tool config")
	}
	config, ok := agent.ChatCtx.Items[len(agent.ChatCtx.Items)-1].(*llm.AgentConfigUpdate)
	if !ok {
		t.Fatalf("last agent chat item = %T, want config update", agent.ChatCtx.Items[len(agent.ChatCtx.Items)-1])
	}
	if !stringSlicesEqual(config.ToolsAdded, []string{"lookup"}) {
		t.Fatalf("config tools added = %q, want flattened function tool names [lookup]", config.ToolsAdded)
	}
}

func TestAgentActivityStartLogsMCPToolsetSetupError(t *testing.T) {
	recorder := &recordingLogger{}
	oldLogger := logutil.Logger
	logutil.SetLogger(recorder)
	t.Cleanup(func() { logutil.SetLogger(oldLogger) })

	agent := NewAgent("")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.SetMCPServers([]llm.MCPServer{
		&fakeActivityMCPServer{initializeErr: errors.New("mcp unavailable")},
	})
	activity := NewAgentActivity(agent, session)

	activity.Start()
	defer activity.Stop()

	if !recorder.hasError("failed to record initial agent configuration") {
		t.Fatalf("error logs = %#v, want initial configuration failure", recorder.errorMessages)
	}
}

func TestAgentActivityStartRecordsInstructionVariants(t *testing.T) {
	agent := NewAgent("")
	agent.InstructionVariants = llm.NewInstructions("speak plainly", "write tersely")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	activity.Start()
	defer activity.Stop()

	if len(agent.ChatCtx.Items) == 0 {
		t.Fatal("agent chat context has no initial items, want instruction variants")
	}
	msg, ok := agent.ChatCtx.Items[0].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("first agent chat item = %T, want instructions message", agent.ChatCtx.Items[0])
	}
	if msg.ID != agentInstructionsMessageID || msg.Role != llm.ChatRoleSystem {
		t.Fatalf("instructions message = %#v, want synthetic system instructions", msg)
	}
	if len(msg.Content) != 1 || msg.Content[0].Instructions == nil {
		t.Fatalf("instructions content = %#v, want instruction variants", msg.Content)
	}
	if got := msg.Content[0].Instructions.AsModality("audio").String(); got != "speak plainly" {
		t.Fatalf("audio instructions = %q, want speak plainly", got)
	}
	if got := msg.Content[0].Instructions.AsModality("text").String(); got != "write tersely" {
		t.Fatalf("text instructions = %q, want write tersely", got)
	}

	config, ok := agent.ChatCtx.Items[len(agent.ChatCtx.Items)-1].(*llm.AgentConfigUpdate)
	if !ok {
		t.Fatalf("last agent chat item = %T, want config update", agent.ChatCtx.Items[len(agent.ChatCtx.Items)-1])
	}
	if config.Instructions == nil || *config.Instructions != "speak plainly" {
		t.Fatalf("config instructions = %v, want speak plainly", config.Instructions)
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

	activity.runEOUDetection(EndOfTurnInfo{NewTranscript: "hello", TranscriptConfidence: 0.9})

	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "hello" {
			t.Fatalf("turn message text = %q, want hello", msg.TextContent())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnUserTurnCompleted was not called after session min endpointing delay")
	}
}

func TestAgentActivityEOUDelayAnchorsToLastSpeechTime(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeSTT
	session := NewAgentSession(agent, nil, AgentSessionOptions{MinEndpointingDelay: 0.08})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()
	stopped := float64(time.Now().Add(-120*time.Millisecond).UnixNano()) / float64(time.Second)

	activity.runEOUDetection(EndOfTurnInfo{
		NewTranscript:        "already ended",
		TranscriptConfidence: 0.9,
		StoppedSpeakingAt:    &stopped,
	})

	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "already ended" {
			t.Fatalf("turn message text = %q, want already ended", msg.TextContent())
		}
	case <-time.After(30 * time.Millisecond):
		t.Fatal("OnUserTurnCompleted was not called immediately after endpointing delay had already elapsed")
	}
}

func TestAgentActivityPendingFinalUsesVADAdjustedSpeechEndTime(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	activity.userSpeechStartedAt = time.Now().Add(-time.Second)

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Text:       "timed by vad",
			Confidence: 0.9,
		}},
	})

	beforeEnd := time.Now()
	activity.OnEndOfSpeech(&vad.VADEvent{
		Type:              vad.VADEventEndOfSpeech,
		SilenceDuration:   0.4,
		InferenceDuration: 0.1,
	})

	info := activity.pendingFinalEndOfTurnInfo()
	if info.StoppedSpeakingAt == nil {
		t.Fatal("StoppedSpeakingAt = nil, want VAD-adjusted stop time")
	}
	stopped := unixSecondsToTime(*info.StoppedSpeakingAt)
	if stopped.After(beforeEnd.Add(-450 * time.Millisecond)) {
		t.Fatalf("StoppedSpeakingAt = %v, want at least 450ms before OnEndOfSpeech", stopped.Sub(beforeEnd))
	}
	if stopped.Before(beforeEnd.Add(-700 * time.Millisecond)) {
		t.Fatalf("StoppedSpeakingAt = %v, want close to VAD-adjusted end", stopped.Sub(beforeEnd))
	}
}

func TestAgentActivityFinalTranscriptEOUDelayUsesSTTEndTime(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.STT = &fakePipelineSTT{}
	agent.TurnDetection = TurnDetectionModeSTT
	session := NewAgentSession(agent, nil, AgentSessionOptions{MinEndpointingDelay: 0.08})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()
	activity.userSpeechStartedAt = time.Now().Add(-180 * time.Millisecond)

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Text:       "timestamped final",
			Confidence: 0.9,
			EndTime:    0.05,
		}},
	})
	activity.OnEndOfSpeech(nil)

	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "timestamped final" {
			t.Fatalf("turn message text = %q, want timestamped final", msg.TextContent())
		}
	case <-time.After(30 * time.Millisecond):
		t.Fatal("OnUserTurnCompleted waited a full endpointing delay instead of using STT end time")
	}
}

func TestAgentActivityRunEOUDetectionSkipsEmptyTranscript(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeVAD
	agent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{MinEndpointingDelay: 0.01})
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity
	defer activity.Stop()

	activity.runEOUDetection(EndOfTurnInfo{})

	select {
	case msg := <-agent.turns:
		t.Fatalf("OnUserTurnCompleted got message %q, want skipped empty transcript", msg.TextContent())
	case <-time.After(30 * time.Millisecond):
	}
	select {
	case ev := <-session.SpeechCreatedEvents():
		t.Fatalf("SpeechCreated event = %#v, want no reply for empty transcript", ev)
	case <-time.After(30 * time.Millisecond):
	}
	if agent.ChatCtx != nil && len(agent.ChatCtx.Items) != 0 {
		t.Fatalf("chat context items = %d, want none for empty transcript", len(agent.ChatCtx.Items))
	}
}

func TestAgentActivityVADTurnWithPipelineSTTNoTranscriptDoesNotStartEOU(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeVAD
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{MinEndpointingDelay: 0.2})
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity
	defer activity.Stop()

	session.Assistant = NewPipelineAgent(agent.VAD, &fakePipelineSTT{}, nil, nil, agent.ChatCtx)

	activity.OnStartOfSpeech(&vad.VADEvent{Timestamp: 1.0})
	activity.OnEndOfSpeech(&vad.VADEvent{Timestamp: 1.2})

	activity.eouMu.Lock()
	eouStarted := activity.eouDone != nil
	activity.eouMu.Unlock()
	if eouStarted {
		t.Fatal("EOU detection started for empty transcript with active pipeline STT")
	}
}

func TestAgentActivityVADTurnCompletesPendingFinalTranscriptAfterEndOfSpeech(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeVAD
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{MinEndpointingDelay: 0.01})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	activity.OnStartOfSpeech(&vad.VADEvent{Timestamp: 1.0})
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "wait for vad eos", Confidence: 0.9}},
	})

	select {
	case msg := <-agent.turns:
		t.Fatalf("OnUserTurnCompleted called before VAD end-of-speech with %q", msg.TextContent())
	case <-time.After(20 * time.Millisecond):
	}

	activity.OnEndOfSpeech(&vad.VADEvent{Timestamp: 1.5})

	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "wait for vad eos" {
			t.Fatalf("turn message text = %q, want wait for vad eos", msg.TextContent())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnUserTurnCompleted was not called after VAD end-of-speech")
	}
}

func TestAgentActivitySTTTurnWaitsForEndOfSpeechBeforeCommit(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeSTT
	agent.STT = &fakePipelineSTT{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{MinEndpointingDelay: 0.01})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	activity.OnStartOfSpeech(nil)
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "wait for stt eos", Confidence: 0.9}},
	})

	select {
	case msg := <-agent.turns:
		t.Fatalf("OnUserTurnCompleted called before STT end-of-speech with %q", msg.TextContent())
	case <-time.After(20 * time.Millisecond):
	}

	activity.OnEndOfSpeech(nil)

	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "wait for stt eos" {
			t.Fatalf("turn message text = %q, want wait for stt eos", msg.TextContent())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnUserTurnCompleted was not called after STT end-of-speech")
	}
}

func TestAgentActivitySTTFinalWithoutSpeakingWaitsForEndOfSpeechBeforeCommit(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeSTT
	agent.STT = &fakePipelineSTT{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{MinEndpointingDelay: 0.01})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "wait for server eos", Confidence: 0.9}},
	})

	select {
	case msg := <-agent.turns:
		t.Fatalf("OnUserTurnCompleted called before STT end-of-speech with %q", msg.TextContent())
	case <-time.After(20 * time.Millisecond):
	}

	activity.OnEndOfSpeech(nil)

	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "wait for server eos" {
			t.Fatalf("turn message text = %q, want wait for server eos", msg.TextContent())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnUserTurnCompleted was not called after STT end-of-speech")
	}
}

func TestAgentActivitySTTEndOfSpeechMarksEOSReceived(t *testing.T) {
	agent := NewAgent("test")
	agent.TurnDetection = TurnDetectionModeSTT
	agent.STT = &fakePipelineSTT{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	activity.OnSTTStartOfSpeech(&stt.SpeechEvent{})
	activity.OnEndOfSpeech(nil)

	if !activity.sttEOSReceived {
		t.Fatal("sttEOSReceived = false, want true after STT end-of-speech")
	}
}

func TestAgentActivityFinalTranscriptDoesNotMarkSTTEOSReceived(t *testing.T) {
	agent := NewAgent("test")
	agent.TurnDetection = TurnDetectionModeSTT
	agent.STT = &fakePipelineSTT{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	activity.OnSTTStartOfSpeech(&stt.SpeechEvent{})
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "partial turn", Confidence: 0.9}},
	})

	if activity.sttEOSReceived {
		t.Fatal("sttEOSReceived = true after final transcript, want only STT end-of-speech to mark EOS")
	}
}

func TestAgentActivityPendingSTTFinalUsesTranscriptEndTimeAfterEndOfSpeech(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeSTT
	agent.STT = &fakePipelineSTT{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{MinEndpointingDelay: 0.08})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()
	activity.userSpeechStartedAt = time.Now().Add(-180 * time.Millisecond)
	activity.speaking = true

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Text:       "timestamped pending final",
			Confidence: 0.9,
			EndTime:    0.05,
		}},
	})
	activity.OnEndOfSpeech(nil)

	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "timestamped pending final" {
			t.Fatalf("turn message text = %q, want timestamped pending final", msg.TextContent())
		}
	case <-time.After(30 * time.Millisecond):
		t.Fatal("OnUserTurnCompleted waited a full endpointing delay instead of using pending STT end time")
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

	activity.runEOUDetection(EndOfTurnInfo{NewTranscript: "still talking", TranscriptConfidence: 0.9})

	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "still talking" {
			t.Fatalf("turn message text = %q, want still talking", msg.TextContent())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnUserTurnCompleted was not called after session max endpointing delay")
	}
}

func TestAgentActivityUsesTurnDetectorLanguageThreshold(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeSTT
	agent.TurnDetector = thresholdTurnDetector{
		probability: 0.4,
		thresholds:  map[string]float64{"en-US": 0.3},
	}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		MinEndpointingDelay: 0.01,
		MaxEndpointingDelay: 0.2,
	})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	activity.runEOUDetection(EndOfTurnInfo{
		NewTranscript:        "done now",
		TranscriptConfidence: 0.9,
		Language:             "en-US",
	})

	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "done now" {
			t.Fatalf("turn message text = %q, want done now", msg.TextContent())
		}
	case <-time.After(80 * time.Millisecond):
		t.Fatal("OnUserTurnCompleted was not called after min delay; threshold was not applied")
	}
}

func TestAgentActivityKeepsReferenceLanguageForShortFinalTranscript(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeSTT
	agent.STT = &fakePipelineSTT{}
	agent.TurnDetector = thresholdTurnDetector{
		probability: 0.4,
		thresholds:  map[string]float64{"en-US": 0.3},
	}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		MinEndpointingDelay: 0.01,
		MaxEndpointingDelay: 0.12,
	})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()
	activity.speaking = true

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Text:       "hello there",
			Language:   "en-US",
			Confidence: 0.9,
		}},
	})
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Text:       "yo",
			Confidence: 0.9,
		}},
	})
	activity.OnEndOfSpeech(nil)

	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "hello there yo" {
			t.Fatalf("turn message text = %q, want hello there yo", msg.TextContent())
		}
	case <-time.After(70 * time.Millisecond):
		t.Fatal("OnUserTurnCompleted waited for max delay; short final transcript lost reference language")
	}
}

func TestAgentActivityUsesAudioTurnDetectorMaxEndpointingDelay(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeSTT
	agent.STT = &fakePipelineSTT{}
	audioDetector := &recordingAudioTurnDetector{probability: 0.1}
	agent.AudioTurnDetector = audioDetector
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		MinEndpointingDelay: 0.01,
		MaxEndpointingDelay: 0.03,
	})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	defer activity.Stop()

	frameData := []byte{0x01, 0x00, 0x02, 0x00}
	audioDetector.originalData = frameData
	session.OnAudioFrame(context.Background(), &model.AudioFrame{
		Data:              frameData,
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	})
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "still talking", Confidence: 0.9}},
	})

	select {
	case msg := <-agent.turns:
		t.Fatalf("OnUserTurnCompleted called before STT end-of-speech with %q", msg.TextContent())
	case <-time.After(20 * time.Millisecond):
	}

	activity.OnEndOfSpeech(nil)

	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "still talking" {
			t.Fatalf("turn message text = %q, want still talking", msg.TextContent())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnUserTurnCompleted was not called after audio turn detector max endpointing delay")
	}
	if audioDetector.calls != 1 {
		t.Fatalf("audio detector calls = %d, want 1", audioDetector.calls)
	}
	if len(audioDetector.frames) != 1 {
		t.Fatalf("audio detector frames = %d, want 1", len(audioDetector.frames))
	}
	if &audioDetector.frames[0].Data[0] == &audioDetector.originalData[0] {
		t.Fatal("audio detector received original frame backing data, want copied snapshot")
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

	activity.runEOUDetection(EndOfTurnInfo{NewTranscript: "updated delay", TranscriptConfidence: 0.9})

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
		t.Fatalf("OnUserTurnCompleted called before STT end-of-speech after update with %q", msg.TextContent())
	case <-time.After(20 * time.Millisecond):
	}

	activity.OnEndOfSpeech(nil)

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

func TestAgentActivitySTTTurnDetectionUsesActivePipelineSTT(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeSTT
	session := NewAgentSession(agent, nil, AgentSessionOptions{MinEndpointingDelay: 0.01})
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity
	session.Assistant = NewPipelineAgent(nil, &fakePipelineSTT{}, nil, nil, agent.ChatCtx)
	defer activity.Stop()

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "pipeline stt turn", Confidence: 0.9}},
	})
	activity.OnEndOfSpeech(nil)

	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "pipeline stt turn" {
			t.Fatalf("turn message text = %q, want pipeline stt turn", msg.TextContent())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnUserTurnCompleted was not called with active pipeline STT")
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

func TestAgentActivityVADTurnDetectionUsesActivePipelineVAD(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeVAD
	session := NewAgentSession(agent, nil, AgentSessionOptions{MinEndpointingDelay: 0.01})
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity
	session.Assistant = NewPipelineAgent(&fakePipelineVAD{}, nil, nil, nil, agent.ChatCtx)
	defer activity.Stop()

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "pipeline vad turn", Confidence: 0.9}},
	})
	activity.OnEndOfSpeech(&vad.VADEvent{Timestamp: 1.0})

	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "pipeline vad turn" {
			t.Fatalf("turn message text = %q, want pipeline vad turn", msg.TextContent())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnUserTurnCompleted was not called with active pipeline VAD")
	}
}

func TestAgentActivityOnFinalTranscriptEmitsUserInputTranscribed(t *testing.T) {
	agent := NewAgent("test")
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	userTranscriptEvents := session.UserInputTranscribedEvents()

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Language:   "en",
			Text:       "final transcript",
			Confidence: 0.9,
			SpeakerID:  "speaker-1",
		}},
	})

	select {
	case ev := <-userTranscriptEvents:
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

func TestAgentActivityFinalTranscriptsAccumulateBeforeCommit(t *testing.T) {
	agent := NewAgent("test")
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "hello", Confidence: 0.8}},
	})
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "world", Confidence: 0.9}},
	})

	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "hello world" {
		t.Fatalf("CommitUserTurn transcript = %q, want accumulated final transcript", transcript)
	}
}

func TestAgentActivityFinalTranscriptConfidenceAveragesBeforeCommit(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "hello", Confidence: 0.2}},
	})
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "world", Confidence: 0.8}},
	})

	if _, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{SkipReply: true}); err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if len(agent.ChatCtx.Items) != 1 {
		t.Fatalf("agent chat context has %d items, want committed user message", len(agent.ChatCtx.Items))
	}
	msg, ok := agent.ChatCtx.Items[0].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("committed chat item = %T, want *llm.ChatMessage", agent.ChatCtx.Items[0])
	}
	if msg.TranscriptConfidence == nil || *msg.TranscriptConfidence != 0.5 {
		t.Fatalf("TranscriptConfidence = %v, want averaged 0.5", msg.TranscriptConfidence)
	}
}

func TestAgentActivityDropsFinalTranscriptBeforeAgentSpeechEnd(t *testing.T) {
	agent := NewAgent("test")
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	userTranscriptEvents := session.UserInputTranscribedEvents()
	startedAt := time.Unix(100, 0)
	activity.userSpeechStartedAt = startedAt
	activity.holdUserTranscriptsUntil(startedAt.Add(2 * time.Second))

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Text:       "stale overlap",
			EndTime:    0.5,
			Confidence: 0.9,
		}},
	})

	select {
	case ev := <-userTranscriptEvents:
		t.Fatalf("unexpected stale transcript event: %#v", ev)
	default:
	}
	if activity.pendingUserTranscriptPresent {
		t.Fatalf("pending user transcript = %q, want stale transcript dropped", activity.pendingUserTranscript)
	}

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Text:       "new turn",
			EndTime:    2.5,
			Confidence: 0.9,
		}},
	})

	select {
	case ev := <-userTranscriptEvents:
		if ev.Transcript != "new turn" || !ev.IsFinal {
			t.Fatalf("event = %#v, want final new turn", ev)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("UserInputTranscribedEvents did not receive later transcript")
	}
	if !activity.pendingUserTranscriptPresent || activity.pendingUserTranscript != "new turn" {
		t.Fatalf("pending user transcript = %q/%v, want new turn present", activity.pendingUserTranscript, activity.pendingUserTranscriptPresent)
	}
}

func TestAgentActivityHeldFinalTranscriptUsesReferenceEndCooldown(t *testing.T) {
	agent := NewAgent("test")
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	userTranscriptEvents := session.UserInputTranscribedEvents()
	startedAt := time.Unix(100, 0)
	activity.userSpeechStartedAt = startedAt
	activity.holdUserTranscriptsUntil(startedAt.Add(time.Second))

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Text:       "near boundary",
			EndTime:    0.5,
			Confidence: 0.9,
		}},
	})

	select {
	case ev := <-userTranscriptEvents:
		if ev.Transcript != "near boundary" || !ev.IsFinal {
			t.Fatalf("event = %#v, want final near boundary transcript", ev)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("UserInputTranscribedEvents did not receive near-boundary transcript")
	}
}

func TestAgentActivityAgentSpeechEndHoldsStaleFinalTranscript(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection:              TurnDetectionModeVAD,
		BackchannelBoundaryEnd:     0,
		BackchannelBoundaryEndSet:  true,
		MinInterruptionDuration:    0.05,
		MinInterruptionDurationSet: true,
	})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	userTranscriptEvents := session.UserInputTranscribedEvents()
	startedAt := time.Now().Add(-3 * time.Second)
	activity.userSpeechStartedAt = startedAt

	session.UpdateAgentState(AgentStateSpeaking)
	session.UpdateAgentState(AgentStateListening)

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Text:       "assistant echo",
			EndTime:    0.25,
			Confidence: 0.9,
		}},
	})

	select {
	case ev := <-userTranscriptEvents:
		t.Fatalf("unexpected stale final transcript after agent speech end: %#v", ev)
	default:
	}
	if activity.pendingUserTranscriptPresent {
		t.Fatalf("pending user transcript = %q, want stale transcript dropped", activity.pendingUserTranscript)
	}
}

func TestAgentActivityBuffersFinalTranscriptWhileAgentSpeaking(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection:              TurnDetectionModeVAD,
		BackchannelBoundaryEnd:     0,
		BackchannelBoundaryEndSet:  true,
		MinInterruptionDuration:    0.05,
		MinInterruptionDurationSet: true,
	})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	userTranscriptEvents := session.UserInputTranscribedEvents()
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	defer current.MarkDone()
	startedAt := time.Now().Add(-3 * time.Second)
	activity.userSpeechStartedAt = startedAt

	session.UpdateAgentState(AgentStateSpeaking)
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{{
			Text:       "after assistant",
			EndTime:    4,
			Confidence: 0.9,
		}},
	})

	select {
	case ev := <-userTranscriptEvents:
		t.Fatalf("transcript emitted while agent speaking: %#v", ev)
	default:
	}
	if current.IsInterrupted() {
		t.Fatal("current speech interrupted before held transcript flush")
	}

	session.UpdateAgentState(AgentStateListening)

	select {
	case ev := <-userTranscriptEvents:
		if ev.Transcript != "after assistant" || !ev.IsFinal {
			t.Fatalf("event = %#v, want final held transcript", ev)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("UserInputTranscribedEvents did not receive held transcript after agent speech end")
	}
	if !activity.pendingUserTranscriptPresent || activity.pendingUserTranscript != "after assistant" {
		t.Fatalf("pending transcript = %q/%v, want held transcript", activity.pendingUserTranscript, activity.pendingUserTranscriptPresent)
	}
}

func TestAgentActivityDropsInterimTranscriptBeforeAgentSpeechEnd(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{TurnDetection: TurnDetectionModeVAD})
	activity := NewAgentActivity(agent, session)
	userTranscriptEvents := session.UserInputTranscribedEvents()
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	defer current.MarkDone()
	startedAt := time.Unix(100, 0)
	activity.userSpeechStartedAt = startedAt
	activity.holdUserTranscriptsUntil(startedAt.Add(2 * time.Second))

	activity.OnInterimTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Text:      "stale interim",
			StartTime: 0.25,
			EndTime:   0.5,
		}},
	})

	select {
	case ev := <-userTranscriptEvents:
		t.Fatalf("unexpected stale interim transcript event: %#v", ev)
	default:
	}
	if current.IsInterrupted() {
		t.Fatal("current speech was interrupted by stale interim transcript")
	}

	activity.OnInterimTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Text:      "new interim",
			StartTime: 2.25,
			EndTime:   2.5,
		}},
	})

	select {
	case ev := <-userTranscriptEvents:
		if ev.Transcript != "new interim" || ev.IsFinal {
			t.Fatalf("event = %#v, want non-final new interim", ev)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("UserInputTranscribedEvents did not receive later interim transcript")
	}
}

func TestAgentActivityStartOfSpeechClearsHeldTranscriptIgnoreWindow(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{TurnDetection: TurnDetectionModeVAD})
	activity := NewAgentActivity(agent, session)
	userTranscriptEvents := session.UserInputTranscribedEvents()
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	defer current.MarkDone()
	startedAt := time.Unix(100, 0)
	activity.userSpeechStartedAt = startedAt
	activity.holdUserTranscriptsUntil(startedAt.Add(time.Second))

	activity.OnStartOfSpeech(&vad.VADEvent{Type: vad.VADEventStartOfSpeech})
	if !activity.ignoreUserTranscriptUntil.IsZero() {
		t.Fatalf("ignoreUserTranscriptUntil = %v, want cleared after start of speech", activity.ignoreUserTranscriptUntil)
	}

	activity.userSpeechStartedAt = startedAt
	activity.OnInterimTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Text:      "new speech",
			StartTime: 0.25,
			EndTime:   0.5,
		}},
	})

	select {
	case ev := <-userTranscriptEvents:
		if ev.Transcript != "new speech" || ev.IsFinal {
			t.Fatalf("event = %#v, want non-final new speech", ev)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("UserInputTranscribedEvents did not receive new speech after start reset")
	}
}

func TestAgentActivityOnFinalTranscriptSkipsEmptyTranscript(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{TurnDetection: TurnDetectionModeVAD})
	activity := NewAgentActivity(agent, session)
	userTranscriptEvents := session.UserInputTranscribedEvents()
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	defer current.MarkDone()

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Language:   "en",
			Text:       "",
			Confidence: 0,
			SpeakerID:  "speaker-1",
		}},
	})

	select {
	case ev := <-userTranscriptEvents:
		t.Fatalf("UserInputTranscribedEvents received empty final transcript: %#v", ev)
	case <-time.After(20 * time.Millisecond):
	}
	if current.IsInterrupted() {
		t.Fatal("current speech was interrupted for empty final transcript")
	}
}

func TestAgentActivityOnFinalTranscriptRespectsMinInterruptionWords(t *testing.T) {
	agent := NewAgent("test")
	agent.STT = &fakePipelineSTT{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection:        TurnDetectionModeSTT,
		MinInterruptionWords: 2,
		MinEndpointingDelay:  0.5,
	})
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "wait", Confidence: 0.9}},
	})

	select {
	case <-current.interruptCh:
		t.Fatal("current speech was interrupted for final transcript below MinInterruptionWords")
	case <-time.After(20 * time.Millisecond):
	}
	current.MarkDone()
}

func TestAgentActivityOnFinalTranscriptStartsFalseInterruptionTimerWhenSpeechEnded(t *testing.T) {
	agent := NewAgent("test")
	agent.STT = &fakePipelineSTT{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection:               TurnDetectionModeSTT,
		FalseInterruptionTimeout:    0.01,
		FalseInterruptionTimeoutSet: true,
		ResumeFalseInterruption:     true,
		ResumeFalseInterruptionSet:  true,
	})
	audioOutput := &recordingAudioOutputController{canPause: true}
	session.SetAudioOutputController(audioOutput)
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	session.agentState = AgentStateSpeaking
	falseInterruptions := session.AgentFalseInterruptionEvents()

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "actually never mind", Confidence: 1}},
	})

	if audioOutput.pauseCount != 1 {
		t.Fatalf("PauseAudioOutput calls = %d, want 1", audioOutput.pauseCount)
	}
	select {
	case ev := <-falseInterruptions:
		if !ev.Resumed {
			t.Fatalf("AgentFalseInterruptionEvent.Resumed = false, want true")
		}
	case <-time.After(time.Second):
		t.Fatal("AgentFalseInterruptionEvents did not receive resumed event")
	}
	if audioOutput.resumeCount != 1 {
		t.Fatalf("ResumeAudioOutput calls = %d, want 1", audioOutput.resumeCount)
	}
	current.MarkDone()
}

func TestAgentActivityOnInterimTranscriptStartsFalseInterruptionTimerWhenSpeechEnded(t *testing.T) {
	agent := NewAgent("test")
	agent.STT = &fakePipelineSTT{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection:               TurnDetectionModeSTT,
		FalseInterruptionTimeout:    0.01,
		FalseInterruptionTimeoutSet: true,
		ResumeFalseInterruption:     true,
		ResumeFalseInterruptionSet:  true,
	})
	audioOutput := &recordingAudioOutputController{canPause: true}
	session.SetAudioOutputController(audioOutput)
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	session.agentState = AgentStateSpeaking
	falseInterruptions := session.AgentFalseInterruptionEvents()

	activity.OnInterimTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "maybe hold on"}},
	})

	if audioOutput.pauseCount != 1 {
		t.Fatalf("PauseAudioOutput calls = %d, want 1", audioOutput.pauseCount)
	}
	select {
	case ev := <-falseInterruptions:
		if !ev.Resumed {
			t.Fatalf("AgentFalseInterruptionEvent.Resumed = false, want true")
		}
	case <-time.After(time.Second):
		t.Fatal("AgentFalseInterruptionEvents did not receive resumed event")
	}
	if audioOutput.resumeCount != 1 {
		t.Fatalf("ResumeAudioOutput calls = %d, want 1", audioOutput.resumeCount)
	}
	current.MarkDone()
}

func TestAgentActivityOnInterimTranscriptEmitsUserInputTranscribed(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	userTranscriptEvents := session.UserInputTranscribedEvents()

	activity.OnInterimTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Language:  "en",
			Text:      "interim transcript",
			SpeakerID: "speaker-1",
		}},
	})

	select {
	case ev := <-userTranscriptEvents:
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

func TestAgentActivityOnInterimTranscriptSkipsRealtimeUserTranscription(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Assistant = realtimeUserTranscriptionAssistant{}
	activity := NewAgentActivity(agent, session)
	userTranscriptEvents := session.UserInputTranscribedEvents()

	activity.OnInterimTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Language: "en",
			Text:     "native realtime transcript",
		}},
	})

	select {
	case ev := <-userTranscriptEvents:
		t.Fatalf("UserInputTranscribedEvents received STT transcript despite realtime user transcription: %#v", ev)
	case <-time.After(10 * time.Millisecond):
	}
}

func TestAgentActivityOnInterimTranscriptInterruptsCurrentSpeech(t *testing.T) {
	agent := NewAgent("test")
	agent.STT = &fakePipelineSTT{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{TurnDetection: TurnDetectionModeSTT})
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current

	activity.OnInterimTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "wait"}},
	})

	waitForInterrupted(t, current)
}

func TestAgentActivityOnInterimTranscriptRespectsMinInterruptionWords(t *testing.T) {
	agent := NewAgent("test")
	agent.STT = &fakePipelineSTT{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection:        TurnDetectionModeSTT,
		MinInterruptionWords: 2,
	})
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current

	activity.OnInterimTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "wait"}},
	})

	select {
	case <-current.interruptCh:
		t.Fatal("current speech was interrupted for interim transcript below MinInterruptionWords")
	case <-time.After(20 * time.Millisecond):
	}
	current.MarkDone()
}

func TestAgentActivityOnInterimTranscriptRespectsMinInterruptionWordsWithPipelineSTT(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection:        TurnDetectionModeSTT,
		MinInterruptionWords: 2,
	})
	session.Assistant = NewPipelineAgent(nil, &fakePipelineSTT{}, nil, nil, agent.ChatCtx)
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current

	activity.OnInterimTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "wait"}},
	})

	select {
	case <-current.interruptCh:
		t.Fatal("current speech was interrupted for pipeline STT transcript below MinInterruptionWords")
	case <-time.After(20 * time.Millisecond):
	}
	current.MarkDone()
}

func TestAgentActivityOnInterimTranscriptIgnoresAECWarmup(t *testing.T) {
	agent := NewAgent("test")
	agent.STT = &fakePipelineSTT{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection:     TurnDetectionModeSTT,
		AECWarmupDuration: 0.05,
	})
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current

	session.UpdateAgentState(AgentStateSpeaking)
	activity.OnInterimTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "echo"}},
	})

	select {
	case <-current.interruptCh:
		t.Fatal("current speech was interrupted during AEC warmup")
	case <-time.After(10 * time.Millisecond):
	}
	current.MarkDone()
}

func TestAgentActivityOnInterimTranscriptDoesNotInterruptManualTurn(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{TurnDetection: TurnDetectionModeManual})
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current

	activity.OnInterimTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "wait"}},
	})

	if current.IsInterrupted() {
		t.Fatal("current speech was interrupted for manual turn detection")
	}
}

func TestAgentActivityOnFinalTranscriptInterruptsCurrentSpeechForVADTurnDetection(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{TurnDetection: TurnDetectionModeVAD})
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "final fallback", Confidence: 0.9}},
	})

	waitForInterrupted(t, current)
	current.MarkDone()
}

func TestAgentActivityOnFinalTranscriptInterruptsCurrentSpeechForDefaultVADTurnDetection(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{MinEndpointingDelay: 5})
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "default vad interrupt", Confidence: 0.9}},
	})

	waitForInterrupted(t, current)
	current.MarkDone()
}

func TestAgentActivityOnFinalTranscriptDoesNotInterruptManualTurnDetection(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{TurnDetection: TurnDetectionModeManual})
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "manual final", Confidence: 0.9}},
	})

	if current.IsInterrupted() {
		t.Fatal("current speech was interrupted for manual turn detection")
	}
	current.MarkDone()
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

func TestAgentActivityClearUserTurnClearsRealtimeAudio(t *testing.T) {
	agent := NewAgent("test")
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	assistant := &recordingRealtimeCommitAssistant{}
	session.Assistant = assistant
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	activity.ClearUserTurn()

	if assistant.clears != 1 {
		t.Fatalf("ClearAudio calls = %d, want 1", assistant.clears)
	}
}

func TestAgentActivityClearUserTurnClearsInputTranscription(t *testing.T) {
	agent := NewAgent("test")
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	assistant := &recordingInputTranscriptionClearer{}
	session.Assistant = assistant
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	activity.ClearUserTurn()

	if assistant.clears != 1 {
		t.Fatalf("ClearInputTranscription calls = %d, want 1", assistant.clears)
	}
}

func TestAgentActivityClearUserTurnResetsUserTurnLimitTracker(t *testing.T) {
	agent := NewAgent("test")
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{UserTurnLimitMaxWords: 3})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()
	events := session.UserTurnExceededEvents()

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "one two", Confidence: 0.9}},
	})
	activity.ClearUserTurn()
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "three", Confidence: 0.9}},
	})

	select {
	case ev := <-events:
		t.Fatalf("UserTurnExceeded emitted after cleared tracker: %#v", ev)
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

func TestAgentActivityManualCommitIgnoresLateInterimTranscript(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 2)}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "first turn", Confidence: 0.9}},
	})
	if _, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{}); err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	select {
	case <-agent.turns:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnUserTurnCompleted was not called for first manual commit")
	}

	activity.OnInterimTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "late stale interim"}},
	})
	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{TranscriptTimeout: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("second CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "" {
		t.Fatalf("second CommitUserTurn transcript = %q, want empty after late interim ignored", transcript)
	}
	select {
	case msg := <-agent.turns:
		t.Fatalf("OnUserTurnCompleted called for stale late interim with %q", msg.TextContent())
	default:
	}
}

func TestAgentActivityManualCommitIgnoresLateFinalTranscript(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 2)}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "first turn", Confidence: 0.9}},
	})
	if _, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{}); err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	select {
	case <-agent.turns:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnUserTurnCompleted was not called for first manual commit")
	}

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "late stale final", Confidence: 0.9}},
	})
	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
	if err != nil {
		t.Fatalf("second CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "" {
		t.Fatalf("second CommitUserTurn transcript = %q, want empty after late final ignored", transcript)
	}
	select {
	case msg := <-agent.turns:
		t.Fatalf("OnUserTurnCompleted called for stale late final with %q", msg.TextContent())
	default:
	}
}

func TestAgentActivityCommitUserTurnFallsBackToInterimTranscriptAfterTimeout(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()
	interimEvents := session.UserInputTranscribedEvents()

	activity.OnInterimTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Text:       "interim fallback",
			Language:   "en",
			Confidence: 0.4,
			SpeakerID:  "speaker-1",
		}},
	})
	<-interimEvents

	finalEvents := session.UserInputTranscribedEvents()
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
	case ev := <-finalEvents:
		if !ev.IsFinal || ev.Transcript != "interim fallback" || ev.Language != "en" || ev.SpeakerID != "speaker-1" {
			t.Fatalf("fallback final event = %#v, want final interim fallback/en/speaker-1", ev)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("UserInputTranscribedEvents did not receive fallback final transcript")
	}
}

func TestAgentActivityCommitUserTurnFlushesSTTBeforeWaitingForFinal(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	flusher := &recordingTranscriptFlusher{flushed: make(chan struct{}, 1)}
	session.Assistant = flusher
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	done := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{
			TranscriptTimeout: 100 * time.Millisecond,
			STTFlushDuration:  20 * time.Millisecond,
		})
		if err != nil {
			errCh <- err
			return
		}
		done <- transcript
	}()

	select {
	case <-flusher.flushed:
	case <-time.After(time.Second):
		t.Fatal("CommitUserTurn did not flush input transcription before waiting for final transcript")
	}

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Text:       "flushed final",
			Language:   "en",
			Confidence: 0.92,
		}},
	})

	select {
	case err := <-errCh:
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	case transcript := <-done:
		if transcript != "flushed final" {
			t.Fatalf("CommitUserTurn transcript = %q, want flushed final", transcript)
		}
	case <-time.After(time.Second):
		t.Fatal("CommitUserTurn did not return after flushed final transcript")
	}
	if flusher.calls != 1 {
		t.Fatalf("FlushInputTranscription calls = %d, want 1", flusher.calls)
	}
	if flusher.flushDuration != 20*time.Millisecond {
		t.Fatalf("FlushInputTranscription duration = %v, want 20ms", flusher.flushDuration)
	}
}

func TestAgentActivityCommitUserTurnDefaultsFlushAndWaitForFinal(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	flusher := &recordingTranscriptFlusher{flushed: make(chan struct{}, 1)}
	session.Assistant = flusher
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	done := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
		if err != nil {
			errCh <- err
			return
		}
		done <- transcript
	}()

	select {
	case <-flusher.flushed:
	case transcript := <-done:
		t.Fatalf("CommitUserTurn returned %q before default STT flush and final transcript", transcript)
	case <-time.After(time.Second):
		t.Fatal("CommitUserTurn did not flush input transcription with default options")
	}
	if flusher.flushDuration != 2*time.Second {
		t.Fatalf("FlushInputTranscription duration = %v, want reference default 2s", flusher.flushDuration)
	}
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Text:       "default flushed final",
			Language:   "en",
			Confidence: 0.92,
		}},
	})

	select {
	case err := <-errCh:
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	case transcript := <-done:
		if transcript != "default flushed final" {
			t.Fatalf("CommitUserTurn transcript = %q, want default flushed final", transcript)
		}
	case <-time.After(time.Second):
		t.Fatal("CommitUserTurn did not return after default flushed final transcript")
	}
}

func TestAgentActivityCommitUserTurnSupersedesPendingCommit(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 2)}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	flusher := &recordingTranscriptFlusher{flushed: make(chan struct{}, 2)}
	session.Assistant = flusher
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	firstDone := make(chan string, 1)
	firstErr := make(chan error, 1)
	go func() {
		transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{
			TranscriptTimeout: 500 * time.Millisecond,
			STTFlushDuration:  20 * time.Millisecond,
		})
		if err != nil {
			firstErr <- err
			return
		}
		firstDone <- transcript
	}()

	select {
	case <-flusher.flushed:
	case <-time.After(time.Second):
		t.Fatal("first CommitUserTurn did not start waiting for a transcript")
	}

	secondDone := make(chan string, 1)
	secondErr := make(chan error, 1)
	go func() {
		transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{
			TranscriptTimeout: 500 * time.Millisecond,
			STTFlushDuration:  20 * time.Millisecond,
		})
		if err != nil {
			secondErr <- err
			return
		}
		secondDone <- transcript
	}()

	select {
	case err := <-firstErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("first CommitUserTurn error = %v, want context canceled", err)
		}
	case transcript := <-firstDone:
		t.Fatalf("first CommitUserTurn transcript = %q, want superseded commit canceled", transcript)
	case <-time.After(time.Second):
		t.Fatal("first CommitUserTurn was not canceled by the newer commit")
	}
	select {
	case <-flusher.flushed:
	case <-time.After(time.Second):
		t.Fatal("second CommitUserTurn did not start waiting for a transcript")
	}

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Text:       "newer final",
			Language:   "en",
			Confidence: 0.91,
		}},
	})

	select {
	case err := <-secondErr:
		t.Fatalf("second CommitUserTurn error = %v, want nil", err)
	case transcript := <-secondDone:
		if transcript != "newer final" {
			t.Fatalf("second CommitUserTurn transcript = %q, want newer final", transcript)
		}
	case <-time.After(time.Second):
		t.Fatal("second CommitUserTurn did not return after final transcript")
	}
	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "newer final" {
			t.Fatalf("turn message text = %q, want newer final", msg.TextContent())
		}
	default:
		t.Fatal("OnUserTurnCompleted was not called for newer commit")
	}
	select {
	case msg := <-agent.turns:
		t.Fatalf("OnUserTurnCompleted called more than once, extra transcript %q", msg.TextContent())
	default:
	}
}

func TestAgentActivityCommitUserTurnDoesNotCancelActiveHook(t *testing.T) {
	agent := &blockingTurnAgent{
		Agent:   NewAgent("test"),
		started: make(chan *llm.ChatMessage, 2),
		release: make(chan struct{}),
	}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "first active hook", Confidence: 0.9}},
	})
	firstDone := make(chan error, 1)
	go func() {
		_, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
		firstDone <- err
	}()
	select {
	case msg := <-agent.started:
		if got := msg.TextContent(); got != "first active hook" {
			t.Fatalf("first hook message = %q, want first active hook", got)
		}
	case <-testTimeout():
		t.Fatal("first hook did not start")
	}

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "second active hook", Confidence: 0.9}},
	})
	secondDone := make(chan error, 1)
	go func() {
		_, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
		secondDone <- err
	}()

	select {
	case err := <-firstDone:
		t.Fatalf("first CommitUserTurn returned before hook release: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	close(agent.release)
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first CommitUserTurn error = %v, want nil", err)
		}
	case <-testTimeout():
		t.Fatal("first CommitUserTurn did not finish after release")
	}
	select {
	case msg := <-agent.started:
		if got := msg.TextContent(); got != "second active hook" {
			t.Fatalf("second hook message = %q, want second active hook", got)
		}
	case <-testTimeout():
		t.Fatal("second hook did not start after first hook finished")
	}
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second CommitUserTurn error = %v, want nil", err)
		}
	case <-testTimeout():
		t.Fatal("second CommitUserTurn did not finish")
	}
}

func TestAgentActivityManualCommitIgnoresInterimWhileHookActive(t *testing.T) {
	agent := &blockingTurnAgent{
		Agent:   NewAgent("test"),
		started: make(chan *llm.ChatMessage, 2),
		release: make(chan struct{}),
	}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "first manual turn", Confidence: 0.9}},
	})
	firstDone := make(chan error, 1)
	go func() {
		_, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
		firstDone <- err
	}()
	select {
	case msg := <-agent.started:
		if got := msg.TextContent(); got != "first manual turn" {
			t.Fatalf("first hook message = %q, want first manual turn", got)
		}
	case <-testTimeout():
		t.Fatal("first hook did not start")
	}

	activity.OnInterimTranscript(&stt.SpeechEvent{
		Type: stt.SpeechEventInterimTranscript,
		Alternatives: []stt.SpeechData{{
			Text:       "stale interim",
			Confidence: 0.9,
		}},
	})

	close(agent.release)
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first CommitUserTurn error = %v, want nil", err)
		}
	case <-testTimeout():
		t.Fatal("first CommitUserTurn did not finish")
	}

	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{
		TranscriptTimeout: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("second CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "" {
		t.Fatalf("second CommitUserTurn transcript = %q, want empty after active-hook interim ignored", transcript)
	}
	select {
	case msg := <-agent.started:
		t.Fatalf("OnUserTurnCompleted called for stale active-hook interim with %q", msg.TextContent())
	default:
	}
}

func TestAgentActivityManualCommitAcceptsPreflightWhileHookActive(t *testing.T) {
	agent := &blockingTurnAgent{
		Agent:   NewAgent("test"),
		started: make(chan *llm.ChatMessage, 2),
		release: make(chan struct{}),
	}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "first manual turn", Confidence: 0.9}},
	})
	firstDone := make(chan error, 1)
	go func() {
		_, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
		firstDone <- err
	}()
	select {
	case msg := <-agent.started:
		if got := msg.TextContent(); got != "first manual turn" {
			t.Fatalf("first hook message = %q, want first manual turn", got)
		}
	case <-testTimeout():
		t.Fatal("first hook did not start")
	}

	activity.OnInterimTranscript(&stt.SpeechEvent{
		Type: stt.SpeechEventPreflightTranscript,
		Alternatives: []stt.SpeechData{{
			Text:       "preflight next turn",
			Confidence: 0.9,
		}},
	})

	close(agent.release)
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first CommitUserTurn error = %v, want nil", err)
		}
	case <-testTimeout():
		t.Fatal("first CommitUserTurn did not finish")
	}

	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{
		TranscriptTimeout: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("second CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "preflight next turn" {
		t.Fatalf("second CommitUserTurn transcript = %q, want preflight next turn", transcript)
	}
	select {
	case msg := <-agent.started:
		if got := msg.TextContent(); got != "preflight next turn" {
			t.Fatalf("second hook message = %q, want preflight next turn", got)
		}
	case <-testTimeout():
		t.Fatal("OnUserTurnCompleted was not called for active-hook preflight")
	}
}

func TestAgentActivityCommitUserTurnFlushesWhenLastFinalIsStale(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	flusher := &recordingTranscriptFlusher{flushed: make(chan struct{}, 1)}
	session.Assistant = flusher
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Text:       "old final",
			Confidence: 0.88,
		}},
	})
	time.Sleep(550 * time.Millisecond)

	done := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{
			TranscriptTimeout: 500 * time.Millisecond,
			STTFlushDuration:  20 * time.Millisecond,
		})
		if err != nil {
			errCh <- err
			return
		}
		done <- transcript
	}()

	select {
	case <-flusher.flushed:
	case transcript := <-done:
		t.Fatalf("CommitUserTurn returned %q before flushing stale final transcript", transcript)
	case <-time.After(time.Second):
		t.Fatal("CommitUserTurn did not flush after stale final transcript")
	}

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Text:       "fresh final",
			Confidence: 0.92,
		}},
	})

	select {
	case err := <-errCh:
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	case transcript := <-done:
		if transcript != "old final fresh final" {
			t.Fatalf("CommitUserTurn transcript = %q, want old final fresh final", transcript)
		}
	case <-time.After(time.Second):
		t.Fatal("CommitUserTurn did not return after fresh final transcript")
	}
}

func TestAgentActivityCommitUserTurnTreatsPreflightAsFreshTranscript(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	flusher := &recordingTranscriptFlusher{flushed: make(chan struct{}, 1)}
	session.Assistant = flusher
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Text:       "old final",
			Confidence: 0.88,
		}},
	})
	time.Sleep(550 * time.Millisecond)
	activity.OnInterimTranscript(&stt.SpeechEvent{
		Type: stt.SpeechEventPreflightTranscript,
		Alternatives: []stt.SpeechData{{
			Text:       "preflight final",
			Confidence: 0.92,
		}},
	})

	started := time.Now()
	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{
		TranscriptTimeout: 100 * time.Millisecond,
		STTFlushDuration:  20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "old final preflight final" {
		t.Fatalf("CommitUserTurn transcript = %q, want old final preflight final", transcript)
	}
	if flusher.calls != 0 {
		t.Fatalf("FlushInputTranscription calls = %d, want 0 after fresh preflight transcript", flusher.calls)
	}
	if elapsed := time.Since(started); elapsed >= 100*time.Millisecond {
		t.Fatalf("CommitUserTurn elapsed = %v, want no transcript timeout wait after fresh preflight transcript", elapsed)
	}
}

func TestAgentActivityCommitUserTurnAppendsInterimToPendingFinal(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Text:       "confirmed words",
			Confidence: 0.9,
		}},
	})
	activity.OnInterimTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Text:      "latest words",
			Language:  "en",
			SpeakerID: "speaker-1",
		}},
	})

	finalEvents := session.UserInputTranscribedEvents()
	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "confirmed words latest words" {
		t.Fatalf("CommitUserTurn transcript = %q, want confirmed words latest words", transcript)
	}
	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "confirmed words latest words" {
			t.Fatalf("turn message text = %q, want confirmed words latest words", msg.TextContent())
		}
	case <-time.After(time.Second):
		t.Fatal("OnUserTurnCompleted was not called")
	}
	select {
	case ev := <-finalEvents:
		if !ev.IsFinal || ev.Transcript != "latest words" || ev.Language != "en" || ev.SpeakerID != "speaker-1" {
			t.Fatalf("fallback final event = %#v, want final latest words/en/speaker-1", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("UserInputTranscribedEvents did not receive fallback final interim transcript")
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
		if ev.UserInitiated {
			t.Fatal("SpeechCreated UserInitiated = true, want false for automatic audio reply")
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

func TestAgentActivityCommitUserTurnRejectsZeroConfidenceTranscript(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeManual
	agent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	defer activity.Stop()
	transcriptEvents := session.UserInputTranscribedEvents()

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "phantom turn", Confidence: 0}},
	})

	select {
	case ev := <-transcriptEvents:
		if ev.Transcript != "phantom turn" || !ev.IsFinal {
			t.Fatalf("zero-confidence transcript event = %#v, want final phantom turn", ev)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("UserInputTranscribedEvents did not receive zero-confidence final transcript")
	}

	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "" {
		t.Fatalf("CommitUserTurn transcript = %q, want empty for zero-confidence transcript", transcript)
	}
	select {
	case msg := <-agent.turns:
		t.Fatalf("OnUserTurnCompleted called for zero-confidence transcript with %q", msg.TextContent())
	case <-time.After(20 * time.Millisecond):
	}
	select {
	case ev := <-session.SpeechCreatedEvents():
		t.Fatalf("unexpected SpeechCreated event for zero-confidence transcript: %#v", ev)
	case <-time.After(20 * time.Millisecond):
	}
	if len(agent.ChatCtx.Items) != 0 {
		t.Fatalf("agent chat context has %d items, want no zero-confidence user message", len(agent.ChatCtx.Items))
	}
}

func TestAgentActivityCommitUserTurnCommitsRealtimeAudioAndGeneratesReply(t *testing.T) {
	agent := NewAgent("test")
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	assistant := &recordingRealtimeCommitAssistant{}
	session.Assistant = assistant
	activity := NewAgentActivity(agent, session)
	session.activity = activity

	events := session.SpeechCreatedEvents()
	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "" {
		t.Fatalf("CommitUserTurn transcript = %q, want empty without STT final", transcript)
	}
	if assistant.commits != 1 {
		t.Fatalf("CommitAudio calls = %d, want 1", assistant.commits)
	}
	select {
	case ev := <-events:
		if ev.Source != "generate_reply" {
			t.Fatalf("SpeechCreated source = %q, want generate_reply", ev.Source)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("SpeechCreatedEvents did not receive realtime commit reply")
	}
}

func TestAgentActivityCommitUserTurnCallsHookAfterRealtimeCommit(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	assistant := &recordingRealtimeCommitAssistant{}
	session.Assistant = assistant
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "realtime hook", Confidence: 0.9}},
	})

	if _, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{}); err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if assistant.commits != 1 {
		t.Fatalf("CommitAudio calls = %d, want 1", assistant.commits)
	}
	select {
	case msg := <-agent.turns:
		if got := msg.TextContent(); got != "realtime hook" {
			t.Fatalf("turn message text = %q, want realtime hook", got)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnUserTurnCompleted was not called after realtime commit")
	}
}

func TestAgentActivityRealtimeCommitWaitsForCurrentSpeechBeforeReply(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	assistant := &recordingRealtimeCommitAssistant{}
	session.Assistant = assistant
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()
	session.activity = activity
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "realtime reply after speech", Confidence: 0.9}},
	})
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current

	events := session.SpeechCreatedEvents()
	done := make(chan error, 1)
	go func() {
		_, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
		done <- err
	}()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !current.IsInterrupted() {
		select {
		case <-deadline:
			t.Fatal("current speech was not interrupted")
		case <-ticker.C:
		}
	}
	select {
	case ev := <-events:
		t.Fatalf("SpeechCreated before current speech finished: %#v", ev)
	case <-time.After(20 * time.Millisecond):
	}
	current.MarkDone()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("CommitUserTurn error = %v, want nil", err)
		}
	case <-testTimeout():
		t.Fatal("CommitUserTurn did not return after current speech finished")
	}
	select {
	case ev := <-events:
		if ev.Source != "generate_reply" {
			t.Fatalf("SpeechCreated source = %q, want generate_reply", ev.Source)
		}
	case <-testTimeout():
		t.Fatal("SpeechCreatedEvents did not receive realtime reply after current speech")
	}
}

func TestAgentActivityRealtimeCommitReplyEmitsEOUMetrics(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	assistant := &recordingRealtimeCommitAssistant{}
	session.Assistant = assistant
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()
	session.activity = activity
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "realtime metrics", Confidence: 0.9}},
	})
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current

	events := session.SpeechCreatedEvents()
	metricsEvents := session.MetricsCollectedEvents()
	done := make(chan error, 1)
	go func() {
		_, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
		done <- err
	}()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !current.IsInterrupted() {
		select {
		case <-deadline:
			t.Fatal("current speech was not interrupted")
		case <-ticker.C:
		}
	}
	current.MarkDone()

	var speechID string
	select {
	case ev := <-events:
		speechID = ev.SpeechHandle.ID
	case <-testTimeout():
		t.Fatal("SpeechCreatedEvents did not receive realtime reply")
	}
	select {
	case ev := <-metricsEvents:
		metrics, ok := ev.Metrics.(*telemetry.EOUMetrics)
		if !ok {
			t.Fatalf("metrics = %T, want *telemetry.EOUMetrics", ev.Metrics)
		}
		if metrics.SpeechID != speechID {
			t.Fatalf("EOUMetrics SpeechID = %q, want %q", metrics.SpeechID, speechID)
		}
	case <-testTimeout():
		t.Fatal("MetricsCollectedEvents did not receive EOU metrics for realtime reply")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("CommitUserTurn error = %v, want nil", err)
		}
	case <-testTimeout():
		t.Fatal("CommitUserTurn did not return")
	}
}

func TestAgentActivityCommitUserTurnSkipReplyCommitsRealtimeAudioOnly(t *testing.T) {
	agent := NewAgent("test")
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	assistant := &recordingRealtimeCommitAssistant{}
	session.Assistant = assistant
	activity := NewAgentActivity(agent, session)
	session.activity = activity

	events := session.SpeechCreatedEvents()
	if _, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{SkipReply: true}); err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if assistant.commits != 1 {
		t.Fatalf("CommitAudio calls = %d, want 1", assistant.commits)
	}
	select {
	case ev := <-events:
		t.Fatalf("unexpected SpeechCreated event with SkipReply: %#v", ev)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestAgentActivityCommitUserTurnSkipsRealtimeCommitWithServerTurnDetection(t *testing.T) {
	agent := NewAgent("test")
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	assistant := &recordingRealtimeCommitAssistant{
		capabilities: llm.RealtimeCapabilities{TurnDetection: true},
	}
	session.Assistant = assistant
	activity := NewAgentActivity(agent, session)
	session.activity = activity

	events := session.SpeechCreatedEvents()
	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "" {
		t.Fatalf("CommitUserTurn transcript = %q, want empty without STT final", transcript)
	}
	if assistant.commits != 0 {
		t.Fatalf("CommitAudio calls = %d, want 0 with server-side turn detection", assistant.commits)
	}
	select {
	case ev := <-events:
		t.Fatalf("unexpected SpeechCreated event with server-side turn detection: %#v", ev)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestAgentActivityCommitUserTurnRealtimeCommitPreservesUserMessageAfterHook(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeManual
	agent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	assistant := &recordingRealtimeCommitAssistant{}
	session.Assistant = assistant
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "realtime pending final", Confidence: 0.9}},
	})
	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "realtime pending final" {
		t.Fatalf("CommitUserTurn transcript = %q, want realtime pending final", transcript)
	}
	if assistant.commits != 1 {
		t.Fatalf("CommitAudio calls = %d, want 1", assistant.commits)
	}
	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "realtime pending final" {
			t.Fatalf("OnUserTurnCompleted message = %q, want realtime pending final", msg.TextContent())
		}
	default:
		t.Fatal("OnUserTurnCompleted was not called")
	}
	select {
	case ev := <-session.SpeechCreatedEvents():
		msg := ev.SpeechHandle.Generation.UserMessage
		if msg == nil || msg.TextContent() != "realtime pending final" {
			t.Fatalf("reply user message = %#v, want committed realtime transcript", msg)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("CommitUserTurn did not generate reply after hook")
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
		Alternatives: []stt.SpeechData{{Text: "do not interrupt", Confidence: 0.9}},
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
		Alternatives: []stt.SpeechData{{Text: "interrupt and reply", Confidence: 0.9}},
	})

	done := make(chan error, 1)
	go func() {
		_, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
		done <- err
	}()

	speechCreatedEvents := session.SpeechCreatedEvents()
	waitForInterrupted(t, current)
	select {
	case err := <-done:
		t.Fatalf("CommitUserTurn returned before current speech completed: %v", err)
	case msg := <-agent.turns:
		t.Fatalf("OnUserTurnCompleted called before current speech completed with %q", msg.TextContent())
	case ev := <-speechCreatedEvents:
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
	case ev := <-speechCreatedEvents:
		if ev.SpeechHandle.Generation.UserMessage == nil || ev.SpeechHandle.Generation.UserMessage.TextContent() != "interrupt and reply" {
			t.Fatalf("reply user message = %#v, want committed user turn", ev.SpeechHandle.Generation.UserMessage)
		}
	default:
		t.Fatal("reply was not generated after current speech completed")
	}
}

func TestAgentActivityCommitUserTurnCancelsPausedFalseInterruption(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.VAD = &fakePipelineVAD{}
	agent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection:               TurnDetectionModeVAD,
		MinInterruptionDuration:     0.05,
		FalseInterruptionTimeout:    10,
		FalseInterruptionTimeoutSet: true,
		ResumeFalseInterruption:     true,
		ResumeFalseInterruptionSet:  true,
	})
	audioOutput := &recordingAudioOutputController{canPause: true}
	session.SetAudioOutputController(audioOutput)
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	session.agentState = AgentStateSpeaking

	activity.OnVADInferenceDone(&vad.VADEvent{
		Type:                  vad.VADEventInferenceDone,
		SpeechDuration:        0.06,
		Speaking:              true,
		RawAccumulatedSilence: 0,
	})
	if audioOutput.pauseCount != 1 {
		t.Fatalf("PauseAudioOutput calls = %d, want 1", audioOutput.pauseCount)
	}
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "real turn", Confidence: 1}},
	})

	done := make(chan error, 1)
	go func() {
		_, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
		done <- err
	}()

	waitForInterrupted(t, current)
	current.MarkDone()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("CommitUserTurn error = %v, want nil", err)
		}
	case <-testTimeout():
		t.Fatal("CommitUserTurn did not return after paused speech completed")
	}
	if audioOutput.resumeCount != 1 {
		t.Fatalf("ResumeAudioOutput calls = %d, want 1 after committing real turn", audioOutput.resumeCount)
	}
	select {
	case ev := <-session.AgentFalseInterruptionEvents():
		t.Fatalf("unexpected false interruption event after real turn commit: %#v", ev)
	default:
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

func TestAgentActivityCompleteUserTurnInterruptsObsoleteReply(t *testing.T) {
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
	events := session.SpeechCreatedEvents()

	firstDone := make(chan error, 1)
	go func() {
		_, err := activity.completeUserTurn(context.Background(), EndOfTurnInfo{
			NewTranscript:        "first obsolete",
			TranscriptConfidence: 0.9,
		})
		firstDone <- err
	}()
	select {
	case msg := <-agent.started:
		if got := msg.TextContent(); got != "first obsolete" {
			t.Fatalf("first hook message = %q, want first obsolete", got)
		}
	case <-testTimeout():
		t.Fatal("first hook did not start")
	}

	secondDone := make(chan error, 1)
	go func() {
		_, err := activity.completeUserTurn(context.Background(), EndOfTurnInfo{
			NewTranscript:        "second current",
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
	var firstSpeech *SpeechHandle
	select {
	case ev := <-events:
		firstSpeech = ev.SpeechHandle
	case <-testTimeout():
		t.Fatal("first reply was not generated")
	}
	if !firstSpeech.IsInterrupted() {
		t.Fatal("first reply was not interrupted after newer turn started")
	}
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first completeUserTurn error = %v, want nil", err)
		}
	case <-testTimeout():
		t.Fatal("first completeUserTurn did not finish")
	}
	select {
	case msg := <-agent.started:
		if got := msg.TextContent(); got != "second current" {
			t.Fatalf("second hook message = %q, want second current", got)
		}
	case <-testTimeout():
		t.Fatal("second hook did not start")
	}
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second completeUserTurn error = %v, want nil", err)
		}
	case <-testTimeout():
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

func TestAgentActivityShortInterruptionDoesNotResetPreemptiveRetryCount(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeSTT
	agent.STT = &fakePipelineSTT{}
	agent.LLM = &fakeGenerationLLM{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		MinInterruptionWords:              2,
		PreemptiveGenerationMaxRetries:    1,
		PreemptiveGenerationMaxRetriesSet: true,
	})
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity
	defer activity.Stop()

	speechEvents := session.SpeechCreatedEvents()
	activity.OnStartOfSpeech(nil)
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "first long", Confidence: 0.9}},
	})

	var preemptive *SpeechHandle
	select {
	case ev := <-speechEvents:
		preemptive = ev.SpeechHandle
	case <-time.After(time.Second):
		t.Fatal("SpeechCreatedEvents did not receive initial preemptive generation")
	}

	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	if _, err := activity.completeUserTurn(context.Background(), EndOfTurnInfo{
		NewTranscript:        "hi",
		TranscriptConfidence: 0.9,
	}); err != nil {
		t.Fatalf("completeUserTurn error = %v, want nil", err)
	}
	if !preemptive.IsInterrupted() {
		t.Fatal("short interruption did not cancel stale preemptive generation")
	}
	current.MarkDone()
	activity.currentSpeech = nil

	activity.OnInterimTranscript(&stt.SpeechEvent{
		Type:         stt.SpeechEventPreflightTranscript,
		Alternatives: []stt.SpeechData{{Text: "second long", Confidence: 0.9}},
	})

	select {
	case ev := <-speechEvents:
		t.Fatalf("SpeechCreatedEvents received retry %#v after short interruption reset retry count", ev)
	default:
	}
}

func TestAgentActivitySkipReplyResetsPreemptiveRetryCount(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeVAD
	agent.LLM = &fakeGenerationLLM{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		PreemptiveGenerationMaxRetries:    1,
		PreemptiveGenerationMaxRetriesSet: true,
	})
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity
	defer activity.Stop()

	speechEvents := session.SpeechCreatedEvents()
	activity.OnStartOfSpeech(&vad.VADEvent{})
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "store this", Confidence: 0.9}},
	})

	var firstPreemptive *SpeechHandle
	select {
	case ev := <-speechEvents:
		firstPreemptive = ev.SpeechHandle
	case <-time.After(time.Second):
		t.Fatal("SpeechCreatedEvents did not receive initial preemptive generation")
	}

	if _, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{SkipReply: true}); err != nil {
		t.Fatalf("CommitUserTurn SkipReply error = %v, want nil", err)
	}
	if !firstPreemptive.IsInterrupted() {
		t.Fatal("SkipReply did not cancel stale preemptive generation")
	}

	activity.OnStartOfSpeech(&vad.VADEvent{})
	activity.OnInterimTranscript(&stt.SpeechEvent{
		Type:         stt.SpeechEventPreflightTranscript,
		Alternatives: []stt.SpeechData{{Text: "next turn", Confidence: 0.9}},
	})

	select {
	case ev := <-speechEvents:
		if ev.SpeechHandle == nil || ev.SpeechHandle == firstPreemptive {
			t.Fatalf("second preemptive speech = %#v, want new handle", ev.SpeechHandle)
		}
	case <-time.After(time.Second):
		t.Fatal("SpeechCreatedEvents did not receive preemptive generation after SkipReply reset")
	}
}

func TestAgentActivityUninterruptibleSpeechResetsPreemptiveRetryCount(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeVAD
	agent.LLM = &fakeGenerationLLM{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		PreemptiveGenerationMaxRetries:    1,
		PreemptiveGenerationMaxRetriesSet: true,
	})
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity
	defer activity.Stop()

	speechEvents := session.SpeechCreatedEvents()
	activity.OnStartOfSpeech(&vad.VADEvent{})
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "first turn", Confidence: 0.9}},
	})

	var firstPreemptive *SpeechHandle
	select {
	case ev := <-speechEvents:
		firstPreemptive = ev.SpeechHandle
	case <-time.After(time.Second):
		t.Fatal("SpeechCreatedEvents did not receive initial preemptive generation")
	}

	activity.currentSpeech = NewSpeechHandle(false, DefaultInputDetails())
	if _, err := activity.completeUserTurn(context.Background(), EndOfTurnInfo{
		NewTranscript:        "blocked turn",
		TranscriptConfidence: 0.9,
	}); err != nil {
		t.Fatalf("completeUserTurn error = %v, want nil", err)
	}
	if !firstPreemptive.IsInterrupted() {
		t.Fatal("uninterruptible current speech did not cancel stale preemptive generation")
	}
	activity.currentSpeech.MarkDone()
	activity.currentSpeech = nil

	activity.OnStartOfSpeech(&vad.VADEvent{})
	activity.OnInterimTranscript(&stt.SpeechEvent{
		Type:         stt.SpeechEventPreflightTranscript,
		Alternatives: []stt.SpeechData{{Text: "next turn", Confidence: 0.9}},
	})

	select {
	case ev := <-speechEvents:
		if ev.SpeechHandle == nil || ev.SpeechHandle == firstPreemptive {
			t.Fatalf("second preemptive speech = %#v, want new handle", ev.SpeechHandle)
		}
	case <-time.After(time.Second):
		t.Fatal("SpeechCreatedEvents did not receive preemptive generation after uninterruptible speech reset")
	}
}

func TestAgentActivitySchedulingPausedDoesNotResetPreemptiveRetryCount(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeVAD
	agent.LLM = &fakeGenerationLLM{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		PreemptiveGenerationMaxRetries:    1,
		PreemptiveGenerationMaxRetriesSet: true,
	})
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity
	defer activity.Stop()

	speechEvents := session.SpeechCreatedEvents()
	activity.OnStartOfSpeech(&vad.VADEvent{})
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "first turn", Confidence: 0.9}},
	})

	var firstPreemptive *SpeechHandle
	select {
	case ev := <-speechEvents:
		firstPreemptive = ev.SpeechHandle
	case <-time.After(time.Second):
		t.Fatal("SpeechCreatedEvents did not receive initial preemptive generation")
	}

	activity.schedulingPaused = true
	if _, err := activity.completeUserTurn(context.Background(), EndOfTurnInfo{
		NewTranscript:        "paused turn",
		TranscriptConfidence: 0.9,
	}); err != nil {
		t.Fatalf("completeUserTurn error = %v, want nil", err)
	}
	if !firstPreemptive.IsInterrupted() {
		t.Fatal("paused scheduling did not cancel stale preemptive generation")
	}
	activity.schedulingPaused = false

	activity.OnStartOfSpeech(&vad.VADEvent{})
	activity.OnInterimTranscript(&stt.SpeechEvent{
		Type:         stt.SpeechEventPreflightTranscript,
		Alternatives: []stt.SpeechData{{Text: "next turn", Confidence: 0.9}},
	})

	select {
	case ev := <-speechEvents:
		t.Fatalf("SpeechCreatedEvents received retry %#v after paused turn reset retry count", ev)
	default:
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
		Alternatives: []stt.SpeechData{{Text: "paused turn", Confidence: 0.9}},
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

func TestAgentActivityCommitUserTurnDoesNotInterruptCurrentSpeechWhilePaused(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeManual
	agent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity
	activity.schedulingPaused = true
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "paused turn", Confidence: 0.9}},
	})

	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "paused turn" {
		t.Fatalf("CommitUserTurn transcript = %q, want paused turn", transcript)
	}
	if current.IsInterrupted() {
		t.Fatal("current speech was interrupted while scheduling was paused")
	}
}

func TestAgentActivityCommitUserTurnInterruptsRealtimeSessionForCurrentSpeech(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	assistant := &fakeInterruptingSessionAssistant{}
	session.Assistant = assistant
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()
	session.activity = activity

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "interrupt realtime current speech now", Confidence: 0.9}},
	})
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current

	done := make(chan error, 1)
	go func() {
		_, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
		done <- err
	}()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !current.IsInterrupted() {
		select {
		case <-deadline:
			t.Fatal("current speech was not interrupted")
		case <-ticker.C:
		}
	}
	current.MarkDone()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("CommitUserTurn error = %v, want nil", err)
		}
	case <-testTimeout():
		t.Fatal("CommitUserTurn did not return after interrupted speech completed")
	}
	if assistant.interrupts != 1 {
		t.Fatalf("realtime Interrupt calls = %d, want 1", assistant.interrupts)
	}
}

func TestAgentActivityCommitUserTurnRecordsClosingPausedTurn(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeManual
	agent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity
	session.closing = true
	activity.schedulingPaused = true

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "closing paused turn", Confidence: 0.9}},
	})

	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "closing paused turn" {
		t.Fatalf("CommitUserTurn transcript = %q, want closing paused turn", transcript)
	}
	select {
	case ev := <-session.ConversationItemAddedEvents():
		msg, ok := ev.Item.(*llm.ChatMessage)
		if !ok || msg.TextContent() != "closing paused turn" {
			t.Fatalf("ConversationItemAdded item = %#v, want closing paused turn", ev.Item)
		}
	case <-time.After(time.Second):
		t.Fatal("ConversationItemAddedEvents did not receive closing paused turn")
	}
	if agent.ChatCtx == nil || len(agent.ChatCtx.Items) != 1 {
		t.Fatalf("chat context items = %#v, want one closing user message", agent.ChatCtx)
	}
	if msg, ok := agent.ChatCtx.Items[0].(*llm.ChatMessage); !ok || msg.TextContent() != "closing paused turn" {
		t.Fatalf("chat context item = %#v, want closing paused turn message", agent.ChatCtx.Items[0])
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
		Alternatives: []stt.SpeechData{{Text: "pause after hook", Confidence: 0.9}},
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

func TestAgentActivityCommitUserTurnRecordsClosingTurnWhenHookPausesScheduling(t *testing.T) {
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
	session.closing = true

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "closing after hook pause", Confidence: 0.9}},
	})

	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "closing after hook pause" {
		t.Fatalf("CommitUserTurn transcript = %q, want closing after hook pause", transcript)
	}
	select {
	case msg := <-agent.turns:
		if msg.TextContent() != "closing after hook pause" {
			t.Fatalf("OnUserTurnCompleted message = %q, want closing after hook pause", msg.TextContent())
		}
	default:
		t.Fatal("OnUserTurnCompleted was not called")
	}
	select {
	case ev := <-session.ConversationItemAddedEvents():
		msg, ok := ev.Item.(*llm.ChatMessage)
		if !ok || msg.TextContent() != "closing after hook pause" {
			t.Fatalf("ConversationItemAdded item = %#v, want closing after hook pause", ev.Item)
		}
	case <-time.After(time.Second):
		t.Fatal("ConversationItemAddedEvents did not receive closing after hook pause")
	}
	if agent.ChatCtx == nil || len(agent.ChatCtx.Items) != 1 {
		t.Fatalf("chat context items = %#v, want one closing user message", agent.ChatCtx)
	}
	if msg, ok := agent.ChatCtx.Items[0].(*llm.ChatMessage); !ok || msg.TextContent() != "closing after hook pause" {
		t.Fatalf("chat context item = %#v, want closing after hook pause message", agent.ChatCtx.Items[0])
	}
	select {
	case ev := <-session.SpeechCreatedEvents():
		t.Fatalf("reply generated after hook paused scheduling during close: %#v", ev)
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
		Alternatives: []stt.SpeechData{{Text: "stop response", Confidence: 0.9}},
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
		Alternatives: []stt.SpeechData{{Text: "hook error", Confidence: 0.9}},
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
		Alternatives: []stt.SpeechData{{Text: "no llm", Confidence: 0.9}},
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
		t.Fatalf("OnUserTurnCompleted called before STT end-of-speech with %q", msg.TextContent())
	case <-time.After(20 * time.Millisecond):
	}

	activity.OnEndOfSpeech(nil)

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

func TestAgentActivityVADEndOfSpeechWithoutFinalKeepsInterimTranscript(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeVAD
	agent.VAD = &fakePipelineVAD{}
	agent.STT = &fakePipelineSTT{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{MinEndpointingDelay: 0.01})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()
	activity.speaking = true

	activity.OnInterimTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "interim only"}},
	})
	activity.OnEndOfSpeech(&vad.VADEvent{Type: vad.VADEventEndOfSpeech})

	select {
	case msg := <-agent.turns:
		t.Fatalf("OnUserTurnCompleted called before final transcript with %q", msg.TextContent())
	case <-time.After(40 * time.Millisecond):
	}

	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{
		SkipReply:         true,
		TranscriptTimeout: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "interim only" {
		t.Fatalf("CommitUserTurn transcript = %q, want interim only after VAD EOU without final", transcript)
	}
}

func TestAgentActivityAutomaticTurnRejectsZeroConfidenceTranscript(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeSTT
	agent.STT = &fakePipelineSTT{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{MinEndpointingDelay: 0.01})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "phantom automatic turn", Confidence: 0}},
	})

	select {
	case msg := <-agent.turns:
		t.Fatalf("OnUserTurnCompleted called for zero-confidence automatic transcript with %q", msg.TextContent())
	case <-time.After(50 * time.Millisecond):
	}
	if len(agent.ChatCtx.Items) != 0 {
		t.Fatalf("agent chat context has %d items, want no zero-confidence automatic message", len(agent.ChatCtx.Items))
	}
	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "" {
		t.Fatalf("CommitUserTurn transcript = %q, want empty after rejecting zero-confidence automatic transcript", transcript)
	}
}

func TestAgentActivityAutomaticShortInterruptionRetainsPendingTranscript(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeSTT
	agent.STT = &fakePipelineSTT{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		MinEndpointingDelay:  0.01,
		MinInterruptionWords: 2,
	})
	activity := NewAgentActivity(agent, session)
	defer activity.Stop()
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "hi", Confidence: 0.9}},
	})

	select {
	case msg := <-agent.turns:
		t.Fatalf("OnUserTurnCompleted called for short false interruption with %q", msg.TextContent())
	case <-time.After(50 * time.Millisecond):
	}
	current.MarkDone()
	activity.currentSpeech = nil

	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{
		SkipReply: true,
	})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "hi" {
		t.Fatalf("CommitUserTurn transcript = %q, want retained short transcript", transcript)
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

func TestAgentActivityPreemptiveGenerationSchedulesMatchingFinalTurn(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeVAD
	agent.LLM = &fakeGenerationLLM{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	defer activity.Stop()

	speechEvents := session.SpeechCreatedEvents()
	conversationEvents := session.ConversationItemAddedEvents()
	activity.OnStartOfSpeech(&vad.VADEvent{})
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "answer quickly", Confidence: 0.91}},
	})

	var preemptive *SpeechHandle
	select {
	case ev := <-speechEvents:
		preemptive = ev.SpeechHandle
		if preemptive == nil || preemptive.IsScheduled() {
			t.Fatalf("preemptive speech = %#v, want unscheduled handle", preemptive)
		}
	case <-time.After(time.Second):
		t.Fatal("SpeechCreatedEvents did not receive preemptive generation")
	}

	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "answer quickly" {
		t.Fatalf("CommitUserTurn transcript = %q, want final transcript", transcript)
	}
	if !preemptive.IsScheduled() {
		t.Fatal("preemptive speech was not scheduled after matching turn completion")
	}
	select {
	case ev := <-conversationEvents:
		if ev.Item == nil || ev.Item.(*llm.ChatMessage).TextContent() != "answer quickly" {
			t.Fatalf("ConversationItemAdded event = %#v, want preemptive user message", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("ConversationItemAddedEvents did not receive scheduled preemptive message")
	}
	select {
	case ev := <-speechEvents:
		t.Fatalf("SpeechCreatedEvents received second generation %#v, want reused preemptive handle", ev)
	default:
	}
}

func TestAgentActivityPreemptiveGenerationStartsLLMBeforeScheduling(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeVAD
	countingLLM := &preemptiveCountingLLM{
		calls: make(chan struct{}, 2),
		stream: &fakeGenerationLLMStream{chunks: []*llm.ChatChunk{{
			Delta: &llm.ChoiceDelta{Content: "early reply"},
		}}},
	}
	agent.LLM = countingLLM
	agent.TTS = &fakePipelineTTS{stream: &fakePipelineTTSStream{}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	pipeline := NewPipelineAgent(nil, nil, agent.LLM, agent.TTS, agent.ChatCtx)
	pipeline.session = session
	session.Assistant = pipeline
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	defer activity.Stop()

	speechEvents := session.SpeechCreatedEvents()
	activity.OnStartOfSpeech(&vad.VADEvent{})
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "start early", Confidence: 0.93}},
	})

	var preemptive *SpeechHandle
	select {
	case ev := <-speechEvents:
		if ev.UserInitiated {
			t.Fatal("preemptive SpeechCreated UserInitiated = true, want false for automatic audio reply")
		}
		preemptive = ev.SpeechHandle
	case <-time.After(time.Second):
		t.Fatal("SpeechCreatedEvents did not receive preemptive generation")
	}
	if preemptive == nil || preemptive.IsScheduled() {
		t.Fatalf("preemptive speech = %#v, want unscheduled handle", preemptive)
	}
	select {
	case <-countingLLM.calls:
	case <-time.After(time.Second):
		t.Fatal("LLM was not started for preemptive generation before scheduling")
	}

	if _, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{}); err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if !preemptive.IsScheduled() {
		t.Fatal("preemptive speech was not scheduled after matching turn completion")
	}
	select {
	case <-countingLLM.calls:
		t.Fatal("LLM started again after scheduling reused preemptive speech")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestAgentActivityPreemptiveGenerationUsesActivePipelineLLM(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeVAD
	countingLLM := &preemptiveCountingLLM{
		calls: make(chan struct{}, 2),
		stream: &fakeGenerationLLMStream{chunks: []*llm.ChatChunk{{
			Delta: &llm.ChoiceDelta{Content: "early pipeline reply"},
		}}},
	}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	pipeline := NewPipelineAgent(nil, nil, countingLLM, &fakePipelineTTS{stream: &fakePipelineTTSStream{}}, agent.ChatCtx)
	pipeline.session = session
	session.Assistant = pipeline
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	defer activity.Stop()

	speechEvents := session.SpeechCreatedEvents()
	activity.OnStartOfSpeech(&vad.VADEvent{})
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "start active pipeline early", Confidence: 0.93}},
	})

	var preemptive *SpeechHandle
	select {
	case ev := <-speechEvents:
		preemptive = ev.SpeechHandle
	case <-time.After(time.Second):
		t.Fatal("SpeechCreatedEvents did not receive active pipeline preemptive generation")
	}
	if preemptive == nil || preemptive.IsScheduled() {
		t.Fatalf("preemptive speech = %#v, want unscheduled handle", preemptive)
	}
	select {
	case <-countingLLM.calls:
	case <-time.After(time.Second):
		t.Fatal("active pipeline LLM was not started for preemptive generation before scheduling")
	}

	if _, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{}); err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if !preemptive.IsScheduled() {
		t.Fatal("active pipeline preemptive speech was not scheduled after matching turn completion")
	}
	select {
	case <-countingLLM.calls:
		t.Fatal("active pipeline LLM started again after scheduling reused preemptive speech")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestAgentActivityPreflightTranscriptStartsPreemptiveGeneration(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeVAD
	countingLLM := &preemptiveCountingLLM{
		calls: make(chan struct{}, 2),
		stream: &fakeGenerationLLMStream{chunks: []*llm.ChatChunk{{
			Delta: &llm.ChoiceDelta{Content: "preflight reply"},
		}}},
	}
	agent.LLM = countingLLM
	agent.TTS = &fakePipelineTTS{stream: &fakePipelineTTSStream{}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	pipeline := NewPipelineAgent(nil, nil, agent.LLM, agent.TTS, agent.ChatCtx)
	pipeline.session = session
	session.Assistant = pipeline
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	defer activity.Stop()

	speechEvents := session.SpeechCreatedEvents()
	activity.OnStartOfSpeech(&vad.VADEvent{})
	activity.OnInterimTranscript(&stt.SpeechEvent{
		Type: stt.SpeechEventPreflightTranscript,
		Alternatives: []stt.SpeechData{{
			Text:       "preflight start",
			Confidence: 0.83,
		}},
	})

	var preemptive *SpeechHandle
	select {
	case ev := <-speechEvents:
		preemptive = ev.SpeechHandle
	case <-time.After(time.Second):
		t.Fatal("SpeechCreatedEvents did not receive preflight preemptive generation")
	}
	if preemptive == nil || preemptive.IsScheduled() {
		t.Fatalf("preemptive speech = %#v, want unscheduled handle", preemptive)
	}
	select {
	case <-countingLLM.calls:
	case <-time.After(time.Second):
		t.Fatal("LLM was not started for preflight preemptive generation")
	}

	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "preflight start" {
		t.Fatalf("CommitUserTurn transcript = %q, want preflight start", transcript)
	}
	if !preemptive.IsScheduled() {
		t.Fatal("preflight preemptive speech was not scheduled after matching turn completion")
	}
}

func TestAgentActivityPreflightTranscriptIncludesPriorFinalTranscript(t *testing.T) {
	agent := NewAgent("test")
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "hello", Confidence: 0.8}},
	})
	activity.OnInterimTranscript(&stt.SpeechEvent{
		Type: stt.SpeechEventPreflightTranscript,
		Alternatives: []stt.SpeechData{{
			Text:       "world",
			Confidence: 0.6,
		}},
	})

	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "hello world" {
		t.Fatalf("CommitUserTurn transcript = %q, want final plus preflight transcript", transcript)
	}
}

func TestAgentActivityMatchingFinalTranscriptKeepsPreflightGeneration(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeVAD
	countingLLM := &preemptiveCountingLLM{
		calls: make(chan struct{}, 3),
		stream: &fakeGenerationLLMStream{chunks: []*llm.ChatChunk{{
			Delta: &llm.ChoiceDelta{Content: "preflight reply"},
		}}},
	}
	agent.LLM = countingLLM
	agent.TTS = &fakePipelineTTS{stream: &fakePipelineTTSStream{}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	pipeline := NewPipelineAgent(nil, nil, agent.LLM, agent.TTS, agent.ChatCtx)
	pipeline.session = session
	session.Assistant = pipeline
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	defer activity.Stop()

	speechEvents := session.SpeechCreatedEvents()
	activity.OnStartOfSpeech(&vad.VADEvent{})
	activity.OnInterimTranscript(&stt.SpeechEvent{
		Type: stt.SpeechEventPreflightTranscript,
		Alternatives: []stt.SpeechData{{
			Text:       "same final",
			Confidence: 0.8,
		}},
	})

	var preemptive *SpeechHandle
	select {
	case ev := <-speechEvents:
		preemptive = ev.SpeechHandle
	case <-time.After(time.Second):
		t.Fatal("SpeechCreatedEvents did not receive preflight generation")
	}
	select {
	case <-countingLLM.calls:
	case <-time.After(time.Second):
		t.Fatal("LLM was not started for preflight generation")
	}

	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{
			Text:       "same final",
			Confidence: 0.8,
		}},
	})

	select {
	case ev := <-speechEvents:
		t.Fatalf("SpeechCreatedEvents received second generation %#v, want preflight handle kept", ev)
	default:
	}
	select {
	case <-countingLLM.calls:
		t.Fatal("LLM started again for matching final transcript")
	default:
	}

	if _, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{}); err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if !preemptive.IsScheduled() {
		t.Fatal("preflight generation was not scheduled after matching final turn")
	}
}

func TestAgentActivityPreemptiveGenerationCancelsStaleBeforeRetryGuard(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeVAD
	agent.LLM = &fakeGenerationLLM{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		PreemptiveGenerationMaxRetries:    1,
		PreemptiveGenerationMaxRetriesSet: true,
	})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	defer activity.Stop()

	speechEvents := session.SpeechCreatedEvents()
	activity.OnStartOfSpeech(&vad.VADEvent{})
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "first partial", Confidence: 0.91}},
	})

	var preemptive *SpeechHandle
	select {
	case ev := <-speechEvents:
		preemptive = ev.SpeechHandle
	case <-time.After(time.Second):
		t.Fatal("SpeechCreatedEvents did not receive first preemptive generation")
	}
	if preemptive == nil || preemptive.IsInterrupted() {
		t.Fatalf("first preemptive speech = %#v, want active unscheduled handle", preemptive)
	}

	activity.OnInterimTranscript(&stt.SpeechEvent{
		Type:         stt.SpeechEventPreflightTranscript,
		Alternatives: []stt.SpeechData{{Text: "second partial", Confidence: 0.92}},
	})

	if !preemptive.IsInterrupted() {
		t.Fatal("stale preemptive speech was not interrupted before retry guard blocked replacement")
	}
	select {
	case ev := <-speechEvents:
		t.Fatalf("SpeechCreatedEvents received retry %#v, want retry guard to block replacement", ev)
	default:
	}
}

func TestAgentActivityPreemptiveGenerationStartsTTSBeforeSchedulingWhenEnabled(t *testing.T) {
	agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeVAD
	agent.LLM = &preemptiveCountingLLM{
		calls: make(chan struct{}, 2),
		stream: &fakeGenerationLLMStream{chunks: []*llm.ChatChunk{{
			Delta: &llm.ChoiceDelta{Content: "spoken early"},
		}}},
	}
	countingTTS := &preemptiveCountingTTS{
		calls: make(chan struct{}, 2),
		stream: &fakePipelineTTSStream{frames: []*model.AudioFrame{{
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: 240,
			Data:              []byte{1, 2, 3, 4},
		}}},
	}
	agent.TTS = countingTTS
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		PreemptiveGenerationPreemptiveTTS:    true,
		PreemptiveGenerationPreemptiveTTSSet: true,
	})
	pipeline := NewPipelineAgent(nil, nil, agent.LLM, agent.TTS, agent.ChatCtx)
	pipeline.session = session
	session.Assistant = pipeline
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	defer activity.Stop()

	speechEvents := session.SpeechCreatedEvents()
	activity.OnStartOfSpeech(&vad.VADEvent{})
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "start speech early", Confidence: 0.93}},
	})

	var preemptive *SpeechHandle
	select {
	case ev := <-speechEvents:
		preemptive = ev.SpeechHandle
	case <-time.After(time.Second):
		t.Fatal("SpeechCreatedEvents did not receive preemptive generation")
	}
	if preemptive == nil || preemptive.IsScheduled() {
		t.Fatalf("preemptive speech = %#v, want unscheduled handle", preemptive)
	}
	select {
	case <-countingTTS.calls:
	case <-time.After(time.Second):
		t.Fatal("TTS was not started for preemptive generation before scheduling")
	}

	if _, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{}); err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if !preemptive.IsScheduled() {
		t.Fatal("preemptive speech was not scheduled after matching turn completion")
	}
	select {
	case <-countingTTS.calls:
		t.Fatal("TTS started again after scheduling reused preemptive speech")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestAgentActivityPreemptiveGenerationCancelsWhenTurnHookMutatesChatContext(t *testing.T) {
	agent := &mutatingTurnAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeVAD
	agent.LLM = &fakeGenerationLLM{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	defer activity.Stop()

	speechEvents := session.SpeechCreatedEvents()
	activity.OnStartOfSpeech(&vad.VADEvent{})
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "revise context", Confidence: 0.88}},
	})

	var preemptive *SpeechHandle
	select {
	case ev := <-speechEvents:
		preemptive = ev.SpeechHandle
	case <-time.After(time.Second):
		t.Fatal("SpeechCreatedEvents did not receive preemptive generation")
	}

	if _, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{}); err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if !preemptive.IsInterrupted() {
		t.Fatal("preemptive speech was not interrupted after chat context mutation")
	}
	select {
	case ev := <-speechEvents:
		if ev.SpeechHandle == preemptive {
			t.Fatal("second SpeechCreated event reused invalidated preemptive handle")
		}
		if ev.SpeechHandle == nil || !ev.SpeechHandle.IsScheduled() {
			t.Fatalf("replacement speech = %#v, want scheduled handle", ev.SpeechHandle)
		}
	case <-time.After(time.Second):
		t.Fatal("SpeechCreatedEvents did not receive replacement generation")
	}
}

func TestAgentActivityPreemptiveGenerationInvalidationCancelsProviderWork(t *testing.T) {
	agent := &mutatingTurnAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	agent.TurnDetection = TurnDetectionModeVAD
	agent.LLM = &preemptiveCountingLLM{
		calls: make(chan struct{}, 2),
		stream: &fakeGenerationLLMStream{chunks: []*llm.ChatChunk{{
			Delta: &llm.ChoiceDelta{Content: "cancel me"},
		}}},
	}
	cancelAwareTTS := &cancelAwarePreemptiveTTS{
		calls:    make(chan struct{}, 1),
		canceled: make(chan struct{}, 1),
	}
	agent.TTS = cancelAwareTTS
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		PreemptiveGenerationPreemptiveTTS:    true,
		PreemptiveGenerationPreemptiveTTSSet: true,
	})
	pipeline := NewPipelineAgent(nil, nil, agent.LLM, agent.TTS, agent.ChatCtx)
	pipeline.session = session
	session.Assistant = pipeline
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	defer activity.Stop()

	speechEvents := session.SpeechCreatedEvents()
	activity.OnStartOfSpeech(&vad.VADEvent{})
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Alternatives: []stt.SpeechData{{Text: "cancel preemptive", Confidence: 0.93}},
	})
	select {
	case <-speechEvents:
	case <-time.After(time.Second):
		t.Fatal("SpeechCreatedEvents did not receive preemptive generation")
	}
	select {
	case <-cancelAwareTTS.calls:
	case <-time.After(time.Second):
		t.Fatal("TTS was not started for preemptive generation")
	}

	if _, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{}); err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	select {
	case <-cancelAwareTTS.canceled:
	case <-time.After(time.Second):
		t.Fatal("invalidated preemptive generation did not cancel provider work")
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

func TestAgentActivityUserTurnExceededKeepsLatestWhileWaiting(t *testing.T) {
	agent := &countingExceededAgent{Agent: NewAgent("test"), calls: make(chan UserTurnExceededEvent, 1)}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(agent, session)
	agent.activity = activity
	session.activity = activity
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current

	activity.OnUserTurnExceeded(UserTurnExceededEvent{Transcript: "first", AccumulatedWordCount: 3})
	activity.OnUserTurnExceeded(UserTurnExceededEvent{Transcript: "second", AccumulatedWordCount: 4})
	current.MarkDone()

	select {
	case ev := <-agent.calls:
		if ev.Transcript != "second" || ev.AccumulatedWordCount != 4 {
			t.Fatalf("OnUserTurnExceeded event = %#v, want latest second/4", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("OnUserTurnExceeded was not called after active speech completed")
	}
	select {
	case ev := <-agent.calls:
		t.Fatalf("OnUserTurnExceeded called more than once: %#v", ev)
	case <-time.After(20 * time.Millisecond):
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

type recordingTranscriptFlusher struct {
	calls         int
	flushDuration time.Duration
	flushed       chan struct{}
}

func (r *recordingTranscriptFlusher) Start(context.Context, *AgentSession) error { return nil }

func (r *recordingTranscriptFlusher) OnAudioFrame(context.Context, *model.AudioFrame) {}

func (r *recordingTranscriptFlusher) SetPublishAudio(func(context.Context, *model.AudioFrame) error) {
}

func (r *recordingTranscriptFlusher) FlushInputTranscription(_ context.Context, duration time.Duration) error {
	r.calls++
	r.flushDuration = duration
	select {
	case r.flushed <- struct{}{}:
	default:
	}
	return nil
}

type recordingInputTranscriptionClearer struct {
	clears int
}

func (r *recordingInputTranscriptionClearer) Start(context.Context, *AgentSession) error { return nil }

func (r *recordingInputTranscriptionClearer) OnAudioFrame(context.Context, *model.AudioFrame) {}

func (r *recordingInputTranscriptionClearer) SetPublishAudio(func(context.Context, *model.AudioFrame) error) {
}

func (r *recordingInputTranscriptionClearer) ClearInputTranscription() error {
	r.clears++
	return nil
}

type mutatingTurnAgent struct {
	*Agent
	turns chan *llm.ChatMessage
}

func (a *mutatingTurnAgent) OnUserTurnCompleted(ctx context.Context, chatCtx *llm.ChatContext, newMsg *llm.ChatMessage) error {
	a.turns <- newMsg
	chatCtx.AddMessage(llm.ChatMessageArgs{
		Role: llm.ChatRoleSystem,
		Text: "context changed",
	})
	return nil
}

type preemptiveCountingLLM struct {
	calls  chan struct{}
	stream llm.LLMStream
}

func (p *preemptiveCountingLLM) Chat(context.Context, *llm.ChatContext, ...llm.ChatOption) (llm.LLMStream, error) {
	p.calls <- struct{}{}
	return p.stream, nil
}

type preemptiveCountingTTS struct {
	calls  chan struct{}
	stream tts.SynthesizeStream
}

func (p *preemptiveCountingTTS) Label() string { return "preemptive-counting-tts" }

func (p *preemptiveCountingTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true}
}

func (p *preemptiveCountingTTS) SampleRate() int { return 24000 }

func (p *preemptiveCountingTTS) NumChannels() int { return 1 }

func (p *preemptiveCountingTTS) Synthesize(context.Context, string) (tts.ChunkedStream, error) {
	return nil, errors.New("Synthesize should not be called for streaming TTS")
}

func (p *preemptiveCountingTTS) Stream(context.Context) (tts.SynthesizeStream, error) {
	p.calls <- struct{}{}
	return p.stream, nil
}

type cancelAwarePreemptiveTTS struct {
	calls    chan struct{}
	canceled chan struct{}
}

func (c *cancelAwarePreemptiveTTS) Label() string { return "cancel-aware-preemptive-tts" }

func (c *cancelAwarePreemptiveTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true}
}

func (c *cancelAwarePreemptiveTTS) SampleRate() int { return 24000 }

func (c *cancelAwarePreemptiveTTS) NumChannels() int { return 1 }

func (c *cancelAwarePreemptiveTTS) Synthesize(context.Context, string) (tts.ChunkedStream, error) {
	return nil, errors.New("Synthesize should not be called for streaming TTS")
}

func (c *cancelAwarePreemptiveTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	c.calls <- struct{}{}
	return &cancelAwarePreemptiveTTSStream{ctx: ctx, canceled: c.canceled}, nil
}

type cancelAwarePreemptiveTTSStream struct {
	ctx      context.Context
	canceled chan struct{}
}

func (s *cancelAwarePreemptiveTTSStream) PushText(string) error { return nil }

func (s *cancelAwarePreemptiveTTSStream) Flush() error { return nil }

func (s *cancelAwarePreemptiveTTSStream) Close() error { return nil }

func (s *cancelAwarePreemptiveTTSStream) Next() (*tts.SynthesizedAudio, error) {
	<-s.ctx.Done()
	select {
	case s.canceled <- struct{}{}:
	default:
	}
	return nil, io.EOF
}

type recordingAudioOutputController struct {
	canPause    bool
	pauseCount  int
	resumeCount int
}

func (r *recordingAudioOutputController) CanPauseAudioOutput() bool {
	return r.canPause
}

func (r *recordingAudioOutputController) PauseAudioOutput() {
	r.pauseCount++
}

func (r *recordingAudioOutputController) ResumeAudioOutput() {
	r.resumeCount++
}

type recordingActivityEndpointing struct {
	startCount       int
	endCount         int
	lastStart        float64
	lastEnd          float64
	lastShouldIgnore bool
}

func (r *recordingActivityEndpointing) UpdateOptions(*float64, *float64) {}
func (r *recordingActivityEndpointing) MinDelay() float64                { return 0 }
func (r *recordingActivityEndpointing) MaxDelay() float64                { return 0 }
func (r *recordingActivityEndpointing) Overlapping() bool                { return false }
func (r *recordingActivityEndpointing) OnStartOfAgentSpeech(float64)     {}
func (r *recordingActivityEndpointing) OnEndOfAgentSpeech(float64)       {}

func (r *recordingActivityEndpointing) OnStartOfSpeech(startedAt float64, _ bool) {
	r.startCount++
	r.lastStart = startedAt
}

func (r *recordingActivityEndpointing) OnEndOfSpeech(endedAt float64, shouldIgnore bool) {
	r.endCount++
	r.lastEnd = endedAt
	r.lastShouldIgnore = shouldIgnore
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

type thresholdTurnDetector struct {
	probability float64
	thresholds  map[string]float64
}

func (d thresholdTurnDetector) PredictEndOfTurn(context.Context, *llm.ChatContext) (float64, error) {
	return d.probability, nil
}

func (d thresholdTurnDetector) UnlikelyThreshold(language string) (float64, bool) {
	threshold, ok := d.thresholds[language]
	return threshold, ok
}

type recordingAudioTurnDetector struct {
	probability  float64
	calls        int
	frames       []*model.AudioFrame
	originalData []byte
}

func (d *recordingAudioTurnDetector) PredictEndOfTurnAudio(ctx context.Context, frames []*model.AudioFrame) (float64, error) {
	d.calls++
	d.frames = frames
	return d.probability, nil
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

func waitForAECWarmupInactive(t *testing.T, session *AgentSession) {
	t.Helper()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			t.Fatal("AEC warmup did not expire")
		case <-ticker.C:
			if !session.aecWarmupActive() {
				return
			}
		}
	}
}

type recordingScheduledSpeechAssistant struct {
	scheduledCh chan *SpeechHandle
}

type realtimeUserTranscriptionAssistant struct{}

func (r realtimeUserTranscriptionAssistant) Start(context.Context, *AgentSession) error {
	return nil
}

func (r realtimeUserTranscriptionAssistant) OnAudioFrame(context.Context, *model.AudioFrame) {
}

func (r realtimeUserTranscriptionAssistant) SetPublishAudio(func(context.Context, *model.AudioFrame) error) {
}

func (r realtimeUserTranscriptionAssistant) RealtimeCapabilities() llm.RealtimeCapabilities {
	return llm.RealtimeCapabilities{UserTranscription: true}
}

func (r *recordingScheduledSpeechAssistant) Start(context.Context, *AgentSession) error {
	return nil
}

func (r *recordingScheduledSpeechAssistant) OnAudioFrame(context.Context, *model.AudioFrame) {
}

func (r *recordingScheduledSpeechAssistant) SetPublishAudio(func(context.Context, *model.AudioFrame) error) {
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

type recordingOptionsAssistant struct {
	options llm.RealtimeSessionOptions
}

func (r *recordingOptionsAssistant) Start(context.Context, *AgentSession) error {
	return nil
}

func (r *recordingOptionsAssistant) OnAudioFrame(context.Context, *model.AudioFrame) {
}

func (r *recordingOptionsAssistant) SetPublishAudio(func(context.Context, *model.AudioFrame) error) {
}

func (r *recordingOptionsAssistant) UpdateOptions(_ context.Context, options llm.RealtimeSessionOptions) error {
	r.options = options
	return nil
}

type recordingRealtimeCommitAssistant struct {
	capabilities llm.RealtimeCapabilities
	commits      int
	clears       int
	interrupts   int
}

func (r *recordingRealtimeCommitAssistant) Start(context.Context, *AgentSession) error {
	return nil
}

func (r *recordingRealtimeCommitAssistant) OnAudioFrame(context.Context, *model.AudioFrame) {
}

func (r *recordingRealtimeCommitAssistant) SetPublishAudio(func(context.Context, *model.AudioFrame) error) {
}

func (r *recordingRealtimeCommitAssistant) CommitAudio() error {
	r.commits++
	return nil
}

func (r *recordingRealtimeCommitAssistant) RealtimeCapabilities() llm.RealtimeCapabilities {
	return r.capabilities
}

func (r *recordingRealtimeCommitAssistant) ClearAudio() error {
	r.clears++
	return nil
}

func (r *recordingRealtimeCommitAssistant) Interrupt() error {
	r.interrupts++
	return nil
}

type fakeActivityMCPServer struct {
	tools         []llm.Tool
	initializeErr error
	initialized   bool
}

func (f *fakeActivityMCPServer) Initialize(context.Context) error {
	if f.initializeErr != nil {
		return f.initializeErr
	}
	f.initialized = true
	return nil
}

func (f *fakeActivityMCPServer) Initialized() bool { return f.initialized }

func (f *fakeActivityMCPServer) InvalidateCache() {}

func (f *fakeActivityMCPServer) ListTools(context.Context) ([]llm.Tool, error) {
	return f.tools, nil
}

func (f *fakeActivityMCPServer) Close() error { return nil }

type nestedAgentToolset struct {
	agentTestTool
	tools []llm.Tool
}

func (n *nestedAgentToolset) Tools() []llm.Tool { return n.tools }

func agentActivityChatItemIDs(items []llm.ChatItem) string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.GetID())
	}
	return strings.Join(ids, ",")
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
