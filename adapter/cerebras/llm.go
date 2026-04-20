package cerebras

import (
	"context"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/sashabaranov/go-openai"
)

type CerebrasLLM struct {
	client *openai.Client
	model  string
}

func NewCerebrasLLM(apiKey string, model string) *CerebrasLLM {
	config := openai.DefaultConfig(apiKey)
	config.BaseURL = "https://api.cerebras.ai/v1"
	
	if model == "" {
		model = "llama3.1-70b" // Default parity with python if not specified
	}

	return &CerebrasLLM{
		client: openai.NewClientWithConfig(config),
		model:  model,
	}
}

func (l *CerebrasLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	// Cerebras is OpenAI-compatible, so we can reuse the OpenAI adapter logic
	// but wrapped in our Cerebras struct. 
	// To avoid code duplication, we could theoretically cast or wrap, 
	// but here we'll implement it cleanly.
	
	options := &llm.ChatOptions{}
	for _, opt := range opts {
		opt(options)
	}

	// We'll use a internal helper or just re-implement since it's small
	messagesRaw, _ := chatCtx.ToProviderFormat("openai")
	messages, _ := messagesRaw.([]map[string]any)

	oaMessages := make([]openai.ChatCompletionMessage, 0, len(messages))
	for _, m := range messages {
		role := m["role"].(string)
		content, _ := m["content"].(string)
		oaMessages = append(oaMessages, openai.ChatCompletionMessage{
			Role:    role,
			Content: content,
		})
	}

	req := openai.ChatCompletionRequest{
		Model:    l.model,
		Messages: oaMessages,
		Stream:   true,
	}

	stream, err := l.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return nil, err
	}

	return &cerebrasStream{
		stream: stream,
	}, nil
}

type cerebrasStream struct {
	stream *openai.ChatCompletionStream
}

func (s *cerebrasStream) Next() (*llm.ChatChunk, error) {
	resp, err := s.stream.Recv()
	if err != nil {
		return nil, err
	}

	if len(resp.Choices) == 0 {
		return &llm.ChatChunk{ID: resp.ID}, nil
	}

	choice := resp.Choices[0]
	return &llm.ChatChunk{
		ID: resp.ID,
		Delta: &llm.ChoiceDelta{
			Role:    llm.ChatRole(choice.Delta.Role),
			Content: choice.Delta.Content,
		},
	}, nil
}

func (s *cerebrasStream) Close() error {
	s.stream.Close()
	return nil
}
