package llm

import (
	"testing"
	"time"
)

func TestChatContext_Truncate(t *testing.T) {
	ctx := NewChatContext()
	
	// Add system instruction
	ctx.Append(&ChatMessage{
		Role:    ChatRoleSystem,
		Content: []ChatContent{{Text: "System Instructions"}},
		ID:      "system-1",
	})
	
	// Add 10 messages
	for i := 0; i < 10; i++ {
		ctx.Append(&ChatMessage{
			Role:    ChatRoleUser,
			Content: []ChatContent{{Text: "msg"}},
		})
	}
	
	ctx.Truncate(5)
	
	if len(ctx.Items) != 6 { // 5 messages + 1 system
		t.Errorf("Expected 6 items after truncation, got %d", len(ctx.Items))
	}
	
	if ctx.Items[0].GetID() != "system-1" {
		t.Error("System instruction should be preserved at the beginning")
	}
}

func TestChatContext_Merge(t *testing.T) {
	ctx1 := NewChatContext()
	ctx2 := NewChatContext()
	
	t1 := time.Now().Add(-1 * time.Hour)
	t2 := time.Now()
	
	ctx1.Append(&ChatMessage{ID: "m1", CreatedAt: t1, Content: []ChatContent{{Text: "m1"}}})
	ctx2.Append(&ChatMessage{ID: "m2", CreatedAt: t2, Content: []ChatContent{{Text: "m2"}}})
	
	ctx1.Merge(ctx2)
	
	if len(ctx1.Items) != 2 {
		t.Errorf("Expected 2 items after merge, got %d", len(ctx1.Items))
	}
	
	if ctx1.Items[1].GetID() != "m2" {
		t.Error("m2 should be at the end due to timestamp")
	}
}

func TestChatContext_ToProviderFormat(t *testing.T) {
	ctx := NewChatContext()
	ctx.Append(&ChatMessage{Role: ChatRoleUser, Content: []ChatContent{{Text: "hello"}}})
	ctx.Append(&FunctionCall{Name: "get_weather", CallID: "c1", Arguments: "{}"})
	ctx.Append(&FunctionCallOutput{Name: "get_weather", CallID: "c1", Output: "sunny"})
	
	// OpenAI
	res, _ := ctx.ToProviderFormat("openai")
	msgs := res.([]map[string]any)
	if len(msgs) != 3 {
		t.Errorf("Expected 3 OpenAI messages, got %d", len(msgs))
	}
	if msgs[2]["role"] != "tool" {
		t.Errorf("Expected tool role, got %v", msgs[2]["role"])
	}
	
	// Anthropic
	res, system := ctx.ToProviderFormat("anthropic")
	msgs = res.([]map[string]any)
	if len(msgs) != 3 {
		t.Errorf("Expected 3 Anthropic messages, got %d", len(msgs))
	}
	if system != "" {
		t.Error("System prompt should be empty")
	}
}
