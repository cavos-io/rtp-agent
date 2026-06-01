package openai

import (
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestRealtimeModelCapabilitiesMatchReference(t *testing.T) {
	model := NewRealtimeModel("test-key", "")

	capabilities := model.Capabilities()

	if !capabilities.MessageTruncation {
		t.Fatal("MessageTruncation = false, want true")
	}
	if !capabilities.ManualFunctionCalls {
		t.Fatal("ManualFunctionCalls = false, want true")
	}
	if !capabilities.MutableChatContext || !capabilities.MutableInstructions || !capabilities.MutableTools {
		t.Fatalf("mutable capabilities = %#v, want chat context, instructions, and tools mutable", capabilities)
	}
	if !capabilities.PerResponseToolChoice {
		t.Fatal("PerResponseToolChoice = false, want true")
	}
}

func TestRealtimeUpdateOptionsMessageMapsNamedToolChoice(t *testing.T) {
	msg := openAIRealtimeUpdateOptionsMessage(llm.RealtimeSessionOptions{
		ToolChoice: map[string]any{
			"type":     "function",
			"function": map[string]any{"name": "lookup"},
		},
	})

	if msg["type"] != "session.update" {
		t.Fatalf("message type = %#v, want session.update", msg["type"])
	}
	session := msg["session"].(map[string]any)
	choice := session["tool_choice"].(map[string]any)
	if choice["type"] != "function" || choice["name"] != "lookup" {
		t.Fatalf("tool_choice = %#v, want realtime named function", choice)
	}
}

func TestRealtimeAudioBufferMessages(t *testing.T) {
	commit := openAIRealtimeCommitAudioMessage()
	if commit["type"] != "input_audio_buffer.commit" {
		t.Fatalf("commit message type = %#v, want input_audio_buffer.commit", commit["type"])
	}

	clear := openAIRealtimeClearAudioMessage()
	if clear["type"] != "input_audio_buffer.clear" {
		t.Fatalf("clear message type = %#v, want input_audio_buffer.clear", clear["type"])
	}
}

func TestRealtimeGenerateReplyMessageMapsPerResponseOptions(t *testing.T) {
	msg := openAIRealtimeGenerateReplyMessage(llm.RealtimeGenerateReplyOptions{
		Instructions: "answer briefly",
		ToolChoice: map[string]any{
			"type":     "function",
			"function": map[string]any{"name": "lookup"},
		},
		Tools: []llm.Tool{requestTestTool{}},
	})

	if msg["type"] != "response.create" {
		t.Fatalf("message type = %#v, want response.create", msg["type"])
	}
	response := msg["response"].(map[string]any)
	if response["instructions"] != "answer briefly" {
		t.Fatalf("instructions = %#v, want answer briefly", response["instructions"])
	}
	choice := response["tool_choice"].(map[string]any)
	if choice["type"] != "function" || choice["name"] != "lookup" {
		t.Fatalf("tool_choice = %#v, want realtime named function", choice)
	}
	tools := response["tools"].([]map[string]any)
	if len(tools) != 1 || tools[0]["name"] != "lookup" {
		t.Fatalf("tools = %#v, want lookup tool", tools)
	}
}

func TestRealtimeTruncateMessageMapsAudioModality(t *testing.T) {
	msg := openAIRealtimeTruncateMessage(llm.RealtimeTruncateOptions{
		MessageID:      "msg_123",
		Modalities:     []string{"text", "audio"},
		AudioEndMillis: 1500,
	})

	if msg["type"] != "conversation.item.truncate" {
		t.Fatalf("message type = %#v, want conversation.item.truncate", msg["type"])
	}
	if msg["item_id"] != "msg_123" {
		t.Fatalf("item_id = %#v, want msg_123", msg["item_id"])
	}
	if msg["content_index"] != 0 {
		t.Fatalf("content_index = %#v, want 0", msg["content_index"])
	}
	if msg["audio_end_ms"] != 1500 {
		t.Fatalf("audio_end_ms = %#v, want 1500", msg["audio_end_ms"])
	}
}

func TestRealtimeVideoMessageMapsImageContent(t *testing.T) {
	msg, err := openAIRealtimeVideoMessage(&llm.ImageContent{
		Image:    "data:image/png;base64,aW1hZ2U=",
		MimeType: "image/png",
	})
	if err != nil {
		t.Fatalf("openAIRealtimeVideoMessage error = %v, want nil", err)
	}

	if msg["type"] != "conversation.item.create" {
		t.Fatalf("message type = %#v, want conversation.item.create", msg["type"])
	}
	item := msg["item"].(map[string]any)
	if item["type"] != "message" || item["role"] != "user" {
		t.Fatalf("item = %#v, want user message", item)
	}
	content := item["content"].([]map[string]any)
	if len(content) != 1 || content[0]["type"] != "input_image" {
		t.Fatalf("content = %#v, want one input_image part", content)
	}
	if content[0]["image_url"] != "data:image/png;base64,aW1hZ2U=" {
		t.Fatalf("image_url = %#v, want data URL", content[0]["image_url"])
	}
}

func TestRealtimeEventMapsInputAudioTranscriptionCompleted(t *testing.T) {
	confidence := 0.87
	ev, ok := openAIRealtimeEvent(map[string]any{
		"type":       "conversation.item.input_audio_transcription.completed",
		"item_id":    "item_123",
		"transcript": "hello",
		"confidence": confidence,
	})
	if !ok {
		t.Fatal("openAIRealtimeEvent returned ok=false, want transcription event")
	}
	if ev.Type != llm.RealtimeEventTypeInputAudioTranscriptionCompleted {
		t.Fatalf("event type = %q, want input audio transcription", ev.Type)
	}
	if ev.InputTranscription == nil {
		t.Fatal("InputTranscription = nil, want transcription payload")
	}
	if ev.InputTranscription.ItemID != "item_123" || ev.InputTranscription.Transcript != "hello" || !ev.InputTranscription.IsFinal {
		t.Fatalf("InputTranscription = %#v, want final item transcript", ev.InputTranscription)
	}
	if ev.InputTranscription.Confidence == nil || *ev.InputTranscription.Confidence != confidence {
		t.Fatalf("Confidence = %#v, want %.2f", ev.InputTranscription.Confidence, confidence)
	}
}

func TestRealtimeEventMapsInputAudioTranscriptionDelta(t *testing.T) {
	ev, ok := openAIRealtimeEvent(map[string]any{
		"type":    "conversation.item.input_audio_transcription.delta",
		"item_id": "item_123",
		"delta":   "hel",
	})
	if !ok {
		t.Fatal("openAIRealtimeEvent returned ok=false, want transcription delta event")
	}
	if ev.Type != llm.RealtimeEventTypeInputAudioTranscriptionCompleted {
		t.Fatalf("event type = %q, want input audio transcription", ev.Type)
	}
	if ev.InputTranscription == nil {
		t.Fatal("InputTranscription = nil, want transcription payload")
	}
	if ev.InputTranscription.ItemID != "item_123" || ev.InputTranscription.Transcript != "hel" || ev.InputTranscription.IsFinal {
		t.Fatalf("InputTranscription = %#v, want non-final delta transcript", ev.InputTranscription)
	}
	if ev.InputTranscription.Confidence != nil {
		t.Fatalf("Confidence = %#v, want nil for delta", ev.InputTranscription.Confidence)
	}
}

func TestRealtimeEventMapsResponseCreated(t *testing.T) {
	ev, ok := openAIRealtimeEvent(map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id":       "resp_123",
			"metadata": map[string]any{"client_event_id": "response_create_123"},
		},
	})
	if !ok {
		t.Fatal("openAIRealtimeEvent returned ok=false, want generation-created event")
	}
	if ev.Type != llm.RealtimeEventTypeGenerationCreated {
		t.Fatalf("event type = %q, want generation created", ev.Type)
	}
	if ev.Generation == nil {
		t.Fatal("Generation = nil, want generation-created payload")
	}
	if ev.Generation.ResponseID != "resp_123" || !ev.Generation.UserInitiated {
		t.Fatalf("Generation = %#v, want user-initiated response", ev.Generation)
	}
}

