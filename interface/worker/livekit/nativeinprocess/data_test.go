package nativeinprocess

import (
	"context"
	"errors"
	pb "github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb"
	"google.golang.org/protobuf/proto"
	"testing"
)

func TestReliableDataUsesSessionIdentityAndRetainsPendingSlot(t *testing.T) {
	s := newSession(context.Background(), 1)
	s.local = 42
	s.published = true
	calls := 0
	r := &Runtime{sessions: map[*Session]bool{s: true}, pending: map[uint64]pendingRequest{}, b: nativeBackend{publishData: func(local, id uint64, topic string, data []byte) error {
		calls++
		if local != 42 || topic != "agent.events" || string(data) != "payload" {
			t.Fatal(local, topic, string(data))
		}
		return nil
	}}}
	send := func(ctx context.Context) chan error {
		reply := make(chan error, 1)
		r.handle(command{s: s, ctx: ctx, data: &DataPacket{"agent.events", []byte("payload")}, reply: reply})
		return reply
	}
	ctx, cancel := context.WithCancel(context.Background())
	reply := send(ctx)
	cancel()
	if err := <-send(context.Background()); err == nil {
		t.Fatal("pending slot released before ack")
	}
	_, err := r.dataCallback(&pb.FfiEvent{Message: &pb.FfiEvent_PublishData{PublishData: &pb.PublishDataCallback{AsyncId: proto.Uint64(1), Error: proto.String("denied")}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-reply; err == nil {
		t.Fatal("failure hidden")
	}
	reply = send(context.Background())
	r.finish(s, context.Canceled)
	if err := <-reply; !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}
