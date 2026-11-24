package nativeinprocess

import (
	"context"
	"errors"
	"fmt"
	pb "github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb"
	"google.golang.org/protobuf/proto"
	"sort"
)

// ConnectMicrophoneCall enables bounded participant-departure delivery as well as recording.
// The caller must drain ParticipantDepartures until the session closes.
func (r *Runtime) ConnectMicrophoneCall(ctx context.Context, url, token, participant, sid string, chat, record bool) (*Session, error) {
	return r.connectSelected(ctx, url, token, participant, sid, true, chat, record, true)
}

func (s *Session) ParticipantIdentities() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.participants))
	for id := range s.participants {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
func (s *Session) observeParticipant(id string, present bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		return
	}
	if present {
		s.participants[id] = true
	} else {
		delete(s.participants, id)
	}
}
func (s *Session) ParticipantDepartures() <-chan string { return s.departures }
func (s *Session) departParticipant(id string) error {
	s.observeParticipant(id, false)
	if !s.departuresEnabled {
		return nil
	}
	select {
	case s.departures <- id:
		return nil
	default:
		return ErrBackpressure
	}
}

// Hold has its own native audio source and publication on the same connection.
// It never enters speech playback accounting or the receive playout path.
func (s *Session) BeginHold(ctx context.Context) error { return s.controlRequest(ctx, "hold-start") }
func (s *Session) EndHold(ctx context.Context) error   { return s.controlRequest(ctx, "hold-clear") }
func (s *Session) SetOutputEnabled(ctx context.Context, enabled bool) error {
	if enabled {
		return s.controlRequest(ctx, "output-unmute")
	}
	return s.controlRequest(ctx, "output-mute")
}
func (s *Session) CaptureHold(ctx context.Context, pcm []int16) error {
	if len(pcm) == 0 || len(pcm) > 1200 {
		return errors.New("invalid hold PCM: expected 24 kHz mono, at most 50 ms")
	}
	c := command{s: s, ctx: ctx, kind: "hold-capture", pcm: append([]int16(nil), pcm...), reply: make(chan error, 1)}
	if err := s.rt.submit(ctx, c); err != nil {
		return err
	}
	select {
	case err := <-c.reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return errors.Join(ErrClosed, s.Err())
	}
}
func isCallControl(kind string) bool {
	switch kind {
	case "hold-start", "hold-clear", "hold-capture", "output-mute", "output-unmute":
		return true
	}
	return false
}
func (r *Runtime) handleCallControl(c command) error {
	s := c.s
	err := c.ctx.Err()
	if err == nil && (!r.sessions[s] || s.closing || s.ctx.Err() != nil) {
		err = ErrClosed
	}
	if err == nil && (!s.published || s.reconnecting) {
		err = errors.New("native call control unavailable during startup/reconnect")
	}
	if err != nil {
		c.reply <- err
		return nil
	}
	switch c.kind {
	case "output-mute", "output-unmute":
		_, err = r.b.request(&pb.FfiRequest{Message: &pb.FfiRequest_LocalTrackMute{LocalTrackMute: &pb.LocalTrackMuteRequest{TrackHandle: proto.Uint64(s.output), Mute: proto.Bool(c.kind == "output-mute")}}})
	case "hold-start":
		if s.holdPublished {
			c.reply <- nil
			return nil
		}
		if s.holdStarting {
			c.reply <- ErrControlBusy
			return nil
		}
		if s.holdSource != 0 || s.holdTrack != 0 {
			c.reply <- errors.New("native hold initialization previously failed")
			return nil
		}
		res, e := r.b.request(&pb.FfiRequest{Message: &pb.FfiRequest_NewAudioSource{NewAudioSource: &pb.NewAudioSourceRequest{Type: pb.AudioSourceType_AUDIO_SOURCE_NATIVE.Enum(), SampleRate: proto.Uint32(24000), NumChannels: proto.Uint32(1), QueueSizeMs: proto.Uint32(200)}}})
		if e != nil {
			c.reply <- e
			return nil
		}
		s.holdSource = res.GetNewAudioSource().GetSource().GetHandle().GetId()
		if s.holdSource == 0 {
			c.reply <- errors.New("missing hold source handle")
			return nil
		}
		res, e = r.b.request(&pb.FfiRequest{Message: &pb.FfiRequest_CreateAudioTrack{CreateAudioTrack: &pb.CreateAudioTrackRequest{Name: proto.String("hold-audio"), SourceHandle: proto.Uint64(s.holdSource)}}})
		if e != nil {
			c.reply <- e
			return nil
		}
		s.holdTrack = res.GetCreateAudioTrack().GetTrack().GetHandle().GetId()
		if s.holdTrack == 0 {
			c.reply <- errors.New("missing hold track handle")
			return nil
		}
		id := r.id(s, "hold-publish", c.reply)
		_, err = r.b.request(&pb.FfiRequest{Message: &pb.FfiRequest_PublishTrack{PublishTrack: &pb.PublishTrackRequest{LocalParticipantHandle: proto.Uint64(s.local), TrackHandle: proto.Uint64(s.holdTrack), Options: &pb.TrackPublishOptions{Source: pb.TrackSource_SOURCE_UNKNOWN.Enum(), Dtx: proto.Bool(false)}, RequestAsyncId: proto.Uint64(id)}}})
		if err == nil {
			s.holdStarting = true
			return nil
		}
		delete(r.pending, id)
	case "hold-capture":
		if !s.holdPublished {
			c.reply <- errors.New("hold source not ready")
			return nil
		}
		if s.holdBusy || s.holdClear != nil {
			c.reply <- ErrCaptureBusy
			return nil
		}
		id := r.id(s, "hold-capture", c.reply)
		err = r.b.capture(s.holdSource, id, c.pcm)
		if err == nil {
			s.holdBusy = true
			return nil
		}
		delete(r.pending, id)
	case "hold-clear":
		if s.holdClear != nil {
			c.reply <- ErrControlBusy
			return nil
		}
		if s.holdBusy {
			s.holdClear = &c
			return nil
		}
		if s.holdSource != 0 {
			_, err = r.b.request(&pb.FfiRequest{Message: &pb.FfiRequest_ClearAudioBuffer{ClearAudioBuffer: &pb.ClearAudioBufferRequest{SourceHandle: proto.Uint64(s.holdSource)}}})
		}
	}
	c.reply <- err
	return nil
}
func (r *Runtime) holdCallback(ev *pb.FfiEvent) (bool, error) {
	var id uint64
	var failure string
	if c := ev.GetPublishTrack(); c != nil {
		id, failure = c.GetAsyncId(), c.GetError()
	} else if c := ev.GetCaptureAudioFrame(); c != nil {
		id, failure = c.GetAsyncId(), c.GetError()
	} else {
		return false, nil
	}
	p, ok := r.pending[id]
	if !ok && id > 0 && id <= r.next {
		return true, nil
	}
	if !ok || (p.kind != "hold-publish" && p.kind != "hold-capture") {
		return false, nil
	}
	delete(r.pending, id)
	var err error
	if failure != "" {
		err = fmt.Errorf("native %s: %s", p.kind, failure)
	}
	if p.kind == "hold-publish" {
		p.s.holdStarting = false
		p.s.holdPublished = err == nil
	} else {
		p.s.holdBusy = false
	}
	p.reply <- err
	if p.kind == "hold-capture" && p.s.holdClear != nil {
		c := *p.s.holdClear
		p.s.holdClear = nil
		return true, r.handleCallControl(c)
	}
	return true, nil
}
