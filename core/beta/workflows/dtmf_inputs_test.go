package workflows

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/beta"
	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestRecordInputsToolRejectsInvalidDtmfEvents(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	tool := &recordInputsTool{task: task}

	_, err := tool.Execute(context.Background(), `{"inputs":["1","12"]}`)
	if err == nil {
		t.Fatal("Execute() error = nil, want invalid DTMF event error")
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for invalid DTMF", result)
	default:
	}
}

func TestConfirmInputsToolRejectsInvalidDtmfEvents(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, true)
	tool := &confirmInputsTool{task: task}

	_, err := tool.Execute(context.Background(), `{"inputs":["1","x"]}`)
	if err == nil {
		t.Fatal("Execute() error = nil, want invalid DTMF event error")
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for invalid DTMF", result)
	default:
	}
}

func TestConfirmInputsToolCompletesWithoutToolOutput(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, true)
	tool := &confirmInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["1","2"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2" {
			t.Fatalf("UserInput = %q, want 1 2", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after confirm_inputs")
	}
}

func TestNewGetDtmfTaskPreservesExplicitZeroInputTimeout(t *testing.T) {
	opts := GetDtmfOptions{
		NumDigits:        2,
		DtmfInputTimeout: 0,
	}
	field := reflect.ValueOf(&opts).Elem().FieldByName("DtmfInputTimeoutSet")
	if !field.IsValid() {
		t.Fatal("GetDtmfOptions.DtmfInputTimeoutSet missing; want reference explicit zero dtmf_input_timeout support")
	}
	field.SetBool(true)

	task, err := NewGetDtmfTaskWithOptions(opts)
	if err != nil {
		t.Fatalf("NewGetDtmfTaskWithOptions() error = %v", err)
	}
	if task.DtmfInputTimeout != 0 {
		t.Fatalf("DtmfInputTimeout = %v, want explicit zero timeout", task.DtmfInputTimeout)
	}
}

func TestRecordInputsToolCompletesWithoutToolOutput(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["1","2"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2" {
			t.Fatalf("UserInput = %q, want 1 2", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after record_inputs")
	}
}

func TestDtmfToolInputsRejectPartialLength(t *testing.T) {
	tests := []struct {
		name string
		task *GetDtmfTask
		tool llm.Tool
		args string
	}{
		{
			name: "record inputs",
			task: newDtmfTaskForTest(t, 4, false),
			args: `{"inputs":["1","2"]}`,
		},
		{
			name: "confirm inputs",
			task: newDtmfTaskForTest(t, 4, true),
			args: `{"inputs":["1","2"]}`,
		},
	}
	tests[0].tool = &recordInputsTool{task: tests[0].task}
	tests[1].tool = &confirmInputsTool{task: tests[1].task}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.tool.Execute(context.Background(), tt.args)
			if err != nil {
				t.Fatalf("Execute() error = %v, want task ToolError completion", err)
			}
			if out != "" {
				t.Fatalf("Execute() output = %q, want empty output after partial input failure", out)
			}

			select {
			case err := <-tt.task.Err:
				var toolErr llm.ToolError
				if !errors.As(err, &toolErr) {
					t.Fatalf("task error = %T %v, want ToolError", err, err)
				}
				want := "Digits input not fully received. Expect 4 digits, got 2"
				if toolErr.Message != want {
					t.Fatalf("ToolError.Message = %q, want %q", toolErr.Message, want)
				}
			case result := <-tt.task.Result:
				t.Fatalf("task completed with %#v, want partial input ToolError", result)
			default:
				t.Fatal("task did not fail after partial tool inputs")
			}
		})
	}
}

