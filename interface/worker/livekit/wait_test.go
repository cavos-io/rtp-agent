package livekit_test

import (
	"context"
	"strings"
	"testing"
	"time"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lkprotocol "github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

func TestWaitForParticipantUsesReferenceDefaultKinds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := workerlivekit.WaitForParticipant(ctx, lksdk.NewRoom(nil), "")
	if err == nil {
		t.Fatal("WaitForParticipant() error = nil, want disconnected room error")
	}
	if !strings.Contains(err.Error(), "room is not connected") {
		t.Fatalf("WaitForParticipant() error = %q, want room is not connected", err)
	}
}

func TestWaitForAgentDelegatesDisconnectedRoomError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := workerlivekit.WaitForAgent(ctx, lksdk.NewRoom(nil), "agent-a")
	if err == nil {
		t.Fatal("WaitForAgent() error = nil, want disconnected room error")
	}
	if !strings.Contains(err.Error(), "room is not connected") {
		t.Fatalf("WaitForAgent() error = %q, want room is not connected", err)
	}
}

func TestWaitForTrackPublicationDelegatesDisconnectedRoomError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := workerlivekit.WaitForTrackPublication(ctx, lksdk.NewRoom(nil), "caller-a", lkprotocol.TrackType_AUDIO)
	if err == nil {
		t.Fatal("WaitForTrackPublication() error = nil, want disconnected room error")
	}
	if !strings.Contains(err.Error(), "room is not connected") {
		t.Fatalf("WaitForTrackPublication() error = %q, want room is not connected", err)
	}
}

func TestWaitForTrackPublicationWithOptionsDelegatesDisconnectedRoomError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := workerlivekit.WaitForTrackPublicationWithOptions(ctx, lksdk.NewRoom(nil), workerlivekit.TrackPublicationWaitOptions{
		Identity: "caller-a",
		Kinds:    []lkprotocol.TrackType{lkprotocol.TrackType_AUDIO},
	})
	if err == nil {
		t.Fatal("WaitForTrackPublicationWithOptions() error = nil, want disconnected room error")
	}
	if !strings.Contains(err.Error(), "room is not connected") {
		t.Fatalf("WaitForTrackPublicationWithOptions() error = %q, want room is not connected", err)
	}
}

func TestWaitForParticipantAttributeDelegatesDisconnectedRoomError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := workerlivekit.WaitForParticipantAttribute(ctx, lksdk.NewRoom(nil), "caller-a", "status", "ready")
	if err == nil {
		t.Fatal("WaitForParticipantAttribute() error = nil, want disconnected room error")
	}
	if !strings.Contains(err.Error(), "room is not connected") {
		t.Fatalf("WaitForParticipantAttribute() error = %q, want room is not connected", err)
	}
}
