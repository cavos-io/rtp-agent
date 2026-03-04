package google

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"iter"

	"github.com/cavos-io/conversation-worker/core/llm"
	"google.golang.org/genai"
)

type GoogleLLM struct {
	client *genai.Client
	model  string
}

func NewGoogleLLM(apiKey string, model string) (*GoogleLLM, error) {
	if model == "" {
		model = "gemini-2.5-flash"
	}
	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, err
	}
	return &GoogleLLM{
		client: client,
		model:  model,
	}, nil
}

func (l *GoogleLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	options := &llm.ChatOptions{}
	for _, opt := range opts {
		opt(options)
	}

	contents := make([]*genai.Content, 0)
	var systemInstructions string

	for _, item := range chatCtx.Items {
		if msg, ok := item.(*llm.ChatMessage); ok {
			if msg.Role == llm.ChatRoleSystem || msg.Role == llm.ChatRoleDeveloper {
				systemInstructions += msg.TextContent() + "\n"
				continue
			}

			role := genai.RoleUser
			if msg.Role == llm.ChatRoleAssistant {
				role = "model"
			}

			parts := make([]*genai.Part, 0)
			for _, content := range msg.Content {
				if content.Text != "" {
					parts = append(parts, genai.NewPartFromText(content.Text))
				}
				// We'd add image/audio parts here as needed
			}

			if len(parts) > 0 {
				contents = append(contents, genai.NewContentFromParts(parts, genai.Role(role)))
			}
		} else if fc, ok := item.(*llm.FunctionCall); ok {
			// Convert function call to model part
			args := make(map[string]any)
			json.Unmarshal([]byte(fc.Arguments), &args)
			contents = append(contents, genai.NewContentFromFunctionCall(fc.Name, args, "model"))
		} else if fco, ok := item.(*llm.FunctionCallOutput); ok {
			// Convert function response to user part
			resp := map[string]any{"output": fco.Output}
			contents = append(contents, genai.NewContentFromFunctionResponse(fco.Name, resp, genai.RoleUser))
		}
	}

	config := &genai.GenerateContentConfig{}
	if systemInstructions != "" {
		config.SystemInstruction = genai.NewContentFromText(systemInstructions, genai.RoleUser)
	}

	if len(options.Tools) > 0 {
		declarations := make([]*genai.FunctionDeclaration, 0)
		for _, t := range options.Tools {
			
			// Map parameters
			schemaMap := t.Parameters()
			var properties map[string]*genai.Schema
			
			if props, ok := schemaMap["properties"].(map[string]any); ok {
				properties = make(map[string]*genai.Schema)
				for k, v := range props {
					if typeMap, ok := v.(map[string]any); ok {
						typeStr, _ := typeMap["type"].(string)
						descStr, _ := typeMap["description"].(string)
						// Simplification for the example
						properties[k] = &genai.Schema{
							Type:        genai.Type(typeStr),
							Description: descStr,
						}
					}
				}
			}

			var required []string
			if reqs, ok := schemaMap["required"].([]any); ok {
				for _, r := range reqs {
					if reqStr, ok := r.(string); ok {
						required = append(required, reqStr)
					}
				}
			}

			declarations = append(declarations, &genai.FunctionDeclaration{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters: &genai.Schema{
					Type:       genai.TypeObject,
					Properties: properties,
					Required:   required,
				},
			})
		}
		
		config.Tools = []*genai.Tool{
			{FunctionDeclarations: declarations},
		}
	}

	stream := l.client.Models.GenerateContentStream(ctx, l.model, contents, config)
	
	next, stop := iter.Pull2(stream)

	return &googleLLMStream{
		next: next,
		stop: stop,
	}, nil
}

type googleLLMStream struct {
	next func() (*genai.GenerateContentResponse, error, bool)
	stop func()
}

func (s *googleLLMStream) Next() (*llm.ChatChunk, error) {
	resp, err, ok := s.next()
	if !ok {
		return nil, io.EOF
	}
	if err != nil {
		if errors.Is(err, genai.ErrPageDone) || errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, err
	}

	chunk := &llm.ChatChunk{
		Delta: &llm.ChoiceDelta{
			Role: llm.ChatRoleAssistant,
		},
	}

	if len(resp.Candidates) > 0 {
		cand := resp.Candidates[0]
		if cand.Content != nil {
			for _, part := range cand.Content.Parts {
				if part.Text != "" {
					chunk.Delta.Content += part.Text
				} else if part.FunctionCall != nil {
					args, _ := json.Marshal(part.FunctionCall.Args)
					chunk.Delta.ToolCalls = append(chunk.Delta.ToolCalls, llm.FunctionToolCall{
						Name:      part.FunctionCall.Name,
						Arguments: string(args),
						Type:      "function",
						CallID:    "call_" + part.FunctionCall.Name, // Gemini doesn't always provide CallID natively like OpenAI
					})
				}
			}
		}
	}

	if resp.UsageMetadata != nil {
		chunk.Usage = &llm.CompletionUsage{
			PromptTokens:     int(resp.UsageMetadata.PromptTokenCount),
			CompletionTokens: int(resp.UsageMetadata.CandidatesTokenCount),
			TotalTokens:      int(resp.UsageMetadata.TotalTokenCount),
		}
	}

	return chunk, nil
}

func (s *googleLLMStream) Close() error {
	if s.stop != nil {
		s.stop()
	}
	return nil
}
