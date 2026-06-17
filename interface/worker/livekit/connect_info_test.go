package livekit_test

import (
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lkprotocol "github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

func TestConnectInfoUsesAcceptedParticipantFields(t *testing.T) {
	info := workerlivekit.ConnectInfo(workerlivekit.ConnectInfoOptions{
		APIKey:              "key",
		APISecret:           "secret",
		RoomName:            "room-a",
		ParticipantName:     "Agent Name",
		ParticipantIdentity: "custom-agent",
		ParticipantMetadata: "custom-metadata",
		ParticipantAttributes: map[string]string{
			"tier": "gold",
		},
	})

	if info.APIKey != "key" {
		t.Fatalf("ConnectInfo.APIKey = %q, want key", info.APIKey)
	}
	if info.APISecret != "secret" {
		t.Fatalf("ConnectInfo.APISecret = %q, want secret", info.APISecret)
	}
	if info.RoomName != "room-a" {
		t.Fatalf("ConnectInfo.RoomName = %q, want room-a", info.RoomName)
	}
	if info.ParticipantIdentity != "custom-agent" {
		t.Fatalf("ConnectInfo.ParticipantIdentity = %q, want custom-agent", info.ParticipantIdentity)
	}
	if info.ParticipantName != "Agent Name" {
		t.Fatalf("ConnectInfo.ParticipantName = %q, want Agent Name", info.ParticipantName)
	}
	if info.ParticipantMetadata != "custom-metadata" {
		t.Fatalf("ConnectInfo.ParticipantMetadata = %q, want custom-metadata", info.ParticipantMetadata)
	}
	if info.ParticipantAttributes["tier"] != "gold" {
		t.Fatalf("ConnectInfo.ParticipantAttributes[tier] = %q, want gold", info.ParticipantAttributes["tier"])
	}
	if info.ParticipantKind != lksdk.ParticipantAgent {
		t.Fatalf("ConnectInfo.ParticipantKind = %v, want ParticipantAgent", info.ParticipantKind)
	}
}

func TestJobConnectInfoUsesJobRoomName(t *testing.T) {
	info := workerlivekit.JobConnectInfo(&lkprotocol.Job{
		Room: &lkprotocol.Room{Name: "room-a"},
	}, workerlivekit.ConnectInfoOptions{
		APIKey:              "key",
		APISecret:           "secret",
		ParticipantName:     "Agent Name",
		ParticipantIdentity: "custom-agent",
	})

	if info.RoomName != "room-a" {
		t.Fatalf("JobConnectInfo.RoomName = %q, want room-a", info.RoomName)
	}
	if info.ParticipantIdentity != "custom-agent" {
		t.Fatalf("JobConnectInfo.ParticipantIdentity = %q, want custom-agent", info.ParticipantIdentity)
	}
	if info.ParticipantKind != lksdk.ParticipantAgent {
		t.Fatalf("JobConnectInfo.ParticipantKind = %v, want ParticipantAgent", info.ParticipantKind)
	}
}

func TestConnectOptionsForAutoSubscribeBuildsSDKOptions(t *testing.T) {
	options := workerlivekit.ConnectOptionsForAutoSubscribe("audio_only")

	if len(options) != 1 {
		t.Fatalf("ConnectOptionsForAutoSubscribe() len = %d, want 1", len(options))
	}
	if options[0] == nil {
		t.Fatal("ConnectOptionsForAutoSubscribe()[0] = nil, want SDK option")
	}
}
