package workflows

import (
	"context"
	"testing"
)

func TestGetCardNumberTaskRecordsValidCardWithoutConfirmation(t *testing.T) {
	task := NewGetCardNumberTask(false)
	tool := &recordCardNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"card_number":"4111 1111-1111 1111"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "Card number captured and task completed." {
		t.Fatalf("Execute() output = %q, want completion message", out)
	}

	select {
	case result := <-task.Result:
		if result.Issuer != "Visa" {
			t.Fatalf("Issuer = %q, want Visa", result.Issuer)
		}
		if result.CardNumber != "4111111111111111" {
			t.Fatalf("CardNumber = %q, want normalized digits", result.CardNumber)
		}
	default:
		t.Fatal("task did not complete after valid card number")
	}
}

func TestGetCardNumberTaskRejectsInvalidLuhnNumber(t *testing.T) {
	task := NewGetCardNumberTask(false)
	tool := &recordCardNumberTool{task: task}

	_, err := tool.Execute(context.Background(), `{"card_number":"4111111111111112"}`)
	if err == nil {
		t.Fatal("Execute() error = nil, want invalid card number error")
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for invalid card", result)
	default:
	}
}

func TestGetCardNumberTaskRequiresMatchingConfirmation(t *testing.T) {
	task := NewGetCardNumberTask(true)
	record := &recordCardNumberTool{task: task}

	out, err := record.Execute(context.Background(), `{"card_number":"5555 5555 5555 4444"}`)
	if err != nil {
		t.Fatalf("record Execute() error = %v", err)
	}
	if out == "" {
		t.Fatal("record Execute() output is empty, want confirmation prompt guidance")
	}
	if len(task.Agent.Tools) != 4 || task.Agent.Tools[3].Name() != "confirm_card_number" {
		t.Fatalf("tools = %#v, want confirm_card_number appended", task.Agent.Tools)
	}

	confirm := &confirmCardNumberTool{task: task, cardNumber: "5555555555554444"}
	if _, err := confirm.Execute(context.Background(), `{"repeated_card_number":"5555555555554444"}`); err != nil {
		t.Fatalf("confirm Execute() error = %v", err)
	}

	select {
	case result := <-task.Result:
		if result.Issuer != "Mastercard" || result.CardNumber != "5555555555554444" {
			t.Fatalf("result = %#v, want Mastercard normalized card", result)
		}
	default:
		t.Fatal("task did not complete after matching confirmation")
	}
}

func TestGetSecurityCodeTaskRecordsValidCodeWithoutConfirmation(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	out, err := tool.Execute(context.Background(), `{"security_code":"012"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "Security code captured and task completed." {
		t.Fatalf("Execute() output = %q, want completion message", out)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "012" {
			t.Fatalf("SecurityCode = %q, want 012", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after valid security code")
	}
}

func TestGetSecurityCodeTaskRejectsInvalidCode(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	_, err := tool.Execute(context.Background(), `{"security_code":"12a"}`)
	if err == nil {
		t.Fatal("Execute() error = nil, want invalid security code error")
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for invalid code", result)
	default:
	}
}

func TestGetSecurityCodeTaskRequiresMatchingConfirmation(t *testing.T) {
	task := NewGetSecurityCodeTask(true)
	update := &updateSecurityCodeTool{task: task}

	out, err := update.Execute(context.Background(), `{"security_code":"1234"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if out == "" {
		t.Fatal("update Execute() output is empty, want confirmation prompt guidance")
	}
	if len(task.Agent.Tools) != 4 || task.Agent.Tools[3].Name() != "confirm_security_code" {
		t.Fatalf("tools = %#v, want confirm_security_code appended", task.Agent.Tools)
	}

	confirm := &confirmSecurityCodeTool{task: task, securityCode: "1234"}
	if _, err := confirm.Execute(context.Background(), `{"repeated_security_code":"1234"}`); err != nil {
		t.Fatalf("confirm Execute() error = %v", err)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "1234" {
			t.Fatalf("SecurityCode = %q, want 1234", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after matching confirmation")
	}
}
