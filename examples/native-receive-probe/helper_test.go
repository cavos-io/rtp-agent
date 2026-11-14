package main

import (
	"google.golang.org/protobuf/proto"
	pb "native-receive-probe/internal/ffipb"
	"testing"
)

func TestHelperSelectsOnlyRequestedAudioTrack(t *testing.T) {
	sink := &helperSink{participant: "caller", trackSID: "TR_input"}
	track := &pb.TrackSubscribed{ParticipantIdentity: proto.String("caller"), Track: &pb.OwnedTrack{Info: &pb.TrackInfo{Sid: proto.String("TR_input"), Kind: pb.TrackKind_KIND_AUDIO.Enum()}}}
	if !sink.acceptTrack(track) {
		t.Fatal("selected audio rejected")
	}
	track.ParticipantIdentity = proto.String("agent")
	if sink.acceptTrack(track) {
		t.Fatal("agent output accepted")
	}
	track.ParticipantIdentity = proto.String("caller")
	track.Track.Info.Sid = proto.String("TR_other")
	if sink.acceptTrack(track) {
		t.Fatal("other track accepted")
	}
	track.Track.Info.Sid = proto.String("TR_input")
	track.Track.Info.Kind = pb.TrackKind_KIND_VIDEO.Enum()
	if sink.acceptTrack(track) {
		t.Fatal("video accepted")
	}
}
