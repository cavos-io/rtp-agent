package nativeinprocess

import (
	"context"
	"errors"
	pb "github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb"
	"google.golang.org/protobuf/proto"
)

// Pause waits for an in-flight capture acknowledgement, then clears the native
// source. Unsubmitted PCM stays bounded to one parked Capture and resumes in order.
// Native-queued audio is discarded, matching the helper's interruption contract.
func (s *Session) Pause(ctx context.Context) error  { return s.controlRequest(ctx, "pause") }
func (s *Session) Resume(ctx context.Context) error { return s.controlRequest(ctx, "resume") }

// Clear is a hard interruption: cancel parked PCM, drain in-flight capture, clear
// native output and unpause. It does not replay already acknowledged samples.
func (s *Session) Clear(ctx context.Context) error { return s.controlRequest(ctx, "clear") }
func (s *Session) controlRequest(ctx context.Context, kind string) error {
	c := command{s: s, ctx: ctx, kind: kind, reply: make(chan error, 1)}
	if e := s.rt.submit(ctx, c); e != nil {
		return e
	}
	select {
	case e := <-c.reply:
		return e
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return errors.Join(ErrClosed, s.Err())
	}
}
func (r *Runtime) playbackControl(c command) error {
	s := c.s
	e := c.ctx.Err()
	if e == nil && (!r.sessions[s] || s.closing || s.ctx.Err() != nil) {
		e = ErrClosed
	}
	if e == nil && !s.published {
		e = errors.New("native output not ready")
	}
	if e == nil && s.control != nil {
		e = ErrControlBusy
	}
	if e != nil {
		c.reply <- e
		return nil
	}
	switch c.kind {
	case "resume":
		s.paused = false
		c.reply <- nil
		if s.parked != nil {
			parked := *s.parked
			s.parked = nil
			return r.handle(parked)
		}
		return nil
	case "pause":
		if s.paused {
			c.reply <- nil
			return nil
		}
		s.paused = true
	case "clear":
		if s.parked != nil {
			s.parked.reply <- context.Canceled
			s.parked = nil
		}
	default:
		c.reply <- errors.New("unknown playback control")
		return nil
	}
	s.control = &c
	if !s.captureBusy {
		return r.finishControl(s)
	}
	return nil
}
func (r *Runtime) finishControl(s *Session) error {
	c := s.control
	if c == nil {
		return nil
	}
	s.control = nil
	if s.closing || s.ctx.Err() != nil {
		c.reply <- ErrClosed
		return nil
	}
	_, e := r.b.request(&pb.FfiRequest{Message: &pb.FfiRequest_ClearAudioBuffer{ClearAudioBuffer: &pb.ClearAudioBufferRequest{SourceHandle: proto.Uint64(s.source)}}})
	if c.kind == "clear" {
		s.paused = false
	}
	c.reply <- e
	if e != nil {
		return r.failSession(s, e)
	}
	return nil
}
