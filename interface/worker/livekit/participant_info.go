package livekit

import (
	"fmt"
	"maps"

	lkprotocol "github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/webrtc/v4"
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
	AutoSubscribe             string
	OnDisconnected            func()
	OnDisconnectedWithReason  func(lksdk.DisconnectionReason)
	OnReconnecting            func()
	OnReconnected             func()
	OnRoomMoved               func(roomName string, token string)
	OnParticipantConnected    func(RemoteParticipantView)
	OnParticipantDisconnected func(RemoteParticipantView)
	OnLocalTrackPublished     func(*lksdk.LocalTrackPublication, *lksdk.LocalParticipant)
	OnLocalTrackSubscribed    func(*lksdk.LocalTrackPublication, *lksdk.LocalParticipant)
	OnTrackSubscribed         func(*webrtc.TrackRemote, *lksdk.RemoteTrackPublication, *lksdk.RemoteParticipant)
	OnTrackUnpublished        func(*lksdk.RemoteTrackPublication, *lksdk.RemoteParticipant)
	OnTrackPublishedEvent     func(*lksdk.RemoteTrackPublication, *lksdk.RemoteParticipant)
	OnAttributesChanged       func(map[string]string, lksdk.Participant)
	OnIsSpeakingChanged       func(lksdk.Participant)
	OnDataPacket              func(lksdk.DataPacket, lksdk.DataReceiveParams)
	OnTrackSubscribeError     func(RemoteTrackSubscriptionResult)
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
	if cb != nil && cb.OnRoomMoved != nil {
		wrapped.OnRoomMoved = cb.OnRoomMoved
	}

	onDisconnected := wrapped.OnDisconnected
	wrapped.OnDisconnected = func() {
		if onDisconnected != nil {
			onDisconnected()
		}
		if handlers.OnDisconnected != nil {
			handlers.OnDisconnected()
		}
	}

	onDisconnectedWithReason := wrapped.OnDisconnectedWithReason
	wrapped.OnDisconnectedWithReason = func(reason lksdk.DisconnectionReason) {
		if onDisconnectedWithReason != nil {
			onDisconnectedWithReason(reason)
		}
		if handlers.OnDisconnectedWithReason != nil {
			handlers.OnDisconnectedWithReason(reason)
		}
	}

	onReconnecting := wrapped.OnReconnecting
	wrapped.OnReconnecting = func() {
		if onReconnecting != nil {
			onReconnecting()
		}
		if handlers.OnReconnecting != nil {
			handlers.OnReconnecting()
		}
	}

	onReconnected := wrapped.OnReconnected
	wrapped.OnReconnected = func() {
		if onReconnected != nil {
			onReconnected()
		}
		if handlers.OnReconnected != nil {
			handlers.OnReconnected()
		}
	}

	onRoomMoved := wrapped.OnRoomMoved
	wrapped.OnRoomMoved = func(roomName string, token string) {
		if onRoomMoved != nil {
			onRoomMoved(roomName, token)
		}
		if handlers.OnRoomMoved != nil {
			handlers.OnRoomMoved(roomName, token)
		}
	}

	onParticipantConnected := wrapped.OnParticipantConnected
	wrapped.OnParticipantConnected = func(participant *lksdk.RemoteParticipant) {
		if onParticipantConnected != nil {
			onParticipantConnected(participant)
		}
		if participant != nil && handlers.OnParticipantConnected != nil {
			handlers.OnParticipantConnected(participant)
		}
	}

	onParticipantDisconnected := wrapped.OnParticipantDisconnected
	wrapped.OnParticipantDisconnected = func(participant *lksdk.RemoteParticipant) {
		if onParticipantDisconnected != nil {
			onParticipantDisconnected(participant)
		}
		if participant != nil && handlers.OnParticipantDisconnected != nil {
			handlers.OnParticipantDisconnected(participant)
		}
	}

	onLocalTrackPublished := wrapped.OnLocalTrackPublished
	wrapped.OnLocalTrackPublished = func(publication *lksdk.LocalTrackPublication, participant *lksdk.LocalParticipant) {
		if onLocalTrackPublished != nil {
			onLocalTrackPublished(publication, participant)
		}
		if handlers.OnLocalTrackPublished != nil {
			handlers.OnLocalTrackPublished(publication, participant)
		}
	}

	onLocalTrackSubscribed := wrapped.OnLocalTrackSubscribed
	wrapped.OnLocalTrackSubscribed = func(publication *lksdk.LocalTrackPublication, participant *lksdk.LocalParticipant) {
		if onLocalTrackSubscribed != nil {
			onLocalTrackSubscribed(publication, participant)
		}
		if handlers.OnLocalTrackSubscribed != nil {
			handlers.OnLocalTrackSubscribed(publication, participant)
		}
	}

	onTrackSubscribed := wrapped.OnTrackSubscribed
	wrapped.OnTrackSubscribed = func(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, participant *lksdk.RemoteParticipant) {
		if onTrackSubscribed != nil {
			onTrackSubscribed(track, publication, participant)
		}
		if handlers.OnTrackSubscribed != nil {
			handlers.OnTrackSubscribed(track, publication, participant)
		}
	}

	onTrackUnpublished := wrapped.OnTrackUnpublished
	wrapped.OnTrackUnpublished = func(publication *lksdk.RemoteTrackPublication, participant *lksdk.RemoteParticipant) {
		if onTrackUnpublished != nil {
			onTrackUnpublished(publication, participant)
		}
		if handlers.OnTrackUnpublished != nil {
			handlers.OnTrackUnpublished(publication, participant)
		}
	}

	onAttributesChanged := wrapped.OnAttributesChanged
	wrapped.OnAttributesChanged = func(changed map[string]string, participant lksdk.Participant) {
		if onAttributesChanged != nil {
			onAttributesChanged(changed, participant)
		}
		if handlers.OnAttributesChanged != nil {
			handlers.OnAttributesChanged(changed, participant)
		}
	}

	onIsSpeakingChanged := wrapped.OnIsSpeakingChanged
	wrapped.OnIsSpeakingChanged = func(participant lksdk.Participant) {
		if onIsSpeakingChanged != nil {
			onIsSpeakingChanged(participant)
		}
		if handlers.OnIsSpeakingChanged != nil {
			handlers.OnIsSpeakingChanged(participant)
		}
	}

	onDataPacket := wrapped.OnDataPacket
	wrapped.OnDataPacket = func(packet lksdk.DataPacket, params lksdk.DataReceiveParams) {
		if onDataPacket != nil {
			onDataPacket(packet, params)
		}
		if handlers.OnDataPacket != nil {
			handlers.OnDataPacket(packet, params)
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
		if handlers.OnTrackPublishedEvent != nil {
			handlers.OnTrackPublishedEvent(publication, participant)
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

type ParticipantEntrypointRegistrationOptions struct {
	Entrypoint            uintptr
	RegisteredEntrypoints []uintptr
	Kinds                 []lkprotocol.ParticipantInfo_Kind
}

type ParticipantEntrypointRegistrationPlanResult struct {
	Kinds []lkprotocol.ParticipantInfo_Kind
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

func ParticipantEntrypointRegistrationPlan(opts ParticipantEntrypointRegistrationOptions) (ParticipantEntrypointRegistrationPlanResult, error) {
	for _, registered := range opts.RegisteredEntrypoints {
		if registered == opts.Entrypoint {
			return ParticipantEntrypointRegistrationPlanResult{}, fmt.Errorf("entrypoints cannot be added more than once")
		}
	}
	return ParticipantEntrypointRegistrationPlanResult{
		Kinds: DefaultParticipantKindsWhenUnset(opts.Kinds),
	}, nil
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
