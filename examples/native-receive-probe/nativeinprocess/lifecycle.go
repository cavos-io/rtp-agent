package nativeinprocess

import (
	"context"
	"errors"
	pb "native-receive-probe/ffipb"
)

// ConnectMicrophone keeps the selected caller across microphone republishing.
// ConnectSelected retains its exact-publication contract for existing probes.
func (r *Runtime) ConnectMicrophone(ctx context.Context, url, token, participant, sid string) (*Session, error) {
	return r.connectSelected(ctx, url, token, participant, sid, true)
}
func (s *Session) InputTrackSID() string {
	v := s.inputSID.Load()
	if v == nil {
		return ""
	}
	return v.(string)
}
func (s *Session) WaitOutputSubscribed(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return errors.Join(ErrClosed, s.Err())
	case <-s.outputSubscribed:
		return nil
	}
}
func (s *Session) observePublication(identity string, p *pb.TrackPublicationInfo) {
	if !s.microphone || identity != s.participant {
		return
	}
	if p.GetKind() == pb.TrackKind_KIND_AUDIO && p.GetSource() == pb.TrackSource_SOURCE_MICROPHONE {
		s.microphones[p.GetSid()] = true
	}
}

// A subscription event may precede the publish callback on the FFI queue.
func (s *Session) markOutputSubscribed() {
	if s.outputSID != "" && s.outputSubscriptionSID == s.outputSID && !s.outputSubscribedOnce {
		s.outputSubscribedOnce = true
		close(s.outputSubscribed)
	}
}
