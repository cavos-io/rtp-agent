package nativeinprocess

import (
	"context"
	"errors"
	"fmt"
	pb "github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb"
	"google.golang.org/protobuf/proto"
	"testing"
	"time"
)

func TestClientStateSerializesLatestAndReconnect(t *testing.T) {
	var attrs []string
	var data []string
	r := &Runtime{sessions: map[*Session]bool{}, pending: map[uint64]pendingRequest{}, b: nativeBackend{request: func(q *pb.FfiRequest) (*pb.FfiResponse, error) {
		attrs = append(attrs, q.GetSetLocalAttributes().Attributes[0].GetValue())
		return &pb.FfiResponse{}, nil
	}, publishState: func(_, _ uint64, b []byte) error { data = append(data, string(b)); return nil }}}
	s := newSession(context.Background(), 2)
	s.published = true
	r.sessions[s] = true
	update := func(v string) {
		t.Helper()
		reply := make(chan error, 1)
		r.handle(command{s: s, ctx: context.Background(), reply: reply, clientState: &ClientState{Agent: v, AgentData: []byte(v), UserData: []byte("user")}})
		if e := <-reply; e != nil {
			t.Fatal(e)
		}
	}
	ack := func() {
		t.Helper()
		var id uint64
		for n, p := range r.pending {
			if p.kind == "client-state" {
				id = n
			}
		}
		if id == 0 {
			t.Fatal("no pending state")
		}
		_, e := r.clientStateCallback(&pb.FfiEvent{Message: &pb.FfiEvent_SetLocalAttributes{SetLocalAttributes: &pb.SetLocalAttributesCallback{AsyncId: proto.Uint64(id)}}})
		if e != nil {
			t.Fatal(e)
		}
	}
	update("thinking")
	update("speaking")
	update("listening")
	if len(attrs) != 1 {
		t.Fatal("concurrent writes")
	}
	ack()
	if len(attrs) != 2 || attrs[1] != "listening" {
		t.Fatal("stale state", attrs)
	}
	ack()
	ack()
	ack()
	if len(data) != 2 || data[0] != "listening" {
		t.Fatal(data)
	}
	if e := r.observeLifecycle(s, &pb.RoomEvent{Message: &pb.RoomEvent_Reconnecting{Reconnecting: &pb.Reconnecting{}}}); e != nil {
		t.Fatal(e)
	}
	update("speaking")
	if len(attrs) != 2 {
		t.Fatal("published during reconnect")
	}
	if e := r.observeLifecycle(s, &pb.RoomEvent{Message: &pb.RoomEvent_Reconnected{Reconnected: &pb.Reconnected{}}}); e != nil {
		t.Fatal(e)
	}
	if len(attrs) != 3 || attrs[2] != "speaking" {
		t.Fatal("no reconnect snapshot", attrs)
	}
	s.closing = true
	r.finish(s, context.Canceled)
	if len(r.pending) != 0 {
		t.Fatal("pending state retained")
	}
}
func TestClientStateFailureDoesNotFailPCM(t *testing.T) {
	r := &Runtime{sessions: map[*Session]bool{}, pending: map[uint64]pendingRequest{}, b: nativeBackend{request: func(*pb.FfiRequest) (*pb.FfiResponse, error) { return nil, errors.New("denied") }}}
	s := newSession(context.Background(), 1)
	s.published = true
	r.sessions[s] = true
	reply := make(chan error, 1)
	r.handle(command{s: s, ctx: context.Background(), reply: reply, clientState: &ClientState{Agent: "speaking"}})
	<-reply
	select {
	case e := <-s.ClientStateErrors():
		if e == nil {
			t.Fatal("missing error")
		}
	default:
		t.Fatal("hidden publication failure")
	}
	if s.Err() != nil || len(r.pending) != 0 {
		t.Fatal("state failed room")
	}
	if e := s.deliver(Frame{PCM: []int16{1}}); e != nil {
		t.Fatal(e)
	}
}

