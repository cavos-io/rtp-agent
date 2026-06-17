package livekit_test

import (
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
)

func TestNewJobAPIReturnsLiveKitClients(t *testing.T) {
	api := workerlivekit.NewJobAPI("wss://livekit.example", "key", "secret")
	if api == nil {
		t.Fatal("NewJobAPI() = nil")
	}
	if api.RoomService == nil {
		t.Fatal("NewJobAPI().RoomService = nil")
	}
	if api.SIP == nil {
		t.Fatal("NewJobAPI().SIP = nil")
	}
}
