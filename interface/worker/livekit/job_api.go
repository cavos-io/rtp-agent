package livekit

import (
	"context"

	lkprotocol "github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"google.golang.org/protobuf/types/known/emptypb"
)

type JobRoomServiceAPI interface {
	DeleteRoom(context.Context, *lkprotocol.DeleteRoomRequest) (*lkprotocol.DeleteRoomResponse, error)
	MoveParticipant(context.Context, *lkprotocol.MoveParticipantRequest) (*lkprotocol.MoveParticipantResponse, error)
}

type JobSIPAPI interface {
	CreateSIPParticipant(context.Context, *lkprotocol.CreateSIPParticipantRequest) (*lkprotocol.SIPParticipantInfo, error)
	TransferSIPParticipant(context.Context, *lkprotocol.TransferSIPParticipantRequest) (*emptypb.Empty, error)
}

type JobAPI struct {
	RoomService JobRoomServiceAPI
	SIP         JobSIPAPI
}

func NewJobAPI(url string, apiKey string, apiSecret string) *JobAPI {
	return &JobAPI{
		RoomService: lksdk.NewRoomServiceClient(url, apiKey, apiSecret),
		SIP:         lksdk.NewSIPClient(url, apiKey, apiSecret),
	}
}
