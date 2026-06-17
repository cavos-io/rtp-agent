package livekit_test

import (
	"errors"
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

type fakeRemoteTrackPublication struct {
	sid        string
	kind       lksdk.TrackKind
	err        error
	subscribed bool
}

func (f *fakeRemoteTrackPublication) SID() string {
	return f.sid
}

func (f *fakeRemoteTrackPublication) Kind() lksdk.TrackKind {
	return f.kind
}

func (f *fakeRemoteTrackPublication) SetSubscribed(subscribed bool) error {
	f.subscribed = subscribed
	return f.err
}

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

func TestSubscribeRemoteTrackIfAllowedSubscribesMatchingTrack(t *testing.T) {
	publication := &fakeRemoteTrackPublication{sid: "TR_audio", kind: lksdk.TrackKindAudio}

	result := workerlivekit.SubscribeRemoteTrackIfAllowed("audio_only", publication)

	if !result.Attempted {
		t.Fatal("SubscribeRemoteTrackIfAllowed().Attempted = false, want true")
	}
	if result.TrackSID != "TR_audio" {
		t.Fatalf("SubscribeRemoteTrackIfAllowed().TrackSID = %q, want TR_audio", result.TrackSID)
	}
	if result.Err != nil {
		t.Fatalf("SubscribeRemoteTrackIfAllowed().Err = %v, want nil", result.Err)
	}
	if !publication.subscribed {
		t.Fatal("publication subscribed = false, want true")
	}
}

func TestSubscribeRemoteTrackIfAllowedSkipsNonMatchingTrack(t *testing.T) {
	publication := &fakeRemoteTrackPublication{sid: "TR_video", kind: lksdk.TrackKindVideo}

	result := workerlivekit.SubscribeRemoteTrackIfAllowed("audio_only", publication)

	if result.Attempted {
		t.Fatal("SubscribeRemoteTrackIfAllowed().Attempted = true, want false")
	}
	if publication.subscribed {
		t.Fatal("publication subscribed = true, want false")
	}
}

func TestSubscribeRemoteTrackIfAllowedReturnsSubscribeError(t *testing.T) {
	wantErr := errors.New("subscribe failed")
	publication := &fakeRemoteTrackPublication{sid: "TR_audio", kind: lksdk.TrackKindAudio, err: wantErr}

	result := workerlivekit.SubscribeRemoteTrackIfAllowed("audio_only", publication)

	if !result.Attempted {
		t.Fatal("SubscribeRemoteTrackIfAllowed().Attempted = false, want true")
	}
	if result.TrackSID != "TR_audio" {
		t.Fatalf("SubscribeRemoteTrackIfAllowed().TrackSID = %q, want TR_audio", result.TrackSID)
	}
	if !errors.Is(result.Err, wantErr) {
		t.Fatalf("SubscribeRemoteTrackIfAllowed().Err = %v, want %v", result.Err, wantErr)
	}
}

func TestApplyAutoSubscribeToRoomHandlesNilRoom(t *testing.T) {
	results := workerlivekit.ApplyAutoSubscribeToRoom(nil, "audio_only")

	if len(results) != 0 {
		t.Fatalf("ApplyAutoSubscribeToRoom(nil) results = %d, want 0", len(results))
	}
}

func TestApplyAutoSubscribeToRoomHandlesRoomWithoutRemoteParticipants(t *testing.T) {
	results := workerlivekit.ApplyAutoSubscribeToRoom(lksdk.NewRoom(nil), "audio_only")

	if len(results) != 0 {
		t.Fatalf("ApplyAutoSubscribeToRoom(empty room) results = %d, want 0", len(results))
	}
}
