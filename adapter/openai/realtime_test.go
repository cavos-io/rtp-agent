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
