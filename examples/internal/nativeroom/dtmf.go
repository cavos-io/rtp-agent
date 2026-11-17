//go:build nativeinprocess

package nativeroom

import (
	"context"
	"errors"
	"github.com/cavos-io/rtp-agent/core/agent"
	tools "github.com/cavos-io/rtp-agent/core/beta/tools"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/livekit/protocol/livekit"
	native "native-receive-probe/nativeinprocess"
	"time"
)

// CallTools supplies session-bound tools for explicit application registration.
// Do not put these on an Agent shared by concurrent sessions.
func (r *Room) CallTools() []llm.Tool {
	return []llm.Tool{tools.NewSendDTMFTool(r), tools.NewEndCallTool(r, tools.EndCallToolOptions{})}
}
func (r *Room) PublishDTMF(code int32, digit string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return r.PublishDTMFContext(ctx, code, digit)
}
func (r *Room) PublishDTMFContext(ctx context.Context, code int32, digit string) error {
	r.mu.Lock()
	child, closed := r.child, r.closed
	r.mu.Unlock()
	if closed {
		return native.ErrClosed
	}
	if child == nil || child.publishDTMF == nil {
		return errors.New("in-process DTMF transport unavailable")
	}
	if code < 0 {
		return errors.New("invalid DTMF code")
	}
	return child.publishDTMF(ctx, uint32(code), digit)
}

// DeleteRoom uses the server API; no Pion room connection is required.
func (r *Room) DeleteRoom(ctx context.Context) error {
	if r.Job == nil {
		return errors.New("native call has no job")
	}
	if r.Job.IsFakeJob() {
		return errors.New("native call cannot delete a room for a fake job")
	}
	r.BeginShutdown() // Room deletion is an intentional call end, not a receive failure.
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// Use the server API directly: Job.DeleteRoom intentionally treats errors as best-effort.
	_, err := r.Job.API().RoomService.DeleteRoom(ctx, &livekit.DeleteRoomRequest{Room: r.Job.AvatarStartInfo().RoomName})
	if err != nil {
		logger.Logger.Warnw("native end-call room deletion failed", err)
	}
	return err
}
func (r *Room) Shutdown(reason string) {
	if r.Job != nil {
		r.Job.Shutdown(reason)
	}
}

func startDTMF(ctx context.Context, events <-chan native.DTMF, session *agent.AgentSession, fail context.CancelCauseFunc) func(context.Context) error {
	run, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-run.Done():
				return
			case ev, ok := <-events:
				if !ok {
					return
				}
				delivery, stop := context.WithTimeout(run, 2*time.Second)
				err := session.EmitSipDTMFContext(delivery, agent.SipDTMFEvent{Code: ev.Code, Digit: ev.Digit, SenderIdentity: ev.Sender})
				stop()
				if err != nil {
					if run.Err() == nil {
						fail(errors.New("native DTMF consumer stalled or closed"))
					}
					return
				}
			}
		}
	}()
	return func(c context.Context) error {
		cancel()
		select {
		case <-done:
			return nil
		case <-c.Done():
			return c.Err()
		}
	}
}
