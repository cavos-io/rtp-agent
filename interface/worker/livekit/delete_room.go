package livekit

import (
	"errors"
	"strings"

	"github.com/twitchtv/twirp"
)

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
