package tools

import (
	"context"
	"testing"

	"github.com/cavos-io/rtp-agent/core/beta"
)

type mockShutter struct {
	shutdownReason string
	deleteCalled   bool
}

func (m *mockShutter) Shutdown(reason string) { m.shutdownReason = reason }
func (m *mockShutter) DeleteRoom(ctx context.Context) error {
	m.deleteCalled = true
	return nil
}

func TestEndCallTool(t *testing.T) {
	shutter := &mockShutter{}
	tool := NewEndCallTool(shutter, EndCallToolOptions{DeleteRoom: true})
	
	if tool.ID() != "end_call" {
		t.Errorf("Expected end_call, got %s", tool.ID())
	}

	res, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if res != "Ending call..." {
		t.Errorf("Expected 'Ending call...', got %v", res)
	}

	// We can't easily wait for the goroutine in Execute without more instrumentation
	// but we've verified basic execution.
}

type mockDtmfPublisher struct {
	events []string
}

func (m *mockDtmfPublisher) PublishDTMF(code int32, digit string) error {
	m.events = append(m.events, digit)
	return nil
}

func TestSendDTMFTool(t *testing.T) {
	pub := &mockDtmfPublisher{}
	tool := NewSendDTMFTool(pub)

	if tool.Name() != "send_dtmf_events" {
		t.Errorf("Expected send_dtmf_events, got %s", tool.Name())
	}

	args := &sendDTMFArgs{
		Events: []beta.DtmfEvent{beta.DtmfEventOne, beta.DtmfEventTwo},
	}

	res, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(pub.events) != 2 {
		t.Errorf("Expected 2 events, got %d", len(pub.events))
	}
	if pub.events[0] != "1" || pub.events[1] != "2" {
		t.Errorf("Unexpected events: %v", pub.events)
	}
	
	expectedRes := "Successfully sent DTMF events: 1 2"
	if res != expectedRes {
		t.Errorf("Expected %q, got %q", expectedRes, res)
	}
}
