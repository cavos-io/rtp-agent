package livekit

import lkprotocol "github.com/livekit/protocol/livekit"

const DefaultSIPParticipantName = "SIP-participant"

func CreateSIPParticipantRequest(
	roomName string,
	callTo string,
	trunkID string,
	identity string,
	name string,
) *lkprotocol.CreateSIPParticipantRequest {
	if name == "" {
		name = DefaultSIPParticipantName
	}
	return &lkprotocol.CreateSIPParticipantRequest{
		RoomName:            roomName,
		ParticipantIdentity: identity,
		ParticipantName:     name,
		SipTrunkId:          trunkID,
		SipCallTo:           callTo,
	}
}

func JobCreateSIPParticipantRequest(
	job *lkprotocol.Job,
	callTo string,
	trunkID string,
	identity string,
	name string,
) *lkprotocol.CreateSIPParticipantRequest {
	return CreateSIPParticipantRequest(JobRoomName(job), callTo, trunkID, identity, name)
}

func TransferSIPParticipantRequest(
	roomName string,
	identity string,
	transferTo string,
	playDialtone bool,
) *lkprotocol.TransferSIPParticipantRequest {
	return &lkprotocol.TransferSIPParticipantRequest{
		ParticipantIdentity: identity,
		RoomName:            roomName,
		TransferTo:          transferTo,
		PlayDialtone:        playDialtone,
	}
}

func JobTransferSIPParticipantRequest(
	job *lkprotocol.Job,
	identity string,
	transferTo string,
	playDialtone bool,
) *lkprotocol.TransferSIPParticipantRequest {
	return TransferSIPParticipantRequest(JobRoomName(job), identity, transferTo, playDialtone)
}
