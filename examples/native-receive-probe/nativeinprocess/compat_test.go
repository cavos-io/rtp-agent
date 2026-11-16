package nativeinprocess

import (
	"context"
	"errors"
	"fmt"
	"google.golang.org/protobuf/proto"
	pb "native-receive-probe/ffipb"
	"testing"
)

func TestPauseWaitsForCaptureThenClears(t *testing.T) {
	var calls []string
	rt := &Runtime{sessions: make(map[*Session]bool), pending: make(map[uint64]pendingRequest), b: nativeBackend{request: func(req *pb.FfiRequest) (*pb.FfiResponse, error) {
		if req.GetClearAudioBuffer() != nil {
			calls = append(calls, "clear")
		}
		return &pb.FfiResponse{}, nil
	}, capture: func(uint64, uint64, []int16) error { calls = append(calls, "capture"); return nil }, drop: func(uint64) error { return nil }}}
	s := newSession(context.Background(), 2)
	s.published = true
	rt.sessions[s] = true
	captureReply, pauseReply := make(chan error, 1), make(chan error, 1)
	if e := rt.handle(command{s: s, ctx: context.Background(), pcm: []int16{1}, reply: captureReply}); e != nil {
		t.Fatal(e)
	}
	if e := rt.handle(command{s: s, ctx: context.Background(), kind: "pause", reply: pauseReply}); e != nil {
		t.Fatal(e)
	}
	if len(calls) != 1 {
		t.Fatal("cleared before capture completion", calls)
	}
	raw, _ := proto.Marshal(&pb.FfiEvent{Message: &pb.FfiEvent_CaptureAudioFrame{CaptureAudioFrame: &pb.CaptureAudioFrameCallback{AsyncId: proto.Uint64(1)}}})
	if e := rt.dispatch(callbackEvent{data: raw}); e != nil {
		t.Fatal(e)
	}
	if e := <-pauseReply; e != nil {
		t.Fatal(e)
	}
	if len(calls) != 2 || calls[1] != "clear" {
		t.Fatal(calls)
	}
	queued := make(chan error, 1)
	if e := rt.handle(command{s: s, ctx: context.Background(), pcm: []int16{2}, reply: queued}); e != nil {
		t.Fatal(e)
	}
	if len(calls) != 2 {
		t.Fatal("capture advanced while paused")
	}
	resume := make(chan error, 1)
	if e := rt.handle(command{s: s, ctx: context.Background(), kind: "resume", reply: resume}); e != nil {
		t.Fatal(e)
	}
	if e := <-resume; e != nil {
		t.Fatal(e)
	}
	if len(calls) != 3 || calls[2] != "capture" {
		t.Fatal("resume lost pending content", calls)
	}
}
func TestClearInvalidatesPausedCapture(t *testing.T) {
	rt := &Runtime{sessions: make(map[*Session]bool), b: nativeBackend{request: func(*pb.FfiRequest) (*pb.FfiResponse, error) { return &pb.FfiResponse{}, nil }}}
	s := newSession(context.Background(), 2)
	s.published = true
	s.paused = true
	rt.sessions[s] = true
	captureReply := make(chan error, 1)
	if e := rt.handle(command{s: s, ctx: context.Background(), pcm: []int16{9}, reply: captureReply}); e != nil {
		t.Fatal(e)
	}
	clearReply := make(chan error, 1)
	if e := rt.handle(command{s: s, ctx: context.Background(), kind: "clear", reply: clearReply}); e != nil {
		t.Fatal(e)
	}
	if e := <-captureReply; !errors.Is(e, context.Canceled) {
		t.Fatal("old content survived clear", e)
	}
	if e := <-clearReply; e != nil {
		t.Fatal(e)
	}
	if s.paused || s.parked != nil {
		t.Fatal("clear retained paused content")
	}
}

