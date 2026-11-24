package livekit

import (
	"context"
	"github.com/cavos-io/rtp-agent/core/agent"
)

// HandleTextInput uses the same default turn handling for every room transport.
// A custom callback owns its handling and must observe context cancellation.
func HandleTextInput(ctx context.Context, session *agent.AgentSession, callback TextInputCallback, ev TextInputEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return roomIOTextInputCallback(RoomOptions{TextInputCallback: callback})(ctx, session, ev)
}
