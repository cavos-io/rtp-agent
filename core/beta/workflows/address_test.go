package workflows

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/beta"
	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestGetAddressTaskRecordsAddressWithoutConfirmation(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"123 Main St","unit_number":"","locality":"Springfield IL 62701","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// Reference returns None after no-confirm completion, avoiding extra post-completion tool chatter.
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if result.Address != "123 Main St Springfield IL 62701 United States" {
			t.Fatalf("Address = %q, want normalized joined address", result.Address)
		}
	default:
		t.Fatal("task did not complete after address update")
	}
}

func TestGetAddressTaskNormalizesSpokenDigits(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street","unit_number":"Apartment four","locality":"Springfield IL six two seven zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street Apartment 4 Springfield IL 62701 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken digits normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken-digit address")
	}
}

func TestGetAddressTaskNormalizesNoisySTTDigitHomophones(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"for to ate Main Street","unit_number":"Apartment too","locality":"Springfield IL for to ate zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want noisy STT digit homophones accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "428 Main Street Apartment 2 Springfield IL 42801 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want noisy STT digit homophones normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after noisy STT address")
	}
}

func TestGetAddressTaskNormalizesForeHomophone(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"fore Main Street","unit_number":"Apartment fore","locality":"Springfield IL fore zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want fore homophone accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "4 Main Street Apartment 4 Springfield IL 401 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want fore homophone normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after fore-homophone address")
	}
}

func TestGetAddressTaskNormalizesThreeHomophones(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"tree free Main Street","unit_number":"Apartment tree","locality":"Springfield IL tree free zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want three homophones accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "33 Main Street Apartment 3 Springfield IL 3301 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want three homophones normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after three-homophone address")
	}
}

func TestGetAddressTaskNormalizesSixHomophone(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"sex Main Street","unit_number":"Apartment sex","locality":"Springfield IL sex zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want six homophone accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "6 Main Street Apartment 6 Springfield IL 601 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want six homophone normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after six-homophone address")
	}
}

func TestGetAddressTaskNormalizesNinerDigit(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"niner Main Street","unit_number":"","locality":"Springfield IL niner zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want niner spoken address digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "9 Main Street Springfield IL 901 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want niner normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after niner address")
	}
}

func TestGetAddressTaskNormalizesAughtDigit(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"aught Main Street","unit_number":"","locality":"Springfield IL aught zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want aught spoken address digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "0 Main Street Springfield IL 001 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want aught normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after aught address")
	}
}

func TestGetAddressTaskNormalizesNaughtDigit(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"naught Main Street","unit_number":"","locality":"Springfield IL naught zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want naught spoken address digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "0 Main Street Springfield IL 001 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want naught normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after naught address")
	}
}

func TestGetAddressTaskNormalizesNoughtDigit(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"nought Main Street","unit_number":"","locality":"Springfield IL nought zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want nought spoken address digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "0 Main Street Springfield IL 001 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want nought normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after nought address")
	}
}

func TestGetAddressTaskNormalizesOughtDigit(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"ought Main Street","unit_number":"","locality":"Springfield IL ought zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want ought spoken address digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "0 Main Street Springfield IL 001 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want ought normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after ought address")
	}
}

func TestGetAddressTaskNormalizesOweDigit(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"owe Main Street","unit_number":"","locality":"Springfield IL owe zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want owe spoken address digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "0 Main Street Springfield IL 001 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want owe normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after owe address")
	}
}

func TestGetAddressTaskPreservesLetterOInAddressNames(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"O apostrophe Connor Street","unit_number":"","locality":"Ottawa ON K one A zero B one","country":"Canada"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want letter O address accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "O'Connor Street Ottawa ON K1A 0B1 Canada"
		if result.Address != want {
			t.Fatalf("Address = %q, want letter O preserved in %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after letter-O address")
	}
}

func TestGetAddressTaskNormalizesCompoundSpokenNumbers(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one twenty three Main Street","unit_number":"","locality":"Springfield IL six two seven zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street Springfield IL 62701 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want compound spoken number normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after compound-spoken-number address")
	}
}

func TestGetAddressTaskNormalizesTwentyOhSpokenNumbers(t *testing.T) {
	tests := []struct {
		name   string
		street string
		zip    string
		want   string
	}{
		{
			name:   "twenty oh",
			street: "twenty oh five Main Street",
			zip:    "twenty oh five zero",
			want:   "2005 Main Street Springfield IL 20050 United States",
		},
		{
			name:   "thirty oh",
			street: "thirty oh seven Main Street",
			zip:    "thirty oh seven zero",
			want:   "3007 Main Street Springfield IL 30070 United States",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
			tool := &updateAddressTool{task: task}

			out, err := tool.Execute(context.Background(), `{"street_address":"`+tt.street+`","unit_number":"","locality":"Springfield IL `+tt.zip+`","country":"United States"}`)
			if err != nil {
				t.Fatalf("Execute() error = %v, want tens-oh spoken address numbers accepted", err)
			}
			if out != "" {
				t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
			}

			select {
			case result := <-task.Result:
				if result.Address != tt.want {
					t.Fatalf("Address = %q, want tens-oh spoken numbers normalized to %q", result.Address, tt.want)
				}
			default:
				t.Fatal("task did not complete after tens-oh address")
			}
		})
	}
}

func TestGetAddressTaskNormalizesTwentyOhSpokenNumbersAcrossFiller(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"twenty uh oh five Main Street","unit_number":"","locality":"Springfield IL twenty um oh five zero","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want filler inside tens-oh address numbers accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "2005 Main Street Springfield IL 20050 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want filler inside tens-oh numbers normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after tens-oh address with filler")
	}
}

func TestGetAddressTaskNormalizesTwentyAughtSpokenNumbers(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"twenty aught five Main Street","unit_number":"","locality":"Springfield IL twenty aught five zero","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want twenty-aught spoken address numbers accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "2005 Main Street Springfield IL 20050 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want twenty-aught spoken numbers normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after twenty-aught address")
	}
}

func TestGetAddressTaskNormalizesTwentyNaughtSpokenNumbers(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"twenty naught five Main Street","unit_number":"","locality":"Springfield IL twenty naught five zero","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want twenty-naught spoken address numbers accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "2005 Main Street Springfield IL 20050 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want twenty-naught spoken numbers normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after twenty-naught address")
	}
}

func TestGetAddressTaskNormalizesRepeatedSpokenNumbers(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"double five Main Street","unit_number":"apartment double two","locality":"Springfield IL nine double zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want repeated spoken address numbers accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "55 Main Street apartment 22 Springfield IL 9001 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want repeated spoken numbers normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after repeated spoken address numbers")
	}
}

func TestGetAddressTaskNormalizesSingleSpokenNumbers(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"single five Main Street","unit_number":"apartment single two","locality":"Springfield IL nine single zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want single spoken address numbers accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "5 Main Street apartment 2 Springfield IL 901 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want single spoken numbers normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after single spoken address numbers")
	}
}

func TestGetAddressTaskNormalizesWonHomophone(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"won Main Street","unit_number":"apartment won","locality":"Springfield IL won zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want won homophone accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "1 Main Street apartment 1 Springfield IL 101 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want won homophone normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after won-homophone address")
	}
}

