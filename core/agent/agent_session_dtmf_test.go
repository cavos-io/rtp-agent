package agent

import "testing"

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
