package agent

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/core/llm"
	"github.com/cavos-io/conversation-worker/library/logger"
	"github.com/cavos-io/conversation-worker/library/telemetry"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

type Event interface {
	GetType() string
}

type TimelineEvent struct {
	Type      string         `json:"type"`
	Timestamp float64        `json:"timestamp"`
	Payload   map[string]any `json:"payload,omitempty"`
}

type EventTimeline struct {
	mu     sync.RWMutex
	events []TimelineEvent
}

func NewEventTimeline() *EventTimeline {
	return &EventTimeline{
		events: make([]TimelineEvent, 0),
	}
}

func (t *EventTimeline) Add(eventType string, payload map[string]any) {
	if t == nil || eventType == "" {
		return
	}

	t.mu.Lock()
	t.events = append(t.events, TimelineEvent{
		Type:      eventType,
		Timestamp: float64(time.Now().UnixNano()) / 1e9,
		Payload:   payload,
	})
	t.mu.Unlock()
}

func (t *EventTimeline) AddEvent(ev Event) {
	if t == nil || ev == nil {
		return
	}
	t.Add(ev.GetType(), eventPayload(ev))
}

func (t *EventTimeline) Snapshot() []TimelineEvent {
	if t == nil {
		return nil
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	out := make([]TimelineEvent, len(t.events))
	copy(out, t.events)
	return out
}

func eventPayload(ev Event) map[string]any {
	b, err := json.Marshal(ev)
	if err != nil {
		return nil
	}

	payload := make(map[string]any)
	if err := json.Unmarshal(b, &payload); err != nil {
		return nil
	}

	return payload
}

type UserInputTranscribedEvent struct {
	Language   string
	Transcript string
	IsFinal    bool
	SpeakerID  string
	CreatedAt  time.Time
}

func (e *UserInputTranscribedEvent) GetType() string { return "user_input_transcribed" }

type ConversationItemAddedEvent struct {
	Item      llm.ChatItem
	CreatedAt time.Time
}

func (e *ConversationItemAddedEvent) GetType() string { return "conversation_item_added" }

type MetricsCollectedEvent struct {
	Metrics   telemetry.AgentMetrics
	CreatedAt time.Time
}

func (e *MetricsCollectedEvent) GetType() string { return "metrics_collected" }

type SpeechCreatedEvent struct {
	UserInitiated bool
	Source        string // "say" or "generate_reply"
	SpeechHandle  *SpeechHandle
	CreatedAt     time.Time
}

func (e *SpeechCreatedEvent) GetType() string { return "speech_created" }

type CloseReason string

const (
	CloseReasonError                   CloseReason = "error"
	CloseReasonJobShutdown             CloseReason = "job_shutdown"
	CloseReasonParticipantDisconnected CloseReason = "participant_disconnected"
	CloseReasonUserInitiated           CloseReason = "user_initiated"
)

type CloseEvent struct {
	Reason    CloseReason
	Error     error
	CreatedAt time.Time
}

func (e *CloseEvent) GetType() string { return "close" }

type RunContext struct {
	Session      *AgentSession
	SpeechHandle *SpeechHandle
	FunctionCall *llm.FunctionCall
}

func (r *RunContext) WaitForPlayout(ctx context.Context) error {
	if r.Session != nil && r.Session.Assistant != nil {
		if r.SpeechHandle != nil {
			return r.SpeechHandle.Wait(ctx)
		}
	}
	return nil
}

type contextKey string

const runContextKey contextKey = "run_context"

func WithRunContext(ctx context.Context, rc *RunContext) context.Context {
	return context.WithValue(ctx, runContextKey, rc)
}

func GetRunContext(ctx context.Context) *RunContext {
	if rc, ok := ctx.Value(runContextKey).(*RunContext); ok {
		return rc
	}
	return nil
}

// ClientEventsDispatcher manages sending Agent states to the LiveKit Room DataChannel
type ClientEventsDispatcher struct {
	room *lksdk.Room
	mu   sync.Mutex
}

func NewClientEventsDispatcher(room *lksdk.Room) *ClientEventsDispatcher {
	return &ClientEventsDispatcher{
		room: room,
	}
}

type ClientEventPayload struct {
	Type  string `json:"type"`
	State string `json:"state,omitempty"`
}

func (d *ClientEventsDispatcher) dispatchData(payload ClientEventPayload) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.room == nil || d.room.LocalParticipant == nil {
		return
	}

	b, err := json.Marshal(payload)
	if err != nil {
		logger.Logger.Errorw("Failed to marshal client event", err)
		return
	}

	// Publish to the "lk-agent-state" topic which the frontend UI components listen to
	err = d.room.LocalParticipant.PublishData(b, lksdk.WithDataPublishReliable(true), lksdk.WithDataPublishTopic("lk-agent-state"))
	if err != nil {
		logger.Logger.Errorw("Failed to publish client event data", err)
	}
}

// DispatchAgentState emits AgentStateIdle, AgentStateThinking, AgentStateSpeaking
func (d *ClientEventsDispatcher) DispatchAgentState(state AgentState) {
	var stateStr string
	switch state {
	case AgentStateIdle:
		stateStr = "idle"
	case AgentStateThinking:
		stateStr = "thinking"
	case AgentStateSpeaking:
		stateStr = "speaking"
	default:
		return
	}

	d.dispatchData(ClientEventPayload{
		Type:  "agent_state_changed",
		State: stateStr,
	})
}

// DispatchUserState emits UserStateListening, UserStateSpeaking
func (d *ClientEventsDispatcher) DispatchUserState(state UserState) {
	var stateStr string
	switch state {
	case UserStateListening:
		stateStr = "listening"
	case UserStateSpeaking:
		stateStr = "speaking"
	default:
		return
	}

	d.dispatchData(ClientEventPayload{
		Type:  "user_state_changed",
		State: stateStr,
	})
}
