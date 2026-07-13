package workflows

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestGetDOBTaskRecordsPastDateWithoutConfirmation(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":1990,"month":1,"day":15}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// Reference returns None after no-confirm completion, avoiding extra post-completion tool chatter.
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want 1990-01-15", got)
		}
	default:
		t.Fatal("task did not complete after valid date of birth")
	}
}

func TestGetDOBTaskNormalizesSpokenDateArguments(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety","month":"January","day":"fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DOB arguments accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want spoken DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after spoken DOB")
	}
}

func TestGetDOBTaskNormalizesSingleSpokenDay(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety","month":"January","day":"single five"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken single-digit DOB day accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-05" {
			t.Fatalf("DateOfBirth = %q, want single-spoken DOB day normalized", got)
		}
	default:
		t.Fatal("task did not complete after single-spoken DOB day")
	}
}

func TestGetDOBTaskNormalizesSpokenDigitPairDay(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety","month":"January","day":"one one"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want digit-pair DOB day accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-11" {
			t.Fatalf("DateOfBirth = %q, want digit-pair DOB day normalized", got)
		}
	default:
		t.Fatal("task did not complete after digit-pair DOB day")
	}
}

func TestGetDOBTaskNormalizesSpokenDigitYear(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"one nine nine zero","month":"zero one","day":"one five"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want digit-by-digit spoken DOB accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want digit-by-digit spoken DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after digit-by-digit spoken DOB")
	}
}

func TestGetDOBTaskNormalizesSpokenDigitOweYear(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"one nine nine owe","month":"zero one","day":"one five"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want owe digit-by-digit DOB year accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want owe digit-by-digit DOB year normalized", got)
		}
	default:
		t.Fatal("task did not complete after owe digit-by-digit DOB year")
	}
}

func TestGetDOBTaskFiltersSpokenDateFieldLabels(t *testing.T) {
	for _, tc := range []struct {
		name  string
		year  string
		month string
		day   string
	}{
		{name: "bare labels", year: "year is nineteen ninety", month: "month is January", day: "day is fifteenth"},
		{name: "article labels", year: "the year is nineteen ninety", month: "the month is January", day: "the day is fifteenth"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
			tool := &updateDOBTool{task: task}

			out, err := tool.Execute(context.Background(), fmt.Sprintf(`{"year":%q,"month":%q,"day":%q}`, tc.year, tc.month, tc.day))
			if err != nil {
				t.Fatalf("Execute() error = %v, want spoken DOB field labels accepted", err)
			}
			if out != "" {
				t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
			}

			select {
			case result := <-task.Result:
				if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
					t.Fatalf("DateOfBirth = %q, want field-label DOB normalized", got)
				}
			default:
				t.Fatal("task did not complete after field-label spoken DOB")
			}
		})
	}
}

func TestGetDOBTaskFiltersSpokenDateConnectors(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety","month":"of January","day":"the fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DOB connectors accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want connector-spoken DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after connector-spoken DOB")
	}
}

func TestGetDOBTaskFiltersSpokenDateSeparators(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety","month":"January slash","day":"fifteenth slash"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DOB separators accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want separator-spoken DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after separator-spoken DOB")
	}
}

func TestGetDOBTaskFiltersBornOnSpokenPreamble(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety","month":"born on January","day":"fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want born-on spoken DOB preamble accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want born-on preamble DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after born-on spoken DOB")
	}
}

func TestGetDOBTaskFiltersWasBornOnSpokenPreamble(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety","month":"I was born on January","day":"fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want was-born-on spoken DOB preamble accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want was-born-on preamble DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after was-born-on spoken DOB")
	}
}

func TestGetDOBTaskNormalizesNineteenOhSpokenYear(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen oh five","month":"January","day":"fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want nineteen-oh spoken year accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1905-01-15" {
			t.Fatalf("DateOfBirth = %q, want nineteen-oh spoken year normalized", got)
		}
	default:
		t.Fatal("task did not complete after nineteen-oh spoken DOB")
	}
}

func TestGetDOBTaskNormalizesNineteenAughtSpokenYear(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen aught five","month":"January","day":"fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want nineteen-aught spoken year accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1905-01-15" {
			t.Fatalf("DateOfBirth = %q, want nineteen-aught spoken DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after nineteen-aught spoken DOB")
	}
}

func TestGetDOBTaskNormalizesNineteenNaughtSpokenYear(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen naught five","month":"January","day":"fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want nineteen-naught spoken year accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1905-01-15" {
			t.Fatalf("DateOfBirth = %q, want nineteen-naught spoken DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after nineteen-naught spoken DOB")
	}
}

func TestGetDOBTaskNormalizesNineteenNoughtSpokenYear(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen nought five","month":"January","day":"fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want nineteen-nought spoken year accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1905-01-15" {
			t.Fatalf("DateOfBirth = %q, want nineteen-nought spoken DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after nineteen-nought spoken DOB")
	}
}

func TestGetDOBTaskNormalizesNineteenOughtSpokenYear(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ought five","month":"January","day":"fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want nineteen-ought spoken year accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1905-01-15" {
			t.Fatalf("DateOfBirth = %q, want nineteen-ought spoken DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after nineteen-ought spoken DOB")
	}
}

func TestGetDOBTaskNormalizesNineteenOweSpokenYear(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen owe five","month":"January","day":"fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want nineteen-owe spoken year accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1905-01-15" {
			t.Fatalf("DateOfBirth = %q, want nineteen-owe spoken DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after nineteen-owe spoken DOB")
	}
}

func TestGetDOBTaskNormalizesNineteenHundredSpokenYear(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen hundred ninety","month":"January","day":"fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want nineteen-hundred spoken year accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want nineteen-hundred spoken DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after nineteen-hundred spoken DOB")
	}
}

func TestGetDOBTaskNormalizesTwoThousandSpokenYear(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"two thousand five","month":"January","day":"fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want two-thousand spoken year accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "2005-01-15" {
			t.Fatalf("DateOfBirth = %q, want two-thousand spoken year normalized", got)
		}
	default:
		t.Fatal("task did not complete after two-thousand spoken DOB")
	}
}

func TestGetDOBTaskNormalizesTwoThousandAndSpokenYear(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"two thousand and five","month":"January","day":"fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want two-thousand-and spoken year accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "2005-01-15" {
			t.Fatalf("DateOfBirth = %q, want two-thousand-and spoken year normalized", got)
		}
	default:
		t.Fatal("task did not complete after two-thousand-and spoken DOB")
	}
}

