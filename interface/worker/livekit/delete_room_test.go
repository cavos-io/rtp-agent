package livekit_test

import (
	"errors"
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	"github.com/twitchtv/twirp"
)

func TestRoomDeleteNotFoundRecognizesLiveKitCleanupErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "twirp not found",
			err:  twirp.NotFoundError("room not found"),
			want: true,
		},
		{
			name: "livekit text fallback",
			err:  errors.New("not_found: room does not exist"),
			want: true,
		},
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "unknown room error",
			err:  errors.New("permission denied for room"),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := workerlivekit.RoomDeleteNotFound(tc.err); got != tc.want {
				t.Fatalf("RoomDeleteNotFound(%v) = %t, want %t", tc.err, got, tc.want)
			}
		})
	}
}
