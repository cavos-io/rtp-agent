package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/sashabaranov/go-openai"
)

type OpenAILLM struct {
	client *openai.Client
	model  string
}

type Option func(*openai.ClientConfig)

func WithBaseURL(url string) Option {
	return func(c *openai.ClientConfig) {
		c.BaseURL = url
	}
}

func NewOpenAILLM(apiKey string, model string, opts ...Option) *OpenAILLM {
	config := openai.DefaultConfig(apiKey)
	for _, opt := range opts {
		opt(&config)
	}
	return &OpenAILLM{
		client: openai.NewClientWithConfig(config),
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

func (l *OpenAILLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	options := &llm.ChatOptions{
		ParallelToolCalls: true,
	}
	for _, opt := range opts {
		opt(options)
	}

	messagesRaw, _ := chatCtx.ToProviderFormat("openai")
	messages, _ := messagesRaw.([]map[string]any)

	oaMessages := make([]openai.ChatCompletionMessage, 0, len(messages))
	for _, m := range messages {
		role := m["role"].(string)
		content, _ := m["content"].(string)
		
		msg := openai.ChatCompletionMessage{
			Role:    role,
			Content: content,
		}

		if toolCalls, ok := m["tool_calls"].([]map[string]any); ok {
			for _, tc := range toolCalls {
				fn := tc["function"].(map[string]any)
				msg.ToolCalls = append(msg.ToolCalls, openai.ToolCall{
					ID:   tc["id"].(string),
					Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name:      fn["name"].(string),
						Arguments: fn["arguments"].(string),
					},
				})
			}
		}

		if toolCallID, ok := m["tool_call_id"].(string); ok {
			msg.ToolCallID = toolCallID
		}

		oaMessages = append(oaMessages, msg)
	}

	tc := llm.NewToolContext(options.Tools)
	schemas := tc.ParseFunctionTools("openai")

	tools := make([]openai.Tool, 0, len(schemas))
	if len(schemas) > 0 {
		b, _ := json.Marshal(schemas)
		_ = json.Unmarshal(b, &tools)
	}

	req := openai.ChatCompletionRequest{
		Model:    l.model,
		Messages: oaMessages,
		Stream:   true,
	}

	// Only set tools params when tools are defined (OpenAI rejects parallel_tool_calls without tools)
	if len(tools) > 0 {
		req.Tools = tools
		req.ParallelToolCalls = &options.ParallelToolCalls
	}

	if options.ToolChoice != nil {
		if str, ok := options.ToolChoice.(string); ok {
			req.ToolChoice = str
		} else if tc, ok := options.ToolChoice.(openai.ToolChoice); ok {
			req.ToolChoice = tc
		}
	}

	stream, err := l.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return nil, err
	}

	return &openaiStream{
		stream: stream,
	}, nil
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
			extra := map[string]any{
				"index": tc.Index,
			}
			chunk.Delta.ToolCalls = append(chunk.Delta.ToolCalls, llm.FunctionToolCall{
				Type:      string(tc.Type),
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
				CallID:    tc.ID,
				Extra:     extra,
			})
		}
	}

	if resp.Usage != nil {
		chunk.Usage = &llm.CompletionUsage{
			CompletionTokens: resp.Usage.CompletionTokens,
			PromptTokens:     resp.Usage.PromptTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		}
		if resp.Usage.PromptTokensDetails != nil {
			chunk.Usage.PromptCachedTokens = resp.Usage.PromptTokensDetails.CachedTokens
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