func TestReconnectRetiresOnlySelectedInputAndKeepsOutput(t *testing.T) {
	var dropped []uint64
	rt := &Runtime{wake: make(chan struct{}, 1), sessions: make(map[*Session]bool), streams: make(map[uint64]*Session), b: nativeBackend{drop: func(h uint64) error {
		if h != 0 {
			dropped = append(dropped, h)
		}
		return nil
	}}}
	s := newSession(context.Background(), 2)
	s.room = 1
	s.stream = 2
	s.track = 3
	s.source = 4
	s.output = 5
	s.participant = "caller"
	s.trackSID = "selected"
	rt.sessions[s] = true
	rt.streams[2] = s
	t.Cleanup(s.endRebind)
	for _, event := range []*pb.RoomEvent{
		{Message: &pb.RoomEvent_Reconnecting{Reconnecting: &pb.Reconnecting{}}},
		{Message: &pb.RoomEvent_TrackUnsubscribed{TrackUnsubscribed: &pb.TrackUnsubscribed{TrackSid: proto.String("other")}}},
		{Message: &pb.RoomEvent_TrackUnsubscribed{TrackUnsubscribed: &pb.TrackUnsubscribed{TrackSid: proto.String("selected")}}},
		{Message: &pb.RoomEvent_ParticipantDisconnected{ParticipantDisconnected: &pb.ParticipantDisconnected{ParticipantIdentity: proto.String("caller")}}},
		{Message: &pb.RoomEvent_Reconnected{Reconnected: &pb.Reconnected{}}},
	} {
		if e := rt.observeLifecycle(s, event); e != nil {
			t.Fatal(e)
		}
	}
	if len(dropped) != 2 || dropped[0] != 2 || dropped[1] != 3 || s.source != 4 || s.output != 5 || s.closing || !s.awaitingTrack || s.rebindDeadline.IsZero() {
		t.Fatalf("reconnect ownership wrong: dropped=%v", dropped)
	}
	if _, ok := rt.streams[2]; ok {
		t.Fatal("old stream remains routed")
	}
}
func TestNormalUnsubscribeIsSourceEnd(t *testing.T) {
	rt := &Runtime{sessions: make(map[*Session]bool)}
	s := newSession(context.Background(), 1)
	s.trackSID = "selected"
	rt.sessions[s] = true
	e := rt.observeLifecycle(s, &pb.RoomEvent{Message: &pb.RoomEvent_TrackUnsubscribed{TrackUnsubscribed: &pb.TrackUnsubscribed{TrackSid: proto.String("selected")}}})
	if e != nil {
		t.Fatal(e)
	}
	if !errors.Is(s.Err(), ErrSourceEnded) {
		t.Fatal(s.Err())
	}
}
func TestPauseClearFailureVisible(t *testing.T) {
	failure := errors.New("clear failed")
	rt := &Runtime{sessions: make(map[*Session]bool), b: nativeBackend{request: func(*pb.FfiRequest) (*pb.FfiResponse, error) { return nil, failure }}}
	s := newSession(context.Background(), 1)
	s.published = true
	rt.sessions[s] = true
	reply := make(chan error, 1)
	if e := rt.handle(command{s: s, ctx: context.Background(), kind: "pause", reply: reply}); e != nil {
		t.Fatal(e)
	}
	if e := <-reply; !errors.Is(e, failure) {
		t.Fatal(e)
	}
	if !errors.Is(s.Err(), failure) {
		t.Fatal("clear failure hidden")
	}
}

