package nativeinprocess

import (
	"context"
	"errors"
	"fmt"
	"google.golang.org/protobuf/proto"
	pb "native-receive-probe/ffipb"
)

// OutputTrackSID is available after WaitReady. The published track identity is
// retained across native reconnects, like the output source itself.
func (s *Session) OutputTrackSID(ctx context.Context) (string, error) {
	if e := s.WaitReady(ctx); e != nil {
		return "", e
	}
	return s.outputSID, nil
}

// PublishCaption submits only caption packet/stream requests. Session ownership
// and async IDs are supplied by the runtime, never by callers. PCM delivery does
// not wait for publication callbacks. At most eight caption requests per session
// can remain in flight, including timed-out calls awaiting late callbacks.
func (s *Session) PublishCaption(ctx context.Context, req *pb.FfiRequest) error {
	if req == nil || (req.GetPublishTranscription() == nil && req.GetSendStreamHeader() == nil && req.GetSendStreamChunk() == nil && req.GetSendStreamTrailer() == nil) {
		return errors.New("unsupported or nil caption request")
	}
	c := command{s: s, ctx: ctx, reply: make(chan error, 1), caption: proto.Clone(req).(*pb.FfiRequest)}
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
func (r *Runtime) handleCaption(c command) error {
	s := c.s
	e := c.ctx.Err()
	if e == nil && (!r.sessions[s] || s.closing || s.ctx.Err() != nil) {
		e = ErrClosed
	}
	if e == nil && (!s.published || s.reconnecting || s.awaitingTrack) {
		e = errors.New("native captions unavailable during startup/reconnect")
	}
	if e == nil && s.captionPending >= 8 {
		e = errors.New("native caption pending limit reached")
	}
	if e != nil {
		c.reply <- e
		return nil
	}
	id := r.id(s, "caption", c.reply)
	switch q := c.caption.Message.(type) {
	case *pb.FfiRequest_PublishTranscription:
		q.PublishTranscription.LocalParticipantHandle = proto.Uint64(s.local)
		q.PublishTranscription.RequestAsyncId = proto.Uint64(id)
	case *pb.FfiRequest_SendStreamHeader:
		q.SendStreamHeader.LocalParticipantHandle = proto.Uint64(s.local)
		q.SendStreamHeader.RequestAsyncId = proto.Uint64(id)
	case *pb.FfiRequest_SendStreamChunk:
		q.SendStreamChunk.LocalParticipantHandle = proto.Uint64(s.local)
		q.SendStreamChunk.RequestAsyncId = proto.Uint64(id)
	case *pb.FfiRequest_SendStreamTrailer:
		q.SendStreamTrailer.LocalParticipantHandle = proto.Uint64(s.local)
		q.SendStreamTrailer.RequestAsyncId = proto.Uint64(id)
	default:
		delete(r.pending, id)
		c.reply <- errors.New("unsupported caption request")
		return nil
	}
	if e = proto.CheckInitialized(c.caption); e != nil {
		delete(r.pending, id)
		c.reply <- e
		return nil
	}
	_, e = r.b.request(c.caption)
	if e != nil {
		delete(r.pending, id)
		c.reply <- e
		return nil
	}
	s.captionPending++
	return nil
}
func (r *Runtime) captionCallback(ev *pb.FfiEvent) (bool, error) {
	var cb interface {
		GetAsyncId() uint64
		GetError() string
	}
	switch {
	case ev.GetPublishTranscription() != nil:
		cb = ev.GetPublishTranscription()
	case ev.GetSendStreamHeader() != nil:
		cb = ev.GetSendStreamHeader()
	case ev.GetSendStreamChunk() != nil:
		cb = ev.GetSendStreamChunk()
	case ev.GetSendStreamTrailer() != nil:
		cb = ev.GetSendStreamTrailer()
	default:
		return false, nil
	}
	// Session teardown retires caption requests without retaining the session.
	// Late completions for already allocated IDs have no owned native handles.
	if _, ok := r.pending[cb.GetAsyncId()]; !ok && cb.GetAsyncId() > 0 && cb.GetAsyncId() <= r.next {
		return true, nil
	}
	p, e := r.resolve(cb.GetAsyncId(), "caption")
	if e != nil {
		return true, e
	}
	p.s.captionPending--
	if cb.GetError() != "" {
		e = fmt.Errorf("native caption publication: %s", cb.GetError())
	}
	p.reply <- e
	return true, nil
}
