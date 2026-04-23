package agent

import (
	"testing"
	"time"
)

func TestIVRActivity(t *testing.T) {
	agent := &testMockAgent{
		agent: &Agent{},
	}
	ivr := NewIVRActivity(agent)
	
	callbackCh := make(chan string, 1)
	ivr.SetDigitCallback(100*time.Millisecond, func(buffer string) (bool, error) {
		if buffer == "123" {
			callbackCh <- buffer
			return false, nil // Stop buffering
		}
		return true, nil
	})
	
	go ivr.run()
	defer ivr.Stop()
	
	ivr.OnDtmf("1")
	ivr.OnDtmf("2")
	ivr.OnDtmf("3")
	
	select {
	case result := <-callbackCh:
		if result != "123" {
			t.Errorf("Expected 123, got %s", result)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timed out waiting for IVR callback")
	}
	
	// Check if buffer is cleared
	ivr.mu.Lock()
	if ivr.buffer != "" {
		t.Errorf("Expected empty buffer, got %s", ivr.buffer)
	}
	ivr.mu.Unlock()
}
