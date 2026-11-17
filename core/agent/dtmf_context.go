package agent

import "context"

// EmitSipDTMFContext allows a transport to cancel delivery to stalled subscribers
// without blocking its media callbacks. Existing EmitSipDTMF behavior is unchanged.
func (s *AgentSession) EmitSipDTMFContext(ctx context.Context, ev SipDTMFEvent) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	primary, subscribed, subscribers, done := s.sipDTMFSubscribers()
	send := func(ch chan SipDTMFEvent) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			return context.Canceled
		case ch <- ev:
			return nil
		}
	}
	if subscribed {
		if e := send(primary); e != nil {
			return e
		}
	} else if primary != nil {
		select {
		case primary <- ev:
		default:
		}
	}
	for _, ch := range subscribers {
		if e := send(ch); e != nil {
			return e
		}
	}
	return nil
}
