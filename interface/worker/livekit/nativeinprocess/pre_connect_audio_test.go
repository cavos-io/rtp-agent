package nativeinprocess

import (
	"context"
	pb "github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb"
	"google.golang.org/protobuf/proto"
	"testing"
	"time"
)

func TestPreConnectStreamSelectionAndSingleDelivery(t *testing.T) {
	r := &Runtime{sessions: map[*Session]bool{}, wake: make(chan struct{}, 1), b: nativeBackend{request: func(q *pb.FfiRequest) (*pb.FfiResponse, error) {
		if q.GetByteReadIncremental() == nil {
			t.Fatal(q)
		}
		return &pb.FfiResponse{}, nil
	}}}
	s := newSession(context.Background(), 1)
	defer s.cancel()
	s.participant = "caller"
	s.trackSID = "track"
	s.preConnectEnabled = true
	r.sessions[s] = true
	opened := func(sender, track string, h uint64) *pb.ByteStreamOpened {
		return &pb.ByteStreamOpened{ParticipantIdentity: proto.String(sender), Reader: &pb.OwnedByteStreamReader{Handle: &pb.FfiOwnedHandle{Id: proto.Uint64(h)}, Info: &pb.ByteStreamInfo{Topic: proto.String(preConnectTopic), Attributes: map[string]string{"trackId": track, "sampleRate": "24000", "channels": "1"}, MimeType: proto.String("audio/pcm")}}}
	}
	for _, ev := range []*pb.ByteStreamOpened{opened("other", "track", 1), opened("caller", "other", 2)} {
		if err := r.openPreConnect(s, ev); err != nil {
			t.Fatal(err)
		}
		if s.preConnectReader != nil {
			t.Fatal("unselected stream accepted")
		}
	}
	ev := opened("caller", "track", 3)
	if err := r.openPreConnect(s, ev); err != nil {
		t.Fatal(err)
	}
	if ev.Reader.Handle.GetId() != 0 {
		t.Fatal("reader ownership not transferred")
	}
	chunk := &pb.ByteStreamReaderEvent{ReaderHandle: proto.Uint64(3), Detail: &pb.ByteStreamReaderEvent_ChunkReceived{ChunkReceived: &pb.ByteStreamReaderChunkReceived{Content: []byte{1, 0}}}}
	if err := r.preConnectEvent(chunk); err != nil {
		t.Fatal(err)
	}
	if len(s.preConnectAudio) != 0 {
		t.Fatal("partial audio delivered")
	}
	end := &pb.ByteStreamReaderEvent{ReaderHandle: proto.Uint64(3), Detail: &pb.ByteStreamReaderEvent_Eos{Eos: &pb.ByteStreamReaderEOS{}}}
	if err := r.preConnectEvent(end); err != nil {
		t.Fatal(err)
	}
	got := <-s.PreConnectAudio()
	if len(got.Chunks) != 1 || got.Chunks[0][0] != 1 {
		t.Fatal(got)
	}
	if err := r.openPreConnect(s, opened("caller", "track", 4)); err != nil {
		t.Fatal(err)
	}
	if s.preConnectReader != nil {
		t.Fatal("duplicate stream accepted")
	}
	if err := r.preConnectEvent(end); err != nil {
		t.Fatal(err)
	}
	if len(s.preConnectAudio) != 0 {
		t.Fatal("duplicate delivery")
	}
}

func TestPreConnectReaderTimeoutAndClose(t *testing.T) {
	s := newSession(context.Background(), 1)
	defer s.cancel()
	s.preConnectReader = &preConnectReader{handle: 1, timer: time.NewTimer(time.Hour)}
	s.stopPreConnect()
	if s.preConnectReader != nil {
		t.Fatal("reader retained")
	}
	s.finish(nil)
	if _, ok := <-s.PreConnectAudio(); ok {
		t.Fatal("channel not closed")
	}
}

