package llm

import (
	"testing"
)

func TestRemoteChatContext(t *testing.T) {
	ctx := NewRemoteChatContext()
	
	m1 := &ChatMessage{ID: "m1", Role: ChatRoleUser, Content: []ChatContent{{Text: "hi"}}}
	m2 := &ChatMessage{ID: "m2", Role: ChatRoleAssistant, Content: []ChatContent{{Text: "hello"}}}
	
	// Test Insert at head
	err := ctx.Insert(nil, m1)
	if err != nil {
		t.Fatalf("Insert m1 failed: %v", err)
	}
	
	// Test Insert after m1
	prevID := "m1"
	err = ctx.Insert(&prevID, m2)
	if err != nil {
		t.Fatalf("Insert m2 failed: %v", err)
	}
	
	// Test ToChatCtx
	chatCtx := ctx.ToChatCtx()
	if len(chatCtx.Items) != 2 {
		t.Errorf("Expected 2 items, got %d", len(chatCtx.Items))
	}
	if chatCtx.Items[1].GetID() != "m2" {
		t.Error("m2 should be second")
	}
	
	// Test Delete
	err = ctx.Delete("m1")
	if err != nil {
		t.Fatalf("Delete m1 failed: %v", err)
	}
	if ctx.head.item.GetID() != "m2" {
		t.Error("m2 should now be head")
	}
	
	// Test Get
	if ctx.Get("m2") == nil {
		t.Error("Get m2 failed")
	}
}
