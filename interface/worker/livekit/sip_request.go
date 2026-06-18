package livekit

import (
	"context"

	lkprotocol "github.com/livekit/protocol/livekit"
	"google.golang.org/protobuf/types/known/emptypb"
)

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

type SIPAPI interface {
	CreateSIPParticipant(context.Context, *lkprotocol.CreateSIPParticipantRequest) (*lkprotocol.SIPParticipantInfo, error)
	TransferSIPParticipant(context.Context, *lkprotocol.TransferSIPParticipantRequest) (*emptypb.Empty, error)
}

func CreateSIPParticipant(
	ctx context.Context,
	api SIPAPI,
	job *lkprotocol.Job,
	callTo string,
	trunkID string,
	identity string,
	name string,
) (*lkprotocol.SIPParticipantInfo, error) {
	return CreateSIPParticipantWithRequest(ctx, api, JobCreateSIPParticipantRequest(job, callTo, trunkID, identity, name))
}

func CreateSIPParticipantWithRequest(
	ctx context.Context,
	api SIPAPI,
	req *lkprotocol.CreateSIPParticipantRequest,
) (*lkprotocol.SIPParticipantInfo, error) {
	if api == nil {
		return &lkprotocol.SIPParticipantInfo{}, nil
	}
	return api.CreateSIPParticipant(ctx, req)
}

func TransferSIPParticipant(
	ctx context.Context,
	api SIPAPI,
	job *lkprotocol.Job,
	identity string,
	transferTo string,
	playDialtone bool,
) error {
	if api == nil {
		return nil
	}
	req := JobTransferSIPParticipantRequest(job, identity, transferTo, playDialtone)
	_, err := api.TransferSIPParticipant(ctx, req)
	return err
}

func TransferSIPParticipantByParticipant(
	ctx context.Context,
	api SIPAPI,
	job *lkprotocol.Job,
	participant any,
	transferTo string,
	playDialtones ...bool,
) error {
	identity, err := TransferSIPParticipantIdentity(participant)
	if err != nil {
		return err
	}
	playDialtone := false
	if len(playDialtones) > 0 {
		playDialtone = playDialtones[0]
	}
	return TransferSIPParticipant(ctx, api, job, identity, transferTo, playDialtone)
}
