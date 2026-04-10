package hume

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/cavos-io/rtp-agent/core/llm"
)

type HumeLLM struct {
	apiKey string
	model  string
}

func NewHumeLLM(apiKey string, model string) *HumeLLM {
	if model == "" {
		model = "hume-evi-2"
	}
	return &HumeLLM{
		apiKey: apiKey,
		model:  model,
	}
}

func (l *HumeLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	url := "https://api.hume.ai/v0/evi/chat/completions"

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
		"model":    l.model,
		"messages": messages,
	}

	jsonBody, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hume-Api-Key", l.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("hume llm error: %s", string(respBody))
	}

	return &humeLLMStream{
		resp: resp,
	}, nil
}

type humeLLMStream struct {
	resp *http.Response
	done bool
}

func (s *humeLLMStream) Next() (*llm.ChatChunk, error) {
	if s.done {
		return nil, io.EOF
	}
	s.done = true
	defer s.resp.Body.Close()

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

func (s *humeLLMStream) Close() error {
	return s.resp.Body.Close()
}