func TestGetAddressTaskNormalizesQuadrupleSpokenNumbers(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"quadruple five Main Street","unit_number":"apartment quadruple two","locality":"Springfield IL quadruple zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want quadruple spoken address numbers accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "5555 Main Street apartment 2222 Springfield IL 00001 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want quadruple spoken numbers normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after quadruple spoken address numbers")
	}
}

func TestGetAddressTaskNormalizesSpokenHundredNumbers(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one hundred twenty three Main Street","unit_number":"","locality":"Springfield IL six two seven zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street Springfield IL 62701 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken hundred number normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken-hundred-number address")
	}
}

func TestGetAddressTaskNormalizesSpokenHundredSingleDigitTail(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one hundred tree Main Street","unit_number":"suite one hundred three","locality":"Springfield IL one hundred free zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken hundred single-digit tail accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "103 Main Street suite 103 Springfield IL 10301 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken hundred single-digit tail normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken-hundred-single-digit-tail address")
	}
}

func TestGetAddressTaskNormalizesSpokenHundredNaughtTail(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one hundred naught five Main Street","unit_number":"suite one hundred naught five","locality":"Springfield IL one hundred naught five zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken hundred naught tail accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "105 Main Street suite 105 Springfield IL 10501 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken hundred naught tail normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken-hundred-naught-tail address")
	}
}

func TestGetAddressTaskNormalizesSpokenAlphanumericPostalCode(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Maple Street","unit_number":"apartment c four","locality":"Ottawa ON k one a zero b one","country":"Canada"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken alphanumeric postal code accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Maple Street apartment c4 Ottawa ON k1a 0b1 Canada"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken alphanumeric postal code normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken alphanumeric postal code")
	}
}

func TestGetAddressTaskNormalizesSpokenPostalLetterAliases(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Maple Street","unit_number":"apartment see four","locality":"Ottawa ON k one ay zero bee one","country":"Canada"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want STT letter aliases accepted in postal/unit codes", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Maple Street apartment c4 Ottawa ON k1a 0b1 Canada"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken postal letter aliases normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken postal letter aliases")
	}
}

func TestGetAddressTaskNormalizesDoubleYouPostalLetterAlias(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Maple Street","unit_number":"unit double you five","locality":"Toronto ON double you one a one a one","country":"Canada"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken double-you postal/unit letter accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Maple Street unit w5 Toronto ON w1a 1a1 Canada"
		if result.Address != want {
			t.Fatalf("Address = %q, want double-you letter alias normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after double-you postal/unit letter alias")
	}
}

func TestGetAddressTaskNormalizesUnitLetterAlias(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Maple Street","unit_number":"apartment bee","locality":"Ottawa ON k one a zero b one","country":"Canada"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken unit letter alias accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Maple Street apartment b Ottawa ON k1a 0b1 Canada"
		if result.Address != want {
			t.Fatalf("Address = %q, want unit letter alias normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after unit letter alias")
	}
}

func TestGetAddressTaskNormalizesUnitLabelLetterAlias(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Maple Street","unit_number":"unit bee","locality":"Ottawa ON k one a zero b one","country":"Canada"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken unit label letter alias accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Maple Street unit b Ottawa ON k1a 0b1 Canada"
		if result.Address != want {
			t.Fatalf("Address = %q, want unit label letter alias normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after unit label letter alias")
	}
}

func TestGetAddressTaskNormalizesSpokenOrdinalNumbers(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street","unit_number":"fourth floor","locality":"Springfield IL six two seven zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street 4th floor Springfield IL 62701 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken ordinal normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken-ordinal address")
	}
}

func TestGetAddressTaskNormalizesSpokenTeenOrdinalNumbers(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street","unit_number":"eleventh floor","locality":"Springfield IL six two seven zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street 11th floor Springfield IL 62701 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken teen ordinal normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken teen-ordinal address")
	}
}

func TestGetAddressTaskNormalizesSpokenCompoundOrdinalNumbers(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street","unit_number":"twenty first floor","locality":"Springfield IL six two seven zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street 21st floor Springfield IL 62701 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken compound ordinal normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken compound-ordinal address")
	}
}

func TestGetAddressTaskNormalizesSpokenHundredOrdinalNumbers(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one hundred and twenty third Street","unit_number":"","locality":"Springfield IL six two seven zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken hundred ordinal address accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123rd Street Springfield IL 62701 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken hundred ordinal normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken hundred-ordinal address")
	}
}

func TestGetAddressTaskNormalizesSpokenSpelling(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"guomao g u o m a o road","unit_number":"","locality":"beijing b e i j i n g","country":"China"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken spelling accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "guomao road beijing China"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken spelling normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken-spelling address")
	}
}

func TestGetAddressTaskNormalizesSpokenSpellingLetterAliases(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"main em ay eye en street","unit_number":"","locality":"dallas dee ay double el ay ess tx seven five two zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want letter-name spelling aliases accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "main street dallas tx 75201 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want letter-name spelling aliases normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken-spelling letter aliases")
	}
}

func TestGetAddressTaskNormalizesSpokenOhOweLetterAliases(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"guomao gee you oh em ay owe road","unit_number":"","locality":"beijing","country":"China"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want oh/owe spelling aliases accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "guomao road beijing China"
		if result.Address != want {
			t.Fatalf("Address = %q, want oh/owe spelling aliases normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after oh/owe spelling address")
	}
}

func TestGetAddressTaskNormalizesSpokenSpellingPreamble(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"street address is spelled main m a i n street","unit_number":"","locality":"city is spelled dallas d a l l a s tx seven five two zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken spelling preamble accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "main street dallas tx 75201 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken spelling preamble normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken-spelling preamble address")
	}
}

func TestGetAddressTaskFiltersStreetFieldPreamble(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"street is one two three Main Street","unit_number":"","locality":"Springfield IL six two seven zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want street field preamble accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street Springfield IL 62701 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want street field preamble normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after street field preamble address")
	}
}

func TestGetAddressTaskNormalizesSpokenCompoundOrdinalAcrossFiller(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street","unit_number":"twenty uh first floor","locality":"Springfield IL six two seven zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street 21st floor Springfield IL 62701 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want filler inside compound ordinal normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after compound ordinal with filler")
	}
}

func TestGetAddressTaskNormalizesSpokenDoubleLetterSpelling(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"willow w i double l o w road","unit_number":"","locality":"dallas d a double l a s tx seven five two zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want repeated-letter spelling accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "willow road dallas tx 75201 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want repeated-letter spelling normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after repeated-letter spelling address")
	}
}

func TestGetAddressTaskNormalizesSpokenDoubleYouLetterSpelling(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"willow double you i double l o w road","unit_number":"","locality":"dallas d a double l a s tx seven five two zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken W spelling accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "willow road dallas tx 75201 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken W spelling normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken-W spelling address")
	}
}

func TestGetAddressTaskNormalizesSpokenDoubleEweLetterSpelling(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"willow double ewe i double l o w road","unit_number":"","locality":"dallas d a double l a s tx seven five two zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken double-ewe W spelling accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "willow road dallas tx 75201 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want double-ewe W spelling normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after double-ewe spelling address")
	}
}

