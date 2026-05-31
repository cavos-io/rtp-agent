package llm

import (
	"time"
)

type ChatMessageArgs struct {
	ID          string
	Role        ChatRole
	Content     []ChatContent
	Interrupted bool
	CreatedAt   time.Time
	Extra       map[string]any
}

type ChatContextCopyOptions struct {
	ExcludeFunctionCall bool
	ExcludeInstructions bool
	ExcludeEmptyMessage bool
	ExcludeHandoff      bool
	ExcludeConfigUpdate bool
	Tools               []interface{}
}

func (c *ChatContext) Copy(options ...ChatContextCopyOptions) *ChatContext {
	var opts ChatContextCopyOptions
	if len(options) > 0 {
		opts = options[0]
	}

	newCtx := NewChatContext()
	validTools := chatContextCopyToolNames(opts.Tools)
	filterByTools := opts.Tools != nil
	for _, item := range c.Items {
		if opts.ExcludeFunctionCall && isFunctionChatItem(item) {
			continue
		}
		if opts.ExcludeInstructions && isInstructionMessage(item) {
			continue
		}
		if opts.ExcludeEmptyMessage && isEmptyMessage(item) {
			continue
		}
		if opts.ExcludeHandoff && item.GetType() == "agent_handoff" {
			continue
		}
		if opts.ExcludeConfigUpdate && item.GetType() == "agent_config_update" {
			continue
		}
		if filterByTools && isFunctionChatItem(item) {
			if _, ok := validTools[functionChatItemName(item)]; !ok {
				continue
			}
		}
		newCtx.Items = append(newCtx.Items, item)
	}
	return newCtx
}

func (c *ChatContext) AddMessage(args ChatMessageArgs) *ChatMessage {
	createdAt := args.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	message := &ChatMessage{
		ID:          args.ID,
		Role:        args.Role,
		Content:     args.Content,
		Interrupted: args.Interrupted,
		Extra:       args.Extra,
		CreatedAt:   createdAt,
	}
	if args.CreatedAt.IsZero() {
		c.Append(message)
		return message
	}
	c.Insert(message)
	return message
}

func (c *ChatContext) Insert(items ...ChatItem) {
	for _, item := range items {
		idx := c.FindInsertionIndex(item.GetCreatedAt())
		c.Items = append(c.Items[:idx], append([]ChatItem{item}, c.Items[idx:]...)...)
	}
}

func (c *ChatContext) GetByID(itemID string) ChatItem {
	for _, item := range c.Items {
		if item.GetID() == itemID {
			return item
		}
	}
	return nil
}

func (c *ChatContext) IndexByID(itemID string) *int {
	for i, item := range c.Items {
		if item.GetID() == itemID {
			return &i
		}
	}
	return nil
}

func chatContextCopyToolNames(tools []interface{}) map[string]struct{} {
	names := make(map[string]struct{})
	for _, tool := range tools {
		switch t := tool.(type) {
		case string:
			names[t] = struct{}{}
		case Tool:
			names[t.Name()] = struct{}{}
		case Toolset:
			for _, childTool := range t.Tools() {
				names[childTool.Name()] = struct{}{}
			}
		}
	}
	return names
}

func isFunctionChatItem(item ChatItem) bool {
	return item.GetType() == "function_call" || item.GetType() == "function_call_output"
}

func isInstructionMessage(item ChatItem) bool {
	msg, ok := item.(*ChatMessage)
	return ok && (msg.Role == ChatRoleSystem || msg.Role == ChatRoleDeveloper)
}

func isEmptyMessage(item ChatItem) bool {
	msg, ok := item.(*ChatMessage)
	return ok && len(msg.Content) == 0
}

func functionChatItemName(item ChatItem) string {
	switch it := item.(type) {
	case *FunctionCall:
		return it.Name
	case *FunctionCallOutput:
		return it.Name
	default:
		return ""
	}
}

func (c *ChatContext) Messages() []*ChatMessage {
	var msgs []*ChatMessage
	for _, item := range c.Items {
		if msg, ok := item.(*ChatMessage); ok {
			msgs = append(msgs, msg)
		}
	}
	return msgs
}

func (c *ChatContext) Truncate(maxItems int) {
	if len(c.Items) <= maxItems {
		return
	}

	var instructions ChatItem
	for _, item := range c.Items {
		if msg, ok := item.(*ChatMessage); ok && (msg.Role == ChatRoleSystem || msg.Role == ChatRoleDeveloper) {
			instructions = item
			break
		}
	}

	newItems := c.Items[len(c.Items)-maxItems:]

	// Don't start with function calls to avoid partial sequences
	for len(newItems) > 0 {
		_, isMsg := newItems[0].(*ChatMessage)
		if isMsg {
			break
		}
		newItems = newItems[1:]
	}

	if instructions != nil {
		found := false
		for _, item := range newItems {
			if item.GetID() == instructions.GetID() {
				found = true
				break
			}
		}
		if !found {
			newItems = append([]ChatItem{instructions}, newItems...)
		}
	}

	c.Items = newItems
}

type ChatContextMergeOptions struct {
	ExcludeFunctionCall bool
	ExcludeInstructions bool
	ExcludeConfigUpdate bool
}