func TestRealtimeEventMapsConversationItemAddedMessage(t *testing.T) {
	ev, ok := openAIRealtimeEvent(map[string]any{
		"type":             "conversation.item.added",
		"previous_item_id": "prev_123",
		"item": map[string]any{
			"id":   "msg_123",
			"type": "message",
			"role": "user",
			"content": []any{
				map[string]any{"type": "input_text", "text": "hello"},
			},
		},
	})
	if !ok {
		t.Fatal("openAIRealtimeEvent returned ok=false, want remote item event")
	}
	if ev.Type != llm.RealtimeEventTypeRemoteItemAdded {
		t.Fatalf("event type = %q, want remote item added", ev.Type)
	}
	if ev.RemoteItem == nil {
		t.Fatal("RemoteItem = nil, want remote item payload")
	}
	if ev.RemoteItem.PreviousItemID != "prev_123" {
		t.Fatalf("PreviousItemID = %q, want prev_123", ev.RemoteItem.PreviousItemID)
	}
	msg, ok := ev.RemoteItem.Item.(*llm.ChatMessage)
	if !ok {
		t.Fatalf("RemoteItem.Item = %T, want *llm.ChatMessage", ev.RemoteItem.Item)
	}
	if msg.ID != "msg_123" || msg.Role != llm.ChatRoleUser || msg.TextContent() != "hello" {
		t.Fatalf("message = %#v, want user text message", msg)
	}
}