func TestGetAddressTaskNormalizesSpokenSingleLetterSpelling(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"ada a single d a road","unit_number":"","locality":"dallas d a double l a s tx seven five two zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want single-letter spelling accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "ada road dallas tx 75201 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want single-letter spelling normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after single-letter spelling address")
	}
}

func TestGetAddressTaskNormalizesSpokenDoubleLetterSpellingAcrossFiller(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"willow w i double uh l o w road","unit_number":"","locality":"dallas d a double um l a s tx seven five two zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want repeated-letter spelling across filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "willow road dallas tx 75201 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want filler after double ignored in spelled address", result.Address)
		}
	default:
		t.Fatal("task did not complete after repeated-letter spelling with filler")
	}
}

func TestGetAddressTaskNormalizesSpokenSymbols(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"smith dash jones road","unit_number":"","locality":"d apostrophe iberville","country":"Canada"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken symbols accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "smith-jones road d'iberville Canada"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken symbols normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken-symbol address")
	}
}

func TestGetAddressTaskNormalizesSpokenMinusHyphen(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	_, err := tool.Execute(context.Background(), `{"street_address":"smith minus jones road","unit_number":"suite a minus one","locality":"d apostrophe iberville","country":"Canada"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken minus accepted as hyphen", err)
	}

	select {
	case result := <-task.Result:
		want := "smith-jones road suite a-1 d'iberville Canada"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken minus normalized as hyphen to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken-minus address")
	}
}

func TestGetAddressTaskNormalizesSplitHyphen(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	_, err := tool.Execute(context.Background(), `{"street_address":"smith hy phen jones road","unit_number":"suite a hy phen one","locality":"d apostrophe iberville","country":"Canada"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split spoken hyphen accepted", err)
	}

	select {
	case result := <-task.Result:
		want := "smith-jones road suite a-1 d'iberville Canada"
		if result.Address != want {
			t.Fatalf("Address = %q, want split spoken hyphen normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after split spoken hyphen address")
	}
}

func TestGetAddressTaskNormalizesSpokenSingleQuote(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"smith dash jones road","unit_number":"","locality":"d single quote iberville","country":"Canada"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken single quote accepted as apostrophe", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "smith-jones road d'iberville Canada"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken single quote normalized as apostrophe to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken-single-quote address")
	}
}

func TestGetAddressTaskNormalizesSpokenQuote(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"smith dash jones road","unit_number":"","locality":"d quote iberville","country":"Canada"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken quote accepted as apostrophe", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "smith-jones road d'iberville Canada"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken quote normalized as apostrophe to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken-quote address")
	}
}

func TestGetAddressTaskNormalizesSpokenSlash(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street","unit_number":"apartment five slash b","locality":"Ottawa ON k one a slash zero b one","country":"Canada"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken slash accepted in address fields", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 5/b Ottawa ON k1a/0b1 Canada"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken slash normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken slash address")
	}
}

