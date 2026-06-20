package xai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
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

func TestNewXaiLLMDefaultsToReferenceModel(t *testing.T) {
	provider := NewXaiLLM("test-key", "")

	if provider.Model() != "grok-4-1-fast-non-reasoning" {
		t.Fatalf("model = %q, want reference default model", provider.Model())
	}
}

func TestNewXaiLLMUsesCustomModel(t *testing.T) {
	provider := NewXaiLLM("test-key", "grok-4")

	if provider.Model() != "grok-4" {
		t.Fatalf("model = %q, want custom model", provider.Model())
	}
}

func TestNewXaiLLMUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("XAI_API_KEY", "env-key")

	if got := resolveXaiLLMAPIKey(""); got != "env-key" {
		t.Fatalf("resolved API key = %q, want env key", got)
	}
	if got := resolveXaiLLMAPIKey("explicit-key"); got != "explicit-key" {
		t.Fatalf("resolved API key = %q, want explicit key", got)
	}
}

func TestXaiLLMRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("XAI_API_KEY", "")
	provider := NewXaiLLM("", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	chatCtx := llm.NewChatContext()
	chatCtx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}

	_, err := provider.Chat(ctx, chatCtx)
	if err == nil {
		t.Fatal("Chat returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "XAI_API_KEY") {
		t.Fatalf("Chat error = %q, want XAI_API_KEY guidance", err)
	}
}

func TestXaiLLMChatReturnsAPIStatusError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: xaiRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewXaiLLM("test-key", "")

	_, err := provider.Chat(context.Background(), xaiTestChatContext())
	if err == nil {
		t.Fatal("Chat error = nil, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Chat error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status code = %d, want 429", statusErr.StatusCode)
	}
	if statusErr.Body != `{"error":"rate limited"}` {
		t.Fatalf("body = %#v, want provider response body", statusErr.Body)
	}
}

func TestXaiLLMChatReturnsAPITimeoutError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: xaiRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewXaiLLM("test-key", "")

	_, err := provider.Chat(context.Background(), xaiTestChatContext())
	if err == nil {
		t.Fatal("Chat error = nil, want APITimeoutError")
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Chat error = %T %v, want APITimeoutError", err, err)
	}
}

