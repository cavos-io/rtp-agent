package anthropic

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

type AnthropicLLM struct {
	apiKey string
	model  string
}

func NewAnthropicLLM(apiKey string, model string) (*AnthropicLLM, error) {
	if model == "" {
		model = "claude-3-5-sonnet-20241022" // Parity with python defaults
	}
	return &AnthropicLLM{
		apiKey: apiKey,
		model:  model,
	}, nil
}

func (l *AnthropicLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	options := &llm.ChatOptions{}
	for _, opt := range opts {
		opt(options)
	}

	messages, system := chatCtx.ToProviderFormat("anthropic")

	body := map[string]interface{}{
		"model":      l.model,
		"messages":   messages,
		"max_tokens": 1024,
		"stream":     true,
	}
	if system != "" && system != nil {
		body["system"] = system
	}

	// Tool support
	if len(options.Tools) > 0 {
		tc := llm.NewToolContext(options.Tools)
		body["tools"] = tc.ParseFunctionTools("anthropic")
	}

	jsonBody, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", l.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("anthropic error: %s", string(respBody))
	}

	return &anthropicStream{
		resp:   resp,
		reader: bufio.NewReader(resp.Body),
	}, nil
}

type anthropicStream struct {
	resp   *http.Response
	reader *bufio.Reader

	// internal states for tracking tool calls over multiple chunks
	toolCallID string
	toolName   string
	toolArgs   string
}

func (s *anthropicStream) Next() (*llm.ChatChunk, error) {
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil, io.EOF
			}
			return nil, err
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
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
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
			continue
		}

		switch event.Type {
		case "message_start":
			return &llm.ChatChunk{
				ID: event.Message.ID,
				Usage: &llm.CompletionUsage{
					PromptTokens: event.Message.Usage.InputTokens,
				},
			}, nil

		case "content_block_start":
			if event.ContentBlock.Type == "tool_use" {
				s.toolCallID = event.ContentBlock.ID
				s.toolName = event.ContentBlock.Name
				s.toolArgs = ""
			}

		case "content_block_delta":
			if event.Delta.Type == "text_delta" {
				return &llm.ChatChunk{
					Delta: &llm.ChoiceDelta{
						Role:    llm.ChatRoleAssistant,
						Content: event.Delta.Text,
					},
				}, nil
			} else if event.Delta.Type == "input_json_delta" {
				s.toolArgs += event.Delta.PartialJson
			}

		case "content_block_stop":
			if s.toolCallID != "" {
				chunk := &llm.ChatChunk{
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
				s.toolCallID = ""
				s.toolName = ""
				s.toolArgs = ""
				return chunk, nil
			}

		case "message_delta":
			return &llm.ChatChunk{
				Usage: &llm.CompletionUsage{
					CompletionTokens: event.Usage.OutputTokens,
				},
			}, nil

		case "message_stop":
			return nil, io.EOF

		case "error":
			return nil, fmt.Errorf("anthropic stream error: %s", data)
		}
	}
}

func (s *anthropicStream) Close() error {
	return s.resp.Body.Close()
}