func TestGetAddressTaskNormalizesPunctuatedSpokenSymbols(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	_, err := tool.Execute(context.Background(), `{"street_address":"smith dash, jones road","unit_number":"","locality":"d apostrophe. iberville","country":"Canada"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want punctuated spoken symbols accepted", err)
	}

	select {
	case result := <-task.Result:
		want := "smith-jones road d'iberville Canada"
		if result.Address != want {
			t.Fatalf("Address = %q, want punctuated spoken symbols normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after punctuated spoken-symbol address")
	}
}

func TestGetAddressTaskNormalizesSpokenSymbolSuffixes(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	_, err := tool.Execute(context.Background(), `{"street_address":"smith dash symbol jones road","unit_number":"","locality":"d apostrophe symbol iberville","country":"Canada"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken symbol suffixes accepted", err)
	}

	select {
	case result := <-task.Result:
		want := "smith-jones road d'iberville Canada"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken symbol suffixes normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken symbol suffix address")
	}
}

func TestGetAddressTaskNormalizesSpokenSignSuffixes(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	_, err := tool.Execute(context.Background(), `{"street_address":"smith dash sign jones road","unit_number":"","locality":"d apostrophe sign iberville","country":"Canada"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken sign suffixes accepted", err)
	}

	select {
	case result := <-task.Result:
		want := "smith-jones road d'iberville Canada"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken sign suffixes normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken sign suffix address")
	}
}

func TestGetAddressTaskNormalizesSpokenKeySuffixes(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	_, err := tool.Execute(context.Background(), `{"street_address":"smith dash key jones road","unit_number":"","locality":"d apostrophe key iberville","country":"Canada"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken key suffixes accepted", err)
	}

	select {
	case result := <-task.Result:
		want := "smith-jones road d'iberville Canada"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken key suffixes normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken key suffix address")
	}
}

func TestGetAddressTaskNormalizesSpokenMarkSuffixes(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	_, err := tool.Execute(context.Background(), `{"street_address":"smith dash mark jones road","unit_number":"","locality":"d apostrophe mark iberville","country":"Canada"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken mark suffixes accepted", err)
	}

	select {
	case result := <-task.Result:
		want := "smith-jones road d'iberville Canada"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken mark suffixes normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken mark suffix address")
	}
}

func TestGetAddressTaskNormalizesSpokenNumberSignUnit(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street","unit_number":"number sign four","locality":"Springfield IL six two seven zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken number-sign unit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street #4 Springfield IL 62701 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken number-sign unit normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken number-sign unit")
	}
}

func TestGetAddressTaskNormalizesSpokenHashSignUnit(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street","unit_number":"hash sign four","locality":"Springfield IL six two seven zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken hash-sign unit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street #4 Springfield IL 62701 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken hash-sign unit normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken hash-sign unit")
	}
}

func TestGetAddressTaskNormalizesSpokenNumberKeyUnit(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street","unit_number":"number key four","locality":"Springfield IL six two seven zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken number-key unit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street #4 Springfield IL 62701 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken number-key unit normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken number-key unit")
	}
}

func TestGetAddressTaskNormalizesSpokenPoundUnit(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street","unit_number":"pound four","locality":"Springfield IL six two seven zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken pound unit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street #4 Springfield IL 62701 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken pound unit normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken pound unit")
	}
}

func TestGetAddressTaskNormalizesSpokenHashtagUnit(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street","unit_number":"hashtag four","locality":"Springfield IL six two seven zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken hashtag unit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street #4 Springfield IL 62701 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken hashtag unit normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken hashtag unit")
	}
}

func TestGetAddressTaskNormalizesSpokenOctothorpeUnit(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street","unit_number":"octothorpe four","locality":"Springfield IL six two seven zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken octothorpe unit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street #4 Springfield IL 62701 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken octothorpe unit normalized to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after spoken octothorpe unit")
	}
}

func TestGetAddressTaskFiltersSpokenFiller(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"um one two three Main Street","unit_number":"uh apartment four","locality":"Springfield Illinois","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want filler words filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after filler-spoken address")
	}
}

func TestGetAddressTaskFiltersLikeSpokenFiller(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"like one two three Main Street","unit_number":"like apartment four","locality":"like Springfield Illinois","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want like filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want like filler words filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after like-filler-spoken address")
	}
}

func TestGetAddressTaskFiltersActuallySpokenFiller(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"actually one two three Main Street","unit_number":"actually apartment four","locality":"actually Springfield Illinois","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want correction filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want correction filler words filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after correction-filler-spoken address")
	}
}

func TestGetAddressTaskFiltersSorrySpokenFiller(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"sorry one two three Main Street","unit_number":"sorry apartment four","locality":"sorry Springfield Illinois","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want apology filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want apology filler words filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after apology-filler-spoken address")
	}
}

func TestGetAddressTaskFiltersTrailingSignoffFiller(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street that's it","unit_number":"apartment four please","locality":"Springfield Illinois that's all","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing sign-off filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want trailing sign-off filler filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after trailing-signoff-spoken address")
	}
}

func TestGetAddressTaskFiltersExpandedTrailingSignoffFiller(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street that is it","unit_number":"apartment four please","locality":"Springfield Illinois that is all","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want expanded trailing sign-off filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want expanded trailing sign-off filler filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after expanded-trailing-signoff-spoken address")
	}
}

func TestGetAddressTaskFiltersThatWillBeAllSignoffFiller(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street that'll be it","unit_number":"apartment four please","locality":"Springfield Illinois that'll be all","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want that'll-be trailing sign-off filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want that'll-be trailing sign-off filler filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after that'll-be trailing-signoff-spoken address")
	}
}

func TestGetAddressTaskFiltersExpandedThatWillBeAllSignoffFiller(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street that will be it","unit_number":"apartment four please","locality":"Springfield Illinois that will be all","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want expanded that-will-be trailing sign-off filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want expanded that-will-be trailing sign-off filler filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after expanded that-will-be trailing-signoff-spoken address")
	}
}

func TestGetAddressTaskFiltersDoneSignoffFiller(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street done","unit_number":"apartment four done","locality":"Springfield Illinois done","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want done sign-off filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want done sign-off filler filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after done-signoff-spoken address")
	}
}

func TestGetAddressTaskFiltersAllDoneSignoffFiller(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street all done","unit_number":"apartment four all done","locality":"Springfield Illinois all done","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want all-done sign-off filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want all-done sign-off filler filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after all-done-signoff-spoken address")
	}
}

func TestGetAddressTaskFiltersTrailingForNowThanksSignoffFiller(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street that's it for now thanks","unit_number":"apartment four please","locality":"Springfield Illinois that's all for now thanks","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-now-thanks sign-off filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want trailing for-now-thanks sign-off filler filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after trailing for-now-thanks sign-off address")
	}
}

func TestGetAddressTaskFiltersTrailingForTodaySignoffFiller(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street for today thanks","unit_number":"apartment four please","locality":"Springfield Illinois for today thanks","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want short trailing for-today sign-off filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want short trailing for-today sign-off filler filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after short trailing for-today sign-off address")
	}
}

func TestGetAddressTaskFiltersTrailingForYouSignoffFiller(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street that's all for you","unit_number":"apartment four please","locality":"Springfield Illinois that's it for you","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-you sign-off filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want trailing for-you sign-off filler filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after trailing for-you sign-off address")
	}
}

func TestGetAddressTaskFiltersTrailingShortForYouSignoffFiller(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street for you thanks","unit_number":"apartment four please","locality":"Springfield Illinois for you thanks","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want short trailing for-you sign-off filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want short trailing for-you sign-off filler filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after short trailing for-you sign-off address")
	}
}

func TestGetAddressTaskFiltersTrailingShortForTheDaySignoffFiller(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street for the day thanks","unit_number":"apartment four please","locality":"Springfield Illinois for the day thanks","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want short trailing for-the-day sign-off filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want short trailing for-the-day sign-off filler filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after short trailing for-the-day sign-off address")
	}
}

func TestGetAddressTaskFiltersTrailingForTodayThanksSignoffFiller(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street that's it for today thanks","unit_number":"apartment four please","locality":"Springfield Illinois that's all for today thanks","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-today-thanks sign-off filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want trailing for-today-thanks sign-off filler filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after trailing for-today-thanks sign-off address")
	}
}

func TestGetAddressTaskFiltersTrailingForTheDayThanksSignoffFiller(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street that's it for the day thanks","unit_number":"apartment four please","locality":"Springfield Illinois that's all for the day thanks","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-the-day-thanks sign-off filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want trailing for-the-day-thanks sign-off filler filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after trailing for-the-day-thanks sign-off address")
	}
}

func TestGetAddressTaskFiltersTrailingExpandedThatWillBeAllForTheDaySignoffFiller(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street that will be all for the day thanks","unit_number":"apartment four please","locality":"Springfield Illinois that will be it for the day thanks","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want expanded that-will-be for-the-day sign-off filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want expanded that-will-be for-the-day sign-off filler filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after expanded that-will-be for-the-day sign-off address")
	}
}

func TestGetAddressTaskFiltersTrailingThatllBeAllForTheDaySignoffFiller(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street that'll be all for the day thanks","unit_number":"apartment four please","locality":"Springfield Illinois that'll be it for the day thanks","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want contracted that'll-be for-the-day sign-off filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want contracted that'll-be for-the-day sign-off filler filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after contracted that'll-be for-the-day sign-off address")
	}
}

func TestGetAddressTaskFiltersTrailingThatllBeAllForDaySignoffFiller(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street that'll be all for day thanks","unit_number":"apartment four please","locality":"Springfield Illinois that'll be it for day thanks","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want contracted that'll-be for-day sign-off filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want contracted that'll-be for-day sign-off filler filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after contracted that'll-be for-day sign-off address")
	}
}

func TestGetAddressTaskFiltersTrailingSplitThatllBeAllForDaySignoffFiller(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street that ll be all for day thanks","unit_number":"apartment four please","locality":"Springfield Illinois that ll be it for day thanks","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split contracted that'll-be for-day sign-off filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want split contracted that'll-be for-day sign-off filler filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after split contracted that'll-be for-day sign-off address")
	}
}

func TestGetAddressTaskFiltersTrailingSplitThatllShortSignoffFiller(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street that ll be all","unit_number":"apartment four please","locality":"Springfield Illinois that ll be it","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split contracted short sign-off filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want split contracted short sign-off filler filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after split contracted short sign-off address")
	}
}

func TestGetAddressTaskFiltersSpokenAddressPreamble(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"my address is one two three Main Street","unit_number":"apartment four","locality":"Springfield Illinois","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken address preamble accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want spoken address preamble filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after preamble-spoken address")
	}
}

func TestGetAddressTaskFiltersArticleAddressPreamble(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	_, err := tool.Execute(context.Background(), `{"street_address":"the address is one two three Main Street","unit_number":"apartment four","locality":"Springfield Illinois","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want article address preamble accepted", err)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want article address preamble filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after article-preamble address")
	}
}

func TestGetAddressTaskFiltersWillBeAddressPreamble(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	_, err := tool.Execute(context.Background(), `{"street_address":"my address will be one two three Main Street","unit_number":"apartment four","locality":"Springfield Illinois","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want will-be address preamble accepted", err)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want will-be address preamble filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after will-be address preamble")
	}
}

func TestGetAddressTaskFiltersLiveAtPreamble(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	_, err := tool.Execute(context.Background(), `{"street_address":"I live at one two three Main Street","unit_number":"apartment four","locality":"Springfield Illinois","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want live-at address preamble accepted", err)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want live-at preamble filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after live-at preamble address")
	}
}

func TestGetAddressTaskFiltersIAmAtPreamble(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	_, err := tool.Execute(context.Background(), `{"street_address":"I am at one two three Main Street","unit_number":"apartment four","locality":"Springfield Illinois","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want i-am-at address preamble accepted", err)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want i-am-at preamble filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after i-am-at preamble address")
	}
}

func TestGetAddressTaskFiltersImAtPreamble(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	_, err := tool.Execute(context.Background(), `{"street_address":"I'm at one two three Main Street","unit_number":"apartment four","locality":"Springfield Illinois","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want im-at address preamble accepted", err)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want im-at preamble filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after im-at preamble address")
	}
}

func TestGetAddressTaskFiltersStreetAddressFieldPreamble(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	_, err := tool.Execute(context.Background(), `{"street_address":"street address is one two three Main Street","unit_number":"apartment four","locality":"Springfield Illinois","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want street-address field preamble accepted", err)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want street-address field preamble filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after street-address field preamble")
	}
}

func TestGetAddressTaskFiltersContractedFieldPreambles(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	_, err := tool.Execute(context.Background(), `{"street_address":"street address's one two three Main Street","unit_number":"apartment's four","locality":"city's Springfield Illinois zip code's six two seven zero one","country":"country's United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want contracted address field preambles accepted", err)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois 62701 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want contracted field preambles filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after contracted address field preambles")
	}
}

func TestGetAddressTaskFiltersSplitContractedFieldPreambles(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	_, err := tool.Execute(context.Background(), `{"street_address":"street address s one two three Main Street","unit_number":"apartment s four","locality":"city s Springfield Illinois zip code s six two seven zero one","country":"country s United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split contracted address field preambles accepted", err)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois 62701 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want split contracted field preambles filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after split contracted address field preambles")
	}
}

func TestGetAddressTaskFiltersUnitNumberFieldPreamble(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	_, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street","unit_number":"unit number is apartment four","locality":"Springfield Illinois","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want unit-number field preamble accepted", err)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want unit-number field preamble filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after unit-number field preamble")
	}
}

func TestGetAddressTaskFiltersUnitTypeNumberPreamble(t *testing.T) {
	for _, tc := range []struct {
		name string
		unit string
		want string
	}{
		{name: "apartment", unit: "apartment number is four", want: "123 Main Street apartment 4 Springfield Illinois United States"},
		{name: "apt", unit: "apt number is four", want: "123 Main Street apt 4 Springfield Illinois United States"},
		{name: "suite", unit: "suite number is four", want: "123 Main Street suite 4 Springfield Illinois United States"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
			tool := &updateAddressTool{task: task}

			_, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street","unit_number":"`+tc.unit+`","locality":"Springfield Illinois","country":"United States"}`)
			if err != nil {
				t.Fatalf("Execute() error = %v, want unit type-number preamble accepted", err)
			}

			select {
			case result := <-task.Result:
				if result.Address != tc.want {
					t.Fatalf("Address = %q, want unit type-number preamble filtered to %q", result.Address, tc.want)
				}
			default:
				t.Fatal("task did not complete after unit type-number preamble")
			}
		})
	}
}