func TestGetDOBTaskNormalizesTwoThousandNinerSpokenYear(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"two thousand niner","month":"January","day":"fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want two-thousand-niner spoken year accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "2009-01-15" {
			t.Fatalf("DateOfBirth = %q, want two-thousand-niner spoken DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after two-thousand-niner spoken DOB")
	}
}

func TestGetDOBTaskNormalizesTwoThousandAughtSpokenYear(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"two thousand aught five","month":"January","day":"fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want two-thousand-aught spoken year accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "2005-01-15" {
			t.Fatalf("DateOfBirth = %q, want two-thousand-aught spoken DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after two-thousand-aught spoken DOB")
	}
}

func TestGetDOBTaskNormalizesTwentyOhSpokenYear(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"twenty oh five","month":"January","day":"fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want twenty-oh spoken year accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "2005-01-15" {
			t.Fatalf("DateOfBirth = %q, want twenty-oh spoken year normalized", got)
		}
	default:
		t.Fatal("task did not complete after twenty-oh spoken DOB")
	}
}

func TestGetDOBTaskNormalizesTwentyAughtSpokenYear(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"twenty aught five","month":"January","day":"fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want twenty-aught spoken year accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "2005-01-15" {
			t.Fatalf("DateOfBirth = %q, want twenty-aught spoken year normalized", got)
		}
	default:
		t.Fatal("task did not complete after twenty-aught spoken DOB")
	}
}

func TestGetDOBTaskNormalizesFutureTwoDigitSpokenYearToPastCentury(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"twenty nine","month":"January","day":"fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want future two-digit spoken year normalized to prior century", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		got := result.DateOfBirth.Format("2006-01-02")
		if got != "1929-01-15" {
			t.Fatalf("DateOfBirth = %q, want future two-digit spoken year normalized to 1929-01-15", got)
		}
	default:
		t.Fatal("task did not complete after future two-digit spoken DOB")
	}
}

func TestGetDOBTaskNormalizesTwoThousandCompoundSpokenYear(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"two thousand twenty one","month":"January","day":"fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want two-thousand compound spoken year accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "2021-01-15" {
			t.Fatalf("DateOfBirth = %q, want two-thousand compound spoken year normalized", got)
		}
	default:
		t.Fatal("task did not complete after two-thousand compound spoken DOB")
	}
}

func TestGetDOBTaskNormalizesWonHomophone(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"twenty won","month":"January","day":"won"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want won homophone accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "2021-01-01" {
			t.Fatalf("DateOfBirth = %q, want won homophone normalized", got)
		}
	default:
		t.Fatal("task did not complete after won-homophone DOB")
	}
}

func TestGetDOBTaskNormalizesSixHomophone(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"one nine sex five","month":"January","day":"sixth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want six homophone accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1965-01-06" {
			t.Fatalf("DateOfBirth = %q, want six homophone normalized", got)
		}
	default:
		t.Fatal("task did not complete after six-homophone DOB")
	}
}

func TestGetDOBTaskFiltersBirthdatePreamble(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"twenty three","month":"birthdate is March","day":"tree"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want birthdate preamble accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "2023-03-03" {
			t.Fatalf("DateOfBirth = %q, want birthdate preamble normalized", got)
		}
	default:
		t.Fatal("task did not complete after birthdate-preamble DOB")
	}
}

func TestGetDOBTaskFiltersContractedBirthdatePreamble(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"twenty three","month":"birthdate's March","day":"tree"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want contracted birthdate preamble accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "2023-03-03" {
			t.Fatalf("DateOfBirth = %q, want contracted birthdate preamble normalized", got)
		}
	default:
		t.Fatal("task did not complete after contracted birthdate-preamble DOB")
	}
}

func TestGetDOBTaskFiltersSplitContractedBirthdatePreamble(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"twenty three","month":"birthdate s March","day":"tree"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split contracted birthdate preamble accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "2023-03-03" {
			t.Fatalf("DateOfBirth = %q, want split contracted birthdate preamble normalized", got)
		}
	default:
		t.Fatal("task did not complete after split contracted birthdate-preamble DOB")
	}
}

func TestGetDOBTaskFiltersSpelledDOBPreamble(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"twenty three","month":"d o b is March","day":"tree"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spelled DOB preamble accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		got := result.DateOfBirth.Format("2006-01-02")
		if got != "2023-03-03" {
			t.Fatalf("DateOfBirth = %q, want spelled DOB preamble normalized", got)
		}
	default:
		t.Fatal("task did not complete after spelled-DOB preamble")
	}
}

func TestGetDOBTaskFiltersSpokenDOBLetterAliasPreamble(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"twenty three","month":"dee oh bee is March","day":"tree"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken DOB letter-alias preamble accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		got := result.DateOfBirth.Format("2006-01-02")
		if got != "2023-03-03" {
			t.Fatalf("DateOfBirth = %q, want spoken DOB letter-alias preamble normalized", got)
		}
	default:
		t.Fatal("task did not complete after spoken-DOB letter-alias preamble")
	}
}

func TestGetDOBTaskFiltersSplitBirthdayPreamble(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"twenty three","month":"my birth day is March","day":"tree"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split birthday preamble accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		got := result.DateOfBirth.Format("2006-01-02")
		if got != "2023-03-03" {
			t.Fatalf("DateOfBirth = %q, want split birthday preamble normalized", got)
		}
	default:
		t.Fatal("task did not complete after split-birthday preamble")
	}
}

func TestGetDOBTaskNormalizesOrdinalSuffixDay(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"90","month":"Jan","day":"15th"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want ordinal suffix day accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want ordinal suffix DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after ordinal suffix DOB")
	}
}

func TestGetDOBTaskNormalizesSplitOrdinalSuffixDay(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety","month":"January","day":"fifteen th"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split ordinal suffix day accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want split ordinal suffix DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after split ordinal suffix DOB")
	}
}

func TestGetDOBTaskFiltersSpokenFiller(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"um ninety","month":"uh Jan","day":"ah 15th"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want filler DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after filler-spoken DOB")
	}
}

func TestGetDOBTaskFiltersLikeSpokenFiller(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety","month":"like January","day":"like fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want like filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want like-filler DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after like-filler spoken DOB")
	}
}

func TestGetDOBTaskFiltersActuallySpokenFiller(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety","month":"actually January","day":"actually fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want correction filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want correction-filler DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after correction-filler spoken DOB")
	}
}

func TestGetDOBTaskFiltersSorrySpokenFiller(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety","month":"sorry January","day":"sorry fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want apology filler accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want apology-filler DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after apology-filler spoken DOB")
	}
}

