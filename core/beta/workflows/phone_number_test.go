package workflows

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
)

func TestGetPhoneNumberTaskRecordsValidNumberWithoutConfirmation(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"(555) 123-4567"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "Phone number captured and task completed." {
		t.Fatalf("Execute() output = %q, want completion message", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want normalized digits", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after valid phone number")
	}
}

func TestGetPhoneNumberTaskRejectsInvalidNumber(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	_, err := tool.Execute(context.Background(), `{"phone_number":"000-12"}`)
	if err == nil {
		t.Fatal("Execute() error = nil, want invalid phone number error")
	}
	if !strings.Contains(err.Error(), "Invalid phone number provided") {
		t.Fatalf("Execute() error = %v, want invalid phone number", err)
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for invalid phone", result)
	default:
	}
}

func TestGetPhoneNumberTaskRequiresConfirmation(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{})
	update := &updatePhoneNumberTool{task: task}

	out, err := update.Execute(context.Background(), `{"phone_number":"+1 555 123 4567"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if out == "" {
		t.Fatal("update Execute() output is empty, want confirmation prompt guidance")
	}
	if len(task.Agent.Tools) != 3 || task.Agent.Tools[2].Name() != "confirm_phone_number" {
		t.Fatalf("tools = %#v, want confirm_phone_number appended", task.Agent.Tools)
	}

	confirm := &confirmPhoneNumberTool{task: task, phoneNumber: "+15551234567"}
	if _, err := confirm.Execute(context.Background(), `{}`); err != nil {
		t.Fatalf("confirm Execute() error = %v", err)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "+15551234567" {
			t.Fatalf("PhoneNumber = %q, want +15551234567", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after confirmation")
	}
}

func TestGetPhoneNumberTaskCanDisableDefaultConfirmation(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmation: false, RequireConfirmationSet: true})
	update := &updatePhoneNumberTool{task: task}

	out, err := update.Execute(context.Background(), `{"phone_number":"(555) 123-4567"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if out != "Phone number captured and task completed." {
		t.Fatalf("update Execute() output = %q, want completion message", out)
	}
}

func TestGetPhoneNumberTaskInstructionsIncludeReferenceConfirmationWhenEnabled(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{})

	want := "Call `confirm_phone_number` after the user confirmed the phone number is correct."
	if !strings.Contains(task.Instructions, want) {
		t.Fatalf("Instructions = %q, want reference confirmation instruction %q", task.Instructions, want)
	}
}

func TestGetPhoneNumberTaskInstructionsUseReferenceBehaviorGuidance(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{})

	for _, want := range []string{
		"Call `update_phone_number` at the first opportunity whenever you form a new hypothesis about the phone number. (before asking any questions or providing any answers.)",
		"Avoid verbosity by not sharing example phone numbers or formats unless prompted to do so. Do not deviate from the goal of collecting the user's phone number.",
		"Always explicitly invoke a tool when applicable. Do not simulate tool usage, no real action is taken unless the tool is explicitly called.",
	} {
		if !strings.Contains(task.Instructions, want) {
			t.Fatalf("Instructions = %q, want reference guidance %q", task.Instructions, want)
		}
	}
}

func TestGetPhoneNumberTaskInstructionsOmitConfirmationWhenDisabled(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmation: false, RequireConfirmationSet: true})

	if strings.Contains(task.Instructions, "confirm_phone_number") {
		t.Fatalf("Instructions = %q, want no confirm_phone_number guidance when confirmation disabled", task.Instructions)
	}
}

func TestGetPhoneNumberTaskStaleConfirmationPromptsForUpdatedNumber(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{})
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
		t.Fatal("timed out waiting for phone on-enter prompt")
	}

	update := &updatePhoneNumberTool{task: task}
	if _, err := update.Execute(context.Background(), `{"phone_number":"+1 555 123 4567"}`); err != nil {
		t.Fatalf("first update Execute() error = %v", err)
	}
	staleConfirm := &confirmPhoneNumberTool{task: task, phoneNumber: "+15551234567"}

	if _, err := update.Execute(context.Background(), `{"phone_number":"+1 555 987 6543"}`); err != nil {
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
		want := phoneNumberStaleConfirmationPrompt()
		if ev.SpeechHandle.Generation.Instructions == nil {
			t.Fatal("stale confirmation instructions = nil, want changed-phone prompt")
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

func TestDeclinePhoneNumberCaptureToolFailsWithReason(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &declinePhoneNumberCaptureTool{task: task}

	if _, err := tool.Execute(context.Background(), `{"reason":"user refused"}`); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	_, err := task.WaitAny(context.Background())
	if err == nil || err.Error() != "couldn't get the phone number: user refused" {
		t.Fatalf("WaitAny() error = %v, want decline reason", err)
	}
}
