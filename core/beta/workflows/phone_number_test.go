package workflows

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestGetPhoneNumberTaskRecordsValidNumberWithoutConfirmation(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"(555) 123-4567"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// Reference returns None after no-confirm completion, avoiding extra post-completion tool chatter.
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
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

func TestGetPhoneNumberTaskNormalizesReferenceWhitespace(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), "{\"phone_number\":\"(555) 123\\r4567\"}")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
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

func TestGetPhoneNumberTaskNormalizesSpokenDigits(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one two three four five six seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken digits accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want spoken digits normalized", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after spoken phone number")
	}
}

func TestGetPhoneNumberTaskNormalizesNoisySTTDigitHomophones(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one to three for five six ate"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want noisy STT digit homophones accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234568" {
			t.Fatalf("PhoneNumber = %q, want noisy STT digit homophones normalized", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after noisy STT phone number")
	}
}

func TestGetPhoneNumberTaskNormalizesForeHomophone(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one two three fore five six seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want fore homophone accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want fore homophone normalized", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after fore-homophone phone number")
	}
}

func TestGetPhoneNumberTaskNormalizesThreeHomophones(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one tree free four five six seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want three homophones accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551334567" {
			t.Fatalf("PhoneNumber = %q, want three homophones normalized", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after three-homophone phone number")
	}
}

func TestGetPhoneNumberTaskNormalizesSixHomophone(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one two three four five sex seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want six homophone accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want six homophone normalized", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after six-homophone phone number")
	}
}

func TestGetPhoneNumberTaskNormalizesWonHomophone(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"won two three four five six seven eight nine zero"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want won homophone accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "1234567890" {
			t.Fatalf("PhoneNumber = %q, want won homophone normalized", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after won-homophone phone number")
	}
}

func TestGetPhoneNumberTaskFiltersTrailingForMeSignoff(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one two three four five six seven that is all for me"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-me sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want trailing for-me sign-off filtered", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after trailing for-me sign-off")
	}
}

func TestGetPhoneNumberTaskFiltersTrailingForYouSignoff(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one two three four five six seven that's all for you"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-you sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want trailing for-you sign-off filtered", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after trailing for-you sign-off")
	}
}

func TestGetPhoneNumberTaskFiltersTrailingShortForYouSignoff(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one two three four five six seven for you thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want short trailing for-you sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want short trailing for-you sign-off filtered", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after short trailing for-you sign-off")
	}
}

func TestGetPhoneNumberTaskFiltersTrailingContractionForMeSignoff(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one two three four five six seven that's all for me"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing contraction sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want trailing contraction sign-off filtered", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after trailing contraction sign-off")
	}
}

func TestGetPhoneNumberTaskFiltersTrailingForTodaySignoff(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one two three four five six seven for today thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-today sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want trailing for-today sign-off filtered", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after trailing for-today sign-off")
	}
}

func TestGetPhoneNumberTaskFiltersTrailingForNowSignoff(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one two three four five six seven that's it for now"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-now sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want trailing for-now sign-off filtered", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after trailing for-now sign-off")
	}
}

func TestGetPhoneNumberTaskFiltersTrailingForNowThanksSignoff(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one two three four five six seven that's it for now thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-now-thanks sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want trailing for-now-thanks sign-off filtered", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after trailing for-now-thanks sign-off")
	}
}

func TestGetPhoneNumberTaskFiltersTrailingForTodayThanksSignoff(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one two three four five six seven that's it for today thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-today-thanks sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want trailing for-today-thanks sign-off filtered", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after trailing for-today-thanks sign-off")
	}
}

func TestGetPhoneNumberTaskFiltersTrailingForTheDayThanksSignoff(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one two three four five six seven that's it for the day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-the-day-thanks sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want trailing for-the-day-thanks sign-off filtered", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after trailing for-the-day-thanks sign-off")
	}
}

