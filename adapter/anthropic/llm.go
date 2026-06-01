package anthropic

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
	"net/url"
	"strings"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
)

type AnthropicLLM struct {
	apiKey string
	model  string
}

func NewAnthropicLLM(apiKey string, model string) (*AnthropicLLM, error) {
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	return &AnthropicLLM{
		apiKey: apiKey,
		model:  model,
	}, nil
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicContentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	Source    map[string]any `json:"source,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   any            `json:"content,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
}

func (l *AnthropicLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	options := &llm.ChatOptions{}
	for _, opt := range opts {
		opt(options)
	}

	connectOptions, err := options.EffectiveConnectOptions()
	if err != nil {
		return nil, err
	}
	var cancel context.CancelFunc
	if connectOptions.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, connectOptions.Timeout)
	}

	messages, system := buildAnthropicMessages(chatCtx)

	body := map[string]interface{}{
		"model":      l.model,
		"messages":   messages,
		"max_tokens": 1024,
		"stream":     true,
	}
	if system != "" {
		body["system"] = system
	}
	applyAnthropicExtraParams(body, options.ExtraParams)

	// Tool support
	if len(options.Tools) > 0 {
		tools := make([]map[string]interface{}, 0)
		for _, tool := range options.Tools {
			if tool.Name() == "computer_use" {
				tools = append(tools, map[string]interface{}{
					"type":              "computer_20241022",
					"name":              "computer",
					"display_width_px":  1280,
					"display_height_px": 720,
					"display_number":    1,
				})
			} else {
				tools = append(tools, map[string]interface{}{
					"name":         tool.Name(),
					"description":  tool.Description(),
					"input_schema": tool.Parameters(),
					"strict":       true,
				})
			}
		}
		body["tools"] = tools
	}
	if toolChoice := buildAnthropicToolChoice(options.ToolChoice, options.ParallelToolCalls); toolChoice != nil {
		body["tool_choice"] = toolChoice
	}

	jsonBody, _ := json.Marshal(body)
	var lastErr error
	for attempt := 0; attempt <= connectOptions.MaxRetry; attempt++ {
		resp, err := l.startAnthropicStream(ctx, jsonBody)
		if err == nil {
			return &anthropicStream{
				resp:   resp,
				reader: bufio.NewReader(resp.Body),
				cancel: cancel,
			}, nil
		}
		lastErr = err
		if attempt == connectOptions.MaxRetry || !anthropicShouldRetryError(lastErr) {
			if cancel != nil {
				cancel()
			}
			return nil, lastErr
		}
		if err := waitAnthropicRetryInterval(ctx, connectOptions.IntervalForRetry(attempt)); err != nil {
			if cancel != nil {
				cancel()
			}
			return nil, err
		}
	}

	if cancel != nil {
		cancel()
	}
	return nil, lastErr
}

func (l *AnthropicLLM) startAnthropicStream(ctx context.Context, jsonBody []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", l.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, llm.NewAPITimeoutError("")
		}
		return nil, llm.NewAPIConnectionError(anthropicConnectionErrorMessage(err))
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		body := strings.TrimSpace(string(respBody))
		return nil, llm.CreateAPIErrorFromHTTP(body, resp.StatusCode, anthropicRequestID(resp.Header), body)
	}
	return resp, nil
}

func anthropicConnectionErrorMessage(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err.Error()
	}
	return err.Error()
}

func anthropicShouldRetryError(err error) bool {
	var apiErr *llm.APIError
	return errors.As(err, &apiErr) && apiErr.Retryable
}

