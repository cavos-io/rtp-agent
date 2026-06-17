package livekit_test

import (
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lkprotocol "github.com/livekit/protocol/livekit"
)

func TestCreateSIPParticipantRequestUsesReferenceDefaultName(t *testing.T) {
	req := workerlivekit.CreateSIPParticipantRequest("room-a", "+15551234567", "trunk-a", "caller-a", "")

	if req.RoomName != "room-a" {
		t.Fatalf("CreateSIPParticipantRequest.RoomName = %q, want room-a", req.RoomName)
	}
	if req.ParticipantIdentity != "caller-a" {
		t.Fatalf("CreateSIPParticipantRequest.ParticipantIdentity = %q, want caller-a", req.ParticipantIdentity)
	}
	if req.SipTrunkId != "trunk-a" {
		t.Fatalf("CreateSIPParticipantRequest.SipTrunkId = %q, want trunk-a", req.SipTrunkId)
	}
	if req.SipCallTo != "+15551234567" {
		t.Fatalf("CreateSIPParticipantRequest.SipCallTo = %q, want +15551234567", req.SipCallTo)
	}
	if req.ParticipantName != "SIP-participant" {
		t.Fatalf("CreateSIPParticipantRequest.ParticipantName = %q, want SIP-participant", req.ParticipantName)
	}
}

func TestCreateSIPParticipantRequestPreservesExplicitName(t *testing.T) {
	req := workerlivekit.CreateSIPParticipantRequest("room-a", "+15551234567", "trunk-a", "caller-a", "SIP Caller")

	if req.ParticipantName != "SIP Caller" {
		t.Fatalf("CreateSIPParticipantRequest.ParticipantName = %q, want SIP Caller", req.ParticipantName)
	}
}

func TestJobCreateSIPParticipantRequestUsesJobRoomName(t *testing.T) {
	req := workerlivekit.JobCreateSIPParticipantRequest(&lkprotocol.Job{
		Room: &lkprotocol.Room{Name: "room-a"},
	}, "+15551234567", "trunk-a", "caller-a", "")

	if req.RoomName != "room-a" {
		t.Fatalf("JobCreateSIPParticipantRequest.RoomName = %q, want room-a", req.RoomName)
	}
	if req.ParticipantName != workerlivekit.DefaultSIPParticipantName {
		t.Fatalf("JobCreateSIPParticipantRequest.ParticipantName = %q, want default name", req.ParticipantName)
	}
}

func TestTransferSIPParticipantRequestMatchesReferenceFields(t *testing.T) {
	req := workerlivekit.TransferSIPParticipantRequest("room-a", "caller-a", "+15557654321", true)

	if req.RoomName != "room-a" {
		t.Fatalf("TransferSIPParticipantRequest.RoomName = %q, want room-a", req.RoomName)
	}
	if req.ParticipantIdentity != "caller-a" {
		t.Fatalf("TransferSIPParticipantRequest.ParticipantIdentity = %q, want caller-a", req.ParticipantIdentity)
	}
	if req.TransferTo != "+15557654321" {
		t.Fatalf("TransferSIPParticipantRequest.TransferTo = %q, want +15557654321", req.TransferTo)
	}
	if !req.PlayDialtone {
		t.Fatal("TransferSIPParticipantRequest.PlayDialtone = false, want true")
	}
}

func TestJobTransferSIPParticipantRequestUsesJobRoomName(t *testing.T) {
	req := workerlivekit.JobTransferSIPParticipantRequest(&lkprotocol.Job{
		Room: &lkprotocol.Room{Name: "room-a"},
	}, "caller-a", "+15557654321", false)

	if req.RoomName != "room-a" {
		t.Fatalf("JobTransferSIPParticipantRequest.RoomName = %q, want room-a", req.RoomName)
	}
	if req.ParticipantIdentity != "caller-a" {
		t.Fatalf("JobTransferSIPParticipantRequest.ParticipantIdentity = %q, want caller-a", req.ParticipantIdentity)
	}
	if req.PlayDialtone {
		t.Fatal("JobTransferSIPParticipantRequest.PlayDialtone = true, want false")
	}
}