func TestGetDOBTaskFiltersSpokenDatePreamble(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety","month":"my birthday is January","day":"fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want spoken date preamble accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want preamble-spoken DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after preamble-spoken DOB")
	}
}

func TestGetDOBTaskFiltersArticleBirthdayPreamble(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	_, err := tool.Execute(context.Background(), `{"year":"nineteen ninety","month":"the birthday is January","day":"fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want article birthday preamble accepted", err)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want article birthday preamble normalized", got)
		}
	default:
		t.Fatal("task did not complete after article-birthday-preamble DOB")
	}
}

func TestGetDOBTaskFiltersWillBeBirthdayPreamble(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	_, err := tool.Execute(context.Background(), `{"year":"nineteen ninety","month":"my birthday will be January","day":"fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want will-be birthday preamble accepted", err)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want will-be birthday preamble normalized", got)
		}
	default:
		t.Fatal("task did not complete after will-be birthday preamble")
	}
}

func TestGetDOBTaskFiltersArticleDateOfBirthPreamble(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	_, err := tool.Execute(context.Background(), `{"year":"nineteen ninety","month":"the date of birth is January","day":"fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want article date-of-birth preamble accepted", err)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want article date-of-birth preamble normalized", got)
		}
	default:
		t.Fatal("task did not complete after article-date-of-birth-preamble DOB")
	}
}

func TestGetDOBTaskFiltersBirthDatePreamble(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	_, err := tool.Execute(context.Background(), `{"year":"nineteen ninety","month":"my birth date is January","day":"fifteenth"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want birth-date preamble accepted", err)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want birth-date preamble normalized", got)
		}
	default:
		t.Fatal("task did not complete after birth-date-preamble DOB")
	}
}