func TestGetAddressTaskFiltersUnitLocationNumberPreamble(t *testing.T) {
	for _, tc := range []struct {
		name string
		unit string
		want string
	}{
		{name: "floor", unit: "floor number is two", want: "123 Main Street floor 2 Springfield Illinois United States"},
		{name: "office", unit: "office number is four oh three", want: "123 Main Street office 403 Springfield Illinois United States"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
			tool := &updateAddressTool{task: task}

			_, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street","unit_number":"`+tc.unit+`","locality":"Springfield Illinois","country":"United States"}`)
			if err != nil {
				t.Fatalf("Execute() error = %v, want unit location-number preamble accepted", err)
			}

			select {
			case result := <-task.Result:
				if result.Address != tc.want {
					t.Fatalf("Address = %q, want unit location-number preamble filtered to %q", result.Address, tc.want)
				}
			default:
				t.Fatal("task did not complete after unit location-number preamble")
			}
		})
	}
}

func TestGetAddressTaskFiltersUnitTypeIsPreamble(t *testing.T) {
	for _, tc := range []struct {
		name string
		unit string
		want string
	}{
		{name: "apartment", unit: "apartment is four", want: "123 Main Street apartment 4 Springfield Illinois United States"},
		{name: "suite", unit: "suite is four", want: "123 Main Street suite 4 Springfield Illinois United States"},
		{name: "floor", unit: "floor is two", want: "123 Main Street floor 2 Springfield Illinois United States"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
			tool := &updateAddressTool{task: task}

			_, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street","unit_number":"`+tc.unit+`","locality":"Springfield Illinois","country":"United States"}`)
			if err != nil {
				t.Fatalf("Execute() error = %v, want unit type-is preamble accepted", err)
			}

			select {
			case result := <-task.Result:
				if result.Address != tc.want {
					t.Fatalf("Address = %q, want unit type-is preamble filtered to %q", result.Address, tc.want)
				}
			default:
				t.Fatal("task did not complete after unit type-is preamble")
			}
		})
	}
}

func TestGetAddressTaskFiltersCityFieldPreamble(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	_, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street","unit_number":"apartment four","locality":"city is Springfield Illinois","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want city field preamble accepted", err)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want city field preamble filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after city field preamble")
	}
}

func TestGetAddressTaskFiltersCountryFieldPreamble(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	_, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street","unit_number":"apartment four","locality":"Springfield Illinois","country":"country is United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want country field preamble accepted", err)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street apartment 4 Springfield Illinois United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want country field preamble filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after country field preamble")
	}
}

func TestGetAddressTaskFiltersZipCodeFieldPreamble(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	_, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street","unit_number":"","locality":"Springfield Illinois zip code is six two seven zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want zip-code field preamble accepted", err)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street Springfield Illinois 62701 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want zip-code field preamble filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after zip-code field preamble")
	}
}

func TestGetAddressTaskFiltersZipFieldPreamble(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	_, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street","unit_number":"","locality":"Springfield Illinois zip is six two seven zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want zip field preamble accepted", err)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street Springfield Illinois 62701 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want zip field preamble filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after zip field preamble")
	}
}

