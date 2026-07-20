package workflows

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestGetCardNumberTaskRecordsValidCardWithoutConfirmation(t *testing.T) {
	task := NewGetCardNumberTask(false)
	tool := &recordCardNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"card_number":"4111 1111-1111 1111"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// Reference returns None after no-confirm completion, avoiding extra sensitive-flow tool chatter.
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
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

func TestCreditCardSubtaskInstructionsPreserveReferenceModalityVariants(t *testing.T) {
	tests := []struct {
		name         string
		variants     *llm.Instructions
		audioWant    string
		textWant     string
		staleText    string
		defaultStale string
	}{
		{
			name:         "card number",
			variants:     NewGetCardNumberTask().InstructionVariants,
			audioWant:    "Handle input as noisy voice transcription. Expect users to read the card number digit by digit.",
			textWant:     "Handle input as typed text. Users may type the number with or without spaces or dashes",
			staleText:    "Normalize spoken digits silently",
			defaultStale: "Call `confirm_card_number` once the user has repeated their card number.",
		},
		{
			name:         "security code",
			variants:     NewGetSecurityCodeTask().InstructionVariants,
			audioWant:    "Handle input as noisy voice transcription. Expect users to read the security code digit by digit.",
			textWant:     "Handle input as typed text. Users will type the security code directly.",
			staleText:    "Normalize spoken digits silently",
			defaultStale: "Call `confirm_security_code` once the user has repeated their security code.",
		},
		{
			name:         "expiration date",
			variants:     NewGetExpirationDateTask().InstructionVariants,
			audioWant:    "Handle input as noisy voice transcription. Expect users to say the expiration date in formats like",
			textWant:     "Handle input as typed text. Expect users to type the expiration date in formats like '04/25', '04/2025', or 'April 2025'.",
			staleText:    "Normalize spoken months and digits silently.",
			defaultStale: "Call `confirm_expiration_date` once the user has repeated their expiration date.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.variants == nil {
				t.Fatal("InstructionVariants = nil, want reference audio/text instruction variants")
			}
			audio := tt.variants.AsModality("audio").String()
			text := tt.variants.AsModality("text").String()
			if !strings.Contains(audio, tt.audioWant) {
				t.Fatalf("audio instructions = %q, want %q", audio, tt.audioWant)
			}
			if !strings.Contains(audio, tt.defaultStale) {
				t.Fatalf("audio instructions = %q, want default confirmation guidance %q", audio, tt.defaultStale)
			}
			if !strings.Contains(text, tt.textWant) {
				t.Fatalf("text instructions = %q, want %q", text, tt.textWant)
			}
			for _, stale := range []string{tt.staleText, tt.defaultStale} {
				if strings.Contains(text, stale) {
					t.Fatalf("text instructions = %q, want no stale audio/default confirmation guidance %q", text, stale)
				}
			}
		})
	}
}