func TestGetDOBTaskFiltersPunctuatedSpokenFiller(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	_, err := tool.Execute(context.Background(), `{"year":"um, ninety","month":"uh, Jan","day":"ah, 15th."}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want punctuated filler DOB accepted", err)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want punctuated filler DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after punctuated filler-spoken DOB")
	}
}

func TestGetDOBTaskFiltersTrailingSignoffFiller(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety that's it","month":"January please","day":"fifteenth that's all"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing sign-off filler DOB accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want trailing sign-off filler DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after trailing-signoff DOB")
	}
}

func TestGetDOBTaskFiltersExpandedTrailingSignoffFiller(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety that is it","month":"January please","day":"fifteenth that is all"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want expanded trailing sign-off filler DOB accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want expanded trailing sign-off filler DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after expanded-trailing-signoff DOB")
	}
}

func TestGetDOBTaskFiltersThatWillBeAllSignoffFiller(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety that'll be all","month":"January that'll be it","day":"fifteenth that'll be all"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want that'll-be trailing sign-off filler DOB accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want that'll-be trailing sign-off DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after that'll-be trailing sign-off DOB")
	}
}

func TestGetDOBTaskFiltersExpandedThatWillBeAllSignoffFiller(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety that will be all","month":"January that will be it","day":"fifteenth that will be all"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want expanded that-will-be trailing sign-off filler DOB accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want expanded that-will-be trailing sign-off DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after expanded that-will-be trailing sign-off DOB")
	}
}

func TestGetDOBTaskFiltersSplitThatllShortSignoffFiller(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety that ll be all","month":"January that ll be it","day":"fifteenth that ll be all"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split contracted trailing sign-off filler DOB accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want split contracted trailing sign-off DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after split contracted trailing sign-off DOB")
	}
}

func TestGetDOBTaskFiltersDoneSignoffFiller(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety done","month":"January done","day":"fifteenth done"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want done sign-off filler DOB accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want done sign-off filler DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after done-signoff DOB")
	}
}

func TestGetDOBTaskFiltersAllDoneSignoffFiller(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety all done","month":"January all done","day":"fifteenth all done"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want all-done sign-off filler DOB accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want all-done sign-off filler DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after all-done-signoff DOB")
	}
}

func TestGetDOBTaskFiltersTrailingForNowThanksSignoffFiller(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety that's it for now thanks","month":"January please","day":"fifteenth that's all for now thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-now-thanks sign-off filler DOB accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want trailing for-now-thanks sign-off DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after trailing for-now-thanks sign-off DOB")
	}
}

func TestGetDOBTaskFiltersTrailingForTodayThanksSignoffFiller(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety that's it for today thanks","month":"January please","day":"fifteenth that's all for today thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-today-thanks sign-off filler DOB accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want trailing for-today-thanks sign-off DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after trailing for-today-thanks sign-off DOB")
	}
}

func TestGetDOBTaskFiltersTrailingForYouSignoffFiller(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety that's all for you","month":"January please","day":"fifteenth that's it for you"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-you sign-off filler DOB accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want trailing for-you sign-off DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after trailing for-you sign-off DOB")
	}
}

func TestGetDOBTaskFiltersTrailingShortForYouSignoffFiller(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety for you thanks","month":"January please","day":"fifteenth for you thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing short for-you sign-off filler DOB accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want trailing short for-you sign-off DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after trailing short for-you sign-off DOB")
	}
}

func TestGetDOBTaskFiltersTrailingShortForTodaySignoffFiller(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety for today thanks","month":"January please","day":"fifteenth for today thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing short for-today sign-off filler DOB accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want trailing short for-today sign-off DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after trailing short for-today sign-off DOB")
	}
}

func TestGetDOBTaskFiltersTrailingShortForTheDaySignoffFiller(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety for the day thanks","month":"January please","day":"fifteenth for the day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing short for-the-day sign-off filler DOB accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want trailing short for-the-day sign-off DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after trailing short for-the-day sign-off DOB")
	}
}

func TestGetDOBTaskFiltersTrailingForTheDayThanksSignoffFiller(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety that's it for the day thanks","month":"January please","day":"fifteenth that's all for the day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want trailing for-the-day-thanks sign-off filler DOB accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want trailing for-the-day-thanks sign-off DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after trailing for-the-day-thanks sign-off DOB")
	}
}

func TestGetDOBTaskFiltersTrailingExpandedThatWillBeAllForTheDaySignoffFiller(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety that will be all for the day thanks","month":"January that will be it for the day thanks","day":"fifteenth that will be all for the day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want expanded that-will-be for-the-day sign-off filler DOB accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want expanded that-will-be for-the-day sign-off DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after expanded that-will-be for-the-day sign-off DOB")
	}
}

func TestGetDOBTaskFiltersTrailingThatllBeAllForTheDaySignoffFiller(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety that'll be all for the day thanks","month":"January that'll be it for the day thanks","day":"fifteenth that'll be all for the day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want contracted that'll-be for-the-day sign-off filler DOB accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want contracted that'll-be for-the-day sign-off DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after contracted that'll-be for-the-day sign-off DOB")
	}
}

func TestGetDOBTaskFiltersTrailingThatllBeAllForDaySignoffFiller(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety that'll be all for day thanks","month":"January that'll be it for day thanks","day":"fifteenth that'll be all for day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want contracted that'll-be for-day sign-off filler DOB accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want contracted that'll-be for-day sign-off DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after contracted that'll-be for-day sign-off DOB")
	}
}

func TestGetDOBTaskFiltersTrailingSplitThatllBeAllForDaySignoffFiller(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":"nineteen ninety that ll be all for day thanks","month":"January that ll be it for day thanks","day":"fifteenth that ll be all for day thanks"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want split contracted that'll-be for-day sign-off filler DOB accepted", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1990-01-15" {
			t.Fatalf("DateOfBirth = %q, want split contracted that'll-be for-day sign-off DOB normalized", got)
		}
	default:
		t.Fatal("task did not complete after split contracted that'll-be for-day sign-off DOB")
	}
}

func TestGetDOBTaskRejectsInvalidOrFutureDate(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	cases := []struct {
		args string
		want string
	}{
		{
			args: `{"year":1990,"month":2,"day":31}`,
			want: "Invalid date: day is out of range for month",
		},
		{
			args: `{"year":2999,"month":1,"day":1}`,
			want: "Invalid date of birth: January 01, 2999 is in the future. Date of birth cannot be a future date.",
		},
	}
	for _, args := range cases {
		_, err := tool.Execute(context.Background(), args.args)
		if err == nil {
			t.Fatalf("Execute(%s) error = nil, want invalid date error", args.args)
		}
		if err.Error() != args.want {
			t.Fatalf("Execute(%s) error = %q, want %q", args.args, err.Error(), args.want)
		}
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for invalid date", result)
	default:
	}
}

func TestGetDOBTaskOptionsSeedReferenceChatContext(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.ChatMessage{
		ID:      "prior-dob",
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "My birthday is January fifteenth nineteen ninety."}},
	})
	opts := GetDOBOptions{}
	field := reflect.ValueOf(&opts).Elem().FieldByName("ChatContext")
	if !field.IsValid() {
		t.Fatal("GetDOBOptions.ChatContext missing; want reference chat_ctx constructor option")
	}
	field.Set(reflect.ValueOf(chatCtx))

	task := NewGetDOBTask(opts)

	if task.ChatCtx == nil {
		t.Fatal("task ChatCtx = nil, want constructor chat context copy")
	}
	if task.ChatCtx == chatCtx {
		t.Fatal("task ChatCtx aliases constructor context, want reference-style copy")
	}
	if task.ChatCtx.GetByID("prior-dob") == nil {
		t.Fatalf("task ChatCtx items = %#v, want constructor chat item", task.ChatCtx.Items)
	}
}

func TestGetDOBTaskPreservesReferenceExtraTools(t *testing.T) {
	opts := GetDOBOptions{}
	field := reflect.ValueOf(&opts).Elem().FieldByName("Tools")
	if !field.IsValid() {
		t.Fatal("GetDOBOptions.Tools missing; want reference tools constructor option")
	}
	field.Set(reflect.ValueOf([]llm.Tool{referenceDOBExtraTool{id: "dob_help"}}))

	task := NewGetDOBTask(opts)

	if len(task.Agent.Tools) < 2 {
		t.Fatalf("tools len = %d, want extra tool before update_dob", len(task.Agent.Tools))
	}
	if got := task.Agent.Tools[0].Name(); got != "dob_help" {
		t.Fatalf("tools[0] = %q, want caller-provided tool preserved first", got)
	}
	if got := task.Agent.Tools[1].Name(); got != "update_dob" {
		t.Fatalf("tools[1] = %q, want update_dob after caller tools", got)
	}
}

func TestGetDOBTaskExplicitAskIgnoresUpdateToolOnEnter(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireExplicitAsk: true})
	tool := task.Agent.Tools[0]

	if !llm.ToolHasFlag(tool, llm.ToolFlagIgnoreOnEnter) {
		t.Fatalf("%s ToolFlags missing ToolFlagIgnoreOnEnter when RequireExplicitAsk is true", tool.Name())
	}
}

func TestGetDOBTaskUpdateToolUsesReferenceSchema(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{})
	tool := task.Agent.Tools[0]

	wantDescription := "Update the date of birth provided by the user. Given a spoken month and year (e.g., 'July 2030'), return its numerical representation (7/2030)."
	if got := tool.Description(); got != wantDescription {
		t.Fatalf("update_dob description = %q, want %q", got, wantDescription)
	}
	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", tool.Parameters()["properties"])
	}
	cases := map[string]string{
		"year":  "The birth year (e.g., 1990)",
		"month": "The birth month (1-12)",
		"day":   "The birth day (1-31)",
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

func TestGetDOBTaskUpdateTimeToolUsesReferenceSchema(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{IncludeTime: true})
	tool := task.Agent.Tools[2]

	wantDescription := "Update the time of birth provided by the user."
	if got := tool.Description(); got != wantDescription {
		t.Fatalf("update_time description = %q, want %q", got, wantDescription)
	}
	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", tool.Parameters()["properties"])
	}
	cases := map[string]string{
		"hour":   "The birth hour (0-23)",
		"minute": "The birth minute (0-59)",
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

func TestGetDOBTaskRejectsInvalidTimeWithReferenceErrors(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{IncludeTime: true})
	tool := &updateDOBTimeTool{task: task}

	cases := []struct {
		args string
		want string
	}{
		{
			args: `{"hour":24,"minute":0}`,
			want: "Invalid time: hour must be in 0..23",
		},
		{
			args: `{"hour":12,"minute":60}`,
			want: "Invalid time: minute must be in 0..59",
		},
	}
	for _, tc := range cases {
		_, err := tool.Execute(context.Background(), tc.args)
		if err == nil {
			t.Fatalf("Execute(%s) error = nil, want invalid time error", tc.args)
		}
		if err.Error() != tc.want {
			t.Fatalf("Execute(%s) error = %q, want %q", tc.args, err.Error(), tc.want)
		}
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for invalid time", result)
	default:
	}
}

func TestGetDOBTaskRequiresConfirmation(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{})
	update := &updateDOBTool{task: task}

	out, err := update.Execute(context.Background(), `{"year":1985,"month":7,"day":4}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if out == "" {
		t.Fatal("update Execute() output is empty, want confirmation guidance")
	}
	want := "Ask the user to confirm the updated date of birth without repeating it back."
	if !strings.Contains(out, want) {
		t.Fatalf("update Execute() output = %q, want %q", out, want)
	}
	if strings.Contains(out, "Repeat the date back to the user") {
		t.Fatalf("update Execute() output = %q, want no date readback guidance", out)
	}
	if len(task.Agent.Tools) != 3 || task.Agent.Tools[2].Name() != "confirm_dob" {
		t.Fatalf("tools = %#v, want confirm_dob appended", task.Agent.Tools)
	}

	confirm := &confirmDOBTool{task: task, dateOfBirth: task.currentDOB, timeOfBirth: task.currentTime}
	confirmOut, err := confirm.Execute(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("confirm Execute() error = %v", err)
	}
	if confirmOut != "" {
		t.Fatalf("confirm Execute() output = %q, want empty output after completion", confirmOut)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1985-07-04" {
			t.Fatalf("DateOfBirth = %q, want 1985-07-04", got)
		}
	default:
		t.Fatal("task did not complete after confirmation")
	}
}

