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

func TestChatContextMergeFiltersReferenceItemTypes(t *testing.T) {
	base := NewChatContext()
	base.Items = []ChatItem{
		&ChatMessage{ID: "existing", Role: ChatRoleUser, Content: []ChatContent{{Text: "hello"}}, CreatedAt: time.Unix(10, 0)},
	}
	other := NewChatContext()
	other.Items = []ChatItem{
		&ChatMessage{ID: "system", Role: ChatRoleSystem, Content: []ChatContent{{Text: "instructions"}}, CreatedAt: time.Unix(1, 0)},
		&FunctionCall{ID: "call", Name: "lookup", CreatedAt: time.Unix(11, 0)},
		&FunctionCallOutput{ID: "output", Name: "lookup", CreatedAt: time.Unix(12, 0)},
		&AgentConfigUpdate{ID: "config", CreatedAt: time.Unix(13, 0)},
		&ChatMessage{ID: "new", Role: ChatRoleUser, Content: []ChatContent{{Text: "new"}}, CreatedAt: time.Unix(14, 0)},
	}

	base.Merge(other, ChatContextMergeOptions{
		ExcludeFunctionCall: true,
		ExcludeInstructions: true,
		ExcludeConfigUpdate: true,
	})

	if got, want := itemIDs(base.Items), "existing,new"; got != want {
		t.Fatalf("Merge() item IDs = %q, want %q", got, want)
	}
}

func TestChatContextMergePreservesCreatedAtOrderAndSkipsDuplicates(t *testing.T) {
	base := NewChatContext()
	base.Items = []ChatItem{
		&ChatMessage{ID: "middle", Role: ChatRoleUser, Content: []ChatContent{{Text: "middle"}}, CreatedAt: time.Unix(20, 0)},
		&ChatMessage{ID: "duplicate", Role: ChatRoleUser, Content: []ChatContent{{Text: "old"}}, CreatedAt: time.Unix(30, 0)},
	}
	other := NewChatContext()
	other.Items = []ChatItem{
		&ChatMessage{ID: "early", Role: ChatRoleUser, Content: []ChatContent{{Text: "early"}}, CreatedAt: time.Unix(10, 0)},
		&ChatMessage{ID: "duplicate", Role: ChatRoleUser, Content: []ChatContent{{Text: "new"}}, CreatedAt: time.Unix(25, 0)},
		&ChatMessage{ID: "late", Role: ChatRoleUser, Content: []ChatContent{{Text: "late"}}, CreatedAt: time.Unix(40, 0)},
	}

	base.Merge(other)

	if got, want := itemIDs(base.Items), "early,middle,duplicate,late"; got != want {
		t.Fatalf("Merge() item IDs = %q, want %q", got, want)
	}
}

func TestChatContextToOpenAIProviderFormatGroupsToolCallsWithOutputs(t *testing.T) {
	ctx := NewChatContext()
	groupID := "assistant-turn"
	ctx.Items = []ChatItem{
		&ChatMessage{ID: groupID, Role: ChatRoleAssistant, Content: []ChatContent{{Text: "checking"}}},
		&FunctionCall{ID: groupID + "/tool-1", CallID: "call_lookup", Name: "lookup", Arguments: `{"city":"Paris"}`},
		&FunctionCall{ID: groupID + "/tool-2", CallID: "call_weather", Name: "weather", Arguments: `{"city":"Paris"}`},
		&FunctionCallOutput{ID: "lookup-output", CallID: "call_lookup", Name: "lookup", Output: "Paris"},
		&FunctionCallOutput{ID: "weather-output", CallID: "call_weather", Name: "weather", Output: "sunny"},
	}

	messages, extra := ctx.ToProviderFormat("openai")

	if extra != nil {
		t.Fatalf("ToProviderFormat() extra = %#v, want nil", extra)
	}
	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want 3: %#v", len(messages), messages)
	}
	assistant := messages[0]
	if assistant["role"] != "assistant" || assistant["content"] != "checking" {
		t.Fatalf("assistant message = %#v", assistant)
	}
	toolCalls, ok := assistant["tool_calls"].([]map[string]any)
	if !ok {
		t.Fatalf("assistant tool_calls = %#v, want []map[string]any", assistant["tool_calls"])
	}
	if len(toolCalls) != 2 {
		t.Fatalf("len(tool_calls) = %d, want 2", len(toolCalls))
	}
	if toolCalls[0]["id"] != "call_lookup" || toolCalls[1]["id"] != "call_weather" {
		t.Fatalf("tool call IDs = %#v", toolCalls)
	}
	if messages[1]["role"] != "tool" || messages[1]["tool_call_id"] != "call_lookup" || messages[1]["content"] != "Paris" {
		t.Fatalf("first tool output = %#v", messages[1])
	}
	if messages[2]["role"] != "tool" || messages[2]["tool_call_id"] != "call_weather" || messages[2]["content"] != "sunny" {
		t.Fatalf("second tool output = %#v", messages[2])
	}
}

func TestChatContextToOpenAIProviderFormatFiltersUnmatchedToolItems(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{ID: "user", Role: ChatRoleUser, Content: []ChatContent{{Text: "hello"}}},
		&FunctionCall{ID: "orphan-call", CallID: "call_missing_output", Name: "lookup", Arguments: `{}`},
		&FunctionCallOutput{ID: "orphan-output", CallID: "call_missing_call", Name: "lookup", Output: "ignored"},
	}

	messages, _ := ctx.ToProviderFormat("openai")

	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1: %#v", len(messages), messages)
	}
	if messages[0]["role"] != "user" || messages[0]["content"] != "hello" {
		t.Fatalf("message = %#v, want user hello", messages[0])
	}
}
