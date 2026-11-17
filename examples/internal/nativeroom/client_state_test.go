//go:build nativeinprocess

package nativeroom

import (
	"context"
	"encoding/json"
	"github.com/cavos-io/rtp-agent/core/agent"
	native "native-receive-probe/nativeinprocess"
	"testing"
	"time"
)

func TestNativeClientStateDefaultMappings(t *testing.T) {
	for _, a := range []agent.AgentState{agent.AgentStateInitializing, agent.AgentStateIdle, agent.AgentStateListening, agent.AgentStateThinking, agent.AgentStateSpeaking, "unknown"} {
		for _, u := range []agent.UserState{agent.UserStateListening, agent.UserStateSpeaking, agent.UserStateAway, "unknown"} {
			v := nativeClientState(a, u)
			if v.Agent != string(a) {
				t.Fatal("raw attribute changed")
			}
			av, aok := agent.ClientAgentStateString(a)
			uv, uok := agent.ClientUserStateString(u)
			for _, tc := range []struct {
				b           []byte
				kind, value string
				ok          bool
			}{
				{v.AgentData, "agent_state_changed", av, aok},
				{v.UserData, "user_state_changed", uv, uok},
			} {
				if !tc.ok {
					if len(tc.b) != 0 {
						t.Fatal("unknown state sent")
					}
					continue
				}
				var got map[string]string
				if e := json.Unmarshal(tc.b, &got); e != nil {
					t.Fatal(e)
				}
				if len(got) != 2 || got["type"] != tc.kind || got["state"] != tc.value {
					t.Fatal(got)
				}
			}
		}
	}
}
func TestNativeClientStateInitialTransitionAndShutdown(t *testing.T) {
	s := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	s.UpdateAgentState(agent.AgentStateListening)
	calls := make(chan native.ClientState, 32)
	closeState := startClientState(context.Background(), s, func(ctx context.Context, v native.ClientState) error { calls <- v; return nil }, nil)
	read := func() native.ClientState {
		select {
		case v := <-calls:
			return v
		case <-time.After(time.Second):
			t.Fatal("missing state")
			return native.ClientState{}
		}
	}
	if read().Agent != "listening" {
		t.Fatal("no current initial state")
	}
	s.UpdateAgentState(agent.AgentStateThinking)
	if read().Agent != "thinking" {
		t.Fatal("transition lost")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e := closeState(ctx); e != nil {
		t.Fatal(e)
	}
	s.UpdateAgentState(agent.AgentStateSpeaking)
	select {
	case v := <-calls:
		t.Fatal("publication after close", v)
	default:
	}
}
func TestNativeClientStateCloseCancelsBlockedSubmit(t *testing.T) {
	s := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	entered := make(chan struct{})
	closeState := startClientState(context.Background(), s, func(ctx context.Context, _ native.ClientState) error { close(entered); <-ctx.Done(); return ctx.Err() }, nil)
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e := closeState(ctx); e != nil {
		t.Fatal(e)
	}
}
