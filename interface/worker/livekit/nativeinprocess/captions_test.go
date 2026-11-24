package nativeinprocess

import (
	"context"
	"errors"
	pb "github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb"
	"google.golang.org/protobuf/proto"
	"testing"
)

func TestCaptionBoundAndFailureDoNotStopPCM(t *testing.T) {
	var sent []*pb.FfiRequest
	r := &Runtime{sessions: map[*Session]bool{}, pending: map[uint64]pendingRequest{}, b: nativeBackend{request: func(q *pb.FfiRequest) (*pb.FfiResponse, error) { sent = append(sent, q); return &pb.FfiResponse{}, nil }}}
	s := newSession(context.Background(), 2)
	s.published = true
	s.local = 123
	r.sessions[s] = true
	submit := func(ctx context.Context) chan error {
		reply := make(chan error, 1)
		if e := r.handle(command{s: s, ctx: ctx, reply: reply, caption: &pb.FfiRequest{Message: &pb.FfiRequest_PublishTranscription{PublishTranscription: &pb.PublishTranscriptionRequest{ParticipantIdentity: proto.String("caller"), TrackId: proto.String("track")}}}}); e != nil {
			t.Fatal(e)
		}
		return reply
	}
	var replies []chan error
	for i := 0; i < 8; i++ {
		replies = append(replies, submit(context.Background()))
	}
	if e := <-submit(context.Background()); e == nil {
		t.Fatal("unbounded pending captions")
	}
	if len(sent) != 8 || sent[0].GetPublishTranscription().GetLocalParticipantHandle() != 123 {
		t.Fatal("ownership/bound", sent)
	}
	if e := s.deliver(Frame{PCM: []int16{7}}); e != nil {
		t.Fatal(e)
	}
	if got := <-s.Frames(); got.PCM[0] != 7 {
		t.Fatal("PCM corrupted")
	}
	handled, e := r.captionCallback(&pb.FfiEvent{Message: &pb.FfiEvent_PublishTranscription{PublishTranscription: &pb.PublishTranscriptionCallback{AsyncId: proto.Uint64(1), Error: proto.String("denied")}}})
	if !handled || e != nil {
		t.Fatal(e)
	}
	if e := <-replies[0]; e == nil {
		t.Fatal("publication failure hidden")
	}
	if s.Err() != nil || s.captionPending != 7 {
		t.Fatal("caption failure killed audio")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := <-submit(ctx); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	s.reconnecting = true
	if e := <-submit(context.Background()); e == nil {
		t.Fatal("published through reconnect")
	}
	s.reconnecting = false
	s.closing = true
	if e := <-submit(context.Background()); !errors.Is(e, ErrClosed) {
		t.Fatal(e)
	}
}

func TestCaptionCloseRetiresPendingAndAcceptsLateCompletion(t *testing.T) {
	r := &Runtime{sessions: map[*Session]bool{}, pending: map[uint64]pendingRequest{}}
	s := newSession(context.Background(), 1)
	r.sessions[s] = true
	reply := make(chan error, 1)
	id := r.id(s, "caption", reply)
	s.captionPending = 1
	r.finish(s, context.Canceled)
	if len(r.pending) != 0 || len(r.sessions) != 0 || s.captionPending != 0 {
		t.Fatal("caption retained session")
	}
	if e := <-reply; !errors.Is(e, ErrClosed) {
		t.Fatal(e)
	}
	handled, e := r.captionCallback(&pb.FfiEvent{Message: &pb.FfiEvent_SendStreamHeader{SendStreamHeader: &pb.SendStreamHeaderCallback{AsyncId: proto.Uint64(id)}}})
	if !handled || e != nil {
		t.Fatal("late completion failed worker", e)
	}
}
