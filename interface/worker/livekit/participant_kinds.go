package livekit

import lkprotocol "github.com/livekit/protocol/livekit"

var defaultParticipantKinds = []lkprotocol.ParticipantInfo_Kind{
	lkprotocol.ParticipantInfo_CONNECTOR,
	lkprotocol.ParticipantInfo_SIP,
	lkprotocol.ParticipantInfo_STANDARD,
}

func DefaultParticipantKinds() []lkprotocol.ParticipantInfo_Kind {
	return append([]lkprotocol.ParticipantInfo_Kind(nil), defaultParticipantKinds...)
}

func DefaultParticipantKindsWhenUnset(kinds []lkprotocol.ParticipantInfo_Kind) []lkprotocol.ParticipantInfo_Kind {
	if len(kinds) > 0 {
		return kinds
	}
	return DefaultParticipantKinds()
}

func ParticipantKindAllowed(kinds []lkprotocol.ParticipantInfo_Kind, kind lkprotocol.ParticipantInfo_Kind) bool {
	if len(kinds) == 0 {
		return true
	}
	for _, allowed := range kinds {
		if allowed == kind {
			return true
		}
	}
	return false
}
