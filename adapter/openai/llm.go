package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/cavos-io/conversation-worker/core/llm"
	"github.com/sashabaranov/go-openai"
)

type OpenAILLM struct {
	client *openai.Client
	model  string
}

func NewOpenAILLM(apiKey string, model string) *OpenAILLM {
	return &OpenAILLM{
		client: openai.NewClient(apiKey),
		model:  model,
	}
}

func NewOpenAILLMWithConfig(config openai.ClientConfig) *OpenAILLM {
	return &OpenAILLM{
		client: openai.NewClientWithConfig(config),
	}
}

func NewOpenAILLMWithBaseURL(apiKey string, model string, baseURL string) *OpenAILLM {
	config := openai.DefaultConfig(apiKey)
	if baseURL != "" {
		config.BaseURL = baseURL
	}
	return &OpenAILLM{
		client: openai.NewClientWithConfig(config),
		model:  model,
	}
}

func (l *OpenAILLM) Model() string {
	return l.model
}

func (l *OpenAILLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	options := &llm.ChatOptions{
		ParallelToolCalls: true,
	}
	for _, opt := range opts {
		opt(options)
	}

	req := buildOpenAIChatCompletionRequest(l.model, chatCtx, options)

	stream, err := l.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return nil, err
	}

	return &openaiStream{
		stream: stream,
	}, nil
}

func buildOpenAIChatCompletionRequest(model string, chatCtx *llm.ChatContext, options *llm.ChatOptions) openai.ChatCompletionRequest {
	messages := buildOpenAIChatMessages(chatCtx)

	tools := make([]openai.Tool, 0, len(options.Tools))
	for _, tool := range options.Tools {
		params, _ := json.Marshal(tool.Parameters())
		tools = append(tools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        tool.Name(),
				Description: tool.Description(),
				Strict:      true,
				Parameters:  json.RawMessage(params),
			},
		})
	}

	req := openai.ChatCompletionRequest{
		Model:             model,
		Messages:          messages,
		Tools:             tools,
		ParallelToolCalls: &options.ParallelToolCalls,
		Stream:            true,
	}

	if options.ToolChoice != nil {
		if toolChoice := buildOpenAIToolChoice(options.ToolChoice); toolChoice != nil {
			req.ToolChoice = toolChoice
		}
	}
	if responseFormat := buildOpenAIResponseFormat(options.ResponseFormat); responseFormat != nil {
		req.ResponseFormat = responseFormat
	}

	applyOpenAIExtraParams(&req, options.ExtraParams)
	return req
}

func buildOpenAIToolChoice(choice llm.ToolChoice) any {
	switch tc := choice.(type) {
	case string:
		return tc
	case openai.ToolChoice:
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
		return openai.ToolChoice{
			Type:     openai.ToolTypeFunction,
			Function: openai.ToolFunction{Name: name},
		}
	default:
		return nil
	}
}

func buildOpenAIResponseFormat(format map[string]any) *openai.ChatCompletionResponseFormat {
	if len(format) == 0 {
		return nil
	}
	formatType, ok := format["type"].(string)
	if !ok || formatType == "" {
		return nil
	}
	responseFormat := &openai.ChatCompletionResponseFormat{
		Type: openai.ChatCompletionResponseFormatType(formatType),
	}
	if formatType != string(openai.ChatCompletionResponseFormatTypeJSONSchema) {
		return responseFormat
	}
	jsonSchema, ok := format["json_schema"].(map[string]any)
	if !ok {
		return responseFormat
	}
	schema := &openai.ChatCompletionResponseFormatJSONSchema{}
	if name, ok := jsonSchema["name"].(string); ok {
		schema.Name = name
	}
	if description, ok := jsonSchema["description"].(string); ok {
		schema.Description = description
	}
	if strict, ok := jsonSchema["strict"].(bool); ok {
		schema.Strict = strict
	}
	if rawSchema, ok := jsonSchema["schema"]; ok {
		if data, err := json.Marshal(rawSchema); err == nil {
			schema.Schema = json.RawMessage(data)
		}
	}
	responseFormat.JSONSchema = schema
	return responseFormat
}

