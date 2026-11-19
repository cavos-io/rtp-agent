package nativeinprocess

import (
	"context"
	"errors"
	pb "github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb"
	"google.golang.org/protobuf/proto"
)

// RecordingFrame is auxiliary microphone PCM, never agent input.
type RecordingFrame struct {
	TrackSID string
	Frame    Frame
}
type recordingTrack struct {
	identity      string
	track, stream uint64
}
type recordingPublication struct {
	identity   string
	microphone bool
}

func (s *Session) RecordingFrames() <-chan RecordingFrame { return s.recordings }

// ConnectMicrophoneRecording enables recording before native room events begin.
func (r *Runtime) ConnectMicrophoneRecording(ctx context.Context, url, token, participant, sid string, chat, record bool) (*Session, error) {
	return r.connectSelected(ctx, url, token, participant, sid, true, chat, record, false)
}
func (s *Session) observeRecordingParticipant(p *pb.ParticipantInfo) {
	if s.recordRemote && p != nil {
		s.recordingParticipants[p.GetIdentity()] = proto.Clone(p).(*pb.ParticipantInfo)
	}
}
func (s *Session) observeRecordingPublication(identity string, p *pb.TrackPublicationInfo) {
	if s.recordRemote && p != nil {
		s.recordingPublications[p.GetSid()] = recordingPublication{identity, p.GetKind() == pb.TrackKind_KIND_AUDIO && p.GetSource() == pb.TrackSource_SOURCE_MICROPHONE}
	}
}
func (s *Session) eligibleRecording(identity, sid string) bool {
	if !s.recordRemote || identity == s.participant {
		return false
	}
	p := s.recordingParticipants[identity]
	pub := s.recordingPublications[sid]
	if p == nil || !pub.microphone || pub.identity != identity || p.GetAttributes()["lk.publish_on_behalf"] != "" {
		return false
	}
	switch p.GetKind() {
	case pb.ParticipantKind_PARTICIPANT_KIND_STANDARD, pb.ParticipantKind_PARTICIPANT_KIND_SIP, pb.ParticipantKind_PARTICIPANT_KIND_CONNECTOR:
		return true
	}
	return false
}
func (r *Runtime) openRecordingTrack(s *Session, tr *pb.TrackSubscribed) error {
	sid, identity := tr.GetTrack().GetInfo().GetSid(), tr.GetParticipantIdentity()
	if !s.eligibleRecording(identity, sid) || s.closing || s.ctx.Err() != nil {
		return nil
	}
	if _, ok := s.recordingTracks[sid]; ok {
		return nil
	}
	if len(s.recordingTracks) >= 32 {
		return r.failSession(s, errors.New("native auxiliary recording track limit exceeded"))
	}
	track := tr.GetTrack().GetHandle().GetId()
	res, err := r.b.request(&pb.FfiRequest{Message: &pb.FfiRequest_NewAudioStream{NewAudioStream: &pb.NewAudioStreamRequest{TrackHandle: proto.Uint64(track), Type: pb.AudioStreamType_AUDIO_STREAM_NATIVE.Enum(), SampleRate: proto.Uint32(24000), NumChannels: proto.Uint32(1), FrameSizeMs: proto.Uint32(20), QueueSizeFrames: proto.Uint32(0)}}})
	if err != nil {
		return r.failSession(s, err)
	}
	stream := res.GetNewAudioStream().GetStream().GetHandle().GetId()
	if stream == 0 {
		return r.failSession(s, errors.New("missing native auxiliary recording stream"))
	}
	s.recordingTracks[sid] = recordingTrack{identity, track, stream}
	r.auxStreams[stream] = s
	return nil
}
func (r *Runtime) closeRecordingTrack(s *Session, sid string) error {
	tr, ok := s.recordingTracks[sid]
	if !ok {
		return nil
	}
	delete(s.recordingTracks, sid)
	delete(r.auxStreams, tr.stream)
	return errors.Join(r.b.drop(tr.stream), r.b.drop(tr.track))
}
func (r *Runtime) closeRecordingTracks(s *Session) error {
	var err error
	for sid := range s.recordingTracks {
		err = errors.Join(err, r.closeRecordingTrack(s, sid))
	}
	return err
}
func (r *Runtime) observeRecordingLifecycle(s *Session, event *pb.RoomEvent) error {
	if !s.recordRemote {
		return nil
	}
	if p := event.GetParticipantConnected(); p != nil {
		s.observeRecordingParticipant(p.GetInfo().GetInfo())
	}
	if change := event.GetParticipantAttributesChanged(); change != nil {
		if p := s.recordingParticipants[change.GetParticipantIdentity()]; p != nil {
			p.Attributes = make(map[string]string)
			for _, a := range change.GetAttributes() {
				p.Attributes[a.GetKey()] = a.GetValue()
			}
		}
	}
	if p := event.GetTrackPublished(); p != nil {
		s.observeRecordingPublication(p.GetParticipantIdentity(), p.GetPublication().GetInfo())
	}
	if p := event.GetTrackUnsubscribed(); p != nil {
		if err := r.closeRecordingTrack(s, p.GetTrackSid()); err != nil {
			return err
		}
	}
	if p := event.GetTrackUnpublished(); p != nil {
		delete(s.recordingPublications, p.GetPublicationSid())
		if err := r.closeRecordingTrack(s, p.GetPublicationSid()); err != nil {
			return err
		}
	}
	if p := event.GetParticipantDisconnected(); p != nil {
		identity := p.GetParticipantIdentity()
		delete(s.recordingParticipants, identity)
		for sid, pub := range s.recordingPublications {
			if pub.identity == identity {
				delete(s.recordingPublications, sid)
			}
		}
		for sid, tr := range s.recordingTracks {
			if tr.identity == identity {
				if err := r.closeRecordingTrack(s, sid); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func (r *Runtime) recordingAudio(s *Session, event *pb.AudioStreamEvent, at int64) error {
	if s.closing || s.ctx.Err() != nil {
		return nil
	}
	for sid, tr := range s.recordingTracks {
		if tr.stream != event.GetStreamHandle() {
			continue
		}
		if event.GetEos() != nil {
			return r.closeRecordingTrack(s, sid)
		}
		if frame := event.GetFrameReceived(); frame != nil {
			pcm, err := r.b.pcm(frame.GetFrame().GetInfo())
			if err != nil {
				return r.failSession(s, err)
			}
			select {
			case s.recordings <- RecordingFrame{sid, Frame{at, pcm}}:
			default:
				return r.failSession(s, errors.New("native auxiliary recording queue full"))
			}
		}
		return nil
	}
	return nil
}
