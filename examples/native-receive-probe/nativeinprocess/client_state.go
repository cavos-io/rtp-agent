package nativeinprocess

import (
	"context"
	"errors"
	"fmt"
	pb "github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb"
	"google.golang.org/protobuf/proto"
	"time"
)

// ClientState is a current-state snapshot, not an event-history queue. Payloads
// use the application's existing client mapping; the native wrapper owns ordering.
type ClientState struct {
	Agent               string
	AgentData, UserData []byte
}

func (s *Session) ClientStateErrors() <-chan error { return s.stateErrors }

// UpdateClientState acknowledges ownership, not remote receipt. Publication
// errors arrive on ClientStateErrors. Only the latest pending snapshot is kept.
func (s *Session) UpdateClientState(ctx context.Context, v ClientState) error {
	if len(v.Agent) > 128 || len(v.AgentData) > 4096 || len(v.UserData) > 4096 {
		return errors.New("client state exceeds bounded payload size")
	}
	v.AgentData = append([]byte(nil), v.AgentData...)
	v.UserData = append([]byte(nil), v.UserData...)
	c := command{s: s, ctx: ctx, clientState: &v, reply: make(chan error, 1)}
	if e := s.rt.submit(ctx, c); e != nil {
		return e
	}
	select {
	case e := <-c.reply:
		return e
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return ErrClosed
	}
}
func (s *Session) reportStateError(e error) {
	select {
	case <-s.ctx.Done():
		return
	default:
	}
	// Keep the latest diagnostic if the consumer stalls; never block the owner.
	select {
	case s.stateErrors <- e:
	default:
		select {
		case <-s.stateErrors:
		default:
		}
		select {
		case s.stateErrors <- e:
		default:
		}
	}
}
func (r *Runtime) handleClientState(c command) error {
	e := c.ctx.Err()
	s := c.s
	if e == nil && (!r.sessions[s] || s.stateStopped || s.closing || s.ctx.Err() != nil) {
		e = ErrClosed
	}
	if e != nil {
		c.reply <- e
		return nil
	}
	s.stateDesired = c.clientState
	s.stateDirty = true
	r.pumpClientState(s)
	c.reply <- nil
	return nil
}
func (r *Runtime) pumpClientState(s *Session) {
	if s.stateStopped || s.statePending || s.closing || s.ctx.Err() != nil || s.reconnecting || !s.published {
		return
	}
	if s.stateDirty {
		s.stateActive = s.stateDesired
		s.stateStep = 0
		s.stateDirty = false
	}
	v := s.stateActive
	if v == nil {
		return
	}
	// Empty mapped data means that the default transport suppresses this state.
	for s.stateStep < 3 && s.stateStep > 0 {
		b := v.AgentData
		if s.stateStep == 2 {
			b = v.UserData
		}
		if len(b) > 0 {
			break
		}
		s.stateStep++
	}
	if s.stateStep == 3 {
		s.stateActive = nil
		return
	}
	id := r.id(s, "client-state", nil)
	var e error
	if s.stateStep == 0 {
		_, e = r.b.request(&pb.FfiRequest{Message: &pb.FfiRequest_SetLocalAttributes{SetLocalAttributes: &pb.SetLocalAttributesRequest{LocalParticipantHandle: proto.Uint64(s.local), RequestAsyncId: proto.Uint64(id), Attributes: []*pb.AttributesEntry{{Key: proto.String("lk.agent.state"), Value: proto.String(v.Agent)}}}}})
	} else {
		b := v.AgentData
		if s.stateStep == 2 {
			b = v.UserData
		}
		e = r.b.publishState(s.local, id, b)
	}
	if e != nil {
		delete(r.pending, id)
		s.stateStep++
		s.reportStateError(fmt.Errorf("native client state: %w", e))
		r.pumpClientState(s)
		return
	}
	s.statePending = true
	// A missing callback never permits a newer write to overtake the old one.
	// Report the stall, retain one request, and retire it at session teardown.
	s.stateTimer = time.AfterFunc(2*time.Second, func() { s.reportStateError(errors.New("native client state acknowledgement timed out")) })
}
func (r *Runtime) clientStateCallback(ev *pb.FfiEvent) (bool, error) {
	var cb interface {
		GetAsyncId() uint64
		GetError() string
	}
	if c := ev.GetSetLocalAttributes(); c != nil {
		cb = c
	} else if c := ev.GetPublishData(); c != nil {
		cb = c
	} else {
		return false, nil
	}
	if _, ok := r.pending[cb.GetAsyncId()]; !ok && cb.GetAsyncId() > 0 && cb.GetAsyncId() <= r.next {
		return true, nil
	}
	p, e := r.resolve(cb.GetAsyncId(), "client-state")
	if e != nil {
		return true, e
	}
	s := p.s
	if s.stateTimer != nil {
		s.stateTimer.Stop()
		s.stateTimer = nil
	}
	s.statePending = false
	if cb.GetError() != "" {
		s.stateStep++
		s.reportStateError(fmt.Errorf("native client state: %s", cb.GetError()))
	} else {
		s.stateStep++
	}
	r.pumpClientState(s)
	return true, nil
}

// StopClientState prevents any remaining snapshot stages from being published.
// The in-flight request is still owned until callback or session teardown.
func (s *Session) StopClientState(ctx context.Context) error {
	c := command{s: s, ctx: ctx, stopState: true, reply: make(chan error, 1)}
	if e := s.rt.submit(ctx, c); e != nil {
		return e
	}
	select {
	case e := <-c.reply:
		return e
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return nil
	}
}