func TestClientStateAsyncFailureIsolationAndLateCallback(t *testing.T) {
	r := &Runtime{sessions: map[*Session]bool{}, pending: map[uint64]pendingRequest{}, b: nativeBackend{request: func(*pb.FfiRequest) (*pb.FfiResponse, error) { return &pb.FfiResponse{}, nil }}}
	a, b := newSession(context.Background(), 1), newSession(context.Background(), 1)
	for _, s := range []*Session{a, b} {
		s.published = true
		r.sessions[s] = true
		reply := make(chan error, 1)
		r.handle(command{s: s, ctx: context.Background(), reply: reply, clientState: &ClientState{Agent: "listening"}})
		<-reply
	}
	_, e := r.clientStateCallback(&pb.FfiEvent{Message: &pb.FfiEvent_SetLocalAttributes{SetLocalAttributes: &pb.SetLocalAttributesCallback{AsyncId: proto.Uint64(1), Error: proto.String("permission denied")}}})
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-a.ClientStateErrors():
	default:
		t.Fatal("failure hidden")
	}
	if a.Err() != nil || b.Err() != nil || !b.statePending {
		t.Fatal("state error crossed sessions")
	}
	r.finish(b, context.Canceled)
	if _, e = r.clientStateCallback(&pb.FfiEvent{Message: &pb.FfiEvent_SetLocalAttributes{SetLocalAttributes: &pb.SetLocalAttributesCallback{AsyncId: proto.Uint64(2)}}}); e != nil {
		t.Fatal("late callback failed runtime", e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reply := make(chan error, 1)
	r.handle(command{s: a, ctx: ctx, reply: reply, clientState: &ClientState{Agent: "speaking"}})
	if e := <-reply; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	r.finish(a, context.Canceled)
}

func TestClientStateStalledAcknowledgementBounded(t *testing.T) {
	calls := 0
	r := &Runtime{sessions: map[*Session]bool{}, pending: map[uint64]pendingRequest{}, b: nativeBackend{request: func(*pb.FfiRequest) (*pb.FfiResponse, error) { calls++; return &pb.FfiResponse{}, nil }}}
	s := newSession(context.Background(), 1)
	s.published = true
	r.sessions[s] = true
	for i := 0; i < 100; i++ {
		reply := make(chan error, 1)
		r.handle(command{s: s, ctx: context.Background(), reply: reply, clientState: &ClientState{Agent: fmt.Sprint(i)}})
		<-reply
	}
	if calls != 1 || len(r.pending) != 1 || s.stateDesired.Agent != "99" {
		t.Fatal("unbounded state writes")
	}
	select {
	case <-s.ClientStateErrors():
	case <-time.After(3 * time.Second):
		t.Fatal("missing timeout diagnostic")
	}
	if e := s.deliver(Frame{PCM: []int16{1}}); e != nil {
		t.Fatal(e)
	}
	r.finish(s, context.Canceled)
	if len(r.pending) != 0 {
		t.Fatal("pending state leaked")
	}
}
func TestClientStateAttributeFailureStillPublishesData(t *testing.T) {
	sent := 0
	r := &Runtime{sessions: map[*Session]bool{}, pending: map[uint64]pendingRequest{}, b: nativeBackend{request: func(*pb.FfiRequest) (*pb.FfiResponse, error) { return nil, errors.New("denied") }, publishState: func(uint64, uint64, []byte) error { sent++; return nil }}}
	s := newSession(context.Background(), 1)
	s.published = true
	r.sessions[s] = true
	reply := make(chan error, 1)
	r.handle(command{s: s, ctx: context.Background(), reply: reply, clientState: &ClientState{Agent: "listening", AgentData: []byte("agent")}})
	<-reply
	if sent != 1 || len(r.pending) != 1 {
		t.Fatal("attribute failure suppressed data")
	}
	r.finish(s, context.Canceled)
}

func TestClientStateStopPreventsRemainingStages(t *testing.T) {
	r := &Runtime{sessions: map[*Session]bool{}, pending: map[uint64]pendingRequest{}, b: nativeBackend{request: func(*pb.FfiRequest) (*pb.FfiResponse, error) { return &pb.FfiResponse{}, nil }, publishState: func(uint64, uint64, []byte) error { t.Fatal("state sent after stop"); return nil }}}
	s := newSession(context.Background(), 1)
	s.published = true
	r.sessions[s] = true
	reply := make(chan error, 1)
	r.handle(command{s: s, ctx: context.Background(), reply: reply, clientState: &ClientState{Agent: "listening", AgentData: []byte("agent")}})
	<-reply
	r.handle(command{s: s, ctx: context.Background(), reply: reply, stopState: true})
	<-reply
	_, e := r.clientStateCallback(&pb.FfiEvent{Message: &pb.FfiEvent_SetLocalAttributes{SetLocalAttributes: &pb.SetLocalAttributesCallback{AsyncId: proto.Uint64(1)}}})
	if e != nil {
		t.Fatal(e)
	}
	if len(r.pending) != 0 || s.stateDesired != nil {
		t.Fatal("state retained after stop")
	}
	r.finish(s, context.Canceled)
}
