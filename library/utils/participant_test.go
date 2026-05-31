package utils

import (
	"testing"

	"github.com/livekit/protocol/livekit"
)

func TestWaitForParticipantKindMatchWithoutKindsAcceptsAnyKind(t *testing.T) {
	if !participantKindMatches(livekit.ParticipantInfo_AGENT, nil) {
		t.Fatal("participantKindMatches() = false, want true when no kinds are provided")
	}
}

func TestWaitForParticipantKindMatchFiltersExplicitKind(t *testing.T) {
	if participantKindMatches(livekit.ParticipantInfo_SIP, []livekit.ParticipantInfo_Kind{livekit.ParticipantInfo_STANDARD}) {
		t.Fatal("participantKindMatches(SIP, STANDARD) = true, want false")
	}
	if !participantKindMatches(livekit.ParticipantInfo_STANDARD, []livekit.ParticipantInfo_Kind{livekit.ParticipantInfo_STANDARD}) {
		t.Fatal("participantKindMatches(STANDARD, STANDARD) = false, want true")
	}
}

func TestWaitForParticipantKindMatchAcceptsReferenceKindList(t *testing.T) {
	kinds := []livekit.ParticipantInfo_Kind{
		livekit.ParticipantInfo_CONNECTOR,
		livekit.ParticipantInfo_SIP,
		livekit.ParticipantInfo_STANDARD,
	}

	if !participantKindMatches(livekit.ParticipantInfo_SIP, kinds) {
		t.Fatal("participantKindMatches(SIP, reference kinds) = false, want true")
	}
	if participantKindMatches(livekit.ParticipantInfo_AGENT, kinds) {
		t.Fatal("participantKindMatches(AGENT, reference kinds) = true, want false")
	}
}
