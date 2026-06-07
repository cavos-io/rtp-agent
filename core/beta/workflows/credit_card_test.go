package workflows

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
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
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	session.Assistant = &fakeDtmfSessionAssistant{}
	speechEvents := session.SpeechCreatedEvents()
	tool := &recordCardNumberTool{task: task}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer session.Stop(context.Background())

	select {
	case <-speechEvents:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for card-number on-enter prompt")
	}

	if _, err := tool.Execute(context.Background(), `{"card_number":"4111111111111112"}`); err != nil {
		t.Fatalf("Execute() error = %v, want nil after prompting for invalid card", err)
	}

	select {
	case ev := <-speechEvents:
		if ev.Source != "generate_reply" {
			t.Fatalf("SpeechCreated Source = %q, want generate_reply", ev.Source)
		}
		if ev.SpeechHandle == nil {
			t.Fatal("SpeechCreated SpeechHandle = nil, want invalid-card reply handle")
		}
		want := "The card number is not valid, ask the user if they made a mistake or to provide another card."
		if ev.SpeechHandle.Generation.Instructions == nil {
			t.Fatal("invalid-card instructions = nil, want invalid-card prompt")
		}
		if got := ev.SpeechHandle.Generation.Instructions.AsModality("text").String(); got != want {
			t.Fatalf("invalid-card instructions = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for invalid-card prompt")
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for invalid card", result)
	default:
	}
}

