package livekit_test

import (
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lkprotocol "github.com/livekit/protocol/livekit"
)

func TestAvatarStartInfoExposesLiveKitConnection(t *testing.T) {
	info := workerlivekit.AvatarStartInfo(workerlivekit.AvatarStartInfoOptions{
		URL:           "wss://livekit.example",
		Token:         "room-token",
		AgentIdentity: "agent-job_avatar",
	})

	if info.LiveKitURL != "wss://livekit.example" {
		t.Fatalf("LiveKitURL = %q, want job URL", info.LiveKitURL)
	}
	if info.LiveKitToken != "room-token" {
		t.Fatalf("LiveKitToken = %q, want job token", info.LiveKitToken)
	}
	if info.RoomName != "" {
		t.Fatalf("RoomName = %q, want empty without room info", info.RoomName)
	}
	if info.AgentIdentity != "agent-job_avatar" {
		t.Fatalf("AgentIdentity = %q, want default local participant identity", info.AgentIdentity)
	}
}

func TestAvatarStartInfoExposesRoomName(t *testing.T) {
	info := workerlivekit.AvatarStartInfo(workerlivekit.AvatarStartInfoOptions{
		URL:           "wss://livekit.example",
		Token:         "room-token",
		RoomName:      "support-room",
		AgentIdentity: "agent-job_avatar",
	})

	if info.RoomName != "support-room" {
		t.Fatalf("RoomName = %q, want job room name", info.RoomName)
	}
}

func TestJobAvatarStartInfoExtractsRoomName(t *testing.T) {
	info := workerlivekit.JobAvatarStartInfo(&lkprotocol.Job{
		Room: &lkprotocol.Room{Name: "support-room"},
	}, "wss://livekit.example", "room-token", "agent-job_avatar")

	if info.LiveKitURL != "wss://livekit.example" {
		t.Fatalf("LiveKitURL = %q, want job URL", info.LiveKitURL)
	}
	if info.LiveKitToken != "room-token" {
		t.Fatalf("LiveKitToken = %q, want job token", info.LiveKitToken)
	}
	if info.RoomName != "support-room" {
		t.Fatalf("RoomName = %q, want job room name", info.RoomName)
	}
	if info.AgentIdentity != "agent-job_avatar" {
		t.Fatalf("AgentIdentity = %q, want agent identity", info.AgentIdentity)
	}
}
