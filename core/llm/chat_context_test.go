package llm

import (
	"strings"
	"testing"
	"time"
)

func itemIDs(items []ChatItem) string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.GetID())
	}
	return strings.Join(ids, ",")
}

func TestChatContextCopyFiltersReferenceItemTypes(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{ID: "system", Role: ChatRoleSystem, Content: []ChatContent{{Text: "instructions"}}},
		&ChatMessage{ID: "empty", Role: ChatRoleUser},
		&ChatMessage{ID: "user", Role: ChatRoleUser, Content: []ChatContent{{Text: "hello"}}},
		&FunctionCall{ID: "call", Name: "lookup"},
		&FunctionCallOutput{ID: "output", Name: "lookup"},
		&AgentHandoff{ID: "handoff", NewAgentID: "next"},
		&AgentConfigUpdate{ID: "config"},
	}

	copied := ctx.Copy(ChatContextCopyOptions{
		ExcludeFunctionCall: true,
		ExcludeInstructions: true,
		ExcludeEmptyMessage: true,
		ExcludeHandoff:      true,
		ExcludeConfigUpdate: true,
	})

	if got, want := itemIDs(copied.Items), "user"; got != want {
		t.Fatalf("Copy() item IDs = %q, want %q", got, want)
	}
}

func TestChatContextCopyFiltersFunctionItemsByTools(t *testing.T) {
	lookup := &testTool{id: "lookup", name: "lookup"}
	weather := &testTool{id: "weather", name: "weather"}
	toolset := &testToolset{id: "tools", tools: []Tool{weather}}
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&FunctionCall{ID: "lookup-call", Name: "lookup"},
		&FunctionCallOutput{ID: "lookup-output", Name: "lookup"},
		&FunctionCall{ID: "weather-call", Name: "weather"},
		&FunctionCallOutput{ID: "weather-output", Name: "weather"},
		&FunctionCall{ID: "calendar-call", Name: "calendar"},
		&FunctionCallOutput{ID: "calendar-output", Name: "calendar"},
	}

	copied := ctx.Copy(ChatContextCopyOptions{
		Tools: []interface{}{"calendar", lookup, toolset},
	})

	if got, want := itemIDs(copied.Items), "lookup-call,lookup-output,weather-call,weather-output,calendar-call,calendar-output"; got != want {
		t.Fatalf("Copy() item IDs = %q, want %q", got, want)
	}
}

func TestChatContextCopyFiltersOutFunctionItemsOutsideTools(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{ID: "user", Role: ChatRoleUser, Content: []ChatContent{{Text: "hello"}}},
		&FunctionCall{ID: "lookup-call", Name: "lookup"},
		&FunctionCallOutput{ID: "lookup-output", Name: "lookup"},
		&FunctionCall{ID: "calendar-call", Name: "calendar"},
		&FunctionCallOutput{ID: "calendar-output", Name: "calendar"},
	}

	copied := ctx.Copy(ChatContextCopyOptions{
		Tools: []interface{}{"lookup"},
	})

	if got, want := itemIDs(copied.Items), "user,lookup-call,lookup-output"; got != want {
		t.Fatalf("Copy() item IDs = %q, want %q", got, want)
	}
}

func TestChatContextCopyPreservesShallowCopyBehavior(t *testing.T) {
	item := &ChatMessage{
		ID:        "user",
		Role:      ChatRoleUser,
		Content:   []ChatContent{{Text: "hello"}},
		CreatedAt: time.Unix(10, 0),
	}
	ctx := NewChatContext()
	ctx.Items = []ChatItem{item}

	copied := ctx.Copy()
	ctx.Items = nil

	if len(copied.Items) != 1 {
		t.Fatalf("len(Copy().Items) = %d, want 1", len(copied.Items))
	}
	if copied.Items[0] != item {
		t.Fatalf("Copy().Items[0] = %p, want %p", copied.Items[0], item)
	}
}
