package livekit

import (
	"context"
	"errors"
	"strings"

	lkprotocol "github.com/livekit/protocol/livekit"
	"github.com/twitchtv/twirp"
)

type DeleteRoomResponse = lkprotocol.DeleteRoomResponse

type DeleteRoomPlanResult struct {
	Skip     bool
	Response *lkprotocol.DeleteRoomResponse
}

func DeleteRoomPlan(fakeJob bool) DeleteRoomPlanResult {
	if !ShouldSkipExternalAPIForFakeJob(fakeJob) {
		return DeleteRoomPlanResult{}
	}
	return DeleteRoomPlanResult{
		Skip:     true,
		Response: &lkprotocol.DeleteRoomResponse{},
	}
}

func DeleteRoomRequest(job *lkprotocol.Job, roomName string) *lkprotocol.DeleteRoomRequest {
	if roomName == "" && job != nil && job.Room != nil {
		roomName = job.Room.Name
	}
	return &lkprotocol.DeleteRoomRequest{Room: roomName}
}

type DeleteRoomAPI interface {
	DeleteRoom(context.Context, *lkprotocol.DeleteRoomRequest) (*lkprotocol.DeleteRoomResponse, error)
}

func DeleteRoomBestEffort(ctx context.Context, api DeleteRoomAPI, job *lkprotocol.Job, roomName string) (*lkprotocol.DeleteRoomResponse, error) {
	if api == nil {
		return &lkprotocol.DeleteRoomResponse{}, nil
	}
	resp, err := api.DeleteRoom(ctx, DeleteRoomRequest(job, roomName))
	if err != nil {
		if RoomDeleteNotFound(err) {
			return &lkprotocol.DeleteRoomResponse{}, nil
		}
		return &lkprotocol.DeleteRoomResponse{}, err
	}
	return resp, nil
}

func RoomDeleteNotFound(err error) bool {
	if err == nil {
		return false
	}
	var twerr twirp.Error
	if errors.As(err, &twerr) && twerr.Code() == twirp.NotFound {
		return true
	}
	errText := strings.ToLower(err.Error())
	return strings.Contains(errText, "not_found") && strings.Contains(errText, "room")
}
