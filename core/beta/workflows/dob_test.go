package workflows

import (
	"context"
	"strings"
	"testing"
)

func TestGetDOBTaskRecordsPastDateWithoutConfirmation(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	out, err := tool.Execute(context.Background(), `{"year":1990,"month":1,"day":15}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "Date of birth captured and task completed." {
		t.Fatalf("Execute() output = %q, want completion message", out)
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

func TestGetDOBTaskRejectsInvalidOrFutureDate(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &updateDOBTool{task: task}

	cases := []string{
		`{"year":1990,"month":2,"day":31}`,
		`{"year":2999,"month":1,"day":1}`,
	}
	for _, args := range cases {
		_, err := tool.Execute(context.Background(), args)
		if err == nil {
			t.Fatalf("Execute(%s) error = nil, want invalid date error", args)
		}
		if !strings.Contains(err.Error(), "Invalid") {
			t.Fatalf("Execute(%s) error = %v, want invalid date", args, err)
		}
	}

	select {
	case result := <-task.Result:
		t.Fatalf("task completed with %#v, want no completion for invalid date", result)
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
	if len(task.Agent.Tools) != 3 || task.Agent.Tools[2].Name() != "confirm_dob" {
		t.Fatalf("tools = %#v, want confirm_dob appended", task.Agent.Tools)
	}

	confirm := &confirmDOBTool{task: task, dateOfBirth: task.currentDOB, timeOfBirth: task.currentTime}
	if _, err := confirm.Execute(context.Background(), `{}`); err != nil {
		t.Fatalf("confirm Execute() error = %v", err)
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

func TestGetDOBTaskUpdateTimeRequiresConfirmationGuidance(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{IncludeTime: true})
	updateTime := &updateDOBTimeTool{task: task}

	out, err := updateTime.Execute(context.Background(), `{"hour":15,"minute":30}`)
	if err != nil {
		t.Fatalf("update_time Execute() error = %v", err)
	}

	wantParts := []string{
		"The time of birth has been updated to 03:30 PM",
		"Repeat the time back to the user in a natural spoken format.",
		"Prompt the user for confirmation, do not call `confirm_dob` directly",
	}
	for _, want := range wantParts {
		if !strings.Contains(out, want) {
			t.Fatalf("update_time output = %q, want to contain %q", out, want)
		}
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

	wantParts := []string{
		"The date and time of birth has been updated to January 15, 1990 at 03:30 PM",
		"Repeat the time back to the user in a natural spoken format.",
		"Prompt the user for confirmation, do not call `confirm_dob` directly",
	}
	for _, want := range wantParts {
		if !strings.Contains(out, want) {
			t.Fatalf("update_time output = %q, want to contain %q", out, want)
		}
	}
}

func TestGetDOBTaskCanDisableDefaultConfirmation(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmation: false, RequireConfirmationSet: true})
	update := &updateDOBTool{task: task}

	out, err := update.Execute(context.Background(), `{"year":1990,"month":1,"day":15}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if out != "Date of birth captured and task completed." {
		t.Fatalf("update Execute() output = %q, want completion message", out)
	}
}

func TestGetDOBTaskInstructionsIncludeReferenceConfirmationWhenEnabled(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{})

	want := "Call `confirm_dob` after the user confirmed the date of birth is correct."
	if !strings.Contains(task.Instructions, want) {
		t.Fatalf("Instructions = %q, want reference confirmation instruction %q", task.Instructions, want)
	}
}

func TestGetDOBTaskInstructionsOmitConfirmationWhenDisabled(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmation: false, RequireConfirmationSet: true})

	if strings.Contains(task.Instructions, "confirm_dob") {
		t.Fatalf("Instructions = %q, want no confirm_dob guidance when confirmation disabled", task.Instructions)
	}
}

func TestDeclineDOBCaptureToolFailsWithReason(t *testing.T) {
	task := NewGetDOBTask(GetDOBOptions{RequireConfirmationSet: true})
	tool := &declineDOBCaptureTool{task: task}

	if _, err := tool.Execute(context.Background(), `{"reason":"user refused"}`); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	_, err := task.WaitAny(context.Background())
	if err == nil || err.Error() != "couldn't get the date of birth: user refused" {
		t.Fatalf("WaitAny() error = %v, want decline reason", err)
	}
}
