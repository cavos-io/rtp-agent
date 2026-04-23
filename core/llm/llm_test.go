package llm

import (
	"context"
	"testing"
	"time"

)

func TestChatMessage_TextContent(t *testing.T) {
	msg := &ChatMessage{
		Content: []ChatContent{
			{Text: "Hello"},
			{Text: "world"},
		},
	}
	expected := "Hello\nworld"
	if msg.TextContent() != expected {
		t.Errorf("Expected %q, got %q", expected, msg.TextContent())
	}
}

func TestChatContext_Append(t *testing.T) {
	ctx := NewChatContext()
	itemAdded := false
	ctx.OnItemAdded = func(item ChatItem) {
		itemAdded = true
	}

	msg := &ChatMessage{
		ID:        "msg-1",
		Role:      ChatRoleUser,
		Content:   []ChatContent{{Text: "Hi"}},
		CreatedAt: time.Now(),
	}

	ctx.Append(msg)

	if len(ctx.Items) != 1 {
		t.Errorf("Expected 1 item, got %d", len(ctx.Items))
	}
	if !itemAdded {
		t.Errorf("OnItemAdded was not called")
	}
}

type mockLLM struct {
	chatFunc func(ctx context.Context, chatCtx *ChatContext, opts ...ChatOption) (LLMStream, error)
}

func (m *mockLLM) Chat(ctx context.Context, chatCtx *ChatContext, opts ...ChatOption) (LLMStream, error) {
	return m.chatFunc(ctx, chatCtx, opts...)
}

func TestFallbackAdapter_Chat(t *testing.T) {
	l1 := &mockLLM{
		chatFunc: func(ctx context.Context, chatCtx *ChatContext, opts ...ChatOption) (LLMStream, error) {
			return nil, context.DeadlineExceeded
		},
	}
	l2 := &mockLLM{
		chatFunc: func(ctx context.Context, chatCtx *ChatContext, opts ...ChatOption) (LLMStream, error) {
			return nil, nil // Success (stream is nil for simplicity)
		},
	}

	fallback := NewFallbackAdapter([]LLM{l1, l2})
	_, err := fallback.Chat(context.Background(), NewChatContext())
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
}