func TestRecordInputsToolCancelsPendingKeypadCollection(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	task.DtmfInputTimeout = time.Hour
	tool := &recordInputsTool{task: task}

	task.onSipDTMFReceived("1")

	out, err := tool.Execute(context.Background(), `{"inputs":["two","three"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF inputs accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after voice completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "2 3" {
			t.Fatalf("UserInput = %q, want spoken voice inputs to win over partial keypad input", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF inputs")
	}

	task.mu.Lock()
	timerStillScheduled := task.timer != nil
	pendingInputs := append([]beta.DtmfEvent(nil), task.currDtmfInputs...)
	task.mu.Unlock()

	if timerStillScheduled {
		t.Fatal("DTMF timer still scheduled after voice completion, want pending keypad debounce canceled")
	}
	if len(pendingInputs) != 0 {
		t.Fatalf("pending DTMF inputs = %#v, want cleared after voice completion", pendingInputs)
	}
}

func TestRecordInputsToolNormalizesSpokenDigits(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one","two","star","pound"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF inputs accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 * #" {
			t.Fatalf("UserInput = %q, want spoken DTMF inputs normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF inputs")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitPhrase(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF phrase accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want spoken DTMF phrase normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF phrase")
	}
}

func TestRecordInputsToolNormalizesNoisySTTDigitHomophones(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["for","to","too","ate"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want noisy STT digit homophones accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "4 2 2 8" {
			t.Fatalf("UserInput = %q, want noisy STT digit homophones normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after noisy STT DTMF inputs")
	}
}

func TestRecordInputsToolNormalizesForeHomophone(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one","two","three","fore"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want fore homophone accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want fore homophone normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after fore-homophone DTMF input")
	}
}

func TestRecordInputsToolNormalizesThreeHomophones(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one","tree","free","four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want three homophones accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 3 3 4" {
			t.Fatalf("UserInput = %q, want three homophones normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after three-homophone DTMF inputs")
	}
}

func TestRecordInputsToolNormalizesSixHomophone(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one","two","sex","four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want six homophone accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 6 4" {
			t.Fatalf("UserInput = %q, want six homophone normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after six-homophone DTMF input")
	}
}

func TestRecordInputsToolNormalizesNinerDigit(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one","two","three","niner"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want niner spoken DTMF digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 9" {
			t.Fatalf("UserInput = %q, want niner normalized to keypad 9", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after niner DTMF input")
	}
}

func TestRecordInputsToolNormalizesAughtDigit(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one","aught","two","three"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want aught spoken DTMF digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 0 2 3" {
			t.Fatalf("UserInput = %q, want aught normalized to keypad 0", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after aught DTMF input")
	}
}

func TestRecordInputsToolNormalizesNaughtDigit(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one","naught","two","three"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want naught spoken DTMF digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 0 2 3" {
			t.Fatalf("UserInput = %q, want naught normalized to keypad 0", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after naught DTMF input")
	}
}

func TestRecordInputsToolNormalizesNoughtDigit(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one","nought","two","three"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want nought spoken DTMF digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 0 2 3" {
			t.Fatalf("UserInput = %q, want nought normalized to keypad 0", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after nought DTMF input")
	}
}

func TestRecordInputsToolNormalizesOughtDigit(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one","ought","two","three"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want ought spoken DTMF digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 0 2 3" {
			t.Fatalf("UserInput = %q, want ought normalized to keypad 0", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after ought DTMF input")
	}
}

func TestRecordInputsToolNormalizesOweDigit(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one","owe","two","three"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want owe spoken DTMF digit accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 0 2 3" {
			t.Fatalf("UserInput = %q, want owe normalized to keypad 0", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after owe DTMF input")
	}
}

func TestRecordInputsToolNormalizesTwentyOhSpokenGroup(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "twenty oh five", want: "2 0 0 5"},
		{input: "thirty oh seven", want: "3 0 0 7"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			task := newDtmfTaskForTest(t, 4, false)
			tool := &recordInputsTool{task: task}

			out, err := tool.Execute(context.Background(), `{"inputs":["`+tt.input+`"]}`)
			if err != nil {
				t.Fatalf("Execute() error = %v, want tens-oh spoken DTMF group accepted", err)
			}
			if out != "" {
				t.Fatalf("Execute() output = %q, want empty output after completion", out)
			}

			select {
			case result := <-task.Result:
				if result.UserInput != tt.want {
					t.Fatalf("UserInput = %q, want tens-oh spoken DTMF group normalized to %q", result.UserInput, tt.want)
				}
			default:
				t.Fatal("task did not complete after tens-oh spoken DTMF group")
			}
		})
	}
}

func TestRecordInputsToolNormalizesTwentyAughtSpokenGroup(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["twenty aught five"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want twenty-aught spoken DTMF group accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "2 0 0 5" {
			t.Fatalf("UserInput = %q, want twenty-aught spoken DTMF group normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after twenty-aught spoken DTMF group")
	}
}

func TestRecordInputsToolNormalizesTwentyNaughtSpokenGroup(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["twenty naught five"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want twenty-naught spoken DTMF group accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "2 0 0 5" {
			t.Fatalf("UserInput = %q, want twenty-naught spoken DTMF group normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after twenty-naught spoken DTMF group")
	}
}

func TestRecordInputsToolNormalizesSpokenTensGroup(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["thirty seven"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken tens DTMF group accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "3 7" {
			t.Fatalf("UserInput = %q, want spoken tens DTMF group normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken tens DTMF group")
	}
}

func TestRecordInputsToolNormalizesSpokenHundredTensGroup(t *testing.T) {
	task := newDtmfTaskForTest(t, 3, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one thirty seven"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken hundred-tens DTMF group accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 3 7" {
			t.Fatalf("UserInput = %q, want spoken hundred-tens DTMF group normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken hundred-tens DTMF group")
	}
}

func TestRecordInputsToolNormalizesSpokenHundredSingleDigitGroup(t *testing.T) {
	task := newDtmfTaskForTest(t, 3, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one hundred tree"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken hundred-single-digit DTMF group accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 0 3" {
			t.Fatalf("UserInput = %q, want spoken hundred-single-digit DTMF group normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken hundred-single-digit DTMF group")
	}
}

func TestRecordInputsToolNormalizesSpokenHundredAndSingleDigitGroup(t *testing.T) {
	task := newDtmfTaskForTest(t, 3, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one hundred and tree"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken hundred-and DTMF group accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 0 3" {
			t.Fatalf("UserInput = %q, want spoken hundred-and DTMF group normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken hundred-and DTMF group")
	}
}

func TestRecordInputsToolNormalizesSpokenHundredNaughtGroup(t *testing.T) {
	task := newDtmfTaskForTest(t, 3, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one hundred naught five"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken hundred-naught DTMF group accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 0 5" {
			t.Fatalf("UserInput = %q, want spoken hundred-naught DTMF group normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken hundred-naught DTMF group")
	}
}

func TestRecordInputsToolNormalizesSpokenHundredTwentyOhGroup(t *testing.T) {
	task := newDtmfTaskForTest(t, 5, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one twenty oh five"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken hundred-twenty-oh DTMF group accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 0 0 5" {
			t.Fatalf("UserInput = %q, want spoken hundred-twenty-oh DTMF group normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken hundred-twenty-oh DTMF group")
	}
}

func TestRecordInputsToolNormalizesSpokenHundredTwentyOhGroupAcrossFiller(t *testing.T) {
	task := newDtmfTaskForTest(t, 5, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one twenty uh oh five"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken hundred-twenty-oh DTMF group with filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 0 0 5" {
			t.Fatalf("UserInput = %q, want spoken hundred-twenty-oh DTMF group with filler normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken hundred-twenty-oh DTMF group with filler")
	}
}

func TestRecordInputsToolNormalizesSplitSpokenHundredTwentyOhGroupAcrossFiller(t *testing.T) {
	task := newDtmfTaskForTest(t, 5, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one","twenty","uh","oh","five"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split spoken hundred-twenty-oh DTMF group with filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 0 0 5" {
			t.Fatalf("UserInput = %q, want split spoken hundred-twenty-oh DTMF group with filler normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after split spoken hundred-twenty-oh DTMF group with filler")
	}
}

func TestRecordInputsToolNormalizesRepeatedSplitSpokenGroup(t *testing.T) {
	task := newDtmfTaskForTest(t, 8, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["double","twenty","oh","five"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want repeated split spoken DTMF group accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "2 0 0 5 2 0 0 5" {
			t.Fatalf("UserInput = %q, want repeated split spoken DTMF group normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after repeated split spoken DTMF group")
	}
}

func TestRecordInputsToolNormalizesRepeatedSplitHundredTensGroup(t *testing.T) {
	task := newDtmfTaskForTest(t, 6, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["double","one","hundred","twenty","three"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want repeated split hundred-tens DTMF group accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 1 2 3" {
			t.Fatalf("UserInput = %q, want repeated split hundred-tens group normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after repeated split hundred-tens group")
	}
}

func TestRecordInputsToolNormalizesSpokenTeenGroup(t *testing.T) {
	task := newDtmfTaskForTest(t, 3, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one fifteen"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken teen DTMF group accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 1 5" {
			t.Fatalf("UserInput = %q, want spoken teen DTMF group normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken teen DTMF group")
	}
}

func TestRecordInputsToolNormalizesWonHomophone(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["won two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want won homophone accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want won homophone normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after won-homophone DTMF input")
	}
}

func TestRecordInputsToolNormalizesSpokenDoubleDigits(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["double five","star","pound"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken doubled DTMF input accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "5 5 * #" {
			t.Fatalf("UserInput = %q, want spoken doubled DTMF input normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken doubled DTMF input")
	}
}

func TestRecordInputsToolNormalizesSpokenSingleDigits(t *testing.T) {
	task := newDtmfTaskForTest(t, 3, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["single five","star","pound"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken single DTMF input accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "5 * #" {
			t.Fatalf("UserInput = %q, want spoken single DTMF input normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken single DTMF input")
	}
}

func TestRecordInputsToolNormalizesSpokenQuadrupleDigits(t *testing.T) {
	task := newDtmfTaskForTest(t, 5, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["quadruple five","star"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken quadruple DTMF input accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "5 5 5 5 *" {
			t.Fatalf("UserInput = %q, want spoken quadruple DTMF input normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken quadruple DTMF input")
	}
}

func TestRecordInputsToolNormalizesPunctuatedSpokenDoubleDigits(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["double, five","star","pound"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want punctuated spoken doubled DTMF input accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "5 5 * #" {
			t.Fatalf("UserInput = %q, want punctuated spoken doubled DTMF input normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after punctuated spoken doubled DTMF input")
	}
}

func TestRecordInputsToolNormalizesSplitSpokenDoubleDigits(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["double","five","star","pound"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split spoken doubled DTMF input accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "5 5 * #" {
			t.Fatalf("UserInput = %q, want split spoken doubled DTMF input normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after split spoken doubled DTMF input")
	}
}

func TestRecordInputsToolNormalizesSpokenDoubleDigitsAcrossFiller(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["double uh five","star","pound"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want doubled DTMF digit across filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "5 5 * #" {
			t.Fatalf("UserInput = %q, want filler after double ignored", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after doubled DTMF input with filler")
	}
}

func TestRecordInputsToolNormalizesSpokenDoubleDigitsAcrossActuallyFiller(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["double actually five","star","pound"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want doubled digit across correction filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "5 5 * #" {
			t.Fatalf("UserInput = %q, want correction filler ignored after double", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF input with correction filler")
	}
}

func TestRecordInputsToolNormalizesSpokenDoubleDigitsAcrossSorryFiller(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["double sorry five","star","pound"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want doubled digit across apology filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "5 5 * #" {
			t.Fatalf("UserInput = %q, want apology filler ignored after double", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF input with apology filler")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAcrossLikeFiller(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["like one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF input with like filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want like filler ignored", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF input with like filler")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsBeforeTrailingSignoff(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one two three four that's it"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF input with trailing sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want trailing sign-off ignored", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF input with trailing sign-off")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsBeforeExpandedTrailingSignoff(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one two three four that is all"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF input with expanded trailing sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want expanded trailing sign-off ignored", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF input with expanded trailing sign-off")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsBeforeThatWillBeAllSignoff(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one two three four that'll be all"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF input with that'll-be-all sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want that'll-be-all trailing sign-off ignored", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF input with that'll-be-all sign-off")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsBeforeThatWillBeAllForNowSignoff(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one two three four that'll be all for now thanks"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF input with that'll-be-all-for-now sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want that'll-be-all-for-now trailing sign-off ignored", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF input with that'll-be-all-for-now sign-off")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsBeforeForNowSignoff(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one two three four that's it for now"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF input with for-now sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want trailing for-now sign-off ignored", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF input with for-now sign-off")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsBeforeForNowThanksSignoff(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one two three four that's it for now thanks"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF input with for-now-thanks sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want trailing for-now-thanks sign-off ignored", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF input with for-now-thanks sign-off")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsBeforeForYouSignoff(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one two three four that's all for you"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF input with for-you sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want trailing for-you sign-off ignored", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF input with for-you sign-off")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsBeforeShortForYouSignoff(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one two three four for you thanks"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF input with short for-you sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want short trailing for-you sign-off ignored", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF input with short for-you sign-off")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsBeforeForTodaySignoff(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one two three four for today thanks"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF input with short for-today sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want short trailing for-today sign-off ignored", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF input with short for-today sign-off")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsBeforeForTheDaySignoff(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one two three four for the day thanks"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF input with short for-the-day sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want short trailing for-the-day sign-off ignored", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF input with short for-the-day sign-off")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsBeforeForTodayThanksSignoff(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one two three four that's it for today thanks"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF input with for-today-thanks sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want trailing for-today-thanks sign-off ignored", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF input with for-today-thanks sign-off")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsBeforeForTheDayThanksSignoff(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one two three four that's it for the day thanks"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF input with for-the-day-thanks sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want trailing for-the-day-thanks sign-off ignored", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF input with for-the-day-thanks sign-off")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsBeforeThatllBeAllForDayThanksSignoff(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one two three four that'll be all for day thanks"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF input with contracted for-day-thanks sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want contracted trailing for-day sign-off ignored", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after contracted trailing for-day sign-off")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsBeforeSplitThatllBeAllForDayThanksSignoff(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one two three four that ll be all for day thanks"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF input with split contracted for-day-thanks sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want split contracted trailing for-day sign-off ignored", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after split contracted trailing for-day sign-off")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsBeforeSplitThatllShortSignoff(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one two three four that ll be all thanks"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF input with split contracted short sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want split contracted short sign-off ignored", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after split contracted short sign-off")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsBeforeDoneSignoff(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one two three four done"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF input with done sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want trailing done sign-off ignored", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF input with done sign-off")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsBeforeFinishedSignoff(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one two three four finished"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF input with finished sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want trailing finished sign-off ignored", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF input with finished sign-off")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsBeforeCompleteSignoff(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one two three four complete"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF input with complete sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want trailing complete sign-off ignored", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF input with complete sign-off")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsBeforeCompletedSignoff(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one two three four completed"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF input with completed sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want trailing completed sign-off ignored", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF input with completed sign-off")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsBeforeSubmittedSignoff(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one two three four submitted"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF input with submitted sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want trailing submitted sign-off ignored", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF input with submitted sign-off")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsBeforeSentSignoff(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one two three four sent"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF input with sent sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want trailing sent sign-off ignored", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF input with sent sign-off")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsBeforeEnteredSignoff(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one two three four entered"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF input with entered sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want trailing entered sign-off ignored", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF input with entered sign-off")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsBeforeAllDoneSignoff(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one two three four all done"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF input with all-done sign-off accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want trailing all-done sign-off ignored", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF input with all-done sign-off")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterPreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["my pin is one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF input with preamble accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want spoken DTMF preamble filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF input with preamble")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterArticlePreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["the pin is one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want article preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want article preamble-spoken DTMF filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after article preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterWillBePreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["pin will be one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want will-be preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want will-be preamble-spoken DTMF filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after will-be preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterPasscodePreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["passcode is one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want passcode preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want passcode preamble-spoken DTMF filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after passcode preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterAccessCodePreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["access code is one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want access-code preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want access-code preamble-spoken DTMF filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after access-code preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterVerificationCodePreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["verification code is one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want verification-code preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want verification-code preamble-spoken DTMF filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after verification-code preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterAuthorizationCodePreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["authorization code is one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want authorization-code preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want authorization-code preamble-spoken DTMF filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after authorization-code preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterConfirmationCodePreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["confirmation code is one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want confirmation-code preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want confirmation-code preamble filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after confirmation-code preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterContractedCodePreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["access code's one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want contracted code preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want contracted code preamble filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after contracted code preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterOTPPreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["OTP is one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want OTP preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want OTP preamble-spoken DTMF filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after OTP preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterExtensionPreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["extension is one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want extension preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want extension preamble-spoken DTMF filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after extension preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterAccountNumberPreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["account number is one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want account-number preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want account-number preamble-spoken DTMF filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after account-number preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterIDPreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["ID is one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want ID preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want ID preamble-spoken DTMF filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after ID preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterMemberIDPreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["member ID is one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want member-ID preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want member-ID preamble-spoken DTMF filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after member-ID preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterCustomerIDPreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["customer ID is one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want customer-ID preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want customer-ID preamble-spoken DTMF filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after customer-ID preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterSubscriberIDPreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["subscriber ID is one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want subscriber-ID preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want subscriber-ID preamble-spoken DTMF filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after subscriber-ID preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterPolicyNumberPreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["policy number is one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want policy-number preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want policy-number preamble-spoken DTMF filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after policy-number preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterClaimNumberPreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["claim number is one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want claim-number preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want claim-number preamble-spoken DTMF filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after claim-number preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterRoutingNumberPreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["routing number is one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want routing-number preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want routing-number preamble-spoken DTMF filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after routing-number preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterInvoiceNumberPreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["invoice number is one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want invoice-number preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want invoice-number preamble-spoken DTMF filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after invoice-number preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterReferenceNumberPreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["reference number is one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want reference-number preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want reference-number preamble-spoken DTMF filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after reference-number preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterReservationNumberPreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["reservation number is one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want reservation-number preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want reservation-number preamble-spoken DTMF filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after reservation-number preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterOrderNumberPreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["order number is one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want order-number preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want order-number preamble-spoken DTMF filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after order-number preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterCaseNumberPreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["case number is one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want case-number preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want case-number preamble-spoken DTMF filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after case-number preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterTicketNumberPreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["ticket number is one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want ticket-number preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want ticket-number preamble-spoken DTMF filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after ticket-number preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterOneTimePasswordPreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one time password is one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want one-time-password preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want one-time-password preamble filtered without extra digit", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after one-time-password preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterCommandPreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["press one two three four"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want command preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want command preamble filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after command preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterOptionPreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 1, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["option one"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want option preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1" {
			t.Fatalf("UserInput = %q, want option preamble filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after option preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterMenuOptionPreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 1, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["menu option one"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want menu option preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1" {
			t.Fatalf("UserInput = %q, want menu option preamble filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after menu option preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterSelectionPreamble(t *testing.T) {
	tests := []struct {
		name      string
		inputJSON string
		want      string
	}{
		{name: "select option", inputJSON: `{"inputs":["select option one"]}`, want: "1"},
		{name: "choose option", inputJSON: `{"inputs":["choose option two"]}`, want: "2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := newDtmfTaskForTest(t, 1, false)
			tool := &recordInputsTool{task: task}

			out, err := tool.Execute(context.Background(), tt.inputJSON)
			if err != nil {
				t.Fatalf("Execute() error = %v, want selection preamble-spoken DTMF accepted", err)
			}
			if out != "" {
				t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
			}

			select {
			case result := <-task.Result:
				if result.UserInput != tt.want {
					t.Fatalf("UserInput = %q, want selection preamble filtered to %q", result.UserInput, tt.want)
				}
			default:
				t.Fatal("task did not complete after selection preamble-spoken DTMF")
			}
		})
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterIntentPreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 1, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["I want option one"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want intent preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1" {
			t.Fatalf("UserInput = %q, want intent preamble filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after intent preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterIntentConnector(t *testing.T) {
	task := newDtmfTaskForTest(t, 1, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["I want to choose option one"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want connector preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1" {
			t.Fatalf("UserInput = %q, want connector preamble filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after connector preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterWouldLikePreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 1, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["I would like option one"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want would-like preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1" {
			t.Fatalf("UserInput = %q, want would-like preamble filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after would-like preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterContractionPreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 1, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["I'd like option one"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want contraction preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1" {
			t.Fatalf("UserInput = %q, want contraction preamble filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after contraction preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesSpokenDigitsAfterOptionNumberPreamble(t *testing.T) {
	task := newDtmfTaskForTest(t, 1, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["option number one"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want option number preamble-spoken DTMF accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after record_inputs completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1" {
			t.Fatalf("UserInput = %q, want option number preamble filtered", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after option number preamble-spoken DTMF")
	}
}

func TestRecordInputsToolNormalizesPunctuatedSpokenDigits(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one,","two.","star,","pound."]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want punctuated spoken DTMF inputs accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 * #" {
			t.Fatalf("UserInput = %q, want punctuated spoken DTMF inputs normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after punctuated spoken DTMF inputs")
	}
}

func TestRecordInputsToolNormalizesSpokenNumberSign(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one","number sign"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken number sign accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 #" {
			t.Fatalf("UserInput = %q, want spoken number sign normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken number sign")
	}
}

func TestRecordInputsToolNormalizesSplitSpokenNumberSign(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one","number","sign"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split spoken number sign accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 #" {
			t.Fatalf("UserInput = %q, want split spoken number sign normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after split spoken number sign")
	}
}

func TestRecordInputsToolNormalizesSpokenNumberKey(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one","number","key"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken number key accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 #" {
			t.Fatalf("UserInput = %q, want spoken number key normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken number key")
	}
}

func TestRecordInputsToolNormalizesSpokenHashSign(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one","hash","sign"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken hash sign accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 #" {
			t.Fatalf("UserInput = %q, want spoken hash sign normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken hash sign")
	}
}

func TestRecordInputsToolNormalizesSpokenHashtag(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one","hashtag"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken hashtag accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 #" {
			t.Fatalf("UserInput = %q, want spoken hashtag normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken hashtag")
	}
}

func TestRecordInputsToolNormalizesSpokenOctothorpe(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one","octothorpe"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken octothorpe accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 #" {
			t.Fatalf("UserInput = %q, want spoken octothorpe normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken octothorpe")
	}
}

func TestRecordInputsToolNormalizesSpokenOctothorpeSignPhrase(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one","octothorpe sign"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken octothorpe sign accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 #" {
			t.Fatalf("UserInput = %q, want spoken octothorpe sign normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken octothorpe sign")
	}
}

func TestRecordInputsToolNormalizesSplitSpokenPoundSign(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one","pound","sign"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split spoken pound sign accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}
	select {
	case result := <-task.Result:
		if result.UserInput != "1 #" {
			t.Fatalf("UserInput = %q, want split spoken pound sign normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after split spoken pound sign")
	}
}

func TestRecordInputsToolNormalizesSpokenPoundSignPhrase(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one","pound sign"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken pound sign phrase accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}
	select {
	case result := <-task.Result:
		if result.UserInput != "1 #" {
			t.Fatalf("UserInput = %q, want spoken pound sign phrase normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken pound sign phrase")
	}
}

func TestRecordInputsToolNormalizesSpokenSymbolPhrase(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one pound sign"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF symbol phrase accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 #" {
			t.Fatalf("UserInput = %q, want spoken DTMF symbol phrase normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF symbol phrase")
	}
}

func TestRecordInputsToolNormalizesSpokenPoundSymbol(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one","pound","symbol"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken pound symbol accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}
	select {
	case result := <-task.Result:
		if result.UserInput != "1 #" {
			t.Fatalf("UserInput = %q, want spoken pound symbol normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken pound symbol")
	}
}

func TestRecordInputsToolNormalizesSpokenKeyAliases(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one","star","key"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken key alias accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 *" {
			t.Fatalf("UserInput = %q, want spoken key alias normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken key alias")
	}
}

func TestRecordInputsToolNormalizesSpokenButtonAliases(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one","star","button"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken button alias accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 *" {
			t.Fatalf("UserInput = %q, want spoken button alias normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken button alias")
	}
}

func TestRecordInputsToolNormalizesSpokenMarkAliases(t *testing.T) {
	task := newDtmfTaskForTest(t, 3, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one","star","mark","pound mark"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken mark aliases accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 * #" {
			t.Fatalf("UserInput = %q, want spoken mark aliases normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken mark aliases")
	}
}

func TestRecordInputsToolNormalizesSpokenStarSymbol(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one","star","symbol"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken star symbol accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 *" {
			t.Fatalf("UserInput = %q, want spoken star symbol normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken star symbol")
	}
}

func TestRecordInputsToolTrimsSpokenStopKeySuffix(t *testing.T) {
	task := newDtmfTaskForTest(t, 4, false)
	tool := &recordInputsTool{task: task}

	out, err := tool.Execute(context.Background(), `{"inputs":["one","two","three","four","pound","key"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken stop-key suffix accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after completion", out)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2 3 4" {
			t.Fatalf("UserInput = %q, want spoken stop key suffix trimmed", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken stop-key suffix")
	}
}

func TestConfirmInputsToolNormalizesSpokenDigits(t *testing.T) {
	task := newDtmfTaskForTest(t, 3, true)
	tool := &confirmInputsTool{task: task}

	_, err := tool.Execute(context.Background(), `{"inputs":["zero","oh","nine"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DTMF inputs accepted", err)
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "0 0 9" {
			t.Fatalf("UserInput = %q, want spoken DTMF inputs normalized", result.UserInput)
		}
	default:
		t.Fatal("task did not complete after spoken DTMF confirmation")
	}
}

func TestConfirmInputsToolUsesReferenceDescription(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, true)
	tool := &confirmInputsTool{task: task}

	want := "Finalize the collected digit inputs after explicit user confirmation.\n\nUse this ONLY after the user has already confirmed the keypad entry is correct.\n\nDo not use this tool to capture the initial digits."
	if got := tool.Description(); got != want {
		t.Fatalf("confirm_inputs description = %q, want %q", got, want)
	}
}

func TestConfirmInputsToolDescriptionAvoidsDigitReadback(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, true)
	tool := &confirmInputsTool{task: task}

	description := tool.Description()
	for _, unsafe := range []string{
		"reading out the digits one by one",
		"digits one by one",
		"read out",
	} {
		if strings.Contains(description, unsafe) {
			t.Fatalf("confirm_inputs description = %q, want no readback guidance %q", description, unsafe)
		}
	}
	if !strings.Contains(description, "Use this ONLY after the user has already confirmed") {
		t.Fatalf("confirm_inputs description = %q, want after-confirmation guard", description)
	}
	if !strings.Contains(description, "Do not use this tool to capture the initial digits.") {
		t.Fatalf("confirm_inputs description = %q, want initial-capture guard", description)
	}
}

func TestRecordInputsToolUsesReferenceDescription(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	tool := &recordInputsTool{task: task}

	want := "Record the collected digit inputs without additional confirmation.\n\nCall this tool as soon as a valid sequence of digits has been provided by the user (via DTMF or spoken)."
	if got := tool.Description(); got != want {
		t.Fatalf("record_inputs description = %q, want %q", got, want)
	}
}

func TestDtmfInputToolsExposeReferenceEventEnum(t *testing.T) {
	cases := []struct {
		name string
		tool llm.Tool
	}{
		{name: "confirm_inputs", tool: &confirmInputsTool{task: newDtmfTaskForTest(t, 2, true)}},
		{name: "record_inputs", tool: &recordInputsTool{task: newDtmfTaskForTest(t, 2, false)}},
	}

	want := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "0", "*", "#", "A", "B", "C", "D"}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			properties, ok := tc.tool.Parameters()["properties"].(map[string]any)
			if !ok {
				t.Fatalf("properties = %#v, want map", tc.tool.Parameters()["properties"])
			}
			inputs, ok := properties["inputs"].(map[string]any)
			if !ok {
				t.Fatalf("inputs schema = %#v, want map", properties["inputs"])
			}
			items, ok := inputs["items"].(map[string]any)
			if !ok {
				t.Fatalf("inputs.items = %#v, want map", inputs["items"])
			}
			enum, ok := items["enum"].([]string)
			if !ok {
				t.Fatalf("inputs.items.enum = %#v, want []string", items["enum"])
			}
			if strings.Join(enum, ",") != strings.Join(want, ",") {
				t.Fatalf("inputs.items.enum = %#v, want %#v", enum, want)
			}
		})
	}
}

func TestRecordInputsToolNormalizesSpokenLetterKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		args string
	}{
		{name: "raw and letter prefix", args: `{"inputs":["a","b","letter c","letter d"]}`},
		{name: "STT letter sounds", args: `{"inputs":["ay","bee","see","dee"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := newDtmfTaskForTest(t, 4, false)
			tool := &recordInputsTool{task: task}

			out, err := tool.Execute(context.Background(), tc.args)
			if err != nil {
				t.Fatalf("Execute() error = %v, want noisy spoken letter DTMF keys accepted", err)
			}
			if out != "" {
				t.Fatalf("Execute() output = %q, want empty output after completion", out)
			}

			select {
			case result := <-task.Result:
				if result.UserInput != "A B C D" {
					t.Fatalf("UserInput = %q, want spoken letter keys normalized", result.UserInput)
				}
			case <-time.After(time.Second):
				t.Fatal("task did not complete after spoken letter DTMF keys")
			}
		})
	}
}

func TestNewGetDtmfTaskRejectsNonPositiveNumDigits(t *testing.T) {
	if _, err := NewGetDtmfTask(0, false); err == nil {
		t.Fatal("NewGetDtmfTask(0, false) error = nil, want invalid num_digits error")
	}
}

func TestNewGetDtmfTaskRejectsInvalidStopEvent(t *testing.T) {
	_, err := NewGetDtmfTaskWithOptions(GetDtmfOptions{
		NumDigits:     2,
		DtmfStopEvent: beta.DtmfEvent("x"),
	})
	if err == nil {
		t.Fatal("NewGetDtmfTaskWithOptions() error = nil, want invalid DTMF stop event rejected")
	}
	want := "invalid DTMF stop event: invalid DTMF event: x"
	if err.Error() != want {
		t.Fatalf("NewGetDtmfTaskWithOptions() error = %q, want %q", err, want)
	}
}

func TestGetDtmfTaskInstructionsMatchReferencePrompt(t *testing.T) {
	cases := []struct {
		name            string
		askConfirmation bool
		extra           string
		want            string
	}{
		{
			name:            "record inputs",
			askConfirmation: false,
			want: "You are a single step in a broader system, responsible solely for gathering digits input from the user. " +
				"You will either receive a sequence of digits through dtmf events tagged by <dtmf_inputs>, or " +
				"user will directly say the digits to you. You should be able to handle both cases. " +
				"If user provides the digits through voice and it is valid, call `record_inputs` with the inputs.",
		},
		{
			name:            "confirm inputs",
			askConfirmation: true,
			want: "You are a single step in a broader system, responsible solely for gathering digits input from the user. " +
				"You will either receive a sequence of digits through dtmf events tagged by <dtmf_inputs>, or " +
				"user will directly say the digits to you. You should be able to handle both cases. " +
				"Once user has confirmed the digits (by verbally spoken or entered manually), call `confirm_inputs` with the inputs.",
		},
		{
			name:            "extra instructions",
			askConfirmation: true,
			extra:           "Tell the user this is their appointment PIN.",
			want: "You are a single step in a broader system, responsible solely for gathering digits input from the user. " +
				"You will either receive a sequence of digits through dtmf events tagged by <dtmf_inputs>, or " +
				"user will directly say the digits to you. You should be able to handle both cases. " +
				"Once user has confirmed the digits (by verbally spoken or entered manually), call `confirm_inputs` with the inputs.\n" +
				"Tell the user this is their appointment PIN.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task, err := NewGetDtmfTaskWithOptions(GetDtmfOptions{
				NumDigits:          4,
				AskForConfirmation: tc.askConfirmation,
				ExtraInstructions:  tc.extra,
			})
			if err != nil {
				t.Fatalf("NewGetDtmfTaskWithOptions() error = %v", err)
			}
			if task.Instructions != tc.want {
				t.Fatalf("Instructions = %q, want %q", task.Instructions, tc.want)
			}
		})
	}
}

func TestNewGetDtmfTaskWithOptionsAppendsExtraInstructions(t *testing.T) {
	task, err := NewGetDtmfTaskWithOptions(GetDtmfOptions{
		NumDigits:          4,
		AskForConfirmation: true,
		ExtraInstructions:  "Tell the user this is their appointment PIN.",
		DtmfInputTimeout:   4 * time.Second,
		DtmfStopEvent:      beta.DtmfEventPound,
	})
	if err != nil {
		t.Fatalf("NewGetDtmfTaskWithOptions() error = %v", err)
	}

	if !strings.Contains(task.Instructions, "Tell the user this is their appointment PIN.") {
		t.Fatalf("Instructions = %q, want extra instructions", task.Instructions)
	}
	if !strings.Contains(task.Instructions, "call `confirm_inputs`") {
		t.Fatalf("Instructions = %q, want confirmation guidance preserved", task.Instructions)
	}
}

func TestGetDtmfTaskOptionsSeedReferenceChatContext(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.ChatMessage{
		ID:      "prior-dtmf",
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "I already said the first two digits are one two."}},
	})
	opts := GetDtmfOptions{NumDigits: 4}
	field := reflect.ValueOf(&opts).Elem().FieldByName("ChatContext")
	if !field.IsValid() {
		t.Fatal("GetDtmfOptions.ChatContext missing; want reference chat_ctx constructor option")
	}
	field.Set(reflect.ValueOf(chatCtx))

	task, err := NewGetDtmfTaskWithOptions(opts)
	if err != nil {
		t.Fatalf("NewGetDtmfTaskWithOptions() error = %v", err)
	}

	if task.ChatCtx == nil {
		t.Fatal("task ChatCtx = nil, want constructor chat context copy")
	}
	if task.ChatCtx == chatCtx {
		t.Fatal("task ChatCtx aliases constructor context, want reference-style copy")
	}
	if task.ChatCtx.GetByID("prior-dtmf") == nil {
		t.Fatalf("task ChatCtx items = %#v, want constructor chat item", task.ChatCtx.Items)
	}
}