func TestGetDOBTaskDefaultConfirmationUsesInputModality(t *testing.T) {
	textCtx := agent.WithRunContext(
		context.Background(),
		agent.NewRunContext(nil, agent.NewSpeechHandle(true, agent.InputDetails{Modality: "text"}), nil),
	)
	task := NewGetDOBTask(GetDOBOptions{})
	update := &updateDOBTool{task: task}

	out, err := update.Execute(textCtx, `{"year":1985,"month":7,"day":4}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("update Execute() output = %q, want empty output for default text confirmation", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1985-07-04" {
			t.Fatalf("DateOfBirth = %q, want 1985-07-04", got)
		}
	default:
		t.Fatal("task did not complete after text date of birth update")
	}
}

func TestGetDOBTaskIncludesOptionalTime(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{IncludeTime: true, RequireConfirmationSet: true})

	var updateTime *updateDOBTimeTool
	for _, tool := range task.Agent.Tools {
		if typed, ok := tool.(*updateDOBTimeTool); ok {
			updateTime = typed
		}
	}
	if updateTime == nil {
		t.Fatal("update_time tool was not installed when IncludeTime is true")
	}
	if _, err := updateTime.Execute(context.Background(), `{"hour":6,"minute":30}`); err != nil {
		t.Fatalf("update_time Execute() error = %v", err)
	}

	updateDOB := &updateDOBTool{task: task}
	if _, err := updateDOB.Execute(context.Background(), `{"year":1992,"month":3,"day":8}`); err != nil {
		t.Fatalf("update_dob Execute() error = %v", err)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1992-03-08" {
			t.Fatalf("DateOfBirth = %q, want 1992-03-08", got)
		}
		if result.TimeOfBirth == nil || result.TimeOfBirth.Format("15:04") != "06:30" {
			t.Fatalf("TimeOfBirth = %v, want 06:30", result.TimeOfBirth)
		}
	default:
		t.Fatal("task did not complete after valid date and time of birth")
	}
}

func TestGetDOBTaskUpdateTimeNormalizesSpokenArguments(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{IncludeTime: true})
	updateTime := &updateDOBTimeTool{task: task}

	out, err := updateTime.Execute(context.Background(), `{"hour":"um, three","minute":"oh five."}`)
	if err != nil {
		t.Fatalf("update_time Execute() error = %v, want spoken time arguments accepted", err)
	}
	if task.currentTime == nil || task.currentTime.Format("15:04") != "03:05" {
		t.Fatalf("currentTime = %v, want spoken time normalized to 03:05", task.currentTime)
	}
	if !strings.Contains(out, "The time of birth has been updated.") {
		t.Fatalf("update_time output = %q, want generic time update guidance", out)
	}
}

func TestGetDOBTaskUpdateTimeNormalizesSpokenPM(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{IncludeTime: true})
	updateTime := &updateDOBTimeTool{task: task}

	out, err := updateTime.Execute(context.Background(), `{"hour":"three p m","minute":"thirty"}`)
	if err != nil {
		t.Fatalf("update_time Execute() error = %v, want spoken PM time accepted", err)
	}
	if task.currentTime == nil || task.currentTime.Format("15:04") != "15:30" {
		t.Fatalf("currentTime = %v, want spoken PM normalized to 15:30", task.currentTime)
	}
	if !strings.Contains(out, "The time of birth has been updated.") {
		t.Fatalf("update_time output = %q, want generic time update guidance", out)
	}
}

func TestGetDOBTaskUpdateTimeNormalizesMinuteMeridiem(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{IncludeTime: true})
	updateTime := &updateDOBTimeTool{task: task}

	out, err := updateTime.Execute(context.Background(), `{"hour":"three","minute":"thirty p m"}`)
	if err != nil {
		t.Fatalf("update_time Execute() error = %v, want spoken PM time accepted", err)
	}
	if task.currentTime == nil || task.currentTime.Format("15:04") != "15:30" {
		t.Fatalf("currentTime = %v, want spoken PM normalized to 15:30", task.currentTime)
	}
	if !strings.Contains(out, "The time of birth has been updated.") {
		t.Fatalf("update_time output = %q, want generic time update guidance", out)
	}
}

func TestGetDOBTaskUpdateTimeFiltersOClockSuffix(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{IncludeTime: true})
	updateTime := &updateDOBTimeTool{task: task}

	out, err := updateTime.Execute(context.Background(), `{"hour":"three o clock p m","minute":"zero"}`)
	if err != nil {
		t.Fatalf("update_time Execute() error = %v, want spoken o'clock time accepted", err)
	}
	if task.currentTime == nil || task.currentTime.Format("15:04") != "15:00" {
		t.Fatalf("currentTime = %v, want spoken o'clock time normalized to 15:00", task.currentTime)
	}
	if !strings.Contains(out, "The time of birth has been updated.") {
		t.Fatalf("update_time output = %q, want generic time update guidance", out)
	}
}

func TestGetDOBTaskUpdateTimeFiltersAtNightMinuteMeridiem(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{IncludeTime: true})
	updateTime := &updateDOBTimeTool{task: task}

	out, err := updateTime.Execute(context.Background(), `{"hour":"three","minute":"thirty at night"}`)
	if err != nil {
		t.Fatalf("update_time Execute() error = %v, want spoken night time accepted", err)
	}
	if task.currentTime == nil || task.currentTime.Format("15:04") != "15:30" {
		t.Fatalf("currentTime = %v, want spoken night time normalized to 15:30", task.currentTime)
	}
	if !strings.Contains(out, "The time of birth has been updated.") {
		t.Fatalf("update_time output = %q, want generic time update guidance", out)
	}
}

func TestGetDOBTaskUpdateTimeFiltersSpokenTimePreamble(t *testing.T) {
	for _, tc := range []struct {
		name string
		hour string
	}{
		{name: "time is", hour: "time is three p m"},
		{name: "the time is", hour: "the time is three p m"},
		{name: "time of birth is", hour: "time of birth is three p m"},
		{name: "birth time is", hour: "birth time is three p m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := NewGetDOBTask(GetDOBOptions{IncludeTime: true})
			updateTime := &updateDOBTimeTool{task: task}

			out, err := updateTime.Execute(context.Background(), fmt.Sprintf(`{"hour":%q,"minute":"oh five"}`, tc.hour))
			if err != nil {
				t.Fatalf("update_time Execute() error = %v, want spoken time preamble accepted", err)
			}
			if task.currentTime == nil || task.currentTime.Format("15:04") != "15:05" {
				t.Fatalf("currentTime = %v, want spoken PM time normalized to 15:05", task.currentTime)
			}
			if !strings.Contains(out, "The time of birth has been updated.") {
				t.Fatalf("update_time output = %q, want generic time update guidance", out)
			}
		})
	}
}

func TestGetDOBTaskUpdateTimeNormalizesSpokenAfternoonPhrase(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{IncludeTime: true})
	updateTime := &updateDOBTimeTool{task: task}

	out, err := updateTime.Execute(context.Background(), `{"hour":"three in the afternoon","minute":"thirty"}`)
	if err != nil {
		t.Fatalf("update_time Execute() error = %v, want spoken afternoon time accepted", err)
	}
	if task.currentTime == nil || task.currentTime.Format("15:04") != "15:30" {
		t.Fatalf("currentTime = %v, want spoken afternoon time normalized to 15:30", task.currentTime)
	}
	if !strings.Contains(out, "The time of birth has been updated.") {
		t.Fatalf("update_time output = %q, want generic time update guidance", out)
	}
}

func TestGetDOBTaskUpdateTimeNormalizesNaturalSpokenTimePhrase(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{IncludeTime: true})
	updateTime := &updateDOBTimeTool{task: task}

	out, err := updateTime.Execute(context.Background(), `{"hour":"quarter past three in the afternoon","minute":"zero"}`)
	if err != nil {
		t.Fatalf("update_time Execute() error = %v, want natural spoken time phrase accepted", err)
	}
	if task.currentTime == nil || task.currentTime.Format("15:04") != "15:15" {
		t.Fatalf("currentTime = %v, want natural spoken time normalized to 15:15", task.currentTime)
	}
	if !strings.Contains(out, "The time of birth has been updated.") {
		t.Fatalf("update_time output = %q, want generic time update guidance", out)
	}
}

func TestGetDOBTaskUpdateTimeNormalizesQuarterPastNoon(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{IncludeTime: true})
	updateTime := &updateDOBTimeTool{task: task}

	out, err := updateTime.Execute(context.Background(), `{"hour":"quarter past noon","minute":"zero"}`)
	if err != nil {
		t.Fatalf("update_time Execute() error = %v, want quarter-past-noon time accepted", err)
	}
	if task.currentTime == nil || task.currentTime.Format("15:04") != "12:15" {
		t.Fatalf("currentTime = %v, want quarter-past-noon normalized to 12:15", task.currentTime)
	}
	if !strings.Contains(out, "The time of birth has been updated.") {
		t.Fatalf("update_time output = %q, want generic time update guidance", out)
	}
}

func TestGetDOBTaskUpdateTimeNormalizesSpokenBoundaryWords(t *testing.T) {
	for _, tc := range []struct {
		name string
		args string
		want string
	}{
		{name: "midnight", args: `{"hour":"midnight","minute":"zero"}`, want: "00:00"},
		{name: "noon", args: `{"hour":"noon","minute":"zero"}`, want: "12:00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := NewGetDOBTask(GetDOBOptions{IncludeTime: true})
			updateTime := &updateDOBTimeTool{task: task}

			if _, err := updateTime.Execute(context.Background(), tc.args); err != nil {
				t.Fatalf("update_time Execute() error = %v, want spoken boundary time accepted", err)
			}
			if task.currentTime == nil || task.currentTime.Format("15:04") != tc.want {
				t.Fatalf("currentTime = %v, want spoken boundary time normalized to %s", task.currentTime, tc.want)
			}
		})
	}
}

func TestGetDOBTaskUpdateTimeCompletesWithoutToolOutputWhenConfirmationDisabled(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{IncludeTime: true})
	updateDOB := &updateDOBTool{task: task}
	updateTime := &updateDOBTimeTool{task: task}

	if _, err := updateDOB.Execute(context.Background(), `{"year":1992,"month":3,"day":8}`); err != nil {
		t.Fatalf("update_dob Execute() error = %v", err)
	}
	task.RequireConfirmation = false

	out, err := updateTime.Execute(context.Background(), `{"hour":6,"minute":30}`)
	if err != nil {
		t.Fatalf("update_time Execute() error = %v", err)
	}
	// Reference returns None after no-confirm completion, avoiding extra post-completion tool chatter.
	if out != "" {
		t.Fatalf("update_time output = %q, want empty output after no-confirm completion", out)
	}

	select {
	case result := <-task.Result:
		if got := result.DateOfBirth.Format("2006-01-02"); got != "1992-03-08" {
			t.Fatalf("DateOfBirth = %q, want 1992-03-08", got)
		}
		if result.TimeOfBirth == nil || result.TimeOfBirth.Format("15:04") != "06:30" {
			t.Fatalf("TimeOfBirth = %v, want 06:30", result.TimeOfBirth)
		}
	default:
		t.Fatal("task did not complete after valid date and time of birth")
	}
}

func TestGetDOBTaskIncludeTimeInstructionsPrecedeUpdateToolGuidance(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{IncludeTime: true})

	timeInstruction := "Also ask for and capture the time of birth if the user knows it. The time is optional - if the user doesn't know it, proceed without it."
	updateInstruction := "Call `update_dob` at the first opportunity whenever you form a new hypothesis about the date of birth. (before asking any questions or providing any answers.)"
	timeIndex := strings.Index(task.Instructions, timeInstruction)
	if timeIndex < 0 {
		t.Fatalf("Instructions = %q, want optional-time instruction %q", task.Instructions, timeInstruction)
	}
	updateIndex := strings.Index(task.Instructions, updateInstruction)
	if updateIndex < 0 {
		t.Fatalf("Instructions = %q, want update guidance %q", task.Instructions, updateInstruction)
	}
	if timeIndex > updateIndex {
		t.Fatalf("optional-time instruction appears after update guidance in %q", task.Instructions)
	}
}

func TestGetDOBTaskUpdateTimeRequiresConfirmationGuidance(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{IncludeTime: true})
	updateTime := &updateDOBTimeTool{task: task}

	out, err := updateTime.Execute(context.Background(), `{"hour":15,"minute":30}`)
	if err != nil {
		t.Fatalf("update_time Execute() error = %v", err)
	}

	wantParts := []string{
		"The time of birth has been updated.",
		"Ask the user to confirm the updated time of birth without repeating it back.",
		"Prompt the user for confirmation, do not call `confirm_dob` directly",
	}
	for _, want := range wantParts {
		if !strings.Contains(out, want) {
			t.Fatalf("update_time output = %q, want to contain %q", out, want)
		}
	}
	if strings.Contains(out, "The time of birth has been updated to 03:30 PM") {
		t.Fatalf("update_time output = %q, want no raw time echo in update status", out)
	}
	if strings.Contains(out, "Repeat the time back to the user") {
		t.Fatalf("update_time output = %q, want no time readback guidance", out)
	}
}

func TestGetDOBTaskUpdateTimeWithDateRequiresConfirmationGuidance(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{IncludeTime: true})
	updateDOB := &updateDOBTool{task: task}
	updateTime := &updateDOBTimeTool{task: task}

	if _, err := updateDOB.Execute(context.Background(), `{"year":1990,"month":1,"day":15}`); err != nil {
		t.Fatalf("update_dob Execute() error = %v", err)
	}
	out, err := updateTime.Execute(context.Background(), `{"hour":15,"minute":30}`)
	if err != nil {
		t.Fatalf("update_time Execute() error = %v", err)
	}

	if strings.Contains(out, "Repeat the time back to the user") {
		t.Fatalf("update_time output = %q, want no time readback guidance", out)
	}
	wantParts := []string{
		"The date and time of birth has been updated.",
		"Ask the user to confirm the updated date and time of birth without repeating it back.",
		"Prompt the user for confirmation, do not call `confirm_dob` directly",
	}
	for _, want := range wantParts {
		if !strings.Contains(out, want) {
			t.Fatalf("update_time output = %q, want to contain %q", out, want)
		}
	}
	if strings.Contains(out, "The date and time of birth has been updated to January 15, 1990 at 03:30 PM") {
		t.Fatalf("update_time output = %q, want no raw date/time echo in update status", out)
	}
	if strings.Contains(out, "Repeat the date and time back to the user") {
		t.Fatalf("update_time output = %q, want no date/time readback guidance", out)
	}
}

func TestGetDOBTaskUpdateDateWithExistingTimeRequiresConfirmationGuidance(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{IncludeTime: true})
	updateTime := &updateDOBTimeTool{task: task}
	updateDOB := &updateDOBTool{task: task}

	if _, err := updateTime.Execute(context.Background(), `{"hour":15,"minute":30}`); err != nil {
		t.Fatalf("update_time Execute() error = %v", err)
	}
	out, err := updateDOB.Execute(context.Background(), `{"year":1990,"month":1,"day":15}`)
	if err != nil {
		t.Fatalf("update_dob Execute() error = %v", err)
	}

	want := "The date of birth has been updated."
	if !strings.Contains(out, want) {
		t.Fatalf("update_dob output = %q, want generic DOB update guidance %q", out, want)
	}
	if strings.Contains(out, "The date of birth has been updated to January 15, 1990 at 03:30 PM") {
		t.Fatalf("update_dob output = %q, want no raw date/time echo in update status", out)
	}
	wantConfirm := "Ask the user to confirm the updated date and time of birth without repeating it back."
	if !strings.Contains(out, wantConfirm) {
		t.Fatalf("update_dob output = %q, want date-and-time confirmation guidance %q", out, wantConfirm)
	}
	if strings.Contains(out, "Repeat the date and time back to the user") {
		t.Fatalf("update_dob output = %q, want no date/time readback guidance", out)
	}
}

func TestGetDOBTaskCanDisableDefaultConfirmation(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmation: false, RequireConfirmationSet: true})
	update := &updateDOBTool{task: task}

	out, err := update.Execute(context.Background(), `{"year":1990,"month":1,"day":15}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("update Execute() output = %q, want empty output after no-confirm completion", out)
	}
}

