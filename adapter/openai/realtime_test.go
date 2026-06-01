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