func TestSameSIDRebindAndLateOldStream(t *testing.T) {
	var dropped []uint64
	rt := &Runtime{wake: make(chan struct{}, 1), sessions: make(map[*Session]bool), rooms: make(map[uint64]*Session), streams: make(map[uint64]*Session), b: nativeBackend{drop: func(h uint64) error {
		if h != 0 {
			dropped = append(dropped, h)
		}
		return nil
	}, request: func(req *pb.FfiRequest) (*pb.FfiResponse, error) {
		if req.GetNewAudioStream() == nil {
			t.Fatal("unexpected request")
		}
		return &pb.FfiResponse{Message: &pb.FfiResponse_NewAudioStream{NewAudioStream: &pb.NewAudioStreamResponse{Stream: &pb.OwnedAudioStream{Handle: &pb.FfiOwnedHandle{Id: proto.Uint64(20)}}}}}, nil
	}}}
	s := newSession(context.Background(), 2)
	s.room = 1
	s.stream = 2
	s.track = 3
	s.source = 4
	s.participant = "caller"
	s.trackSID = "selected"
	rt.sessions[s] = true
	rt.rooms[1] = s
	rt.streams[2] = s
	if e := rt.retireInput(s); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.endRebind)
	event := &pb.FfiEvent{Message: &pb.FfiEvent_RoomEvent{RoomEvent: &pb.RoomEvent{RoomHandle: proto.Uint64(1), Message: &pb.RoomEvent_TrackSubscribed{TrackSubscribed: &pb.TrackSubscribed{ParticipantIdentity: proto.String("caller"), Track: &pb.OwnedTrack{Handle: &pb.FfiOwnedHandle{Id: proto.Uint64(30)}, Info: &pb.TrackInfo{Sid: proto.String("selected")}}}}}}}
	fillRequired(event.ProtoReflect())
	data, e := proto.Marshal(event)
	if e != nil {
		t.Fatal(e)
	}
	if e = rt.dispatch(callbackEvent{data: data}); e != nil {
		t.Fatal(e)
	}
	if s.stream != 20 || s.track != 30 || rt.streams[20] != s || !s.rebindDeadline.IsZero() || s.awaitingTrack {
		t.Fatal("same-SID track failed to rebind")
	}
	old := &pb.FfiEvent{Message: &pb.FfiEvent_AudioStreamEvent{AudioStreamEvent: &pb.AudioStreamEvent{StreamHandle: proto.Uint64(2), Message: &pb.AudioStreamEvent_FrameReceived{FrameReceived: &pb.AudioFrameReceived{Frame: &pb.OwnedAudioFrameBuffer{Handle: &pb.FfiOwnedHandle{Id: proto.Uint64(99)}}}}}}}
	fillRequired(old.ProtoReflect())
	data, e = proto.Marshal(old)
	if e != nil {
		t.Fatal(e)
	}
	if e = rt.dispatch(callbackEvent{data: data}); e != nil {
		t.Fatal(e)
	}
	if len(s.frames) != 0 || dropped[len(dropped)-1] != 99 {
		t.Fatal("old stream frame delivered or leaked", dropped)
	}
}
func TestPausedCaptureCancellationAndClose(t *testing.T) {
	for _, closeSession := range []bool{false, true} {
		t.Run(fmt.Sprint(closeSession), func(t *testing.T) {
			rt := &Runtime{sessions: make(map[*Session]bool)}
			s := newSession(context.Background(), 1)
			s.published = true
			s.paused = true
			rt.sessions[s] = true
			ctx, cancel := context.WithCancel(context.Background())
			reply := make(chan error, 1)
			if e := rt.handle(command{s: s, ctx: ctx, pcm: []int16{1}, reply: reply}); e != nil {
				t.Fatal(e)
			}
			if closeSession {
				rt.finish(s, context.Canceled)
			} else {
				cancel()
				resume := make(chan error, 1)
				if e := rt.handle(command{s: s, ctx: context.Background(), kind: "resume", reply: resume}); e != nil {
					t.Fatal(e)
				}
			}
			cancel()
			if e := <-reply; !errors.Is(e, context.Canceled) {
				t.Fatal("parked producer not released", e)
			}
			if s.parked != nil {
				t.Fatal("retained paused capture")
			}
		})
	}
}