func TestGetDOBTaskInstructionsIncludeReferenceConfirmationWhenEnabled(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{})

	wantParts := []string{
		"Handle input as noisy voice transcription. Expect that users will say dates aloud with formats like:",
		"'the fifteenth of January nineteen ninety'",
		"Convert spoken numbers and ordinals to their numeric form: 'fifteenth'",
		"Handle two-digit years appropriately: '90' likely means 1990, '05' likely means 2005.",
		"Don't mention corrections. Treat inputs as possibly imperfect but fix them silently.",
		"Call `update_dob` at the first opportunity whenever you form a new hypothesis about the date of birth. (before asking any questions or providing any answers.)",
		"Call `confirm_dob` after the user confirmed the date of birth is correct.",
		"Avoid verbosity by not sharing example dates or formats unless prompted to do so. Do not deviate from the goal of collecting the user's birthday.",
		"Always explicitly invoke a tool when applicable. Do not simulate tool usage, no real action is taken unless the tool is explicitly called.",
	}
	for _, want := range wantParts {
		if !strings.Contains(task.Instructions, want) {
			t.Fatalf("Instructions = %q, want reference instruction %q", task.Instructions, want)
		}
	}
}

func TestGetDOBTaskInstructionsUseReferenceSpokenDateGuidance(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{})

	for _, want := range []string{
		"Convert spoken numbers and ordinals to their numeric form: 'fifteenth' → 15, 'ninety' → 1990.",
		"When reading back dates, use a natural spoken format like 'January fifteenth, nineteen ninety'.",
	} {
		if !strings.Contains(task.Instructions, want) {
			t.Fatalf("Instructions = %q, want spoken date guidance %q", task.Instructions, want)
		}
	}
}

