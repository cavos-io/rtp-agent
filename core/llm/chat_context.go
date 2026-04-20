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

func (c *ChatContext) ToDict(excludeTimestamp bool) map[string]any {
	items := make([]map[string]any, 0)
	for _, item := range c.Items {
		var itemDict map[string]any
		switch it := item.(type) {
		case *ChatMessage:
			content := make([]map[string]any, 0)
			for _, c := range it.Content {
				content = append(content, map[string]any{
					"text": c.Text,
				})
			}
			itemDict = map[string]any{
				"type":    "message",
				"role":    string(it.Role),
				"content": content,
			}
		case *FunctionCall:
			itemDict = map[string]any{
				"type":      "function_call",
				"call_id":   it.CallID,
				"name":      it.Name,
				"arguments": it.Arguments,
			}
		case *FunctionCallOutput:
			itemDict = map[string]any{
				"type":    "function_call_output",
				"call_id": it.CallID,
				"name":    it.Name,
				"output":  it.Output,
				"error":   it.IsError,
			}
		case *AgentHandoff:
			itemDict = map[string]any{
				"type":         "agent_handoff",
				"old_agent_id": it.OldAgentID,
				"new_agent_id": it.NewAgentID,
			}
		}
		if itemDict != nil {
			if !excludeTimestamp {
				itemDict["created_at"] = float64(item.GetCreatedAt().UnixNano()) / 1e9
			}
			items = append(items, itemDict)
		}
	}
	return map[string]any{
		"items": items,
	}
}

func (c *ChatContext) ToProviderFormat(format string) (any, any) {
	switch format {
	case "openai", "groq":
		messages := make([]map[string]any, 0)
		for _, item := range c.Items {
			switch it := item.(type) {
			case *ChatMessage:
				msg := map[string]any{
					"role":    string(it.Role),
					"content": it.TextContent(),
				}
				if it.Role == ChatRoleDeveloper {
					msg["role"] = "system"
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

	case "anthropic":
		messages := make([]map[string]any, 0)
		var systemPrompt string
		for _, item := range c.Items {
			switch it := item.(type) {
			case *ChatMessage:
				if it.Role == ChatRoleSystem || it.Role == ChatRoleDeveloper {
					systemPrompt += it.TextContent() + "\n"
					continue
				}
				messages = append(messages, map[string]any{
					"role":    it.Role,
					"content": it.TextContent(),
				})
			case *FunctionCall:
				messages = append(messages, map[string]any{
					"role": "assistant",
					"content": []map[string]any{
						{
							"type": "tool_use",
							"id":   it.CallID,
							"name": it.Name,
							"input": it.Extra, // Expecting parsed map in Extra if possible
						},
					},
				})
			case *FunctionCallOutput:
				messages = append(messages, map[string]any{
					"role": "user",
					"content": []map[string]any{
						{
							"type":    "tool_result",
							"tool_use_id": it.CallID,
							"content": it.Output,
							"is_error": it.IsError,
						},
					},
				})
			}
		}
		return messages, systemPrompt

	case "google":
		contents := make([]map[string]any, 0)
		for _, item := range c.Items {
			switch it := item.(type) {
			case *ChatMessage:
				role := "user"
				if it.Role == ChatRoleAssistant {
					role = "model"
				}
				contents = append(contents, map[string]any{
					"role": role,
					"parts": []map[string]any{
						{"text": it.TextContent()},
					},
				})
			case *FunctionCall:
				contents = append(contents, map[string]any{
					"role": "model",
					"parts": []map[string]any{
						{
							"functionCall": map[string]any{
								"name": it.Name,
								"args": it.Extra,
							},
						},
					},
				})
			case *FunctionCallOutput:
				contents = append(contents, map[string]any{
					"role": "user",
					"parts": []map[string]any{
						{
							"functionResponse": map[string]any{
								"name": it.Name,
								"response": map[string]any{
									"content": it.Output,
								},
							},
						},
					},
				})
			}
		}
		return contents, nil
	}
	return nil, nil
}
