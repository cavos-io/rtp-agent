package nativeinprocess

import (
	"context"
	pb "github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb"
	"google.golang.org/protobuf/proto"
	"testing"
)

func TestAuxRecordingSelectionAndCleanup(t *testing.T) {
	s := newSession(context.Background(), 1)
	s.participant = "caller"
	s.recordRemote = true
	var drops []uint64
	r := &Runtime{sessions: map[*Session]bool{s: true}, auxStreams: map[uint64]*Session{}, b: nativeBackend{drop: func(h uint64) error { drops = append(drops, h); return nil }, request: func(q *pb.FfiRequest) (*pb.FfiResponse, error) {
		if q.GetNewAudioStream() == nil {
			t.Fatal(q)
		}
		return &pb.FfiResponse{Message: &pb.FfiResponse_NewAudioStream{NewAudioStream: &pb.NewAudioStreamResponse{Stream: &pb.OwnedAudioStream{Handle: &pb.FfiOwnedHandle{Id: proto.Uint64(40)}}}}}, nil
	}}}
	defer s.cancel()
	s.observeRecordingParticipant(&pb.ParticipantInfo{Identity: proto.String("other"), Kind: pb.ParticipantKind_PARTICIPANT_KIND_STANDARD.Enum()})
	s.observeRecordingPublication("other", &pb.TrackPublicationInfo{Sid: proto.String("mic"), Kind: pb.TrackKind_KIND_AUDIO.Enum(), Source: pb.TrackSource_SOURCE_MICROPHONE.Enum()})
	tr := &pb.TrackSubscribed{ParticipantIdentity: proto.String("other"), Track: &pb.OwnedTrack{Handle: &pb.FfiOwnedHandle{Id: proto.Uint64(30)}, Info: &pb.TrackInfo{Sid: proto.String("mic")}}}
	if err := r.openRecordingTrack(s, tr); err != nil {
		t.Fatal(err)
	}
	if len(s.recordingTracks) != 1 || r.auxStreams[40] != s {
		t.Fatal("aux stream not retained")
	}
	if err := r.closeRecordingTrack(s, "mic"); err != nil {
		t.Fatal(err)
	}
	if len(s.recordingTracks) != 0 || len(r.auxStreams) != 0 || len(drops) != 2 {
		t.Fatal("aux handles leaked", drops)
	}
	if s.ctx.Err() != nil {
		t.Fatal("aux departure ended caller")
	}
}

func TestAuxRecordingFilters(t *testing.T) {
	for _, tc := range []struct {
		name             string
		kind             pb.ParticipantKind
		source           pb.TrackSource
		identity, behalf string
		enabled, want    bool
	}{
		{"standard", pb.ParticipantKind_PARTICIPANT_KIND_STANDARD, pb.TrackSource_SOURCE_MICROPHONE, "other", "", true, true},
		{"sip", pb.ParticipantKind_PARTICIPANT_KIND_SIP, pb.TrackSource_SOURCE_MICROPHONE, "other", "", true, true},
		{"connector", pb.ParticipantKind_PARTICIPANT_KIND_CONNECTOR, pb.TrackSource_SOURCE_MICROPHONE, "other", "", true, true},
		{"agent", pb.ParticipantKind_PARTICIPANT_KIND_AGENT, pb.TrackSource_SOURCE_MICROPHONE, "other", "", true, false},
		{"behalf", pb.ParticipantKind_PARTICIPANT_KIND_STANDARD, pb.TrackSource_SOURCE_MICROPHONE, "other", "agent", true, false},
		{"screen", pb.ParticipantKind_PARTICIPANT_KIND_STANDARD, pb.TrackSource_SOURCE_SCREENSHARE_AUDIO, "other", "", true, false},
		{"selected", pb.ParticipantKind_PARTICIPANT_KIND_STANDARD, pb.TrackSource_SOURCE_MICROPHONE, "caller", "", true, false},
		{"disabled", pb.ParticipantKind_PARTICIPANT_KIND_STANDARD, pb.TrackSource_SOURCE_MICROPHONE, "other", "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSession(context.Background(), 1)
			defer s.cancel()
			s.participant = "caller"
			s.recordRemote = tc.enabled
			s.observeRecordingParticipant(&pb.ParticipantInfo{Identity: proto.String(tc.identity), Kind: tc.kind.Enum(), Attributes: map[string]string{"lk.publish_on_behalf": tc.behalf}})
			s.observeRecordingPublication(tc.identity, &pb.TrackPublicationInfo{Sid: proto.String("track"), Kind: pb.TrackKind_KIND_AUDIO.Enum(), Source: tc.source.Enum()})
			if got := s.eligibleRecording(tc.identity, "track"); got != tc.want {
				t.Fatal(got)
			}
			if s.eligibleRecording(tc.identity, "unknown") {
				t.Fatal("unknown publication accepted")
			}
		})
	}
}

