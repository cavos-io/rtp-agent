package nativeinprocess

import (
	"context"
	"errors"
	"google.golang.org/protobuf/proto"
	pb "native-receive-probe/ffipb"
	"testing"
)

func TestDTMFOrderRepeatsAndOverflow(t *testing.T) {
	s := newSession(context.Background(), 1)
	for i := 0; i < 32; i++ {
		if e := s.deliverDTMF(DTMF{Code: 1, Digit: "1", Sender: "caller"}); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.deliverDTMF(DTMF{Code: 2, Digit: "2"}); !errors.Is(e, ErrDTMFOverflow) {
		t.Fatal(e)
	}
	for i := 0; i < 32; i++ {
		if v := <-s.DTMF(); v.Digit != "1" || v.Sender != "caller" {
			t.Fatal(v)
		}
	}
}
func TestDTMFSendBusyReconnectFailureAndLateCallback(t *testing.T) {
	calls := 0
	r := &Runtime{sessions: map[*Session]bool{}, pending: map[uint64]pendingRequest{}, b: nativeBackend{request: func(q *pb.FfiRequest) (*pb.FfiResponse, error) {
		calls++
		if q.GetPublishSipDtmf().GetCode() != 11 {
			t.Fatal(q)
		}
		return &pb.FfiResponse{}, nil
	}}}
	s := newSession(context.Background(), 1)
	s.published = true
	r.sessions[s] = true
	send := func() chan error {
		c := make(chan error, 1)
		r.handle(command{s: s, ctx: context.Background(), reply: c, dtmf: &DTMF{Code: 11, Digit: "#"}})
		return c
	}
	reply := send()
	if e := <-send(); !errors.Is(e, ErrDTMFBusy) {
		t.Fatal(e)
	}
	_, e := r.dtmfCallback(&pb.FfiEvent{Message: &pb.FfiEvent_PublishSipDtmf{PublishSipDtmf: &pb.PublishSipDtmfCallback{AsyncId: proto.Uint64(1), Error: proto.String("denied")}}})
	if e != nil {
		t.Fatal(e)
	}
	if e := <-reply; e == nil {
		t.Fatal("failure hidden")
	}
	s.reconnecting = true
	if e := <-send(); e == nil {
		t.Fatal("sent during reconnect")
	}
	if calls != 1 {
		t.Fatal("replayed")
	}
	s.reconnecting = false
	reply = send()
	r.finish(s, context.Canceled)
	if e := <-reply; !errors.Is(e, ErrClosed) {
		t.Fatal(e)
	}
	if _, e = r.dtmfCallback(&pb.FfiEvent{Message: &pb.FfiEvent_PublishSipDtmf{PublishSipDtmf: &pb.PublishSipDtmfCallback{AsyncId: proto.Uint64(2)}}}); e != nil {
		t.Fatal(e)
	}
}

func TestDTMFCanceledAndInvalidSends(t *testing.T) {
	s := newSession(context.Background(), 1)
	for _, v := range []DTMF{{Code: 1, Digit: "2"}, {Code: 16, Digit: "D"}, {Code: 0, Digit: ""}} {
		if e := s.SendDTMF(context.Background(), v.Code, v.Digit); e == nil {
			t.Fatal("invalid send accepted", v)
		}
	}
	r := &Runtime{sessions: map[*Session]bool{s: true}}
	s.published = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reply := make(chan error, 1)
	if e := r.handleDTMF(command{s: s, ctx: ctx, dtmf: &DTMF{Code: 1, Digit: "1"}, reply: reply}); e != nil {
		t.Fatal(e)
	}
	if e := <-reply; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

func TestDTMFNativeEventRoutingAndOverflowIsolation(t *testing.T) {
	a, b := newSession(context.Background(), 1), newSession(context.Background(), 1)
	a.room, b.room = 1, 2
	r := &Runtime{rooms: map[uint64]*Session{1: a, 2: b}, sessions: map[*Session]bool{a: true, b: true}, pending: map[uint64]pendingRequest{}, b: nativeBackend{request: func(*pb.FfiRequest) (*pb.FfiResponse, error) { return &pb.FfiResponse{}, nil }}}
	event := &pb.FfiEvent{Message: &pb.FfiEvent_RoomEvent{RoomEvent: &pb.RoomEvent{RoomHandle: proto.Uint64(1), Message: &pb.RoomEvent_DataPacketReceived{DataPacketReceived: &pb.DataPacketReceived{Kind: pb.DataPacketKind_KIND_RELIABLE.Enum(), ParticipantIdentity: proto.String("caller"), Value: &pb.DataPacketReceived_SipDtmf{SipDtmf: &pb.SipDTMF{Code: proto.Uint32(1), Digit: proto.String("1")}}}}}}}
	raw, e := proto.Marshal(event)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 33; i++ {
		if e := r.dispatch(callbackEvent{data: raw}); e != nil {
			t.Fatal(e)
		}
	}
	if !errors.Is(a.Err(), ErrDTMFOverflow) || !a.closing {
		t.Fatal("overflow not visible", a.Err())
	}
	if b.Err() != nil || len(b.dtmf) != 0 {
		t.Fatal("DTMF crossed sessions")
	}
	for i := 0; i < 32; i++ {
		v := <-a.dtmf
		if v.Code != 1 || v.Digit != "1" || v.Sender != "caller" {
			t.Fatal(v)
		}
	}
}

func TestDTMFCancelAfterSubmitDoesNotReplay(t *testing.T) {
	r := &Runtime{ctx: context.Background(), commands: make(chan command), done: make(chan struct{}), sessions: map[*Session]bool{}, pending: map[uint64]pendingRequest{}, b: nativeBackend{request: func(*pb.FfiRequest) (*pb.FfiResponse, error) { return &pb.FfiResponse{}, nil }}}
	s := newSession(context.Background(), 1)
	s.rt = r
	s.published = true
	r.sessions[s] = true
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.SendDTMF(ctx, 1, "1") }()
	c := <-r.commands
	if e := r.handleDTMF(c); e != nil {
		t.Fatal(e)
	}
	cancel()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if !s.dtmfPending || len(r.pending) != 1 {
		t.Fatal("uncertain send slot released")
	}
	reply := make(chan error, 1)
	r.handleDTMF(command{s: s, ctx: context.Background(), dtmf: &DTMF{Code: 1, Digit: "1"}, reply: reply})
	if e := <-reply; !errors.Is(e, ErrDTMFBusy) {
		t.Fatal(e)
	}
	_, e := r.dtmfCallback(&pb.FfiEvent{Message: &pb.FfiEvent_PublishSipDtmf{PublishSipDtmf: &pb.PublishSipDtmfCallback{AsyncId: proto.Uint64(1)}}})
	if e != nil || s.dtmfPending || len(r.pending) != 0 {
		t.Fatal("late completion failed", e)
	}
}