func TestRealtimeEventMapsConversationItemAddedFunctionCall(t *testing.T) {
	ev, ok := openAIRealtimeEvent(map[string]any{
		"type": "conversation.item.added",
		"item": map[string]any{
			"id":        "fc_123",
			"type":      "function_call",
			"call_id":   "call_123",
			"name":      "lookup",
			"arguments": `{"query":"hello"}`,
		},
	})
	if !ok {
		t.Fatal("openAIRealtimeEvent returned ok=false, want remote function call event")
	}
	if ev.Type != llm.RealtimeEventTypeRemoteItemAdded {
		t.Fatalf("event type = %q, want remote item added", ev.Type)
	}
	call, ok := ev.RemoteItem.Item.(*llm.FunctionCall)
	if !ok {
		t.Fatalf("RemoteItem.Item = %T, want *llm.FunctionCall", ev.RemoteItem.Item)
	}
	if call.ID != "fc_123" || call.CallID != "call_123" || call.Name != "lookup" || call.Arguments != `{"query":"hello"}` {
		t.Fatalf("function call = %#v, want OpenAI function call item", call)
	}
}

func TestRealtimeEventMapsConversationItemAddedFunctionCallOutput(t *testing.T) {
	ev, ok := openAIRealtimeEvent(map[string]any{
		"type": "conversation.item.added",
		"item": map[string]any{
			"id":      "out_123",
			"type":    "function_call_output",
			"call_id": "call_123",
			"output":  "Paris",
		},
	})
	if !ok {
		t.Fatal("openAIRealtimeEvent returned ok=false, want remote function output event")
	}
	if ev.Type != llm.RealtimeEventTypeRemoteItemAdded {
		t.Fatalf("event type = %q, want remote item added", ev.Type)
	}
	output, ok := ev.RemoteItem.Item.(*llm.FunctionCallOutput)
	if !ok {
		t.Fatalf("RemoteItem.Item = %T, want *llm.FunctionCallOutput", ev.RemoteItem.Item)
	}
	if output.ID != "out_123" || output.CallID != "call_123" || output.Output != "Paris" || output.IsError {
		t.Fatalf("function output = %#v, want successful OpenAI function output item", output)
	}
}

