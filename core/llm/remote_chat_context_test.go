package llm

import (
	"strings"
	"testing"
)

func TestRemoteChatContextInsertOrdersItemsLikeReference(t *testing.T) {
	ctx := NewRemoteChatContext()

	first := &ChatMessage{ID: "first", Role: ChatRoleUser, Content: []ChatContent{{Text: "first"}}}
	second := &ChatMessage{ID: "second", Role: ChatRoleAssistant, Content: []ChatContent{{Text: "second"}}}
	head := &ChatMessage{ID: "head", Role: ChatRoleUser, Content: []ChatContent{{Text: "head"}}}
	tail := &ChatMessage{ID: "tail", Role: ChatRoleAssistant, Content: []ChatContent{{Text: "tail"}}}

	if err := ctx.Insert(nil, first); err != nil {
		t.Fatalf("Insert(first) error = %v", err)
	}
	previousID := "first"
	if err := ctx.Insert(&previousID, second); err != nil {
		t.Fatalf("Insert(second) error = %v", err)
	}
	if err := ctx.Insert(nil, head); err != nil {
		t.Fatalf("Insert(head) error = %v", err)
	}
	previousID = "second"
	if err := ctx.Insert(&previousID, tail); err != nil {
		t.Fatalf("Insert(tail) error = %v", err)
	}

	if got := itemIDs(ctx.ToChatCtx().Items); got != "head,first,second,tail" {
		t.Fatalf("ToChatCtx item order = %q, want head,first,second,tail", got)
	}
	if got := ctx.Get("second"); got != second {
		t.Fatalf("Get(second) = %#v, want inserted item", got)
	}
}

func TestRemoteChatContextErrorsMatchReferenceMessages(t *testing.T) {
	ctx := NewRemoteChatContext()
	first := &ChatMessage{ID: "first", Role: ChatRoleUser, Content: []ChatContent{{Text: "first"}}}
	if err := ctx.Insert(nil, first); err != nil {
		t.Fatalf("Insert(first) error = %v", err)
	}

	if err := ctx.Insert(nil, &ChatMessage{ID: "first", Role: ChatRoleUser}); err == nil || err.Error() != "Item with ID first already exists." {
		t.Fatalf("Insert(duplicate) error = %v, want reference duplicate message", err)
	}

	missingPrevious := "missing"
	if err := ctx.Insert(&missingPrevious, &ChatMessage{ID: "second", Role: ChatRoleAssistant}); err == nil || !strings.Contains(err.Error(), "previous_item_id `missing` not found") {
		t.Fatalf("Insert(missing previous) error = %v, want reference missing previous message", err)
	}

	if err := ctx.Delete("missing"); err == nil || !strings.Contains(err.Error(), "item_id `missing` not found") {
		t.Fatalf("Delete(missing) error = %v, want reference missing item message", err)
	}
}
