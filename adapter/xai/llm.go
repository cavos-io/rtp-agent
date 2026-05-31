package xai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cavos-io/conversation-worker/core/llm"
)

type XaiLLM struct {
	apiKey string
	model  string
}

func NewXaiLLM(apiKey string, model string) *XaiLLM {
	if model == "" {
		model = "grok-2-latest"
	}
	return &XaiLLM{
		apiKey: apiKey,
		model:  model,
	}
}

type xaiMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content"`
	ToolCalls  []xaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type xaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (l *XaiLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	options := &llm.ChatOptions{
		ParallelToolCalls: true,
	}
	for _, opt := range opts {
		opt(options)
	}

	messages := buildXAIMessages(chatCtx)

	body := map[string]interface{}{
		"model":    l.model,
		"messages": messages,
		"stream":   true,
	}

	if len(options.Tools) > 0 {
		tools := make([]map[string]interface{}, 0)
		for _, tool := range options.Tools {
			if tool.Name() == "xai_web_search" {
				tools = append(tools, map[string]interface{}{
					"type": "web_search",
				})
			} else if tool.Name() == "xai_x_search" {
				tools = append(tools, map[string]interface{}{
					"type": "x_search",
				}) // Expand allowed_x_handles if needed via parameters later
			} else if tool.Name() == "xai_file_search" {
				tools = append(tools, map[string]interface{}{
					"type": "file_search",
				}) // Expand vector_store_ids if needed
			} else {
				tools = append(tools, map[string]interface{}{
					"type": "function",
					"function": map[string]interface{}{
						"name":        tool.Name(),
						"description": tool.Description(),
						"parameters":  tool.Parameters(),
					},
				})
			}
		}
		body["tools"] = tools
	}

	jsonBody, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.x.ai/v1/chat/completions", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+l.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("xai error: %s", string(respBody))
	}

	return &xaiStream{
		resp: resp,
	}, nil
}

type xaiStream struct {
	resp    *http.Response
	scanner *bufio.Scanner
}

func buildXAIMessages(chatCtx *llm.ChatContext) []xaiMessage {
	messages := make([]xaiMessage, 0, len(chatCtx.Items))
	for _, group := range groupXAIChatItems(chatCtx.Items) {
		if group.message == nil && len(group.toolCalls) == 0 && len(group.toolOutputs) == 0 {
			continue
		}

		var msg xaiMessage
		if group.message != nil {
			msg = buildXAIChatMessage(group.message)
		} else {
			msg = xaiMessage{Role: "assistant"}
		}
		for _, toolCall := range group.toolCalls {
			msg.ToolCalls = append(msg.ToolCalls, buildXAIToolCall(toolCall))
		}
		messages = append(messages, msg)

		for _, toolOutput := range group.toolOutputs {
			messages = append(messages, buildXAIToolOutput(toolOutput))
		}
	}
	return messages
}

func buildXAIChatMessage(msg *llm.ChatMessage) xaiMessage {
	role := string(msg.Role)
	if role == "developer" {
		role = "system"
	}
	return xaiMessage{
		Role:    role,
		Content: msg.TextContent(),
	}
}

type xaiChatItemGroup struct {
	message     *llm.ChatMessage
	toolCalls   []*llm.FunctionCall
	toolOutputs []*llm.FunctionCallOutput
}

func groupXAIChatItems(items []llm.ChatItem) []*xaiChatItemGroup {
	groups := make([]*xaiChatItemGroup, 0)
	groupsByID := make(map[string]*xaiChatItemGroup)
	toolOutputs := make([]*llm.FunctionCallOutput, 0)

	addToGroup := func(groupID string, item llm.ChatItem) {
		group := groupsByID[groupID]
		if group == nil {
			group = &xaiChatItemGroup{}
			groupsByID[groupID] = group
			groups = append(groups, group)
		}
		group.add(item)
	}

	for _, item := range items {
		switch it := item.(type) {
		case *llm.ChatMessage:
			if it.Role == llm.ChatRoleAssistant {
				addToGroup(xaiGroupID(it.ID, nil), it)
			} else {
				addToGroup(it.ID, it)
			}
		case *llm.FunctionCall:
			addToGroup(xaiGroupID(it.ID, it.GroupID), it)
		case *llm.FunctionCallOutput:
			toolOutputs = append(toolOutputs, it)
		}
	}

	groupsByCallID := make(map[string]*xaiChatItemGroup)
	for _, group := range groups {
		for _, toolCall := range group.toolCalls {
			groupsByCallID[toolCall.CallID] = group
		}
	}
	for _, toolOutput := range toolOutputs {
		if group := groupsByCallID[toolOutput.CallID]; group != nil {
			group.add(toolOutput)
		}
	}
	for _, group := range groups {
		group.removeInvalidToolItems()
	}
	return groups
}

