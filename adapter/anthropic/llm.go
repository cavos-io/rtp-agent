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
	"os"
	"strings"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
)

type AnthropicLLM struct {
	apiKey  string
	model   string
	baseURL string
}

type anthropicToolSpecProvider interface {
	AnthropicToolSpec() map[string]interface{}
}

type anthropicBetaToolProvider interface {
	AnthropicBetaFlag() string
}

const (
	anthropicAPIKeyEnv   = "ANTHROPIC_API_KEY"
	defaultAnthropicURL  = "https://api.anthropic.com"
	defaultAnthropicMode = "claude-sonnet-4-6"
)

var anthropicNoPrefillModelPrefixes = []string{
	"claude-sonnet-4-6",
	"claude-opus-4-6",
}

type AnthropicOption func(*AnthropicLLM)

func WithAnthropicBaseURL(baseURL string) AnthropicOption {
	return func(l *AnthropicLLM) {
		if baseURL != "" {
			l.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func NewAnthropicLLM(apiKey string, model string, opts ...AnthropicOption) (*AnthropicLLM, error) {
	if model == "" {
		model = defaultAnthropicMode
	}
	if apiKey == "" {
		apiKey = os.Getenv(anthropicAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, errors.New("anthropic API key is required, either as argument or set ANTHROPIC_API_KEY environment variable")
	}
	llm := &AnthropicLLM{
		apiKey:  apiKey,
		model:   model,
		baseURL: defaultAnthropicURL,
	}
	for _, opt := range opts {
		opt(llm)
	}
	return llm, nil
}

func (l *AnthropicLLM) Model() string {
	return l.model
}

func (l *AnthropicLLM) Provider() string {
	u, err := url.Parse(l.baseURL)
	if err != nil || u.Host == "" {
		return "anthropic"
	}
	return u.Host
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicContentBlock struct {
	Type         string         `json:"type"`
	Text         string         `json:"text,omitempty"`
	Source       map[string]any `json:"source,omitempty"`
	ID           string         `json:"id,omitempty"`
	Name         string         `json:"name,omitempty"`
	Input        map[string]any `json:"input,omitempty"`
	ToolUseID    string         `json:"tool_use_id,omitempty"`
	Content      any            `json:"content,omitempty"`
	IsError      bool           `json:"is_error,omitempty"`
	CacheControl map[string]any `json:"cache_control,omitempty"`
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

	messages, systemMessages, err := buildAnthropicMessagesE(chatCtx)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, err
	}
	if anthropicModelDisablesPrefill(l.model) {
		messages = appendAnthropicTrailingUserMessage(messages)
	}
	cacheControl := anthropicEphemeralCacheControl(options.ExtraParams)
	if cacheControl != nil {
		applyAnthropicMessageCacheControl(messages, cacheControl)
	}
	if err := validateAnthropicExtraParams(options.ExtraParams); err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, err
	}

	body := map[string]interface{}{
		"model":      l.model,
		"messages":   messages,
		"max_tokens": 1024,
		"stream":     true,
	}
	if len(systemMessages) > 0 {
		body["system"] = anthropicSystemBlocks(systemMessages, cacheControl)
	}
	applyAnthropicExtraParams(body, options.ExtraParams)

	var betaFlag string
	// Tool support
	if len(options.Tools) > 0 {
		tools := make([]map[string]interface{}, 0)
		for _, tool := range options.Tools {
			if providerTool, ok := tool.(anthropicToolSpecProvider); ok {
				tools = append(tools, providerTool.AnthropicToolSpec())
				if betaTool, ok := tool.(anthropicBetaToolProvider); ok {
					if flag := betaTool.AnthropicBetaFlag(); flag != "" {
						betaFlag = flag
					}
				}
			} else {
				tools = append(tools, map[string]interface{}{
					"name":         tool.Name(),
					"description":  tool.Description(),
					"input_schema": llm.ToolParameters(tool),
					"strict":       true,
				})
			}
		}
		if cacheControl != nil && len(tools) > 0 {
			tools[len(tools)-1]["cache_control"] = cacheControl
		}
		body["tools"] = tools
		if anthropicToolChoiceNone(options.ToolChoice) {
			body["tools"] = []map[string]interface{}{}
		}
		if toolChoice := buildAnthropicToolChoice(options.ToolChoice, options.ParallelToolCalls, options.ParallelToolCallsSet); toolChoice != nil {
			body["tool_choice"] = toolChoice
		}
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, err
	}
	var lastErr error
	for attempt := 0; attempt <= connectOptions.MaxRetry; attempt++ {
		resp, err := l.startAnthropicStream(ctx, jsonBody, betaFlag)
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

func (l *AnthropicLLM) startAnthropicStream(ctx context.Context, jsonBody []byte, betaFlag string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", l.baseURL+"/v1/messages", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", l.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	if betaFlag != "" {
		req.Header.Set("anthropic-beta", betaFlag)
	}

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
		message, body := parseAnthropicErrorBody(respBody)
		return nil, llm.CreateAPIErrorFromHTTP(message, resp.StatusCode, anthropicRequestID(resp.Header), body)
	}
	return resp, nil
}

func parseAnthropicErrorBody(respBody []byte) (string, any) {
	bodyText := strings.TrimSpace(string(respBody))
	if bodyText == "" {
		return "", nil
	}
	var parsed map[string]any
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return bodyText, bodyText
	}
	message := bodyText
	if errorBody, ok := parsed["error"].(map[string]any); ok {
		if nestedMessage, ok := errorBody["message"].(string); ok && nestedMessage != "" {
			message = nestedMessage
		}
	} else if topLevelMessage, ok := parsed["message"].(string); ok && topLevelMessage != "" {
		message = topLevelMessage
	}
	return message, parsed
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
		if key == "caching" {
			continue
		}
		body[key] = value
	}
}

func validateAnthropicExtraParams(params map[string]any) error {
	for _, key := range []string{"model", "messages", "stream", "timeout"} {
		if _, ok := params[key]; ok {
			return fmt.Errorf("extra param %q conflicts with reserved Anthropic request field", key)
		}
	}
	return nil
}

func anthropicEphemeralCacheControl(params map[string]any) map[string]any {
	if params["caching"] != "ephemeral" {
		return nil
	}
	return map[string]any{"type": "ephemeral"}
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

func buildAnthropicToolChoice(choice llm.ToolChoice, parallelToolCalls bool, parallelToolCallsSet bool) map[string]any {
	var toolChoice map[string]any
	switch tc := choice.(type) {
	case string:
		toolChoice = map[string]any{"type": "auto"}
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
	if parallelToolCallsSet {
		toolChoice["disable_parallel_tool_use"] = !parallelToolCalls
	}
	return toolChoice
}

func anthropicToolChoiceNone(choice llm.ToolChoice) bool {
	tc, ok := choice.(string)
	return ok && tc == "none"
}

func anthropicModelDisablesPrefill(model string) bool {
	for _, prefix := range anthropicNoPrefillModelPrefixes {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}
	return false
}

func appendAnthropicTrailingUserMessage(messages []anthropicMessage) []anthropicMessage {
	if len(messages) == 0 || messages[len(messages)-1].Role != "assistant" {
		return messages
	}
	return append(messages, anthropicMessage{
		Role: "user",
		Content: []anthropicContentBlock{
			{Type: "text", Text: " "},
		},
	})
}

func applyAnthropicMessageCacheControl(messages []anthropicMessage, cacheControl map[string]any) {
	seenAssistant := false
	for i := len(messages) - 1; i >= 0; i-- {
		if len(messages[i].Content) == 0 {
			continue
		}
		switch messages[i].Role {
		case "assistant":
			if !seenAssistant {
				last := len(messages[i].Content) - 1
				messages[i].Content[last].CacheControl = cacheControl
				seenAssistant = true
			}
		case "user":
			if seenAssistant {
				last := len(messages[i].Content) - 1
				messages[i].Content[last].CacheControl = cacheControl
				return
			}
		}
	}
}

type anthropicStream struct {
	resp   *http.Response
	reader *bufio.Reader
	cancel context.CancelFunc
	closed bool

	// internal states for tracking tool calls over multiple chunks
	toolCallActive      bool
	toolCallID          string
	toolName            string
	toolArgs            string
	requestID           string
	inputTokens         int
	outputTokens        int
	cacheCreationTokens int
	cacheReadTokens     int
	ignoringCoT         bool
	emittedChunk        bool
	emittedFinalUsage   bool
}

func buildAnthropicMessages(chatCtx *llm.ChatContext) ([]anthropicMessage, string) {
	messages, systemMessages, _ := buildAnthropicMessagesE(chatCtx)
	return messages, strings.Join(systemMessages, "\n")
}

func buildAnthropicMessagesE(chatCtx *llm.ChatContext) ([]anthropicMessage, []string, error) {
	messages := make([]anthropicMessage, 0, len(chatCtx.Items))
	systemMessages := make([]string, 0)
	var currentRole string
	instructionSeen := false
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

	groups, err := groupAnthropicChatItems(chatCtx.Items)
	if err != nil {
		return nil, nil, err
	}
	for _, group := range groups {
		for _, item := range group.flatten() {
			switch msg := item.(type) {
			case *llm.ChatMessage:
				if msg.Role == llm.ChatRoleSystem || msg.Role == llm.ChatRoleDeveloper {
					if text := msg.TextContent(); text != "" {
						if !instructionSeen {
							systemMessages = append(systemMessages, text)
						} else {
							appendBlocks("user", anthropicContentBlock{
								Type: "text",
								Text: inlineAnthropicInstructions(text),
							})
						}
					}
					instructionSeen = true
					continue
				}
				role := "user"
				if msg.Role == llm.ChatRoleAssistant {
					role = "assistant"
				}
				blocks, err := anthropicMessageContentBlocks(msg)
				if err != nil {
					return nil, nil, err
				}
				if len(blocks) > 0 {
					appendBlocks(role, blocks...)
				}
			case *llm.FunctionCall:
				block, err := anthropicToolUseBlock(msg)
				if err != nil {
					return nil, nil, err
				}
				appendBlocks("assistant", block)
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

	return messages, systemMessages, nil
}

func inlineAnthropicInstructions(text string) string {
	return "<instructions>\n" + text + "\n</instructions>"
}

func anthropicSystemBlocks(systemMessages []string, cacheControl map[string]any) []anthropicContentBlock {
	blocks := make([]anthropicContentBlock, 0, len(systemMessages))
	for _, text := range systemMessages {
		blocks = append(blocks, anthropicContentBlock{Type: "text", Text: text})
	}
	if cacheControl != nil && len(blocks) > 0 {
		blocks[len(blocks)-1].CacheControl = cacheControl
	}
	return blocks
}

func anthropicMessageContentBlocks(msg *llm.ChatMessage) ([]anthropicContentBlock, error) {
	blocks := make([]anthropicContentBlock, 0, len(msg.Content))
	for _, c := range msg.Content {
		if c.Text != "" {
			blocks = append(blocks, anthropicContentBlock{Type: "text", Text: c.Text})
		}
		if c.Image != nil {
			block, err := anthropicImageBlock(c.Image)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, block)
		}
	}
	return blocks, nil
}

func anthropicImageBlock(image *llm.ImageContent) (anthropicContentBlock, error) {
	img, err := llm.SerializeImage(image)
	if err != nil {
		return anthropicContentBlock{}, err
	}
	if img.ExternalURL != "" {
		return anthropicContentBlock{
			Type: "image",
			Source: map[string]any{
				"type": "url",
				"url":  img.ExternalURL,
			},
		}, nil
	}
	return anthropicContentBlock{
		Type: "image",
		Source: map[string]any{
			"type":       "base64",
			"data":       base64.StdEncoding.EncodeToString(img.DataBytes),
			"media_type": img.MIMEType,
		},
	}, nil
}

func anthropicToolUseBlock(fc *llm.FunctionCall) (anthropicContentBlock, error) {
	input := make(map[string]any)
	arguments := fc.Arguments
	if arguments == "" {
		arguments = "{}"
	}
	if err := json.Unmarshal([]byte(arguments), &input); err != nil {
		return anthropicContentBlock{}, err
	}
	return anthropicContentBlock{
		Type:  "tool_use",
		ID:    fc.CallID,
		Name:  fc.Name,
		Input: input,
	}, nil
}

func anthropicToolResultBlock(fco *llm.FunctionCallOutput) anthropicContentBlock {
	return anthropicContentBlock{
		Type:      "tool_result",
		ToolUseID: fco.CallID,
		Content:   anthropicToolResultContent(fco.Output),
		IsError:   fco.IsError,
	}
}

func anthropicToolResultContent(output string) any {
	var parsed []any
	if err := json.Unmarshal([]byte(output), &parsed); err == nil {
		return parsed
	}
	return output
}

type anthropicChatItemGroup struct {
	message     *llm.ChatMessage
	toolCalls   []*llm.FunctionCall
	toolOutputs []*llm.FunctionCallOutput
}

func groupAnthropicChatItems(items []llm.ChatItem) ([]*anthropicChatItemGroup, error) {
	groups := make([]*anthropicChatItemGroup, 0)
	groupsByID := make(map[string]*anthropicChatItemGroup)
	toolOutputs := make([]*llm.FunctionCallOutput, 0)

	addToGroup := func(groupID string, item llm.ChatItem) error {
		group := groupsByID[groupID]
		if group == nil {
			group = &anthropicChatItemGroup{}
			groupsByID[groupID] = group
			groups = append(groups, group)
		}
		return group.add(item)
	}

	for _, item := range items {
		switch it := item.(type) {
		case *llm.ChatMessage:
			if it.Role == llm.ChatRoleAssistant {
				if err := addToGroup(anthropicGroupID(it.ID, nil), it); err != nil {
					return nil, err
				}
			} else {
				if err := addToGroup(it.ID, it); err != nil {
					return nil, err
				}
			}
		case *llm.FunctionCall:
			if err := addToGroup(anthropicGroupID(it.ID, it.GroupID), it); err != nil {
				return nil, err
			}
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
			if err := group.add(toolOutput); err != nil {
				return nil, err
			}
		}
	}
	for _, group := range groups {
		group.removeInvalidToolItems()
	}
	return groups, nil
}

func (g *anthropicChatItemGroup) add(item llm.ChatItem) error {
	switch it := item.(type) {
	case *llm.ChatMessage:
		if g.message != nil {
			return fmt.Errorf("only one message is allowed in a group")
		}
		g.message = it
	case *llm.FunctionCall:
		g.toolCalls = append(g.toolCalls, it)
	case *llm.FunctionCallOutput:
		g.toolOutputs = append(g.toolOutputs, it)
	}
	return nil
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

	outputCallIDs := make(map[string]bool)
	for _, toolOutput := range g.toolOutputs {
		outputCallIDs[toolOutput.CallID] = true
	}

	validCalls := make([]*llm.FunctionCall, 0, len(g.toolCalls))
	validOutputs := make([]*llm.FunctionCallOutput, 0, len(g.toolOutputs))
	validCallIDs := make(map[string]bool)
	for _, toolCall := range g.toolCalls {
		if outputCallIDs[toolCall.CallID] {
			validCalls = append(validCalls, toolCall)
			validCallIDs[toolCall.CallID] = true
		}
	}
	for _, toolOutput := range g.toolOutputs {
		if validCallIDs[toolOutput.CallID] {
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
		if s.closed {
			return nil, io.EOF
		}

		line, err := s.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				if chunk := s.finalUsageChunk(); chunk != nil && !s.closed {
					return markAnthropicStreamChunk(s, chunk), nil
				}
				return nil, io.EOF
			}
			if s.closed {
				return nil, io.EOF
			}
			return nil, s.wrapReadError(err)
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
			return nil, s.wrapReadError(err)
		}

		switch event.Type {
		case "message_start":
			s.requestID = event.Message.ID
			s.inputTokens = event.Message.Usage.InputTokens
			s.outputTokens = event.Message.Usage.OutputTokens
			s.cacheCreationTokens = event.Message.Usage.CacheCreationInputTokens
			s.cacheReadTokens = event.Message.Usage.CacheReadInputTokens

		case "content_block_start":
			if event.ContentBlock.Type == "tool_use" {
				s.toolCallActive = true
				s.toolCallID = event.ContentBlock.ID
				s.toolName = event.ContentBlock.Name
				s.toolArgs = ""
			}

		case "content_block_delta":
			if event.Delta.Type == "text_delta" {
				text, emit := s.visibleAnthropicTextDelta(event.Delta.Text)
				if !emit {
					continue
				}
				return markAnthropicStreamChunk(s, &llm.ChatChunk{
					ID: s.requestID,
					Delta: &llm.ChoiceDelta{
						Role:    llm.ChatRoleAssistant,
						Content: text,
					},
				}), nil
			} else if event.Delta.Type == "input_json_delta" {
				if !s.toolCallActive {
					return nil, s.wrapReadError(errors.New("input_json_delta without tool_use content block"))
				}
				s.toolArgs += event.Delta.PartialJson
			}

		case "content_block_stop":
			if s.toolCallActive {
				chunk := &llm.ChatChunk{
					ID: s.requestID,
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
				s.toolCallActive = false
				s.toolCallID = ""
				s.toolName = ""
				s.toolArgs = ""
				return markAnthropicStreamChunk(s, chunk), nil
			}

		case "message_delta":
			s.outputTokens += event.Usage.OutputTokens

		case "message_stop":
			if chunk := s.finalUsageChunk(); chunk != nil {
				return markAnthropicStreamChunk(s, chunk), nil
			}

		case "error":
			message, body := parseAnthropicErrorBody([]byte(data))
			return nil, llm.NewAPIError(message, body, !s.emittedChunk)
		}
	}
}

func (s *anthropicStream) finalUsageChunk() *llm.ChatChunk {
	if s.emittedFinalUsage {
		return nil
	}
	s.emittedFinalUsage = true
	promptTokens := s.inputTokens + s.cacheCreationTokens + s.cacheReadTokens
	return &llm.ChatChunk{
		ID: s.requestID,
		Usage: &llm.CompletionUsage{
			PromptTokens:        promptTokens,
			CompletionTokens:    s.outputTokens,
			TotalTokens:         promptTokens + s.outputTokens,
			PromptCachedTokens:  s.cacheReadTokens,
			CacheCreationTokens: s.cacheCreationTokens,
			CacheReadTokens:     s.cacheReadTokens,
		},
	}
}

func markAnthropicStreamChunk(s *anthropicStream, chunk *llm.ChatChunk) *llm.ChatChunk {
	s.emittedChunk = true
	return chunk
}

func (s *anthropicStream) wrapReadError(err error) error {
	retryable := !s.emittedChunk
	if errors.Is(err, context.DeadlineExceeded) {
		return llm.NewAPITimeoutErrorWithRetryable("", retryable)
	}
	return llm.NewAPIConnectionErrorWithRetryable(err.Error(), retryable)
}

func (s *anthropicStream) visibleAnthropicTextDelta(text string) (string, bool) {
	if strings.HasPrefix(text, "<thinking>") {
		s.ignoringCoT = true
		return "", false
	}
	if s.ignoringCoT {
		if _, after, ok := strings.Cut(text, "</thinking>"); ok {
			s.ignoringCoT = false
			return after, true
		}
		return "", false
	}
	return text, true
}

func (s *anthropicStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	var err error
	if s.resp != nil && s.resp.Body != nil {
		err = s.resp.Body.Close()
	}
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	return err
}
