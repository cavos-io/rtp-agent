package fal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/cavos-io/rtp-agent/core/llm"
)

type FalLLM struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
}

type Option func(*FalLLM)

func WithBaseURL(url string) Option {
	return func(l *FalLLM) {
		l.baseURL = url
	}
}

func WithHTTPClient(client *http.Client) Option {
	return func(l *FalLLM) {
		l.httpClient = client
	}
}

func NewFalLLM(apiKey string, model string, opts ...Option) *FalLLM {
	l := &FalLLM{
		apiKey:     apiKey,
		model:      model,
		baseURL:    fmt.Sprintf("https://fal.run/%s", model),
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

func (l *FalLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	// Fal generally acts as a proxy for various open-source LLMs.
	// This is a basic implementation for a typical chat completions endpoint.
	options := &llm.ChatOptions{}
	for _, opt := range opts {
		opt(options)
	}

	messages := make([]map[string]string, 0)
	for _, item := range chatCtx.Items {
		if msg, ok := item.(*llm.ChatMessage); ok {
			messages = append(messages, map[string]string{
				"role":    string(msg.Role),
				"content": msg.TextContent(),
			})
		}
	}

	body := map[string]interface{}{
		"messages": messages,
	}

	jsonBody, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", l.baseURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Key "+l.apiKey)

	resp, err := l.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("fal llm error: %s", string(respBody))
	}

	return &falLLMStream{
		resp: resp,
	}, nil
}

type falLLMStream struct {
	resp *http.Response
	done bool
}

func (s *falLLMStream) Next() (*llm.ChatChunk, error) {
	if s.done {
		return nil, io.EOF
	}
	s.done = true
	defer s.resp.Body.Close()

	// Assuming a simple response structure
	var result struct {
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(s.resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	chunk := &llm.ChatChunk{
		Delta: &llm.ChoiceDelta{},
	}

	if len(result.Choices) > 0 {
		chunk.Delta.Role = llm.ChatRole(result.Choices[0].Message.Role)
		chunk.Delta.Content = result.Choices[0].Message.Content
	}

	return chunk, nil
}

func (s *falLLMStream) Close() error {
	return s.resp.Body.Close()
}