func TestPreConnectRejectsDisabledLateAndOversize(t *testing.T) {
	for _, mode := range []string{"disabled", "stopped", "size", "wrong-topic", "wrong-track", "wrong-sender", "closed"} {
		t.Run(mode, func(t *testing.T) {
			s := newSession(context.Background(), 1)
			defer s.cancel()
			s.participant = "caller"
			s.trackSID = "track"
			s.preConnectEnabled = true
			ev := &pb.ByteStreamOpened{ParticipantIdentity: proto.String("caller"), Reader: &pb.OwnedByteStreamReader{Handle: &pb.FfiOwnedHandle{Id: proto.Uint64(12)}, Info: &pb.ByteStreamInfo{Topic: proto.String(preConnectTopic), Attributes: map[string]string{"trackId": "track"}}}}
			switch mode {
			case "disabled":
				s.preConnectEnabled = false
			case "stopped":
				s.preConnectStopped.Store(true)
			case "size":
				ev.Reader.Info.TotalLength = proto.Uint64(maxPreConnectBytes + 1)
			case "wrong-topic":
				ev.Reader.Info.Topic = proto.String("other")
			case "wrong-track":
				ev.Reader.Info.Attributes["trackId"] = "other"
			case "wrong-sender":
				ev.ParticipantIdentity = proto.String("other")
			case "closed":
				s.cancel()
			}
			r := &Runtime{b: nativeBackend{request: func(*pb.FfiRequest) (*pb.FfiResponse, error) { t.Fatal("rejected stream consumed"); return nil, nil }}}
			if err := r.openPreConnect(s, ev); err != nil {
				t.Fatal(err)
			}
			if ev.Reader.Handle.GetId() != 12 || s.preConnectReader != nil {
				t.Fatal("rejected reader ownership lost")
			}
		})
	}
}
func TestPreConnectExpiresAndKeepsSessionsIsolated(t *testing.T) {
	r := &Runtime{sessions: map[*Session]bool{}}
	a := newSession(context.Background(), 1)
	defer a.cancel()
	b := newSession(context.Background(), 1)
	defer b.cancel()
	r.sessions[a] = true
	r.sessions[b] = true
	a.preConnectReader = &preConnectReader{handle: 1, deadline: time.Now().Add(-time.Second), timer: time.NewTimer(time.Hour)}
	b.preConnectReader = &preConnectReader{handle: 2, deadline: time.Now().Add(time.Hour), timer: time.NewTimer(time.Hour)}
	defer b.stopPreConnect()
	end := &pb.ByteStreamReaderEvent{ReaderHandle: proto.Uint64(1), Detail: &pb.ByteStreamReaderEvent_Eos{Eos: &pb.ByteStreamReaderEOS{}}}
	if err := r.preConnectEvent(end); err != nil {
		t.Fatal(err)
	}
	if a.preConnectReader != nil || b.preConnectReader == nil || len(a.preConnectAudio) != 0 || len(b.preConnectAudio) != 0 {
		t.Fatal("late audio delivered or crossed session")
	}
}

func TestPreConnectFailureNeverDeliversPartialAudio(t *testing.T) {
	for _, mode := range []string{"eos-error", "byte-limit", "chunk-limit"} {
		t.Run(mode, func(t *testing.T) {
			s := newSession(context.Background(), 1)
			defer s.cancel()
			reader := &preConnectReader{handle: 9, deadline: time.Now().Add(time.Hour), timer: time.NewTimer(time.Hour)}
			s.preConnectReader = reader
			defer s.stopPreConnect()
			r := &Runtime{sessions: map[*Session]bool{s: true}}
			var ev *pb.ByteStreamReaderEvent
			switch mode {
			case "eos-error":
				ev = &pb.ByteStreamReaderEvent{ReaderHandle: proto.Uint64(9), Detail: &pb.ByteStreamReaderEvent_Eos{Eos: &pb.ByteStreamReaderEOS{Error: &pb.StreamError{}}}}
			case "byte-limit":
				reader.size = maxPreConnectBytes
			case "chunk-limit":
				reader.audio.Chunks = make([][]byte, 4096)
			}
			if ev == nil {
				ev = &pb.ByteStreamReaderEvent{ReaderHandle: proto.Uint64(9), Detail: &pb.ByteStreamReaderEvent_ChunkReceived{ChunkReceived: &pb.ByteStreamReaderChunkReceived{Content: []byte{1}}}}
			}
			if err := r.preConnectEvent(ev); err != nil {
				t.Fatal(err)
			}
			if s.preConnectReader != nil || len(s.preConnectAudio) != 0 {
				t.Fatal("partial stream retained/delivered")
			}
		})
	}
}
