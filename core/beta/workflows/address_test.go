package workflows

import (
	"context"
	"strings"
	"testing"
)

func TestGetAddressTaskRecordsAddressWithoutConfirmation(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmationSet: true})
	tool := &updateAddressTool{task: task}

	out, err := tool.Execute(context.Background(), `{"street_address":"123 Main St","unit_number":"","locality":"Springfield IL 62701","country":"United States"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "Address captured and task completed." {
		t.Fatalf("Execute() output = %q, want completion message", out)
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
	if !strings.Contains(out, `Repeat the address field by field: ["123 Main St" "Apt 4" "Springfield IL 62701" "United States"] if needed`) {
		t.Fatalf("update Execute() output = %q, want field-by-field address guidance", out)
	}
	if len(task.Agent.Tools) != 3 || task.Agent.Tools[2].Name() != "confirm_address" {
		t.Fatalf("tools = %#v, want confirm_address appended", task.Agent.Tools)
	}

	confirm := &confirmAddressTool{task: task, address: "123 Main St Apt 4 Springfield IL 62701 United States"}
	if _, err := confirm.Execute(context.Background(), `{}`); err != nil {
		t.Fatalf("confirm Execute() error = %v", err)
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

func TestGetAddressTaskCanDisableDefaultConfirmation(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{RequireConfirmation: false, RequireConfirmationSet: true})
	update := &updateAddressTool{task: task}

	out, err := update.Execute(context.Background(), `{"street_address":"123 Main St","unit_number":"","locality":"Springfield IL 62701","country":"United States"}`)
	if err != nil {
		t.Fatalf("update Execute() error = %v", err)
	}
	if out != "Address captured and task completed." {
		t.Fatalf("update Execute() output = %q, want completion message", out)
	}
}

func TestGetAddressTaskRejectsStaleConfirmation(t *testing.T) {
	task := NewGetAddressTask(GetAddressOptions{})
	update := &updateAddressTool{task: task}

	if _, err := update.Execute(context.Background(), `{"street_address":"123 Main St","unit_number":"","locality":"Springfield IL 62701","country":"United States"}`); err != nil {
		t.Fatalf("first update Execute() error = %v", err)
	}
	staleConfirm := &confirmAddressTool{task: task, address: "123 Main St Springfield IL 62701 United States"}

	if _, err := update.Execute(context.Background(), `{"street_address":"456 Oak Ave","unit_number":"","locality":"Springfield IL 62701","country":"United States"}`); err != nil {
		t.Fatalf("second update Execute() error = %v", err)
	}
	if _, err := staleConfirm.Execute(context.Background(), `{}`); err == nil {
		t.Fatal("stale confirm Execute() error = nil, want changed-address error")
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

	if _, err := tool.Execute(context.Background(), `{"reason":"user refused"}`); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	_, err := task.WaitAny(context.Background())
	if err == nil || err.Error() != "couldn't get the address: user refused" {
		t.Fatalf("WaitAny() error = %v, want decline reason", err)
	}
}
