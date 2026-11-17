package nativeinprocess

import (
	"context"
	"errors"
	"google.golang.org/protobuf/proto"
	pb "native-receive-probe/ffipb"
	"testing"
	"time"
)

func TestMicrophoneReplacementMuteAndOutputSubscription(t *testing.T) {
	r := &Runtime{wake: make(chan struct{}, 1), sessions: map[*Session]bool{}, rooms: map[uint64]*Session{}, streams: map[uint64]*Session{}, pending: map[uint64]pendingRequest{}, b: nativeBackend{drop: func(uint64) error { return nil }}}
	next := uint64(100)
	r.b.request = func(q *pb.FfiRequest) (*pb.FfiResponse, error) {
		next++
		return &pb.FfiResponse{Message: &pb.FfiResponse_NewAudioStream{NewAudioStream: &pb.NewAudioStreamResponse{Stream: &pb.OwnedAudioStream{Handle: &pb.FfiOwnedHandle{Id: proto.Uint64(next)}}}}}, nil
	}
	s := newSession(context.Background(), 25)
	s.microphone = true
	s.participant = "caller"
	s.room = 1
	s.outputSID = "output"
	s.published = true
	r.sessions[s] = true
	r.rooms[1] = s
	t.Cleanup(s.endRebind)
	dispatch := func(e *pb.RoomEvent) {
		t.Helper()
		e.RoomHandle = proto.Uint64(1)
		ev := &pb.FfiEvent{Message: &pb.FfiEvent_RoomEvent{RoomEvent: e}}
		fillRequired(ev.ProtoReflect())
		b, err := proto.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		if err = r.dispatch(callbackEvent{data: b}); err != nil {
			t.Fatal(err)
		}
	}
	pub := func(id, sid string, source pb.TrackSource) {
		s.observePublication(id, &pb.TrackPublicationInfo{Sid: proto.String(sid), Kind: pb.TrackKind_KIND_AUDIO.Enum(), Source: source.Enum()})
	}
	sub := func(id, sid string) {
		dispatch(&pb.RoomEvent{Message: &pb.RoomEvent_TrackSubscribed{TrackSubscribed: &pb.TrackSubscribed{ParticipantIdentity: proto.String(id), Track: &pb.OwnedTrack{Handle: &pb.FfiOwnedHandle{Id: proto.Uint64(next + 100)}, Info: &pb.TrackInfo{Sid: proto.String(sid)}}}}})
	}
	pub("caller", "mic", pb.TrackSource_SOURCE_MICROPHONE)
	pub("caller", "screen", pb.TrackSource_SOURCE_SCREENSHARE_AUDIO)
	pub("other", "other", pb.TrackSource_SOURCE_MICROPHONE)
	sub("caller", "screen")
	sub("other", "other")
	if s.stream != 0 {
		t.Fatal("wrong input selected")
	}
	sub("caller", "mic")
	old := s.stream
	dispatch(&pb.RoomEvent{Message: &pb.RoomEvent_TrackMuted{TrackMuted: &pb.TrackMuted{TrackSid: proto.String("mic"), ParticipantIdentity: proto.String("caller")}}})
	dispatch(&pb.RoomEvent{Message: &pb.RoomEvent_TrackUnmuted{TrackUnmuted: &pb.TrackUnmuted{TrackSid: proto.String("mic"), ParticipantIdentity: proto.String("caller")}}})
	if s.stream != old || s.closing {
		t.Fatal("mute retired input")
	}
	dispatch(&pb.RoomEvent{Message: &pb.RoomEvent_TrackUnsubscribed{TrackUnsubscribed: &pb.TrackUnsubscribed{TrackSid: proto.String("mic")}}})
	if !s.awaitingTrack || s.closing {
		t.Fatal("unsubscribe ended caller")
	}
	pub("caller", "new", pb.TrackSource_SOURCE_MICROPHONE)
	sub("caller", "new")
	if s.InputTrackSID() != "new" || s.stream == old || s.awaitingTrack || r.streams[old] != nil {
		t.Fatal("replacement failed")
	}
	dispatch(&pb.RoomEvent{Message: &pb.RoomEvent_TrackUnsubscribed{TrackUnsubscribed: &pb.TrackUnsubscribed{TrackSid: proto.String("mic")}}})
	if s.awaitingTrack {
		t.Fatal("stale unsubscribe retired new input")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if e := s.WaitOutputSubscribed(ctx); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("output prematurely ready", e)
	}
	for i := 0; i < 2; i++ {
		dispatch(&pb.RoomEvent{Message: &pb.RoomEvent_LocalTrackSubscribed{LocalTrackSubscribed: &pb.LocalTrackSubscribed{TrackSid: proto.String("output")}}})
	}
	if e := s.WaitOutputSubscribed(context.Background()); e != nil {
		t.Fatal(e)
	}
}

func TestOutputSubscriptionBeforePublishAndCancellation(t *testing.T) {
	s := newSession(context.Background(), 1)
	s.outputSubscriptionSID = "output"
	s.markOutputSubscribed()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := s.WaitOutputSubscribed(ctx); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	s.outputSID = "output"
	s.markOutputSubscribed()
	s.markOutputSubscribed()
	if e := s.WaitOutputSubscribed(context.Background()); e != nil {
		t.Fatal(e)
	}
	s2 := newSession(context.Background(), 1)
	s2.finish(context.Canceled)
	if e := s2.WaitOutputSubscribed(context.Background()); !errors.Is(e, ErrClosed) {
		t.Fatal(e)
	}
}

func TestMicrophoneDepartureAndRebindDeadline(t *testing.T) {
	s := newSession(context.Background(), 1)
	s.microphone = true
	s.participant = "caller"
	s.trackSID = "mic"
	r := &Runtime{sessions: map[*Session]bool{s: true}, streams: map[uint64]*Session{}, pending: map[uint64]pendingRequest{}, wake: make(chan struct{}, 1), b: nativeBackend{drop: func(uint64) error { return nil }}}
	t.Cleanup(s.endRebind)
	if e := r.observeLifecycle(s, &pb.RoomEvent{Message: &pb.RoomEvent_TrackUnsubscribed{TrackUnsubscribed: &pb.TrackUnsubscribed{TrackSid: proto.String("mic")}}}); e != nil {
		t.Fatal(e)
	}
	if s.rebindDeadline.IsZero() || s.closing || !s.awaitingTrack {
		t.Fatal("missing bounded replacement wait")
	}
	deadline := s.rebindDeadline
	r.beginRebind(s)
	if s.rebindDeadline != deadline {
		t.Fatal("repeated event extended bound")
	}
	if e := r.observeLifecycle(s, &pb.RoomEvent{Message: &pb.RoomEvent_ParticipantDisconnected{ParticipantDisconnected: &pb.ParticipantDisconnected{ParticipantIdentity: proto.String("caller")}}}); e != nil {
		t.Fatal(e)
	}
	if !errors.Is(s.Err(), ErrSourceEnded) {
		t.Fatal("departure not normal", s.Err())
	}
}