func waitAnthropicRetryInterval(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return nil
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func applyAnthropicExtraParams(body map[string]any, params map[string]any) {
	for key, value := range params {
		switch key {
		case "user", "temperature", "top_k", "max_tokens":
			body[key] = value
		}
	}
}

func anthropicRequestID(header http.Header) string {
	if requestID := header.Get("request-id"); requestID != "" {
		return requestID
	}
	for key, values := range header {
		if strings.EqualFold(key, "request-id") && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func buildAnthropicToolChoice(choice llm.ToolChoice, parallelToolCalls bool) map[string]any {
	var toolChoice map[string]any
	switch tc := choice.(type) {
	case string:
		switch tc {
		case "auto":
			toolChoice = map[string]any{"type": "auto"}
		case "required":
			toolChoice = map[string]any{"type": "any"}
		case "none":
			return nil
		}
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
		toolChoice = map[string]any{"type": "tool", "name": name}
	}
	if toolChoice == nil {
		return nil
	}
	toolChoice["disable_parallel_tool_use"] = !parallelToolCalls
	return toolChoice
}

type anthropicStream struct {
	resp   *http.Response
	reader *bufio.Reader
	cancel context.CancelFunc

	// internal states for tracking tool calls over multiple chunks
	toolCallID string
	toolName   string
	toolArgs   string
}

func buildAnthropicMessages(chatCtx *llm.ChatContext) ([]anthropicMessage, string) {
	messages := make([]anthropicMessage, 0, len(chatCtx.Items))
	systemMessages := make([]string, 0)
	var currentRole string
	content := make([]anthropicContentBlock, 0)

	flush := func() {
		if currentRole == "" || len(content) == 0 {
			return
		}
		messages = append(messages, anthropicMessage{
			Role:    currentRole,
			Content: content,
		})
		content = nil
	}

	appendBlocks := func(role string, blocks ...anthropicContentBlock) {
		if currentRole == "" || currentRole != role {
			flush()
			currentRole = role
			content = make([]anthropicContentBlock, 0, len(blocks))
		}
		content = append(content, blocks...)
	}

	for _, group := range groupAnthropicChatItems(chatCtx.Items) {
		for _, item := range group.flatten() {
			switch msg := item.(type) {
			case *llm.ChatMessage:
				if msg.Role == llm.ChatRoleSystem || msg.Role == llm.ChatRoleDeveloper {
					if text := msg.TextContent(); text != "" {
						systemMessages = append(systemMessages, text)
					}
					continue
				}
				role := "user"
				if msg.Role == llm.ChatRoleAssistant {
					role = "assistant"
				}
				blocks := anthropicMessageContentBlocks(msg)
				if len(blocks) > 0 {
					appendBlocks(role, blocks...)
				}
			case *llm.FunctionCall:
				appendBlocks("assistant", anthropicToolUseBlock(msg))
			case *llm.FunctionCallOutput:
				appendBlocks("user", anthropicToolResultBlock(msg))
			}
		}
	}
	flush()

	if len(messages) == 0 || messages[0].Role != "user" {
		messages = append([]anthropicMessage{
			{
				Role: "user",
				Content: []anthropicContentBlock{
					{Type: "text", Text: "(empty)"},
				},
			},
		}, messages...)
	}

	return messages, strings.Join(systemMessages, "\n")
}

func anthropicMessageContentBlocks(msg *llm.ChatMessage) []anthropicContentBlock {
	blocks := make([]anthropicContentBlock, 0, len(msg.Content))
	for _, c := range msg.Content {
		if c.Text != "" {
			blocks = append(blocks, anthropicContentBlock{Type: "text", Text: c.Text})
		}
		if c.Image != nil {
			if block := anthropicImageBlock(c.Image); block != nil {
				blocks = append(blocks, *block)
			}
		}
	}
	return blocks
}

func anthropicImageBlock(image *llm.ImageContent) *anthropicContentBlock {
	img, err := llm.SerializeImage(image)
	if err != nil {
		return nil
	}
	if img.ExternalURL != "" {
		return &anthropicContentBlock{
			Type: "image",
			Source: map[string]any{
				"type": "url",
				"url":  img.ExternalURL,
			},
		}
	}
	return &anthropicContentBlock{
		Type: "image",
		Source: map[string]any{
			"type":       "base64",
			"data":       base64.StdEncoding.EncodeToString(img.DataBytes),
			"media_type": img.MIMEType,
		},
	}
}

func anthropicToolUseBlock(fc *llm.FunctionCall) anthropicContentBlock {
	input := make(map[string]any)
	_ = json.Unmarshal([]byte(fc.Arguments), &input)
	return anthropicContentBlock{
		Type:  "tool_use",
		ID:    fc.CallID,
		Name:  fc.Name,
		Input: input,
	}
}

func anthropicToolResultBlock(fco *llm.FunctionCallOutput) anthropicContentBlock {
	return anthropicContentBlock{
		Type:      "tool_result",
		ToolUseID: fco.CallID,
		Content:   fco.Output,
		IsError:   fco.IsError,
	}
}

type anthropicChatItemGroup struct {
	message     *llm.ChatMessage
	toolCalls   []*llm.FunctionCall
	toolOutputs []*llm.FunctionCallOutput
}

func groupAnthropicChatItems(items []llm.ChatItem) []*anthropicChatItemGroup {
	groups := make([]*anthropicChatItemGroup, 0)
	groupsByID := make(map[string]*anthropicChatItemGroup)
	toolOutputs := make([]*llm.FunctionCallOutput, 0)

	addToGroup := func(groupID string, item llm.ChatItem) {
		group := groupsByID[groupID]
		if group == nil {
			group = &anthropicChatItemGroup{}
			groupsByID[groupID] = group
			groups = append(groups, group)
		}
		group.add(item)
	}

	for _, item := range items {
		switch it := item.(type) {
		case *llm.ChatMessage:
			if it.Role == llm.ChatRoleAssistant {
				addToGroup(anthropicGroupID(it.ID, nil), it)
			} else {
				addToGroup(it.ID, it)
			}
		case *llm.FunctionCall:
			addToGroup(anthropicGroupID(it.ID, it.GroupID), it)
		case *llm.FunctionCallOutput:
			toolOutputs = append(toolOutputs, it)
		}
	}

	groupsByCallID := make(map[string]*anthropicChatItemGroup)
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

func (g *anthropicChatItemGroup) add(item llm.ChatItem) {
	switch it := item.(type) {
	case *llm.ChatMessage:
		g.message = it
	case *llm.FunctionCall:
		g.toolCalls = append(g.toolCalls, it)
	case *llm.FunctionCallOutput:
		g.toolOutputs = append(g.toolOutputs, it)
	}
}

func (g *anthropicChatItemGroup) flatten() []llm.ChatItem {
	items := make([]llm.ChatItem, 0, 1+len(g.toolCalls)+len(g.toolOutputs))
	if g.message != nil {
		items = append(items, g.message)
	}
	for _, toolCall := range g.toolCalls {
		items = append(items, toolCall)
	}
	for _, toolOutput := range g.toolOutputs {
		items = append(items, toolOutput)
	}
	return items
}

func (g *anthropicChatItemGroup) removeInvalidToolItems() {
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

func anthropicGroupID(itemID string, groupID *string) string {
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

func (s *anthropicStream) Next() (*llm.ChatChunk, error) {
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil, io.EOF
			}
			return nil, err
		}

		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		var event struct {
			Type string `json:"type"`

			// message_start fields
			Message struct {
				ID    string `json:"id"`
				Usage struct {
					InputTokens              int `json:"input_tokens"`
					OutputTokens             int `json:"output_tokens"`
					CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
					CacheReadInputTokens     int `json:"cache_read_input_tokens"`
				} `json:"usage"`
			} `json:"message"`

			// message_delta fields
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`

			// content_block_start fields
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`

			// content_block_delta fields
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJson string `json:"partial_json"`
			} `json:"delta"`
		}

		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		switch event.Type {
		case "message_start":
			return &llm.ChatChunk{
				ID: event.Message.ID,
				Usage: &llm.CompletionUsage{
					PromptTokens:        event.Message.Usage.InputTokens,
					CacheCreationTokens: event.Message.Usage.CacheCreationInputTokens,
					CacheReadTokens:     event.Message.Usage.CacheReadInputTokens,
				},
			}, nil

		case "content_block_start":
			if event.ContentBlock.Type == "tool_use" {
				s.toolCallID = event.ContentBlock.ID
				s.toolName = event.ContentBlock.Name
				s.toolArgs = ""
			}

		case "content_block_delta":
			if event.Delta.Type == "text_delta" {
				return &llm.ChatChunk{
					Delta: &llm.ChoiceDelta{
						Role:    llm.ChatRoleAssistant,
						Content: event.Delta.Text,
					},
				}, nil
			} else if event.Delta.Type == "input_json_delta" {
				s.toolArgs += event.Delta.PartialJson
			}

		case "content_block_stop":
			if s.toolCallID != "" {
				chunk := &llm.ChatChunk{
					Delta: &llm.ChoiceDelta{
						Role: llm.ChatRoleAssistant,
						ToolCalls: []llm.FunctionToolCall{
							{
								CallID:    s.toolCallID,
								Name:      s.toolName,
								Arguments: s.toolArgs,
								Type:      "function",
							},
						},
					},
				}
				s.toolCallID = ""
				s.toolName = ""
				s.toolArgs = ""
				return chunk, nil
			}

		case "message_delta":
			return &llm.ChatChunk{
				Usage: &llm.CompletionUsage{
					CompletionTokens: event.Usage.OutputTokens,
				},
			}, nil

		case "message_stop":
			return nil, io.EOF

		case "error":
			return nil, fmt.Errorf("anthropic stream error: %s", data)
		}
	}
}

func (s *anthropicStream) Close() error {
	err := s.resp.Body.Close()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	return err
}