func TestGetAddressTaskFiltersPostalCodeFieldPreamble(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	_, err := tool.Execute(context.Background(), `{"street_address":"one two three Maple Street","unit_number":"","locality":"Ottawa Ontario postal code is k one a zero b one","country":"Canada"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want postal-code field preamble accepted", err)
	}

	select {
	case result := <-task.Result:
		want := "123 Maple Street Ottawa Ontario k1a 0b1 Canada"
		if result.Address != want {
			t.Fatalf("Address = %q, want postal-code field preamble filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after postal-code field preamble")
	}
}

func TestGetAddressTaskFiltersPostcodeFieldPreamble(t *testing.T) {
	cases := []struct {
		name     string
		locality string
	}{
		{name: "postcode", locality: "Ottawa Ontario postcode is k one a zero b one"},
		{name: "post code", locality: "Ottawa Ontario post code is k one a zero b one"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
			tool := &updateAddressTool{task: task}

			_, err := tool.Execute(context.Background(), `{"street_address":"one two three Maple Street","unit_number":"","locality":"`+tc.locality+`","country":"Canada"}`)
			if err != nil {
				t.Fatalf("Execute() error = %v, want postcode field preamble accepted", err)
			}

			select {
			case result := <-task.Result:
				want := "123 Maple Street Ottawa Ontario k1a 0b1 Canada"
				if result.Address != want {
					t.Fatalf("Address = %q, want postcode field preamble filtered to %q", result.Address, want)
				}
			default:
				t.Fatal("task did not complete after postcode field preamble")
			}
		})
	}
}

func TestGetAddressTaskFiltersStateFieldPreamble(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	_, err := tool.Execute(context.Background(), `{"street_address":"one two three Main Street","unit_number":"","locality":"Springfield state is Illinois zip code is six two seven zero one","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want state field preamble accepted", err)
	}

	select {
	case result := <-task.Result:
		want := "123 Main Street Springfield Illinois 62701 United States"
		if result.Address != want {
			t.Fatalf("Address = %q, want state field preamble filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after state field preamble")
	}
}

func TestGetAddressTaskFiltersProvinceFieldPreamble(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	_, err := tool.Execute(context.Background(), `{"street_address":"one two three Maple Street","unit_number":"","locality":"Ottawa province is Ontario postal code is k one a zero b one","country":"Canada"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want province field preamble accepted", err)
	}

	select {
	case result := <-task.Result:
		want := "123 Maple Street Ottawa Ontario k1a 0b1 Canada"
		if result.Address != want {
			t.Fatalf("Address = %q, want province field preamble filtered to %q", result.Address, want)
		}
	default:
		t.Fatal("task did not complete after province field preamble")
	}
}

func TestGetAddressTaskSkipsWhitespaceOnlyUnitNumber(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	_, err := tool.Execute(context.Background(), `{"street_address":"123 Main St","unit_number":"   ","locality":"Springfield IL 62701","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	select {
	case result := <-task.Result:
		if result.Address != "123 Main St Springfield IL 62701 United States" {
			t.Fatalf("Address = %q, want whitespace-only unit omitted", result.Address)
		}
	default:
		t.Fatal("task did not complete after address update")
	}
}

func TestGetAddressTaskOptionsSeedReferenceChatContext(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.ChatMessage{
		ID:      "prior-address",
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "I live at 123 Main Street apartment 4 in Springfield."}},
	})
	opts := GetAddressOptions{}
	field := reflect.ValueOf(&opts).Elem().FieldByName("ChatContext")
	if !field.IsValid() {
		t.Fatal("GetAddressOptions.ChatContext missing; want reference chat_ctx constructor option")
	}
	field.Set(reflect.ValueOf(chatCtx))

	task := NewGetAddressTask(opts)

	if task.ChatCtx == nil {
		t.Fatal("task ChatCtx = nil, want constructor chat context copy")
	}
	if task.ChatCtx == chatCtx {
		t.Fatal("task ChatCtx aliases constructor context, want reference-style copy")
	}
	if task.ChatCtx.GetByID("prior-address") == nil {
		t.Fatalf("task ChatCtx items = %#v, want constructor chat item", task.ChatCtx.Items)
	}
}

func TestGetAddressTaskPreservesReferenceExtraTools(t *testing.T) {
	opts := GetAddressOptions{}
	field := reflect.ValueOf(&opts).Elem().FieldByName("Tools")
	if !field.IsValid() {
		t.Fatal("GetAddressOptions.Tools missing; want reference tools constructor option")
	}
	field.Set(reflect.ValueOf([]llm.Tool{referenceAddressExtraTool{id: "address_help"}}))

	task := NewGetAddressTask(opts)

	if len(task.Agent.Tools) < 2 {
		t.Fatalf("tools = %#v, want extra tool then update/decline tools", task.Agent.Tools)
	}
	if got := task.Agent.Tools[0].Name(); got != "address_help" {
		t.Fatalf("first tool = %q, want reference extra tool before update tool", got)
	}
	if got := task.Agent.Tools[1].Name(); got != "update_address" {
		t.Fatalf("second tool = %q, want update_address after extra tools", got)
	}
}

func TestGetAddressTaskExplicitAskIgnoresUpdateToolOnEnter(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireExplicitAsk: true})
	tool := task.Agent.Tools[0]

	if !llm.ToolHasFlag(tool, llm.ToolFlagIgnoreOnEnter) {
		t.Fatalf("%s ToolFlags missing ToolFlagIgnoreOnEnter when RequireExplicitAsk is true", tool.Name())
	}
}

func TestGetAddressTaskInjectsConfirmToolAfterUpdate(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{})
	if len(task.Agent.Tools) != 2 {
		t.Fatalf("initial tools = %d, want update/decline before address is captured", len(task.Agent.Tools))
	}

	update := &updateAddressTool{task: task}
	out, err := update.Execute(context.Background(), `{"street_address":"123 Main St","unit_number":"Apt 4","locality":"Springfield IL 62701","country":"United States"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if out == "" {
		t.Fatal("update Execute() output is empty, want confirmation guidance")
	}
	if strings.Contains(out, "['123 Main St', 'Apt 4', 'Springfield IL 62701', 'United States']") {
		t.Fatalf("update Execute() output = %q, want no field-by-field address echo", out)
	}
	if strings.Contains(out, "123 Main St Apt 4 Springfield IL 62701 United States") {
		t.Fatalf("update Execute() output = %q, want no raw full-address echo", out)
	}
	want := "The address has been updated.\nAsk the user to confirm the updated address without repeating it back.\nPrompt the user for confirmation, do not call `confirm_address` directly"
	if out != want {
		t.Fatalf("update Execute() output = %q, want %q", out, want)
	}
	if len(task.Agent.Tools) != 3 || task.Agent.Tools[2].Name() != "confirm_address" {
		t.Fatalf("tools = %#v, want confirm_address appended", task.Agent.Tools)
	}

	confirm := &confirmAddressTool{task: task, address: "123 Main St Apt 4 Springfield IL 62701 United States"}
	confirmOut, err := confirm.Execute(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("confirm Execute() error = %v", err)
	}
	if confirmOut != "" {
		t.Fatalf("confirm Execute() output = %q, want empty output after completion", confirmOut)
	}

	select {
	case result := <-task.Result:
		if result.Address != "123 Main St Apt 4 Springfield IL 62701 United States" {
			t.Fatalf("Address = %q, want captured address", result.Address)
		}
	default:
		t.Fatal("task did not complete after confirmation")
	}
}

func TestGetAddressTaskConfirmationGuidanceAvoidsFieldListEcho(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{})
	update := &updateAddressTool{task: task}

	out, err := update.Execute(context.Background(), `{"street_address":"123 Main St","unit_number":"Apt 4","locality":"Springfield IL 62701","country":"United States"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}

	for _, stale := range []string{
		"Repeat the address field by field",
		"123 Main St",
		"Apt 4",
		"Springfield IL 62701",
		"United States",
	} {
		if strings.Contains(out, stale) {
			t.Fatalf("update Execute() output = %q, want no address field echo %q", out, stale)
		}
	}
}

