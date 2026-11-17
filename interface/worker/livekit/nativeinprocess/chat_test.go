package nativeinprocess

import (
	"context"
	pb "github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb"
	"google.golang.org/protobuf/proto"
	"strings"
	"testing"
)

func TestChatCompletedDeliveryMetadataAndCleanup(t *testing.T) {
	var drops []uint64
	r := &Runtime{sessions: map[*Session]bool{}, pending: map[uint64]pendingRequest{}, wake: make(chan struct{}, 1), b: nativeBackend{drop: func(h uint64) error { drops = append(drops, h); return nil }, request: func(q *pb.FfiRequest) (*pb.FfiResponse, error) {
		if q.GetTextReadIncremental() == nil {
			t.Fatal(q)
		}
		return &pb.FfiResponse{}, nil
	}}}
	s := newSession(context.Background(), 1)
	s.participant = "caller"
	s.chatEnabled = true
	r.sessions[s] = true
	opened := &pb.TextStreamOpened{ParticipantIdentity: proto.String("caller"), Reader: &pb.OwnedTextStreamReader{Handle: &pb.FfiOwnedHandle{Id: proto.Uint64(20)}, Info: &pb.TextStreamInfo{StreamId: proto.String("chat-id"), Topic: proto.String("lk.chat"), Timestamp: proto.Int64(123), Attributes: map[string]string{"a": "b"}}}}
	if e := r.openChat(s, proto.Clone(opened).(*pb.TextStreamOpened)); e != nil {
		t.Fatal(e)
	}
	for _, text := range []string{"hello ", "世界"} {
		if e := r.chatEvent(&pb.TextStreamReaderEvent{ReaderHandle: proto.Uint64(20), Detail: &pb.TextStreamReaderEvent_ChunkReceived{ChunkReceived: &pb.TextStreamReaderChunkReceived{Content: proto.String(text)}}}); e != nil {
			t.Fatal(e)
		}
	}
	if len(s.chats) != 0 {
		t.Fatal("partial text delivered")
	}
	eos := &pb.TextStreamReaderEvent{ReaderHandle: proto.Uint64(20), Detail: &pb.TextStreamReaderEvent_Eos{Eos: &pb.TextStreamReaderEOS{Attributes: map[string]string{"final": "yes"}}}}
	if e := r.chatEvent(eos); e != nil {
		t.Fatal(e)
	}
	got := <-s.Chats()
	if got.Text != "hello 世界" || got.Sender != "caller" || got.Info.GetStreamId() != "chat-id" || got.Info.GetTimestamp() != 123 || got.Info.Attributes["final"] != "yes" {
		t.Fatal(got)
	}
	if len(drops) != 0 || len(s.chatReaders) != 0 {
		t.Fatal("reader leaked", drops)
	}
	if e := r.chatEvent(eos); e != nil || len(s.chats) != 0 {
		t.Fatal("late callback delivered", e)
	}
	if e := r.openChat(s, proto.Clone(opened).(*pb.TextStreamOpened)); e != nil {
		t.Fatal(e)
	}
	r.finish(s, context.Canceled)
	if len(s.chatReaders) != 0 || len(drops) != 0 {
		t.Fatal("close retained readers")
	}
	if _, ok := <-s.Chats(); ok {
		t.Fatal("delivery channel not closed")
	}
}

func TestChatDisabledFilteredAndCanceled(t *testing.T) {
	for _, mode := range []string{"disabled", "other-sender", "other-topic", "canceled", "closing"} {
		t.Run(mode, func(t *testing.T) {
			s := newSession(context.Background(), 1)
			defer s.cancel()
			s.participant = "caller"
			s.chatEnabled = mode != "disabled"
			sender, topic := "caller", "lk.chat"
			switch mode {
			case "other-sender":
				sender = "other"
			case "other-topic":
				topic = "other"
			case "canceled":
				s.cancel()
			case "closing":
				s.closing = true
			}
			r := &Runtime{b: nativeBackend{request: func(*pb.FfiRequest) (*pb.FfiResponse, error) { t.Fatal("ignored stream read"); return nil, nil }}}
			event := &pb.TextStreamOpened{ParticipantIdentity: proto.String(sender), Reader: &pb.OwnedTextStreamReader{Handle: &pb.FfiOwnedHandle{Id: proto.Uint64(20)}, Info: &pb.TextStreamInfo{Topic: proto.String(topic)}}}
			if err := r.openChat(s, event); err != nil {
				t.Fatal(err)
			}
			if len(s.chatReaders) != 0 || len(s.chats) != 0 {
				t.Fatal("ignored stream retained or delivered")
			}
		})
	}
}

