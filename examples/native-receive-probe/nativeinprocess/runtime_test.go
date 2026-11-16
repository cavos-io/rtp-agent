package nativeinprocess

import (
	"context"
	"errors"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	pb "native-receive-probe/ffipb"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBoundedDeliveryIsolatesStalledSession(t *testing.T) {
	a, b := newSession(context.Background(), 2), newSession(context.Background(), 2)
	for i := 0; i < 2; i++ {
		if err := a.deliver(Frame{PCM: []int16{int16(i)}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.deliver(Frame{PCM: []int16{3}}); !errors.Is(err, ErrBackpressure) {
		t.Fatal("overflow must fail visibly", err)
	}
	if err := b.deliver(Frame{PCM: []int16{9}}); err != nil {
		t.Fatal(err)
	}
	if got := <-b.Frames(); got.PCM[0] != 9 {
		t.Fatal("session contamination")
	}
	for i := 0; i < 2; i++ {
		if got := <-a.Frames(); got.PCM[0] != int16(i) {
			t.Fatal("burst order changed")
		}
	}
}
func TestDeliveryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := newSession(ctx, 2)
	cancel()
	if err := s.deliver(Frame{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	s.finish(context.Canceled)
	select {
	case <-s.Done():
	case <-time.After(time.Second):
		t.Fatal("no completion")
	}
	if !errors.Is(s.Err(), context.Canceled) {
		t.Fatal(s.Err())
	}
}

// fakeBackend exercises ownership/cancellation deterministically; live native
// behavior is checked separately by TestNativeWrapperLocal.
func TestCancelLateConnectRetainsOwnership(t *testing.T) {
	events := make(chan callbackEvent, 8)
	requests := make(chan *pb.FfiRequest, 8)
	var droppedMu sync.Mutex
	var dropped []uint64
	var disposed atomic.Bool
	b := nativeBackend{events: events, request: func(req *pb.FfiRequest) (*pb.FfiResponse, error) { requests <- req; return &pb.FfiResponse{}, nil }, drop: func(h uint64) error {
		if h != 0 {
			droppedMu.Lock()
			dropped = append(dropped, h)
			droppedMu.Unlock()
		}
		return nil
	}, dispose: func() { disposed.Store(true) }, overflow: func() bool { return false }}
	rt := startRuntime(b)
	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), time.Second)
	defer cancelCleanup()
	defer rt.Close(cleanupCtx)
	ctx, cancel := context.WithCancel(context.Background())
	s, e := rt.Connect(ctx, "ws://local", "token")
	if e != nil {
		t.Fatal(e)
	}
	req := <-requests
	id := req.GetConnect().GetRequestAsyncId()
	cancel()
	waitCtx, stop := context.WithCancel(context.Background())
	stop()
	if e = s.Close(waitCtx); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if disposed.Load() {
		t.Fatal("disposed with connect in flight")
	}
	result := &pb.ConnectCallback_Result{Room: &pb.OwnedRoom{Handle: &pb.FfiOwnedHandle{Id: proto.Uint64(10)}}, LocalParticipant: &pb.OwnedParticipant{Handle: &pb.FfiOwnedHandle{Id: proto.Uint64(11)}}}
	sendFake(t, events, &pb.FfiEvent{Message: &pb.FfiEvent_Connect{Connect: &pb.ConnectCallback{AsyncId: proto.Uint64(id), Message: &pb.ConnectCallback_Result_{Result: result}}}})
	req = <-requests
	if req.GetDisconnect().GetRoomHandle() != 10 {
		t.Fatal("late room was not disconnected")
	}
	sendFake(t, events, &pb.FfiEvent{Message: &pb.FfiEvent_Disconnect{Disconnect: &pb.DisconnectCallback{AsyncId: proto.Uint64(req.GetDisconnect().GetRequestAsyncId())}}})
	if e = s.Close(cleanupCtx); e != nil {
		t.Fatal(e)
	}
	if e = rt.Close(cleanupCtx); e != nil {
		t.Fatal(e)
	}
	droppedMu.Lock()
	defer droppedMu.Unlock()
	if len(dropped) != 2 || dropped[0] != 11 || dropped[1] != 10 {
		t.Fatalf("handles=%v", dropped)
	}
	if !disposed.Load() {
		t.Fatal("no final disposal")
	}
}
func sendFake(t *testing.T, ch chan callbackEvent, ev *pb.FfiEvent) {
	t.Helper()
	fillRequired(ev.ProtoReflect())
	data, e := proto.Marshal(ev)
	if e != nil {
		t.Fatal(e)
	}
	ch <- callbackEvent{data: data}
}

func TestFailedConnectDoesNotStopOtherSessions(t *testing.T) {
	events := make(chan callbackEvent, 8)
	ids := make(chan uint64, 8)
	rt := startRuntime(nativeBackend{events: events, request: func(req *pb.FfiRequest) (*pb.FfiResponse, error) {
		ids <- req.GetConnect().GetRequestAsyncId()
		return &pb.FfiResponse{}, nil
	}, drop: func(uint64) error { return nil }, dispose: func() {}, overflow: func() bool { return false }})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	defer rt.Close(ctx)
	a, e := rt.Connect(ctx, "local", "a")
	if e != nil {
		t.Fatal(e)
	}
	aid := <-ids
	b, e := rt.Connect(ctx, "local", "b")
	if e != nil {
		t.Fatal(e)
	}
	bid := <-ids
	sendFake(t, events, &pb.FfiEvent{Message: &pb.FfiEvent_Connect{Connect: &pb.ConnectCallback{AsyncId: proto.Uint64(aid), Message: &pb.ConnectCallback_Error{Error: "rejected"}}}})
	select {
	case <-a.Done():
	case <-ctx.Done():
		t.Fatal("failed connection never ended")
	}
	if a.Err() == nil {
		t.Fatal("failure hidden")
	}
	select {
	case <-b.Done():
		t.Fatal("unrelated session closed")
	default:
	}
	sendFake(t, events, &pb.FfiEvent{Message: &pb.FfiEvent_Connect{Connect: &pb.ConnectCallback{AsyncId: proto.Uint64(bid), Message: &pb.ConnectCallback_Error{Error: "fixture cleanup"}}}})
	if e = rt.Close(ctx); e != nil {
		t.Fatal(e)
	}
}

// Supply unrelated required proto2 metadata so fixtures remain wire-valid.
func fillRequired(m protoreflect.Message) {
	fields := m.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		if f.Cardinality() == protoreflect.Required && !m.Has(f) {
			if f.Kind() == protoreflect.MessageKind {
				m.Mutable(f)
			} else {
				m.Set(f, f.Default())
			}
		}
		if f.Kind() == protoreflect.MessageKind && !f.IsList() && !f.IsMap() && m.Has(f) {
			fillRequired(m.Get(f).Message())
		}
	}
}

func TestShutdownWaitsForPendingConnect(t *testing.T) {
	events := make(chan callbackEvent, 4)
	ids := make(chan uint64, 1)
	var disposed atomic.Bool
	rt := startRuntime(nativeBackend{events: events, request: func(req *pb.FfiRequest) (*pb.FfiResponse, error) {
		ids <- req.GetConnect().GetRequestAsyncId()
		return &pb.FfiResponse{}, nil
	}, drop: func(uint64) error { return nil }, dispose: func() { disposed.Store(true) }, overflow: func() bool { return false }})
	s, e := rt.Connect(context.Background(), "local", "token")
	if e != nil {
		t.Fatal(e)
	}
	id := <-ids
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if e = rt.Close(canceled); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if disposed.Load() {
		t.Fatal("disposed before pending native connect completed")
	}
	sendFake(t, events, &pb.FfiEvent{Message: &pb.FfiEvent_Connect{Connect: &pb.ConnectCallback{AsyncId: proto.Uint64(id), Message: &pb.ConnectCallback_Error{Error: "fixture completion"}}}})
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if e = rt.Close(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-s.Done():
	default:
		t.Fatal("session unfinished")
	}
	if !disposed.Load() {
		t.Fatal("no disposal")
	}
}

func TestMalformedCallbackFailsRuntimeAndSessions(t *testing.T) {
	events := make(chan callbackEvent, 1)
	started := make(chan struct{}, 1)
	var disposed atomic.Bool
	rt := startRuntime(nativeBackend{events: events, request: func(*pb.FfiRequest) (*pb.FfiResponse, error) { started <- struct{}{}; return &pb.FfiResponse{}, nil }, drop: func(uint64) error { return nil }, dispose: func() { disposed.Store(true) }, overflow: func() bool { return false }})
	s, e := rt.Connect(context.Background(), "local", "token")
	if e != nil {
		t.Fatal(e)
	}
	<-started
	events <- callbackEvent{data: []byte{0xff}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	select {
	case <-s.Done():
	case <-ctx.Done():
		t.Fatal("fatal event not delivered")
	}
	if s.Err() == nil {
		t.Fatal("runtime error hidden from session")
	}
	if e = rt.Close(ctx); e == nil {
		t.Fatal("runtime error hidden from owner")
	}
	if !disposed.Load() {
		t.Fatal("no global cleanup after fatal event")
	}
}

func TestCanceledCaptureIsNotSubmitted(t *testing.T) {
	called := false
	rt := &Runtime{sessions: make(map[*Session]bool), b: nativeBackend{capture: func(uint64, uint64, []int16) error { called = true; return nil }}}
	s := newSession(context.Background(), 1)
	s.published = true
	rt.sessions[s] = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reply := make(chan error, 1)
	if e := rt.handle(command{s: s, ctx: ctx, pcm: []int16{1}, reply: reply}); e != nil {
		t.Fatal(e)
	}
	if e := <-reply; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if called {
		t.Fatal("submitted capture after cancellation")
	}
}

func TestLateCaptureCompletionAndBusyRejection(t *testing.T) {
	rt := &Runtime{sessions: make(map[*Session]bool), pending: make(map[uint64]pendingRequest), b: nativeBackend{capture: func(uint64, uint64, []int16) error { return nil }, drop: func(uint64) error { return nil }}}
	s := newSession(context.Background(), 1)
	s.published = true
	rt.sessions[s] = true
	first, second := make(chan error, 1), make(chan error, 1)
	if e := rt.handle(command{s: s, ctx: context.Background(), pcm: []int16{1}, reply: first}); e != nil {
		t.Fatal(e)
	}
	if e := rt.handle(command{s: s, ctx: context.Background(), pcm: []int16{2}, reply: second}); e != nil {
		t.Fatal(e)
	}
	if e := <-second; !errors.Is(e, ErrCaptureBusy) {
		t.Fatal(e)
	}
	// Disconnect may finish before a queued capture callback. The pending request
	// remains owned until its callback; it must not target a later session.
	rt.finish(s, context.Canceled)
	ev := &pb.FfiEvent{Message: &pb.FfiEvent_CaptureAudioFrame{CaptureAudioFrame: &pb.CaptureAudioFrameCallback{AsyncId: proto.Uint64(1)}}}
	data, e := proto.Marshal(ev)
	if e != nil {
		t.Fatal(e)
	}
	if e = rt.dispatch(callbackEvent{data: data}); e != nil {
		t.Fatal(e)
	}
	if e = <-first; e != nil {
		t.Fatal(e)
	}
	if len(rt.pending) != 0 || s.captureBusy {
		t.Fatal("late capture ownership leaked")
	}
}
