package worker

import (
	"slices"
	"testing"

	lksdk "github.com/livekit/server-sdk-go/v2"
)

func TestRoomPriority(t *testing.T) {
	room := NewRoom()

	var order []int
	cb1 := lksdk.NewRoomCallback()
	cb1.OnDisconnected = func() {
		order = append(order, 1)
	}
	cb2 := lksdk.NewRoomCallback()
	cb2.OnDisconnected = func() {
		order = append(order, 2)
	}
	cb3 := lksdk.NewRoomCallback()
	cb3.OnDisconnected = func() {
		order = append(order, 3)
	}

	room.AddCallbackWithPriority(cb1, 10)
	room.AddCallbackWithPriority(cb2, 20)
	room.AddCallbackWithPriority(cb3, 5)
	room.AddCallback(lksdk.NewRoomCallback()) // default priority 0

	room.OnDisconnected(lksdk.RoomClosed)

	expected := []int{2, 1, 3}
	if !slices.Equal(order, expected) {
		t.Errorf("expected order %v, got %v", expected, order)
	}
}
