package anthropic

import (
	"bytes"
	"context"
	"encoding/base64"
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

func TestAnthropicChatMapsNamedToolChoice(t *testing.T) {
	transport := &captureRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	stream, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithTools([]llm.Tool{anthropicRequestTestTool{}}),
		llm.WithToolChoice(map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": "lookup",
			},
		}),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	_ = stream.Close()

	choice, ok := transport.body["tool_choice"].(map[string]any)
	if !ok {
		t.Fatalf("tool_choice = %#v, want map", transport.body["tool_choice"])
	}
	if choice["type"] != "tool" || choice["name"] != "lookup" {
		t.Fatalf("tool_choice = %#v, want named lookup tool", choice)
	}
}

func TestAnthropicDefaultsMatchReference(t *testing.T) {
	model, err := NewAnthropicLLM("test-key", "")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}

	if model.model != "claude-sonnet-4-6" {
		t.Fatalf("model = %q, want claude-sonnet-4-6", model.model)
	}
}

func TestAnthropicChatAppliesExtraParams(t *testing.T) {
	transport := &captureRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	stream, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithExtraParams(map[string]any{
			"user":        "participant-1",
			"temperature": 0.7,
			"top_k":       40,
			"max_tokens":  2048,
		}),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	_ = stream.Close()

	if transport.body["user"] != "participant-1" {
		t.Fatalf("user = %#v, want participant-1", transport.body["user"])
	}
	if transport.body["temperature"] != 0.7 {
		t.Fatalf("temperature = %#v, want 0.7", transport.body["temperature"])
	}
	if transport.body["top_k"] != float64(40) {
		t.Fatalf("top_k = %#v, want 40", transport.body["top_k"])
	}
	if transport.body["max_tokens"] != float64(2048) {
		t.Fatalf("max_tokens = %#v, want 2048", transport.body["max_tokens"])
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

func TestBuildAnthropicMessagesIncludesImageBlocks(t *testing.T) {
	imageData := base64.StdEncoding.EncodeToString([]byte("gif-bytes"))
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{
			ID:   "user",
			Role: llm.ChatRoleUser,
			Content: []llm.ChatContent{
				{Text: "describe"},
				{Image: &llm.ImageContent{Image: "data:image/gif;base64," + imageData}},
				{Image: &llm.ImageContent{Image: "https://example.test/image.png"}},
			},
		},
	}

	messages, _ := buildAnthropicMessages(ctx)

	blocks := messages[0].Content
	if len(blocks) != 3 {
		t.Fatalf("len(blocks) = %d, want 3: %#v", len(blocks), blocks)
	}
	inlineBlock := anthropicContentBlockToMap(t, blocks[1])
	if inlineBlock["type"] != "image" {
		t.Fatalf("inline image block = %#v", inlineBlock)
	}
	inlineSource := inlineBlock["source"].(map[string]any)
	if inlineSource["type"] != "base64" || inlineSource["data"] != imageData || inlineSource["media_type"] != "image/gif" {
		t.Fatalf("inline image source = %#v", inlineSource)
	}
	urlBlock := anthropicContentBlockToMap(t, blocks[2])
	if urlBlock["type"] != "image" {
		t.Fatalf("url image block = %#v", urlBlock)
	}
	urlSource := urlBlock["source"].(map[string]any)
	if urlSource["type"] != "url" || urlSource["url"] != "https://example.test/image.png" {
		t.Fatalf("url image source = %#v", urlSource)
	}
}

func anthropicContentBlockToMap(t *testing.T, block anthropicContentBlock) map[string]any {
	t.Helper()
	data, err := json.Marshal(block)
	if err != nil {
		t.Fatalf("marshal block: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal block: %v", err)
	}
	return out
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
