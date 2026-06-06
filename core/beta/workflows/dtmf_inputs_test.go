package workflows

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
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

func newDtmfTaskForTest(t *testing.T, numDigits int, askConfirmation bool) *GetDtmfTask {
	t.Helper()

	task, err := NewGetDtmfTask(numDigits, askConfirmation)
	if err != nil {
		t.Fatalf("NewGetDtmfTask() error = %v", err)
	}
	return task
}
