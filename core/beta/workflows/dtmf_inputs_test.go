package workflows

import (
	"context"
	"strings"
	"testing"
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
