package main

import pb "github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb"

// Native full restart removes remote participants and subscriptions before
// reconnecting. Only that bounded lifecycle transition may retire the old sink
// without ending the application session. This does not decide packet playout.
type receiveLifecycle struct{ reconnecting bool }

func (l *receiveLifecycle) observe(r *pb.RoomEvent, participant, sid string) (reset bool, err error) {
	if r.GetReconnecting() != nil {
		l.reconnecting = true
	}
	if r.GetReconnected() != nil {
		l.reconnecting = false
	}
	if p := r.GetParticipantDisconnected(); p != nil && p.GetParticipantIdentity() == participant && !l.reconnecting {
		return false, errPublisherDisconnected
	}
	if t := r.GetTrackUnsubscribed(); t != nil && t.GetTrackSid() == sid {
		if l.reconnecting {
			return true, nil
		}
		return false, errTrackUnsubscribed
	}
	return false, nil
}
