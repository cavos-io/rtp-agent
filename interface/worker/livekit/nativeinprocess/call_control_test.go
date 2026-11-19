package nativeinprocess

import (
	"context"
	"errors"
	pb "github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb"
	"google.golang.org/protobuf/proto"
	"testing"
)

func TestParticipantSnapshotAndDepartureIsolation(t *testing.T) {
	a, b := newSession(context.Background(), 1), newSession(context.Background(), 1)
	a.departuresEnabled = true
	a.observeParticipant("caller", true)
	a.observeParticipant("supervisor", true)
	if len(a.ParticipantIdentities()) != 2 || len(b.ParticipantIdentities()) != 0 {
		t.Fatal("snapshot crossed rooms")
	}
	if err := a.departParticipant("caller"); err != nil {
		t.Fatal(err)
	}
	if got := <-a.ParticipantDepartures(); got != "caller" {
		t.Fatal(got)
	}
	if got := a.ParticipantIdentities(); len(got) != 1 || got[0] != "supervisor" {
		t.Fatal(got)
	}
	for i := 0; i < cap(a.departures); i++ {
		if err := a.departParticipant("other"); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.departParticipant("overflow"); !errors.Is(err, ErrBackpressure) {
		t.Fatal(err)
	}
}

func TestHoldCaptureUsesSeparateSourceAndShutdownRetiresPending(t *testing.T) {
	s := newSession(context.Background(), 1)
	s.published = true
	s.source = 10
	s.holdSource = 20
	s.holdPublished = true
	r := &Runtime{sessions: map[*Session]bool{s: true}, pending: map[uint64]pendingRequest{}, b: nativeBackend{capture: func(source, id uint64, pcm []int16) error {
		if source != 20 {
			t.Fatal("hold entered speech source")
		}
		return nil
	}}}
	reply := make(chan error, 1)
	if err := r.handle(command{s: s, ctx: context.Background(), kind: "hold-capture", pcm: []int16{1}, reply: reply}); err != nil {
		t.Fatal(err)
	}
	if s.captureBusy || !s.holdBusy {
		t.Fatal("speech accounting changed")
	}
	other := make(chan error, 1)
	r.handle(command{s: s, ctx: context.Background(), kind: "hold-capture", pcm: []int16{2}, reply: other})
	if err := <-other; !errors.Is(err, ErrCaptureBusy) {
		t.Fatal(err)
	}
	r.finish(s, context.Canceled)
	if err := <-reply; !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestNativeOutputMuteAndHoldClearDoNotClearSpeech(t *testing.T) {
	s := newSession(context.Background(), 1)
	s.published = true
	s.output = 11
	s.source = 12
	s.holdSource = 21
	s.holdPublished = true
	r := &Runtime{sessions: map[*Session]bool{s: true}, b: nativeBackend{request: func(q *pb.FfiRequest) (*pb.FfiResponse, error) {
		if m := q.GetLocalTrackMute(); m != nil {
			if m.GetTrackHandle() != 11 || !m.GetMute() {
				t.Fatal(q)
			}
		} else if q.GetClearAudioBuffer().GetSourceHandle() != 21 {
			t.Fatal(q)
		}
		return &pb.FfiResponse{}, nil
	}}}
	for _, kind := range []string{"output-mute", "hold-clear"} {
		reply := make(chan error, 1)
		if err := r.handle(command{s: s, ctx: context.Background(), kind: kind, reply: reply}); err != nil {
			t.Fatal(err)
		}
		if err := <-reply; err != nil {
			t.Fatal(err)
		}
	}
	s.closing = true
	reply := make(chan error, 1)
	r.handle(command{s: s, ctx: context.Background(), kind: "output-unmute", reply: reply})
	if err := <-reply; !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestHoldClearWaitsForCaptureAcknowledgement(t *testing.T) {
	s := newSession(context.Background(), 1)
	s.published = true
	s.holdSource = 20
	s.holdPublished = true
	cleared := false
	r := &Runtime{sessions: map[*Session]bool{s: true}, pending: map[uint64]pendingRequest{}, b: nativeBackend{capture: func(uint64, uint64, []int16) error { return nil }, request: func(q *pb.FfiRequest) (*pb.FfiResponse, error) {
		if q.GetClearAudioBuffer().GetSourceHandle() != 20 {
			t.Fatal(q)
		}
		cleared = true
		return &pb.FfiResponse{}, nil
	}}}
	capture, clear := make(chan error, 1), make(chan error, 1)
	r.handle(command{s: s, ctx: context.Background(), kind: "hold-capture", pcm: []int16{1}, reply: capture})
	r.handle(command{s: s, ctx: context.Background(), kind: "hold-clear", reply: clear})
	if cleared {
		t.Fatal("clear overtook capture")
	}
	handled, err := r.holdCallback(&pb.FfiEvent{Message: &pb.FfiEvent_CaptureAudioFrame{CaptureAudioFrame: &pb.CaptureAudioFrameCallback{AsyncId: proto.Uint64(1)}}})
	if !handled || err != nil {
		t.Fatal(handled, err)
	}
	if err := <-capture; err != nil {
		t.Fatal(err)
	}
	if err := <-clear; err != nil {
		t.Fatal(err)
	}
	if !cleared || s.holdClear != nil || s.holdBusy {
		t.Fatal("hold did not settle")
	}
}
func TestHoldStartPublicationAndDisconnectOwnHandles(t *testing.T) {
	s := newSession(context.Background(), 1)
	s.published = true
	s.local = 11
	dropped := map[uint64]bool{}
	r := &Runtime{sessions: map[*Session]bool{s: true}, pending: map[uint64]pendingRequest{}, rooms: map[uint64]*Session{}, streams: map[uint64]*Session{}, b: nativeBackend{drop: func(h uint64) error { dropped[h] = true; return nil }, request: func(q *pb.FfiRequest) (*pb.FfiResponse, error) {
		switch {
		case q.GetNewAudioSource() != nil:
			return &pb.FfiResponse{Message: &pb.FfiResponse_NewAudioSource{NewAudioSource: &pb.NewAudioSourceResponse{Source: &pb.OwnedAudioSource{Handle: &pb.FfiOwnedHandle{Id: proto.Uint64(20)}}}}}, nil
		case q.GetCreateAudioTrack() != nil:
			return &pb.FfiResponse{Message: &pb.FfiResponse_CreateAudioTrack{CreateAudioTrack: &pb.CreateAudioTrackResponse{Track: &pb.OwnedTrack{Handle: &pb.FfiOwnedHandle{Id: proto.Uint64(21)}}}}}, nil
		case q.GetPublishTrack() != nil:
			if q.GetPublishTrack().GetTrackHandle() != 21 || q.GetPublishTrack().GetLocalParticipantHandle() != 11 {
				t.Fatal(q)
			}
			return &pb.FfiResponse{}, nil
		default:
			t.Fatal(q)
			return nil, nil
		}
	}}}
	reply := make(chan error, 1)
	r.handle(command{s: s, ctx: context.Background(), kind: "hold-start", reply: reply})
	if !s.holdStarting || s.holdPublished {
		t.Fatal("publication not acknowledged")
	}
	r.holdCallback(&pb.FfiEvent{Message: &pb.FfiEvent_PublishTrack{PublishTrack: &pb.PublishTrackCallback{AsyncId: proto.Uint64(1)}}})
	if err := <-reply; err != nil {
		t.Fatal(err)
	}
	if !s.holdPublished {
		t.Fatal("not published")
	}
	id := r.id(s, "disconnect", nil)
	ev := &pb.FfiEvent{Message: &pb.FfiEvent_Disconnect{Disconnect: &pb.DisconnectCallback{AsyncId: proto.Uint64(id)}}}
	data, err := proto.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.dispatch(callbackEvent{data: data}); err != nil {
		t.Fatal(err)
	}
	if !dropped[20] || !dropped[21] {
		t.Fatal("hold handles leaked", dropped)
	}
}
