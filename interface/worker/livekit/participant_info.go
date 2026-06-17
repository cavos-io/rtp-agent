package livekit

import (
	"maps"

	lkprotocol "github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

type RemoteParticipantView interface {
	SID() string
	Identity() string
	Name() string
	Kind() lksdk.ParticipantKind
	Metadata() string
	Attributes() map[string]string
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

func RoomLocalParticipant(room *lksdk.Room) *lksdk.LocalParticipant {
	if room == nil {
		return nil
	}
	return room.LocalParticipant
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

func ParticipantInfoDetails(participant *lkprotocol.ParticipantInfo) ParticipantDetails {
	if participant == nil {
		return ParticipantDetails{}
	}
	return ParticipantDetails{
		Identity: participant.Identity,
		Kind:     participant.Kind,
	}
}
