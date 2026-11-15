package main

import (
	"bytes"
	"encoding/json"
	"errors"
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

func TestHelperDepartureClassification(t *testing.T) {
	for _, tc := range []struct {
		err    error
		reason string
	}{
		{errPublisherDisconnected, "participant_disconnected"},
		{errTrackUnsubscribed, "track_unsubscribed"},
		{errors.New("native panic"), ""},
		{errors.Join(errTrackUnsubscribed, errors.New("disconnect callback timed out")), ""},
		{errors.New("unexpected room disconnect"), ""},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			var output bytes.Buffer
			sink := &helperSink{encoder: json.NewEncoder(&output)}
			err := sink.finish(tc.err)
			if tc.reason == "" {
				if err == nil || output.Len() != 0 {
					t.Fatal("failure suppressed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var event struct{ Event, Reason string }
			if err := json.Unmarshal(output.Bytes(), &event); err != nil {
				t.Fatal(err)
			}
			if event.Event != "ended" || event.Reason != tc.reason {
				t.Fatalf("wrong event: %+v", event)
			}
		})
	}
	var output bytes.Buffer
	sink := &helperSink{encoder: json.NewEncoder(&output), err: errors.New("broken pipe")}
	if sink.finish(errTrackUnsubscribed) == nil || output.Len() != 0 {
		t.Fatal("pipe failure suppressed")
	}
}
