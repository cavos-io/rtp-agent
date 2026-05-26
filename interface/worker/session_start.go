package worker

import (
	"context"
	"fmt"

	"github.com/cavos-io/rtp-agent/core/agent"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

// StartSession wires RoomIO to session and starts both, mirroring the Python pattern:
//
//	await session.start(agent, room=room, room_options=opts)
//
// RoomIO is created, started, then the session pipeline is started. If RoomIO
// starts successfully but session fails, RoomIO is closed before returning the error.
// The caller is responsible for calling RoomIO.Close() and AgentSession.Close() on exit.
func StartSession(ctx context.Context, room *lksdk.Room, session *agent.AgentSession, opts RoomOptions) (*RoomIO, error) {
	rio := NewRoomIO(room, session, opts)

	if err := rio.Start(ctx); err != nil {
		return nil, fmt.Errorf("start room io: %w", err)
	}

	if err := session.Start(ctx); err != nil {
		rio.Close()
		return nil, fmt.Errorf("start agent session: %w", err)
	}

	return rio, nil
}