func applyOpenAIExtraParams(req *openai.ChatCompletionRequest, params map[string]any) {
	for key, value := range params {
		switch key {
		case "temperature":
			if v, ok := asFloat32(value); ok {
				req.Temperature = v
			}
		case "top_p":
			if v, ok := asFloat32(value); ok {
				req.TopP = v
			}
		case "presence_penalty":
			if v, ok := asFloat32(value); ok {
				req.PresencePenalty = v
			}
		case "frequency_penalty":
			if v, ok := asFloat32(value); ok {
				req.FrequencyPenalty = v
			}
		case "n":
			if v, ok := asInt(value); ok {
				req.N = v
			}
		case "max_tokens":
			if v, ok := asInt(value); ok {
				req.MaxTokens = v
			}
		case "max_completion_tokens":
			if v, ok := asInt(value); ok {
				req.MaxCompletionTokens = v
			}
		case "logit_bias":
			if v := asIntMap(value); v != nil {
				req.LogitBias = v
			}
		case "logprobs":
			if v, ok := value.(bool); ok {
				req.LogProbs = v
			}
		case "top_logprobs":
			if v, ok := asInt(value); ok {
				req.TopLogProbs = v
			}
		case "reasoning_effort":
			if v, ok := value.(string); ok {
				req.ReasoningEffort = v
			}
		case "metadata":
			if v := asStringMap(value); v != nil {
				req.Metadata = v
			}
		case "seed":
			if v, ok := asInt(value); ok {
				req.Seed = &v
			}
		case "stop":
			if v := asStringSlice(value); v != nil {
				req.Stop = v
			}
		case "user":
			if v, ok := value.(string); ok {
				req.User = v
			}
		case "store":
			if v, ok := value.(bool); ok {
				req.Store = v
			}
		case "stream_options":
			if v := asStreamOptions(value); v != nil {
				req.StreamOptions = v
			}
		}
	}
}

func asFloat32(value any) (float32, bool) {
	switch v := value.(type) {
	case float32:
		return v, true
	case float64:
		return float32(v), true
	case int:
		return float32(v), true
	default:
		return 0, false
	}
}

func asInt(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	default:
		return 0, false
	}
}

func asIntMap(value any) map[string]int {
	switch v := value.(type) {
	case map[string]int:
		return v
	case map[string]any:
		out := make(map[string]int, len(v))
		for key, val := range v {
			intVal, ok := asInt(val)
			if !ok {
				return nil
			}
			out[key] = intVal
		}
		return out
	default:
		return nil
	}
}

func asStringMap(value any) map[string]string {
	switch v := value.(type) {
	case map[string]string:
		return v
	case map[string]any:
		out := make(map[string]string, len(v))
		for key, val := range v {
			out[key] = fmt.Sprint(val)
		}
		return out
	default:
		return nil
	}
}

func asStringSlice(value any) []string {
	switch v := value.(type) {
	case string:
		return []string{v}
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			str, ok := item.(string)
			if !ok {
				return nil
			}
			out = append(out, str)
		}
		return out
	default:
		return nil
	}
}

func asStreamOptions(value any) *openai.StreamOptions {
	optionsMap, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	options := &openai.StreamOptions{}
	if includeUsage, ok := optionsMap["include_usage"].(bool); ok {
		options.IncludeUsage = includeUsage
	}
	return options
}

func buildOpenAIChatMessages(chatCtx *llm.ChatContext) []openai.ChatCompletionMessage {
	messages := make([]openai.ChatCompletionMessage, 0, len(chatCtx.Items))
	for _, group := range groupOpenAIChatItems(chatCtx.Items) {
		if group.message == nil && len(group.toolCalls) == 0 && len(group.toolOutputs) == 0 {
			continue
		}

		var msg openai.ChatCompletionMessage
		if group.message != nil {
			msg = buildOpenAIChatMessage(group.message)
		} else {
			msg = openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant}
		}
		for _, toolCall := range group.toolCalls {
			msg.ToolCalls = append(msg.ToolCalls, buildOpenAIToolCall(toolCall))
		}
		messages = append(messages, msg)

		for _, toolOutput := range group.toolOutputs {
			messages = append(messages, buildOpenAIToolOutput(toolOutput))
		}
	}
	return messages
}

func buildOpenAIChatMessage(msg *llm.ChatMessage) openai.ChatCompletionMessage {
	oaMsg := openai.ChatCompletionMessage{
		Role: string(msg.Role),
	}
	if len(msg.Content) == 1 && msg.Content[0].Text != "" {
		oaMsg.Content = msg.Content[0].Text
		return oaMsg
	}

	parts := make([]openai.ChatMessagePart, 0, len(msg.Content))
	for _, c := range msg.Content {
		if c.Text != "" {
			parts = append(parts, openai.ChatMessagePart{
				Type: openai.ChatMessagePartTypeText,
				Text: c.Text,
			})
		} else if c.Image != nil {
			imageURL := ""
			if str, ok := c.Image.Image.(string); ok {
				imageURL = str
			}
			if imageURL != "" {
				parts = append(parts, openai.ChatMessagePart{
					Type: openai.ChatMessagePartTypeImageURL,
					ImageURL: &openai.ChatMessageImageURL{
						URL:    imageURL,
						Detail: openai.ImageURLDetail(c.Image.InferenceDetail),
					},
				})
			}
		}
	}
	oaMsg.MultiContent = parts
	return oaMsg
}