func TestRealtimeChatContextCreateMessagesMapUserTextMessage(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.AddMessage(llm.ChatMessageArgs{
		ID:   "msg_123",
		Role: llm.ChatRoleUser,
		Text: "hello",
	})

	msgs, err := openAIRealtimeChatContextCreateMessages(chatCtx)
	if err != nil {
		t.Fatalf("openAIRealtimeChatContextCreateMessages error = %v, want nil", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages len = %d, want 1", len(msgs))
	}
	msg := msgs[0]
	if msg["type"] != "conversation.item.create" {
		t.Fatalf("message type = %#v, want conversation.item.create", msg["type"])
	}
	if msg["previous_item_id"] != "root" {
		t.Fatalf("previous_item_id = %#v, want root", msg["previous_item_id"])
	}
	item := msg["item"].(map[string]any)
	if item["id"] != "msg_123" || item["type"] != "message" || item["role"] != "user" {
		t.Fatalf("item = %#v, want user message item", item)
	}
	content := item["content"].([]map[string]any)
	if len(content) != 1 || content[0]["type"] != "input_text" || content[0]["text"] != "hello" {
		t.Fatalf("content = %#v, want input text hello", content)
	}
}

func TestRealtimeChatContextUpdateMessagesDeleteRemovedAndRecreateChangedItems(t *testing.T) {
	oldCtx := llm.NewChatContext()
	oldCtx.AddMessage(llm.ChatMessageArgs{ID: "keep", Role: llm.ChatRoleUser, Text: "keep"})
	oldCtx.AddMessage(llm.ChatMessageArgs{ID: "changed", Role: llm.ChatRoleAssistant, Text: "old"})
	oldCtx.AddMessage(llm.ChatMessageArgs{ID: "removed", Role: llm.ChatRoleUser, Text: "remove"})

	newCtx := llm.NewChatContext()
	newCtx.AddMessage(llm.ChatMessageArgs{ID: "keep", Role: llm.ChatRoleUser, Text: "keep"})
	newCtx.AddMessage(llm.ChatMessageArgs{ID: "changed", Role: llm.ChatRoleAssistant, Text: "new"})
	newCtx.AddMessage(llm.ChatMessageArgs{ID: "created", Role: llm.ChatRoleUser, Text: "create"})

	msgs, err := openAIRealtimeChatContextUpdateMessages(oldCtx, newCtx)
	if err != nil {
		t.Fatalf("openAIRealtimeChatContextUpdateMessages error = %v, want nil", err)
	}
	if len(msgs) != 4 {
		t.Fatalf("messages len = %d, want delete removed, create created, delete changed, create changed", len(msgs))
	}
	if msgs[0]["type"] != "conversation.item.delete" || msgs[0]["item_id"] != "removed" {
		t.Fatalf("first message = %#v, want delete removed", msgs[0])
	}
	if msgs[1]["type"] != "conversation.item.create" || msgs[1]["previous_item_id"] != "changed" {
		t.Fatalf("second message = %#v, want create after changed", msgs[1])
	}
	created := msgs[1]["item"].(map[string]any)
	if created["id"] != "created" {
		t.Fatalf("created item = %#v, want created", created)
	}
	if msgs[2]["type"] != "conversation.item.delete" || msgs[2]["item_id"] != "changed" {
		t.Fatalf("third message = %#v, want delete changed", msgs[2])
	}
	if msgs[3]["type"] != "conversation.item.create" || msgs[3]["previous_item_id"] != "keep" {
		t.Fatalf("fourth message = %#v, want recreate changed after keep", msgs[3])
	}
	changed := msgs[3]["item"].(map[string]any)
	content := changed["content"].([]map[string]any)
	if changed["id"] != "changed" || content[0]["text"] != "new" {
		t.Fatalf("changed item = %#v, want changed text new", changed)
	}
}

func TestRealtimeEventMapsResponseDoneMetrics(t *testing.T) {
	ev, ok := openAIRealtimeEvent(map[string]any{
		"type": "response.done",
		"response": map[string]any{
			"id":     "resp_123",
			"status": "cancelled",
			"usage": map[string]any{
				"input_tokens":  11.0,
				"output_tokens": 7.0,
				"total_tokens":  18.0,
				"input_token_details": map[string]any{
					"audio_tokens":  3.0,
					"text_tokens":   4.0,
					"image_tokens":  1.0,
					"cached_tokens": 2.0,
					"cached_tokens_details": map[string]any{
						"text_tokens":  1.0,
						"audio_tokens": 1.0,
						"image_tokens": 0.0,
					},
				},
				"output_token_details": map[string]any{
					"text_tokens":  5.0,
					"audio_tokens": 2.0,
					"image_tokens": 0.0,
				},
			},
		},
	})
	if !ok {
		t.Fatal("openAIRealtimeEvent returned ok=false, want metrics event")
	}
	if ev.Type != llm.RealtimeEventTypeMetricsCollected {
		t.Fatalf("event type = %q, want metrics collected", ev.Type)
	}
	if ev.Metrics == nil {
		t.Fatal("Metrics = nil, want realtime metrics payload")
	}
	if ev.Metrics.RequestID != "resp_123" || !ev.Metrics.Cancelled {
		t.Fatalf("metrics identity = %#v, want cancelled response metrics", ev.Metrics)
	}
	if ev.Metrics.InputTokens != 11 || ev.Metrics.OutputTokens != 7 || ev.Metrics.TotalTokens != 18 {
		t.Fatalf("token totals = %#v, want 11/7/18", ev.Metrics)
	}
	if ev.Metrics.InputTokenDetails.AudioTokens != 3 || ev.Metrics.InputTokenDetails.TextTokens != 4 || ev.Metrics.InputTokenDetails.ImageTokens != 1 || ev.Metrics.InputTokenDetails.CachedTokens != 2 {
		t.Fatalf("input details = %#v, want audio/text/image/cached usage", ev.Metrics.InputTokenDetails)
	}
	if ev.Metrics.InputTokenDetails.CachedTokensDetails == nil {
		t.Fatal("CachedTokensDetails = nil, want cached token breakdown")
	}
	if ev.Metrics.InputTokenDetails.CachedTokensDetails.TextTokens != 1 || ev.Metrics.InputTokenDetails.CachedTokensDetails.AudioTokens != 1 {
		t.Fatalf("cached details = %#v, want text/audio cached usage", ev.Metrics.InputTokenDetails.CachedTokensDetails)
	}
	if ev.Metrics.OutputTokenDetails.TextTokens != 5 || ev.Metrics.OutputTokenDetails.AudioTokens != 2 {
		t.Fatalf("output details = %#v, want text/audio output usage", ev.Metrics.OutputTokenDetails)
	}
}
