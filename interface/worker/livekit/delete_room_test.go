package livekit_test

import (
	"context"
	"errors"
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lkprotocol "github.com/livekit/protocol/livekit"
	"github.com/twitchtv/twirp"
)

func TestDeleteRoomResponseAliasUsesLiveKitProtocolResponse(t *testing.T) {
	resp := &workerlivekit.DeleteRoomResponse{}
	var protocolResp *lkprotocol.DeleteRoomResponse = resp

	if protocolResp != resp {
		t.Fatal("DeleteRoomResponse alias did not preserve response pointer")
	}
}

func TestDeleteRoomRequestUsesExplicitRoomName(t *testing.T) {
	req := workerlivekit.DeleteRoomRequest(&lkprotocol.Job{
		Room: &lkprotocol.Room{Name: "job-room"},
	}, "explicit-room")

	if req.GetRoom() != "explicit-room" {
		t.Fatalf("DeleteRoomRequest().Room = %q, want explicit-room", req.GetRoom())
	}
}

func TestDeleteRoomRequestFallsBackToJobRoomName(t *testing.T) {
	req := workerlivekit.DeleteRoomRequest(&lkprotocol.Job{
		Room: &lkprotocol.Room{Name: "job-room"},
	}, "")

	if req.GetRoom() != "job-room" {
		t.Fatalf("DeleteRoomRequest().Room = %q, want job-room", req.GetRoom())
	}
}

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

func TestDeleteRoomBestEffortUsesJobRoomName(t *testing.T) {
	api := &fakeDeleteRoomAPI{}
	resp, warnErr := workerlivekit.DeleteRoomBestEffort(context.Background(), api, &lkprotocol.Job{
		Room: &lkprotocol.Room{Name: "job-room"},
	}, "")

	if warnErr != nil {
		t.Fatalf("DeleteRoomBestEffort() warnErr = %v, want nil", warnErr)
	}
	if resp == nil {
		t.Fatal("DeleteRoomBestEffort() response = nil")
	}
	if api.request == nil {
		t.Fatal("DeleteRoomBestEffort() did not call room API")
	}
	if api.request.Room != "job-room" {
		t.Fatalf("DeleteRoomBestEffort() room = %q, want job-room", api.request.Room)
	}
}

func TestDeleteRoomBestEffortSuppressesNotFound(t *testing.T) {
	api := &fakeDeleteRoomAPI{err: twirp.NotFoundError("room not found")}
	resp, warnErr := workerlivekit.DeleteRoomBestEffort(context.Background(), api, &lkprotocol.Job{}, "room-a")

	if warnErr != nil {
		t.Fatalf("DeleteRoomBestEffort() warnErr = %v, want nil for not found", warnErr)
	}
	if resp == nil {
		t.Fatal("DeleteRoomBestEffort() response = nil")
	}
}

func TestDeleteRoomBestEffortReturnsWarningErrorForUnknownFailure(t *testing.T) {
	wantErr := errors.New("server disconnected")
	api := &fakeDeleteRoomAPI{err: wantErr}
	resp, warnErr := workerlivekit.DeleteRoomBestEffort(context.Background(), api, &lkprotocol.Job{}, "room-a")

	if !errors.Is(warnErr, wantErr) {
		t.Fatalf("DeleteRoomBestEffort() warnErr = %v, want %v", warnErr, wantErr)
	}
	if resp == nil {
		t.Fatal("DeleteRoomBestEffort() response = nil")
	}
}

type fakeDeleteRoomAPI struct {
	request *lkprotocol.DeleteRoomRequest
	err     error
}

func (f *fakeDeleteRoomAPI) DeleteRoom(_ context.Context, req *lkprotocol.DeleteRoomRequest) (*lkprotocol.DeleteRoomResponse, error) {
	f.request = req
	if f.err != nil {
		return nil, f.err
	}
	return &lkprotocol.DeleteRoomResponse{}, nil
}