func TestDifferentSIDCannotReplaceReconnectingSource(t *testing.T) {
	rt := &Runtime{pending: make(map[uint64]pendingRequest), sessions: make(map[*Session]bool), rooms: make(map[uint64]*Session), b: nativeBackend{drop: func(uint64) error { return nil }, request: func(req *pb.FfiRequest) (*pb.FfiResponse, error) {
		if req.GetDisconnect() == nil {
			t.Fatal("unexpected subscription to replacement")
		}
		return &pb.FfiResponse{}, nil
	}}}
	s := newSession(context.Background(), 1)
	s.room = 1
	s.participant = "caller"
	s.trackSID = "selected"
	s.awaitingTrack = true
	rt.sessions[s] = true
	rt.rooms[1] = s
	ev := &pb.FfiEvent{Message: &pb.FfiEvent_RoomEvent{RoomEvent: &pb.RoomEvent{RoomHandle: proto.Uint64(1), Message: &pb.RoomEvent_TrackSubscribed{TrackSubscribed: &pb.TrackSubscribed{ParticipantIdentity: proto.String("caller"), Track: &pb.OwnedTrack{Handle: &pb.FfiOwnedHandle{Id: proto.Uint64(9)}, Info: &pb.TrackInfo{Sid: proto.String("replacement")}}}}}}}
	fillRequired(ev.ProtoReflect())
	data, e := proto.Marshal(ev)
	if e != nil {
		t.Fatal(e)
	}
	if e = rt.dispatch(callbackEvent{data: data}); e != nil {
		t.Fatal(e)
	}
	if !s.closing || s.Err() == nil || s.track != 0 {
		t.Fatal("replacement SID silently accepted")
	}
}

func TestSelectedSubscriptionReadyWithoutPCM(t *testing.T) {
	var drops []uint64
	rt := &Runtime{sessions: make(map[*Session]bool), rooms: make(map[uint64]*Session), streams: make(map[uint64]*Session), b: nativeBackend{drop: func(h uint64) error { drops = append(drops, h); return nil }, request: func(req *pb.FfiRequest) (*pb.FfiResponse, error) {
		if req.GetNewAudioStream() == nil {
			t.Fatal("unexpected request")
		}
		return &pb.FfiResponse{Message: &pb.FfiResponse_NewAudioStream{NewAudioStream: &pb.NewAudioStreamResponse{Stream: &pb.OwnedAudioStream{Handle: &pb.FfiOwnedHandle{Id: proto.Uint64(20)}}}}}, nil
	}}}
	s := newSession(context.Background(), 1)
	s.room = 1
	s.explicitSelection = true
	s.participant = "caller"
	s.trackSID = "selected"
	rt.sessions[s] = true
	rt.rooms[1] = s
	for i, sid := range []string{"unselected", "selected"} {
		ev := &pb.FfiEvent{Message: &pb.FfiEvent_RoomEvent{RoomEvent: &pb.RoomEvent{RoomHandle: proto.Uint64(1), Message: &pb.RoomEvent_TrackSubscribed{TrackSubscribed: &pb.TrackSubscribed{ParticipantIdentity: proto.String("caller"), Track: &pb.OwnedTrack{Handle: &pb.FfiOwnedHandle{Id: proto.Uint64(uint64(8 + i))}, Info: &pb.TrackInfo{Sid: proto.String(sid)}}}}}}}
		fillRequired(ev.ProtoReflect())
		data, e := proto.Marshal(ev)
		if e != nil {
			t.Fatal(e)
		}
		if e = rt.dispatch(callbackEvent{data: data}); e != nil {
			t.Fatal(e)
		}
		if i == 0 {
			select {
			case <-s.subscribed:
				t.Fatal("unselected track satisfied barrier")
			default:
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if e := s.WaitSubscribed(ctx); e != nil {
		t.Fatal(e)
	}
	if len(s.frames) != 0 || s.stream != 20 || len(drops) != 1 || drops[0] != 8 {
		t.Fatal("bad selection/readiness", drops)
	}
}
