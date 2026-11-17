//go:build nativeinprocess

package nativeroom

import (
	wl "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	"testing"
)

func TestNativeRecordingRoomOptions(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		r := NewInProcess(nil, nil, Config{}, "", "")
		if err := r.ConfigureRoomOptions(wl.RoomOptions{DisablePreConnectAudio: true, RecordRemoteParticipants: true, DisableAudioInput: disabled}); err != nil {
			t.Fatal(err)
		}
		if !r.Config.captionOptions.RecordRemoteParticipants || r.Config.captionOptions.DisableAudioInput != disabled {
			t.Fatal("recording options not propagated")
		}
	}
}
