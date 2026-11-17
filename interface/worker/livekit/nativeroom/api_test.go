//go:build nativeinprocess

package nativeroom_test

import (
	"github.com/cavos-io/rtp-agent/interface/worker"
	"github.com/cavos-io/rtp-agent/interface/worker/livekit/nativeroom"
	"testing"
)

func TestPublicNativeTransportConfiguration(t *testing.T) {
	room := nativeroom.NewInProcess(nil, nil, nativeroom.Config{Library: "not-loaded-during-configuration", Participant: "caller"}, "", "")
	var _ worker.SessionRoomIO = room
	if err := room.ConfigureRoomOptions(worker.RoomOptions{DisablePreConnectAudio: true, ParticipantIdentity: "caller", RecordRemoteParticipants: true}); err != nil {
		t.Fatal(err)
	}
	if err := room.ConfigureRoomOptions(worker.RoomOptions{DisablePreConnectAudio: true, ParticipantIdentity: "other"}); err == nil {
		t.Fatal("conflicting participant accepted")
	}
	if err := room.ConfigureRoomOptions(worker.RoomOptions{}); err == nil {
		t.Fatal("unsupported pre-connect accepted")
	}
}
