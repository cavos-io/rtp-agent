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

type RemoteTrackPublicationView interface {
	SID() string
	Kind() lksdk.TrackKind
	SetSubscribed(bool) error
}

type RemoteTrackSubscriptionResult struct {
	Attempted bool
	TrackSID  string
	Err       error
}

func SubscribeRemoteTrackIfAllowed(mode string, publication RemoteTrackPublicationView) RemoteTrackSubscriptionResult {
	if publication == nil || !ShouldAutoSubscribeTrack(mode, publication.Kind()) {
		return RemoteTrackSubscriptionResult{}
	}
	result := RemoteTrackSubscriptionResult{
		Attempted: true,
		TrackSID:  publication.SID(),
	}
	result.Err = publication.SetSubscribed(true)
	return result
}

func ApplyAutoSubscribeToRoom(room *lksdk.Room, mode string) []RemoteTrackSubscriptionResult {
	if room == nil {
		return nil
	}
	var results []RemoteTrackSubscriptionResult
	for _, participant := range room.GetRemoteParticipants() {
		for _, publication := range participant.TrackPublications() {
			remotePublication, ok := publication.(*lksdk.RemoteTrackPublication)
			if !ok {
				continue
			}
			result := SubscribeRemoteTrackIfAllowed(mode, remotePublication)
			if result.Attempted {
				results = append(results, result)
			}
		}
	}
	return results
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
