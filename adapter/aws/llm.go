package aws

import (
	"context"
	"encoding/json"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type AWSLLM struct {
	client *bedrockruntime.Client
	model  string
}

func NewAWSLLM(ctx context.Context, region string, model string) (*AWSLLM, error) {
	if model == "" {
		model = "anthropic.claude-3-haiku-20240307-v1:0"
	}
	
	opts := []func(*config.LoadOptions) error{}
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}

	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}

	return &AWSLLM{
		client: bedrockruntime.NewFromConfig(cfg),
		model:  model,
	}, nil
}

func (l *AWSLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	options := &llm.ChatOptions{}
	for _, opt := range opts {
		opt(options)
	}

	messages := make([]types.Message, 0)
	var systemText string

	for _, item := range chatCtx.Items {
		if msg, ok := item.(*llm.ChatMessage); ok {
			if msg.Role == llm.ChatRoleSystem || msg.Role == llm.ChatRoleDeveloper {
				systemText += msg.TextContent() + "\n"
				continue
			}

			role := types.ConversationRoleUser
			if msg.Role == llm.ChatRoleAssistant {
				role = types.ConversationRoleAssistant
			}

			contentBlocks := make([]types.ContentBlock, 0)
			for _, c := range msg.Content {
				if c.Text != "" {
					contentBlocks = append(contentBlocks, &types.ContentBlockMemberText{Value: c.Text})
				}
			}

			if len(contentBlocks) > 0 {
				messages = append(messages, types.Message{
					Role:    role,
					Content: contentBlocks,
				})
			}
		} else if fc, ok := item.(*llm.FunctionCall); ok {
			var args map[string]interface{}
			json.Unmarshal([]byte(fc.Arguments), &args)

			doc := document.NewLazyDocument(args)

			messages = append(messages, types.Message{
				Role: types.ConversationRoleAssistant,
				Content: []types.ContentBlock{
					&types.ContentBlockMemberToolUse{
						Value: types.ToolUseBlock{
							ToolUseId: aws.String(fc.CallID),
							Name:      aws.String(fc.Name),
							Input:     doc,
						},
					},
				},
			})
		} else if fco, ok := item.(*llm.FunctionCallOutput); ok {
			doc := document.NewLazyDocument(map[string]interface{}{
				"output": fco.Output,
			})
			status := types.ToolResultStatusSuccess
			if fco.IsError {
				status = types.ToolResultStatusError
			}

			messages = append(messages, types.Message{
				Role: types.ConversationRoleUser,
				Content: []types.ContentBlock{
					&types.ContentBlockMemberToolResult{
						Value: types.ToolResultBlock{
							ToolUseId: aws.String(fco.CallID),
							Status:    status,
							Content: []types.ToolResultContentBlock{
								&types.ToolResultContentBlockMemberJson{
									Value: doc,
								},							},
						},
					},
				},
			})
		}
	}

	req := &bedrockruntime.ConverseStreamInput{
		ModelId:  aws.String(l.model),
		Messages: messages,
	}

	if systemText != "" {
		req.System = []types.SystemContentBlock{
			&types.SystemContentBlockMemberText{Value: systemText},
		}
	}

	if len(options.Tools) > 0 {
		toolSpecs := make([]types.Tool, 0)
		tc := llm.NewToolContext(options.Tools)
		schemas := tc.ParseFunctionTools("aws")

		for _, schema := range schemas {
			if specAny, ok := schema["toolSpec"]; ok {
				if spec, ok := specAny.(map[string]any); ok {
					name, _ := spec["name"].(string)
					desc, _ := spec["description"].(string)
					
					var jsonSchema any
					if inSchemaAny, ok := spec["inputSchema"]; ok {
						if inSchema, ok := inSchemaAny.(map[string]any); ok {
							jsonSchema = inSchema["json"]
						}
					}
					
					doc := document.NewLazyDocument(jsonSchema)
					toolSpecs = append(toolSpecs, &types.ToolMemberToolSpec{
						Value: types.ToolSpecification{
							Name:        aws.String(name),
							Description: aws.String(desc),
							InputSchema: &types.ToolInputSchemaMemberJson{
								Value: doc,
							},
						},
					})
				}
			}
		}
		req.ToolConfig = &types.ToolConfiguration{
			Tools: toolSpecs,
		}
	}

	out, err := l.client.ConverseStream(ctx, req)
	if err != nil {
		return nil, err
	}

	return &awsLLMStream{
		stream: out.GetStream(),
	}, nil
}

type awsLLMStream struct {
	stream *bedrockruntime.ConverseStreamEventStream
	closed bool
}

func (s *awsLLMStream) Next() (*llm.ChatChunk, error) {
	if s.closed {
		return nil, io.EOF
	}

	for {
		event := <-s.stream.Events()
		if event == nil {
			if err := s.stream.Err(); err != nil {
				return nil, err
			}
			return nil, io.EOF
		}

		chunk := &llm.ChatChunk{
			Delta: &llm.ChoiceDelta{
				Role: llm.ChatRoleAssistant,
			},
		}

		switch v := event.(type) {
		case *types.ConverseStreamOutputMemberContentBlockDelta:
			if textDelta, ok := v.Value.Delta.(*types.ContentBlockDeltaMemberText); ok {
				chunk.Delta.Content = textDelta.Value
				return chunk, nil
			}
			if toolDelta, ok := v.Value.Delta.(*types.ContentBlockDeltaMemberToolUse); ok {
				// Bedrock tool calls can be chunked.
				// Aggregate tool call deltas correctly.
				chunk.Delta.ToolCalls = append(chunk.Delta.ToolCalls, llm.FunctionToolCall{
					Arguments: aws.ToString(toolDelta.Value.Input),
				})
				return chunk, nil
			}
		case *types.ConverseStreamOutputMemberContentBlockStart:
			if toolStart, ok := v.Value.Start.(*types.ContentBlockStartMemberToolUse); ok {
				chunk.Delta.ToolCalls = append(chunk.Delta.ToolCalls, llm.FunctionToolCall{
					CallID: aws.ToString(toolStart.Value.ToolUseId),
					Name:   aws.ToString(toolStart.Value.Name),
					Type:   "function",
				})
				return chunk, nil
			}
		case *types.ConverseStreamOutputMemberMetadata:
			if v.Value.Usage != nil {
				chunk.Usage = &llm.CompletionUsage{
					PromptTokens:     int(aws.ToInt32(v.Value.Usage.InputTokens)),
					CompletionTokens: int(aws.ToInt32(v.Value.Usage.OutputTokens)),
					TotalTokens:      int(aws.ToInt32(v.Value.Usage.TotalTokens)),
				}
				return chunk, nil
			}
		case *types.ConverseStreamOutputMemberMessageStop:
			s.closed = true
			// We can return a final chunk
			return chunk, nil
		}
	}
}

func (s *awsLLMStream) Close() error {
	s.stream.Close()
	s.closed = true
	return nil
}

