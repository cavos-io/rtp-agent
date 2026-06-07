package workflows

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/beta"
)

func TestRecordInputsToolRejectsInvalidDtmfEvents(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	tool := &recordInputsTool{task: task}

	_, err := tool.Execute(context.Background(), `{"inputs":["1","12"]}`)
	if err == nil {
		t.Fatal("Execute() error = nil, want invalid DTMF event error")
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for invalid DTMF", result)
	default:
	}
}

func TestConfirmInputsToolRejectsInvalidDtmfEvents(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, true)
	tool := &confirmInputsTool{task: task}

	_, err := tool.Execute(context.Background(), `{"inputs":["1","x"]}`)
	if err == nil {
		t.Fatal("Execute() error = nil, want invalid DTMF event error")
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for invalid DTMF", result)
	default:
	}
}

func TestNewGetDtmfTaskRejectsNonPositiveNumDigits(t *testing.T) {
	if _, err := NewGetDtmfTask(0, false); err == nil {
		t.Fatal("NewGetDtmfTask(0, false) error = nil, want invalid num_digits error")
	}
}

func TestNewGetDtmfTaskWithOptionsAppendsExtraInstructions(t *testing.T) {
	task, err := NewGetDtmfTaskWithOptions(GetDtmfOptions{
		NumDigits:          4,
		AskForConfirmation: true,
		ExtraInstructions:  "Tell the user this is their appointment PIN.",
		DtmfInputTimeout:   4 * time.Second,
		DtmfStopEvent:      beta.DtmfEventPound,
	})
	if err != nil {
		t.Fatalf("NewGetDtmfTaskWithOptions() error = %v", err)
	}

	if !strings.Contains(task.Instructions, "Tell the user this is their appointment PIN.") {
		t.Fatalf("Instructions = %q, want extra instructions", task.Instructions)
	}
	if !strings.Contains(task.Instructions, "call `confirm_inputs`") {
		t.Fatalf("Instructions = %q, want confirmation guidance preserved", task.Instructions)
	}
}

func TestBuildDtmfConfirmationInstructionsMatchesReferencePrompt(t *testing.T) {
	got := buildDtmfConfirmationInstructions("1 2 3")

	if !strings.Contains(got, "<dtmf_inputs>1 2 3</dtmf_inputs>") {
		t.Fatalf("confirmation instructions = %q, want dtmf_inputs tag", got)
	}
	if !strings.Contains(got, "Please confirm it with the user by saying the digits one by one") {
		t.Fatalf("confirmation instructions = %q, want reference confirmation instruction", got)
	}
	if !strings.Contains(got, "call `confirm_inputs`") {
		t.Fatalf("confirmation instructions = %q, want confirm_inputs tool instruction", got)
	}
}

func TestGetDtmfTaskCompletesFromSessionSipDTMFEvents(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	task.Agent.Start(session, task)
	defer task.Agent.GetActivity().Stop()

	session.EmitSipDTMF(agent.SipDTMFEvent{Digit: "1", Code: 1})
	session.EmitSipDTMF(agent.SipDTMFEvent{Digit: "2", Code: 2})
	session.EmitSipDTMF(agent.SipDTMFEvent{Digit: "#", Code: 11})

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2" {
			t.Fatalf("UserInput = %q, want 1 2", result.UserInput)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for DTMF task completion")
	}
}

func TestGetDtmfTaskOnEnterGeneratesInitialReplyWithoutTools(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	session.Assistant = &fakeDtmfSessionAssistant{}
	speechEvents := session.SpeechCreatedEvents()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer session.Stop(context.Background())

	select {
	case ev := <-speechEvents:
		if ev.Source != "generate_reply" {
			t.Fatalf("SpeechCreated Source = %q, want generate_reply", ev.Source)
		}
		if ev.SpeechHandle == nil {
			t.Fatal("SpeechCreated SpeechHandle = nil, want initial reply handle")
		}
		if ev.SpeechHandle.Generation.ToolChoice != "none" {
			t.Fatalf("initial reply ToolChoice = %#v, want none", ev.SpeechHandle.Generation.ToolChoice)
		}
		if ev.SpeechHandle.Generation.UserMessage != nil {
			t.Fatalf("initial reply UserMessage = %#v, want nil", ev.SpeechHandle.Generation.UserMessage)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for DTMF initial reply")
	}
}

func TestGetDtmfTaskFlushesPendingInputsOnExit(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)

	task.onSipDTMFReceived("1")
	task.onSipDTMFReceived("2")
	task.OnExit()

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2" {
			t.Fatalf("UserInput = %q, want 1 2", result.UserInput)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for DTMF task completion after exit")
	}
}

