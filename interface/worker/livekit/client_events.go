package livekit

import (
	"encoding/json"
	"sync"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/library/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

// clientEventsDispatcher manages sending agent states to the LiveKit Room DataChannel.
type clientEventsDispatcher struct {
	room *lksdk.Room
	mu   sync.Mutex
}

func newClientEventsDispatcher(room *lksdk.Room) *clientEventsDispatcher {
	return &clientEventsDispatcher{
		room: room,
	}
}

type clientEventPayload struct {
	Type  string `json:"type"`
	State string `json:"state,omitempty"`
}

func (d *clientEventsDispatcher) dispatchData(payload clientEventPayload) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.room == nil || d.room.LocalParticipant == nil || d.room.ConnectionState() != lksdk.ConnectionStateConnected {
		return
	}

	b, err := json.Marshal(payload)
	if err != nil {
		logger.Logger.Errorw("Failed to marshal client event", err)
		return
	}

	err = d.room.LocalParticipant.PublishDataPacket(lksdk.UserData(b), lksdk.WithDataPublishReliable(true), lksdk.WithDataPublishTopic("lk-agent-state"))
	if err != nil {
		logger.Logger.Errorw("Failed to publish client event data", err)
	}
}

// DispatchAgentState emits reference-style client agent states.
func (d *clientEventsDispatcher) DispatchAgentState(state agent.AgentState) {
	stateStr, ok := agent.ClientAgentStateString(state)
	if !ok {
		return
	}

	d.dispatchData(clientEventPayload{
		Type:  "agent_state_changed",
		State: stateStr,
	})
}

// DispatchUserState emits reference-style client user states.
func (d *clientEventsDispatcher) DispatchUserState(state agent.UserState) {
	stateStr, ok := agent.ClientUserStateString(state)
	if !ok {
		return
	}

	d.dispatchData(clientEventPayload{
		Type:  "user_state_changed",
		State: stateStr,
	})
}
