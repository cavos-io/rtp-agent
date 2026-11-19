package nativeroom

import (
	lk "github.com/livekit/protocol/livekit"
	"testing"
)

func TestMicrophoneDiscoveryAndHelperCompatibility(t *testing.T) {
	for _, c := range []struct {
		name           string
		source         lk.TrackSource
		muted          bool
		native, helper bool
	}{
		{"microphone", lk.TrackSource_MICROPHONE, false, true, true},
		{"muted microphone", lk.TrackSource_MICROPHONE, true, true, false},
		{"screen audio", lk.TrackSource_SCREEN_SHARE_AUDIO, false, false, true},
		{"unknown", lk.TrackSource_UNKNOWN, false, false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			tr := &lk.TrackInfo{Type: lk.TrackType_AUDIO, Source: c.source, Muted: c.muted}
			if eligibleInput(tr, true) != c.native || eligibleInput(tr, false) != c.helper {
				t.Fatal("selection contract changed")
			}
			tr.Type = lk.TrackType_VIDEO
			if eligibleInput(tr, true) || eligibleInput(tr, false) {
				t.Fatal("video selected")
			}
		})
	}
}
