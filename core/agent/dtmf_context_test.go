package agent

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDTMFContextCancelsStalledSubscriber(t *testing.T) {
	s := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	events := s.SipDTMFEvents()
	for i := 0; i < 10; i++ {
		if e := s.EmitSipDTMFContext(context.Background(), SipDTMFEvent{Digit: "1", Code: 1}); e != nil {
			t.Fatal(e)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.EmitSipDTMFContext(ctx, SipDTMFEvent{Digit: "2", Code: 2}) }()
	cancel()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked")
	}
	for i := 0; i < 10; i++ {
		if ev := <-events; ev.Digit != "1" {
			t.Fatal(ev)
		}
	}
}
