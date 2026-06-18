package livekit

import (
	"maps"

	lkprotocol "github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

type ParticipantInfo = lkprotocol.ParticipantInfo
type ParticipantInfoKind = lkprotocol.ParticipantInfo_Kind
type Room = lkprotocol.Room
type TrackType = lkprotocol.TrackType

type RemoteParticipantView interface {
	SID() string
	Identity() string
	Name() string
	Kind() lksdk.ParticipantKind
	Metadata() string
	Attributes() map[string]string
}

type RoomCallbackHandlers struct {
	AutoSubscribe          string
	OnParticipantConnected func(RemoteParticipantView)
	OnTrackSubscribeError  func(RemoteTrackSubscriptionResult)
}

func RemoteParticipantViews(participants []*lksdk.RemoteParticipant) []RemoteParticipantView {
	views := make([]RemoteParticipantView, 0, len(participants))
	for _, participant := range participants {
		if participant != nil {
			views = append(views, participant)
		}
	}
	return views
}

func RoomRemoteParticipantViews(room *lksdk.Room) []RemoteParticipantView {
	if room == nil {
		return nil
	}
	return RemoteParticipantViews(room.GetRemoteParticipants())
}

func RoomLocalParticipant(room *lksdk.Room) *lksdk.LocalParticipant {
	if room == nil {
		return nil
	}
	return room.LocalParticipant
}

func RoomCallbackWithHandlers(cb *lksdk.RoomCallback, handlers RoomCallbackHandlers) *lksdk.RoomCallback {
	wrapped := lksdk.NewRoomCallback()
	wrapped.Merge(cb)

	onParticipantConnected := wrapped.OnParticipantConnected
	wrapped.OnParticipantConnected = func(participant *lksdk.RemoteParticipant) {
		if onParticipantConnected != nil {
			onParticipantConnected(participant)
		}
		if participant != nil && handlers.OnParticipantConnected != nil {
			handlers.OnParticipantConnected(participant)
		}
	}

	onTrackPublished := wrapped.OnTrackPublished
	wrapped.OnTrackPublished = func(publication *lksdk.RemoteTrackPublication, participant *lksdk.RemoteParticipant) {
		if onTrackPublished != nil {
			onTrackPublished(publication, participant)
		}
		result := SubscribeRemoteTrackIfAllowed(handlers.AutoSubscribe, publication)
		if result.Attempted && result.Err != nil && handlers.OnTrackSubscribeError != nil {
			handlers.OnTrackSubscribeError(result)
		}
	}

	return wrapped
}

func ParticipantInfoFromRemoteParticipant(participant RemoteParticipantView) *lkprotocol.ParticipantInfo {
	if participant == nil {
		return nil
	}
	return &lkprotocol.ParticipantInfo{
		Sid:        participant.SID(),
		Identity:   participant.Identity(),
		Name:       participant.Name(),
		Kind:       lkprotocol.ParticipantInfo_Kind(participant.Kind()),
		Metadata:   participant.Metadata(),
		Attributes: maps.Clone(participant.Attributes()),
	}
}

type ParticipantDetails struct {
	Identity string
	Kind     lkprotocol.ParticipantInfo_Kind
}

type ParticipantTaskKey struct {
	Identity   string
	Entrypoint uintptr
}

type ParticipantEntrypointTaskPlanResult struct {
	Schedule    bool
	Participant ParticipantDetails
	TaskKey     ParticipantTaskKey
}

func ParticipantInfoDetails(participant *lkprotocol.ParticipantInfo) ParticipantDetails {
	if participant == nil {
		return ParticipantDetails{}
	}
	return ParticipantDetails{
		Identity: participant.Identity,
		Kind:     participant.Kind,
	}
}

func ParticipantInfoKindAllowed(kinds []lkprotocol.ParticipantInfo_Kind, participant *lkprotocol.ParticipantInfo) bool {
	return ParticipantKindAllowed(kinds, ParticipantInfoDetails(participant).Kind)
}

func ParticipantEntrypointTaskKey(participant *lkprotocol.ParticipantInfo, entrypoint uintptr) ParticipantTaskKey {
	return ParticipantTaskKey{
		Identity:   ParticipantInfoDetails(participant).Identity,
		Entrypoint: entrypoint,
	}
}

func ParticipantEntrypointTaskPlan(participant *lkprotocol.ParticipantInfo, kinds []lkprotocol.ParticipantInfo_Kind, entrypoint uintptr) ParticipantEntrypointTaskPlanResult {
	if participant == nil {
		return ParticipantEntrypointTaskPlanResult{}
	}
	details := ParticipantInfoDetails(participant)
	if !ParticipantKindAllowed(kinds, details.Kind) {
		return ParticipantEntrypointTaskPlanResult{}
	}
	return ParticipantEntrypointTaskPlanResult{
		Schedule:    true,
		Participant: details,
		TaskKey: ParticipantTaskKey{
			Identity:   details.Identity,
			Entrypoint: entrypoint,
		},
	}
}

func UpsertParticipantInfo(participants []*lkprotocol.ParticipantInfo, info *lkprotocol.ParticipantInfo) []*lkprotocol.ParticipantInfo {
	infoDetails := ParticipantInfoDetails(info)
	for i, participant := range participants {
		participantDetails := ParticipantInfoDetails(participant)
		if participantDetails.Identity == infoDetails.Identity {
			participants[i] = info
			return participants
		}
	}
	return append(participants, info)
}