func TestXaiLLMChatReturnsAPIConnectionError(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: xaiRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("dial refused")
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewXaiLLM("test-key", "")

	_, err := provider.Chat(context.Background(), xaiTestChatContext())
	if err == nil {
		t.Fatal("Chat error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Chat error = %T %v, want APIConnectionError", err, err)
	}
	var timeoutErr *llm.APITimeoutError
	if errors.As(err, &timeoutErr) {
		t.Fatalf("Chat error = %T %v, want APIConnectionError but not APITimeoutError", err, err)
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

func TestXaiLLMChatMapsReferenceProviderToolOptions(t *testing.T) {
	var body map[string]any
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: xaiRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: [DONE]\n")),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewXaiLLM("test-key", "")
	stream, err := provider.Chat(context.Background(), xaiTestChatContext(),
		llm.WithTools([]llm.Tool{
			&WebSearchTool{},
			&XSearchTool{AllowedHandles: []string{"livekit", "xai"}},
			&FileSearchTool{VectorStoreIDs: []string{"vs_1", "vs_2"}, MaxNumResults: 3},
		}),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	defer stream.Close()

	tools, ok := body["tools"].([]any)
	if !ok {
		t.Fatalf("tools = %#v, want list", body["tools"])
	}
	if len(tools) != 3 {
		t.Fatalf("len(tools) = %d, want 3: %#v", len(tools), tools)
	}
	webSearch := tools[0].(map[string]any)
	if webSearch["type"] != "web_search" {
		t.Fatalf("web search tool = %#v", webSearch)
	}
	xSearch := tools[1].(map[string]any)
	if xSearch["type"] != "x_search" {
		t.Fatalf("x search tool type = %#v", xSearch)
	}
	handles, ok := xSearch["allowed_x_handles"].([]any)
	if !ok || len(handles) != 2 || handles[0] != "livekit" || handles[1] != "xai" {
		t.Fatalf("allowed_x_handles = %#v, want reference handles", xSearch["allowed_x_handles"])
	}
	fileSearch := tools[2].(map[string]any)
	if fileSearch["type"] != "file_search" {
		t.Fatalf("file search tool type = %#v", fileSearch)
	}
	vectorStores, ok := fileSearch["vector_store_ids"].([]any)
	if !ok || len(vectorStores) != 2 || vectorStores[0] != "vs_1" || vectorStores[1] != "vs_2" {
		t.Fatalf("vector_store_ids = %#v, want reference vector stores", fileSearch["vector_store_ids"])
	}
	if fileSearch["max_num_results"] != float64(3) {
		t.Fatalf("max_num_results = %#v, want 3", fileSearch["max_num_results"])
	}
}

func TestXaiLLMChatMapsReferenceToolChoice(t *testing.T) {
	var body map[string]any
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: xaiRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: [DONE]\n")),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewXaiLLM("test-key", "")
	stream, err := provider.Chat(context.Background(), xaiTestChatContext(),
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
	defer stream.Close()

	choice, ok := body["tool_choice"].(map[string]any)
	if !ok {
		t.Fatalf("tool_choice = %#v, want named function choice", body["tool_choice"])
	}
	if choice["type"] != "function" {
		t.Fatalf("tool_choice type = %#v, want function", choice["type"])
	}
	function, ok := choice["function"].(map[string]any)
	if !ok || function["name"] != "lookup" {
		t.Fatalf("tool_choice function = %#v, want lookup", choice["function"])
	}
}

func TestXaiLLMChatMapsReferenceParallelToolCalls(t *testing.T) {
	var body map[string]any
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: xaiRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: [DONE]\n")),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewXaiLLM("test-key", "")
	stream, err := provider.Chat(context.Background(), xaiTestChatContext(),
		llm.WithParallelToolCalls(false),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	defer stream.Close()

	if got, ok := body["parallel_tool_calls"].(bool); !ok || got {
		t.Fatalf("parallel_tool_calls = %#v, want explicit false", body["parallel_tool_calls"])
	}
}

func TestXaiLLMChatForwardsReferenceExtraParams(t *testing.T) {
	var body map[string]any
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: xaiRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: [DONE]\n")),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewXaiLLM("test-key", "")
	stream, err := provider.Chat(context.Background(), xaiTestChatContext(),
		llm.WithExtraParams(map[string]any{
			"max_output_tokens": 128,
			"reasoning": map[string]any{
				"effort": "low",
			},
		}),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	defer stream.Close()

	if body["max_output_tokens"] != float64(128) {
		t.Fatalf("max_output_tokens = %#v, want 128", body["max_output_tokens"])
	}
	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "low" {
		t.Fatalf("reasoning = %#v, want low effort", body["reasoning"])
	}
}

func TestXaiLLMChatMapsReferenceResponseFormat(t *testing.T) {
	var body map[string]any
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: xaiRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: [DONE]\n")),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewXaiLLM("test-key", "")
	stream, err := provider.Chat(context.Background(), xaiTestChatContext(),
		llm.WithResponseFormat(map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "WeatherAnswer",
				"strict": true,
				"schema": map[string]any{
					"type": "object",
				},
			},
		}),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	defer stream.Close()

	format, ok := body["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format = %#v, want map", body["response_format"])
	}
	if format["type"] != "json_schema" {
		t.Fatalf("response_format type = %#v, want json_schema", format["type"])
	}
	schema, ok := format["json_schema"].(map[string]any)
	if !ok || schema["name"] != "WeatherAnswer" || schema["strict"] != true {
		t.Fatalf("response_format json_schema = %#v, want strict WeatherAnswer", format["json_schema"])
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

func TestXAIStreamStripsThinkingChunks(t *testing.T) {
	stream := &xaiStream{resp: &http.Response{
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"id":"chat","choices":[{"delta":{"role":"assistant","content":"<think>"}}]}`,
			`data: {"id":"chat","choices":[{"delta":{"role":"assistant","content":"hidden reasoning"}}]}`,
			`data: {"id":"chat","choices":[{"delta":{"role":"assistant","content":"</think>visible"}}]}`,
			`data: [DONE]`,
		}, "\n"))),
	}}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if chunk.Delta.Content != "" {
		t.Fatalf("first chunk content = %q, want empty", chunk.Delta.Content)
	}

	chunk, err = stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if chunk.Delta.Content != "visible" {
		t.Fatalf("second visible content = %q, want visible", chunk.Delta.Content)
	}
}

func TestXAIStreamMapsToolCallDeltas(t *testing.T) {
	stream := &xaiStream{resp: &http.Response{
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"id":"chat","choices":[{"delta":{"role":"assistant","tool_calls":[{"id":"call_lookup","type":"function","function":{"name":"lookup","arguments":"{\"city\":\"Paris\"}"}}]}}]}`,
			`data: [DONE]`,
		}, "\n"))),
	}}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if chunk.Delta == nil {
		t.Fatal("Delta = nil, want tool-call delta")
	}
	if len(chunk.Delta.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(chunk.Delta.ToolCalls))
	}
	toolCall := chunk.Delta.ToolCalls[0]
	if toolCall.Type != "function" || toolCall.CallID != "call_lookup" || toolCall.Name != "lookup" || toolCall.Arguments != `{"city":"Paris"}` {
		t.Fatalf("tool call = %#v, want lookup call delta", toolCall)
	}
}

func TestXAIStreamHandlesLargeReferenceDeltas(t *testing.T) {
	largeDelta := strings.Repeat("a", 70*1024)
	payload, err := json.Marshal(map[string]any{
		"id": "chat",
		"choices": []map[string]any{
			{
				"delta": map[string]any{
					"role":    "assistant",
					"content": largeDelta,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal chunk: %v", err)
	}
	stream := &xaiStream{resp: &http.Response{
		Body: io.NopCloser(strings.NewReader("data: " + string(payload) + "\n" + "data: [DONE]\n")),
	}}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v, want large delta chunk", err)
	}
	if chunk.Delta == nil || chunk.Delta.Content != largeDelta {
		t.Fatalf("large delta length = %d, want %d", len(chunk.Delta.Content), len(largeDelta))
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

func xaiTestChatContext() *llm.ChatContext {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}
	return ctx
}
