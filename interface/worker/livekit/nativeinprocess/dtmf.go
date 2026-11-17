package nativeinprocess

import (
	"context"
	"errors"
	"fmt"
	pb "github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb"
	"google.golang.org/protobuf/proto"
	"strings"
)

var ErrDTMFOverflow = errors.New("native incoming DTMF queue full")
var ErrDTMFBusy = errors.New("native DTMF send still in flight")

type DTMF struct {
	Code          uint32
	Digit, Sender string
}

func validDTMF(v DTMF) bool {
	return len(v.Digit) == 1 && strings.Index("0123456789*#ABCD", v.Digit) == int(v.Code)
}
func (s *Session) DTMF() <-chan DTMF { return s.dtmf }
func (s *Session) deliverDTMF(v DTMF) error {
	if !validDTMF(v) {
		return errors.New("invalid native DTMF code/digit")
	}
	if e := s.ctx.Err(); e != nil {
		return e
	}
	select {
	case s.dtmf <- v:
		return nil
	default:
		return ErrDTMFOverflow
	}
}

// SendDTMF never retries. A timeout/cancellation after submission means delivery
// is uncertain. Keep the slot until native acknowledgement or session teardown.
func (s *Session) SendDTMF(ctx context.Context, code uint32, digit string) error {
	v := DTMF{Code: code, Digit: digit}
	if !validDTMF(v) {
		return errors.New("invalid DTMF code/digit")
	}
	c := command{s: s, ctx: ctx, dtmf: &v, reply: make(chan error, 1)}
	if e := s.rt.submit(ctx, c); e != nil {
		return e
	}
	select {
	case e := <-c.reply:
		return e
	case <-ctx.Done():
		return fmt.Errorf("DTMF delivery uncertain: %w", ctx.Err())
	case <-s.done:
		return errors.Join(ErrClosed, s.Err())
	}
}
func (r *Runtime) handleDTMF(c command) error {
	s := c.s
	e := c.ctx.Err()
	if e == nil && (!r.sessions[s] || s.closing || s.ctx.Err() != nil) {
		e = ErrClosed
	}
	if e == nil && (!s.published || s.reconnecting || s.awaitingTrack) {
		e = errors.New("native DTMF unavailable during startup/reconnect")
	}
	if e == nil && s.dtmfPending {
		e = ErrDTMFBusy
	}
	if e != nil {
		c.reply <- e
		return nil
	}
	id := r.id(s, "dtmf", c.reply)
	_, e = r.b.request(&pb.FfiRequest{Message: &pb.FfiRequest_PublishSipDtmf{PublishSipDtmf: &pb.PublishSipDtmfRequest{LocalParticipantHandle: proto.Uint64(s.local), Code: proto.Uint32(c.dtmf.Code), Digit: proto.String(c.dtmf.Digit), RequestAsyncId: proto.Uint64(id)}}})
	if e != nil {
		delete(r.pending, id)
		c.reply <- e
		return nil
	}
	s.dtmfPending = true
	return nil
}
func (r *Runtime) dtmfCallback(ev *pb.FfiEvent) (bool, error) {
	c := ev.GetPublishSipDtmf()
	if c == nil {
		return false, nil
	}
	if _, ok := r.pending[c.GetAsyncId()]; !ok && c.GetAsyncId() > 0 && c.GetAsyncId() <= r.next {
		return true, nil
	}
	p, e := r.resolve(c.GetAsyncId(), "dtmf")
	if e != nil {
		return true, e
	}
	p.s.dtmfPending = false
	if c.GetError() != "" {
		e = fmt.Errorf("native DTMF publication: %s", c.GetError())
	}
	p.reply <- e
	return true, nil
}
