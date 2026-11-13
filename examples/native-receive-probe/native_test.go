package main

import (
	"google.golang.org/protobuf/proto"
	pb "native-receive-probe/internal/ffipb"
	"testing"
)

func TestOwnedHandlesNested(t *testing.T) {
	event := &pb.FfiEvent{Message: &pb.FfiEvent_AudioStreamEvent{AudioStreamEvent: &pb.AudioStreamEvent{
		StreamHandle: proto.Uint64(9), Message: &pb.AudioStreamEvent_FrameReceived{FrameReceived: &pb.AudioFrameReceived{
			Frame: &pb.OwnedAudioFrameBuffer{Handle: &pb.FfiOwnedHandle{Id: proto.Uint64(42)}}}}}}}
	ids := ownedHandles(event.ProtoReflect())
	if len(ids) != 1 || ids[0] != 42 {
		t.Fatalf("owned handles %v; stream_handle is borrowed", ids)
	}
}
func TestCallbackQueueCopiesAndFailsOnOverflow(t *testing.T) {
	q := newEventQueue(1)
	src := []byte{1, 2, 3}
	q.push(src, 123)
	src[0] = 9
	got := <-q.events
	if got.data[0] != 1 || got.at != 123 {
		t.Fatal("callback memory not copied")
	}
	q.push(src, 124)
	q.push(src, 125)
	if !q.overflow.Load() {
		t.Fatal("overflow not reported")
	}
}
