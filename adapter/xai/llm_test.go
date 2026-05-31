package xai

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/cavos-io/conversation-worker/core/llm"
)

func TestBuildXAIMessagesGroupsToolCallsWithOutputs(t *testing.T) {
	ctx := llm.NewChatContext()
	groupID := "assistant-turn"
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: groupID, Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "checking"}}},
		&llm.FunctionCall{ID: groupID + "/tool-1", CallID: "call_lookup", Name: "lookup", Arguments: `{"city":"Paris"}`},
		&llm.FunctionCall{ID: groupID + "/tool-2", CallID: "call_weather", Name: "weather", Arguments: `{"city":"Paris"}`},
		&llm.FunctionCallOutput{ID: "lookup-output", CallID: "call_lookup", Name: "lookup", Output: "Paris"},
		&llm.FunctionCallOutput{ID: "weather-output", CallID: "call_weather", Name: "weather", Output: "sunny"},
	}

	messages := buildXAIMessages(ctx)

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

func TestBuildXAIMessagesFiltersUnmatchedToolItems(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
		&llm.FunctionCall{ID: "orphan-call", CallID: "call_missing_output", Name: "lookup", Arguments: `{}`},
		&llm.FunctionCallOutput{ID: "orphan-output", CallID: "call_missing_call", Name: "lookup", Output: "ignored"},
	}

	messages := buildXAIMessages(ctx)

	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1: %#v", len(messages), messages)
	}
	if messages[0].Role != "user" || messages[0].Content != "hello" {
		t.Fatalf("message = %#v, want user hello", messages[0])
	}
}

func TestBuildXAIMessagesMapsDeveloperRoleToSystem(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "developer", Role: llm.ChatRoleDeveloper, Content: []llm.ChatContent{{Text: "instructions"}}},
	}

	messages := buildXAIMessages(ctx)

	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1", len(messages))
	}
	if messages[0].Role != "system" || messages[0].Content != "instructions" {
		t.Fatalf("message = %#v, want system instructions", messages[0])
	}
}

func TestBuildXAIMessagesIncludesImageContent(t *testing.T) {
	imageData := base64.StdEncoding.EncodeToString([]byte("png-bytes"))
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{
			ID:   "user",
			Role: llm.ChatRoleUser,
			Content: []llm.ChatContent{
				{Text: "describe"},
				{Image: &llm.ImageContent{Image: "data:image/png;base64," + imageData, InferenceDetail: "high"}},
			},
		},
	}

	messages := buildXAIMessages(ctx)

	content := xaiMessageContentAsList(t, messages[0])
	if len(content) != 2 {
		t.Fatalf("len(content) = %d, want 2: %#v", len(content), content)
	}
	if content[0]["type"] != "image_url" {
		t.Fatalf("image content = %#v", content[0])
	}
	imageURL := content[0]["image_url"].(map[string]any)
	if imageURL["url"] != "data:image/png;base64,"+imageData || imageURL["detail"] != "high" {
		t.Fatalf("image_url = %#v", imageURL)
	}
	if content[1]["type"] != "text" || content[1]["text"] != "describe" {
		t.Fatalf("text content = %#v", content[1])
	}
}

func xaiMessageContentAsList(t *testing.T, message xaiMessage) []map[string]any {
	t.Helper()
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}
	rawContent, ok := raw["content"].([]any)
	if !ok {
		t.Fatalf("content = %#v, want multipart content", raw["content"])
	}
	content := make([]map[string]any, 0, len(rawContent))
	for _, item := range rawContent {
		part, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("content item = %#v, want map", item)
		}
		content = append(content, part)
	}
	return content
}
