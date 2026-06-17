package livekit

import lksdk "github.com/livekit/server-sdk-go/v2"

const (
	autoSubscribeSubscribeAll = "subscribe_all"
	autoSubscribeAudioOnly    = "audio_only"
	autoSubscribeVideoOnly    = "video_only"
)

func AutoSubscribeSDKEnabled(mode string) bool {
	return normalizeAutoSubscribe(mode) == autoSubscribeSubscribeAll
}

func ShouldAutoSubscribeTrack(mode string, kind lksdk.TrackKind) bool {
	switch normalizeAutoSubscribe(mode) {
	case autoSubscribeAudioOnly:
		return kind == lksdk.TrackKindAudio
	case autoSubscribeVideoOnly:
		return kind == lksdk.TrackKindVideo
	default:
		return false
	}
}

func NormalizeAutoSubscribeMode(mode string) string {
	return normalizeAutoSubscribe(mode)
}

func normalizeAutoSubscribe(mode string) string {
	if mode == "" {
		return autoSubscribeSubscribeAll
	}
	return mode
}
