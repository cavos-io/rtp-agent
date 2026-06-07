package workflows

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
)

func TestGetNameTaskUpdatesRequiredPartsWithoutConfirmation(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	out, err := tool.Execute(context.Background(), `{"first_name":" Ada ","last_name":" Lovelace "}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "Name captured and task completed." {
		t.Fatalf("Execute() output = %q, want completion message", out)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" || result.MiddleName != "" {
			t.Fatalf("result = %#v, want Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after valid name")
	}
}

func TestGetNameTaskVerifySpellingAddsReferenceInstruction(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, VerifySpelling: true})

	for _, want := range []string{
		"After receiving the name, always verify the spelling by asking the user to confirm or spell out the name letter by letter.",
		"When confirming, spell out each name part letter by letter to the user.",
	} {
		if !strings.Contains(task.Instructions, want) {
			t.Fatalf("Instructions = %q, want spelling verification guidance %q", task.Instructions, want)
		}
	}
}

func TestGetNameTaskRejectsMissingRequiredPart(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmationSet: true})
	tool := &updateNameTool{task: task}

	_, err := tool.Execute(context.Background(), `{"first_name":"Ada"}`)
	if err == nil {
		t.Fatal("Execute() error = nil, want missing last name error")
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for incomplete name", result)
	default:
	}
}

func TestGetNameTaskRequiresConfirmation(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true})
	update := &updateNameTool{task: task}

	out, err := update.Execute(context.Background(), `{"first_name":"Ada","last_name":"Lovelace"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if !strings.Contains(out, "prompt for confirmation") {
		t.Fatalf("update output = %q, want confirmation guidance", out)
	}
	if !strings.Contains(out, "Repeat the name back to the user and prompt for confirmation") {
		t.Fatalf("update output = %q, want repeat-name confirmation guidance", out)
	}
	if len(task.Agent.Tools) != 3 || task.Agent.Tools[2].Name() != "confirm_name" {
		t.Fatalf("tools = %#v, want confirm_name appended", task.Agent.Tools)
	}

	confirm := &confirmNameTool{task: task, firstName: "Ada", lastName: "Lovelace"}
	if _, err := confirm.Execute(context.Background(), `{}`); err != nil {
		t.Fatalf("confirm Execute() error = %v", err)
	}

	select {
	case result := <-task.Result:
		if result.FirstName != "Ada" || result.LastName != "Lovelace" {
			t.Fatalf("result = %#v, want Ada Lovelace", result)
		}
	default:
		t.Fatal("task did not complete after confirmation")
	}
}

func TestGetNameTaskVerifySpellingOutputIncludesName(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, VerifySpelling: true})
	update := &updateNameTool{task: task}

	out, err := update.Execute(context.Background(), `{"first_name":"Ada","last_name":"Lovelace"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if !strings.Contains(out, "Spell out the name letter by letter for verification: Ada Lovelace") {
		t.Fatalf("update output = %q, want spell-out guidance with full name", out)
	}
}

func TestGetNameTaskCanDisableDefaultConfirmation(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true, RequireConfirmation: false, RequireConfirmationSet: true})
	update := &updateNameTool{task: task}

	out, err := update.Execute(context.Background(), `{"first_name":"Ada","last_name":"Lovelace"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if out != "Name captured and task completed." {
		t.Fatalf("update Execute() output = %q, want completion message", out)
	}
}

func TestGetNameTaskInstructionsIncludeReferenceConfirmationWhenEnabled(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true})

	wantParts := []string{
		"You need to naturally collect the name parts in this order: {first_name}.",
		"Call `update_name` at the first opportunity whenever you form a new hypothesis about the name. (before asking any questions or providing any answers.)",
		"Call `confirm_name` after the user confirmed the name is correct.",
		"If the name is unclear or it takes too much back-and-forth, prompt for each name part separately.",
		"Avoid verbosity by not sharing example names or spellings unless prompted to do so. Do not deviate from the goal of collecting the user's name.",
		"Always explicitly invoke a tool when applicable. Do not simulate tool usage, no real action is taken unless the tool is explicitly called.",
	}
	for _, want := range wantParts {
		if !strings.Contains(task.Instructions, want) {
			t.Fatalf("Instructions = %q, want reference instruction %q", task.Instructions, want)
		}
	}
}

func TestGetNameTaskInstructionsOmitConfirmationWhenDisabled(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, RequireConfirmation: false, RequireConfirmationSet: true})

	if strings.Contains(task.Instructions, "confirm_name") {
		t.Fatalf("Instructions = %q, want no confirm_name guidance when confirmation disabled", task.Instructions)
	}
}

func TestGetNameTaskStaleConfirmationPromptsForUpdatedName(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, LastName: true})
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
	case <-speechEvents:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for name on-enter prompt")
	}

	update := &updateNameTool{task: task}
	if _, err := update.Execute(context.Background(), `{"first_name":"Ada","last_name":"Lovelace"}`); err != nil {
		t.Fatalf("first update Execute() error = %v", err)
	}
	staleConfirm := &confirmNameTool{task: task, firstName: "Ada", lastName: "Lovelace"}

	if _, err := update.Execute(context.Background(), `{"first_name":"Grace","last_name":"Hopper"}`); err != nil {
		t.Fatalf("second update Execute() error = %v", err)
	}
	if _, err := staleConfirm.Execute(context.Background(), `{}`); err != nil {
		t.Fatalf("stale confirm Execute() error = %v, want nil after prompting for updated confirmation", err)
	}

	select {
	case ev := <-speechEvents:
		if ev.Source != "generate_reply" {
			t.Fatalf("SpeechCreated Source = %q, want generate_reply", ev.Source)
		}
		if ev.SpeechHandle == nil {
			t.Fatal("SpeechCreated SpeechHandle = nil, want stale confirmation reply handle")
		}
		want := nameStaleConfirmationPrompt()
		if ev.SpeechHandle.Generation.Instructions == nil {
			t.Fatal("stale confirmation instructions = nil, want changed-name prompt")
		}
		if got := ev.SpeechHandle.Generation.Instructions.AsModality("text").String(); got != want {
			t.Fatalf("stale confirmation instructions = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stale confirmation prompt")
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for stale confirmation", result)
	default:
	}
}

func TestGetNameTaskDeclineFailsTask(t *testing.T) {
	task := NewGetNameTask(GetNameOptions{FirstName: true, RequireConfirmationSet: true})
	tool := &declineNameCaptureTool{task: task}

	if _, err := tool.Execute(context.Background(), `{"reason":"privacy"}`); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, err := task.WaitAny(context.Background()); err == nil || !strings.Contains(err.Error(), "privacy") {
		t.Fatalf("WaitAny() error = %v, want privacy decline", err)
	}
}
