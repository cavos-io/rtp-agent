package livekit

import (
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

func TestClientEventsDispatcherNoopsWithoutRoom(t *testing.T) {
	dispatcher := newClientEventsDispatcher(nil)

	dispatcher.DispatchAgentState(agent.AgentStateIdle)
	dispatcher.DispatchAgentState(agent.AgentStateThinking)
	dispatcher.DispatchAgentState(agent.AgentStateSpeaking)
	dispatcher.DispatchAgentState(agent.AgentState("unknown"))
	dispatcher.DispatchUserState(agent.UserStateListening)
	dispatcher.DispatchUserState(agent.UserStateSpeaking)
	dispatcher.DispatchUserState(agent.UserStateAway)
	dispatcher.DispatchUserState(agent.UserState("unknown"))
}

func TestClientEventsDispatcherNoopsWhenRoomDisconnected(t *testing.T) {
	dispatcher := newClientEventsDispatcher(&lksdk.Room{LocalParticipant: &lksdk.LocalParticipant{}})

	dispatcher.DispatchAgentState(agent.AgentStateThinking)
	dispatcher.DispatchUserState(agent.UserStateSpeaking)
}
