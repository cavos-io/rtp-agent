package agent

import (
	"context"
	"encoding/json"
	"fmt"
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

// Discriminator types
type UserState string

const (
	UserStateSpeaking  UserState = "speaking"
	UserStateListening UserState = "listening"
	UserStateAway      UserState = "away"
)

type AgentState string

const (
	AgentStateInitializing AgentState = "initializing"
	AgentStateIdle         AgentState = "idle"
	AgentStateListening    AgentState = "listening"
	AgentStateThinking     AgentState = "thinking"
	AgentStateSpeaking     AgentState = "speaking"
)

// -- Strongly Typed Events --

type UserStateChangedEvent struct {
	OldState  UserState `json:"old_state"`
	NewState  UserState `json:"new_state"`
	CreatedAt time.Time `json:"created_at"`
}

func (e *UserStateChangedEvent) GetType() string { return "user_state_changed" }

type AgentStateChangedEvent struct {
	OldState  AgentState `json:"old_state"`
	NewState  AgentState `json:"new_state"`
	CreatedAt time.Time  `json:"created_at"`
}

func (e *AgentStateChangedEvent) GetType() string { return "agent_state_changed" }

type UserInputTranscribedEvent struct {
	Transcript string    `json:"transcript"`
	IsFinal    bool      `json:"is_final"`
	SpeakerID  string    `json:"speaker_id,omitempty"`
	Language   string    `json:"language,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

func (e *UserInputTranscribedEvent) GetType() string { return "user_input_transcribed" }

type AgentFalseInterruptionEvent struct {
	Resumed   bool      `json:"resumed"`
	CreatedAt time.Time `json:"created_at"`
}

func (e *AgentFalseInterruptionEvent) GetType() string { return "agent_false_interruption" }

type MetricsCollectedEvent struct {
	Metrics   telemetry.AgentMetrics `json:"metrics"`
	CreatedAt time.Time              `json:"created_at"`
}

func (e *MetricsCollectedEvent) GetType() string { return "metrics_collected" }

type ConversationItemAddedEvent struct {
	Item      llm.ChatItem `json:"item"`
	CreatedAt time.Time    `json:"created_at"`
}

func (e *ConversationItemAddedEvent) GetType() string { return "conversation_item_added" }

type FunctionToolsExecutedEvent struct {
	FunctionCalls       []llm.FunctionCall        `json:"function_calls"`
	FunctionCallOutputs []*llm.FunctionCallOutput `json:"function_call_outputs"`
	CreatedAt           time.Time                 `json:"created_at"`
	HasToolReply        bool                      `json:"has_tool_reply"`
	HasAgentHandoff     bool                      `json:"has_agent_handoff"`
}

func (e *FunctionToolsExecutedEvent) GetType() string { return "function_tools_executed" }

type AgentHandoffEvent struct {
	OldAgent   AgentInterface    `json:"-"`
	NewAgent   AgentInterface    `json:"-"`
	OldAgentID string            `json:"old_agent_id"`
	NewAgentID string            `json:"new_agent_id"`
	Handoff    *llm.AgentHandoff `json:"handoff"`
	CreatedAt  time.Time         `json:"created_at"`
}

func (e *AgentHandoffEvent) GetType() string { return "agent_handoff" }

type SpeechCreatedEvent struct {
	UserInitiated bool          `json:"user_initiated"`
	Source        string        `json:"source"`
	SpeechHandle  *SpeechHandle `json:"-"`
	ParticipantID string        `json:"participant_id,omitempty"`
	CreatedAt     time.Time     `json:"created_at"`
}

func (e *SpeechCreatedEvent) GetType() string { return "speech_created" }

type ErrorEvent struct {
	Error     error     `json:"error"`
	Source    any       `json:"source,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func (e *ErrorEvent) GetType() string { return "error" }

type CloseReason string

const (
	CloseReasonError                   CloseReason = "error"
	CloseReasonJobShutdown             CloseReason = "job_shutdown"
	CloseReasonParticipantDisconnected CloseReason = "participant_disconnected"
	CloseReasonUserInitiated           CloseReason = "user_initiated"
	CloseReasonTaskCompleted           CloseReason = "task_completed"
)

type CloseEvent struct {
	Reason    CloseReason `json:"reason"`
	Error     error       `json:"error,omitempty"`
	CreatedAt time.Time   `json:"created_at"`
}

func (e *CloseEvent) GetType() string { return "close" }

type TimelineEvent struct {
	Event     Event   `json:"-"`
	Timestamp float64 `json:"timestamp"`
}

func (e TimelineEvent) MarshalJSON() ([]byte, error) {
	if e.Event == nil {
		return []byte(`{"timestamp": ` + fmt.Sprintf("%f", e.Timestamp) + `}`), nil
	}

	// Marshal the inner event to a map
	b, err := json.Marshal(e.Event)
	if err != nil {
		return nil, err
	}

	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}

	// Inject discriminator and wrapper fields
	m["type"] = e.Event.GetType()
	m["timestamp"] = e.Timestamp

	return json.Marshal(m)
}

type EventTimeline struct {
	mu      sync.RWMutex
	events  []TimelineEvent
	OnEvent func(ev Event)
}

func NewEventTimeline() *EventTimeline {
	return &EventTimeline{
		events: make([]TimelineEvent, 0),
	}
}

func (t *EventTimeline) AddEvent(ev Event) {
	if t == nil || ev == nil {
		return
	}

	t.mu.Lock()
	t.events = append(t.events, TimelineEvent{
		Event:     ev,
		Timestamp: float64(time.Now().UnixNano()) / 1e9,
	})
	onEvent := t.OnEvent
	t.mu.Unlock()

	if onEvent != nil {
		onEvent(ev)
	}
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

type GetSessionStateResponse struct {
	AgentState string         `json:"agent_state"`
	UserState  string         `json:"user_state"`
	AgentID    string         `json:"agent_id"`
	Options    map[string]any `json:"options"`
	CreatedAt  float64        `json:"created_at"`
}

type GetChatHistoryResponse struct {
	Items []llm.ChatItem `json:"items"`
}

type GetAgentInfoResponse struct {
	ID           string         `json:"id"`
	Instructions string         `json:"instructions,omitempty"`
	Tools        []string       `json:"tools"`
	ChatCtx      []llm.ChatItem `json:"chat_ctx"`
}

type SendMessageRequest struct {
	Text string `json:"text"`
}

type SendMessageResponse struct {
	Items []llm.ChatItem `json:"items"`
}

type StreamRequest struct {
	ID      string `json:"id"`
	Method  string `json:"method"`
	Payload string `json:"payload"`
}

type StreamResponse struct {
	ID      string `json:"id"`
	Payload string `json:"payload,omitempty"`
	Error   string `json:"error,omitempty"`
}

// ClientEventsDispatcher manages sending Agent states to the LiveKit Room DataChannel
// and handling inbound RPC and DataChannel requests.
type ClientEventsDispatcher struct {
	room    *lksdk.Room
	session *AgentSession
	mu      sync.Mutex
}

func NewClientEventsDispatcher(room *lksdk.Room, session *AgentSession) *ClientEventsDispatcher {
	d := &ClientEventsDispatcher{
		room:    room,
		session: session,
	}
	d.registerHandlers()
	
	if session != nil && session.Timeline != nil {
		session.Timeline.OnEvent = d.streamClientEvent
	}
	
	return d
}

const TopicClientEvents = "lk-agent-client-events"

func (d *ClientEventsDispatcher) streamClientEvent(ev Event) {
	if d.room == nil || d.room.LocalParticipant == nil {
		return
	}

	b, err := json.Marshal(ev)
	if err != nil {
		return
	}

	// Wrap in ClientEventPayload for the client
	payload := map[string]any{
		"type":       ev.GetType(),
		"event":      json.RawMessage(b),
		"created_at": float64(time.Now().UnixNano()) / 1e9,
	}
	
	b, _ = json.Marshal(payload)
	
	_ = d.room.LocalParticipant.PublishDataPacket(
		&lksdk.UserDataPacket{
			Topic:   TopicClientEvents,
			Payload: b,
		},
	)
}

const (
	TopicAgentRequest  = "lk.agent.request"
	TopicAgentResponse = "lk.agent.response"
	TopicChat          = "lk.chat"
)

func (d *ClientEventsDispatcher) registerHandlers() {
	if d.room == nil || d.room.LocalParticipant == nil {
		return
	}

	d.room.RegisterRpcMethod("lk-agent-get-session-state", d.handleGetSessionState)
	d.room.RegisterRpcMethod("lk-agent-get-chat-history", d.handleGetChatHistory)
	d.room.RegisterRpcMethod("lk-agent-get-info", d.handleGetAgentInfo)
	d.room.RegisterRpcMethod("lk-agent-send-message", d.handleSendMessage)

	_ = d.room.RegisterTextStreamHandler(TopicAgentRequest, d.handleStreamRequest)
	_ = d.room.RegisterTextStreamHandler(TopicChat, d.handleUserTextInput)
}

func (d *ClientEventsDispatcher) handleUserTextInput(reader *lksdk.TextStreamReader, participantIdentity string) {
	if d.session == nil {
		return
	}

	text := reader.ReadAll()
	if text == "" {
		return
	}

	logger.Logger.Infow("received user text input", "text", text, "participant", participantIdentity)
	
	// Triggers async generation
	_, _ = d.session.GenerateReply(context.Background(), text)
}

func (d *ClientEventsDispatcher) handleStreamRequest(reader *lksdk.TextStreamReader, participantIdentity string) {
	data := reader.ReadAll()
	var req StreamRequest
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		logger.Logger.Warnw("failed to unmarshal stream request", err)
		return
	}

	go func() {
		var responsePayload string
		var errStr string

		switch req.Method {
		case "get_session_state":
			resp, err := d.handleGetSessionState(lksdk.RpcInvocationData{Payload: req.Payload})
			if err != nil {
				errStr = err.Error()
			} else {
				responsePayload = resp
			}
		case "get_chat_history":
			resp, err := d.handleGetChatHistory(lksdk.RpcInvocationData{Payload: req.Payload})
			if err != nil {
				errStr = err.Error()
			} else {
				responsePayload = resp
			}
		case "get_agent_info":
			resp, err := d.handleGetAgentInfo(lksdk.RpcInvocationData{Payload: req.Payload})
			if err != nil {
				errStr = err.Error()
			} else {
				responsePayload = resp
			}
		case "send_message":
			resp, err := d.handleSendMessage(lksdk.RpcInvocationData{Payload: req.Payload})
			if err != nil {
				errStr = err.Error()
			} else {
				responsePayload = resp
			}
		default:
			errStr = "unknown method: " + req.Method
		}

		response := StreamResponse{
			ID:      req.ID,
			Payload: responsePayload,
			Error:   errStr,
		}

		b, _ := json.Marshal(response)
		_ = d.room.LocalParticipant.PublishDataPacket(
			&lksdk.UserDataPacket{
				Topic:   TopicAgentResponse,
				Payload: b,
			},
			lksdk.WithDataPublishDestination([]string{participantIdentity}),
		)
	}()
}

func (d *ClientEventsDispatcher) handleGetSessionState(data lksdk.RpcInvocationData) (string, error) {
	if d.session == nil {
		return "", fmt.Errorf("no active session")
	}

	d.session.mu.Lock()
	agentState := d.session.AgentState
	userState := d.session.UserState

	// Convert options to map for JSON serialization
	optsBytes, _ := json.Marshal(d.session.Options)
	var optsMap map[string]any
	_ = json.Unmarshal(optsBytes, &optsMap)

	agentID := ""
	if d.session.Agent != nil {
		if a := d.session.Agent.GetAgent(); a != nil {
			agentID = a.ID
		}
	}
	d.session.mu.Unlock()

	resp := GetSessionStateResponse{
		AgentState: string(agentState),
		UserState:  string(userState),
		AgentID:    agentID,
		Options:    optsMap,
		CreatedAt:  float64(time.Now().UnixMilli()) / 1000.0, // Best effort since we don't track start time explicitly yet
	}

	b, _ := json.Marshal(resp)
	return string(b), nil
}

func (d *ClientEventsDispatcher) handleGetChatHistory(data lksdk.RpcInvocationData) (string, error) {
	if d.session == nil || d.session.ChatCtx == nil {
		b, _ := json.Marshal(GetChatHistoryResponse{Items: []llm.ChatItem{}})
		return string(b), nil
	}

	items := d.session.ChatCtx.Items
	resp := GetChatHistoryResponse{Items: items}
	b, _ := json.Marshal(resp)
	return string(b), nil
}

func (d *ClientEventsDispatcher) handleGetAgentInfo(data lksdk.RpcInvocationData) (string, error) {
	if d.session == nil || d.session.Agent == nil {
		return "", fmt.Errorf("no agent found")
	}

	a := d.session.Agent.GetAgent()
	if a == nil {
		return "", fmt.Errorf("no agent implementation found")
	}

	toolNames := make([]string, 0)
	for _, t := range a.Tools {
		if ft, ok := t.(llm.Tool); ok {
			toolNames = append(toolNames, ft.Name())
		} else if pt, ok := t.(llm.ProviderTool); ok {
			toolNames = append(toolNames, pt.Name())
		}
	}

	chatCtxItems := []llm.ChatItem{}
	if a.ChatCtx != nil {
		chatCtxItems = a.ChatCtx.Items
	}

	resp := GetAgentInfoResponse{
		ID:           a.ID,
		Instructions: a.Instructions,
		Tools:        toolNames,
		ChatCtx:      chatCtxItems,
	}

	b, _ := json.Marshal(resp)
	return string(b), nil
}

func (d *ClientEventsDispatcher) handleSendMessage(data lksdk.RpcInvocationData) (string, error) {
	if d.session == nil {
		return "", fmt.Errorf("no active session")
	}

	var req SendMessageRequest
	if err := json.Unmarshal([]byte(data.Payload), &req); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runResult, err := d.session.GenerateReply(ctx, req.Text)
	if err != nil {
		return "", fmt.Errorf("failed to generate reply: %w", err)
	}
	
	if runResult != nil {
		if err := runResult.Wait(ctx); err != nil {
			return "", fmt.Errorf("run failed: %w", err)
		}
	}

	items := []llm.ChatItem{}
	if runResult != nil {
		for _, ev := range runResult.Events {
			if item := ev.GetItem(); item != nil {
				items = append(items, item)
			}
		}
	}

	resp := SendMessageResponse{Items: items}
	b, _ := json.Marshal(resp)
	return string(b), nil
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