func TestGetDOBTaskInstructionsPreserveReferenceModalityVariants(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{})
	if task.InstructionVariants == nil {
		t.Fatal("InstructionVariants = nil, want reference audio/text instruction variants")
	}
	audio := task.InstructionVariants.AsModality("audio").String()
	text := task.InstructionVariants.AsModality("text").String()

	for _, want := range []string{
		"Handle input as noisy voice transcription. Expect that users will say dates aloud with formats like:",
		"Convert spoken numbers and ordinals to their numeric form: 'fifteenth' → 15, 'ninety' → 1990.",
		"Call `confirm_dob` after the user confirmed the date of birth is correct.",
	} {
		if !strings.Contains(audio, want) {
			t.Fatalf("audio instructions = %q, want reference audio guidance %q", audio, want)
		}
	}
	for _, want := range []string{
		"Handle input as typed text. Expect users to type their date of birth directly.",
		"Accept common date formats like 'MM/DD/YYYY', 'January 15, 1990', or '1990-01-15'.",
		"Handle two-digit years appropriately: '90' likely means 1990, '05' likely means 2005.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("text instructions = %q, want reference text guidance %q", text, want)
		}
	}
	for _, stale := range []string{
		"Handle input as noisy voice transcription.",
		"Call `confirm_dob` after the user confirmed the date of birth is correct.",
	} {
		if strings.Contains(text, stale) {
			t.Fatalf("text instructions = %q, want no stale audio/default confirmation guidance %q", text, stale)
		}
	}
}

