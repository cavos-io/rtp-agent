package openai

import (
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestNewOpenAILLMWithBaseURLProviderUsesReferenceHost(t *testing.T) {
	model := NewOpenAILLMWithBaseURL("test-key", "gpt-4o", "https://gateway.openai.test/v1")

	if got := model.Provider(); got != "gateway.openai.test" {
		t.Fatalf("Provider() = %q, want gateway.openai.test", got)
	}
}

func TestBuildOpenAIChatMessagesGroupsToolCallsWithOutputs(t *testing.T) {
	ctx := llm.NewChatContext()
	groupID := "assistant-turn"
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: groupID, Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "checking"}}},
		&llm.FunctionCall{ID: groupID + "/tool-1", CallID: "call_lookup", Name: "lookup", Arguments: `{"city":"Paris"}`},
		&llm.FunctionCall{ID: groupID + "/tool-2", CallID: "call_weather", Name: "weather", Arguments: `{"city":"Paris"}`},
		&llm.FunctionCallOutput{ID: "lookup-output", CallID: "call_lookup", Name: "lookup", Output: "Paris"},
		&llm.FunctionCallOutput{ID: "weather-output", CallID: "call_weather", Name: "weather", Output: "sunny"},
	}

	messages := buildOpenAIChatMessages(ctx)

	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want 3: %#v", len(messages), messages)
	}
	if messages[0].Role != "assistant" || messages[0].Content != "checking" {
		t.Fatalf("assistant message = %#v", messages[0])
	}
	if len(messages[0].ToolCalls) != 2 {
		t.Fatalf("len(ToolCalls) = %d, want 2", len(messages[0].ToolCalls))
	}
	if messages[0].ToolCalls[0].ID != "call_lookup" || messages[0].ToolCalls[1].ID != "call_weather" {
		t.Fatalf("ToolCalls = %#v", messages[0].ToolCalls)
	}
	if messages[1].Role != "tool" || messages[1].ToolCallID != "call_lookup" || messages[1].Content != "Paris" {
		t.Fatalf("first tool output = %#v", messages[1])
	}
	if messages[2].Role != "tool" || messages[2].ToolCallID != "call_weather" || messages[2].Content != "sunny" {
		t.Fatalf("second tool output = %#v", messages[2])
	}
}

func TestBuildOpenAIChatMessagesFiltersUnmatchedToolItems(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
		&llm.FunctionCall{ID: "orphan-call", CallID: "call_missing_output", Name: "lookup", Arguments: `{}`},
		&llm.FunctionCallOutput{ID: "orphan-output", CallID: "call_missing_call", Name: "lookup", Output: "ignored"},
	}

	messages := buildOpenAIChatMessages(ctx)

	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1: %#v", len(messages), messages)
	}
	if messages[0].Role != "user" || messages[0].Content != "hello" {
		t.Fatalf("message = %#v, want user hello", messages[0])
	}
}
