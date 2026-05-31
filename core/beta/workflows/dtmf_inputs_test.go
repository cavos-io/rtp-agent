package workflows

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/conversation-worker/core/agent"
)

func TestRecordInputsToolRejectsInvalidDtmfEvents(t *testing.T) {
	task := NewGetDtmfTask(2, false)
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
	task := NewGetDtmfTask(2, true)
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
	task := NewGetDtmfTask(2, false)
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