func TestChatCancellationBeforeEOS(t *testing.T) {
	s := newSession(context.Background(), 1)
	s.chatEnabled, s.participant = true, "caller"
	drops := 0
	r := &Runtime{sessions: map[*Session]bool{s: true}, wake: make(chan struct{}, 1), b: nativeBackend{
		request: func(*pb.FfiRequest) (*pb.FfiResponse, error) { return &pb.FfiResponse{}, nil },
		drop:    func(uint64) error { drops++; return nil },
	}}
	event := &pb.TextStreamOpened{ParticipantIdentity: proto.String("caller"), Reader: &pb.OwnedTextStreamReader{Handle: &pb.FfiOwnedHandle{Id: proto.Uint64(20)}, Info: &pb.TextStreamInfo{Topic: proto.String("lk.chat")}}}
	if err := r.openChat(s, event); err != nil {
		t.Fatal(err)
	}
	s.cancel()
	eos := &pb.TextStreamReaderEvent{ReaderHandle: proto.Uint64(20), Detail: &pb.TextStreamReaderEvent_Eos{Eos: &pb.TextStreamReaderEOS{}}}
	if err := r.chatEvent(eos); err != nil {
		t.Fatal(err)
	}
	if len(s.chats) != 0 || len(s.chatReaders) != 0 || drops != 0 {
		t.Fatal("canceled stream delivered or leaked")
	}
	r.finish(s, context.Canceled)
	if err := r.chatEvent(eos); err != nil {
		t.Fatal(err)
	}
	if drops != 0 {
		t.Fatal("reader released twice")
	}
}

func TestChatLimitsFailOnlyAffectedSession(t *testing.T) {
	for _, mode := range []string{"readers", "declared-size", "chunk-size", "delivery-queue", "read-error"} {
		t.Run(mode, func(t *testing.T) {
			s := newSession(context.Background(), 1)
			s.participant = "caller"
			s.chatEnabled = true
			other := newSession(context.Background(), 1)
			defer other.cancel()
			r := &Runtime{sessions: map[*Session]bool{s: true, other: true}, wake: make(chan struct{}, 1), b: nativeBackend{request: func(*pb.FfiRequest) (*pb.FfiResponse, error) { return &pb.FfiResponse{}, nil }}}
			defer func() {
				if r.sessions[s] {
					r.finish(s, context.Canceled)
				}
			}()
			open := func(h uint64, size uint64) {
				t.Helper()
				err := r.openChat(s, &pb.TextStreamOpened{ParticipantIdentity: proto.String("caller"), Reader: &pb.OwnedTextStreamReader{Handle: &pb.FfiOwnedHandle{Id: proto.Uint64(h)}, Info: &pb.TextStreamInfo{Topic: proto.String("lk.chat"), TotalLength: proto.Uint64(size)}}})
				if err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "readers":
				for i := uint64(1); i <= 9; i++ {
					open(i, 0)
				}
			case "declared-size":
				open(1, maxChatBytes+1)
			default:
				open(1, 0)
				ev := &pb.TextStreamReaderEvent{ReaderHandle: proto.Uint64(1), Detail: &pb.TextStreamReaderEvent_Eos{Eos: &pb.TextStreamReaderEOS{}}}
				switch mode {
				case "chunk-size":
					ev.Detail = &pb.TextStreamReaderEvent_ChunkReceived{ChunkReceived: &pb.TextStreamReaderChunkReceived{Content: proto.String(strings.Repeat("x", maxChatBytes+1))}}
				case "delivery-queue":
					for range cap(s.chats) {
						s.chats <- Chat{}
					}
				case "read-error":
					ev.GetEos().Error = &pb.StreamError{}
				}
				if err := r.chatEvent(ev); err != nil {
					t.Fatal(err)
				}
			}
			if s.Err() == nil || len(s.chatReaders) != 0 {
				t.Fatal("limit not enforced or readers retained")
			}
			if other.Err() != nil || !r.sessions[other] {
				t.Fatal("unrelated session affected")
			}
		})
	}
}

func TestChatDispatchTransfersAcceptedHandleAndDropsIgnoredHandle(t *testing.T) {
	s := newSession(context.Background(), 1)
	s.chatEnabled = true
	s.participant = "caller"
	var drops []uint64
	r := &Runtime{sessions: map[*Session]bool{s: true}, rooms: map[uint64]*Session{1: s}, wake: make(chan struct{}, 1), b: nativeBackend{drop: func(h uint64) error { drops = append(drops, h); return nil }, request: func(*pb.FfiRequest) (*pb.FfiResponse, error) { return &pb.FfiResponse{}, nil }}}
	defer r.finish(s, context.Canceled)
	send := func(sender string, h uint64) {
		t.Helper()
		event := &pb.FfiEvent{Message: &pb.FfiEvent_RoomEvent{RoomEvent: &pb.RoomEvent{RoomHandle: proto.Uint64(1), Message: &pb.RoomEvent_TextStreamOpened{TextStreamOpened: &pb.TextStreamOpened{ParticipantIdentity: proto.String(sender), Reader: &pb.OwnedTextStreamReader{Handle: &pb.FfiOwnedHandle{Id: proto.Uint64(h)}, Info: &pb.TextStreamInfo{Topic: proto.String("lk.chat")}}}}}}}
		fillRequired(event.ProtoReflect())
		data, err := proto.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		if err = r.dispatch(callbackEvent{data: data}); err != nil {
			t.Fatal(err)
		}
	}
	send("caller", 20)
	send("other", 21)
	if len(s.chatReaders) != 1 || len(drops) != 1 || drops[0] != 21 {
		t.Fatal("native ownership mismatch", drops)
	}
}
