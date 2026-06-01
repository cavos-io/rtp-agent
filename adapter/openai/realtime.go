package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/gorilla/websocket"
)

type RealtimeModel struct {
	apiKey  string
	model   string
	baseURL string
}

func NewRealtimeModel(apiKey, model string) *RealtimeModel {
	if model == "" {
		model = "gpt-4o-realtime-preview"
	}
	return &RealtimeModel{
		apiKey:  apiKey,
		model:   model,
		baseURL: "wss://api.openai.com/v1/realtime",
	}
}

func (m *RealtimeModel) Capabilities() llm.RealtimeCapabilities {
	return llm.RealtimeCapabilities{
		MessageTruncation:       true,
		TurnDetection:           true,
		UserTranscription:       true,
		AutoToolReplyGeneration: false,
		AudioOutput:             true,
		ManualFunctionCalls:     true,
		MutableChatContext:      true,
		MutableInstructions:     true,
		MutableTools:            true,
		PerResponseToolChoice:   true,
	}
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

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
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

func (s *realtimeSession) UpdateTools(tools []llm.Tool) error {
	msg := map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"tools": openAIRealtimeTools(tools),
		},
	}
	return s.sendMsg(msg)
}

func openAIRealtimeTools(tools []llm.Tool) []map[string]any {
	var oaTools []map[string]any
	for _, t := range tools {
		oaTools = append(oaTools, map[string]any{
			"type":        "function",
			"name":        t.Name(),
			"description": t.Description(),
			"parameters":  t.Parameters(),
		})
	}
	return oaTools
}

func (s *realtimeSession) UpdateOptions(options llm.RealtimeSessionOptions) error {
	return s.sendMsg(openAIRealtimeUpdateOptionsMessage(options))
}

func openAIRealtimeUpdateOptionsMessage(options llm.RealtimeSessionOptions) map[string]any {
	session := make(map[string]any)
	if toolChoice := openAIRealtimeToolChoice(options.ToolChoice); toolChoice != nil {
		session["tool_choice"] = toolChoice
	}
	return map[string]any{
		"type":    "session.update",
		"session": session,
	}
}

func openAIRealtimeToolChoice(choice llm.ToolChoice) any {
	switch tc := choice.(type) {
	case nil:
		return nil
	case string:
		return tc
	case map[string]any:
		if tc["type"] != "function" {
			return nil
		}
		function, ok := tc["function"].(map[string]any)
		if !ok {
			return nil
		}
		name, ok := function["name"].(string)
		if !ok || name == "" {
			return nil
		}
		return map[string]any{
			"type": "function",
			"name": name,
		}
	default:
		return nil
	}
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
		"type":  "input_audio_buffer.append",
		"audio": b64Audio,
	}
	return s.sendMsg(msg)
}

func (s *realtimeSession) GenerateReply(options llm.RealtimeGenerateReplyOptions) error {
	return s.sendMsg(openAIRealtimeGenerateReplyMessage(options))
}

func openAIRealtimeGenerateReplyMessage(options llm.RealtimeGenerateReplyOptions) map[string]any {
	response := make(map[string]any)
	if options.Instructions != "" {
		response["instructions"] = options.Instructions
	}
	if toolChoice := openAIRealtimeToolChoice(options.ToolChoice); toolChoice != nil {
		response["tool_choice"] = toolChoice
	}
	if options.Tools != nil {
		response["tools"] = openAIRealtimeTools(options.Tools)
	}

	return map[string]any{
		"type":     "response.create",
		"response": response,
	}
}

func (s *realtimeSession) CommitAudio() error {
	return s.sendMsg(openAIRealtimeCommitAudioMessage())
}

func openAIRealtimeCommitAudioMessage() map[string]any {
	return map[string]any{
		"type": "input_audio_buffer.commit",
	}
}

func (s *realtimeSession) ClearAudio() error {
	return s.sendMsg(openAIRealtimeClearAudioMessage())
}

func openAIRealtimeClearAudioMessage() map[string]any {
	return map[string]any{
		"type": "input_audio_buffer.clear",
	}
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