func TestGetPhoneNumberTaskNormalizesNinerDigit(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one two three four five six niner"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want niner spoken digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234569" {
			t.Fatalf("PhoneNumber = %q, want niner normalized to 9", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after niner phone number")
	}
}

func TestGetPhoneNumberTaskNormalizesAughtDigit(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one two three four aught six seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want aught spoken digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234067" {
			t.Fatalf("PhoneNumber = %q, want aught normalized to 0", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after aught phone number")
	}
}

func TestGetPhoneNumberTaskNormalizesNaughtDigit(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one two three four naught six seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want naught spoken digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234067" {
			t.Fatalf("PhoneNumber = %q, want naught normalized to 0", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after naught phone number")
	}
}

func TestGetPhoneNumberTaskNormalizesNoughtDigit(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one two three four nought six seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want nought spoken digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234067" {
			t.Fatalf("PhoneNumber = %q, want nought normalized to 0", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after nought phone number")
	}
}

func TestGetPhoneNumberTaskNormalizesOughtDigit(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one two three four ought six seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want ought spoken digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234067" {
			t.Fatalf("PhoneNumber = %q, want ought normalized to 0", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after ought phone number")
	}
}

func TestGetPhoneNumberTaskNormalizesOweDigit(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one two three four owe six seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want owe spoken digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234067" {
			t.Fatalf("PhoneNumber = %q, want owe normalized to 0", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after owe phone number")
	}
}

func TestGetPhoneNumberTaskNormalizesSpokenDoubleDigits(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five double five one two three four five six seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken doubled digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want spoken double digit normalized", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after spoken doubled phone number")
	}
}

func TestGetPhoneNumberTaskNormalizesSpokenQuadrupleDigits(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"quadruple five one two three four five six"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken quadruple digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5555123456" {
			t.Fatalf("PhoneNumber = %q, want spoken quadruple digit normalized", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after spoken quadruple phone number")
	}
}

func TestGetPhoneNumberTaskNormalizesSpokenDoubleDigitsAcrossFiller(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five double uh five one two three four five six seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want doubled digit across filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want filler after double ignored", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after spoken doubled phone number with filler")
	}
}

func TestGetPhoneNumberTaskFiltersTrailingExpandedThatWillBeAllForTheDaySignoff(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one two three four five six seven that will be all for the day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want expanded that-will-be for-the-day sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want expanded that-will-be for-the-day sign-off filtered", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after expanded that-will-be for-the-day sign-off")
	}
}

func TestGetPhoneNumberTaskFiltersTrailingThatllBeAllForTheDaySignoff(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one two three four five six seven that'll be all for the day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want that'll-be for-the-day sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want that'll-be for-the-day sign-off filtered", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after that'll-be for-the-day sign-off")
	}
}

func TestGetPhoneNumberTaskFiltersTrailingThatllBeAllForDaySignoff(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one two three four five six seven that'll be all for day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want that'll-be for-day sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want that'll-be for-day sign-off filtered", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after that'll-be for-day sign-off")
	}
}

func TestGetPhoneNumberTaskFiltersTrailingShortForTheDaySignoff(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one two three four five six seven for the day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want short for-the-day sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want short for-the-day sign-off filtered", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after short for-the-day sign-off")
	}
}

func TestGetPhoneNumberTaskNormalizesSpokenDoubleDigitsAcrossLikeFiller(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five double like five one two three four five six seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want doubled digit across like filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want like filler after double ignored", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after spoken doubled phone number with like filler")
	}
}

func TestGetPhoneNumberTaskNormalizesSpokenDoubleDigitsAcrossActuallyFiller(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five double actually five one two three four five six seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want correction filler between double and digit ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want correction filler stripped without losing double digit", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after doubled phone number with correction filler")
	}
}

func TestGetPhoneNumberTaskNormalizesSpokenDoubleDigitsAcrossSorryFiller(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five double sorry five one two three four five six seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want correction filler between double and digit ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want apology filler stripped without losing double digit", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after doubled phone number with apology filler")
	}
}

func TestGetPhoneNumberTaskNormalizesSpokenPlusPrefix(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"plus one five five five one two three four five six seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken plus prefix accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "+15551234567" {
			t.Fatalf("PhoneNumber = %q, want spoken plus prefix normalized", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after spoken plus phone number")
	}
}

func TestGetPhoneNumberTaskNormalizesSpokenCountryCodePrefix(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"country code one five five five one two three four five six seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken country-code prefix accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "+15551234567" {
			t.Fatalf("PhoneNumber = %q, want spoken country-code prefix normalized", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after spoken country-code phone number")
	}
}

