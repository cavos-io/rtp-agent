package livekit_test

import (
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
)

func TestLiveKitPackageOwnsRoomIOContracts(t *testing.T) {
	if workerlivekit.RoomIOChatTopic != "lk.chat" {
		t.Fatalf("RoomIOChatTopic = %q, want lk.chat", workerlivekit.RoomIOChatTopic)
	}
}
