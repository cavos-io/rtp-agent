package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/gorilla/websocket"
)

type RealtimeModel struct {
	apiKey  string
	model   string
	baseURL string
	dialer  *websocket.Dialer
}

type RealtimeOption func(*RealtimeModel)

func WithRealtimeDialer(dialer *websocket.Dialer) RealtimeOption {
	return func(m *RealtimeModel) {
		m.dialer = dialer
	}
}

func WithRealtimeBaseURL(url string) RealtimeOption {
	return func(m *RealtimeModel) {
		m.baseURL = url
	}
}

func NewRealtimeModel(apiKey, model string, opts ...RealtimeOption) *RealtimeModel {
	if model == "" {
		model = "gpt-4o-realtime-preview"
	}
	m := &RealtimeModel{
		apiKey:  apiKey,
		model:   model,
		baseURL: "wss://api.openai.com/v1/realtime",
		dialer:  websocket.DefaultDialer,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

type realtimeSession struct {
	conn    *websocket.Conn
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	eventCh chan llm.RealtimeEvent
}

func (m *RealtimeModel) Session() (llm.RealtimeSession, error) {
	wsURL := fmt.Sprintf("%s?model=%s", m.baseURL, m.model)
	
	header := http.Header{}
	header.Add("Authorization", "Bearer "+m.apiKey)
	header.Add("OpenAI-Beta", "realtime=v1")

	conn, _, err := m.dialer.Dial(wsURL, header)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to OpenAI realtime: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &realtimeSession{
		conn:    conn,
		ctx:     ctx,
		cancel:  cancel,
		eventCh: make(chan llm.RealtimeEvent, 100),
	}

	go s.eventLoop()

	return s, nil
}

func (s *realtimeSession) EventCh() <-chan llm.RealtimeEvent {
	return s.eventCh
}

func (s *realtimeSession) UpdateInstructions(instructions string) error {
	msg := map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"instructions": instructions,
		},
	}
	return s.sendMsg(msg)
}

func (s *realtimeSession) UpdateChatContext(chatCtx *llm.ChatContext) error {
	// Send existing context to OpenAI
	return nil
}

func (s *realtimeSession) UpdateTools(tools []interface{}) error {
	tc := llm.NewToolContext(tools)
	oaTools := tc.ParseFunctionTools("openai.responses")

	msg := map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"tools": oaTools,
		},
	}
	return s.sendMsg(msg)
}

func (s *realtimeSession) Interrupt() error {
	msg := map[string]any{
		"type": "response.cancel",
	}
	return s.sendMsg(msg)
}

func (s *realtimeSession) PushAudio(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}

	b64Audio := base64.StdEncoding.EncodeToString(frame.Data)
	msg := map[string]any{
		"type": "input_audio_buffer.append",
		"audio": b64Audio,
	}
	return s.sendMsg(msg)
}

func (s *realtimeSession) Close() error {
	s.cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.Close()
}

func (s *realtimeSession) sendMsg(msg any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.TextMessage, b)
}

func (s *realtimeSession) eventLoop() {
	defer close(s.eventCh)
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			_, msg, err := s.conn.ReadMessage()
			if err != nil {
				logger.Logger.Errorw("OpenAI realtime disconnected", err)
				s.cancel()
				return
			}

			var ev map[string]any
			if err := json.Unmarshal(msg, &ev); err != nil {
				continue
			}

			evType, _ := ev["type"].(string)
			switch evType {
			case "error":
				logger.Logger.Errorw("OpenAI realtime error", nil, "payload", string(msg))
				s.eventCh <- llm.RealtimeEvent{
					Type:  llm.RealtimeEventTypeError,
					Error: fmt.Errorf("openai error: %s", string(msg)),
				}
			case "response.text.delta":
				if delta, ok := ev["delta"].(string); ok {
					s.eventCh <- llm.RealtimeEvent{
						Type: llm.RealtimeEventTypeText,
						Text: delta,
					}
				}
			case "response.audio.delta":
				if delta, ok := ev["delta"].(string); ok {
					s.eventCh <- llm.RealtimeEvent{
						Type: llm.RealtimeEventTypeAudio,
						Data: []byte(delta), // base64 encoded audio
					}
				}
			case "response.function_call_arguments.delta":
				if name, ok := ev["name"].(string); ok {
					if args, ok2 := ev["delta"].(string); ok2 {
						callID, _ := ev["call_id"].(string)
						s.eventCh <- llm.RealtimeEvent{
							Type: llm.RealtimeEventTypeFunctionCall,
							Function: &llm.FunctionToolCall{
								CallID:    callID,
								Name:      name,
								Arguments: args,
							},
						}
					}
				}
			case "input_audio_buffer.speech_started":
				s.eventCh <- llm.RealtimeEvent{Type: llm.RealtimeEventTypeSpeechStarted}
			case "input_audio_buffer.speech_stopped":
				s.eventCh <- llm.RealtimeEvent{Type: llm.RealtimeEventTypeSpeechStopped}
			}
		}
	}
}