func TestBuildDtmfConfirmationInstructionsMatchesReferencePrompt(t *testing.T) {
	got := buildDtmfConfirmationInstructions("1 2 3")

	if !strings.Contains(got, "<dtmf_inputs>1 2 3</dtmf_inputs>") {
		t.Fatalf("confirmation instructions = %q, want dtmf_inputs tag", got)
	}
	if !strings.Contains(got, "Please ask the user to confirm the keypad entry without reading the digits back.") {
		t.Fatalf("confirmation instructions = %q, want safe confirmation instruction", got)
	}
	for _, stale := range []string{
		"saying the digits one by one",
		"one two three four five six seven eight nine zero",
		"nine ten",
	} {
		if strings.Contains(got, stale) {
			t.Fatalf("confirmation instructions = %q, want no digit readback guidance %q", got, stale)
		}
	}
	if !strings.Contains(got, "call `confirm_inputs`") {
		t.Fatalf("confirmation instructions = %q, want confirm_inputs tool instruction", got)
	}
}

func TestGetDtmfTaskCompletesFromSessionSipDTMFEvents(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	task.Agent.Start(session, task)
	defer task.Agent.GetActivity().Stop()

	session.EmitSipDTMF(agent.SipDTMFEvent{Digit: "1", Code: 1})
	session.EmitSipDTMF(agent.SipDTMFEvent{Digit: "2", Code: 2})
	session.EmitSipDTMF(agent.SipDTMFEvent{Digit: "#", Code: 11})

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2" {
			t.Fatalf("UserInput = %q, want 1 2", result.UserInput)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for DTMF task completion")
	}
}

