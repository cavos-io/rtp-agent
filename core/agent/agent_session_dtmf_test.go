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
