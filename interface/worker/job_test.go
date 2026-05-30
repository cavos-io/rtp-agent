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