func TestGetDtmfTaskDefersPendingReplyWhileUserSpeaking(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	task.DtmfInputTimeout = 20 * time.Millisecond
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	task.Agent.Start(session, task)
	defer task.Agent.GetActivity().Stop()

	session.EmitSipDTMF(agent.SipDTMFEvent{Digit: "1", Code: 1})
	session.EmitSipDTMF(agent.SipDTMFEvent{Digit: "2", Code: 2})
	session.UpdateUserState(agent.UserStateSpeaking)
	waitForDtmfTaskUserState(t, task, agent.UserStateSpeaking)
	time.Sleep(2 * task.DtmfInputTimeout)

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v while user was speaking, want pending input deferred", result)
	default:
	}

	session.UpdateUserState(agent.UserStateListening)

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2" {
			t.Fatalf("UserInput = %q, want 1 2", result.UserInput)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for DTMF task completion after user stopped speaking")
	}
}

func TestGetDtmfTaskDefersPendingReplyWhileAgentThinking(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	task.DtmfInputTimeout = 20 * time.Millisecond
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	task.Agent.Start(session, task)
	defer task.Agent.GetActivity().Stop()

	session.EmitSipDTMF(agent.SipDTMFEvent{Digit: "1", Code: 1})
	session.EmitSipDTMF(agent.SipDTMFEvent{Digit: "2", Code: 2})
	session.UpdateAgentState(agent.AgentStateThinking)
	waitForDtmfTaskAgentState(t, task, agent.AgentStateThinking)
	time.Sleep(2 * task.DtmfInputTimeout)

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v while agent was thinking, want pending input deferred", result)
	default:
	}

	session.UpdateAgentState(agent.AgentStateListening)

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2" {
			t.Fatalf("UserInput = %q, want 1 2", result.UserInput)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for DTMF task completion after agent stopped thinking")
	}
}

func waitForDtmfTaskUserState(t *testing.T, task *GetDtmfTask, want agent.UserState) {
	t.Helper()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		task.mu.Lock()
		got := task.userState
		task.mu.Unlock()
		if got == want {
			return
		}

		select {
		case <-deadline:
			t.Fatalf("DTMF task userState = %q, want %q", got, want)
		case <-ticker.C:
		}
	}
}

func waitForDtmfTaskAgentState(t *testing.T, task *GetDtmfTask, want agent.AgentState) {
	t.Helper()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		task.mu.Lock()
		got := task.agentState
		task.mu.Unlock()
		if got == want {
			return
		}

		select {
		case <-deadline:
			t.Fatalf("DTMF task agentState = %q, want %q", got, want)
		case <-ticker.C:
		}
	}
}

func newDtmfTaskForTest(t *testing.T, numDigits int, askConfirmation bool) *GetDtmfTask {
	t.Helper()

	task, err := NewGetDtmfTask(numDigits, askConfirmation)
	if err != nil {
		t.Fatalf("NewGetDtmfTask() error = %v", err)
	}
	return task
}

type fakeDtmfSessionAssistant struct{}

func (f *fakeDtmfSessionAssistant) Start(context.Context, *agent.AgentSession) error { return nil }
func (f *fakeDtmfSessionAssistant) OnAudioFrame(context.Context, *model.AudioFrame)  {}
func (f *fakeDtmfSessionAssistant) SetPublishAudio(func(frame *model.AudioFrame) error) {
}
