package main

import (
	"errors"
	"google.golang.org/protobuf/proto"
	pb "github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb"
	"testing"
)

func TestNativeRestartDoesNotEndSelectedSource(t *testing.T) {
	l := receiveLifecycle{}
	reconnect := &pb.RoomEvent{Message: &pb.RoomEvent_Reconnecting{Reconnecting: &pb.Reconnecting{}}}
	unsub := &pb.RoomEvent{Message: &pb.RoomEvent_TrackUnsubscribed{TrackUnsubscribed: &pb.TrackUnsubscribed{TrackSid: proto.String("selected")}}}
	depart := &pb.RoomEvent{Message: &pb.RoomEvent_ParticipantDisconnected{ParticipantDisconnected: &pb.ParticipantDisconnected{ParticipantIdentity: proto.String("caller")}}}
	connected := &pb.RoomEvent{Message: &pb.RoomEvent_Reconnected{Reconnected: &pb.Reconnected{}}}
	// Recorded native ordering: reconnecting, unsubscribe, participant departure,
	// reconnected, then new subscription. Source termination is suppressed only
	// during native reconnect; normal departures must still end the session.
	for i, ev := range []*pb.RoomEvent{reconnect, unsub, depart, connected} {
		reset, err := l.observe(ev, "caller", "selected")
		if err != nil || reset != (i == 1) {
			t.Fatalf("event%d reset=%v err=%v", i, reset, err)
		}
	}
	if _, err := l.observe(unsub, "caller", "selected"); !errors.Is(err, errTrackUnsubscribed) {
		t.Fatalf("normal unsubscribe lost: %v", err)
	}
	if _, err := l.observe(depart, "caller", "selected"); !errors.Is(err, errPublisherDisconnected) {
		t.Fatalf("normal departure lost: %v", err)
	}
}

func TestNativeLifecycleIgnoresUnrelatedSource(t *testing.T) {
	for _, reconnecting := range []bool{false, true} {
		l := receiveLifecycle{reconnecting: reconnecting}
		for _, ev := range []*pb.RoomEvent{
			{Message: &pb.RoomEvent_TrackUnsubscribed{TrackUnsubscribed: &pb.TrackUnsubscribed{TrackSid: proto.String("other")}}},
			{Message: &pb.RoomEvent_ParticipantDisconnected{ParticipantDisconnected: &pb.ParticipantDisconnected{ParticipantIdentity: proto.String("other")}}},
		} {
			if reset, err := l.observe(ev, "caller", "selected"); reset || err != nil {
				t.Fatalf("unrelated source changed lifecycle: %v %v", reset, err)
			}
		}
	}
}
