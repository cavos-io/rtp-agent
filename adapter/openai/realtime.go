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
	"github.com/cavos-io/rtp-agent/library/utils/images"
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

func (s *realtimeSession) PushVideo(frame *images.VideoFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	data, err := images.Encode(frame, images.NewEncodeOptions())
	if err != nil {
		return err
	}
	image := &llm.ImageContent{
		Image:    fmt.Sprintf("data:image/jpeg;base64,%s", base64.StdEncoding.EncodeToString(data)),
		MimeType: "image/jpeg",
	}
	msg, err := openAIRealtimeVideoMessage(image)
	if err != nil {
		return err
	}
	return s.sendMsg(msg)
}

func openAIRealtimeVideoMessage(image *llm.ImageContent) (map[string]any, error) {
	img, err := llm.SerializeImage(image)
	if err != nil {
		return nil, err
	}
	if img.ExternalURL != "" {
		return nil, fmt.Errorf("openai realtime input_image does not support external image URLs")
	}
	url := fmt.Sprintf("data:%s;base64,%s", img.MIMEType, base64.StdEncoding.EncodeToString(img.DataBytes))
	return map[string]any{
		"type": "conversation.item.create",
		"item": map[string]any{
			"type": "message",
			"role": "user",
			"content": []map[string]any{
				{
					"type":      "input_image",
					"image_url": url,
				},
			},
		},
	}, nil
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

func (s *realtimeSession) Truncate(options llm.RealtimeTruncateOptions) error {
	if !realtimeModalitiesInclude(options.Modalities, "audio") {
		return nil
	}
	return s.sendMsg(openAIRealtimeTruncateMessage(options))
}

func openAIRealtimeTruncateMessage(options llm.RealtimeTruncateOptions) map[string]any {
	return map[string]any{
		"type":          "conversation.item.truncate",
		"content_index": 0,
		"item_id":       options.MessageID,
		"audio_end_ms":  options.AudioEndMillis,
	}
}

func realtimeModalitiesInclude(modalities []string, target string) bool {
	for _, modality := range modalities {
		if modality == target {
			return true
		}
	}
	return false
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

			realtimeEvent, ok := openAIRealtimeEvent(ev)
			if !ok {
				continue
			}
			if realtimeEvent.Type == llm.RealtimeEventTypeError {
				logger.Logger.Errorw("OpenAI realtime error", nil, "payload", string(msg))
			}
			s.eventCh <- realtimeEvent
		}
	}
}

func openAIRealtimeEvent(ev map[string]any) (llm.RealtimeEvent, bool) {
	evType, _ := ev["type"].(string)
	switch evType {
	case "error":
		return llm.RealtimeEvent{
			Type:  llm.RealtimeEventTypeError,
			Error: fmt.Errorf("openai error: %v", ev),
		}, true
	case "response.text.delta":
		if delta, ok := ev["delta"].(string); ok {
			return llm.RealtimeEvent{
				Type: llm.RealtimeEventTypeText,
				Text: delta,
			}, true
		}
	case "response.audio.delta":
		if delta, ok := ev["delta"].(string); ok {
			return llm.RealtimeEvent{
				Type: llm.RealtimeEventTypeAudio,
				Data: []byte(delta), // base64 encoded audio
			}, true
		}
	case "response.function_call_arguments.delta":
		if name, ok := ev["name"].(string); ok {
			if args, ok2 := ev["delta"].(string); ok2 {
				callID, _ := ev["call_id"].(string)
				return llm.RealtimeEvent{
					Type: llm.RealtimeEventTypeFunctionCall,
					Function: &llm.FunctionToolCall{
						CallID:    callID,
						Name:      name,
						Arguments: args,
					},
				}, true
			}
		}
	case "conversation.item.input_audio_transcription.completed":
		itemID, _ := ev["item_id"].(string)
		transcript, _ := ev["transcript"].(string)
		if itemID == "" && transcript == "" {
			return llm.RealtimeEvent{}, false
		}
		return llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
			InputTranscription: &llm.InputTranscriptionCompleted{
				ItemID:     itemID,
				Transcript: transcript,
				IsFinal:    true,
				Confidence: openAIRealtimeFloatPtr(ev["confidence"]),
			},
		}, true
	case "conversation.item.input_audio_transcription.delta":
		itemID, _ := ev["item_id"].(string)
		delta, _ := ev["delta"].(string)
		if itemID == "" && delta == "" {
			return llm.RealtimeEvent{}, false
		}
		return llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
			InputTranscription: &llm.InputTranscriptionCompleted{
				ItemID:     itemID,
				Transcript: delta,
				IsFinal:    false,
			},
		}, true
	case "response.created":
		response, _ := ev["response"].(map[string]any)
		responseID, _ := response["id"].(string)
		if responseID == "" {
			return llm.RealtimeEvent{}, false
		}
		_, userInitiated := openAIRealtimeResponseClientEventID(response)
		return llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeGenerationCreated,
			Generation: &llm.GenerationCreatedEvent{
				ResponseID:    responseID,
				UserInitiated: userInitiated,
			},
		}, true
	case "conversation.item.added", "conversation.item.created":
		item, ok := ev["item"].(map[string]any)
		if !ok {
			return llm.RealtimeEvent{}, false
		}
		chatItem, err := openAIRealtimeChatItem(item)
		if err != nil {
			return llm.RealtimeEvent{}, false
		}
		previousItemID, _ := ev["previous_item_id"].(string)
		return llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeRemoteItemAdded,
			RemoteItem: &llm.RemoteItemAddedEvent{
				PreviousItemID: previousItemID,
				Item:           chatItem,
			},
		}, true
	case "input_audio_buffer.speech_started":
		return llm.RealtimeEvent{Type: llm.RealtimeEventTypeSpeechStarted}, true
	case "input_audio_buffer.speech_stopped":
		return llm.RealtimeEvent{Type: llm.RealtimeEventTypeSpeechStopped}, true
	}
	return llm.RealtimeEvent{}, false
}

