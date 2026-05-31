package utils

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

func TestWaitForParticipantReturnsDisconnectedRoomError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	room := lksdk.NewRoom(nil)

	_, err := WaitForParticipant(ctx, room, "")
	if err == nil {
		t.Fatal("WaitForParticipant() error = nil, want disconnected room error")
	}
	if !strings.Contains(err.Error(), "room is not connected") {
		t.Fatalf("WaitForParticipant() error = %q, want room is not connected", err)
	}
}

func TestWaitForAgentReturnsDisconnectedRoomError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	room := lksdk.NewRoom(nil)

	_, err := WaitForAgent(ctx, room, "")
	if err == nil {
		t.Fatal("WaitForAgent() error = nil, want disconnected room error")
	}
	if !strings.Contains(err.Error(), "room is not connected") {
		t.Fatalf("WaitForAgent() error = %q, want room is not connected", err)
	}
}

func TestWaitForTrackPublicationReturnsDisconnectedRoomError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	room := lksdk.NewRoom(nil)

	_, err := WaitForTrackPublication(ctx, room, "", livekit.TrackType_AUDIO)
	if err == nil {
		t.Fatal("WaitForTrackPublication() error = nil, want disconnected room error")
	}
	if !strings.Contains(err.Error(), "room is not connected") {
		t.Fatalf("WaitForTrackPublication() error = %q, want room is not connected", err)
	}
}

func TestWaitForParticipantAttributeReturnsDisconnectedRoomError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	room := lksdk.NewRoom(nil)

	err := WaitForParticipantAttribute(ctx, room, "caller-a", "status", "ready")
	if err == nil {
		t.Fatal("WaitForParticipantAttribute() error = nil, want disconnected room error")
	}
	if !strings.Contains(err.Error(), "room is not connected") {
		t.Fatalf("WaitForParticipantAttribute() error = %q, want room is not connected", err)
	}
}

func TestAgentParticipantMatchesWithoutNameAcceptsAnyAgent(t *testing.T) {
	if !agentParticipantMatches(livekit.ParticipantInfo_AGENT, map[string]string{}, nil) {
		t.Fatal("agentParticipantMatches(agent, no name) = false, want true")
	}
}

func TestAgentParticipantMatchesExplicitEmptyNameRequiresAttribute(t *testing.T) {
	if agentParticipantMatches(livekit.ParticipantInfo_AGENT, map[string]string{}, []string{""}) {
		t.Fatal("agentParticipantMatches(agent without name, empty name) = true, want false")
	}
	if !agentParticipantMatches(livekit.ParticipantInfo_AGENT, map[string]string{AttributeAgentName: ""}, []string{""}) {
		t.Fatal("agentParticipantMatches(agent with empty name, empty name) = false, want true")
	}
}

func TestAgentParticipantMatchesRejectsNonAgent(t *testing.T) {
	if agentParticipantMatches(livekit.ParticipantInfo_STANDARD, map[string]string{}, nil) {
		t.Fatal("agentParticipantMatches(standard, no name) = true, want false")
	}
}

func TestParticipantAttributeMatchesExpectedValue(t *testing.T) {
	attrs := map[string]string{"status": "ready"}
	if !participantAttributeMatches(attrs, "status", "ready") {
		t.Fatal("participantAttributeMatches() = false, want true")
	}
	if participantAttributeMatches(attrs, "status", "waiting") {
		t.Fatal("participantAttributeMatches() = true for wrong value, want false")
	}
	if participantAttributeMatches(attrs, "missing", "ready") {
		t.Fatal("participantAttributeMatches() = true for missing attribute, want false")
	}
	if participantAttributeMatches(attrs, "missing", "") {
		t.Fatal("participantAttributeMatches() = true for missing empty attribute, want false")
	}
}

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

func TestWaitForTrackKindMatchWithoutKindsAcceptsAnyKind(t *testing.T) {
	if !trackKindMatches(livekit.TrackType_AUDIO, nil) {
		t.Fatal("trackKindMatches(AUDIO, nil) = false, want true")
	}
	if !trackKindMatches(livekit.TrackType_VIDEO, nil) {
		t.Fatal("trackKindMatches(VIDEO, nil) = false, want true")
	}
}

func TestWaitForTrackKindMatchFiltersExplicitKinds(t *testing.T) {
	kinds := []livekit.TrackType{livekit.TrackType_AUDIO}
	if !trackKindMatches(livekit.TrackType_AUDIO, kinds) {
		t.Fatal("trackKindMatches(AUDIO, [AUDIO]) = false, want true")
	}
	if trackKindMatches(livekit.TrackType_VIDEO, kinds) {
		t.Fatal("trackKindMatches(VIDEO, [AUDIO]) = true, want false")
	}
}