func TestAuxRecordingPCMIsolationOverflowAndLateEvents(t *testing.T) {
	s := newSession(context.Background(), 1)
	s.recordRemote = true
	other := newSession(context.Background(), 1)
	defer other.cancel()
	s.recordingTracks["aux"] = recordingTrack{"other", 30, 40}
	r := &Runtime{sessions: map[*Session]bool{s: true, other: true}, auxStreams: map[uint64]*Session{40: s}, b: nativeBackend{drop: func(uint64) error { return nil }, pcm: func(*pb.AudioFrameBufferInfo) ([]int16, error) { return []int16{11, 22}, nil }}}
	frame := &pb.AudioStreamEvent{StreamHandle: proto.Uint64(40), Message: &pb.AudioStreamEvent_FrameReceived{FrameReceived: &pb.AudioFrameReceived{Frame: &pb.OwnedAudioFrameBuffer{}}}}
	if err := r.recordingAudio(s, frame, 123); err != nil {
		t.Fatal(err)
	}
	got := <-s.RecordingFrames()
	if got.TrackSID != "aux" || got.Frame.At != 123 || got.Frame.PCM[0] != 11 {
		t.Fatal(got)
	}
	if len(s.Frames()) != 0 || len(other.RecordingFrames()) != 0 {
		t.Fatal("aux mixed into agent input or another room")
	}
	for range cap(s.recordings) {
		if err := r.recordingAudio(s, frame, 123); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.recordingAudio(s, frame, 123); err != nil {
		t.Fatal(err)
	}
	if s.Err() == nil || other.Err() != nil || !r.sessions[other] {
		t.Fatal("overflow not isolated")
	}
	if len(r.auxStreams) != 0 || len(s.recordingTracks) != 0 {
		t.Fatal("shutdown leaked aux handles")
	}
	if err := r.recordingAudio(s, frame, 123); err != nil {
		t.Fatal("late callback", err)
	}
}

func TestAuxRecordingDepartureDoesNotRebindPrimary(t *testing.T) {
	for _, event := range []*pb.RoomEvent{
		{Message: &pb.RoomEvent_TrackUnsubscribed{TrackUnsubscribed: &pb.TrackUnsubscribed{TrackSid: proto.String("aux")}}},
		{Message: &pb.RoomEvent_TrackUnpublished{TrackUnpublished: &pb.TrackUnpublished{PublicationSid: proto.String("aux")}}},
		{Message: &pb.RoomEvent_ParticipantDisconnected{ParticipantDisconnected: &pb.ParticipantDisconnected{ParticipantIdentity: proto.String("other")}}},
	} {
		s := newSession(context.Background(), 1)
		s.recordRemote = true
		s.participant = "caller"
		s.trackSID = "primary"
		s.stream = 50
		s.recordingTracks["aux"] = recordingTrack{"other", 30, 40}
		r := &Runtime{auxStreams: map[uint64]*Session{40: s}, b: nativeBackend{drop: func(uint64) error { return nil }}}
		if err := r.observeRecordingLifecycle(s, event); err != nil {
			t.Fatal(err)
		}
		if len(s.recordingTracks) != 0 || len(r.auxStreams) != 0 || s.stream != 50 || s.awaitingTrack || s.ctx.Err() != nil {
			t.Fatal("aux departure damaged primary")
		}
		s.cancel()
	}
}

func TestAuxRecordingEOSRepublishAndCanceledDelivery(t *testing.T) {
	s := newSession(context.Background(), 1)
	s.recordRemote = true
	s.participant = "caller"
	drops := map[uint64]int{}
	next := uint64(40)
	r := &Runtime{sessions: map[*Session]bool{s: true}, auxStreams: map[uint64]*Session{}, b: nativeBackend{drop: func(h uint64) error { drops[h]++; return nil }, request: func(*pb.FfiRequest) (*pb.FfiResponse, error) {
		next++
		return &pb.FfiResponse{Message: &pb.FfiResponse_NewAudioStream{NewAudioStream: &pb.NewAudioStreamResponse{Stream: &pb.OwnedAudioStream{Handle: &pb.FfiOwnedHandle{Id: proto.Uint64(next)}}}}}, nil
	}}}
	s.observeRecordingParticipant(&pb.ParticipantInfo{Identity: proto.String("other"), Kind: pb.ParticipantKind_PARTICIPANT_KIND_STANDARD.Enum()})
	open := func(sid string, h uint64) {
		s.observeRecordingPublication("other", &pb.TrackPublicationInfo{Sid: proto.String(sid), Kind: pb.TrackKind_KIND_AUDIO.Enum(), Source: pb.TrackSource_SOURCE_MICROPHONE.Enum()})
		if err := r.openRecordingTrack(s, &pb.TrackSubscribed{ParticipantIdentity: proto.String("other"), Track: &pb.OwnedTrack{Handle: &pb.FfiOwnedHandle{Id: proto.Uint64(h)}, Info: &pb.TrackInfo{Sid: proto.String(sid)}}}); err != nil {
			t.Fatal(err)
		}
	}
	open("old", 30)
	eos := &pb.AudioStreamEvent{StreamHandle: proto.Uint64(41), Message: &pb.AudioStreamEvent_Eos{Eos: &pb.AudioStreamEOS{}}}
	if err := r.recordingAudio(s, eos, 0); err != nil {
		t.Fatal(err)
	}
	open("new", 31)
	if err := r.recordingAudio(s, eos, 0); err != nil {
		t.Fatal(err)
	}
	if len(s.recordingTracks) != 1 || len(r.auxStreams) != 1 {
		t.Fatal("late EOS damaged replacement")
	}
	s.cancel()
	if err := r.recordingAudio(s, &pb.AudioStreamEvent{StreamHandle: proto.Uint64(42)}, 0); err != nil {
		t.Fatal(err)
	}
	r.finish(s, context.Canceled)
	if len(r.auxStreams) != 0 || len(s.recordingTracks) != 0 {
		t.Fatal("shutdown leaked tracks")
	}
	for _, h := range []uint64{30, 31, 41, 42} {
		if drops[h] != 1 {
			t.Fatal("handle not released once", drops)
		}
	}
	if _, ok := <-s.RecordingFrames(); ok {
		t.Fatal("recording channel not closed")
	}
}