func openAIRealtimeChatItem(item map[string]any) (llm.ChatItem, error) {
	itemType, _ := item["type"].(string)
	switch itemType {
	case "message":
		return openAIRealtimeChatMessage(item)
	case "function_call":
		return openAIRealtimeFunctionCall(item)
	default:
		return nil, fmt.Errorf("unsupported realtime item type %q", itemType)
	}
}

func openAIRealtimeFunctionCall(item map[string]any) (*llm.FunctionCall, error) {
	id, _ := item["id"].(string)
	callID, _ := item["call_id"].(string)
	name, _ := item["name"].(string)
	arguments, _ := item["arguments"].(string)
	if id == "" || callID == "" || name == "" {
		return nil, fmt.Errorf("malformed realtime function call item")
	}
	return &llm.FunctionCall{
		ID:        id,
		CallID:    callID,
		Name:      name,
		Arguments: arguments,
	}, nil
}

func openAIRealtimeChatMessage(item map[string]any) (*llm.ChatMessage, error) {
	id, _ := item["id"].(string)
	roleRaw, _ := item["role"].(string)
	role := llm.ChatRole(roleRaw)
	switch role {
	case llm.ChatRoleSystem, llm.ChatRoleDeveloper, llm.ChatRoleUser, llm.ChatRoleAssistant:
	default:
		return nil, fmt.Errorf("unsupported realtime message role %q", roleRaw)
	}
	contents, _ := item["content"].([]any)
	return &llm.ChatMessage{
		ID:      id,
		Role:    role,
		Content: openAIRealtimeChatContent(contents),
	}, nil
}

func openAIRealtimeChatContent(contents []any) []llm.ChatContent {
	out := make([]llm.ChatContent, 0, len(contents))
	for _, content := range contents {
		part, ok := content.(map[string]any)
		if !ok {
			continue
		}
		partType, _ := part["type"].(string)
		switch partType {
		case "input_text", "output_text":
			if text, _ := part["text"].(string); text != "" {
				out = append(out, llm.ChatContent{Text: text})
			}
		case "input_image":
			if imageURL, _ := part["image_url"].(string); imageURL != "" {
				out = append(out, llm.ChatContent{
					Image: &llm.ImageContent{Image: imageURL},
				})
			}
		case "input_audio":
			if transcript, _ := part["transcript"].(string); transcript != "" {
				out = append(out, llm.ChatContent{Text: transcript})
			}
		}
	}
	return out
}

func openAIRealtimeFloatPtr(v any) *float64 {
	switch value := v.(type) {
	case float64:
		return &value
	case float32:
		f := float64(value)
		return &f
	default:
		return nil
	}
}

func openAIRealtimeResponseClientEventID(response map[string]any) (string, bool) {
	metadata, ok := response["metadata"].(map[string]any)
	if !ok {
		return "", false
	}
	clientEventID, ok := metadata["client_event_id"].(string)
	if !ok || clientEventID == "" {
		return "", false
	}
	return clientEventID, true
}
