package nativeinprocess

import (
	"fmt"
	pb "native-receive-probe/ffipb"
	"time"
)

// A lifecycle deadline, not an RTP/PCM admission or playout policy. Native owns
// reconnect attempts; we only retain session ownership while its track rebinds.
func (r *Runtime) beginRebind(s *Session) {
	if s.rebindDeadline.IsZero() {
		s.rebindDeadline = time.Now().Add(30 * time.Second)
		s.rebindTimer = time.AfterFunc(30*time.Second, r.notify)
	}
}
func (s *Session) endRebind() {
	if s.rebindTimer != nil {
		s.rebindTimer.Stop()
		s.rebindTimer = nil
	}
	s.rebindDeadline = time.Time{}
	s.awaitingTrack = false
}
func (r *Runtime) retireInput(s *Session) error {
	r.beginRebind(s)
	s.awaitingTrack = true
	delete(r.streams, s.stream)
	for _, h := range []uint64{s.stream, s.track} {
		if e := r.b.drop(h); e != nil {
			return e
		}
	}
	s.stream = 0
	s.track = 0
	return nil
}
func (r *Runtime) observeLifecycle(s *Session, event *pb.RoomEvent) error {
	if event.GetReconnecting() != nil {
		s.reconnecting = true
		s.stateDirty = s.stateDesired != nil
		s.stateActive = nil
		r.beginRebind(s)
	}
	if event.GetReconnected() != nil {
		s.reconnecting = false
		s.stateDirty = s.stateDesired != nil
		r.pumpClientState(s)
		if s.stream != 0 {
			s.endRebind()
		}
	}
	if p := event.GetParticipantDisconnected(); p != nil && p.GetParticipantIdentity() == s.participant && !s.reconnecting {
		return r.failSession(s, fmt.Errorf("%w: publisher disconnected", ErrSourceEnded))
	}
	if tr := event.GetTrackUnsubscribed(); tr != nil && tr.GetTrackSid() == s.trackSID {
		if s.reconnecting {
			return r.retireInput(s)
		}
		return r.failSession(s, fmt.Errorf("%w: track unsubscribed", ErrSourceEnded))
	}
	return nil
}