func TestGetPhoneNumberTaskNormalizesSpokenHundred(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"one eight hundred five five five one two one two"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken hundred accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "18005551212" {
			t.Fatalf("PhoneNumber = %q, want spoken hundred normalized", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after spoken hundred phone number")
	}
}

func TestGetPhoneNumberTaskNormalizesSpokenEightHundred(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"eight hundred five five five one two one two"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken eight hundred accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "8005551212" {
			t.Fatalf("PhoneNumber = %q, want spoken eight hundred normalized", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after spoken eight hundred phone number")
	}
}

func TestGetPhoneNumberTaskNormalizesSpokenHundredAreaCode(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five hundred fifty five one two three four five six seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken hundred area code accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want spoken hundred area code normalized", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after spoken hundred area code phone number")
	}
}

func TestGetPhoneNumberTaskNormalizesSpokenHundredSingleDigitGroup(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one hundred tree four five six seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken hundred single-digit phone group accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551034567" {
			t.Fatalf("PhoneNumber = %q, want spoken hundred single-digit group normalized", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after spoken hundred single-digit phone number")
	}
}

func TestGetPhoneNumberTaskNormalizesSpokenHundredNaughtTail(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one hundred naught five four five six seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken hundred naught tail accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551054567" {
			t.Fatalf("PhoneNumber = %q, want spoken hundred naught tail normalized", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after spoken hundred naught phone number")
	}
}

func TestGetPhoneNumberTaskNormalizesSpokenGroupedNumbers(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five fifty five one twenty three forty five sixty seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken grouped phone number accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want spoken grouped phone number normalized", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after spoken grouped phone number")
	}
}

func TestGetPhoneNumberTaskNormalizesTwentyOhGroupedDigits(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one twenty oh five six seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want twenty-oh grouped phone number accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551200567" {
			t.Fatalf("PhoneNumber = %q, want twenty-oh group normalized", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after twenty-oh grouped phone number")
	}
}

func TestGetPhoneNumberTaskNormalizesRepeatedGroupedDigits(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five double twenty oh five six seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want repeated grouped phone number accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5552005200567" {
			t.Fatalf("PhoneNumber = %q, want repeated grouped phone number normalized", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after repeated grouped phone number")
	}
}

func TestGetPhoneNumberTaskNormalizesRepeatedHundredGroup(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five double one hundred tree one two"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want repeated hundred-group phone number accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "55510310312" {
			t.Fatalf("PhoneNumber = %q, want repeated hundred group normalized", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after repeated hundred-group phone number")
	}
}

func TestGetPhoneNumberTaskNormalizesRepeatedHundredTensGroup(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five double one hundred twenty three one two"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want repeated hundred-tens phone number accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "55512312312" {
			t.Fatalf("PhoneNumber = %q, want repeated hundred-tens group normalized", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after repeated hundred-tens phone number")
	}
}

func TestGetPhoneNumberTaskStopsAtSpokenExtensionLabel(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one two three four five six seven extension eight nine"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken extension label ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want extension digits ignored", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after phone number with spoken extension")
	}
}

func TestGetPhoneNumberTaskStopsAtExtensionXLabel(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five five five one two three four five six seven x eight nine"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want x extension label ignored", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want x-extension digits ignored", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after phone number with x extension")
	}
}

