package openai

import (
	"context"
	"encoding/json"
	"errors"
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

func (l *OpenAILLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	options := &llm.ChatOptions{
		ParallelToolCalls: true,
	}
	for _, opt := range opts {
		opt(options)
	}

	messages := make([]openai.ChatCompletionMessage, 0, len(chatCtx.Items))
	for _, item := range chatCtx.Items {
		switch msg := item.(type) {
		case *llm.ChatMessage:
			oaMsg := openai.ChatCompletionMessage{
				Role: string(msg.Role),
			}
			if len(msg.Content) == 1 && msg.Content[0].Text != "" {
				oaMsg.Content = msg.Content[0].Text
			} else {
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
			}
			messages = append(messages, oaMsg)
		case *llm.FunctionCall:
			messages = append(messages, openai.ChatCompletionMessage{
				Role: openai.ChatMessageRoleAssistant,
				ToolCalls: []openai.ToolCall{
					{
						ID:   msg.CallID,
						Type: openai.ToolTypeFunction,
						Function: openai.FunctionCall{
							Name:      msg.Name,
							Arguments: msg.Arguments,
						},
					},
				},
			})
		case *llm.FunctionCallOutput:
			messages = append(messages, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    msg.Output,
				ToolCallID: msg.CallID,
			})
		}
	}

	tools := make([]openai.Tool, 0, len(options.Tools))
	for _, tool := range options.Tools {
		params, _ := json.Marshal(tool.Parameters())
		tools = append(tools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        tool.Name(),
				Description: tool.Description(),
				Parameters:  json.RawMessage(params),
			},
		})
	}

	req := openai.ChatCompletionRequest{
		Model:    l.model,
		Messages: messages,
		Tools:    tools,
		Stream:   true,
	}
	if len(tools) > 0 {
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