func TestGetAddressTaskCanDisableDefaultConfirmation(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmation: false, RequireConfirmationSet: true})
	update := &updateAddressTool{task: task}

	out, err := update.Execute(context.Background(), `{"street_address":"123 Main St","unit_number":"","locality":"Springfield IL 62701","country":"United States"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("update Execute() output = %q, want empty output after no-confirm completion", out)
	}
}

func TestGetAddressTaskDefaultConfirmationUsesInputModality(t *testing.T) {
	textCtx := agent.WithRunContext(
		context.Background(),
		agent.NewRunContext(nil, agent.NewSpeechHandle(true, agent.InputDetails{Modality: "text"}), nil),
	)
	task := NewGetAddressTask(GetAddressOptions{})
	update := &updateAddressTool{task: task}

	out, err := update.Execute(textCtx, `{"street_address":"123 Main St","unit_number":"","locality":"Springfield IL 62701","country":"United States"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("update Execute() output = %q, want direct text completion without confirmation prompt", out)
	}

	select {
	case result := <-task.Result:
		if result.Address != "123 Main St Springfield IL 62701 United States" {
			t.Fatalf("Address = %q, want direct text completion", result.Address)
		}
	default:
		t.Fatal("task did not complete for text input")
	}
}

func TestGetAddressTaskInstructionsOmitConfirmationWhenDisabled(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmation: false, RequireConfirmationSet: true})

	if strings.Contains(task.Instructions, "confirm_address") {
		t.Fatalf("Instructions = %q, want no confirm_address guidance when confirmation disabled", task.Instructions)
	}
}

func TestConfirmAddressToolUsesReferenceSchema(t *testing.T) {
	tool := &confirmAddressTool{task: NewGetAddressTask(GetAddressOptions{}), address: "123 Main St"}

	wantDescription := "Call after the user confirms the address is correct."
	if got := tool.Description(); got != wantDescription {
		t.Fatalf("confirm_address description = %q, want %q", got, wantDescription)
	}
	if got := tool.Parameters()["type"]; got != "object" {
		t.Fatalf("confirm_address schema type = %#v, want object", got)
	}
	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want empty map", tool.Parameters()["properties"])
	}
	if len(properties) != 0 {
		t.Fatalf("properties = %#v, want empty map", properties)
	}
}

func TestGetAddressTaskInstructionsUseReferenceToolGuidance(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{})

	for _, want := range []string{
		"Call `update_address` at the first opportunity whenever you form a new hypothesis about the address. (before asking any questions or providing any answers.)",
		"Always explicitly invoke a tool when applicable. Do not simulate tool usage, no real action is taken unless the tool is explicitly called.",
	} {
		if !strings.Contains(task.Instructions, want) {
			t.Fatalf("Instructions = %q, want reference guidance %q", task.Instructions, want)
		}
	}
}

func TestGetAddressTaskInstructionsUseReferencePromptOrder(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{})

	safety := strings.Index(task.Instructions, "Ask the user to confirm postal codes and spelled address parts without reading long values back.")
	update := strings.Index(task.Instructions, "Call `update_address` at the first opportunity")
	confirm := strings.Index(task.Instructions, "Call `confirm_address` after the user confirmed the address is correct.")
	recover := strings.Index(task.Instructions, "If the address is unclear or invalid")

	if safety < 0 || update < 0 || confirm < 0 || recover < 0 {
		t.Fatalf("Instructions = %q, want reference address prompt sections", task.Instructions)
	}
	if !(safety < update && update < confirm && confirm < recover) {
		t.Fatalf("address prompt order safety=%d update=%d confirm=%d recover=%d, want safety before update before confirm before recovery", safety, update, confirm, recover)
	}
	for _, stale := range []string{
		"Confirm postal codes by reading them out digit-by-digit",
		"For example, read 90210 as 'nine zero two one zero.'",
		"Spell out the address letter-by-letter when applicable",
	} {
		if strings.Contains(task.Instructions, stale) {
			t.Fatalf("Instructions = %q, want no long address readback guidance %q", task.Instructions, stale)
		}
	}
}

func TestGetAddressTaskInstructionsUseReferenceSpokenSymbolGuidance(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{})

	for _, want := range []string{
		"Convert words like 'dash' and 'apostrophe' into symbols: `-`, `'`.",
		"Convert spelled out numbers like 'six' and 'seven' into numerals: `6`, `7`.",
	} {
		if !strings.Contains(task.Instructions, want) {
			t.Fatalf("Instructions = %q, want spoken symbol guidance %q", task.Instructions, want)
		}
	}
}

func TestGetAddressTaskInstructionsPreserveReferenceModalityVariants(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{})

	if task.InstructionVariants == nil {
		t.Fatal("InstructionVariants = nil, want reference audio/text instruction variants")
	}
	audio := task.InstructionVariants.AsModality("audio").String()
	text := task.InstructionVariants.AsModality("text").String()

	for _, want := range []string{
		"Expect that users will say address in different formats with fields filled like:",
		"Convert words like 'dash' and 'apostrophe' into symbols: `-`, `'`.",
		"Ask the user to confirm postal codes and spelled address parts without reading long values back.",
		"Call `confirm_address` after the user confirmed the address is correct.",
	} {
		if !strings.Contains(audio, want) {
			t.Fatalf("audio instructions = %q, want reference audio guidance %q", audio, want)
		}
	}
	for _, want := range []string{
		"Expect users to type their address directly.",
		"If the address looks almost correct but has minor issues (e.g. missing country or postal code), prompt for clarification.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("text instructions = %q, want reference text guidance %q", text, want)
		}
	}
	for _, stale := range []string{
		"Expect that users will say address in different formats with fields filled like:",
		"Ask the user to confirm postal codes and spelled address parts without reading long values back.",
		"Call `confirm_address` after the user confirmed the address is correct.",
	} {
		if strings.Contains(text, stale) {
			t.Fatalf("text instructions = %q, want no audio/default-confirmation guidance %q", text, stale)
		}
	}
}

func TestGetAddressTaskInstructionPartsCustomizePersonaAndExtra(t *testing.T) {
	customPersona := "You only collect shipping addresses for hardware orders."
	task := NewGetAddressTask(GetAddressOptions{
		Instructions: &beta.InstructionParts{
			Persona: &customPersona,
			Extra:   "Ask whether the destination is residential or commercial.",
		},
	})

	if !strings.Contains(task.Instructions, customPersona) {
		t.Fatalf("Instructions = %q, want custom persona", task.Instructions)
	}
	if strings.Contains(task.Instructions, "responsible solely for capturing an address") {
		t.Fatalf("Instructions = %q, want default persona replaced", task.Instructions)
	}
	if !strings.Contains(task.Instructions, "Ask whether the destination is residential or commercial.") {
		t.Fatalf("Instructions = %q, want extra instructions appended", task.Instructions)
	}
}