func TestGetCardNumberTaskNormalizesSpokenDigits(t *testing.T) {
	task := NewGetCardNumberTask(false)
	tool := &recordCardNumberTool{task: task}

	_, err := tool.Execute(context.Background(), `{"card_number":"four one one one one one one one one one one one one one one one"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken digits accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.CardNumber != "4111111111111111" {
			t.Fatalf("CardNumber = %q, want spoken digits normalized", result.CardNumber)
		}
	default:
		t.Fatal("task did not complete after spoken card number")
	}
}

func TestGetCardNumberTaskFiltersSpokenNumberLengthLabel(t *testing.T) {
	task := NewGetCardNumberTask(false)
	tool := &recordCardNumberTool{task: task}

	_, err := tool.Execute(context.Background(), `{"card_number":"sixteen digit card number four one one one one one one one one one one one one one one one"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken card-number length label accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Issuer != "Visa" || result.CardNumber != "4111111111111111" {
			t.Fatalf("result = %#v, want spoken card-number length label filtered", result)
		}
	default:
		t.Fatal("task did not complete after spoken card-number length label")
	}
}

func TestGetCardNumberTaskFiltersFillerInSpokenNumberLengthLabel(t *testing.T) {
	task := NewGetCardNumberTask(false)
	tool := &recordCardNumberTool{task: task}

	_, err := tool.Execute(context.Background(), `{"card_number":"sixteen uh digit card number four one one one one one one one one one one one one one one one"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want filler in spoken card-number length label accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Issuer != "Visa" || result.CardNumber != "4111111111111111" {
			t.Fatalf("result = %#v, want filler in spoken card-number length label filtered", result)
		}
	default:
		t.Fatal("task did not complete after filler in spoken card-number length label")
	}
}

func TestGetCardNumberTaskFiltersLikeFillerInSpokenNumberLengthLabel(t *testing.T) {
	task := NewGetCardNumberTask(false)
	tool := &recordCardNumberTool{task: task}

	_, err := tool.Execute(context.Background(), `{"card_number":"sixteen like digit card number four one one one one one one one one one one one one one one one"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want like filler in spoken card-number length label accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Issuer != "Visa" || result.CardNumber != "4111111111111111" {
			t.Fatalf("result = %#v, want like filler in spoken card-number length label filtered", result)
		}
	default:
		t.Fatal("task did not complete after like filler in spoken card-number length label")
	}
}

func TestGetCardNumberTaskFiltersPreambleBeforeSpokenNumberLengthLabel(t *testing.T) {
	task := NewGetCardNumberTask(false)
	tool := &recordCardNumberTool{task: task}

	_, err := tool.Execute(context.Background(), `{"card_number":"my card number is sixteen digit card number four one one one one one one one one one one one one one one one"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want preamble plus spoken card-number length label accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Issuer != "Visa" || result.CardNumber != "4111111111111111" {
			t.Fatalf("result = %#v, want preamble and spoken card-number length label filtered", result)
		}
	default:
		t.Fatal("task did not complete after preamble plus spoken card-number length label")
	}
}

func TestGetCardNumberTaskNormalizesNoisySTTDigitHomophones(t *testing.T) {
	task := NewGetCardNumberTask(false)
	tool := &recordCardNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"card_number":"for one one one one one one one one one one one one one one one"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want noisy STT digit homophones accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Issuer != "Visa" || result.CardNumber != "4111111111111111" {
			t.Fatalf("result = %#v, want noisy STT digit homophones normalized to Visa 4111111111111111", result)
		}
	default:
		t.Fatal("task did not complete after noisy STT card number")
	}
}

func TestGetCardNumberTaskFiltersTrailingForMeSignoff(t *testing.T) {
	task := NewGetCardNumberTask(false)
	tool := &recordCardNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"card_number":"four one one one one one one one one one one one one one one one that is all for me"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-me sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Issuer != "Visa" || result.CardNumber != "4111111111111111" {
			t.Fatalf("result = %#v, want trailing for-me sign-off filtered", result)
		}
	default:
		t.Fatal("task did not complete after trailing for-me sign-off")
	}
}

func TestGetCardNumberTaskFiltersTrailingContractionForMeSignoff(t *testing.T) {
	task := NewGetCardNumberTask(false)
	tool := &recordCardNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"card_number":"four one one one one one one one one one one one one one one one that's all for me"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing contraction sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Issuer != "Visa" || result.CardNumber != "4111111111111111" {
			t.Fatalf("result = %#v, want trailing contraction sign-off filtered", result)
		}
	default:
		t.Fatal("task did not complete after trailing contraction sign-off")
	}
}

func TestGetCardNumberTaskFiltersTrailingForNowSignoff(t *testing.T) {
	task := NewGetCardNumberTask(false)
	tool := &recordCardNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"card_number":"four one one one one one one one one one one one one one one one that's it for now"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-now sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Issuer != "Visa" || result.CardNumber != "4111111111111111" {
			t.Fatalf("result = %#v, want trailing for-now sign-off filtered", result)
		}
	default:
		t.Fatal("task did not complete after trailing for-now sign-off")
	}
}

func TestGetCardNumberTaskFiltersTrailingForNowThanksSignoff(t *testing.T) {
	task := NewGetCardNumberTask(false)
	tool := &recordCardNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"card_number":"four one one one one one one one one one one one one one one one that's it for now thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-now-thanks sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Issuer != "Visa" || result.CardNumber != "4111111111111111" {
			t.Fatalf("result = %#v, want trailing for-now-thanks sign-off filtered", result)
		}
	default:
		t.Fatal("task did not complete after trailing for-now-thanks sign-off")
	}
}

func TestGetCardNumberTaskFiltersTrailingShortForTodaySignoff(t *testing.T) {
	task := NewGetCardNumberTask(false)
	tool := &recordCardNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"card_number":"four one one one one one one one one one one one one one one one for today thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing short for-today sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Issuer != "Visa" || result.CardNumber != "4111111111111111" {
			t.Fatalf("result = %#v, want trailing short for-today sign-off filtered", result)
		}
	default:
		t.Fatal("task did not complete after trailing short for-today sign-off")
	}
}

func TestGetCardNumberTaskFiltersTrailingShortForYouSignoff(t *testing.T) {
	task := NewGetCardNumberTask(false)
	tool := &recordCardNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"card_number":"four one one one one one one one one one one one one one one one for you thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing short for-you sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Issuer != "Visa" || result.CardNumber != "4111111111111111" {
			t.Fatalf("result = %#v, want trailing short for-you sign-off filtered", result)
		}
	default:
		t.Fatal("task did not complete after trailing short for-you sign-off")
	}
}

func TestGetCardNumberTaskFiltersTrailingShortForTheDaySignoff(t *testing.T) {
	task := NewGetCardNumberTask(false)
	tool := &recordCardNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"card_number":"four one one one one one one one one one one one one one one one for the day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing short for-the-day sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Issuer != "Visa" || result.CardNumber != "4111111111111111" {
			t.Fatalf("result = %#v, want trailing short for-the-day sign-off filtered", result)
		}
	default:
		t.Fatal("task did not complete after trailing short for-the-day sign-off")
	}
}

func TestGetCardNumberTaskFiltersTrailingThatllBeAllForTheDaySignoff(t *testing.T) {
	task := NewGetCardNumberTask(false)
	tool := &recordCardNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"card_number":"four one one one one one one one one one one one one one one one that'll be all for the day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing that'll-be for-the-day sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Issuer != "Visa" || result.CardNumber != "4111111111111111" {
			t.Fatalf("result = %#v, want trailing that'll-be for-the-day sign-off filtered", result)
		}
	default:
		t.Fatal("task did not complete after trailing that'll-be for-the-day sign-off")
	}
}

func TestGetCardNumberTaskNormalizesTwentyOhGroupedDigits(t *testing.T) {
	task := NewGetCardNumberTask(false)
	tool := &recordCardNumberTool{task: task}

	_, err := tool.Execute(context.Background(), `{"card_number":"four zero zero zero zero zero six zero zero zero zero zero twenty oh five"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want twenty-oh grouped card digits accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Issuer != "Visa" || result.CardNumber != "4000006000002005" {
			t.Fatalf("result = %#v, want twenty-oh group normalized to Visa 4000006000002005", result)
		}
	default:
		t.Fatal("task did not complete after twenty-oh grouped card number")
	}
}

func TestGetCardNumberTaskNormalizesRepeatedGroupedDigits(t *testing.T) {
	task := NewGetCardNumberTask(false)
	tool := &recordCardNumberTool{task: task}

	_, err := tool.Execute(context.Background(), `{"card_number":"four zero zero zero zero zero zero four double twenty oh five"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want repeated grouped card digits accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Issuer != "Visa" || result.CardNumber != "4000000420052005" {
			t.Fatalf("result = %#v, want repeated grouped card digits normalized to Visa 4000000420052005", result)
		}
	default:
		t.Fatal("task did not complete after repeated grouped card number")
	}
}

func TestGetCardNumberTaskNormalizesRepeatedHundredGroup(t *testing.T) {
	task := NewGetCardNumberTask(false)
	tool := &recordCardNumberTool{task: task}

	_, err := tool.Execute(context.Background(), `{"card_number":"four zero zero zero zero zero double one hundred tree one two one four"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want repeated hundred-group card digits accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Issuer != "Visa" || result.CardNumber != "4000001031031214" {
			t.Fatalf("result = %#v, want repeated hundred group normalized to Visa 4000001031031214", result)
		}
	default:
		t.Fatal("task did not complete after repeated hundred-group card number")
	}
}

func TestGetCardNumberTaskNormalizesRepeatedHundredNaughtGroup(t *testing.T) {
	task := NewGetCardNumberTask(false)
	tool := &recordCardNumberTool{task: task}

	if got := normalizeCardDigits("double one hundred naught five"); got != "105105" {
		t.Fatalf("normalizeCardDigits() = %q, want 105105", got)
	}

	_, err := tool.Execute(context.Background(), `{"card_number":"four zero zero zero zero zero double one hundred naught five one two one seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want repeated hundred-naught card digits accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Issuer != "Visa" || result.CardNumber != "4000001051051217" {
			t.Fatalf("result = %#v, want repeated hundred-naught group normalized to Visa 4000001051051217", result)
		}
	default:
		t.Fatal("task did not complete after repeated hundred-naught card number")
	}
}

func TestGetCardNumberTaskNormalizesRepeatedHundredTensGroup(t *testing.T) {
	task := NewGetCardNumberTask(false)
	tool := &recordCardNumberTool{task: task}

	_, err := tool.Execute(context.Background(), `{"card_number":"four zero zero zero zero zero double one hundred twenty three one two one eight"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want repeated hundred-tens card digits accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Issuer != "Visa" || result.CardNumber != "4000001231231218" {
			t.Fatalf("result = %#v, want repeated hundred-tens group normalized to Visa 4000001231231218", result)
		}
	default:
		t.Fatal("task did not complete after repeated hundred-tens card number")
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

func TestGetCardNumberTaskRejectsInvalidLengthWithPrompt(t *testing.T) {
	task := NewGetCardNumberTask()
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

	if _, err := tool.Execute(context.Background(), `{"card_number":"4111"}`); err != nil {
		t.Fatalf("Execute() error = %v, want nil after prompting for invalid length", err)
	}

	select {
	case ev := <-speechEvents:
		if ev.Source != "generate_reply" {
			t.Fatalf("SpeechCreated Source = %q, want generate_reply", ev.Source)
		}
		if ev.SpeechHandle == nil {
			t.Fatal("SpeechCreated SpeechHandle = nil, want invalid-length reply handle")
		}
		want := "The length of the card number is invalid, ask the user to repeat their card number."
		if ev.SpeechHandle.Generation.Instructions == nil {
			t.Fatal("invalid-length instructions = nil, want invalid-length prompt")
		}
		if got := ev.SpeechHandle.Generation.Instructions.AsModality("text").String(); got != want {
			t.Fatalf("invalid-length instructions = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for invalid-length prompt")
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for invalid length", result)
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
	wantOut := "The card number has been updated.\n" +
		"Ask them to repeat the number, do not repeat the number back to them.\n"
	if out != wantOut {
		t.Fatalf("record Execute() output = %q, want %q", out, wantOut)
	}
	if len(task.Agent.Tools) != 4 || task.Agent.Tools[3].Name() != "confirm_card_number" {
		t.Fatalf("tools = %#v, want confirm_card_number appended", task.Agent.Tools)
	}

	confirm := &confirmCardNumberTool{task: task, cardNumber: "5555555555554444"}
	confirmOut, err := confirm.Execute(context.Background(), `{"repeated_card_number":"5555555555554444"}`)
	if err != nil {
		t.Fatalf("confirm Execute() error = %v", err)
	}
	if confirmOut != "" {
		t.Fatalf("confirm Execute() output = %q, want empty output after completion", confirmOut)
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

func TestGetCardNumberTaskConfirmFiltersSpokenNumberLengthLabel(t *testing.T) {
	task := NewGetCardNumberTask()
	record := &recordCardNumberTool{task: task}

	if _, err := record.Execute(context.Background(), `{"card_number":"4111 1111 1111 1111"}`); err != nil {
		t.Fatalf("record Execute() error = %v", err)
	}

	confirm := &confirmCardNumberTool{task: task, cardNumber: "4111111111111111"}
	confirmOut, err := confirm.Execute(context.Background(), `{"repeated_card_number":"sixteen digit card number four one one one one one one one one one one one one one one one"}`)
	if err != nil {
		t.Fatalf("confirm Execute() error = %v, want spoken card-number length label accepted", err)
	}
	if confirmOut != "" {
		t.Fatalf("confirm Execute() output = %q, want empty output after completion", confirmOut)
	}

	select {
	case result := <-task.Result:
		if result.Issuer != "Visa" || result.CardNumber != "4111111111111111" {
			t.Fatalf("result = %#v, want spoken card-number confirmation label filtered", result)
		}
	default:
		t.Fatal("task did not complete after spoken card-number confirmation label")
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

func TestGetCardNumberTaskStaleConfirmationPromptsForUpdatedNumber(t *testing.T) {
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
	if _, err := record.Execute(context.Background(), `{"card_number":"5555555555554444"}`); err != nil {
		t.Fatalf("first record Execute() error = %v", err)
	}
	staleConfirm := &confirmCardNumberTool{task: task, cardNumber: "5555555555554444"}

	if _, err := record.Execute(context.Background(), `{"card_number":"4111111111111111"}`); err != nil {
		t.Fatalf("second record Execute() error = %v", err)
	}
	if _, err := staleConfirm.Execute(context.Background(), `{"repeated_card_number":"5555555555554444"}`); err != nil {
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
		want := "The card number has changed since confirmation was requested, ask the user to confirm the updated number."
		if ev.SpeechHandle.Generation.Instructions == nil {
			t.Fatal("stale confirmation instructions = nil, want changed-card prompt")
		}
		if got := ev.SpeechHandle.Generation.Instructions.AsModality("text").String(); got != want {
			t.Fatalf("stale confirmation instructions = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stale card-number confirmation prompt")
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for stale confirmation", result)
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
	// Reference returns None after no-confirm completion, avoiding extra sensitive-flow tool chatter.
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
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

func TestGetSecurityCodeTaskNormalizesSpokenDigits(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	_, err := tool.Execute(context.Background(), `{"security_code":"zero four two"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken digits accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "042" {
			t.Fatalf("SecurityCode = %q, want spoken digits normalized", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after spoken security code")
	}
}

func TestGetSecurityCodeTaskNormalizesSixHomophone(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	out, err := tool.Execute(context.Background(), `{"security_code":"sex one two"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want six homophone accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "612" {
			t.Fatalf("SecurityCode = %q, want six homophone normalized to 612", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after six-homophone security code")
	}
}

func TestGetSecurityCodeTaskNormalizesNinerDigit(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	out, err := tool.Execute(context.Background(), `{"security_code":"one two niner"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want niner spoken security code accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "129" {
			t.Fatalf("SecurityCode = %q, want niner normalized to 9", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after niner security code")
	}
}

func TestGetSecurityCodeTaskNormalizesAughtDigit(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	out, err := tool.Execute(context.Background(), `{"security_code":"one aught two"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want aught spoken security code accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "102" {
			t.Fatalf("SecurityCode = %q, want aught normalized to 0", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after aught security code")
	}
}

func TestGetSecurityCodeTaskNormalizesNaughtDigit(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	out, err := tool.Execute(context.Background(), `{"security_code":"one naught two"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want naught spoken security code accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "102" {
			t.Fatalf("SecurityCode = %q, want naught normalized to 0", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after naught security code")
	}
}

func TestGetSecurityCodeTaskNormalizesNoughtDigit(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	out, err := tool.Execute(context.Background(), `{"security_code":"one nought two"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want nought spoken security code accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "102" {
			t.Fatalf("SecurityCode = %q, want nought normalized to 0", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after nought security code")
	}
}

func TestGetSecurityCodeTaskNormalizesOughtDigit(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	out, err := tool.Execute(context.Background(), `{"security_code":"one ought two"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want ought spoken security code accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "102" {
			t.Fatalf("SecurityCode = %q, want ought normalized to 0", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after ought security code")
	}
}

func TestGetSecurityCodeTaskNormalizesOweDigit(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	out, err := tool.Execute(context.Background(), `{"security_code":"one owe two"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want owe spoken security code accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "102" {
			t.Fatalf("SecurityCode = %q, want owe normalized to 0", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after owe security code")
	}
}

func TestGetSecurityCodeTaskFiltersSpokenCodeLengthLabel(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	_, err := tool.Execute(context.Background(), `{"security_code":"three digit code one two three"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken code-length label accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "123" {
			t.Fatalf("SecurityCode = %q, want spoken code-length label filtered", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after spoken code-length label")
	}
}

func TestGetSecurityCodeTaskFiltersFillerInSpokenCodeLengthLabel(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	_, err := tool.Execute(context.Background(), `{"security_code":"three uh digit code one two three"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want filler in spoken code-length label accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "123" {
			t.Fatalf("SecurityCode = %q, want filler in spoken code-length label filtered", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after filler in spoken code-length label")
	}
}

func TestGetSecurityCodeTaskFiltersPreambleBeforeSpokenCodeLengthLabel(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	_, err := tool.Execute(context.Background(), `{"security_code":"my security code is three digit code one two three"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want preamble plus spoken code-length label accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "123" {
			t.Fatalf("SecurityCode = %q, want preamble and spoken code-length label filtered", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after preamble plus spoken code-length label")
	}
}

func TestGetSecurityCodeTaskNormalizesSpokenDoubleDigits(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	_, err := tool.Execute(context.Background(), `{"security_code":"double four two"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken doubled digit accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "442" {
			t.Fatalf("SecurityCode = %q, want spoken double digit normalized", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after spoken doubled security code")
	}
}

func TestGetSecurityCodeTaskNormalizesWonHomophone(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	_, err := tool.Execute(context.Background(), `{"security_code":"won two three"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want won homophone accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "123" {
			t.Fatalf("SecurityCode = %q, want won homophone normalized", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after won-homophone security code")
	}
}

func TestGetSecurityCodeTaskNormalizesThreeHomophones(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	out, err := tool.Execute(context.Background(), `{"security_code":"tree free four"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want three homophones accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want no sensitive echo", out)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "334" {
			t.Fatalf("SecurityCode = %q, want three homophones normalized", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after three-homophone security code")
	}
}

func TestGetSecurityCodeTaskNormalizesForeHomophone(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	out, err := tool.Execute(context.Background(), `{"security_code":"one two fore"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want fore homophone accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want no sensitive echo", out)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "124" {
			t.Fatalf("SecurityCode = %q, want fore homophone normalized", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after fore-homophone security code")
	}
}

func TestGetSecurityCodeTaskNormalizesSpokenQuadrupleDigits(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	_, err := tool.Execute(context.Background(), `{"security_code":"quadruple four"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken quadruple digit accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "4444" {
			t.Fatalf("SecurityCode = %q, want spoken quadruple digit normalized", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after spoken quadruple security code")
	}
}

func TestGetSecurityCodeTaskNormalizesSpokenDoubleDigitsAcrossFiller(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	_, err := tool.Execute(context.Background(), `{"security_code":"double uh four two"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want doubled digit across filler accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "442" {
			t.Fatalf("SecurityCode = %q, want filler after double ignored", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after doubled security code with filler")
	}
}

func TestGetSecurityCodeTaskNormalizesSpokenDoubleDigitsAcrossLikeFiller(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	_, err := tool.Execute(context.Background(), `{"security_code":"double like four two"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want doubled digit across like filler accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "442" {
			t.Fatalf("SecurityCode = %q, want like filler after double ignored", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after doubled security code with like filler")
	}
}

func TestGetSecurityCodeTaskNormalizesSpokenDoubleDigitsAcrossActuallyFiller(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	_, err := tool.Execute(context.Background(), `{"security_code":"double actually four two"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want doubled digit across correction filler accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "442" {
			t.Fatalf("SecurityCode = %q, want correction filler after double ignored", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after doubled security code with correction filler")
	}
}

func TestGetSecurityCodeTaskNormalizesSpokenDoubleDigitsAcrossSorryFiller(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	_, err := tool.Execute(context.Background(), `{"security_code":"double sorry four two"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want doubled digit across apology filler accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "442" {
			t.Fatalf("SecurityCode = %q, want apology filler after double ignored", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after doubled security code with apology filler")
	}
}

func TestGetSecurityCodeTaskNormalizesSpokenGroupedDigits(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	_, err := tool.Execute(context.Background(), `{"security_code":"one twenty three"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want grouped spoken security code accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "123" {
			t.Fatalf("SecurityCode = %q, want grouped spoken security code normalized", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after grouped spoken security code")
	}
}

func TestGetSecurityCodeTaskNormalizesSpokenHundredSingleDigitGroup(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	if got := normalizeCardDigits("one hundred tree"); got != "103" {
		t.Fatalf("normalizeCardDigits() = %q, want 103", got)
	}

	out, err := tool.Execute(context.Background(), `{"security_code":"one hundred tree"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken hundred single-digit security code accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "103" {
			t.Fatalf("SecurityCode = %q, want spoken hundred single-digit group normalized", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after spoken hundred single-digit security code")
	}
}

func TestGetSecurityCodeTaskNormalizesSpokenHundredNaughtGroup(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	if got := normalizeCardDigits("one hundred naught five"); got != "105" {
		t.Fatalf("normalizeCardDigits() = %q, want 105", got)
	}

	out, err := tool.Execute(context.Background(), `{"security_code":"one hundred naught five"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken hundred naught security code accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "105" {
			t.Fatalf("SecurityCode = %q, want spoken hundred naught group normalized", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after spoken hundred naught security code")
	}
}

func TestGetSecurityCodeTaskNormalizesSpokenHundredWithAnd(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	out, err := tool.Execute(context.Background(), `{"security_code":"one hundred and tree"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken hundred-and security code accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "103" {
			t.Fatalf("SecurityCode = %q, want hundred-and group normalized", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after spoken hundred-and security code")
	}
}

func TestGetSecurityCodeTaskNormalizesSpokenTeenDigits(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	_, err := tool.Execute(context.Background(), `{"security_code":"one fifteen"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want teen spoken security code accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "115" {
			t.Fatalf("SecurityCode = %q, want teen spoken security code normalized", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after teen spoken security code")
	}
}

func TestGetSecurityCodeTaskFiltersTrailingForTodayThanksSignoff(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	out, err := tool.Execute(context.Background(), `{"security_code":"one two three that's it for today thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-today-thanks sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "123" {
			t.Fatalf("SecurityCode = %q, want trailing for-today-thanks sign-off filtered", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after trailing for-today-thanks sign-off")
	}
}

func TestGetSecurityCodeTaskFiltersTrailingForYouSignoff(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	out, err := tool.Execute(context.Background(), `{"security_code":"one two three that's all for you"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-you sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "123" {
			t.Fatalf("SecurityCode = %q, want trailing for-you sign-off filtered", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after trailing for-you sign-off")
	}
}

func TestGetSecurityCodeTaskFiltersTrailingShortForYouSignoff(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	out, err := tool.Execute(context.Background(), `{"security_code":"one two three for you thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing short for-you sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "123" {
			t.Fatalf("SecurityCode = %q, want trailing short for-you sign-off filtered", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after trailing short for-you sign-off")
	}
}

func TestGetSecurityCodeTaskFiltersTrailingShortForTodaySignoff(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	out, err := tool.Execute(context.Background(), `{"security_code":"one two three for today thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing short for-today sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "123" {
			t.Fatalf("SecurityCode = %q, want trailing short for-today sign-off filtered", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after trailing short for-today sign-off")
	}
}

func TestGetSecurityCodeTaskFiltersTrailingForTheDayThanksSignoff(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	out, err := tool.Execute(context.Background(), `{"security_code":"one two three that's it for the day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-the-day-thanks sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "123" {
			t.Fatalf("SecurityCode = %q, want trailing for-the-day-thanks sign-off filtered", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after trailing for-the-day-thanks sign-off")
	}
}

func TestGetSecurityCodeTaskFiltersTrailingExpandedThatWillBeAllForTheDaySignoff(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	out, err := tool.Execute(context.Background(), `{"security_code":"one two three that will be all for the day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want expanded that-will-be for-the-day sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "123" {
			t.Fatalf("SecurityCode = %q, want expanded that-will-be for-the-day sign-off filtered", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after expanded that-will-be for-the-day sign-off")
	}
}

func TestGetSecurityCodeTaskFiltersTrailingThatllBeAllForDaySignoff(t *testing.T) {
	task := NewGetSecurityCodeTask(false)
	tool := &updateSecurityCodeTool{task: task}

	out, err := tool.Execute(context.Background(), `{"security_code":"one two three that'll be all for day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want contracted for-day sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "123" {
			t.Fatalf("SecurityCode = %q, want contracted for-day sign-off filtered", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after contracted for-day sign-off")
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
	wantOut := "The security code has been updated.\n" +
		"Do not repeat the security code back to the user, ask them to repeat the code.\n" +
		"Call `confirm_security_code` once the user confirms, do not call it preemptively.\n"
	if out != wantOut {
		t.Fatalf("update Execute() output = %q, want %q", out, wantOut)
	}
	if strings.Contains(out, "repeat themselves") {
		t.Fatalf("update Execute() output = %q, want explicit repeat-code wording", out)
	}
	if len(task.Agent.Tools) != 4 || task.Agent.Tools[3].Name() != "confirm_security_code" {
		t.Fatalf("tools = %#v, want confirm_security_code appended", task.Agent.Tools)
	}

	confirm := &confirmSecurityCodeTool{task: task, securityCode: "1234"}
	confirmOut, err := confirm.Execute(context.Background(), `{"repeated_security_code":"1234"}`)
	if err != nil {
		t.Fatalf("confirm Execute() error = %v", err)
	}
	if confirmOut != "" {
		t.Fatalf("confirm Execute() output = %q, want empty output after completion", confirmOut)
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

func TestGetSecurityCodeTaskConfirmFiltersSpokenCodeLengthLabel(t *testing.T) {
	task := NewGetSecurityCodeTask()
	update := &updateSecurityCodeTool{task: task}

	if _, err := update.Execute(context.Background(), `{"security_code":"123"}`); err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}

	confirm := &confirmSecurityCodeTool{task: task, securityCode: "123"}
	confirmOut, err := confirm.Execute(context.Background(), `{"repeated_security_code":"three digit code one two three"}`)
	if err != nil {
		t.Fatalf("confirm Execute() error = %v, want spoken code-length label accepted", err)
	}
	if confirmOut != "" {
		t.Fatalf("confirm Execute() output = %q, want empty output after completion", confirmOut)
	}

	select {
	case result := <-task.Result:
		if result.SecurityCode != "123" {
			t.Fatalf("SecurityCode = %q, want spoken code-length confirmation label filtered", result.SecurityCode)
		}
	default:
		t.Fatal("task did not complete after spoken code-length confirmation label")
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

func TestGetSecurityCodeTaskStaleConfirmationPromptsForUpdatedCode(t *testing.T) {
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
	if _, err := update.Execute(context.Background(), `{"security_code":"123"}`); err != nil {
		t.Fatalf("first update Execute() error = %v", err)
	}
	staleConfirm := &confirmSecurityCodeTool{task: task, securityCode: "123"}

	if _, err := update.Execute(context.Background(), `{"security_code":"987"}`); err != nil {
		t.Fatalf("second update Execute() error = %v", err)
	}
	if _, err := staleConfirm.Execute(context.Background(), `{"repeated_security_code":"123"}`); err != nil {
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
		want := "The security code has changed since confirmation was requested, ask the user to confirm the updated code."
		if ev.SpeechHandle.Generation.Instructions == nil {
			t.Fatal("stale confirmation instructions = nil, want changed-code prompt")
		}
		if got := ev.SpeechHandle.Generation.Instructions.AsModality("text").String(); got != want {
			t.Fatalf("stale confirmation instructions = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stale security-code confirmation prompt")
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for stale confirmation", result)
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
	// Reference returns None after no-confirm completion, avoiding extra sensitive-flow tool chatter.
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
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

func TestGetExpirationDateTaskNormalizesSpokenMonthYearArguments(t *testing.T) {
	task := NewGetExpirationDateTask()
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"April","expiration_year":"twenty seven"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v, want spoken expiration date accepted", err)
	}
	if out == "" {
		t.Fatal("update Execute() output is empty, want confirmation prompt guidance")
	}
	if strings.Contains(out, "04/27") {
		t.Fatalf("update Execute() output = %q, want no sensitive expiration date repeated", out)
	}

	confirm := &confirmExpirationDateTool{
		task:            task,
		expirationMonth: 4,
		expirationYear:  27,
		expirationDate:  "04/27",
	}
	confirmOut, err := confirm.Execute(context.Background(), `{"repeated_expiration_month":"april","repeated_expiration_year":"twenty seven"}`)
	if err != nil {
		t.Fatalf("confirm Execute() error = %v, want spoken repeated expiration date accepted", err)
	}
	if confirmOut != "" {
		t.Fatalf("confirm Execute() output = %q, want empty output after completion", confirmOut)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want spoken expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after spoken expiration date confirmation")
	}
}

func TestGetExpirationDateTaskFiltersFieldLabels(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"month is April","expiration_year":"year is twenty seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want field labels accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want field labels filtered", result.Date)
		}
	default:
		t.Fatal("task did not complete after field-labeled expiration date")
	}
}

func TestGetExpirationDateTaskFiltersWillBeFieldLabels(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"month will be April","expiration_year":"year will be twenty seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want future-tense field labels accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want future-tense field labels filtered", result.Date)
		}
	default:
		t.Fatal("task did not complete after future-tense field-labeled expiration date")
	}
}

func TestGetExpirationDateTaskFiltersContractedFieldLabels(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"month's April","expiration_year":"year's twenty seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want contracted field labels accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want contracted field labels filtered", result.Date)
		}
	default:
		t.Fatal("task did not complete after contracted field-labeled expiration date")
	}
}

func TestGetExpirationDateTaskFiltersSplitContractedFieldLabels(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"month s April","expiration_year":"year s twenty seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split contracted field labels accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want split contracted field labels filtered", result.Date)
		}
	default:
		t.Fatal("task did not complete after split contracted field-labeled expiration date")
	}
}

func TestGetExpirationDateTaskNormalizesNoisySTTDigitHomophones(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"for","expiration_year":"twenty ate"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want noisy STT digit homophones accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/28" {
			t.Fatalf("Date = %q, want noisy STT digit homophones normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after noisy STT expiration date")
	}
}

func TestGetExpirationDateTaskNormalizesSixHomophone(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"sex","expiration_year":"twenty seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want six homophone accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "06/27" {
			t.Fatalf("Date = %q, want six homophone normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after six-homophone expiration date")
	}
}

func TestGetExpirationDateTaskNormalizesForeHomophone(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"fore","expiration_year":"thirty fore"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want fore homophone accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/34" {
			t.Fatalf("Date = %q, want fore homophone normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after fore-homophone expiration date")
	}
}

func TestGetExpirationDateTaskNormalizesThreeHomophones(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"tree","expiration_year":"thirty free"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want three homophones accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "03/33" {
			t.Fatalf("Date = %q, want three homophones normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after three-homophone expiration date")
	}
}

func TestGetExpirationDateTaskFiltersSpokenFillerInArguments(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"uh April","expiration_year":"um twenty seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want filler-spoken expiration date accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want filler-spoken expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after filler-spoken expiration date")
	}
}

func TestGetExpirationDateTaskFiltersLikeSpokenFillerInArguments(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"like April","expiration_year":"like twenty seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want like-filler expiration date accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want like-filler expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after like-filler expiration date")
	}
}

func TestGetExpirationDateTaskFiltersActuallySpokenFillerInArguments(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"actually April","expiration_year":"actually twenty seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want correction-filler expiration date accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want correction-filler expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after correction-filler expiration date")
	}
}

func TestGetExpirationDateTaskFiltersSorrySpokenFillerInArguments(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"sorry April","expiration_year":"sorry twenty seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want apology-filler expiration date accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want apology-filler expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after apology-filler expiration date")
	}
}

func TestGetExpirationDateTaskFiltersTrailingSignoffFiller(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"April please","expiration_year":"twenty seven that's it"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing sign-off filler expiration date accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want trailing sign-off filler expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after trailing-signoff expiration date")
	}
}

func TestGetExpirationDateTaskFiltersExpandedTrailingSignoffFiller(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"April that is it","expiration_year":"twenty seven that is all"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want expanded trailing sign-off filler expiration date accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want expanded trailing sign-off filler expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after expanded-trailing-signoff expiration date")
	}
}

func TestGetExpirationDateTaskFiltersThatWillBeAllSignoffFiller(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"April that'll be it","expiration_year":"twenty seven that'll be all"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want that'll-be trailing sign-off filler expiration date accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want that'll-be trailing sign-off filler expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after that'll-be trailing sign-off expiration date")
	}
}

func TestGetExpirationDateTaskFiltersExpandedThatWillBeAllSignoffFiller(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"April that will be it","expiration_year":"twenty seven that will be all"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want expanded that-will-be trailing sign-off filler expiration date accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want expanded that-will-be trailing sign-off filler expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after expanded that-will-be trailing sign-off expiration date")
	}
}

func TestGetExpirationDateTaskFiltersSplitThatllShortSignoffFiller(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"April that ll be it","expiration_year":"twenty seven that ll be all"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split contracted trailing sign-off filler expiration date accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want split contracted trailing sign-off expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after split contracted trailing sign-off expiration date")
	}
}

func TestGetExpirationDateTaskFiltersDoneSignoffFiller(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"April done","expiration_year":"twenty seven done"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want done sign-off expiration date accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want done sign-off expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after done-signoff expiration date")
	}
}

func TestGetExpirationDateTaskFiltersAllDoneSignoffFiller(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"April all done","expiration_year":"twenty seven all done"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want all-done sign-off expiration date accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want all-done sign-off expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after all-done-signoff expiration date")
	}
}

func TestGetExpirationDateTaskFiltersTrailingForNowThanksSignoffFiller(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"April please","expiration_year":"twenty seven that's it for now thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-now-thanks sign-off expiration date accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want trailing for-now-thanks sign-off expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after trailing for-now-thanks sign-off expiration date")
	}
}

func TestGetExpirationDateTaskFiltersTrailingThatWillBeAllForNowSignoffFiller(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"April please","expiration_year":"twenty seven that'll be all for now thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing that'll-be-all-for-now sign-off expiration date accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want trailing that'll-be-all-for-now sign-off expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after trailing that'll-be-all-for-now sign-off expiration date")
	}
}

func TestGetExpirationDateTaskFiltersTrailingExpandedThatWillBeAllForNowSignoffFiller(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"April please","expiration_year":"twenty seven that will be all for now thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing expanded that-will-be-all-for-now sign-off expiration date accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want trailing expanded that-will-be-all-for-now sign-off expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after trailing expanded that-will-be-all-for-now sign-off expiration date")
	}
}

func TestGetExpirationDateTaskFiltersTrailingForTodayThanksSignoffFiller(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"April please","expiration_year":"twenty seven that's it for today thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-today-thanks sign-off expiration date accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want trailing for-today-thanks sign-off expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after trailing for-today-thanks sign-off expiration date")
	}
}

func TestGetExpirationDateTaskFiltersTrailingForYouSignoffFiller(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"April","expiration_year":"twenty seven that's all for you"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-you sign-off expiration date accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want trailing for-you sign-off expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after trailing for-you sign-off expiration date")
	}
}

func TestGetExpirationDateTaskFiltersTrailingShortForYouSignoffFiller(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"April please","expiration_year":"twenty seven for you thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing short for-you sign-off expiration date accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want trailing short for-you sign-off expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after trailing short for-you sign-off expiration date")
	}
}

func TestGetExpirationDateTaskFiltersTrailingShortForTodaySignoffFiller(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"April please","expiration_year":"twenty seven for today thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing short for-today sign-off expiration date accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want trailing short for-today sign-off expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after trailing short for-today sign-off expiration date")
	}
}

func TestGetExpirationDateTaskFiltersTrailingShortForTheDaySignoffFiller(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"April please","expiration_year":"twenty seven for the day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing short for-the-day sign-off expiration date accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want trailing short for-the-day sign-off expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after trailing short for-the-day sign-off expiration date")
	}
}

func TestGetExpirationDateTaskFiltersTrailingForTheDayThanksSignoffFiller(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"April please","expiration_year":"twenty seven that's it for the day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-the-day-thanks sign-off expiration date accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want trailing for-the-day-thanks sign-off expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after trailing for-the-day-thanks sign-off expiration date")
	}
}

func TestGetExpirationDateTaskFiltersTrailingExpandedThatWillBeAllForTheDaySignoff(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"April that will be all for the day thanks","expiration_year":"twenty seven that will be it for the day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want STT-expanded for-the-day sign-off expiration date accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want STT-expanded for-the-day sign-off expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after STT-expanded for-the-day sign-off expiration date")
	}
}

func TestGetExpirationDateTaskFiltersTrailingThatllBeAllForDaySignoff(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"April that'll be all for day thanks","expiration_year":"twenty seven that'll be it for day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want contracted omitted-article for-day sign-off expiration date accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want contracted omitted-article for-day sign-off expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after contracted omitted-article for-day sign-off expiration date")
	}
}

func TestGetExpirationDateTaskFiltersTrailingSplitThatllBeAllForTheDaySignoff(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"April that ll be all for the day thanks","expiration_year":"twenty seven that ll be it for the day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split-contraction for-the-day sign-off expiration date accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want split-contraction for-the-day sign-off expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after split-contraction for-the-day sign-off expiration date")
	}
}

func TestGetExpirationDateTaskFiltersTrailingSplitThatllBeAllForDaySignoff(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"April that ll be all for day thanks","expiration_year":"twenty seven that ll be it for day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split-contraction omitted-article for-day sign-off expiration date accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want split-contraction omitted-article for-day sign-off expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after split-contraction omitted-article for-day sign-off expiration date")
	}
}

func TestGetExpirationDateTaskFiltersTrailingExpandedThatWillBeAllForDaySignoff(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"April that will be all for day thanks","expiration_year":"twenty seven that will be it for day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want expanded omitted-article for-day sign-off expiration date accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want expanded omitted-article for-day sign-off expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after expanded omitted-article for-day sign-off expiration date")
	}
}

func TestGetExpirationDateTaskFiltersPunctuatedSpokenFiller(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	_, err := update.Execute(context.Background(), `{"expiration_month":"uh, April","expiration_year":"um, twenty seven."}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want punctuated filler-spoken expiration date accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want punctuated filler-spoken expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after punctuated filler-spoken expiration date")
	}
}

func TestGetExpirationDateTaskFiltersSpokenPreamble(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	out, err := update.Execute(context.Background(), `{"expiration_month":"my expiration date is April","expiration_year":"twenty seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken expiration preamble accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want spoken expiration preamble normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after spoken expiration preamble")
	}
}

func TestGetExpirationDateTaskFiltersSpokenValidThroughPrefix(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	_, err := update.Execute(context.Background(), `{"expiration_month":"valid through April","expiration_year":"twenty seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken valid-through prefix accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want valid-through prefix filtered", result.Date)
		}
	default:
		t.Fatal("task did not complete after spoken valid-through prefix")
	}
}

func TestGetExpirationDateTaskFiltersSpokenGoodThruPrefix(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	_, err := update.Execute(context.Background(), `{"expiration_month":"good thru April","expiration_year":"twenty seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken good-thru prefix accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want good-thru prefix filtered", result.Date)
		}
	default:
		t.Fatal("task did not complete after spoken good-thru prefix")
	}
}

func TestGetExpirationDateTaskFiltersSpokenCardPrefix(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	_, err := update.Execute(context.Background(), `{"expiration_month":"card expires April","expiration_year":"twenty seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken card-expiration prefix accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want card-expiration prefix filtered", result.Date)
		}
	default:
		t.Fatal("task did not complete after spoken card-expiration prefix")
	}
}

func TestGetExpirationDateTaskFiltersSpokenExpiresInPrefix(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	_, err := update.Execute(context.Background(), `{"expiration_month":"expires in April","expiration_year":"twenty seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken expires-in prefix accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want expires-in prefix filtered", result.Date)
		}
	default:
		t.Fatal("task did not complete after spoken expires-in prefix")
	}
}

func TestGetExpirationDateTaskFiltersSpokenExpiresOnPrefix(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	_, err := update.Execute(context.Background(), `{"expiration_month":"expires on April","expiration_year":"twenty seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken expires-on prefix accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want expires-on prefix filtered", result.Date)
		}
	default:
		t.Fatal("task did not complete after spoken expires-on prefix")
	}
}

func TestGetExpirationDateTaskFiltersArticleCardPrefix(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	_, err := update.Execute(context.Background(), `{"expiration_month":"the card expires April","expiration_year":"twenty seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want article card-expiration prefix accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want article card-expiration prefix filtered", result.Date)
		}
	default:
		t.Fatal("task did not complete after article card-expiration prefix")
	}
}

func TestGetExpirationDateTaskFiltersSpokenEndOfMonthPrefix(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	_, err := update.Execute(context.Background(), `{"expiration_month":"end of April","expiration_year":"twenty seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken end-of-month prefix accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want end-of-month prefix filtered", result.Date)
		}
	default:
		t.Fatal("task did not complete after spoken end-of-month prefix")
	}
}

func TestGetExpirationDateTaskFiltersSpokenSeparator(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	_, err := update.Execute(context.Background(), `{"expiration_month":"eight slash,","expiration_year":"twenty nine."}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken slash expiration date accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Date != "08/29" {
			t.Fatalf("Date = %q, want spoken slash expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after slash-spoken expiration date")
	}
}

func TestGetExpirationDateTaskFiltersSpokenNumberConnector(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	_, err := update.Execute(context.Background(), `{"expiration_month":"eight","expiration_year":"twenty and nine"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken connector expiration date accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Date != "08/29" {
			t.Fatalf("Date = %q, want spoken connector expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after connector-spoken expiration date")
	}
}

func TestGetExpirationDateTaskNormalizesNaughtDigit(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	_, err := update.Execute(context.Background(), `{"expiration_month":"naught four","expiration_year":"twenty thirty one"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want naught spoken expiration digit accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/31" {
			t.Fatalf("Date = %q, want naught spoken expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after naught-spoken expiration date")
	}
}

func TestGetExpirationDateTaskNormalizesOweDigit(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	_, err := update.Execute(context.Background(), `{"expiration_month":"owe four","expiration_year":"twenty thirty one"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want owe spoken expiration digit accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/31" {
			t.Fatalf("Date = %q, want owe spoken expiration date normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after owe-spoken expiration date")
	}
}

func TestGetExpirationDateTaskNormalizesSpokenFullYearPhrase(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	_, err := update.Execute(context.Background(), `{"expiration_month":"April","expiration_year":"twenty twenty seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken full-year phrase accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want spoken full-year phrase normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after spoken full-year expiration date")
	}
}

func TestGetExpirationDateTaskNormalizesSpokenTwoThousandYear(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	_, err := update.Execute(context.Background(), `{"expiration_month":"April","expiration_year":"two thousand twenty seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken two-thousand year accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want spoken two-thousand year normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after spoken two-thousand expiration date")
	}
}

func TestGetExpirationDateTaskNormalizesSpokenDigitYear(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	_, err := update.Execute(context.Background(), `{"expiration_month":"April","expiration_year":"two zero two seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want digit-by-digit expiration year accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/27" {
			t.Fatalf("Date = %q, want digit-by-digit expiration year normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after digit-by-digit expiration date")
	}
}

func TestGetExpirationDateTaskNormalizesSpokenFutureFullYearPhrase(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	_, err := update.Execute(context.Background(), `{"expiration_month":"April","expiration_year":"twenty thirty one"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want future spoken full-year phrase accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/31" {
			t.Fatalf("Date = %q, want future spoken full-year phrase normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after future spoken full-year expiration date")
	}
}

func TestGetExpirationDateTaskNormalizesSpokenRoundedFutureYearPhrase(t *testing.T) {
	task := NewGetExpirationDateTask(false)
	update := &updateExpirationDateTool{task: task}

	_, err := update.Execute(context.Background(), `{"expiration_month":"April","expiration_year":"twenty thirty"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want rounded future spoken full-year phrase accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.Date != "04/30" {
			t.Fatalf("Date = %q, want rounded future spoken full-year phrase normalized", result.Date)
		}
	default:
		t.Fatal("task did not complete after rounded future spoken full-year expiration date")
	}
}

func TestGetExpirationDateTaskRejectsInvalidOrExpiredDate(t *testing.T) {
	cases := []struct {
		name string
		args string
		want string
	}{
		{
			name: "invalid month",
			args: `{"expiration_month":13,"expiration_year":35}`,
			want: "The expiration month is invalid, ask the user to repeat the expiration month.",
		},
		{
			name: "invalid year",
			args: `{"expiration_month":1,"expiration_year":100}`,
			want: "The expiration year is invalid, ask the user to repeat the expiration year.",
		},
		{
			name: "expired",
			args: `{"expiration_month":1,"expiration_year":0}`,
			want: "The expiration date is in the past, the card is expired. Ask the user to provide another card.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := NewGetExpirationDateTask(false)
			session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
			session.Assistant = &fakeDtmfSessionAssistant{}
			speechEvents := session.SpeechCreatedEvents()
			tool := &updateExpirationDateTool{task: task}

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

			if _, err := tool.Execute(context.Background(), tc.args); err != nil {
				t.Fatalf("Execute(%s) error = %v, want nil after prompting for invalid expiration date", tc.args, err)
			}

			select {
			case ev := <-speechEvents:
				if ev.Source != "generate_reply" {
					t.Fatalf("SpeechCreated Source = %q, want generate_reply", ev.Source)
				}
				if ev.SpeechHandle == nil {
					t.Fatal("SpeechCreated SpeechHandle = nil, want invalid-expiration reply handle")
				}
				if ev.SpeechHandle.Generation.Instructions == nil {
					t.Fatal("invalid-expiration instructions = nil, want invalid-expiration prompt")
				}
				if got := ev.SpeechHandle.Generation.Instructions.AsModality("text").String(); got != tc.want {
					t.Fatalf("invalid-expiration instructions = %q, want %q", got, tc.want)
				}
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for invalid-expiration prompt")
			}

			select {
			case result := <-task.Result:
				t.Fatalf("task completed with %#v, want no completion for invalid date", result)
			default:
			}
		})
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
	wantOut := "The expiration date has been updated.\n" +
		"Do not repeat the expiration date back to the user, ask them to repeat the expiration date.\n" +
		"Call `confirm_expiration_date` once the user confirms, do not call it preemptively.\n"
	if out != wantOut {
		t.Fatalf("update Execute() output = %q, want %q", out, wantOut)
	}
	if strings.Contains(out, "repeat themselves") {
		t.Fatalf("update Execute() output = %q, want explicit repeat-date wording", out)
	}
	if len(task.Agent.Tools) != 4 || task.Agent.Tools[3].Name() != "confirm_expiration_date" {
		t.Fatalf("tools = %#v, want confirm_expiration_date appended", task.Agent.Tools)
	}

	confirm := &confirmExpirationDateTool{task: task, expirationMonth: 12, expirationYear: futureYear, expirationDate: "12/" + twoDigit(futureYear)}
	confirmOut, err := confirm.Execute(context.Background(), `{"repeated_expiration_month":12,"repeated_expiration_year":`+itoa(futureYear)+`}`)
	if err != nil {
		t.Fatalf("confirm Execute() error = %v", err)
	}
	if confirmOut != "" {
		t.Fatalf("confirm Execute() output = %q, want empty output after completion", confirmOut)
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

func TestGetExpirationDateTaskStaleConfirmationPromptsForUpdatedDate(t *testing.T) {
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
		t.Fatalf("first update Execute() error = %v", err)
	}
	staleConfirm := &confirmExpirationDateTool{task: task, expirationMonth: 12, expirationYear: futureYear, expirationDate: "12/" + twoDigit(futureYear)}

	if _, err := update.Execute(context.Background(), `{"expiration_month":11,"expiration_year":`+itoa(futureYear)+`}`); err != nil {
		t.Fatalf("second update Execute() error = %v", err)
	}
	if _, err := staleConfirm.Execute(context.Background(), `{"repeated_expiration_month":12,"repeated_expiration_year":`+itoa(futureYear)+`}`); err != nil {
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
		want := "The expiration date has changed since confirmation was requested, ask the user to confirm the updated date."
		if ev.SpeechHandle.Generation.Instructions == nil {
			t.Fatal("stale confirmation instructions = nil, want changed-date prompt")
		}
		if got := ev.SpeechHandle.Generation.Instructions.AsModality("text").String(); got != want {
			t.Fatalf("stale confirmation instructions = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stale expiration-date confirmation prompt")
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for stale confirmation", result)
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

func TestCreditCardDefaultConfirmationUsesInputModality(t *testing.T) {
	textCtx := agent.WithRunContext(
		context.Background(),
		agent.NewRunContext(nil, agent.NewSpeechHandle(true, agent.InputDetails{Modality: "text"}), nil),
	)

	cardTask := NewGetCardNumberTask()
	cardOut, err := (&recordCardNumberTool{task: cardTask}).Execute(textCtx, `{"card_number":"4111111111111111"}`)
	if err != nil {
		t.Fatalf("card number Execute() error = %v", err)
	}
	if cardOut != "" {
		t.Fatalf("card number Execute() output = %q, want direct text completion without confirmation prompt", cardOut)
	}
	select {
	case result := <-cardTask.Result:
		if result.CardNumber != "4111111111111111" || result.Issuer != "Visa" {
			t.Fatalf("card number result = %#v, want direct text completion", result)
		}
	default:
		t.Fatal("card number task did not complete for text input")
	}

	codeTask := NewGetSecurityCodeTask()
	codeOut, err := (&updateSecurityCodeTool{task: codeTask}).Execute(textCtx, `{"security_code":"123"}`)
	if err != nil {
		t.Fatalf("security code Execute() error = %v", err)
	}
	if codeOut != "" {
		t.Fatalf("security code Execute() output = %q, want direct text completion without confirmation prompt", codeOut)
	}
	select {
	case result := <-codeTask.Result:
		if result.SecurityCode != "123" {
			t.Fatalf("security code result = %#v, want direct text completion", result)
		}
	default:
		t.Fatal("security code task did not complete for text input")
	}

	dateTask := NewGetExpirationDateTask()
	dateOut, err := (&updateExpirationDateTool{task: dateTask}).Execute(textCtx, `{"expiration_month":12,"expiration_year":35}`)
	if err != nil {
		t.Fatalf("expiration date Execute() error = %v", err)
	}
	if dateOut != "" {
		t.Fatalf("expiration date Execute() output = %q, want direct text completion without confirmation prompt", dateOut)
	}
	select {
	case result := <-dateTask.Result:
		if result.Date != "12/35" {
			t.Fatalf("expiration date result = %#v, want direct text completion", result)
		}
	default:
		t.Fatal("expiration date task did not complete for text input")
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
				"You are solely responsible for collecting the credit card number.",
				"Normalize spoken digits silently: 'four' → 4, 'zero' / 'oh' → 0.",
				"If the user refuses to provide a credit card number, call decline_card_capture().",
				"If the user wishes to start over the credit card collection process, call restart_card_collection().",
				"Never repeat any sensitive information, such as the user's credit card number, back to the user.",
				"Call `confirm_card_number` once the user has repeated their card number.",
			},
		},
		{
			name:         "security_code",
			instructions: NewGetSecurityCodeTask().Instructions,
			wantParts: []string{
				"You are solely responsible for collecting the user's card's security code.",
				"Normalize spoken digits silently: 'four' → 4, 'zero' / 'oh' → 0.",
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
				"Handle input as noisy voice transcription. Expect users to say the expiration date in formats like 'April twenty five', 'oh four twenty five', 'four slash twenty five', or 'April 2025'.",
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

func TestCreditCardSensitiveDigitInstructionsUseReferenceSpokenDigitGuidance(t *testing.T) {
	for _, tc := range []struct {
		name         string
		instructions string
	}{
		{name: "card_number", instructions: NewGetCardNumberTask().Instructions},
		{name: "security_code", instructions: NewGetSecurityCodeTask().Instructions},
	} {
		want := "Normalize spoken digits silently: 'four' → 4, 'zero' / 'oh' → 0."
		if !strings.Contains(tc.instructions, want) {
			t.Fatalf("%s instructions = %q, want spoken digit guidance %q", tc.name, tc.instructions, want)
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

func TestGetCardNumberTaskUsesReferenceUpdateToolName(t *testing.T) {
	task := NewGetCardNumberTask()

	if len(task.Agent.Tools) == 0 {
		t.Fatal("card number task has no tools")
	}
	if got := task.Agent.Tools[0].ID(); got != "update_card_number" {
		t.Fatalf("card number tool ID = %q, want update_card_number", got)
	}
	if got := task.Agent.Tools[0].Name(); got != "update_card_number" {
		t.Fatalf("card number tool Name = %q, want update_card_number", got)
	}
}

func TestGetCardNumberTaskUpdateToolUsesReferenceSchema(t *testing.T) {
	task := NewGetCardNumberTask()
	tool := task.Agent.Tools[0]

	wantDescription := "Call to record the user's card number. Only call once the entire number has been given, do not call in increments."
	if got := tool.Description(); got != wantDescription {
		t.Fatalf("card number tool description = %q, want %q", got, wantDescription)
	}

	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", tool.Parameters()["properties"])
	}
	schema, ok := properties["card_number"].(map[string]any)
	if !ok {
		t.Fatalf("card_number schema = %#v, want map", properties["card_number"])
	}
	wantParam := "The credit card number as a string with no dashes or spaces"
	if got := schema["description"]; got != wantParam {
		t.Fatalf("card_number description = %#v, want %q", got, wantParam)
	}
}

func TestGetSecurityCodeTaskUpdateToolUsesReferenceSchema(t *testing.T) {
	task := NewGetSecurityCodeTask()
	tool := task.Agent.Tools[0]

	wantDescription := "Call to update the card's security code."
	if got := tool.Description(); got != wantDescription {
		t.Fatalf("security code tool description = %q, want %q", got, wantDescription)
	}

	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", tool.Parameters()["properties"])
	}
	schema, ok := properties["security_code"].(map[string]any)
	if !ok {
		t.Fatalf("security_code schema = %#v, want map", properties["security_code"])
	}
	wantParam := "The card's security code (3-4 digits, may have leading zeros)."
	if got := schema["description"]; got != wantParam {
		t.Fatalf("security_code description = %#v, want %q", got, wantParam)
	}
}

func TestGetExpirationDateTaskUpdateToolUsesReferenceSchema(t *testing.T) {
	task := NewGetExpirationDateTask()
	tool := task.Agent.Tools[0]

	wantDescription := "Call to update the card's expiration date. Collect both the numerical month and year."
	if got := tool.Description(); got != wantDescription {
		t.Fatalf("expiration date tool description = %q, want %q", got, wantDescription)
	}

	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", tool.Parameters()["properties"])
	}
	cases := map[string]string{
		"expiration_month": "The numerical expiration month of the card, example: '04' for April",
		"expiration_year":  "The numerical expiration year of the card shortened to the last two digits, for example, '35' for 2035",
	}
	for field, want := range cases {
		schema, ok := properties[field].(map[string]any)
		if !ok {
			t.Fatalf("%s schema = %#v, want map", field, properties[field])
		}
		if got := schema["description"]; got != want {
			t.Fatalf("%s description = %#v, want %q", field, got, want)
		}
	}
}

func TestCreditCardSubtasksExplicitAskIgnoreUpdateToolsOnEnter(t *testing.T) {
	cases := []struct {
		name string
		tool llm.Tool
	}{
		{
			name: "card_number",
			tool: NewGetCardNumberTaskWithOptions(GetCardNumberOptions{
				RequireExplicitAsk: true,
			}).Agent.Tools[0],
		},
		{
			name: "security_code",
			tool: NewGetSecurityCodeTaskWithOptions(GetSecurityCodeOptions{
				RequireExplicitAsk: true,
			}).Agent.Tools[0],
		},
		{
			name: "expiration_date",
			tool: NewGetExpirationDateTaskWithOptions(GetExpirationDateOptions{
				RequireExplicitAsk: true,
			}).Agent.Tools[0],
		},
	}
	for _, tc := range cases {
		if !llm.ToolHasFlag(tc.tool, llm.ToolFlagIgnoreOnEnter) {
			t.Fatalf("%s %s ToolFlags missing ToolFlagIgnoreOnEnter when RequireExplicitAsk is true", tc.name, tc.tool.Name())
		}
	}
}

func TestGetCardNumberTaskOptionsSeedReferenceChatContext(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.ChatMessage{
		ID:      "prior-card-number",
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "I already read the card number as four one one one then one one one one."}},
	})
	opts := GetCardNumberOptions{}
	field := reflect.ValueOf(&opts).Elem().FieldByName("ChatContext")
	if !field.IsValid() {
		t.Fatal("GetCardNumberOptions.ChatContext missing; want reference chat_ctx constructor option")
	}
	field.Set(reflect.ValueOf(chatCtx))

	task := NewGetCardNumberTaskWithOptions(opts)

	if task.ChatCtx == nil {
		t.Fatal("task ChatCtx = nil, want constructor chat context copy")
	}
	if task.ChatCtx == chatCtx {
		t.Fatal("task ChatCtx aliases constructor context, want reference-style copy")
	}
	if task.ChatCtx.GetByID("prior-card-number") == nil {
		t.Fatalf("task ChatCtx items = %#v, want constructor chat item", task.ChatCtx.Items)
	}
}

func TestGetSecurityCodeTaskOptionsSeedReferenceChatContext(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.ChatMessage{
		ID:      "prior-security-code",
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "The security code is zero four two."}},
	})
	opts := GetSecurityCodeOptions{}
	field := reflect.ValueOf(&opts).Elem().FieldByName("ChatContext")
	if !field.IsValid() {
		t.Fatal("GetSecurityCodeOptions.ChatContext missing; want reference chat_ctx constructor option")
	}
	field.Set(reflect.ValueOf(chatCtx))

	task := NewGetSecurityCodeTaskWithOptions(opts)

	if task.ChatCtx == nil {
		t.Fatal("task ChatCtx = nil, want constructor chat context copy")
	}
	if task.ChatCtx == chatCtx {
		t.Fatal("task ChatCtx aliases constructor context, want reference-style copy")
	}
	if task.ChatCtx.GetByID("prior-security-code") == nil {
		t.Fatalf("task ChatCtx items = %#v, want constructor chat item", task.ChatCtx.Items)
	}
}

func TestGetExpirationDateTaskOptionsSeedReferenceChatContext(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.ChatMessage{
		ID:      "prior-expiration-date",
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "The card expires in April twenty seven."}},
	})
	opts := GetExpirationDateOptions{}
	field := reflect.ValueOf(&opts).Elem().FieldByName("ChatContext")
	if !field.IsValid() {
		t.Fatal("GetExpirationDateOptions.ChatContext missing; want reference chat_ctx constructor option")
	}
	field.Set(reflect.ValueOf(chatCtx))

	task := NewGetExpirationDateTaskWithOptions(opts)

	if task.ChatCtx == nil {
		t.Fatal("task ChatCtx = nil, want constructor chat context copy")
	}
	if task.ChatCtx == chatCtx {
		t.Fatal("task ChatCtx aliases constructor context, want reference-style copy")
	}
	if task.ChatCtx.GetByID("prior-expiration-date") == nil {
		t.Fatalf("task ChatCtx items = %#v, want constructor chat item", task.ChatCtx.Items)
	}
}

func TestGetCardNumberTaskConfirmToolUsesReferenceSchema(t *testing.T) {
	tool := &confirmCardNumberTool{task: NewGetCardNumberTask(), cardNumber: "4111111111111111"}

	wantDescription := "Call after the user repeats their card number for confirmation."
	if got := tool.Description(); got != wantDescription {
		t.Fatalf("confirm card number tool description = %q, want %q", got, wantDescription)
	}

	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", tool.Parameters()["properties"])
	}
	schema, ok := properties["repeated_card_number"].(map[string]any)
	if !ok {
		t.Fatalf("repeated_card_number schema = %#v, want map", properties["repeated_card_number"])
	}
	wantParam := "The card number repeated by the user as a string"
	if got := schema["description"]; got != wantParam {
		t.Fatalf("repeated_card_number description = %#v, want %q", got, wantParam)
	}
}

func TestGetSecurityCodeTaskConfirmToolUsesReferenceSchema(t *testing.T) {
	tool := &confirmSecurityCodeTool{task: NewGetSecurityCodeTask(), securityCode: "123"}

	wantDescription := "Call after the user repeats their security code for confirmation."
	if got := tool.Description(); got != wantDescription {
		t.Fatalf("confirm security code tool description = %q, want %q", got, wantDescription)
	}

	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", tool.Parameters()["properties"])
	}
	schema, ok := properties["repeated_security_code"].(map[string]any)
	if !ok {
		t.Fatalf("repeated_security_code schema = %#v, want map", properties["repeated_security_code"])
	}
	wantParam := "The security code repeated by the user"
	if got := schema["description"]; got != wantParam {
		t.Fatalf("repeated_security_code description = %#v, want %q", got, wantParam)
	}
}

func TestGetExpirationDateTaskConfirmToolUsesReferenceSchema(t *testing.T) {
	tool := &confirmExpirationDateTool{
		task:            NewGetExpirationDateTask(),
		expirationMonth: 12,
		expirationYear:  35,
		expirationDate:  "12/35",
	}

	wantDescription := "Call after the user repeats their expiration date for confirmation."
	if got := tool.Description(); got != wantDescription {
		t.Fatalf("confirm expiration date tool description = %q, want %q", got, wantDescription)
	}

	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", tool.Parameters()["properties"])
	}
	cases := map[string]string{
		"repeated_expiration_month": "The expiration month repeated by the user",
		"repeated_expiration_year":  "The expiration year repeated by the user",
	}
	for field, want := range cases {
		schema, ok := properties[field].(map[string]any)
		if !ok {
			t.Fatalf("%s schema = %#v, want map", field, properties[field])
		}
		if got := schema["description"]; got != want {
			t.Fatalf("%s description = %#v, want %q", field, got, want)
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
			want: "Get the user's credit card number. First scan the conversation - if a credit card number was already given (e.g. the user volunteered it before the task started), use it via update_card_number rather than re-asking. Only ask fresh when no credit card number is in the conversation yet.",
		},
		{
			name: "security_code",
			got:  securityCodeOnEnterPrompt(),
			want: "Get the user's card security code. First scan the conversation - if a code was already given, use it via update_security_code rather than re-asking. Only ask fresh when no code is in the conversation yet.",
		},
		{
			name: "expiration_date",
			got:  expirationDateOnEnterPrompt(),
			// Mirrors Python reference wording: "no date", not "no expiration date".
			want: "Get the user's card expiration date. First scan the conversation - if an expiration date was already given, use it via update_expiration_date rather than re-asking. Only ask fresh when no date is in the conversation yet.",
		},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Fatalf("%s OnEnter prompt = %q, want %q", tc.name, tc.got, tc.want)
		}
		if !strings.Contains(tc.got, "rather than re-asking") {
			t.Fatalf("%s OnEnter prompt = %q, want conversation scan guidance", tc.name, tc.got)
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

func TestGetCreditCardTaskCombinesCardholderFirstLastOnly(t *testing.T) {
	task := NewGetCreditCardTask(true)

	err := task.completeCreditCardFromTaskResults(map[string]any{
		"cardholder_name_task": &GetNameResult{FirstName: "Ada", MiddleName: "Byron", LastName: "Lovelace"},
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
			t.Fatalf("CardholderName = %q, want reference first+last only", result.CardholderName)
		}
	default:
		t.Fatal("task did not complete after combining subtask results")
	}
}

func TestGetCreditCardTaskInstructionsUseReferenceSubtaskOrder(t *testing.T) {
	task := NewGetCreditCardTask()

	if task.Instructions != "*none*" {
		t.Fatalf("Instructions = %q, want reference aggregate placeholder %q", task.Instructions, "*none*")
	}

	group := task.buildTaskGroup()
	wantIDs := []string{"card_number_task", "expiration_date_task", "security_code_task", "cardholder_name_task"}
	if len(group.RegisteredTasks) != len(wantIDs) {
		t.Fatalf("RegisteredTasks = %d, want %d", len(group.RegisteredTasks), len(wantIDs))
	}
	for i, want := range wantIDs {
		if got := group.RegisteredTasks[i].ID; got != want {
			t.Fatalf("RegisteredTasks[%d].ID = %q, want %q", i, got, want)
		}
	}

	stale := "cardholder name, card number, security code, and expiration date"
	if strings.Contains(task.Instructions, stale) {
		t.Fatalf("Instructions = %q, want no stale subtask order %q", task.Instructions, stale)
	}
}

func TestGetCreditCardTaskBuildsReferenceSubtasks(t *testing.T) {
	task := NewGetCreditCardTask()
	group := task.buildTaskGroup()

	if len(group.RegisteredTasks) != 4 {
		t.Fatalf("RegisteredTasks = %d, want 4", len(group.RegisteredTasks))
	}
	wantIDs := []string{"card_number_task", "expiration_date_task", "security_code_task", "cardholder_name_task"}
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
			if !nameTask.CollectFirstName || !nameTask.CollectLastName || !nameTask.RequireConfirmation || !nameTask.RequireExplicitAsk {
				t.Fatalf("name task options = %#v, want first+last with confirmation and explicit ask", nameTask)
			}
			if !llm.ToolHasFlag(nameTask.Agent.Tools[0], llm.ToolFlagIgnoreOnEnter) {
				t.Fatalf("%s ToolFlags missing ToolFlagIgnoreOnEnter for cardholder name", nameTask.Agent.Tools[0].Name())
			}
			for _, want := range []string{
				"You are collecting the name on the credit card (the cardholder).",
				"anchor the question to the card or cardholder",
				"not just 'is it [name]?' in the abstract.",
			} {
				if !strings.Contains(nameTask.Instructions, want) {
					t.Fatalf("cardholder name instructions = %q, want %q", nameTask.Instructions, want)
				}
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

func TestGetCreditCardTaskCardholderKeepsDefaultConfirmationModality(t *testing.T) {
	task := NewGetCreditCardTask()
	group := task.buildTaskGroup()

	for _, info := range group.RegisteredTasks {
		if info.ID != "cardholder_name_task" {
			continue
		}
		nameTask, ok := info.TaskFactory().(*GetNameTask)
		if !ok {
			t.Fatalf("cardholder task = %T, want *GetNameTask", info.TaskFactory())
		}
		if nameTask.RequireConfirmationSet {
			t.Fatalf("cardholder RequireConfirmationSet = true, want reference omitted confirmation to stay modality-aware")
		}
		return
	}
	t.Fatal("cardholder_name_task not registered")
}

func TestGetCreditCardTaskBuildsTaskGroupWithParentChatContext(t *testing.T) {
	task := NewGetCreditCardTask()
	task.ChatCtx.Append(&llm.ChatMessage{
		ID:      "prior-card-detail",
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "The cardholder is Ada Lovelace."}},
	})

	group := task.buildTaskGroup()
	if group.ChatCtx == nil {
		t.Fatal("group ChatCtx = nil, want parent context copy")
	}
	if group.ChatCtx == task.ChatCtx {
		t.Fatal("group ChatCtx aliases parent, want copy")
	}
	if group.ChatCtx.GetByID("prior-card-detail") == nil {
		t.Fatalf("group ChatCtx items = %#v, want prior parent chat item", group.ChatCtx.Items)
	}
	for _, info := range group.RegisteredTasks {
		child := info.TaskFactory()
		if child.GetAgent().ChatCtx.GetByID("prior-card-detail") == nil {
			t.Fatalf("%s child ChatCtx items = %#v, want prior parent chat item", info.ID, child.GetAgent().ChatCtx.Items)
		}
	}
}

func TestGetCreditCardTaskOptionsSeedReferenceChatContext(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.ChatMessage{
		ID:      "prior-cardholder",
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "The cardholder should be Ada Lovelace."}},
	})
	opts := GetCreditCardOptions{}
	field := reflect.ValueOf(&opts).Elem().FieldByName("ChatContext")
	if !field.IsValid() {
		t.Fatal("GetCreditCardOptions.ChatContext missing; want reference chat_ctx constructor option")
	}
	field.Set(reflect.ValueOf(chatCtx))

	task := NewGetCreditCardTaskWithOptions(opts)

	if task.ChatCtx == nil {
		t.Fatal("task ChatCtx = nil, want constructor chat context copy")
	}
	if task.ChatCtx == chatCtx {
		t.Fatal("task ChatCtx aliases constructor context, want reference-style copy")
	}
	if task.ChatCtx.GetByID("prior-cardholder") == nil {
		t.Fatalf("task ChatCtx items = %#v, want constructor chat item", task.ChatCtx.Items)
	}
	group := task.buildTaskGroup()
	if group.ChatCtx.GetByID("prior-cardholder") == nil {
		t.Fatalf("group ChatCtx items = %#v, want constructor chat item passed to TaskGroup", group.ChatCtx.Items)
	}
}

func TestGetCreditCardTaskPreservesReferenceExtraTools(t *testing.T) {
	opts := GetCreditCardOptions{}
	field := reflect.ValueOf(&opts).Elem().FieldByName("Tools")
	if !field.IsValid() {
		t.Fatal("GetCreditCardOptions.Tools missing; want reference tools constructor option")
	}
	field.Set(reflect.ValueOf([]llm.Tool{referenceCreditCardExtraTool{id: "card_help"}}))

	task := NewGetCreditCardTaskWithOptions(opts)

	if len(task.Agent.Tools) != 1 {
		t.Fatalf("tools len = %d, want caller-provided aggregate tool preserved", len(task.Agent.Tools))
	}
	if got := task.Agent.Tools[0].Name(); got != "card_help" {
		t.Fatalf("tools[0] = %q, want caller-provided tool preserved", got)
	}
}

func TestGetCreditCardTaskPropagatesExtraInstructionsToSubtasks(t *testing.T) {
	extra := "Ask whether this is the card the caller wants saved on file."
	task := NewGetCreditCardTaskWithOptions(GetCreditCardOptions{
		RequireConfirmation:    true,
		RequireConfirmationSet: true,
		ExtraInstructions:      extra,
	})

	group := task.buildTaskGroup()
	for _, info := range group.RegisteredTasks {
		child := info.TaskFactory()
		if !strings.Contains(child.GetAgent().Instructions, extra) {
			t.Fatalf("%s instructions = %q, want extra guidance %q", info.ID, child.GetAgent().Instructions, extra)
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

func TestCreditCardSubtaskOnEnterPromptsUseInstructions(t *testing.T) {
	cases := []struct {
		name string
		task agent.AgentInterface
		want string
	}{
		{
			name: "card number",
			task: NewGetCardNumberTask(),
			want: cardNumberOnEnterPrompt(),
		},
		{
			name: "security code",
			task: NewGetSecurityCodeTask(),
			want: securityCodeOnEnterPrompt(),
		},
		{
			name: "expiration date",
			task: NewGetExpirationDateTask(),
			want: expirationDateOnEnterPrompt(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session := agent.NewAgentSession(tc.task, nil, agent.AgentSessionOptions{})
			session.Assistant = &fakeDtmfSessionAssistant{}
			speechEvents := session.SpeechCreatedEvents()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := session.Start(ctx); err != nil {
				t.Fatalf("Start error = %v", err)
			}
			defer session.Stop(context.Background())

			select {
			case ev := <-speechEvents:
				if ev.SpeechHandle == nil {
					t.Fatal("SpeechCreated SpeechHandle = nil, want on-enter reply handle")
				}
				if ev.SpeechHandle.Generation.UserMessage != nil {
					t.Fatalf("on-enter UserMessage = %#v, want nil for instruction-backed prompt", ev.SpeechHandle.Generation.UserMessage)
				}
				if ev.SpeechHandle.Generation.Instructions == nil {
					t.Fatal("on-enter instructions = nil, want reference prompt")
				}
				if got := ev.SpeechHandle.Generation.Instructions.AsModality("text").String(); got != tc.want {
					t.Fatalf("on-enter instructions = %q, want %q", got, tc.want)
				}
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for card subtask on-enter prompt")
			}
		})
	}
}

func TestDeclineCardCaptureToolUsesReferenceSchema(t *testing.T) {
	tool := &declineCardCaptureTool{task: NewGetCardNumberTask(false)}

	wantDescription := "Handles the case when the user explicitly declines to provide a detail for their card information."
	if got := tool.Description(); got != wantDescription {
		t.Fatalf("decline_card_capture description = %q, want %q", got, wantDescription)
	}

	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", tool.Parameters()["properties"])
	}
	reason, ok := properties["reason"].(map[string]any)
	if !ok {
		t.Fatalf("reason schema = %#v, want map", properties["reason"])
	}
	wantParam := "A short explanation of why the user declined to provide card information"
	if got := reason["description"]; got != wantParam {
		t.Fatalf("reason description = %#v, want %q", got, wantParam)
	}
}

func TestRestartCardCollectionToolUsesReferenceSchema(t *testing.T) {
	tool := &restartCardCollectionTool{task: NewGetCardNumberTask(false)}

	wantDescription := "Handles the case when the user wishes to start over the card information collection process and validate a new card."
	if got := tool.Description(); got != wantDescription {
		t.Fatalf("restart_card_collection description = %q, want %q", got, wantDescription)
	}

	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", tool.Parameters()["properties"])
	}
	reason, ok := properties["reason"].(map[string]any)
	if !ok {
		t.Fatalf("reason schema = %#v, want map", properties["reason"])
	}
	wantParam := "A short explanation of why the user wishes to start over"
	if got := reason["description"]; got != wantParam {
		t.Fatalf("reason description = %#v, want %q", got, wantParam)
	}
}

func TestDeclineCardCaptureToolFailsWithTypedReason(t *testing.T) {
	task := NewGetCardNumberTask(false)
	tool := &declineCardCaptureTool{task: task}

	out, err := tool.Execute(context.Background(), `{"reason":"user refused"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after decline", out)
	}
	_, err = task.WaitAny(context.Background())
	var declined *CardCaptureDeclinedError
	if !errors.As(err, &declined) {
		t.Fatalf("WaitAny() error = %T %v, want CardCaptureDeclinedError", err, err)
	}
	if declined.Reason != "user refused" {
		t.Fatalf("Reason = %q, want user refused", declined.Reason)
	}
}

func TestDeclineCardCaptureToolUsesRunContextCurrentAgent(t *testing.T) {
	staleTask := NewGetCardNumberTask(false)
	currentTask := NewGetSecurityCodeTask(false)
	session := agent.NewAgentSession(currentTask, nil, agent.AgentSessionOptions{})
	ctx := agent.WithRunContext(context.Background(), agent.NewRunContext(session, nil, nil))
	tool := &declineCardCaptureTool{task: staleTask}

	out, err := tool.Execute(ctx, `{"reason":"user refused current field"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after decline", out)
	}

	select {
	case err := <-currentTask.Err:
		var declined *CardCaptureDeclinedError
		if !errors.As(err, &declined) {
			t.Fatalf("current task error = %T %v, want CardCaptureDeclinedError", err, err)
		}
		if declined.Reason != "user refused current field" {
			t.Fatalf("Reason = %q, want user refused current field", declined.Reason)
		}
	case <-time.After(time.Second):
		t.Fatal("current task did not fail after decline_card_capture")
	}

	select {
	case err := <-staleTask.Err:
		t.Fatalf("stale task failed with %v, want decline routed to current agent", err)
	default:
	}
}

func TestDeclineCardCaptureToolDoesNotFailDifferentCurrentAgent(t *testing.T) {
	staleTask := NewGetCardNumberTask(false)
	currentTask := NewGetEmailTask(GetEmailOptions{})
	session := agent.NewAgentSession(currentTask, nil, agent.AgentSessionOptions{})
	ctx := agent.WithRunContext(context.Background(), agent.NewRunContext(session, nil, nil))
	tool := &declineCardCaptureTool{task: staleTask}

	out, err := tool.Execute(ctx, `{"reason":"late stale card decline"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after decline", out)
	}

	select {
	case err := <-currentTask.Err:
		t.Fatalf("current email task failed with %v, want stale card decline ignored for different current agent", err)
	default:
	}

	select {
	case err := <-staleTask.Err:
		var declined *CardCaptureDeclinedError
		if !errors.As(err, &declined) {
			t.Fatalf("stale task error = %T %v, want CardCaptureDeclinedError", err, err)
		}
		if declined.Reason != "late stale card decline" {
			t.Fatalf("Reason = %q, want late stale card decline", declined.Reason)
		}
	case <-time.After(time.Second):
		t.Fatal("stale card task did not fail after decline_card_capture")
	}
}

func TestRestartCardCollectionToolFailsWithTypedReason(t *testing.T) {
	task := NewGetCardNumberTask(false)
	tool := &restartCardCollectionTool{task: task}

	out, err := tool.Execute(context.Background(), `{"reason":"wrong card"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after restart", out)
	}
	_, err = task.WaitAny(context.Background())
	var restart *CardCollectionRestartError
	if !errors.As(err, &restart) {
		t.Fatalf("WaitAny() error = %T %v, want CardCollectionRestartError", err, err)
	}
	if restart.Reason != "wrong card" {
		t.Fatalf("Reason = %q, want wrong card", restart.Reason)
	}
}

func TestRestartCardCollectionToolUsesRunContextCurrentAgent(t *testing.T) {
	staleTask := NewGetCardNumberTask(false)
	currentTask := NewGetSecurityCodeTask(false)
	session := agent.NewAgentSession(currentTask, nil, agent.AgentSessionOptions{})
	ctx := agent.WithRunContext(context.Background(), agent.NewRunContext(session, nil, nil))
	tool := &restartCardCollectionTool{task: staleTask}

	out, err := tool.Execute(ctx, `{"reason":"wrong current field"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after restart", out)
	}

	select {
	case err := <-currentTask.Err:
		var restart *CardCollectionRestartError
		if !errors.As(err, &restart) {
			t.Fatalf("current task error = %T %v, want CardCollectionRestartError", err, err)
		}
		if restart.Reason != "wrong current field" {
			t.Fatalf("Reason = %q, want wrong current field", restart.Reason)
		}
	case <-time.After(time.Second):
		t.Fatal("current task did not fail after restart_card_collection")
	}

	select {
	case err := <-staleTask.Err:
		t.Fatalf("stale task failed with %v, want restart routed to current agent", err)
	default:
	}
}

func TestRestartCardCollectionToolDoesNotFailDifferentCurrentAgent(t *testing.T) {
	staleTask := NewGetCardNumberTask(false)
	currentTask := NewGetEmailTask(GetEmailOptions{})
	session := agent.NewAgentSession(currentTask, nil, agent.AgentSessionOptions{})
	ctx := agent.WithRunContext(context.Background(), agent.NewRunContext(session, nil, nil))
	tool := &restartCardCollectionTool{task: staleTask}

	out, err := tool.Execute(ctx, `{"reason":"late stale card restart"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after restart", out)
	}

	select {
	case err := <-currentTask.Err:
		t.Fatalf("current email task failed with %v, want stale card restart ignored for different current agent", err)
	default:
	}

	select {
	case err := <-staleTask.Err:
		var restart *CardCollectionRestartError
		if !errors.As(err, &restart) {
			t.Fatalf("stale task error = %T %v, want CardCollectionRestartError", err, err)
		}
		if restart.Reason != "late stale card restart" {
			t.Fatalf("Reason = %q, want late stale card restart", restart.Reason)
		}
	case <-time.After(time.Second):
		t.Fatal("stale card task did not fail after restart_card_collection")
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

type referenceCreditCardExtraTool struct {
	id string
}

func (t referenceCreditCardExtraTool) ID() string {
	return t.id
}

func (t referenceCreditCardExtraTool) Name() string {
	return t.id
}

func (t referenceCreditCardExtraTool) Description() string {
	return "reference credit-card extra tool"
}

func (t referenceCreditCardExtraTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t referenceCreditCardExtraTool) Execute(ctx context.Context, args string) (string, error) {
	return "", nil
}