func TestGetPhoneNumberTaskNormalizesSpokenTeenGroups(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmationSet: true})
	tool := &updatePhoneNumberTool{task: task}

	out, err := tool.Execute(context.Background(), `{"phone_number":"five fifteen one twenty three forty five sixty seven"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken teen-group phone number accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5151234567" {
			t.Fatalf("PhoneNumber = %q, want spoken teen-group phone number normalized", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete after spoken teen-group phone number")
	}
}

func TestGetPhoneNumberTaskOptionsSeedReferenceChatContext(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.ChatMessage{
		ID:      "prior-phone",
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "My phone number is five five five one two three four five six seven."}},
	})
	opts := GetPhoneNumberOptions{}
	field := reflect.ValueOf(&opts).Elem().FieldByName("ChatContext")
	if !field.IsValid() {
		t.Fatal("GetPhoneNumberOptions.ChatContext missing; want reference chat_ctx constructor option")
	}
	field.Set(reflect.ValueOf(chatCtx))

	task := NewGetPhoneNumberTask(opts)

	if task.ChatCtx == nil {
		t.Fatal("task ChatCtx = nil, want constructor chat context copy")
	}
	if task.ChatCtx == chatCtx {
		t.Fatal("task ChatCtx aliases constructor context, want reference-style copy")
	}
	if task.ChatCtx.GetByID("prior-phone") == nil {
		t.Fatalf("task ChatCtx items = %#v, want constructor chat item", task.ChatCtx.Items)
	}
}

func TestGetPhoneNumberTaskPreservesReferenceVoiceAgentOptions(t *testing.T) {
	mode := agent.TurnDetectionModeManual
	allowInterruptions := false
	opts := GetPhoneNumberOptions{
		AgentOptions: AgentOptions{
			TurnDetection:      &mode,
			AllowInterruptions: &allowInterruptions,
		},
	}
	if field := reflect.ValueOf(&opts).Elem().FieldByName("AgentOptions"); !field.IsValid() {
		t.Fatal("GetPhoneNumberOptions.AgentOptions missing; want reference voice agent constructor options")
	}

	task := NewGetPhoneNumberTask(opts)
	if task.Agent.TurnDetection != agent.TurnDetectionModeManual {
		t.Fatalf("TurnDetection = %q, want %q", task.Agent.TurnDetection, agent.TurnDetectionModeManual)
	}
	if !task.Agent.AllowInterruptionsSet {
		t.Fatal("AllowInterruptionsSet = false, want explicit reference allow_interruptions option preserved")
	}
	if task.Agent.AllowInterruptions {
		t.Fatal("AllowInterruptions = true, want false")
	}
}

func TestGetPhoneNumberTaskPreservesReferenceExtraTools(t *testing.T) {
	opts := GetPhoneNumberOptions{}
	field := reflect.ValueOf(&opts).Elem().FieldByName("Tools")
	if !field.IsValid() {
		t.Fatal("GetPhoneNumberOptions.Tools missing; want reference tools constructor option")
	}
	field.Set(reflect.ValueOf([]llm.Tool{referencePhoneExtraTool{id: "phone_help"}}))

	task := NewGetPhoneNumberTask(opts)

	if len(task.Agent.Tools) < 2 {
		t.Fatalf("tools = %#v, want extra tool then update/decline tools", task.Agent.Tools)
	}
	if got := task.Agent.Tools[0].Name(); got != "phone_help" {
		t.Fatalf("first tool = %q, want reference extra tool before update tool", got)
	}
	if got := task.Agent.Tools[1].Name(); got != "update_phone_number" {
		t.Fatalf("second tool = %q, want update_phone_number after extra tools", got)
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
	confirmOut, err := confirm.Execute(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("confirm Execute() error = %v", err)
	}
	if confirmOut != "" {
		t.Fatalf("confirm Execute() output = %q, want empty output after completion", confirmOut)
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

func TestGetPhoneNumberTaskUpdateOutputAvoidsDigitReadbackForConfirmation(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{})
	update := &updatePhoneNumberTool{task: task}

	out, err := update.Execute(context.Background(), `{"phone_number":"+1 555 123 4567"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if strings.Contains(out, "+15551234567") {
		t.Fatalf("update Execute() output = %q, want no raw contiguous phone number", out)
	}
	if strings.Contains(out, "+1 555 123 4567") || strings.Contains(out, "555 123 4567") {
		t.Fatalf("update Execute() output = %q, want no grouped phone number", out)
	}
	want := "The phone number has been updated.\nAsk the user to confirm the updated phone number without repeating it back.\nPrompt the user for confirmation, do not call `confirm_phone_number` directly"
	if out != want {
		t.Fatalf("update Execute() output = %q, want %q", out, want)
	}
}

func TestGetPhoneNumberTaskCanDisableDefaultConfirmation(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmation: false, RequireConfirmationSet: true})
	update := &updatePhoneNumberTool{task: task}

	out, err := update.Execute(context.Background(), `{"phone_number":"(555) 123-4567"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("update Execute() output = %q, want empty output after no-confirm completion", out)
	}
}

func TestGetPhoneNumberTaskDefaultConfirmationUsesInputModality(t *testing.T) {
	textCtx := agent.WithRunContext(
		context.Background(),
		agent.NewRunContext(nil, agent.NewSpeechHandle(true, agent.InputDetails{Modality: "text"}), nil),
	)
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{})
	update := &updatePhoneNumberTool{task: task}

	out, err := update.Execute(textCtx, `{"phone_number":"(555) 123-4567"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("update Execute() output = %q, want direct text completion without confirmation prompt", out)
	}

	select {
	case result := <-task.Result:
		if result.PhoneNumber != "5551234567" {
			t.Fatalf("PhoneNumber = %q, want direct text completion", result.PhoneNumber)
		}
	default:
		t.Fatal("task did not complete for text input")
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
		"five five five, one two three, four five six seven",
		"area code 555, 123 4567",
		"Convert spoken digits to their numeric form: 'five' → 5, 'zero' → 0, 'oh' → 0.",
		"Recognize 'area code' as a prefix for the area code digits.",
		"Call `update_phone_number` at the first opportunity whenever you form a new hypothesis about the phone number. (before asking any questions or providing any answers.)",
		"Ask the user to confirm the updated phone number without repeating it back.",
		"Avoid verbosity by not sharing example phone numbers or formats unless prompted to do so. Do not deviate from the goal of collecting the user's phone number.",
		"Always explicitly invoke a tool when applicable. Do not simulate tool usage, no real action is taken unless the tool is explicitly called.",
	} {
		if !strings.Contains(task.Instructions, want) {
			t.Fatalf("Instructions = %q, want reference guidance %q", task.Instructions, want)
		}
	}
	if strings.Contains(task.Instructions, "Read it back in groups.") {
		t.Fatalf("Instructions = %q, want no phone readback guidance", task.Instructions)
	}
}

