package llm

import (
	"time"
)

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

func (c *ChatContext) Merge(other *ChatContext) {
	existingIDs := make(map[string]struct{})
	for _, item := range c.Items {
		existingIDs[item.GetID()] = struct{}{}
	}

	for _, item := range other.Items {
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
		for _, item := range c.Items {
			switch it := item.(type) {
			case *ChatMessage:
				msg := map[string]any{
					"role":    string(it.Role),
					"content": it.TextContent(),
				}
				messages = append(messages, msg)
			case *FunctionCall:
				msg := map[string]any{
					"role": "assistant",
					"tool_calls": []map[string]any{
						{
							"id":   it.CallID,
							"type": "function",
							"function": map[string]any{
								"name":      it.Name,
								"arguments": it.Arguments,
							},
						},
					},
				}
				messages = append(messages, msg)
			case *FunctionCallOutput:
				msg := map[string]any{
					"role":         "tool",
					"tool_call_id": it.CallID,
					"content":      it.Output,
				}
				messages = append(messages, msg)
			}
		}
		return messages, nil
	}
	return nil, nil
}
