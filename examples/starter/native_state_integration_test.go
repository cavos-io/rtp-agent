//go:build nativeroomintegration

package main

import (
	"context"
	"encoding/json"
	"github.com/cavos-io/rtp-agent/core/agent"
	lk "github.com/livekit/server-sdk-go/v2"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type stateObservation struct{ Kind, State, Identity string }
type nativeStateCheck struct {
	t       *testing.T
	enabled bool
	events  chan stateObservation
}

func newNativeStateCheck(t *testing.T, cb *lk.RoomCallback) *nativeStateCheck {
	c := &nativeStateCheck{t: t, enabled: os.Getenv("NATIVE_TEST_STATE") == "1", events: make(chan stateObservation, 128)}
	if !c.enabled {
		return c
	}
	cb.OnAttributesChanged = func(changed map[string]string, p lk.Participant) {
		if value, ok := changed["lk.agent.state"]; ok {
			select {
			case c.events <- stateObservation{"attribute", value, p.Identity()}:
			default:
			}
		}
	}
	cb.OnDataPacket = func(packet lk.DataPacket, params lk.DataReceiveParams) {
		p, ok := packet.(*lk.UserDataPacket)
		if !ok || params.Topic != "lk-agent-state" {
			return
		}
		var v struct{ Type, State string }
		if json.Unmarshal(p.Payload, &v) != nil {
			return
		}
		select {
		case c.events <- stateObservation{v.Type, v.State, params.SenderIdentity}:
		default:
		}
	}
	return c
}
func (c *nativeStateCheck) verify(ctx context.Context, s *agent.AgentSession, dir string) {
	if !c.enabled {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var seen []stateObservation
	wait := func(a, u string) {
		c.t.Helper()
		wanted := map[string]bool{"attribute": false, "agent_state_changed": false, "user_state_changed": false}
		for {
			select {
			case v := <-c.events:
				seen = append(seen, v)
				if v.Identity != "primary-agent" {
					c.t.Fatal("wrong sender", v)
				}
				value := a
				if v.Kind == "user_state_changed" {
					value = u
				}
				if v.State == value {
					wanted[v.Kind] = true
				}
				if wanted["attribute"] && wanted["agent_state_changed"] && wanted["user_state_changed"] {
					return
				}
			case <-ctx.Done():
				c.t.Fatal("state timeout", a, u, seen)
			}
		}
	}
	wait("listening", "listening")
	s.UpdateUserState(agent.UserStateSpeaking)
	s.UpdateAgentState(agent.AgentStateThinking)
	wait("thinking", "speaking")
	s.UpdateUserState(agent.UserStateListening)
	s.UpdateAgentState(agent.AgentStateSpeaking)
	wait("speaking", "listening")
	s.UpdateAgentState(agent.AgentStateListening)
	wait("listening", "listening")
	b, e := json.MarshalIndent(seen, "", "  ")
	if e != nil {
		c.t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "client-state.json"), b, 0600); e != nil {
		c.t.Fatal(e)
	}
}
