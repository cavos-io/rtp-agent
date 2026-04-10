package xai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cavos-io/rtp-agent/core/llm"
)

type XaiLLM struct {
	apiKey string
	model  string
}

func NewXaiLLM(apiKey string, model string) *XaiLLM {
	if model == "" {
		model = "grok-2-latest"
	}
	return &XaiLLM{
		apiKey: apiKey,
		model:  model,
	}
}

type xaiMessage struct {
	Role       string      `json:"role"`
	Content    string      `json:"content"`
	ToolCalls  []xaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
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
	options := &llm.ChatOptions{
		ParallelToolCalls: true,
	}
	for _, opt := range opts {
		opt(options)
	}

	messages := make([]xaiMessage, 0)
	for _, item := range chatCtx.Items {
		switch msg := item.(type) {
		case *llm.ChatMessage:
			role := string(msg.Role)
			if role == "developer" {
				role = "system"
			}
			messages = append(messages, xaiMessage{
				Role:    role,
				Content: msg.TextContent(),
			})
		case *llm.FunctionCall:
			messages = append(messages, xaiMessage{
				Role: "assistant",
				ToolCalls: []xaiToolCall{
					{
						ID:   msg.CallID,
						Type: "function",
						Function: struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						}{
							Name:      msg.Name,
							Arguments: msg.Arguments,
						},
					},
				},
			})
		case *llm.FunctionCallOutput:
			messages = append(messages, xaiMessage{
				Role:       "tool",
				Content:    msg.Output,
				ToolCallID: msg.CallID,
			})
		}
	}

	body := map[string]interface{}{
		"model":    l.model,
		"messages": messages,
		"stream":   true,
	}

	if len(options.Tools) > 0 {
		tools := make([]map[string]interface{}, 0)
		for _, tool := range options.Tools {
			if tool.Name() == "xai_web_search" {
				tools = append(tools, map[string]interface{}{
					"type": "web_search",
				})
			} else if tool.Name() == "xai_x_search" {
				tools = append(tools, map[string]interface{}{
					"type": "x_search",
				}) // Expand allowed_x_handles if needed via parameters later
			} else if tool.Name() == "xai_file_search" {
				tools = append(tools, map[string]interface{}{
					"type": "file_search",
				}) // Expand vector_store_ids if needed
			} else {
				tools = append(tools, map[string]interface{}{
					"type": "function",
					"function": map[string]interface{}{
						"name":        tool.Name(),
						"description": tool.Description(),
						"parameters":  tool.Parameters(),
					},
				})
			}
		}
		body["tools"] = tools
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
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("xai error: %s", string(respBody))
	}

	return &xaiStream{
		resp: resp,
	}, nil
}

type xaiStream struct {
	resp    *http.Response
	scanner *bufio.Scanner
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
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}

		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil, err
		}

		if len(chunk.Choices) > 0 {
			return &llm.ChatChunk{
				ID: chunk.ID,
				Delta: &llm.ChoiceDelta{
					Role:    llm.ChatRole(chunk.Choices[0].Delta.Role),
					Content: chunk.Choices[0].Delta.Content,
				},
			}, nil
		}
	}

	if err := s.scanner.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func (s *xaiStream) Close() error {
	return s.resp.Body.Close()
}

// XAITool implementations
type WebSearchTool struct{}
func (t *WebSearchTool) ID() string { return "xai_web_search" }
func (t *WebSearchTool) Name() string { return "xai_web_search" }
func (t *WebSearchTool) Description() string { return "Enable web search tool for real-time internet searches." }
func (t *WebSearchTool) Parameters() map[string]any { return nil }
func (t *WebSearchTool) Execute(ctx context.Context, args string) (string, error) { return "dispatched", nil }

type XSearchTool struct{ AllowedHandles []string }
func (t *XSearchTool) ID() string { return "xai_x_search" }
func (t *XSearchTool) Name() string { return "xai_x_search" }
func (t *XSearchTool) Description() string { return "Enable X (Twitter) search tool for searching posts." }
func (t *XSearchTool) Parameters() map[string]any { return nil }
func (t *XSearchTool) Execute(ctx context.Context, args string) (string, error) { return "dispatched", nil }

type FileSearchTool struct{ VectorStoreIDs []string; MaxNumResults int }
func (t *FileSearchTool) ID() string { return "xai_file_search" }
func (t *FileSearchTool) Name() string { return "xai_file_search" }
func (t *FileSearchTool) Description() string { return "Enable file search tool for searching uploaded document collections." }
func (t *FileSearchTool) Parameters() map[string]any { return nil }
func (t *FileSearchTool) Execute(ctx context.Context, args string) (string, error) { return "dispatched", nil }