func TestGetAddressTaskAppendsReferenceExtraInstructions(t *testing.T) {
	opts := GetAddressOptions{}
	field := reflect.ValueOf(&opts).Elem().FieldByName("ExtraInstructions")
	if !field.IsValid() {
		t.Fatal("GetAddressOptions.ExtraInstructions missing; want reference extra_instructions constructor option")
	}
	field.SetString("Ask whether the delivery address has a loading dock.")

	task := NewGetAddressTask(opts)

	if !strings.Contains(task.Instructions, "Ask whether the delivery address has a loading dock.") {
		t.Fatalf("Instructions = %q, want extra instructions appended", task.Instructions)
	}
	text := task.InstructionVariants.AsModality("text").String()
	if !strings.Contains(text, "Ask whether the delivery address has a loading dock.") {
		t.Fatalf("text instructions = %q, want extra instructions appended", text)
	}
}

func TestGetAddressTaskIgnoresExtraInstructionsWithCustomInstructions(t *testing.T) {
	persona := "You only collect billing addresses."
	task := NewGetAddressTask(GetAddressOptions{
		Instructions:      &beta.InstructionParts{Persona: &persona, Extra: "Only ask for billing-address details."},
		ExtraInstructions: "Ask for a loading dock.",
	})

	if strings.Contains(task.Instructions, "Ask for a loading dock.") {
		t.Fatalf("Instructions = %q, want ExtraInstructions ignored when custom Instructions are provided", task.Instructions)
	}
	text := task.InstructionVariants.AsModality("text").String()
	if strings.Contains(text, "Ask for a loading dock.") {
		t.Fatalf("text instructions = %q, want ExtraInstructions ignored when custom Instructions are provided", text)
	}
	if !strings.Contains(task.Instructions, "Only ask for billing-address details.") {
		t.Fatalf("Instructions = %q, want custom InstructionParts extra preserved", task.Instructions)
	}
}

func TestGetAddressTaskInstructionPartsCanRemovePersona(t *testing.T) {
	emptyPersona := ""
	task := NewGetAddressTask(GetAddressOptions{
		Instructions: &beta.InstructionParts{Persona: &emptyPersona},
	})

	if strings.Contains(task.Instructions, "responsible solely for capturing an address") {
		t.Fatalf("Instructions = %q, want default persona removed", task.Instructions)
	}
	if !strings.Contains(task.Instructions, "Call `update_address` at the first opportunity") {
		t.Fatalf("Instructions = %q, want workflow guidance preserved", task.Instructions)
	}
}

func TestUpdateAddressToolParametersUseReferenceDescriptions(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{})
	tool := &updateAddressTool{task: task}

	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", tool.Parameters()["properties"])
	}

	for field, want := range map[string]string{
		"street_address": "Dependent on country, may include fields like house number, street name, block, or district",
		"unit_number":    "The unit number, for example Floor 1 or Apartment 12. If there is no unit number, return ''",
		"locality":       "Dependent on country, may include fields like city, zip code, or province",
		"country":        "The country the user lives in spelled out fully",
	} {
		schema, ok := properties[field].(map[string]any)
		if !ok {
			t.Fatalf("properties[%s] = %#v, want map", field, properties[field])
		}
		if got := schema["description"]; got != want {
			t.Fatalf("properties[%s].description = %#v, want %q", field, got, want)
		}
	}
}

func TestGetAddressTaskOnEnterUsesReferencePrompt(t *testing.T) {
	want := "Ask the user to provide their address."
	if got := addressOnEnterPrompt(); got != want {
		t.Fatalf("addressOnEnterPrompt() = %q, want %q", got, want)
	}

	task := NewGetAddressTask(GetAddressOptions{})
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
		t.Fatal("timed out waiting for address on-enter prompt")
	}
}

func TestGetAddressTaskStaleConfirmationPromptsForUpdatedAddress(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{})
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
		t.Fatal("timed out waiting for address on-enter prompt")
	}

	update := &updateAddressTool{task: task}

	if _, err := update.Execute(context.Background(), `{"street_address":"123 Main St","unit_number":"","locality":"Springfield IL 62701","country":"United States"}`); err != nil {
		t.Fatalf("first update Execute() error = %v", err)
	}
	staleConfirm := &confirmAddressTool{task: task, address: "123 Main St Springfield IL 62701 United States"}

	if _, err := update.Execute(context.Background(), `{"street_address":"456 Oak Ave","unit_number":"","locality":"Springfield IL 62701","country":"United States"}`); err != nil {
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
		want := addressStaleConfirmationPrompt()
		if ev.SpeechHandle.Generation.Instructions == nil {
			t.Fatal("stale confirmation instructions = nil, want changed-address prompt")
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

func TestDeclineAddressCaptureToolFailsWithReason(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &declineAddressCaptureTool{task: task}

	out, err := tool.Execute(context.Background(), `{"reason":"user refused"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after decline", out)
	}
	_, err = task.WaitAny(context.Background())
	var toolErr llm.ToolError
	if !errors.As(err, &toolErr) {
		t.Fatalf("WaitAny() error = %T %v, want ToolError", err, err)
	}
	want := "couldn't get the address: user refused"
	if toolErr.Message != want {
		t.Fatalf("ToolError.Message = %q, want %q", toolErr.Message, want)
	}
}

func TestDeclineAddressCaptureToolUsesRunContextCurrentAgent(t *testing.T) {
	staleTask := NewGetAddressTask(GetAddressOptions{})
	currentTask := NewGetAddressTask(GetAddressOptions{})
	session := agent.NewAgentSession(currentTask, nil, agent.AgentSessionOptions{})
	ctx := agent.WithRunContext(context.Background(), agent.NewRunContext(session, nil, nil))
	tool := &declineAddressCaptureTool{task: staleTask}

	out, err := tool.Execute(ctx, `{"reason":"user refused current address"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after decline", out)
	}

	select {
	case err := <-currentTask.Err:
		var toolErr llm.ToolError
		if !errors.As(err, &toolErr) {
			t.Fatalf("current task error = %T %v, want ToolError", err, err)
		}
		want := "couldn't get the address: user refused current address"
		if toolErr.Message != want {
			t.Fatalf("ToolError.Message = %q, want %q", toolErr.Message, want)
		}
	case <-time.After(time.Second):
		t.Fatal("current task did not fail after decline_address_capture")
	}

	select {
	case err := <-staleTask.Err:
		t.Fatalf("stale task failed with %v, want decline routed to current agent", err)
	default:
	}
}

func TestDeclineAddressCaptureToolUsesReferenceSchema(t *testing.T) {
	tool := &declineAddressCaptureTool{task: NewGetAddressTask(GetAddressOptions{})}

	wantDescription := "Handles the case when the user explicitly declines to provide an address."
	if got := tool.Description(); got != wantDescription {
		t.Fatalf("decline_address_capture description = %q, want %q", got, wantDescription)
	}
	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", tool.Parameters()["properties"])
	}
	reason, ok := properties["reason"].(map[string]any)
	if !ok {
		t.Fatalf("reason schema = %#v, want map", properties["reason"])
	}
	wantParam := "A short explanation of why the user declined to provide the address"
	if got := reason["description"]; got != wantParam {
		t.Fatalf("reason description = %#v, want %q", got, wantParam)
	}
}

type referenceAddressExtraTool struct {
	id string
}

func (t referenceAddressExtraTool) ID() string          { return t.id }
func (t referenceAddressExtraTool) Name() string        { return t.id }
func (t referenceAddressExtraTool) Description() string { return "reference extra address tool" }
func (t referenceAddressExtraTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t referenceAddressExtraTool) Execute(ctx context.Context, args string) (string, error) {
	return "", nil
}
