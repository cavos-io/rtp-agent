package workflows

import (
	"context"
	"strings"
	"testing"
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

	if !strings.Contains(task.Instructions, "always verify the spelling") {
		t.Fatalf("Instructions = %q, want spelling verification guidance", task.Instructions)
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