func TestGetDtmfTaskIgnoresInvalidSessionSipDTMFEvents(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	task.DtmfInputTimeout = time.Hour

	task.onSipDTMFReceived("1")
	task.onSipDTMFReceived("x")
	task.onSipDTMFReceived("2")
	task.onSipDTMFReceived("#")

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2" {
			t.Fatalf("UserInput = %q, want invalid SIP DTMF digit ignored", result.UserInput)
		}
	case err := <-task.Err:
		t.Fatalf("task failed with %v, want invalid SIP DTMF digit ignored", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for DTMF task completion after invalid digit")
	}
}

func TestGetDtmfTaskOnEnterGeneratesInitialReplyWithoutTools(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
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
		if ev.Source != "generate_reply" {
			t.Fatalf("SpeechCreated Source = %q, want generate_reply", ev.Source)
		}
		if ev.SpeechHandle == nil {
			t.Fatal("SpeechCreated SpeechHandle = nil, want initial reply handle")
		}
		if ev.SpeechHandle.Generation.ToolChoice != "none" {
			t.Fatalf("initial reply ToolChoice = %#v, want none", ev.SpeechHandle.Generation.ToolChoice)
		}
		if ev.SpeechHandle.Generation.UserMessage != nil {
			t.Fatalf("initial reply UserMessage = %#v, want nil", ev.SpeechHandle.Generation.UserMessage)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for DTMF initial reply")
	}
}

