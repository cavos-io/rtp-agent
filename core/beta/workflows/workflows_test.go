package workflows

import (
	"context"
	"strings"
	"testing"
)

func TestGetAddressTask(t *testing.T) {
	task := NewGetAddressTask(true)
	if task.RequireConfirmation != true {
		t.Error("Expected RequireConfirmation to be true")
	}

	// Test updateAddressTool
	updateTool := &updateAddressTool{task: task}
	args := &updateAddressArgs{
		StreetAddress: "123 Main St",
		UnitNumber:    "Apt 4",
		Locality:      "New York",
		Country:       "USA",
	}
	
	res, err := updateTool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	
	expectedAddress := "123 Main St Apt 4 New York USA"
	if task.currentAddress != expectedAddress {
		t.Errorf("Expected address %q, got %q", expectedAddress, task.currentAddress)
	}
	
	if !strings.Contains(res.(string), expectedAddress) {
		t.Errorf("Response should contain address, got %v", res)
	}

	// Test confirmAddressTool
	confirmTool := &confirmAddressTool{task: task}
	_, err = confirmTool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Confirm failed: %v", err)
	}
	
	if !task.addressConfirmed {
		t.Error("Expected addressConfirmed to be true")
	}
	
	select {
	case result := <-task.Result:
		if result.Address != expectedAddress {
			t.Errorf("Expected result address %q, got %q", expectedAddress, result.Address)
		}
	default:
		t.Error("Task result should have been sent")
	}
}

