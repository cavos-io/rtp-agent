package workflows

import (
	"context"
	"strings"
	"testing"
)

func TestGetEmailTaskRecordsEmailWithoutConfirmation(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	out, err := tool.Execute(context.Background(), `{"email":"ada@example.com"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "Email captured and task completed." {
		t.Fatalf("Execute() output = %q, want completion message", out)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want ada@example.com", result.Email)
		}
	default:
		t.Fatal("task did not complete after valid email")
	}
}

func TestGetEmailTaskRejectsInvalidEmail(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &updateEmailTool{task: task}

	_, err := tool.Execute(context.Background(), `{"email":"ada at example"}`)
	if err == nil {
		t.Fatal("Execute() error = nil, want invalid email error")
	}
	if !strings.Contains(err.Error(), "Invalid email address provided") {
		t.Fatalf("Execute() error = %v, want invalid email", err)
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for invalid email", result)
	default:
	}
}

func TestGetEmailTaskInjectsConfirmToolAfterUpdate(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{})
	if len(task.Agent.Tools) != 2 {
		t.Fatalf("initial tools = %d, want update/decline before email is captured", len(task.Agent.Tools))
	}

	update := &updateEmailTool{task: task}
	out, err := update.Execute(context.Background(), `{"email":"ada@example.com"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if out == "" {
		t.Fatal("update Execute() output is empty, want confirmation guidance")
	}
	if len(task.Agent.Tools) != 3 || task.Agent.Tools[2].Name() != "confirm_email_address" {
		t.Fatalf("tools = %#v, want confirm_email_address appended", task.Agent.Tools)
	}

	confirm := &confirmEmailTool{task: task, email: "ada@example.com"}
	if _, err := confirm.Execute(context.Background(), `{}`); err != nil {
		t.Fatalf("confirm Execute() error = %v", err)
	}

	select {
	case result := <-task.Result:
		if result.Email != "ada@example.com" {
			t.Fatalf("Email = %q, want ada@example.com", result.Email)
		}
	default:
		t.Fatal("task did not complete after confirmation")
	}
}

func TestGetEmailTaskCanDisableDefaultConfirmation(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmation: false, RequireConfirmationSet: true})
	update := &updateEmailTool{task: task}

	out, err := update.Execute(context.Background(), `{"email":"ada@example.com"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if out != "Email captured and task completed." {
		t.Fatalf("update Execute() output = %q, want completion message", out)
	}
}

func TestGetEmailTaskRejectsStaleConfirmation(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{})
	update := &updateEmailTool{task: task}

	if _, err := update.Execute(context.Background(), `{"email":"ada@example.com"}`); err != nil {
		t.Fatalf("first update Execute() error = %v", err)
	}
	staleConfirm := &confirmEmailTool{task: task, email: "ada@example.com"}

	if _, err := update.Execute(context.Background(), `{"email":"grace@example.com"}`); err != nil {
		t.Fatalf("second update Execute() error = %v", err)
	}
	if _, err := staleConfirm.Execute(context.Background(), `{}`); err == nil {
		t.Fatal("stale confirm Execute() error = nil, want changed-email error")
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for stale confirmation", result)
	default:
	}
}

func TestDeclineEmailCaptureToolFailsWithReason(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmationSet: true})
	tool := &declineEmailCaptureTool{task: task}

	if _, err := tool.Execute(context.Background(), `{"reason":"user refused"}`); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	_, err := task.WaitAny(context.Background())
	if err == nil || err.Error() != "couldn't get the email address: user refused" {
		t.Fatalf("WaitAny() error = %v, want decline reason", err)
	}
}