func TestGetDtmfTaskCancelsPendingInputsOnExit(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	task.DtmfInputTimeout = 20 * time.Millisecond

	task.onSipDTMFReceived("1")
	task.onSipDTMFReceived("2")
	task.OnExit()
	time.Sleep(2 * task.DtmfInputTimeout)

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v after exit, want pending DTMF inputs cancelled", result)
	default:
	}
	select {
	case err := <-task.Err:
		t.Fatalf("task failed with %v after exit, want pending DTMF inputs cancelled", err)
	default:
	}
}

func TestGetDtmfTaskOnExitDoesNotLeaveBlockingDTMFSubscriber(t *testing.T) {
	session := &agent.AgentSession{}
	for i := 0; i < 12; i++ {
		task := newDtmfTaskForTest(t, 2, false)
		task.Agent.Start(session, task)
		task.OnExit()
	}

	done := make(chan struct{})
	go func() {
		for i := 0; i < 25; i++ {
			session.EmitSipDTMF(agent.SipDTMFEvent{Digit: "1", Code: 1})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("EmitSipDTMF blocked after exited DTMF tasks, want reference on_exit listener cleanup")
	}
}

func TestGetDtmfTaskOnExitDropsStaleDTMFBeforeReuse(t *testing.T) {
	session := &agent.AgentSession{}
	exited := newDtmfTaskForTest(t, 2, false)
	exited.Agent.Start(session, exited)
	exited.OnExit()

	session.EmitSipDTMF(agent.SipDTMFEvent{Digit: "7", Code: 7})

	next := newDtmfTaskForTest(t, 2, false)
	next.DtmfInputTimeout = time.Hour
	next.Agent.Start(session, next)
	defer next.Agent.GetActivity().Stop()
	time.Sleep(20 * time.Millisecond)

	next.mu.Lock()
	inputs := append([]beta.DtmfEvent(nil), next.currDtmfInputs...)
	next.mu.Unlock()
	if len(inputs) != 0 {
		t.Fatalf("new DTMF task consumed stale exited-task input %#v, want no buffered DTMF reuse", inputs)
	}
}

func TestGetDtmfTaskIgnoresLateDTMFCallbackAfterExit(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	task.DtmfInputTimeout = time.Hour
	task.OnExit()

	task.onSipDTMFReceived("1")
	task.onSipDTMFReceived("2")
	task.onSipDTMFReceived("#")

	select {
	case result := <-task.Result:
		t.Fatalf("task completed from late DTMF callback after exit with %#v", result)
	case err := <-task.Err:
		t.Fatalf("task failed from late DTMF callback after exit with %v", err)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestGetDtmfTaskStopEventCancelsPendingTimer(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	task.DtmfInputTimeout = time.Hour

	task.onSipDTMFReceived("1")
	task.onSipDTMFReceived("2")

	task.mu.Lock()
	timerScheduled := task.timer != nil
	task.mu.Unlock()
	if !timerScheduled {
		t.Fatal("DTMF timer was not scheduled after keypad input")
	}

	task.onSipDTMFReceived("#")

	task.mu.Lock()
	timerStillScheduled := task.timer != nil
	task.mu.Unlock()
	if timerStillScheduled {
		t.Fatal("DTMF timer still scheduled after stop event, want pending debounce canceled")
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2" {
			t.Fatalf("UserInput = %q, want 1 2", result.UserInput)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for DTMF task completion after stop event")
	}
}

func TestGetDtmfTaskPartialInputCompletesWithToolError(t *testing.T) {
	task := newDtmfTaskForTest(t, 3, false)
	task.DtmfInputTimeout = time.Hour

	task.onSipDTMFReceived("1")
	task.onSipDTMFReceived("#")

	select {
	case err := <-task.Err:
		var toolErr llm.ToolError
		if !errors.As(err, &toolErr) {
			t.Fatalf("error = %T %v, want ToolError", err, err)
		}
		// Mirrors the Python reference ToolError text exactly for contract parity.
		want := "Digits input not fully received. Expect 3 digits, got 1"
		if toolErr.Message != want {
			t.Fatalf("ToolError.Message = %q, want %q", toolErr.Message, want)
		}
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want ToolError", result)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for partial DTMF ToolError")
	}
}

func TestGetDtmfTaskInterruptsActiveSpeechBeforeReply(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	assistant := &interruptAwareDtmfSessionAssistant{
		scheduled:   make(chan *agent.SpeechHandle, 2),
		interrupted: make(chan struct{}),
	}
	session.Assistant = assistant

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer session.Stop(context.Background())

	var initialSpeech *agent.SpeechHandle
	select {
	case initialSpeech = <-assistant.scheduled:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initial DTMF prompt speech")
	}

	session.EmitSipDTMF(agent.SipDTMFEvent{Digit: "1", Code: 1})
	session.EmitSipDTMF(agent.SipDTMFEvent{Digit: "2", Code: 2})
	session.EmitSipDTMF(agent.SipDTMFEvent{Digit: "#", Code: 11})

	select {
	case <-assistant.interrupted:
	case <-time.After(time.Second):
		t.Fatal("active DTMF prompt speech was not interrupted before keypad reply")
	}
	if !initialSpeech.IsInterrupted() {
		t.Fatal("initial DTMF prompt speech IsInterrupted = false, want true")
	}

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2" {
			t.Fatalf("UserInput = %q, want 1 2", result.UserInput)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for DTMF task completion after interrupt")
	}
}

func TestGetDtmfTaskDefersPendingReplyWhileUserSpeaking(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	task.DtmfInputTimeout = 20 * time.Millisecond
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	task.Agent.Start(session, task)
	defer task.Agent.GetActivity().Stop()

	session.EmitSipDTMF(agent.SipDTMFEvent{Digit: "1", Code: 1})
	session.EmitSipDTMF(agent.SipDTMFEvent{Digit: "2", Code: 2})
	session.UpdateUserState(agent.UserStateSpeaking)
	waitForDtmfTaskUserState(t, task, agent.UserStateSpeaking)
	time.Sleep(2 * task.DtmfInputTimeout)

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v while user was speaking, want pending input deferred", result)
	default:
	}

	session.UpdateUserState(agent.UserStateListening)

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2" {
			t.Fatalf("UserInput = %q, want 1 2", result.UserInput)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for DTMF task completion after user stopped speaking")
	}
}

