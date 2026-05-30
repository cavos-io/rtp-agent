package worker

import (
	"context"
	"reflect"
	"testing"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

func TestJobContextShutdownRunsCallbacks(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_shutdown"}, "", "", "")
	var calls []string

	if err := ctx.AddShutdownCallback(func(reason string) {
		calls = append(calls, "reason:"+reason)
	}); err != nil {
		t.Fatalf("AddShutdownCallback(reason) error = %v", err)
	}
	if err := ctx.AddShutdownCallback(func() {
		calls = append(calls, "no-reason")
	}); err != nil {
		t.Fatalf("AddShutdownCallback() error = %v", err)
	}

	ctx.Shutdown("user_initiated")

	want := []string{"reason:user_initiated", "no-reason"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("shutdown callbacks = %#v, want %#v", calls, want)
	}
}

func TestJobContextShutdownRunsCallbacksOnce(t *testing.T) {
	ctx := NewJobContext(&livekit.Job{Id: "job_shutdown_once"}, "", "", "")
	callCount := 0

	if err := ctx.AddShutdownCallback(func(string) {
		callCount++
	}); err != nil {
		t.Fatalf("AddShutdownCallback() error = %v", err)
	}

	ctx.Shutdown("first")
	ctx.Shutdown("second")

	if callCount != 1 {
		t.Fatalf("shutdown callback call count = %d, want 1", callCount)
	}
}

func TestJobContextConnectInfoUsesAcceptedParticipantFields(t *testing.T) {
	ctx := NewJobContext(
		&livekit.Job{Id: "job_connect_info", Room: &livekit.Room{Name: "room-a"}},
		"wss://livekit.example",
		"key",
		"secret",
	)
	ctx.AcceptArguments = JobAcceptArguments{
		Name:     "Agent Name",
		Identity: "custom-agent",
		Metadata: "custom-metadata",
		Attributes: map[string]string{
			"tier": "gold",
		},
	}

	info := ctx.connectInfo()

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

func TestJobContextRoomInfoReturnsJobRoom(t *testing.T) {
	room := &livekit.Room{Name: "room-a", Sid: "RM_a"}
	ctx := NewJobContext(&livekit.Job{Id: "job_room", Room: room}, "", "", "")

	if got := ctx.RoomInfo(); got != room {
		t.Fatal("RoomInfo() did not return the job room")
	}

	ctx.Job = nil
	if got := ctx.RoomInfo(); got != nil {
		t.Fatalf("RoomInfo() with nil job = %#v, want nil", got)
	}
}

func TestJobRequestAccessorsExposeJobFields(t *testing.T) {
	room := &livekit.Room{Name: "room-a"}
	publisher := &livekit.ParticipantInfo{Identity: "publisher-a"}
	req := &JobRequest{
		Job: &livekit.Job{
			Id:          "job_request",
			Room:        room,
			Participant: publisher,
			AgentName:   "agent-a",
		},
	}

	if got := req.ID(); got != "job_request" {
		t.Fatalf("ID() = %q, want job_request", got)
	}
	if got := req.Room(); got != room {
		t.Fatal("Room() did not return the job room")
	}
	if got := req.Publisher(); got != publisher {
		t.Fatal("Publisher() did not return the job participant")
	}
	if got := req.AgentName(); got != "agent-a" {
		t.Fatalf("AgentName() = %q, want agent-a", got)
	}
}

func TestLocalJobContextSkipsDestructiveLiveKitAPIs(t *testing.T) {
	ctx := newLocalJobContext("room-a", "agent-local", WorkerOptions{})
	if !ctx.IsFakeJob() {
		t.Fatal("local job context IsFakeJob() = false, want true")
	}

	if resp, err := ctx.DeleteRoom(context.Background(), ""); err != nil {
		t.Fatalf("DeleteRoom() error = %v", err)
	} else if resp == nil {
		t.Fatal("DeleteRoom() response = nil, want empty response")
	}

	if info, err := ctx.AddSIPParticipant(context.Background(), "+15551234567", "trunk", "sip-user", "SIP User"); err != nil {
		t.Fatalf("AddSIPParticipant() error = %v", err)
	} else if info == nil {
		t.Fatal("AddSIPParticipant() info = nil, want empty info")
	}

	if err := ctx.TransferSIPParticipant(context.Background(), "sip-user", "+15557654321", false); err != nil {
		t.Fatalf("TransferSIPParticipant() error = %v", err)
	}
}
