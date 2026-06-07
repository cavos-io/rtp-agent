package workflows

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
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
	if !strings.Contains(out, "Repeat the email character by character: a d a @ e x a m p l e . c o m if needed") {
		t.Fatalf("update Execute() output = %q, want character-by-character guidance", out)
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

func TestGetEmailTaskInstructionsOmitConfirmationWhenDisabled(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{RequireConfirmation: false, RequireConfirmationSet: true})

	if strings.Contains(task.Instructions, "confirm_email_address") {
		t.Fatalf("Instructions = %q, want no confirm_email_address guidance when confirmation disabled", task.Instructions)
	}
}

func TestGetEmailTaskInstructionsUseReferenceToolGuidance(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{})

	for _, want := range []string{
		"Call `update_email_address` at the first opportunity whenever you form a new hypothesis about the email. (before asking any questions or providing any answers.)",
		"Always explicitly invoke a tool when applicable. Do not simulate tool usage, no real action is taken unless the tool is explicitly called.",
	} {
		if !strings.Contains(task.Instructions, want) {
			t.Fatalf("Instructions = %q, want reference guidance %q", task.Instructions, want)
		}
	}
}

func TestGetEmailTaskOnEnterUsesReferencePrompt(t *testing.T) {
	want := "Ask the user to provide an email address."
	if got := emailOnEnterPrompt(); got != want {
		t.Fatalf("emailOnEnterPrompt() = %q, want %q", got, want)
	}
}

func TestGetEmailTaskStaleConfirmationPromptsForUpdatedEmail(t *testing.T) {
	task := NewGetEmailTask(GetEmailOptions{})
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
		t.Fatal("timed out waiting for email on-enter prompt")
	}

	update := &updateEmailTool{task: task}

	if _, err := update.Execute(context.Background(), `{"email":"ada@example.com"}`); err != nil {
		t.Fatalf("first update Execute() error = %v", err)
	}
	staleConfirm := &confirmEmailTool{task: task, email: "ada@example.com"}

	if _, err := update.Execute(context.Background(), `{"email":"grace@example.com"}`); err != nil {
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
		want := emailStaleConfirmationPrompt()
		if ev.SpeechHandle.Generation.Instructions == nil {
			t.Fatal("stale confirmation instructions = nil, want changed-email prompt")
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
