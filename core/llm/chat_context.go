package llm

import (
	"time"
)

func (c *ChatContext) Copy() *ChatContext {
	newCtx := NewChatContext()
	newCtx.Items = append(newCtx.Items, c.Items...)
	return newCtx
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
