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
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
}

type Option func(*XaiLLM)

func WithBaseURL(url string) Option {
	return func(l *XaiLLM) {
		l.baseURL = url
	}
}

func WithHTTPClient(client *http.Client) Option {
	return func(l *XaiLLM) {
		l.httpClient = client
	}
}

func NewXaiLLM(apiKey string, model string, opts ...Option) *XaiLLM {
	if model == "" {
		model = "grok-2-latest"
	}
	l := &XaiLLM{
		apiKey:     apiKey,
		model:      model,
		baseURL:    "https://api.x.ai/v1/chat/completions",
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
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
		tc := llm.NewToolContext(options.Tools)
		body["tools"] = tc.ParseFunctionTools("openai")
	}

	jsonBody, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", l.baseURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+l.apiKey)

	resp, err := l.httpClient.Do(req)
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
func (t *WebSearchTool) Execute(ctx context.Context, args any) (any, error) { return "dispatched", nil }
func (t *WebSearchTool) IsProviderTool() bool { return true }
func (t *WebSearchTool) ProviderSchema(format string) map[string]any {
	if format == "openai" {
		return map[string]any{"type": "web_search"}
	}
	return nil
}

type XSearchTool struct{ AllowedHandles []string }
func (t *XSearchTool) ID() string { return "xai_x_search" }
func (t *XSearchTool) Name() string { return "xai_x_search" }
func (t *XSearchTool) Description() string { return "Enable X (Twitter) search tool for searching posts." }
func (t *XSearchTool) Parameters() map[string]any { return nil }
func (t *XSearchTool) Execute(ctx context.Context, args any) (any, error) { return "dispatched", nil }
func (t *XSearchTool) IsProviderTool() bool { return true }
func (t *XSearchTool) ProviderSchema(format string) map[string]any {
	if format == "openai" {
		return map[string]any{"type": "x_search"}
	}
	return nil
}

type FileSearchTool struct{ VectorStoreIDs []string; MaxNumResults int }
func (t *FileSearchTool) ID() string { return "xai_file_search" }
func (t *FileSearchTool) Name() string { return "xai_file_search" }
func (t *FileSearchTool) Description() string { return "Enable file search tool for searching uploaded document collections." }
func (t *FileSearchTool) Parameters() map[string]any { return nil }
func (t *FileSearchTool) Execute(ctx context.Context, args any) (any, error) { return "dispatched", nil }
func (t *FileSearchTool) IsProviderTool() bool { return true }
func (t *FileSearchTool) ProviderSchema(format string) map[string]any {
	if format == "openai" {
		return map[string]any{"type": "file_search"}
	}
	return nil
}

