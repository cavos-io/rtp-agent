package xai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/cavos-io/rtp-agent/core/llm"
)

type XaiLLM struct {
	apiKey string
	model  string
}

func NewXaiLLM(apiKey string, model string) *XaiLLM {
	if model == "" {
		model = "grok-4-1-fast-non-reasoning"
	}
	return &XaiLLM{
		apiKey: resolveXaiLLMAPIKey(apiKey),
		model:  model,
	}
}

func resolveXaiLLMAPIKey(apiKey string) string {
	if apiKey != "" {
		return apiKey
	}
	return os.Getenv("XAI_API_KEY")
}

func (l *XaiLLM) Model() string {
	return l.model
}

type xaiMessage struct {
	Role       string        `json:"role"`
	Content    any           `json:"content"`
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
	if l.apiKey == "" {
		return nil, fmt.Errorf("xAI API key is required, either as argument or set XAI_API_KEY environmental variable")
	}

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
			if payload := xaiProviderToolPayload(tool); payload != nil {
				tools = append(tools, payload)
			} else {
				tools = append(tools, map[string]interface{}{
					"type": "function",
					"function": map[string]interface{}{
						"name":        tool.Name(),
						"description": tool.Description(),
						"parameters":  llm.ToolParameters(tool),
					},
				})
			}
		}
		body["tools"] = tools
	}
	if toolChoice := xaiToolChoicePayload(options.ToolChoice); toolChoice != nil {
		body["tool_choice"] = toolChoice
	}
	if options.ParallelToolCallsSet {
		body["parallel_tool_calls"] = options.ParallelToolCalls
	}
	for key, value := range options.ExtraParams {
		body[key] = value
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
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, llm.NewAPITimeoutError(err.Error())
		}
		return nil, llm.NewAPIConnectionError(err.Error())
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, llm.NewAPIStatusError("xAI LLM request failed", resp.StatusCode, "", string(respBody))
	}

	return &xaiStream{
		resp: resp,
	}, nil
}

func xaiProviderToolPayload(tool llm.Tool) map[string]interface{} {
	switch t := tool.(type) {
	case *WebSearchTool:
		return map[string]interface{}{"type": "web_search"}
	case *XSearchTool:
		payload := map[string]interface{}{"type": "x_search"}
		if len(t.AllowedHandles) > 0 {
			payload["allowed_x_handles"] = append([]string(nil), t.AllowedHandles...)
		}
		return payload
	case *FileSearchTool:
		payload := map[string]interface{}{
			"type":             "file_search",
			"vector_store_ids": append([]string(nil), t.VectorStoreIDs...),
		}
		if t.MaxNumResults > 0 {
			payload["max_num_results"] = t.MaxNumResults
		}
		return payload
	default:
		return nil
	}
}

func xaiToolChoicePayload(choice llm.ToolChoice) any {
	switch tc := choice.(type) {
	case string:
		if tc == "" {
			return nil
		}
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
			"function": map[string]any{
				"name": name,
			},
		}
	default:
		return nil
	}
}

type xaiStream struct {
	resp     *http.Response
	scanner  *bufio.Scanner
	thinking bool
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
		Content: xaiMessageContent(msg),
	}
}

func xaiMessageContent(msg *llm.ChatMessage) any {
	parts := make([]map[string]any, 0)
	textContent := ""
	for _, item := range msg.Content {
		if text := item.Text; text != "" {
			if textContent != "" {
				textContent += "\n"
			}
			textContent += text
		}
		if item.Image != nil {
			if part := xaiImageContent(item.Image); part != nil {
				parts = append(parts, part)
			}
		}
	}
	if len(parts) == 0 {
		return msg.TextContent()
	}
	if textContent != "" {
		parts = append(parts, map[string]any{
			"type": "text",
			"text": textContent,
		})
	}
	return parts
}

func xaiImageContent(image *llm.ImageContent) map[string]any {
	img, err := llm.SerializeImage(image)
	if err != nil {
		return nil
	}
	url := img.ExternalURL
	if url == "" {
		url = fmt.Sprintf("data:%s;base64,%s", img.MIMEType, base64.StdEncoding.EncodeToString(img.DataBytes))
	}
	return map[string]any{
		"type": "image_url",
		"image_url": map[string]any{
			"url":    url,
			"detail": img.InferenceDetail,
		},
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
					Role      string        `json:"role"`
					Content   string        `json:"content"`
					ToolCalls []xaiToolCall `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}

		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil, err
		}

		if len(chunk.Choices) > 0 {
			content, ok := llm.StripThinkingTokens(chunk.Choices[0].Delta.Content, &s.thinking)
			if !ok {
				continue
			}
			return &llm.ChatChunk{
				ID: chunk.ID,
				Delta: &llm.ChoiceDelta{
					Role:      llm.ChatRole(chunk.Choices[0].Delta.Role),
					Content:   content,
					ToolCalls: xaiFunctionToolCalls(chunk.Choices[0].Delta.ToolCalls),
				},
			}, nil
		}
	}

	if err := s.scanner.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func xaiFunctionToolCalls(toolCalls []xaiToolCall) []llm.FunctionToolCall {
	if len(toolCalls) == 0 {
		return nil
	}
	out := make([]llm.FunctionToolCall, 0, len(toolCalls))
	for _, tc := range toolCalls {
		out = append(out, llm.FunctionToolCall{
			Type:      tc.Type,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
			CallID:    tc.ID,
		})
	}
	return out
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