type openAIChatItemGroup struct {
	message     *llm.ChatMessage
	toolCalls   []*llm.FunctionCall
	toolOutputs []*llm.FunctionCallOutput
}

func groupOpenAIChatItems(items []llm.ChatItem) []*openAIChatItemGroup {
	groups := make([]*openAIChatItemGroup, 0)
	groupsByID := make(map[string]*openAIChatItemGroup)
	toolOutputs := make([]*llm.FunctionCallOutput, 0)

	addToGroup := func(groupID string, item llm.ChatItem) {
		group := groupsByID[groupID]
		if group == nil {
			group = &openAIChatItemGroup{}
			groupsByID[groupID] = group
			groups = append(groups, group)
		}
		group.add(item)
	}

	for _, item := range items {
		switch it := item.(type) {
		case *llm.ChatMessage:
			if it.Role == llm.ChatRoleAssistant {
				addToGroup(openAIGroupID(it.ID, nil), it)
			} else {
				addToGroup(it.ID, it)
			}
		case *llm.FunctionCall:
			addToGroup(openAIGroupID(it.ID, it.GroupID), it)
		case *llm.FunctionCallOutput:
			toolOutputs = append(toolOutputs, it)
		}
	}

	groupsByCallID := make(map[string]*openAIChatItemGroup)
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

func (g *openAIChatItemGroup) add(item llm.ChatItem) {
	switch it := item.(type) {
	case *llm.ChatMessage:
		g.message = it
	case *llm.FunctionCall:
		g.toolCalls = append(g.toolCalls, it)
	case *llm.FunctionCallOutput:
		g.toolOutputs = append(g.toolOutputs, it)
	}
}

func (g *openAIChatItemGroup) removeInvalidToolItems() {
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

func openAIGroupID(itemID string, groupID *string) string {
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

func buildOpenAIToolCall(toolCall *llm.FunctionCall) openai.ToolCall {
	return openai.ToolCall{
		ID:   toolCall.CallID,
		Type: openai.ToolTypeFunction,
		Function: openai.FunctionCall{
			Name:      toolCall.Name,
			Arguments: toolCall.Arguments,
		},
	}
}

func buildOpenAIToolOutput(toolOutput *llm.FunctionCallOutput) openai.ChatCompletionMessage {
	return openai.ChatCompletionMessage{
		Role:       openai.ChatMessageRoleTool,
		Content:    toolOutput.Output,
		ToolCallID: toolOutput.CallID,
	}
}

type openaiStream struct {
	stream *openai.ChatCompletionStream
}

func (s *openaiStream) Next() (*llm.ChatChunk, error) {
	resp, err := s.stream.Recv()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, err
	}

	if len(resp.Choices) == 0 {
		return &llm.ChatChunk{ID: resp.ID}, nil
	}

	choice := resp.Choices[0]
	chunk := &llm.ChatChunk{
		ID: resp.ID,
		Delta: &llm.ChoiceDelta{
			Role:    llm.ChatRole(choice.Delta.Role),
			Content: choice.Delta.Content,
		},
	}

	if len(choice.Delta.ToolCalls) > 0 {
		chunk.Delta.ToolCalls = make([]llm.FunctionToolCall, 0, len(choice.Delta.ToolCalls))
		for _, tc := range choice.Delta.ToolCalls {
			chunk.Delta.ToolCalls = append(chunk.Delta.ToolCalls, llm.FunctionToolCall{
				Type:      string(tc.Type),
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
				CallID:    tc.ID,
			})
		}
	}

	if resp.Usage != nil {
		chunk.Usage = &llm.CompletionUsage{
			CompletionTokens:   resp.Usage.CompletionTokens,
			PromptTokens:       resp.Usage.PromptTokens,
			PromptCachedTokens: resp.Usage.PromptTokensDetails.CachedTokens,
			TotalTokens:        resp.Usage.TotalTokens,
		}
	}

	return chunk, nil
}

func (s *openaiStream) Close() error {
	s.stream.Close()
	return nil
}

func (l *OpenAILLM) RawClient() *openai.Client {
	return l.client
}
