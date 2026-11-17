//go:build nativeinprocess

package nativeroom

import (
	"context"
	"github.com/cavos-io/rtp-agent/core/agent"
	wl "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	"github.com/cavos-io/rtp-agent/library/logger"
	native "native-receive-probe/nativeinprocess"
)

// One consumer per session preserves completion order without blocking the FFI
// owner. Close cancels cooperative callbacks and bounds waiting for others.
func startTextInput(ctx context.Context, events <-chan native.Chat, session *agent.AgentSession, callback wl.TextInputCallback) func(context.Context) error {
	run, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-run.Done():
				return
			case ev, ok := <-events:
				if !ok || run.Err() != nil {
					return
				}
				deliverTextInput(run, session, callback, ev)
			}
		}
	}()
	return func(ctx context.Context) error {
		cancel()
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
func deliverTextInput(ctx context.Context, session *agent.AgentSession, callback wl.TextInputCallback, ev native.Chat) {
	defer func() {
		if v := recover(); v != nil {
			logger.Logger.Warnw("failed to handle native chat text stream", nil, "panic", v)
		}
	}()
	err := wl.HandleTextInput(ctx, session, callback, wl.TextInputEvent{Text: ev.Text, Info: ev.Info, ParticipantIdentity: ev.Sender})
	if err != nil && ctx.Err() == nil {
		logger.Logger.Warnw("failed to handle native chat text stream", err)
	}
}
