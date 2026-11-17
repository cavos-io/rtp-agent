//go:build nativeinprocess

package nativeroom

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/library/logger"
	native "native-receive-probe/nativeinprocess"
	"time"
)

func nativeClientState(a agent.AgentState, u agent.UserState) native.ClientState {
	data := func(kind, state string, ok bool) []byte {
		if !ok {
			return nil
		}
		b, _ := json.Marshal(struct {
			Type  string `json:"type"`
			State string `json:"state,omitempty"`
		}{kind, state})
		return b
	}
	av, aok := agent.ClientAgentStateString(a)
	uv, uok := agent.ClientUserStateString(u)
	return native.ClientState{Agent: string(a), AgentData: data("agent_state_changed", av, aok), UserData: data("user_state_changed", uv, uok)}
}

// State notifications wake one observer. It reads current state instead of
// replaying queued obsolete transitions; native retains the latest snapshot.
func startClientState(ctx context.Context, session *agent.AgentSession, send func(context.Context, native.ClientState) error, failures <-chan error) func(context.Context) error {
	run, cancel := context.WithCancel(ctx)
	agents, offAgent := session.SubscribeAgentStateChangedEvents()
	users, offUser := session.SubscribeUserStateChangedEvents()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer offAgent()
		defer offUser()
		publish := func() {
			c, stop := context.WithTimeout(run, 2*time.Second)
			defer stop()
			if e := send(c, nativeClientState(session.AgentState(), session.UserState())); e != nil && run.Err() == nil && !errors.Is(e, native.ErrClosed) {
				logger.Logger.Warnw("native client state submission failed", e)
			}
		}
		publish()
		for {
			select {
			case <-run.Done():
				return
			case _, ok := <-agents:
				if !ok {
					return
				}
				publish()
			case _, ok := <-users:
				if !ok {
					return
				}
				publish()
			case e, ok := <-failures:
				if !ok {
					failures = nil
					continue
				}
				if run.Err() == nil {
					logger.Logger.Warnw("native client state publication failed", e)
				}
			}
		}
	}()
	return func(c context.Context) error {
		cancel()
		select {
		case <-done:
			return nil
		case <-c.Done():
			return c.Err()
		}
	}
}