func (g *xaiChatItemGroup) add(item llm.ChatItem) {
	switch it := item.(type) {
	case *llm.ChatMessage:
		g.message = it
	case *llm.FunctionCall:
		g.toolCalls = append(g.toolCalls, it)
	case *llm.FunctionCallOutput:
		g.toolOutputs = append(g.toolOutputs, it)
	}
}

func (g *xaiChatItemGroup) removeInvalidToolItems() {
	if len(g.toolCalls) == len(g.toolOutputs) {
		return
	}

	outputsByCallID := make(map[string]*llm.FunctionCallOutput)
	for _, toolOutput := range g.toolOutputs {
		outputsByCallID[toolOutput.CallID] = toolOutput
	}

	validCalls := make([]*llm.FunctionCall, 0, len(g.toolCalls))
	validOutputs := make([]*llm.FunctionCallOutput, 0, len(g.toolOutputs))
	for _, toolCall := range g.toolCalls {
		if toolOutput := outputsByCallID[toolCall.CallID]; toolOutput != nil {
			validCalls = append(validCalls, toolCall)
			validOutputs = append(validOutputs, toolOutput)
		}
	}

	g.toolCalls = validCalls
	g.toolOutputs = validOutputs
}

func xaiGroupID(itemID string, groupID *string) string {
	if groupID != nil && *groupID != "" {
		return *groupID
	}
	for i, r := range itemID {
		if r == '/' {
			return itemID[:i]
		}
	}
	return itemID
}

func buildXAIToolCall(toolCall *llm.FunctionCall) xaiToolCall {
	xaiCall := xaiToolCall{
		ID:   toolCall.CallID,
		Type: "function",
	}
	xaiCall.Function.Name = toolCall.Name
	xaiCall.Function.Arguments = toolCall.Arguments
	return xaiCall
}

func buildXAIToolOutput(toolOutput *llm.FunctionCallOutput) xaiMessage {
	return xaiMessage{
		Role:       "tool",
		Content:    toolOutput.Output,
		ToolCallID: toolOutput.CallID,
	}
}

func (s *xaiStream) Next() (*llm.ChatChunk, error) {
	if s.scanner == nil {
		s.scanner = bufio.NewScanner(s.resp.Body)
	}

	for s.scanner.Scan() {
		line := s.scanner.Text()
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			return nil, io.EOF
		}

		var chunk struct {
			ID      string `json:"id"`
			Choices []struct {
				Delta struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}

		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil, err
		}

		if len(chunk.Choices) > 0 {
			return &llm.ChatChunk{
				ID: chunk.ID,
				Delta: &llm.ChoiceDelta{
					Role:    llm.ChatRole(chunk.Choices[0].Delta.Role),
					Content: chunk.Choices[0].Delta.Content,
				},
			}, nil
		}
	}

	if err := s.scanner.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func (s *xaiStream) Close() error {
	return s.resp.Body.Close()
}

// XAITool implementations
type WebSearchTool struct{}

func (t *WebSearchTool) ID() string   { return "xai_web_search" }
func (t *WebSearchTool) Name() string { return "xai_web_search" }
func (t *WebSearchTool) Description() string {
	return "Enable web search tool for real-time internet searches."
}
func (t *WebSearchTool) Parameters() map[string]any { return nil }
func (t *WebSearchTool) Execute(ctx context.Context, args string) (string, error) {
	return "dispatched", nil
}

type XSearchTool struct{ AllowedHandles []string }

func (t *XSearchTool) ID() string   { return "xai_x_search" }
func (t *XSearchTool) Name() string { return "xai_x_search" }
func (t *XSearchTool) Description() string {
	return "Enable X (Twitter) search tool for searching posts."
}
func (t *XSearchTool) Parameters() map[string]any { return nil }
func (t *XSearchTool) Execute(ctx context.Context, args string) (string, error) {
	return "dispatched", nil
}

type FileSearchTool struct {
	VectorStoreIDs []string
	MaxNumResults  int
}

func (t *FileSearchTool) ID() string   { return "xai_file_search" }
func (t *FileSearchTool) Name() string { return "xai_file_search" }
func (t *FileSearchTool) Description() string {
	return "Enable file search tool for searching uploaded document collections."
}
func (t *FileSearchTool) Parameters() map[string]any { return nil }
func (t *FileSearchTool) Execute(ctx context.Context, args string) (string, error) {
	return "dispatched", nil
}
