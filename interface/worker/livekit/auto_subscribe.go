package livekit

import lksdk "github.com/livekit/server-sdk-go/v2"

type AutoSubscribe string

const (
	AutoSubscribeSubscribeAll  AutoSubscribe = "subscribe_all"
	AutoSubscribeSubscribeNone AutoSubscribe = "subscribe_none"
	AutoSubscribeAudioOnly     AutoSubscribe = "audio_only"
	AutoSubscribeVideoOnly     AutoSubscribe = "video_only"
)

func AutoSubscribeSDKEnabled(mode string) bool {
	return normalizeAutoSubscribe(mode) == string(AutoSubscribeSubscribeAll)
}

func ShouldAutoSubscribeTrack(mode string, kind lksdk.TrackKind) bool {
	switch normalizeAutoSubscribe(mode) {
	case string(AutoSubscribeAudioOnly):
		return kind == lksdk.TrackKindAudio
	case string(AutoSubscribeVideoOnly):
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
		return string(AutoSubscribeSubscribeAll)
	}
	return mode
}