func TestGetPhoneNumberTaskInstructionsPreserveReferenceModalityVariants(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{})

	if task.InstructionVariants == nil {
		t.Fatal("InstructionVariants = nil, want reference audio/text instruction variants")
	}
	audio := task.InstructionVariants.AsModality("audio").String()
	text := task.InstructionVariants.AsModality("text").String()

	for _, want := range []string{
		"Handle input as noisy voice transcription.",
		"five five five, one two three, four five six seven",
		"Recognize 'plus' at the start as the international prefix `+`.",
		"Call `confirm_phone_number` after the user confirmed the phone number is correct.",
	} {
		if !strings.Contains(audio, want) {
			t.Fatalf("audio instructions = %q, want reference audio guidance %q", audio, want)
		}
	}
	for _, want := range []string{
		"Handle input as typed text. Expect users to type their phone number directly.",
		"Strip dashes, spaces, parentheses, and dots from the number.",
		"If the number looks almost correct but has minor formatting issues, clean it up silently.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("text instructions = %q, want reference text guidance %q", text, want)
		}
	}
	for _, stale := range []string{
		"Handle input as noisy voice transcription.",
		"five five five, one two three",
		"Call `confirm_phone_number` after the user confirmed the phone number is correct.",
	} {
		if strings.Contains(text, stale) {
			t.Fatalf("text instructions = %q, want no audio/default-confirmation guidance %q", text, stale)
		}
	}
}

func TestGetPhoneNumberTaskInstructionsOmitConfirmationWhenDisabled(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireConfirmation: false, RequireConfirmationSet: true})

	if strings.Contains(task.Instructions, "confirm_phone_number") {
		t.Fatalf("Instructions = %q, want no confirm_phone_number guidance when confirmation disabled", task.Instructions)
	}
}

func TestGetPhoneNumberTaskOnEnterUsesReferencePrompt(t *testing.T) {
	const want = "Ask the user to provide their phone number."

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
		if got := ev.SpeechHandle.Generation.Instructions.AsModality("text").String(); got != want {
			t.Fatalf("on-enter instructions = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for phone on-enter prompt")
	}
}

func TestGetPhoneNumberTaskUpdateToolUsesReferenceSchema(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{})
	tool := task.Agent.Tools[0]

	wantDescription := "Update the phone number provided by the user."
	if got := tool.Description(); got != wantDescription {
		t.Fatalf("update_phone_number description = %q, want %q", got, wantDescription)
	}
	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", tool.Parameters()["properties"])
	}
	phone, ok := properties["phone_number"].(map[string]any)
	if !ok {
		t.Fatalf("phone_number schema = %#v, want map", properties["phone_number"])
	}
	wantParam := "The phone number provided by the user, digits only with optional leading +"
	if got := phone["description"]; got != wantParam {
		t.Fatalf("phone_number description = %#v, want %q", got, wantParam)
	}
}

