package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/cavos-io/conversation-worker/core/llm"
)

type captureRoundTripper struct {
	body map[string]any
}

func (rt *captureRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	defer req.Body.Close()
	if err := json.NewDecoder(req.Body).Decode(&rt.body); err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBuffer(nil)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

type anthropicRequestTestTool struct{}

func (anthropicRequestTestTool) ID() string          { return "lookup" }
func (anthropicRequestTestTool) Name() string        { return "lookup" }
func (anthropicRequestTestTool) Description() string { return "look up information" }
func (anthropicRequestTestTool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
		},
		"required": []string{"query"},
	}
}
func (anthropicRequestTestTool) Execute(context.Context, string) (string, error) {
	return "", nil
}

func TestBuildAnthropicMessagesGroupsToolCallsWithResults(t *testing.T) {
	ctx := llm.NewChatContext()
	groupID := "assistant-turn"
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: groupID, Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "checking"}}},
		&llm.FunctionCall{ID: groupID + "/tool-1", CallID: "call_lookup", Name: "lookup", Arguments: `{"city":"Paris"}`},
		&llm.FunctionCall{ID: groupID + "/tool-2", CallID: "call_weather", Name: "weather", Arguments: `{"city":"Paris"}`},
		&llm.FunctionCallOutput{ID: "lookup-output", CallID: "call_lookup", Name: "lookup", Output: "Paris"},
		&llm.FunctionCallOutput{ID: "weather-output", CallID: "call_weather", Name: "weather", Output: "sunny"},
	}

	messages, system := buildAnthropicMessages(ctx)

	if system != "" {
		t.Fatalf("system = %q, want empty", system)
	}
	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want 3: %#v", len(messages), messages)
	}
	if messages[0].Role != "user" {
		t.Fatalf("first role = %q, want user", messages[0].Role)
	}
	assertAnthropicTextBlock(t, messages[0].Content, 0, "(empty)")
	if messages[1].Role != "assistant" {
		t.Fatalf("second role = %q, want assistant", messages[1].Role)
	}
	assertAnthropicTextBlock(t, messages[1].Content, 0, "checking")
	assertAnthropicToolUseBlock(t, messages[1].Content, 1, "call_lookup", "lookup")
	assertAnthropicToolUseBlock(t, messages[1].Content, 2, "call_weather", "weather")
	if messages[2].Role != "user" {
		t.Fatalf("third role = %q, want user", messages[2].Role)
	}
	assertAnthropicToolResultBlock(t, messages[2].Content, 0, "call_lookup", "Paris", false)
	assertAnthropicToolResultBlock(t, messages[2].Content, 1, "call_weather", "sunny", false)
}

func TestBuildAnthropicMessagesFiltersUnmatchedToolItems(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
		&llm.FunctionCall{ID: "orphan-call", CallID: "call_missing_output", Name: "lookup", Arguments: `{}`},
		&llm.FunctionCallOutput{ID: "orphan-output", CallID: "call_missing_call", Name: "lookup", Output: "ignored"},
	}

	messages, _ := buildAnthropicMessages(ctx)

	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1: %#v", len(messages), messages)
	}
	if messages[0].Role != "user" {
		t.Fatalf("role = %q, want user", messages[0].Role)
	}
	assertAnthropicTextBlock(t, messages[0].Content, 0, "hello")
}

func TestAnthropicChatUsesStrictToolInputSchema(t *testing.T) {
	transport := &captureRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	stream, err := model.Chat(context.Background(), llm.NewChatContext(), llm.WithTools([]llm.Tool{anthropicRequestTestTool{}}))
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	_ = stream.Close()

	tools, ok := transport.body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one tool", transport.body["tools"])
	}
	tool, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("tool = %#v, want map", tools[0])
	}
	if tool["strict"] != true {
		t.Fatalf("tool strict = %#v, want true", tool["strict"])
	}
	inputSchema, ok := tool["input_schema"].(map[string]any)
	if !ok {
		t.Fatalf("input_schema = %#v, want map", tool["input_schema"])
	}
	if inputSchema["additionalProperties"] != false {
		t.Fatalf("input_schema additionalProperties = %#v, want false", inputSchema["additionalProperties"])
	}
	if inputSchema["type"] != "object" {
		t.Fatalf("input_schema type = %#v, want object", inputSchema["type"])
	}
}

func TestBuildAnthropicMessagesCollectsSystemText(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "system", Role: llm.ChatRoleSystem, Content: []llm.ChatContent{{Text: "base"}}},
		&llm.ChatMessage{ID: "developer", Role: llm.ChatRoleDeveloper, Content: []llm.ChatContent{{Text: "dev"}}},
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}

	messages, system := buildAnthropicMessages(ctx)

	if system != "base\ndev" {
		t.Fatalf("system = %q, want base/dev", system)
	}
	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1", len(messages))
	}
	assertAnthropicTextBlock(t, messages[0].Content, 0, "hello")
}

func assertAnthropicTextBlock(t *testing.T, blocks []anthropicContentBlock, index int, want string) {
	t.Helper()
	if len(blocks) <= index {
		t.Fatalf("len(blocks) = %d, want index %d", len(blocks), index)
	}
	if blocks[index].Type != "text" || blocks[index].Text != want {
		t.Fatalf("text block[%d] = %#v, want %q", index, blocks[index], want)
	}
}

func assertAnthropicToolUseBlock(t *testing.T, blocks []anthropicContentBlock, index int, wantID, wantName string) {
	t.Helper()
	if len(blocks) <= index {
		t.Fatalf("len(blocks) = %d, want index %d", len(blocks), index)
	}
	if blocks[index].Type != "tool_use" || blocks[index].ID != wantID || blocks[index].Name != wantName {
		t.Fatalf("tool use block[%d] = %#v", index, blocks[index])
	}
}

func assertAnthropicToolResultBlock(t *testing.T, blocks []anthropicContentBlock, index int, wantID, wantContent string, wantError bool) {
	t.Helper()
	if len(blocks) <= index {
		t.Fatalf("len(blocks) = %d, want index %d", len(blocks), index)
	}
	if blocks[index].Type != "tool_result" || blocks[index].ToolUseID != wantID || blocks[index].Content != wantContent || blocks[index].IsError != wantError {
		t.Fatalf("tool result block[%d] = %#v", index, blocks[index])
	}
}
