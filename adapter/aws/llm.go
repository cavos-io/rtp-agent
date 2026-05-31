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
	"github.com/cavos-io/conversation-worker/core/llm"
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

	messages, systemText := buildAWSMessages(chatCtx)

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
		for _, t := range options.Tools {
			doc := document.NewLazyDocument(t.Parameters())
			toolSpecs = append(toolSpecs, &types.ToolMemberToolSpec{
				Value: types.ToolSpecification{
					Name:        aws.String(t.Name()),
					Description: aws.String(t.Description()),
					InputSchema: &types.ToolInputSchemaMemberJson{
						Value: doc,
					},
				},
			})
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

func buildAWSMessages(chatCtx *llm.ChatContext) ([]types.Message, string) {
	messages := make([]types.Message, 0, len(chatCtx.Items))
	var systemText string
	var currentRole *types.ConversationRole
	currentContent := make([]types.ContentBlock, 0)

	flush := func() {
		if currentRole == nil || len(currentContent) == 0 {
			return
		}
		messages = append(messages, types.Message{
			Role:    *currentRole,
			Content: currentContent,
		})
		currentContent = nil
	}

	appendBlock := func(role types.ConversationRole, blocks ...types.ContentBlock) {
		if currentRole == nil || *currentRole != role {
			flush()
			currentRole = &role
			currentContent = make([]types.ContentBlock, 0, len(blocks))
		}
		currentContent = append(currentContent, blocks...)
	}

	for _, group := range groupAWSChatItems(chatCtx.Items) {
		for _, item := range group.flatten() {
			switch msg := item.(type) {
			case *llm.ChatMessage:
				if msg.Role == llm.ChatRoleSystem || msg.Role == llm.ChatRoleDeveloper {
					if text := msg.TextContent(); text != "" {
						systemText += text + "\n"
					}
					continue
				}
				role := types.ConversationRoleUser
				if msg.Role == llm.ChatRoleAssistant {
					role = types.ConversationRoleAssistant
				}
				blocks := awsMessageContentBlocks(msg)
				if len(blocks) > 0 {
					appendBlock(role, blocks...)
				}
			case *llm.FunctionCall:
				appendBlock(types.ConversationRoleAssistant, awsToolUseBlock(msg))
			case *llm.FunctionCallOutput:
				appendBlock(types.ConversationRoleUser, awsToolResultBlock(msg))
			}
		}
	}
	flush()

	if len(messages) == 0 || messages[0].Role != types.ConversationRoleUser {
		messages = append([]types.Message{
			{
				Role:    types.ConversationRoleUser,
				Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: "(empty)"}},
			},
		}, messages...)
	}

	return messages, systemText
}

func awsMessageContentBlocks(msg *llm.ChatMessage) []types.ContentBlock {
	blocks := make([]types.ContentBlock, 0, len(msg.Content))
	for _, c := range msg.Content {
		if c.Text != "" {
			blocks = append(blocks, &types.ContentBlockMemberText{Value: c.Text})
		}
	}
	return blocks
}

func awsToolUseBlock(fc *llm.FunctionCall) types.ContentBlock {
	var args map[string]interface{}
	_ = json.Unmarshal([]byte(fc.Arguments), &args)
	if args == nil {
		args = map[string]interface{}{}
	}

	return &types.ContentBlockMemberToolUse{
		Value: types.ToolUseBlock{
			ToolUseId: aws.String(fc.CallID),
			Name:      aws.String(fc.Name),
			Input:     document.NewLazyDocument(args),
		},
	}
}

func awsToolResultBlock(fco *llm.FunctionCallOutput) types.ContentBlock {
	status := types.ToolResultStatusSuccess
	if fco.IsError {
		status = types.ToolResultStatusError
	}

	return &types.ContentBlockMemberToolResult{
		Value: types.ToolResultBlock{
			ToolUseId: aws.String(fco.CallID),
			Status:    status,
			Content: []types.ToolResultContentBlock{
				&types.ToolResultContentBlockMemberJson{
					Value: document.NewLazyDocument(map[string]interface{}{
						"output": fco.Output,
					}),
				},
			},
		},
	}
}

type awsChatItemGroup struct {
	message     *llm.ChatMessage
	toolCalls   []*llm.FunctionCall
	toolOutputs []*llm.FunctionCallOutput
}

func groupAWSChatItems(items []llm.ChatItem) []*awsChatItemGroup {
	groups := make([]*awsChatItemGroup, 0)
	groupsByID := make(map[string]*awsChatItemGroup)
	toolOutputs := make([]*llm.FunctionCallOutput, 0)

	addToGroup := func(groupID string, item llm.ChatItem) {
		group := groupsByID[groupID]
		if group == nil {
			group = &awsChatItemGroup{}
			groupsByID[groupID] = group
			groups = append(groups, group)
		}
		group.add(item)
	}

	for _, item := range items {
		switch it := item.(type) {
		case *llm.ChatMessage:
			if it.Role == llm.ChatRoleAssistant {
				addToGroup(awsGroupID(it.ID, nil), it)
			} else {
				addToGroup(it.ID, it)
			}
		case *llm.FunctionCall:
			addToGroup(awsGroupID(it.ID, it.GroupID), it)
		case *llm.FunctionCallOutput:
			toolOutputs = append(toolOutputs, it)
		}
	}

	groupsByCallID := make(map[string]*awsChatItemGroup)
	for _, group := range groups {
		for _, toolCall := range group.toolCalls {
			groupsByCallID[toolCall.CallID] = group
		}
	}
	for _, toolOutput := range toolOutputs {
		if group := groupsByCallID[toolOutput.CallID]; group != nil {
			group.add(toolOutput)
		}
	}
	for _, group := range groups {
		group.removeInvalidToolItems()
	}
	return groups
}

func (g *awsChatItemGroup) add(item llm.ChatItem) {
	switch it := item.(type) {
	case *llm.ChatMessage:
		g.message = it
	case *llm.FunctionCall:
		g.toolCalls = append(g.toolCalls, it)
	case *llm.FunctionCallOutput:
		g.toolOutputs = append(g.toolOutputs, it)
	}
}

func (g *awsChatItemGroup) flatten() []llm.ChatItem {
	items := make([]llm.ChatItem, 0, 1+len(g.toolCalls)+len(g.toolOutputs))
	if g.message != nil {
		items = append(items, g.message)
	}
	for _, toolCall := range g.toolCalls {
		items = append(items, toolCall)
	}
	for _, toolOutput := range g.toolOutputs {
		items = append(items, toolOutput)
	}
	return items
}

func (g *awsChatItemGroup) removeInvalidToolItems() {
	if len(g.toolCalls) == len(g.toolOutputs) {
		return
	}

	outputsByCallID := make(map[string]*llm.FunctionCallOutput)
	for _, toolOutput := range g.toolOutputs {
		outputsByCallID[toolOutput.CallID] = toolOutput
	}

	validCalls := make([]*llm.FunctionCall, 0, len(g.toolCalls))
	validOutputs := make([]*llm.FunctionCallOutput, 0, len(g.toolOutputs))
	for _, toolCall := range g.toolCalls {
		if toolOutput := outputsByCallID[toolCall.CallID]; toolOutput != nil {
			validCalls = append(validCalls, toolCall)
			validOutputs = append(validOutputs, toolOutput)
		}
	}

	g.toolCalls = validCalls
	g.toolOutputs = validOutputs
}

func awsGroupID(itemID string, groupID *string) string {
	if groupID != nil && *groupID != "" {
		return *groupID
	}
	for i, r := range itemID {
		if r == '/' {
			return itemID[:i]
		}
	}
	return itemID
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