func TestGetPhoneNumberTaskExplicitAskIgnoresUpdateToolOnEnter(t *testing.T) {
	task := NewGetPhoneNumberTask(GetPhoneNumberOptions{RequireExplicitAsk: true})
	tool := task.Agent.Tools[0]

	if !llm.ToolHasFlag(tool, llm.ToolFlagIgnoreOnEnter) {
		t.Fatalf("%s ToolFlags missing ToolFlagIgnoreOnEnter when RequireExplicitAsk is true", tool.Name())
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

	out, err := tool.Execute(context.Background(), `{"reason":"user refused"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after decline", out)
	}
	_, err = task.WaitAny(context.Background())
	if err == nil || err.Error() != "couldn't get the phone number: user refused" {
		t.Fatalf("WaitAny() error = %v, want decline reason", err)
	}
}

func TestDeclinePhoneNumberCaptureToolUsesRunContextCurrentAgent(t *testing.T) {
	staleTask := NewGetPhoneNumberTask(GetPhoneNumberOptions{})
	currentTask := NewGetPhoneNumberTask(GetPhoneNumberOptions{})
	session := agent.NewAgentSession(currentTask, nil, agent.AgentSessionOptions{})
	ctx := agent.WithRunContext(context.Background(), agent.NewRunContext(session, nil, nil))
	tool := &declinePhoneNumberCaptureTool{task: staleTask}

	out, err := tool.Execute(ctx, `{"reason":"user refused current phone"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after decline", out)
	}

	select {
	case err := <-currentTask.Err:
		if err == nil || err.Error() != "couldn't get the phone number: user refused current phone" {
			t.Fatalf("current task error = %v, want decline reason", err)
		}
	case <-time.After(time.Second):
		t.Fatal("current task did not fail after decline_phone_number_capture")
	}

	select {
	case err := <-staleTask.Err:
		t.Fatalf("stale task failed with %v, want decline routed to current agent", err)
	default:
	}
}

func TestDeclinePhoneNumberCaptureToolDoesNotFailDifferentCurrentAgent(t *testing.T) {
	staleTask := NewGetPhoneNumberTask(GetPhoneNumberOptions{})
	currentTask := NewGetEmailTask(GetEmailOptions{})
	session := agent.NewAgentSession(currentTask, nil, agent.AgentSessionOptions{})
	ctx := agent.WithRunContext(context.Background(), agent.NewRunContext(session, nil, nil))
	tool := &declinePhoneNumberCaptureTool{task: staleTask}

	out, err := tool.Execute(ctx, `{"reason":"late stale phone decline"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after decline", out)
	}

	select {
	case err := <-currentTask.Err:
		t.Fatalf("current email task failed with %v, want stale phone decline ignored for different current agent", err)
	default:
	}

	select {
	case err := <-staleTask.Err:
		if err == nil || err.Error() != "couldn't get the phone number: late stale phone decline" {
			t.Fatalf("stale phone task error = %v, want phone decline reason", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stale phone task did not fail after decline_phone_number_capture")
	}
}

func TestDeclinePhoneNumberCaptureToolUsesReferenceSchema(t *testing.T) {
	tool := &declinePhoneNumberCaptureTool{task: NewGetPhoneNumberTask(GetPhoneNumberOptions{})}

	wantDescription := "Handles the case when the user explicitly declines to provide a phone number."
	if got := tool.Description(); got != wantDescription {
		t.Fatalf("decline_phone_number_capture description = %q, want %q", got, wantDescription)
	}
	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", tool.Parameters()["properties"])
	}
	reason, ok := properties["reason"].(map[string]any)
	if !ok {
		t.Fatalf("reason schema = %#v, want map", properties["reason"])
	}
	wantParam := "A short explanation of why the user declined to provide the phone number"
	if got := reason["description"]; got != wantParam {
		t.Fatalf("reason description = %#v, want %q", got, wantParam)
	}
}

type referencePhoneExtraTool struct {
	id string
}

func (t referencePhoneExtraTool) ID() string          { return t.id }
func (t referencePhoneExtraTool) Name() string        { return t.id }
func (t referencePhoneExtraTool) Description() string { return "reference extra phone tool" }
func (t referencePhoneExtraTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t referencePhoneExtraTool) Execute(ctx context.Context, args string) (string, error) {
	return "", nil
}
