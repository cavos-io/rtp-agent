package llm

import "testing"

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

func TestRemoteChatContextDeleteRewiresItemsLikeReference(t *testing.T) {
	ctx := NewRemoteChatContext()

	for _, item := range []*ChatMessage{
		{ID: "first", Role: ChatRoleUser, Content: []ChatContent{{Text: "first"}}},
		{ID: "second", Role: ChatRoleAssistant, Content: []ChatContent{{Text: "second"}}},
		{ID: "third", Role: ChatRoleUser, Content: []ChatContent{{Text: "third"}}},
		{ID: "fourth", Role: ChatRoleAssistant, Content: []ChatContent{{Text: "fourth"}}},
	} {
		previous := ""
		if got := ctx.ToChatCtx().Items; len(got) > 0 {
			previous = got[len(got)-1].GetID()
		}
		var previousID *string
		if previous != "" {
			previousID = &previous
		}
		if err := ctx.Insert(previousID, item); err != nil {
			t.Fatalf("Insert(%s) error = %v", item.ID, err)
		}
	}

	for _, id := range []string{"first", "third", "fourth"} {
		if err := ctx.Delete(id); err != nil {
			t.Fatalf("Delete(%s) error = %v", id, err)
		}
	}

	if got := itemIDs(ctx.ToChatCtx().Items); got != "second" {
		t.Fatalf("ToChatCtx item order after deletes = %q, want second", got)
	}
	if got := ctx.Get("first"); got != nil {
		t.Fatalf("Get(first) = %#v, want deleted item missing", got)
	}
	if got := ctx.Get("second"); got == nil || got.GetID() != "second" {
		t.Fatalf("Get(second) = %#v, want surviving second item", got)
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
	err := ctx.Insert(&missingPrevious, &ChatMessage{ID: "second", Role: ChatRoleAssistant})
	if err == nil {
		t.Fatal("Insert(missing previous) error = nil, want reference missing previous message")
	}
	if got, want := err.Error(), "previous_item_id `missing` not found"; got != want {
		t.Fatalf("Insert(missing previous) error = %q, want %q", got, want)
	}

	err = ctx.Delete("missing")
	if err == nil {
		t.Fatal("Delete(missing) error = nil, want reference missing item message")
	}
	if got, want := err.Error(), "item_id `missing` not found"; got != want {
		t.Fatalf("Delete(missing) error = %q, want %q", got, want)
	}
}
