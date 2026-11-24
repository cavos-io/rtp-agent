package worker

import (
	"context"
	"github.com/cavos-io/rtp-agent/core/agent"
)

// SessionTransportProvider owns a worker's session transport resources. Factories
// must return a fresh provider per server. Close runs only after jobs settle.
// A nil factory preserves the default Pion transport.
type SessionTransportProvider interface {
	NewSession(*JobContext, *agent.AgentSession, StartSessionOptions) (SessionRoomIO, error)
	Close(context.Context) error
}
