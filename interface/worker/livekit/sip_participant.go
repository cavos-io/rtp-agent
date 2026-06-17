package livekit

import (
	"fmt"

	lksdk "github.com/livekit/server-sdk-go/v2"
)

func TransferSIPParticipantIdentity(participant any) (string, error) {
	switch p := participant.(type) {
	case string:
		return p, nil
	case RemoteParticipantView:
		if p.Kind() != lksdk.ParticipantSIP {
			return "", participantMustBeSIPError{}
		}
		return p.Identity(), nil
	default:
		return "", fmt.Errorf("participant must be a SIP participant or identity string")
	}
}

type participantMustBeSIPError struct{}

func (participantMustBeSIPError) Error() string {
	return "Participant must be a SIP participant"
}
