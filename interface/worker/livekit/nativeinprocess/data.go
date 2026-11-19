package nativeinprocess

import (
	"context"
	"errors"
	"fmt"
	pb "github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb"
)

type DataPacket struct {
	Topic   string
	Payload []byte
}

// PublishData sends reliable data from the native agent identity. A canceled
// request is not replayed; its pending slot remains owned until acknowledgement.
func (s *Session) PublishData(ctx context.Context, topic string, data []byte) error {
	if len(topic) == 0 || len(topic) > 256 || len(data) == 0 || len(data) > 15000 {
		return errors.New("invalid native reliable data size/topic")
	}
	c := command{s: s, ctx: ctx, data: &DataPacket{topic, append([]byte(nil), data...)}, reply: make(chan error, 1)}
	if err := s.rt.submit(ctx, c); err != nil {
		return err
	}
	select {
	case err := <-c.reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return errors.Join(ErrClosed, s.Err())
	}
}
func (r *Runtime) handleData(c command) error {
	s := c.s
	err := c.ctx.Err()
	if err == nil && (!r.sessions[s] || s.closing || s.ctx.Err() != nil) {
		err = ErrClosed
	}
	if err == nil && (!s.published || s.reconnecting) {
		err = errors.New("native data unavailable during startup/reconnect")
	}
	if err == nil && s.dataPending {
		err = errors.New("native data publication still pending")
	}
	if err != nil {
		c.reply <- err
		return nil
	}
	id := r.id(s, "data", c.reply)
	if err = r.b.publishData(s.local, id, c.data.Topic, c.data.Payload); err != nil {
		delete(r.pending, id)
		c.reply <- err
		return nil
	}
	s.dataPending = true
	return nil
}
func (r *Runtime) dataCallback(ev *pb.FfiEvent) (bool, error) {
	c := ev.GetPublishData()
	if c == nil {
		return false, nil
	}
	p, ok := r.pending[c.GetAsyncId()]
	if !ok || p.kind != "data" {
		return false, nil
	}
	delete(r.pending, c.GetAsyncId())
	p.s.dataPending = false
	var err error
	if c.GetError() != "" {
		err = fmt.Errorf("native data: %s", c.GetError())
	}
	p.reply <- err
	return true, nil
}
