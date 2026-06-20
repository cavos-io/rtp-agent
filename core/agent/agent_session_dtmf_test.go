package agent

import (
	"fmt"
	"testing"
	"time"
)

func TestAgentSessionEmitSipDTMFPublishesEvent(t *testing.T) {
	session := &AgentSession{}

	session.EmitSipDTMF(SipDTMFEvent{
		Digit:          "5",
		Code:           5,
		SenderIdentity: "caller",
	})

	select {
	case ev := <-session.SipDTMFEvents():
		if ev.Digit != "5" {
			t.Fatalf("SipDTMFEvent.Digit = %q, want 5", ev.Digit)
		}
		if ev.Code != 5 {
			t.Fatalf("SipDTMFEvent.Code = %d, want 5", ev.Code)
		}
		if ev.SenderIdentity != "caller" {
			t.Fatalf("SipDTMFEvent.SenderIdentity = %q, want caller", ev.SenderIdentity)
		}
	default:
		t.Fatal("SipDTMFEvents() did not receive emitted event")
	}
}

func TestAgentSessionSipDTMFEventsFanOutToSubscribers(t *testing.T) {
	session := &AgentSession{}
	first := session.SipDTMFEvents()
	second := session.SipDTMFEvents()

	session.EmitSipDTMF(SipDTMFEvent{
		Digit:          "9",
		Code:           9,
		SenderIdentity: "caller",
	})

	assertSipDTMFEvent(t, first, "first")
	assertSipDTMFEvent(t, second, "second")
}

func TestAgentSessionSipDTMFDeliveredWhenChannelFull(t *testing.T) {
	session := &AgentSession{}
	events := session.sipDTMFEvents()
	for i := 0; i < cap(events); i++ {
		events <- SipDTMFEvent{
			Digit:          fmt.Sprintf("%d", i%10),
			Code:           uint32(i % 10),
			SenderIdentity: "prefill",
		}
	}

	done := make(chan struct{})
	go func() {
		session.EmitSipDTMF(SipDTMFEvent{
			Digit:          "9",
			Code:           9,
			SenderIdentity: "caller",
		})
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("EmitSipDTMF returned while event channel was full; DTMF event may be dropped")
	case <-time.After(20 * time.Millisecond):
	}

	<-events
	select {
	case <-done:
	case <-testTimeout():
		t.Fatal("EmitSipDTMF did not unblock after DTMF event channel had capacity")
	}

	for {
		select {
		case ev := <-events:
			if ev.Digit == "9" && ev.Code == 9 && ev.SenderIdentity == "caller" {
				return
			}
		default:
			t.Fatal("SipDTMFEvents did not receive emitted event")
		}
	}
}

func TestAgentSessionSipDTMFDoesNotBlockWithoutSubscriber(t *testing.T) {
	session := &AgentSession{
		sipDTMFCh: make(chan SipDTMFEvent, 10),
	}
	for i := 0; i < cap(session.sipDTMFCh); i++ {
		session.sipDTMFCh <- SipDTMFEvent{
			Digit:          fmt.Sprintf("%d", i%10),
			Code:           uint32(i % 10),
			SenderIdentity: "prefill",
		}
	}

	done := make(chan struct{})
	go func() {
		session.EmitSipDTMF(SipDTMFEvent{
			Digit:          "9",
			Code:           9,
			SenderIdentity: "caller",
		})
		close(done)
	}()

	select {
	case <-done:
	case <-testTimeout():
		t.Fatal("EmitSipDTMF blocked on unclaimed DTMF event channel")
	}
}

func assertSipDTMFEvent(t *testing.T, events <-chan SipDTMFEvent, name string) {
	t.Helper()

	select {
	case ev := <-events:
		if ev.Digit != "9" || ev.Code != 9 || ev.SenderIdentity != "caller" {
			t.Fatalf("%s subscriber event = %#v, want caller digit 9", name, ev)
		}
	default:
		t.Fatalf("%s subscriber did not receive DTMF event", name)
	}
}