func TestGetDtmfTaskDefersPendingReplyWhileAgentThinking(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	task.DtmfInputTimeout = 20 * time.Millisecond
	session := agent.NewAgentSession(task, nil, agent.AgentSessionOptions{})
	task.Agent.Start(session, task)
	defer task.Agent.GetActivity().Stop()

	session.EmitSipDTMF(agent.SipDTMFEvent{Digit: "1", Code: 1})
	session.EmitSipDTMF(agent.SipDTMFEvent{Digit: "2", Code: 2})
	session.UpdateAgentState(agent.AgentStateThinking)
	waitForDtmfTaskAgentState(t, task, agent.AgentStateThinking)
	time.Sleep(2 * task.DtmfInputTimeout)

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v while agent was thinking, want pending input deferred", result)
	default:
	}

	session.UpdateAgentState(agent.AgentStateListening)

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2" {
			t.Fatalf("UserInput = %q, want 1 2", result.UserInput)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for DTMF task completion after agent stopped thinking")
	}
}

func TestGetDtmfTaskIgnoresStateChangesWhileReplyRunning(t *testing.T) {
	task := newDtmfTaskForTest(t, 2, false)
	task.DtmfInputTimeout = 20 * time.Millisecond

	task.mu.Lock()
	task.dtmfReplyRunning = true
	task.mu.Unlock()

	task.onUserStateChanged(agent.UserStateSpeaking)
	task.onAgentStateChanged(agent.AgentStateThinking)

	task.mu.Lock()
	userState := task.userState
	agentState := task.agentState
	task.dtmfReplyRunning = false
	task.mu.Unlock()

	if userState != agent.UserStateListening {
		t.Fatalf("userState changed while DTMF reply was running: got %q, want %q", userState, agent.UserStateListening)
	}
	if agentState != agent.AgentStateInitializing {
		t.Fatalf("agentState changed while DTMF reply was running: got %q, want %q", agentState, agent.AgentStateInitializing)
	}

	task.onSipDTMFReceived("1")
	task.onSipDTMFReceived("2")

	select {
	case result := <-task.Result:
		if result.UserInput != "1 2" {
			t.Fatalf("UserInput = %q, want 1 2", result.UserInput)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for DTMF completion after ignored running-state callbacks")
	}
}

func waitForDtmfTaskUserState(t *testing.T, task *GetDtmfTask, want agent.UserState) {
	t.Helper()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		task.mu.Lock()
		got := task.userState
		task.mu.Unlock()
		if got == want {
			return
		}

		select {
		case <-deadline:
			t.Fatalf("DTMF task userState = %q, want %q", got, want)
		case <-ticker.C:
		}
	}
}

