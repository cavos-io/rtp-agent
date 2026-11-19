//go:build nativeinprocess

package nativeroom

import (
	wl "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	"testing"
)

func TestNativeRecordingRoomOptions(t *testing.T) {
	for _, record := range []bool{false, true} {
		r := NewInProcess(nil, nil, Config{}, "", "")
		if err := r.ConfigureRoomOptions(wl.RoomOptions{DisablePreConnectAudio: true, RecordRemoteParticipants: record}); err != nil {
			t.Fatal(err)
		}
		if r.Config.captionOptions.RecordRemoteParticipants != record {
			t.Fatal("recording option not propagated")
		}
		if err := r.ConfigureRoomOptions(wl.RoomOptions{DisablePreConnectAudio: true, DisableAudioInput: true}); err == nil {
			t.Fatal("unsupported input disable accepted")
		}
		if r.Config.captionOptions.RecordRemoteParticipants != record {
			t.Fatal("rejected options mutated recording configuration")
		}
	}
}
