package livekit_test

import (
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

func TestAutoSubscribeSDKEnabledMatchesReferenceModes(t *testing.T) {
	tests := []struct {
		mode string
		want bool
	}{
		{"subscribe_all", true},
		{"subscribe_none", false},
		{"audio_only", false},
		{"video_only", false},
		{"", true},
	}

	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			if got := workerlivekit.AutoSubscribeSDKEnabled(tt.mode); got != tt.want {
				t.Fatalf("AutoSubscribeSDKEnabled(%q) = %v, want %v", tt.mode, got, tt.want)
			}
		})
	}
}

func TestNormalizeAutoSubscribeModeDefaultsToSubscribeAll(t *testing.T) {
	if got := workerlivekit.NormalizeAutoSubscribeMode(""); got != "subscribe_all" {
		t.Fatalf("NormalizeAutoSubscribeMode(empty) = %q, want subscribe_all", got)
	}
	if got := workerlivekit.NormalizeAutoSubscribeMode("audio_only"); got != "audio_only" {
		t.Fatalf("NormalizeAutoSubscribeMode(audio_only) = %q, want audio_only", got)
	}
}

func TestShouldAutoSubscribeTrackMatchesReferenceModes(t *testing.T) {
	tests := []struct {
		mode string
		kind lksdk.TrackKind
		want bool
	}{
		{"subscribe_all", lksdk.TrackKindAudio, false},
		{"subscribe_none", lksdk.TrackKindAudio, false},
		{"audio_only", lksdk.TrackKindAudio, true},
		{"audio_only", lksdk.TrackKindVideo, false},
		{"video_only", lksdk.TrackKindAudio, false},
		{"video_only", lksdk.TrackKindVideo, true},
		{"", lksdk.TrackKindAudio, false},
	}

	for _, tt := range tests {
		t.Run(tt.mode+"_"+string(tt.kind), func(t *testing.T) {
			if got := workerlivekit.ShouldAutoSubscribeTrack(tt.mode, tt.kind); got != tt.want {
				t.Fatalf("ShouldAutoSubscribeTrack(%q, %q) = %v, want %v", tt.mode, tt.kind, got, tt.want)
			}
		})
	}
}