func waitForDtmfTaskAgentState(t *testing.T, task *GetDtmfTask, want agent.AgentState) {
	t.Helper()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		task.mu.Lock()
		got := task.agentState
		task.mu.Unlock()
		if got == want {
			return
		}

		select {
		case <-deadline:
			t.Fatalf("DTMF task agentState = %q, want %q", got, want)
		case <-ticker.C:
		}
	}
}

func newDtmfTaskForTest(t *testing.T, numDigits int, askConfirmation bool) *GetDtmfTask {
	t.Helper()

	task, err := NewGetDtmfTask(numDigits, askConfirmation)
	if err != nil {
		t.Fatalf("NewGetDtmfTask() error = %v", err)
	}
	return task
}

type fakeDtmfSessionAssistant struct{}

func (f *fakeDtmfSessionAssistant) Start(context.Context, *agent.AgentSession) error { return nil }
func (f *fakeDtmfSessionAssistant) OnAudioFrame(context.Context, *model.AudioFrame)  {}
func (f *fakeDtmfSessionAssistant) SetPublishAudio(func(context.Context, *model.AudioFrame) error) {
}

type interruptAwareDtmfSessionAssistant struct {
	fakeDtmfSessionAssistant
	scheduled   chan *agent.SpeechHandle
	interrupted chan struct{}
}

func (f *interruptAwareDtmfSessionAssistant) OnSpeechScheduled(ctx context.Context, speech *agent.SpeechHandle) {
	select {
	case f.scheduled <- speech:
	default:
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if speech.IsInterrupted() {
			select {
			case <-f.interrupted:
			default:
				close(f.interrupted)
			}
			speech.MarkDone()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
