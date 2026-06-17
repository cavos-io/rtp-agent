package livekit

import (
	"errors"
	"strings"

	lkprotocol "github.com/livekit/protocol/livekit"
	"github.com/twitchtv/twirp"
)

func DeleteRoomRequest(job *lkprotocol.Job, roomName string) *lkprotocol.DeleteRoomRequest {
	if roomName == "" && job != nil && job.Room != nil {
		roomName = job.Room.Name
	}
	return &lkprotocol.DeleteRoomRequest{Room: roomName}
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
