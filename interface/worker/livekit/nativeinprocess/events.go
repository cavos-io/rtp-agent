package nativeinprocess

import (
	"context"
	"errors"
	"fmt"
	pb "github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb"
	"google.golang.org/protobuf/proto"
)

func (r *Runtime) dispatch(raw callbackEvent) (err error) {
	ev := new(pb.FfiEvent)
	if err = proto.Unmarshal(raw.data, ev); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, r.release(ev)) }()
	if ev.GetTextStreamReaderEvent() != nil {
		return r.chatEvent(ev.GetTextStreamReaderEvent())
	}
	if ev.GetPanic() != nil {
		return errors.New("native panic callback")
	}
	if handled, e := r.dtmfCallback(ev); handled {
		return e
	}
	if handled, e := r.dataCallback(ev); handled {
		return e
	}
	if handled, e := r.clientStateCallback(ev); handled {
		return e
	}
	if handled, e := r.captionCallback(ev); handled {
		return e
	}
	if handled, e := r.holdCallback(ev); handled {
		return e
	}
	if c := ev.GetConnect(); c != nil {
		pending, e := r.resolve(c.GetAsyncId(), "connect")
		if e != nil {
			return e
		}
		s := pending.s
		s.connecting = false
		if c.GetError() != "" {
			return r.failSession(s, fmt.Errorf("native connect: %s", c.GetError()))
		}
		s.room = c.GetResult().GetRoom().GetHandle().GetId()
		s.local = c.GetResult().GetLocalParticipant().GetHandle().GetId()
		if s.room == 0 || s.local == 0 {
			return errors.New("missing native room/local handle")
		}
		r.rooms[s.room] = s
		// Cancellation before completion still owns the returned room. Close it before
		// dropping handles; never abandon a successful late connection.
		if s.ctx.Err() != nil || r.ctx.Err() != nil {
			return r.failSession(s, context.Canceled)
		}
		for _, p := range c.GetResult().GetParticipants() {
			s.observeParticipant(p.GetParticipant().GetInfo().GetIdentity(), true)
			s.observeRecordingParticipant(p.GetParticipant().GetInfo())
			for _, pub := range p.GetPublications() {
				s.observePublication(p.GetParticipant().GetInfo().GetIdentity(), pub.GetInfo())
				s.observeRecordingPublication(p.GetParticipant().GetInfo().GetIdentity(), pub.GetInfo())
			}
		}
		res, e := r.b.request(&pb.FfiRequest{Message: &pb.FfiRequest_NewAudioSource{NewAudioSource: &pb.NewAudioSourceRequest{Type: pb.AudioSourceType_AUDIO_SOURCE_NATIVE.Enum(), SampleRate: proto.Uint32(24000), NumChannels: proto.Uint32(1), QueueSizeMs: proto.Uint32(200)}}})
		if e != nil {
			return r.failSession(s, e)
		}
		s.source = res.GetNewAudioSource().GetSource().GetHandle().GetId()
		res, e = r.b.request(&pb.FfiRequest{Message: &pb.FfiRequest_CreateAudioTrack{CreateAudioTrack: &pb.CreateAudioTrackRequest{Name: proto.String("inprocess-output"), SourceHandle: proto.Uint64(s.source)}}})
		if e != nil {
			return r.failSession(s, e)
		}
		s.output = res.GetCreateAudioTrack().GetTrack().GetHandle().GetId()
		if s.source == 0 || s.output == 0 {
			return r.failSession(s, errors.New("missing native output handle"))
		}
		id := r.id(s, "publish", nil)
		_, e = r.b.request(&pb.FfiRequest{Message: &pb.FfiRequest_PublishTrack{PublishTrack: &pb.PublishTrackRequest{LocalParticipantHandle: proto.Uint64(s.local), TrackHandle: proto.Uint64(s.output), Options: &pb.TrackPublishOptions{Source: pb.TrackSource_SOURCE_MICROPHONE.Enum(), Dtx: proto.Bool(false)}, RequestAsyncId: proto.Uint64(id)}}})
		if e != nil {
			delete(r.pending, id)
			return r.failSession(s, e)
		}
		_, e = r.b.request(&pb.FfiRequest{Message: &pb.FfiRequest_ReadyForRoomEvent{ReadyForRoomEvent: &pb.ReadyForRoomEventRequest{RoomHandle: proto.Uint64(s.room)}}})
		if e != nil {
			return r.failSession(s, e)
		}
	}
	if c := ev.GetPublishTrack(); c != nil {
		p, e := r.resolve(c.GetAsyncId(), "publish")
		if e != nil {
			return e
		}
		if c.GetError() != "" {
			return r.failSession(p.s, errors.New(c.GetError()))
		}
		p.s.outputSID = c.GetPublication().GetInfo().GetSid()
		p.s.published = true
		p.s.markOutputSubscribed()
		close(p.s.ready)
	}
	if c := ev.GetCaptureAudioFrame(); c != nil {
		p, e := r.resolve(c.GetAsyncId(), "capture")
		if e != nil {
			return e
		}
		p.s.captureBusy = false
		if c.GetError() != "" {
			e = errors.New(c.GetError())
		}
		p.reply <- e
		if e != nil && r.sessions[p.s] {
			return r.failSession(p.s, e)
		}
		if r.sessions[p.s] {
			if e = r.finishControl(p.s); e != nil {
				return e
			}
		}
	}
	if c := ev.GetDisconnect(); c != nil {
		p, e := r.resolve(c.GetAsyncId(), "disconnect")
		if e != nil {
			return e
		}
		s := p.s
		if e = r.closeRecordingTracks(s); e != nil {
			return e
		}
		for _, h := range []uint64{s.stream, s.track, s.holdTrack, s.holdSource, s.output, s.source, s.local, s.room} {
			if e = r.b.drop(h); e != nil {
				return e
			}
		}
		delete(r.rooms, s.room)
		delete(r.streams, s.stream)
		s.room = 0
		s.local = 0
		s.track = 0
		s.stream = 0
		s.output = 0
		s.source = 0
		r.finish(s, s.Err())
	}
	if event := ev.GetRoomEvent(); event != nil {
		s := r.rooms[event.GetRoomHandle()]
		if s == nil || s.closing {
			return nil
		}
		if p := event.GetParticipantConnected(); p != nil {
			s.observeParticipant(p.GetInfo().GetInfo().GetIdentity(), true)
		}
		if p := event.GetParticipantDisconnected(); p != nil && !s.reconnecting {
			if e := s.departParticipant(p.GetParticipantIdentity()); e != nil {
				return r.failSession(s, e)
			}
		}
		if err := r.observeRecordingLifecycle(s, event); err != nil {
			return err
		}
		if chat := event.GetTextStreamOpened(); chat != nil {
			return r.openChat(s, chat)
		}
		if p := event.GetTrackPublished(); p != nil {
			s.observePublication(p.GetParticipantIdentity(), p.GetPublication().GetInfo())
		}
		if sub := event.GetLocalTrackSubscribed(); sub != nil {
			s.outputSubscriptionSID = sub.GetTrackSid()
			s.markOutputSubscribed()
		}
		if e := r.observeLifecycle(s, event); e != nil {
			return e
		}
		if s.closing {
			return nil
		}
		if packet := event.GetDataPacketReceived(); packet != nil && packet.GetSipDtmf() != nil {
			d := packet.GetSipDtmf()
			if e := s.deliverDTMF(DTMF{Code: d.GetCode(), Digit: d.GetDigit(), Sender: packet.GetParticipantIdentity()}); e != nil {
				return r.failSession(s, e)
			}
		}
		if tr := event.GetTrackSubscribed(); tr != nil {
			sid, identity := tr.GetTrack().GetInfo().GetSid(), tr.GetParticipantIdentity()
			if s.recordRemote && identity != s.participant {
				return r.openRecordingTrack(s, tr)
			}
			if s.microphone {
				if identity != s.participant || (!s.microphones[sid] && sid != s.trackSID) {
					return nil
				}
				if s.stream != 0 {
					if sid == s.trackSID {
						return nil
					}
					if e := r.retireInput(s); e != nil {
						return e
					}
					delete(s.microphones, s.trackSID)
				}
			} else if s.explicitSelection && (identity != s.participant || (sid != s.trackSID && !s.awaitingTrack)) {
				return nil
			}
			if !s.microphone && s.trackSID != "" && (sid != s.trackSID || identity != s.participant) {
				return r.failSession(s, errors.New("native selected track replaced"))
			}
			s.trackSID = sid
			s.inputSID.Store(sid)
			s.participant = identity
			if s.stream != 0 {
				return r.failSession(s, errors.New("multiple input tracks not supported by prototype"))
			}
			s.track = tr.GetTrack().GetHandle().GetId()
			res, e := r.b.request(&pb.FfiRequest{Message: &pb.FfiRequest_NewAudioStream{NewAudioStream: &pb.NewAudioStreamRequest{TrackHandle: proto.Uint64(s.track), Type: pb.AudioStreamType_AUDIO_STREAM_NATIVE.Enum(), SampleRate: proto.Uint32(24000), NumChannels: proto.Uint32(1), FrameSizeMs: proto.Uint32(20), QueueSizeFrames: proto.Uint32(0)}}})
			if e != nil {
				return r.failSession(s, e)
			}
			s.stream = res.GetNewAudioStream().GetStream().GetHandle().GetId()
			if s.stream == 0 {
				return r.failSession(s, errors.New("missing input stream"))
			}
			r.streams[s.stream] = s
			if !s.subscribedOnce {
				s.subscribedOnce = true
				close(s.subscribed)
			}
			s.endRebind()
		}
		if failure := event.GetTrackSubscriptionFailed(); failure != nil {
			if s.microphone && (failure.GetParticipantIdentity() != s.participant || !s.microphones[failure.GetTrackSid()]) {
				return nil
			}
			return r.failSession(s, errors.New(failure.GetError()))
		}
		if event.GetDisconnected() != nil {
			return r.failSession(s, errors.New("native room disconnected"))
		}
	}
	if audio := ev.GetAudioStreamEvent(); audio != nil {
		if s := r.auxStreams[audio.GetStreamHandle()]; s != nil {
			return r.recordingAudio(s, audio, raw.at)
		}
		s := r.streams[audio.GetStreamHandle()]
		if s == nil || s.closing {
			return nil
		}
		if audio.GetEos() != nil {
			if s.microphone || s.reconnecting || s.awaitingTrack {
				return r.retireInput(s)
			}
			return r.failSession(s, fmt.Errorf("%w: stream ended", ErrSourceEnded))
		}
		if audio.GetFrameReceived() != nil {
			pcm, e := r.b.pcm(audio.GetFrameReceived().GetFrame().GetInfo())
			if e != nil {
				return r.failSession(s, e)
			}
			if e = s.deliver(Frame{raw.at, pcm}); e != nil {
				return r.failSession(s, e)
			}
		}
	}
	return nil
}