func TestGetCardNumberTaskDefersLuhnValidationUntilConfirmation(t *testing.T) {
	task := NewGetCardNumberTask()
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	session.Assistant = &fakeDtmfSessionAssistant{}
	speechEvents := session.SpeechCreatedEvents()
	record := &recordCardNumberTool{task: task}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer session.Stop(context.Background())

	select {
	case <-speechEvents:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for card-number on-enter prompt")
	}

	out, err := record.Execute(context.Background(), `{"card_number":"4111111111111112"}`)
	if err != nil {
		t.Fatalf("record Execute() error = %v, want confirmation prompt before Luhn validation", err)
	}
	if !strings.Contains(out, "Ask them to repeat the number, do not repeat the number back to them.") {
		t.Fatalf("record Execute() output = %q, want repeat prompt", out)
	}
	if len(task.Agent.Tools) != 4 || task.Agent.Tools[3].Name() != "confirm_card_number" {
		t.Fatalf("tools = %#v, want confirm_card_number appended", task.Agent.Tools)
	}

	confirm := &confirmCardNumberTool{task: task, cardNumber: "4111111111111112"}
	if _, err = confirm.Execute(context.Background(), `{"repeated_card_number":"4111111111111112"}`); err != nil {
		t.Fatalf("confirm Execute() error = %v, want nil after prompting for invalid card", err)
	}

	select {
	case ev := <-speechEvents:
		if ev.Source != "generate_reply" {
			t.Fatalf("SpeechCreated Source = %q, want generate_reply", ev.Source)
		}
		if ev.SpeechHandle == nil {
			t.Fatal("SpeechCreated SpeechHandle = nil, want invalid-card reply handle")
		}
		want := "The card number is not valid, ask the user if they made a mistake or to provide another card."
		if ev.SpeechHandle.Generation.Instructions == nil {
			t.Fatal("invalid-card instructions = nil, want invalid-card prompt")
		}
		if got := ev.SpeechHandle.Generation.Instructions.AsModality("text").String(); got != want {
			t.Fatalf("invalid-card instructions = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for invalid-card prompt")
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for invalid card", result)
	default:
	}
}

func TestGetCardNumberTaskRequiresMatchingConfirmation(t *testing.T) {
	task := NewGetCardNumberTask()
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

func TestGetCardNumberTaskMismatchedConfirmationPromptsForRetry(t *testing.T) {
	task := NewGetCardNumberTask()
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
		t.Fatal("timed out waiting for card-number on-enter prompt")
	}

	record := &recordCardNumberTool{task: task}
	if _, err := record.Execute(context.Background(), `{"card_number":"5555 5555 5555 4444"}`); err != nil {
		t.Fatalf("record Execute() error = %v", err)
	}
	confirm := &confirmCardNumberTool{task: task, cardNumber: "5555555555554444"}

	if _, err := confirm.Execute(context.Background(), `{"repeated_card_number":"4111111111111111"}`); err != nil {
		t.Fatalf("confirm Execute() error = %v, want nil after prompting for retry", err)
	}

	select {
	case ev := <-speechEvents:
		if ev.Source != "generate_reply" {
			t.Fatalf("SpeechCreated Source = %q, want generate_reply", ev.Source)
		}
		if ev.SpeechHandle == nil {
			t.Fatal("SpeechCreated SpeechHandle = nil, want retry reply handle")
		}
		want := "The repeated card number does not match, ask the user to try again."
		if ev.SpeechHandle.Generation.Instructions == nil {
			t.Fatal("retry instructions = nil, want mismatch prompt")
		}
		if got := ev.SpeechHandle.Generation.Instructions.AsModality("text").String(); got != want {
			t.Fatalf("retry instructions = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for card-number retry prompt")
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for mismatched confirmation", result)
	default:
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
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	session.Assistant = &fakeDtmfSessionAssistant{}
	speechEvents := session.SpeechCreatedEvents()
	tool := &updateSecurityCodeTool{task: task}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer session.Stop(context.Background())

	select {
	case <-speechEvents:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for security-code on-enter prompt")
	}

	if _, err := tool.Execute(context.Background(), `{"security_code":"12a"}`); err != nil {
		t.Fatalf("Execute() error = %v, want nil after prompting for invalid code", err)
	}

	select {
	case ev := <-speechEvents:
		if ev.Source != "generate_reply" {
			t.Fatalf("SpeechCreated Source = %q, want generate_reply", ev.Source)
		}
		if ev.SpeechHandle == nil {
			t.Fatal("SpeechCreated SpeechHandle = nil, want invalid-code reply handle")
		}
		want := "The security code's length is invalid, ask the user to repeat or to provide a new card and start over."
		if ev.SpeechHandle.Generation.Instructions == nil {
			t.Fatal("invalid-code instructions = nil, want invalid-code prompt")
		}
		if got := ev.SpeechHandle.Generation.Instructions.AsModality("text").String(); got != want {
			t.Fatalf("invalid-code instructions = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for invalid-code prompt")
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for invalid code", result)
	default:
	}
}

func TestGetSecurityCodeTaskRequiresMatchingConfirmation(t *testing.T) {
	task := NewGetSecurityCodeTask()
	update := &updateSecurityCodeTool{task: task}

	out, err := update.Execute(context.Background(), `{"security_code":"1234"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if out == "" {
		t.Fatal("update Execute() output is empty, want confirmation prompt guidance")
	}
	if !strings.Contains(out, "Call `confirm_security_code` once the user confirms, do not call it preemptively.") {
		t.Fatalf("update Execute() output = %q, want reference confirm-tool guidance", out)
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

func TestGetSecurityCodeTaskMismatchedConfirmationPromptsForRetry(t *testing.T) {
	task := NewGetSecurityCodeTask()
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
		t.Fatal("timed out waiting for security-code on-enter prompt")
	}

	update := &updateSecurityCodeTool{task: task}
	if _, err := update.Execute(context.Background(), `{"security_code":"1234"}`); err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	confirm := &confirmSecurityCodeTool{task: task, securityCode: "1234"}

	if _, err := confirm.Execute(context.Background(), `{"repeated_security_code":"4321"}`); err != nil {
		t.Fatalf("confirm Execute() error = %v, want nil after prompting for retry", err)
	}

	select {
	case ev := <-speechEvents:
		if ev.Source != "generate_reply" {
			t.Fatalf("SpeechCreated Source = %q, want generate_reply", ev.Source)
		}
		if ev.SpeechHandle == nil {
			t.Fatal("SpeechCreated SpeechHandle = nil, want retry reply handle")
		}
		want := "The repeated security code does not match, ask the user to try again."
		if ev.SpeechHandle.Generation.Instructions == nil {
			t.Fatal("retry instructions = nil, want mismatch prompt")
		}
		if got := ev.SpeechHandle.Generation.Instructions.AsModality("text").String(); got != want {
			t.Fatalf("retry instructions = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for security-code retry prompt")
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for mismatched confirmation", result)
	default:
	}
}

func TestGetExpirationDateTaskRecordsFutureDateWithoutConfirmation(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	tool := &updateExpirationDateTool{task: task}
	futureYear := (time.Now().Year() + 1) % 100

	out, err := tool.Execute(context.Background(), `{"expiration_month":4,"expiration_year":`+itoa(futureYear)+`}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "Expiration date captured and task completed." {
		t.Fatalf("Execute() output = %q, want completion message", out)
	}

	select {
	case result := <-task.Result:
		want := "04/" + twoDigit(futureYear)
		if result.Date != want {
			t.Fatalf("Date = %q, want %s", result.Date, want)
		}
	default:
		t.Fatal("task did not complete after valid expiration date")
	}
}

func TestGetExpirationDateTaskRejectsInvalidOrExpiredDate(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	tool := &updateExpirationDateTool{task: task}

	cases := []string{
		`{"expiration_month":13,"expiration_year":35}`,
		`{"expiration_month":1,"expiration_year":100}`,
		`{"expiration_month":1,"expiration_year":0}`,
	}
	for _, args := range cases {
		if _, err := tool.Execute(context.Background(), args); err == nil {
			t.Fatalf("Execute(%s) error = nil, want invalid expiration date error", args)
		}
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for invalid date", result)
	default:
	}
}

func TestGetExpirationDateTaskRequiresMatchingConfirmation(t *testing.T) {
	task := NewGetExpirationDateTask()
	update := &updateExpirationDateTool{task: task}
	futureYear := (time.Now().Year() + 1) % 100

	out, err := update.Execute(context.Background(), `{"expiration_month":12,"expiration_year":`+itoa(futureYear)+`}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if out == "" {
		t.Fatal("update Execute() output is empty, want confirmation prompt guidance")
	}
	if !strings.Contains(out, "Call `confirm_expiration_date` once the user confirms, do not call it preemptively.") {
		t.Fatalf("update Execute() output = %q, want reference confirm-tool guidance", out)
	}
	if len(task.Agent.Tools) != 4 || task.Agent.Tools[3].Name() != "confirm_expiration_date" {
		t.Fatalf("tools = %#v, want confirm_expiration_date appended", task.Agent.Tools)
	}

	confirm := &confirmExpirationDateTool{task: task, expirationMonth: 12, expirationYear: futureYear, expirationDate: "12/" + twoDigit(futureYear)}
	if _, err := confirm.Execute(context.Background(), `{"repeated_expiration_month":12,"repeated_expiration_year":`+itoa(futureYear)+`}`); err != nil {
		t.Fatalf("confirm Execute() error = %v", err)
	}

	select {
	case result := <-task.Result:
		want := "12/" + twoDigit(futureYear)
		if result.Date != want {
			t.Fatalf("Date = %q, want %s", result.Date, want)
		}
	default:
		t.Fatal("task did not complete after matching confirmation")
	}
}

func TestGetExpirationDateTaskMismatchedConfirmationPromptsForRetry(t *testing.T) {
	task := NewGetExpirationDateTask()
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	session.Assistant = &fakeDtmfSessionAssistant{}
	speechEvents := session.SpeechCreatedEvents()
	futureYear := (time.Now().Year() + 1) % 100

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer session.Stop(context.Background())

	select {
	case <-speechEvents:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for expiration-date on-enter prompt")
	}

	update := &updateExpirationDateTool{task: task}
	if _, err := update.Execute(context.Background(), `{"expiration_month":12,"expiration_year":`+itoa(futureYear)+`}`); err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	confirm := &confirmExpirationDateTool{task: task, expirationMonth: 12, expirationYear: futureYear, expirationDate: "12/" + twoDigit(futureYear)}

	if _, err := confirm.Execute(context.Background(), `{"repeated_expiration_month":11,"repeated_expiration_year":`+itoa(futureYear)+`}`); err != nil {
		t.Fatalf("confirm Execute() error = %v, want nil after prompting for retry", err)
	}

	select {
	case ev := <-speechEvents:
		if ev.Source != "generate_reply" {
			t.Fatalf("SpeechCreated Source = %q, want generate_reply", ev.Source)
		}
		if ev.SpeechHandle == nil {
			t.Fatal("SpeechCreated SpeechHandle = nil, want retry reply handle")
		}
		want := "The repeated expiration date does not match, ask the user to try again."
		if ev.SpeechHandle.Generation.Instructions == nil {
			t.Fatal("retry instructions = nil, want mismatch prompt")
		}
		if got := ev.SpeechHandle.Generation.Instructions.AsModality("text").String(); got != want {
			t.Fatalf("retry instructions = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for expiration-date retry prompt")
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for mismatched confirmation", result)
	default:
	}
}

func TestCreditCardTasksDefaultToConfirmation(t *testing.T) {
	if task := NewGetCardNumberTask(); !task.RequireConfirmation {
		t.Fatal("NewGetCardNumberTask() RequireConfirmation = false, want true")
	}
	if task := NewGetSecurityCodeTask(); !task.RequireConfirmation {
		t.Fatal("NewGetSecurityCodeTask() RequireConfirmation = false, want true")
	}
	if task := NewGetExpirationDateTask(); !task.RequireConfirmation {
		t.Fatal("NewGetExpirationDateTask() RequireConfirmation = false, want true")
	}
	if task := NewGetCreditCardTask(); !task.RequireConfirmation {
		t.Fatal("NewGetCreditCardTask() RequireConfirmation = false, want true")
	}
}

func TestCreditCardSubtaskInstructionsIncludeReferenceConfirmationWhenEnabled(t *testing.T) {
	cases := []struct {
		name         string
		instructions string
		wantParts    []string
	}{
		{
			name:         "card_number",
			instructions: NewGetCardNumberTask().Instructions,
			wantParts: []string{
				"If the user refuses to provide a number, call decline_card_capture().",
				"If the user wishes to start over the card collection process, call restart_card_collection().",
				"Never repeat any sensitive information, such as the user's card number, back to the user.",
				"Call `confirm_card_number` once the user has repeated their card number.",
			},
		},
		{
			name:         "security_code",
			instructions: NewGetSecurityCodeTask().Instructions,
			wantParts: []string{
				"You are solely responsible for collecting the user's card's security code.",
				"If the user refuses to provide a code, call decline_card_capture().",
				"If the user wishes to start over the card collection process, call restart_card_collection().",
				"Never repeat any sensitive information, such as the user's security code, back to the user.",
				"Call `confirm_security_code` once the user has repeated their security code.",
			},
		},
		{
			name:         "expiration_date",
			instructions: NewGetExpirationDateTask().Instructions,
			wantParts: []string{
				"You are solely responsible for collecting the user's card's expiration date.",
				"If the user refuses to provide a date, call decline_card_capture().",
				"If the user wishes to start over the card collection process, call restart_card_collection().",
				"Filter out filler words or hesitations.",
				"Never repeat any sensitive information, such as the user's expiration date, back to the user.",
				"Call `confirm_expiration_date` once the user has repeated their expiration date.",
			},
		},
	}
	for _, tc := range cases {
		for _, want := range tc.wantParts {
			if !strings.Contains(tc.instructions, want) {
				t.Fatalf("%s instructions = %q, want reference instruction %q", tc.name, tc.instructions, want)
			}
		}
	}
}

func TestCreditCardSubtaskInstructionsOmitConfirmationWhenDisabled(t *testing.T) {
	cases := []struct {
		name         string
		instructions string
		unwanted     string
	}{
		{
			name:         "card_number",
			instructions: NewGetCardNumberTask(false).Instructions,
			unwanted:     "confirm_card_number",
		},
		{
			name:         "security_code",
			instructions: NewGetSecurityCodeTask(false).Instructions,
			unwanted:     "confirm_security_code",
		},
		{
			name:         "expiration_date",
			instructions: NewGetExpirationDateTask(false).Instructions,
			unwanted:     "confirm_expiration_date",
		},
	}
	for _, tc := range cases {
		if strings.Contains(tc.instructions, tc.unwanted) {
			t.Fatalf("%s instructions = %q, want no %s when confirmation disabled", tc.name, tc.instructions, tc.unwanted)
		}
	}
}

func TestCreditCardSubtaskOnEnterPromptsUseReferenceText(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "card_number",
			got:  cardNumberOnEnterPrompt(),
			want: "Ask for the user's credit card number.",
		},
		{
			name: "security_code",
			got:  securityCodeOnEnterPrompt(),
			want: "Collect the user's card's security code.",
		},
		{
			name: "expiration_date",
			got:  expirationDateOnEnterPrompt(),
			want: "Collect the user's card's expiration date.",
		},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Fatalf("%s OnEnter prompt = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestGetCreditCardTaskCombinesSubtaskResults(t *testing.T) {
	task := NewGetCreditCardTask(true)

	err := task.completeCreditCardFromTaskResults(map[string]any{
		"cardholder_name_task": &GetNameResult{FirstName: "Ada", LastName: "Lovelace"},
		"card_number_task":     &GetCardNumberResult{Issuer: "Visa", CardNumber: "4111111111111111"},
		"security_code_task":   &GetSecurityCodeResult{SecurityCode: "123"},
		"expiration_date_task": &GetExpirationDateResult{Date: "04/35"},
	})
	if err != nil {
		t.Fatalf("completeCreditCardFromTaskResults() error = %v", err)
	}

	select {
	case result := <-task.Result:
		if result.CardholderName != "Ada Lovelace" {
			t.Fatalf("CardholderName = %q, want Ada Lovelace", result.CardholderName)
		}
		if result.Issuer != "Visa" || result.CardNumber != "4111111111111111" {
			t.Fatalf("card result = %#v, want Visa 4111111111111111", result)
		}
		if result.SecurityCode != "123" || result.ExpirationDate != "04/35" {
			t.Fatalf("security/expiration result = %#v, want 123 04/35", result)
		}
	default:
		t.Fatal("task did not complete after combining subtask results")
	}
}

func TestGetCreditCardTaskBuildsReferenceSubtasks(t *testing.T) {
	task := NewGetCreditCardTask()
	group := task.buildTaskGroup()

	if len(group.RegisteredTasks) != 4 {
		t.Fatalf("RegisteredTasks = %d, want 4", len(group.RegisteredTasks))
	}
	wantIDs := []string{"cardholder_name_task", "card_number_task", "security_code_task", "expiration_date_task"}
	for i, want := range wantIDs {
		if got := group.RegisteredTasks[i].ID; got != want {
			t.Fatalf("RegisteredTasks[%d].ID = %q, want %q", i, got, want)
		}
	}
	for _, info := range group.RegisteredTasks {
		child := info.TaskFactory()
		switch info.ID {
		case "cardholder_name_task":
			nameTask, ok := child.(*GetNameTask)
			if !ok {
				t.Fatalf("cardholder task = %T, want *GetNameTask", child)
			}
			if !nameTask.CollectFirstName || !nameTask.CollectLastName || !nameTask.RequireConfirmation {
				t.Fatalf("name task options = %#v, want first+last with confirmation", nameTask)
			}
		case "card_number_task":
			if cardTask, ok := child.(*GetCardNumberTask); !ok || !cardTask.RequireConfirmation {
				t.Fatalf("card number task = %#v, want confirming *GetCardNumberTask", child)
			}
		case "security_code_task":
			if codeTask, ok := child.(*GetSecurityCodeTask); !ok || !codeTask.RequireConfirmation {
				t.Fatalf("security code task = %#v, want confirming *GetSecurityCodeTask", child)
			}
		case "expiration_date_task":
			if dateTask, ok := child.(*GetExpirationDateTask); !ok || !dateTask.RequireConfirmation {
				t.Fatalf("expiration date task = %#v, want confirming *GetExpirationDateTask", child)
			}
		}
	}
}

func TestGetCreditCardTaskRestartsCollectionOnRestartError(t *testing.T) {
	task := NewGetCreditCardTask(false)
	attempts := 0

	task.runCreditCardCollection(context.Background(), func() *TaskGroup {
		attempts++
		group := NewTaskGroup(false, false)
		if attempts == 1 {
			if err := group.Fail(&CardCollectionRestartError{Reason: "wrong card"}); err != nil {
				t.Fatalf("group.Fail() error = %v", err)
			}
			return group
		}
		if err := group.Complete(&TaskGroupResult{TaskResults: map[string]any{
			"cardholder_name_task": &GetNameResult{FirstName: "Ada", LastName: "Lovelace"},
			"card_number_task":     &GetCardNumberResult{Issuer: "Visa", CardNumber: "4111111111111111"},
			"security_code_task":   &GetSecurityCodeResult{SecurityCode: "123"},
			"expiration_date_task": &GetExpirationDateResult{Date: "04/35"},
		}}); err != nil {
			t.Fatalf("group.Complete() error = %v", err)
		}
		return group
	}, func(group *TaskGroup) {})

	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	select {
	case result := <-task.Result:
		if result.CardholderName != "Ada Lovelace" || result.CardNumber != "4111111111111111" {
			t.Fatalf("result = %#v, want second group result", result)
		}
	case err := <-task.Err:
		t.Fatalf("task failed with %T %v, want completed result", err, err)
	default:
		t.Fatal("task did not complete after restart and second group result")
	}
}

func TestDeclineCardCaptureToolFailsWithTypedReason(t *testing.T) {
	task := NewGetCardNumberTask(false)
	tool := &declineCardCaptureTool{task: task}

	if _, err := tool.Execute(context.Background(), `{"reason":"user refused"}`); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	_, err := task.WaitAny(context.Background())
	var declined *CardCaptureDeclinedError
	if !errors.As(err, &declined) {
		t.Fatalf("WaitAny() error = %T %v, want CardCaptureDeclinedError", err, err)
	}
	if declined.Reason != "user refused" {
		t.Fatalf("Reason = %q, want user refused", declined.Reason)
	}
}

func TestRestartCardCollectionToolFailsWithTypedReason(t *testing.T) {
	task := NewGetCardNumberTask(false)
	tool := &restartCardCollectionTool{task: task}

	if _, err := tool.Execute(context.Background(), `{"reason":"wrong card"}`); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	_, err := task.WaitAny(context.Background())
	var restart *CardCollectionRestartError
	if !errors.As(err, &restart) {
		t.Fatalf("WaitAny() error = %T %v, want CardCollectionRestartError", err, err)
	}
	if restart.Reason != "wrong card" {
		t.Fatalf("Reason = %q, want wrong card", restart.Reason)
	}
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

func twoDigit(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}