func TestGetDOBTaskInstructionsOmitConfirmationWhenDisabled(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmation: false, RequireConfirmationSet: true})

	if strings.Contains(task.Instructions, "confirm_dob") {
		t.Fatalf("Instructions = %q, want no confirm_dob guidance when confirmation disabled", task.Instructions)
	}
}

func TestGetDOBTaskOnEnterUsesReferencePrompt(t *testing.T) {
	const want = "Ask the user to provide their date of birth."

	task := NewGetDOBTask(GetDOBOptions{})
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
		t.Fatal("timed out waiting for DOB on-enter prompt")
	}
}

func TestGetDOBTaskStaleConfirmationPromptsForUpdatedDate(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{})
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
		t.Fatal("timed out waiting for DOB on-enter prompt")
	}

	update := &updateDOBTool{task: task}
	if _, err := update.Execute(context.Background(), `{"year":1985,"month":7,"day":4}`); err != nil {
		t.Fatalf("first update Execute() error = %v", err)
	}
	staleConfirm := &confirmDOBTool{task: task, dateOfBirth: task.currentDOB, timeOfBirth: task.currentTime}

	if _, err := update.Execute(context.Background(), `{"year":1990,"month":1,"day":15}`); err != nil {
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
		want := dobStaleConfirmationPrompt()
		if ev.SpeechHandle.Generation.Instructions == nil {
			t.Fatal("stale confirmation instructions = nil, want changed-DOB prompt")
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

func TestGetDOBTaskConfirmWithoutDatePromptsForDate(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{IncludeTime: true})
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
		t.Fatal("timed out waiting for DOB on-enter prompt")
	}

	updateTime := &updateDOBTimeTool{task: task}
	if _, err := updateTime.Execute(context.Background(), `{"hour":15,"minute":30}`); err != nil {
		t.Fatalf("update_time Execute() error = %v", err)
	}
	confirm := &confirmDOBTool{task: task, dateOfBirth: nil, timeOfBirth: task.currentTime}

	if _, err := confirm.Execute(context.Background(), `{}`); err != nil {
		t.Fatalf("confirm Execute() error = %v, want nil after prompting for date", err)
	}

	select {
	case ev := <-speechEvents:
		if ev.Source != "generate_reply" {
			t.Fatalf("SpeechCreated Source = %q, want generate_reply", ev.Source)
		}
		if ev.SpeechHandle == nil {
			t.Fatal("SpeechCreated SpeechHandle = nil, want missing-date reply handle")
		}
		want := "No date of birth was provided yet, ask the user to provide it."
		if ev.SpeechHandle.Generation.Instructions == nil {
			t.Fatal("missing-date instructions = nil, want date prompt")
		}
		if got := ev.SpeechHandle.Generation.Instructions.AsModality("text").String(); got != want {
			t.Fatalf("missing-date instructions = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for missing-date prompt")
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion without date", result)
	default:
	}
}

func TestDeclineDOBCaptureToolFailsWithReason(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &declineDOBCaptureTool{task: task}

	out, err := tool.Execute(context.Background(), `{"reason":"user refused"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after decline", out)
	}
	_, err = task.WaitAny(context.Background())
	if err == nil || err.Error() != "couldn't get the date of birth: user refused" {
		t.Fatalf("WaitAny() error = %v, want decline reason", err)
	}
}

func TestDeclineDOBCaptureToolUsesRunContextCurrentAgent(t *testing.T) {
	staleTask := NewGetDOBTask(GetDOBOptions{})
	currentTask := NewGetDOBTask(GetDOBOptions{})
	session := agent.NewAgentSession(currentTask, nil, agent.AgentSessionOptions{})
	ctx := agent.WithRunContext(context.Background(), agent.NewRunContext(session, nil, nil))
	tool := &declineDOBCaptureTool{task: staleTask}

	out, err := tool.Execute(ctx, `{"reason":"user refused current dob"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output after decline", out)
	}

	select {
	case err := <-currentTask.Err:
		if err == nil || err.Error() != "couldn't get the date of birth: user refused current dob" {
			t.Fatalf("current task error = %v, want decline reason", err)
		}
	case <-time.After(time.Second):
		t.Fatal("current task did not fail after decline_dob_capture")
	}

	select {
	case err := <-staleTask.Err:
		t.Fatalf("stale task failed with %v, want decline routed to current agent", err)
	default:
	}
}

func TestDeclineDOBCaptureToolUsesReferenceSchema(t *testing.T) {
	tool := &declineDOBCaptureTool{task: NewGetDOBTask(GetDOBOptions{})}

	wantDescription := "Handles the case when the user explicitly declines to provide a date of birth."
	if got := tool.Description(); got != wantDescription {
		t.Fatalf("decline_dob_capture description = %q, want %q", got, wantDescription)
	}
	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", tool.Parameters()["properties"])
	}
	reason, ok := properties["reason"].(map[string]any)
	if !ok {
		t.Fatalf("reason schema = %#v, want map", properties["reason"])
	}
	wantParam := "A short explanation of why the user declined to provide the date of birth"
	if got := reason["description"]; got != wantParam {
		t.Fatalf("reason description = %#v, want %q", got, wantParam)
	}
}

type referenceDOBExtraTool struct {
	id string
}

func (t referenceDOBExtraTool) ID() string {
	return t.id
}

func (t referenceDOBExtraTool) Name() string {
	return t.id
}

func (t referenceDOBExtraTool) Description() string {
	return "reference extra DOB tool"
}

func (t referenceDOBExtraTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t referenceDOBExtraTool) Execute(ctx context.Context, args string) (string, error) {
	return "", nil
}
