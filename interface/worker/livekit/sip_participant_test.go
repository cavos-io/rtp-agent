package livekit_test

import (
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

func TestTransferSIPParticipantIdentityAcceptsString(t *testing.T) {
	identity, err := workerlivekit.TransferSIPParticipantIdentity("caller-a")
	if err != nil {
		t.Fatalf("TransferSIPParticipantIdentity(string) error = %v", err)
	}
	if identity != "caller-a" {
		t.Fatalf("TransferSIPParticipantIdentity(string) = %q, want caller-a", identity)
	}
}

func TestTransferSIPParticipantIdentityAcceptsSIPParticipant(t *testing.T) {
	identity, err := workerlivekit.TransferSIPParticipantIdentity(fakeRemoteParticipantView{
		identity: "caller-a",
		kind:     lksdk.ParticipantSIP,
	})
	if err != nil {
		t.Fatalf("TransferSIPParticipantIdentity(SIP participant) error = %v", err)
	}
	if identity != "caller-a" {
		t.Fatalf("TransferSIPParticipantIdentity(SIP participant) = %q, want caller-a", identity)
	}
}

func TestTransferSIPParticipantIdentityRejectsNonSIPParticipant(t *testing.T) {
	_, err := workerlivekit.TransferSIPParticipantIdentity(fakeRemoteParticipantView{
		identity: "agent-a",
		kind:     lksdk.ParticipantAgent,
	})
	if err == nil {
		t.Fatal("TransferSIPParticipantIdentity(agent participant) error = nil, want error")
	}
	if got, want := err.Error(), "Participant must be a SIP participant"; got != want {
		t.Fatalf("TransferSIPParticipantIdentity(agent participant) error = %q, want %q", got, want)
	}
}

func TestTransferSIPParticipantIdentityRejectsUnsupportedValue(t *testing.T) {
	_, err := workerlivekit.TransferSIPParticipantIdentity(42)
	if err == nil {
		t.Fatal("TransferSIPParticipantIdentity(int) error = nil, want error")
	}
	if got, want := err.Error(), "participant must be a SIP participant or identity string"; got != want {
		t.Fatalf("TransferSIPParticipantIdentity(int) error = %q, want %q", got, want)
	}
}