func (c *ChatContext) Merge(other *ChatContext, options ...ChatContextMergeOptions) {
	var opts ChatContextMergeOptions
	if len(options) > 0 {
		opts = options[0]
	}

	existingIDs := make(map[string]struct{})
	for _, item := range c.Items {
		existingIDs[item.GetID()] = struct{}{}
	}

	for _, item := range other.Items {
		if opts.ExcludeFunctionCall && isFunctionChatItem(item) {
			continue
		}
		if opts.ExcludeInstructions && isInstructionMessage(item) {
			continue
		}
		if opts.ExcludeConfigUpdate && item.GetType() == "agent_config_update" {
			continue
		}
		if _, ok := existingIDs[item.GetID()]; !ok {
			idx := c.FindInsertionIndex(item.GetCreatedAt())
			c.Items = append(c.Items[:idx], append([]ChatItem{item}, c.Items[idx:]...)...)
			existingIDs[item.GetID()] = struct{}{}
		}
	}
}

func (c *ChatContext) FindInsertionIndex(createdAt time.Time) int {
	for i := len(c.Items) - 1; i >= 0; i-- {
		if !c.Items[i].GetCreatedAt().After(createdAt) {
			return i + 1
		}
	}
	return 0
}

func (c *ChatContext) ToProviderFormat(format string) ([]map[string]any, any) {
	if format == "openai" {
		messages := make([]map[string]any, 0)
		for _, group := range groupOpenAIToolCalls(c.Items) {
			if group.message == nil && len(group.toolCalls) == 0 && len(group.toolOutputs) == 0 {
				continue
			}

			var msg map[string]any
			if group.message != nil {
				msg = openAIChatMessage(group.message)
			} else {
				msg = map[string]any{"role": "assistant"}
			}

			if len(group.toolCalls) > 0 {
				toolCalls := make([]map[string]any, 0, len(group.toolCalls))
				for _, toolCall := range group.toolCalls {
					toolCalls = append(toolCalls, openAIToolCall(toolCall))
				}
				msg["tool_calls"] = toolCalls
			}
			messages = append(messages, msg)

			for _, toolOutput := range group.toolOutputs {
				messages = append(messages, openAIToolOutput(toolOutput))
			}
		}
		return messages, nil
	}
	return nil, nil
}

type openAIToolCallGroup struct {
	message     *ChatMessage
	toolCalls   []*FunctionCall
	toolOutputs []*FunctionCallOutput
}

func groupOpenAIToolCalls(items []ChatItem) []*openAIToolCallGroup {
	groups := make([]*openAIToolCallGroup, 0)
	groupsByID := make(map[string]*openAIToolCallGroup)
	toolOutputs := make([]*FunctionCallOutput, 0)

	addToGroup := func(groupID string, item ChatItem) {
		group := groupsByID[groupID]
		if group == nil {
			group = &openAIToolCallGroup{}
			groupsByID[groupID] = group
			groups = append(groups, group)
		}
		group.add(item)
	}

	for _, item := range items {
		switch it := item.(type) {
		case *ChatMessage:
			if it.Role == ChatRoleAssistant {
				addToGroup(openAIToolGroupID(it.ID, nil), it)
			} else {
				addToGroup(it.ID, it)
			}
		case *FunctionCall:
			addToGroup(openAIToolGroupID(it.ID, it.GroupID), it)
		case *FunctionCallOutput:
			toolOutputs = append(toolOutputs, it)
		}
	}

	groupsByCallID := make(map[string]*openAIToolCallGroup)
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

func (g *openAIToolCallGroup) add(item ChatItem) {
	switch it := item.(type) {
	case *ChatMessage:
		g.message = it
	case *FunctionCall:
		g.toolCalls = append(g.toolCalls, it)
	case *FunctionCallOutput:
		g.toolOutputs = append(g.toolOutputs, it)
	}
}

func (g *openAIToolCallGroup) removeInvalidToolItems() {
	if len(g.toolCalls) == len(g.toolOutputs) {
		return
	}

	outputsByCallID := make(map[string]*FunctionCallOutput)
	for _, toolOutput := range g.toolOutputs {
		outputsByCallID[toolOutput.CallID] = toolOutput
	}

	validCalls := make([]*FunctionCall, 0, len(g.toolCalls))
	validOutputs := make([]*FunctionCallOutput, 0, len(g.toolOutputs))
	for _, toolCall := range g.toolCalls {
		if toolOutput := outputsByCallID[toolCall.CallID]; toolOutput != nil {
			validCalls = append(validCalls, toolCall)
			validOutputs = append(validOutputs, toolOutput)
		}
	}

	g.toolCalls = validCalls
	g.toolOutputs = validOutputs
}

func openAIToolGroupID(itemID string, groupID *string) string {
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

func openAIChatMessage(msg *ChatMessage) map[string]any {
	return map[string]any{
		"role":    string(msg.Role),
		"content": msg.TextContent(),
	}
}

func openAIToolCall(toolCall *FunctionCall) map[string]any {
	return map[string]any{
		"id":   toolCall.CallID,
		"type": "function",
		"function": map[string]any{
			"name":      toolCall.Name,
			"arguments": toolCall.Arguments,
		},
	}
}

func openAIToolOutput(toolOutput *FunctionCallOutput) map[string]any {
	return map[string]any{
		"role":         "tool",
		"tool_call_id": toolOutput.CallID,
		"content":      toolOutput.Output,
	}
}
