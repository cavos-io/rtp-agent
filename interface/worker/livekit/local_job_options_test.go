package livekit_test

import (
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lkprotocol "github.com/livekit/protocol/livekit"
)

func TestLocalJobOptionsOwnsLiveKitRoomAndRecordingFields(t *testing.T) {
	room := &lkprotocol.Room{Name: "room-a"}
	opts := workerlivekit.LocalJobOptions{
		FakeJob:           false,
		RoomInfo:          room,
		Token:             "room-token",
		RecordingOptions:  agent.RecordingOptions{Audio: true},
		SessionReportPath: "reports/session.json",
		SessionDirectory:  "sessions/job-a",
	}

	if opts.RoomInfo != room {
		t.Fatalf("RoomInfo = %p, want %p", opts.RoomInfo, room)
	}
	if !opts.RecordingOptions.Audio {
		t.Fatal("RecordingOptions.Audio = false, want true")
	}
	if opts.Token != "room-token" {
		t.Fatalf("Token = %q, want room-token", opts.Token)
	}
}
