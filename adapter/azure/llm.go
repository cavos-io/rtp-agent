package azure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/sashabaranov/go-openai"
)

// AzureLLM implements llm.LLM using the Azure OpenAI Service REST API.
// It leverages the go-openai client with an Azure-compatible configuration.
type AzureLLM struct {
	client     *openai.Client
	deployment string // Azure deployment name (maps to model)
}

// NewAzureLLM creates an AzureLLM using the Azure OpenAI endpoint.
//
// Parameters:
//   - apiKey:     Azure OpenAI API key (from Azure portal)
//   - endpoint:   Azure resource endpoint, e.g. "https://<resource>.openai.azure.com/"
//   - deployment: Deployment name configured in Azure (e.g. "gpt-4o")
//   - apiVersion: Azure OpenAI API version, e.g. "2024-02-01"
func NewAzureLLM(apiKey, endpoint, deployment, apiVersion string) *AzureLLM {
	config := openai.DefaultAzureConfig(apiKey, endpoint)
	if apiVersion != "" {
		config.APIVersion = apiVersion
	}
	return &AzureLLM{
		client:     openai.NewClientWithConfig(config),
		deployment: deployment,
	}
}

func (l *AzureLLM) Label() string { return fmt.Sprintf("azure.LLM(%s)", l.deployment) }

// Chat sends a streaming chat completion request to Azure OpenAI and returns an LLMStream.
func (l *AzureLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	log.Println("AzureLLM Chat called with deployment:", l.deployment)
	options := &llm.ChatOptions{
		ParallelToolCalls: true,
	}
	for _, opt := range opts {
		opt(options)
	}

	messagesRaw, _ := chatCtx.ToProviderFormat("openai")
	messages, _ := messagesRaw.([]map[string]any)

	log.Println("ChatContext converted to provider format:", messages)

	oaMessages := make([]openai.ChatCompletionMessage, 0, len(messages))
	for _, m := range messages {
		role, _ := m["role"].(string)
		content, _ := m["content"].(string)

		msg := openai.ChatCompletionMessage{
			Role:    role,
			Content: content,
		}

		if toolCalls, ok := m["tool_calls"].([]map[string]any); ok {
			for _, tc := range toolCalls {
				fn, _ := tc["function"].(map[string]any)
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

	log.Println("Converted messages for Azure OpenAI:", oaMessages)

	tc := llm.NewToolContext(options.Tools)
	schemas := tc.ParseFunctionTools("openai")

	tools := make([]openai.Tool, 0, len(schemas))
	if len(schemas) > 0 {
		b, _ := json.Marshal(schemas)
		_ = json.Unmarshal(b, &tools)
	}

	req := openai.ChatCompletionRequest{
		Model:    l.deployment,
		Messages: oaMessages,
		Stream:   true,
	}

	if len(tools) > 0 {
		req.Tools = tools
		req.ParallelToolCalls = &options.ParallelToolCalls
	}

	if options.ToolChoice != nil {
		if str, ok := options.ToolChoice.(string); ok {
			req.ToolChoice = str
		} else if choice, ok := options.ToolChoice.(openai.ToolChoice); ok {
			req.ToolChoice = choice
		}
	}

	log.Println("Sending chat completion request to Azure OpenAI with deployment:", l.deployment)

	stream, err := l.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		log.Println("Error creating Azure OpenAI stream:", err.Error())
		return nil, err
	}

	log.Println("Azure OpenAI stream created successfully")

	return &azureLLMStream{stream: stream}, nil
}

// RawClient returns the underlying go-openai client for advanced usage.
func (l *AzureLLM) RawClient() *openai.Client {
	return l.client
}

type azureLLMStream struct {
	stream *openai.ChatCompletionStream
}

func (s *azureLLMStream) Next() (*llm.ChatChunk, error) {
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
				Extra: map[string]any{
					"index": tc.Index,
				},
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

func (s *azureLLMStream) Close() error {
	s.stream.Close()
	return nil
}
