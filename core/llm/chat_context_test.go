package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/library/utils/images"
)

func itemIDs(items []ChatItem) string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.GetID())
	}
	return strings.Join(ids, ",")
}

func boolPtr(value bool) *bool {
	return &value
}

func assertPanicsWithValue(t *testing.T, name string, want any, fn func()) {
	t.Helper()
	defer func() {
		got := recover()
		if got == nil {
			t.Fatalf("%s did not panic", name)
		}
		if got != want {
			t.Fatalf("%s panic = %#v, want %#v", name, got, want)
		}
	}()
	fn()
}

type nestedChatToolset struct {
	testTool
	tools []Tool
}

func (s *nestedChatToolset) Tools() []Tool { return s.tools }

func TestEmptyChatContextReturnsMutableEmptyContext(t *testing.T) {
	ctx := EmptyChatContext()
	if ctx == nil {
		t.Fatal("EmptyChatContext() = nil, want context")
	}
	if ctx.Readonly() {
		t.Fatal("EmptyChatContext().Readonly() = true, want false")
	}
	if ctx.Items == nil {
		t.Fatal("EmptyChatContext().Items = nil, want empty slice")
	}
	if len(ctx.Items) != 0 {
		t.Fatalf("len(EmptyChatContext().Items) = %d, want 0", len(ctx.Items))
	}

	ctx.Append(&ChatMessage{ID: "user", Role: ChatRoleUser})
	if got := len(ctx.Items); got != 1 {
		t.Fatalf("len(EmptyChatContext().Items) after Append = %d, want 1", got)
	}
}

func TestChatContextEmptyMatchesReferenceConstructor(t *testing.T) {
	var receiver ChatContext
	ctx := receiver.Empty()
	if ctx == nil {
		t.Fatal("ChatContext.Empty() = nil, want context")
	}
	if ctx.Readonly() {
		t.Fatal("ChatContext.Empty().Readonly() = true, want false")
	}
	if ctx.Items == nil {
		t.Fatal("ChatContext.Empty().Items = nil, want empty slice")
	}
	if len(ctx.Items) != 0 {
		t.Fatalf("len(ChatContext.Empty().Items) = %d, want 0", len(ctx.Items))
	}

	ctx.Append(&ChatMessage{ID: "msg", Role: ChatRoleUser})
	if got := len(ctx.Items); got != 1 {
		t.Fatalf("len(ChatContext.Empty().Items) after Append = %d, want 1", got)
	}
}

func TestChatContextInsertAssignsReferenceConfigUpdateID(t *testing.T) {
	ctx := NewChatContext()
	config := &AgentConfigUpdate{CreatedAt: time.Unix(10, 0)}

	ctx.Insert(config)

	if config.ID == "" {
		t.Fatal("AgentConfigUpdate.ID after Insert = empty, want generated item id")
	}
	if !strings.HasPrefix(config.ID, "item_") {
		t.Fatalf("AgentConfigUpdate.ID after Insert = %q, want item_ prefix", config.ID)
	}
	if ctx.GetByID(config.ID) != config {
		t.Fatalf("GetByID(%q) did not return inserted config update", config.ID)
	}
}

func TestChatContextInsertAssignsReferenceConfigUpdateCreatedAt(t *testing.T) {
	ctx := NewChatContext()
	config := &AgentConfigUpdate{ID: "config"}

	ctx.Insert(config)

	if config.CreatedAt.IsZero() {
		t.Fatal("AgentConfigUpdate.CreatedAt after Insert is zero, want generated timestamp")
	}
}

func TestChatContextAppendAssignsReferenceConfigUpdateDefaults(t *testing.T) {
	ctx := NewChatContext()
	config := &AgentConfigUpdate{}

	ctx.Append(config)

	if config.ID == "" {
		t.Fatal("AgentConfigUpdate.ID after Append = empty, want generated item id")
	}
	if !strings.HasPrefix(config.ID, "item_") {
		t.Fatalf("AgentConfigUpdate.ID after Append = %q, want item_ prefix", config.ID)
	}
	if config.CreatedAt.IsZero() {
		t.Fatal("AgentConfigUpdate.CreatedAt after Append is zero, want generated timestamp")
	}
	if ctx.GetByID(config.ID) != config {
		t.Fatalf("GetByID(%q) did not return appended config update", config.ID)
	}
}

func TestChatContextAppendAssignsReferenceItemDefaults(t *testing.T) {
	ctx := NewChatContext()
	message := &ChatMessage{Role: ChatRoleUser}
	call := &FunctionCall{CallID: "call_lookup", Name: "lookup", Arguments: "{}"}
	output := &FunctionCallOutput{CallID: "call_lookup", Name: "lookup", Output: "ok"}
	handoff := &AgentHandoff{NewAgentID: "next"}

	ctx.Append(message)
	ctx.Append(call)
	ctx.Append(output)
	ctx.Append(handoff)

	for _, item := range []ChatItem{message, call, output, handoff} {
		if item.GetID() == "" {
			t.Fatalf("%s ID after Append = empty, want generated item id", item.GetType())
		}
		if !strings.HasPrefix(item.GetID(), "item_") {
			t.Fatalf("%s ID after Append = %q, want item_ prefix", item.GetType(), item.GetID())
		}
		if item.GetCreatedAt().IsZero() {
			t.Fatalf("%s CreatedAt after Append is zero, want generated timestamp", item.GetType())
		}
		if ctx.GetByID(item.GetID()) != item {
			t.Fatalf("GetByID(%q) did not return appended %s", item.GetID(), item.GetType())
		}
	}
}

func TestChatContextUpsertAssignsReferenceConfigUpdateDefaults(t *testing.T) {
	ctx := NewChatContext()
	config := &AgentConfigUpdate{}

	if err := ctx.UpsertItem(config); err != nil {
		t.Fatalf("UpsertItem error = %v", err)
	}

	if config.ID == "" {
		t.Fatal("AgentConfigUpdate.ID after UpsertItem = empty, want generated item id")
	}
	if !strings.HasPrefix(config.ID, "item_") {
		t.Fatalf("AgentConfigUpdate.ID after UpsertItem = %q, want item_ prefix", config.ID)
	}
	if config.CreatedAt.IsZero() {
		t.Fatal("AgentConfigUpdate.CreatedAt after UpsertItem is zero, want generated timestamp")
	}
	if ctx.GetByID(config.ID) != config {
		t.Fatalf("GetByID(%q) did not return upserted config update", config.ID)
	}
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

func TestChatContextGetToolNamesIncludesStringsToolsAndToolsets(t *testing.T) {
	lookup := &testTool{id: "lookup", name: "lookup"}
	weather := &testTool{id: "weather", name: "weather"}
	toolset := &testToolset{id: "tools", tools: []Tool{weather}}
	ctx := NewChatContext()

	names := ctx.GetToolNames([]interface{}{"calendar", lookup, toolset, 123})

	if got, want := strings.Join(names, ","), "calendar,lookup,weather"; got != want {
		t.Fatalf("GetToolNames() = %q, want %q", got, want)
	}
}

func TestChatContextGetToolNamesRecursesNestedToolsets(t *testing.T) {
	lookup := &testTool{id: "lookup", name: "lookup"}
	innerToolset := &nestedChatToolset{
		testTool: testTool{id: "inner", name: "inner"},
		tools:    []Tool{lookup},
	}
	outerToolset := &nestedChatToolset{
		testTool: testTool{id: "outer", name: "outer"},
		tools:    []Tool{innerToolset},
	}
	ctx := NewChatContext()

	names := ctx.GetToolNames([]interface{}{outerToolset})

	if got, want := strings.Join(names, ","), "lookup"; got != want {
		t.Fatalf("GetToolNames() = %q, want recursive nested tool names %q", got, want)
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

func TestChatContextReadOnlyViewRejectsMutatingMethods(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{ID: "user", Role: ChatRoleUser, Content: []ChatContent{{Text: "hello"}}},
	}

	readOnly := ctx.ReadOnly()

	if !readOnly.Readonly() {
		t.Fatal("ReadOnly().Readonly() = false, want true")
	}
	if ctx.Readonly() {
		t.Fatal("Readonly() on mutable context = true, want false")
	}
	if got, want := itemIDs(readOnly.Items), "user"; got != want {
		t.Fatalf("read-only items = %q, want %q", got, want)
	}

	const readOnlyError = "trying to modify a read-only chat context, please use .copy() and agent.update_chat_ctx() to modify the chat context"
	assertPanicsWithValue(t, "AddMessage on read-only context", readOnlyError, func() {
		readOnly.AddMessage(ChatMessageArgs{Role: ChatRoleUser, Text: "blocked"})
	})

	if got, want := itemIDs(ctx.Items), "user"; got != want {
		t.Fatalf("source items after read-only mutation = %q, want %q", got, want)
	}

	mutable := readOnly.Copy()
	if mutable.Readonly() {
		t.Fatal("Copy() from read-only context is still read-only, want mutable copy")
	}
	mutable.AddMessage(ChatMessageArgs{ID: "copy", Role: ChatRoleAssistant, Text: "ok"})
	if got, want := itemIDs(mutable.Items), "user,copy"; got != want {
		t.Fatalf("mutable copy items = %q, want %q", got, want)
	}
	if got, want := itemIDs(ctx.Items), "user"; got != want {
		t.Fatalf("source items after mutable copy change = %q, want %q", got, want)
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

func TestChatContextMergeReturnsReceiver(t *testing.T) {
	base := NewChatContext()
	other := NewChatContext()

	if got := base.Merge(other); got != base {
		t.Fatalf("Merge() = %p, want receiver %p", got, base)
	}
}

func TestChatContextAddMessageInsertsByCreatedAt(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{ID: "late", Role: ChatRoleUser, Content: []ChatContent{{Text: "late"}}, CreatedAt: time.Unix(30, 0)},
	}

	message := ctx.AddMessage(ChatMessageArgs{
		ID:        "early",
		Role:      ChatRoleAssistant,
		Content:   []ChatContent{{Text: "early"}},
		CreatedAt: time.Unix(10, 0),
	})

	if message.ID != "early" || message.Role != ChatRoleAssistant || message.TextContent() != "early" {
		t.Fatalf("AddMessage() = %#v", message)
	}
	if got, want := itemIDs(ctx.Items), "early,late"; got != want {
		t.Fatalf("items = %q, want %q", got, want)
	}
}

func TestChatContextAddMessageAppendsWhenCreatedAtIsZero(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{ID: "existing", Role: ChatRoleUser, Content: []ChatContent{{Text: "existing"}}, CreatedAt: time.Unix(30, 0)},
	}

	message := ctx.AddMessage(ChatMessageArgs{
		ID:      "new",
		Role:    ChatRoleUser,
		Content: []ChatContent{{Text: "new"}},
	})

	if message.CreatedAt.IsZero() {
		t.Fatal("AddMessage() CreatedAt is zero, want generated timestamp")
	}
	if got, want := itemIDs(ctx.Items), "existing,new"; got != want {
		t.Fatalf("items = %q, want %q", got, want)
	}
}

func TestChatContextAddMessageDefaultsID(t *testing.T) {
	ctx := NewChatContext()

	message := ctx.AddMessage(ChatMessageArgs{
		Role:    ChatRoleUser,
		Content: []ChatContent{{Text: "hello"}},
	})

	if !strings.HasPrefix(message.ID, "item_") {
		t.Fatalf("AddMessage() ID = %q, want item_ prefix", message.ID)
	}
	if ctx.Items[0].GetID() != message.ID {
		t.Fatalf("stored message ID = %q, want %q", ctx.Items[0].GetID(), message.ID)
	}
}

func TestChatContextAddMessageAcceptsTextContent(t *testing.T) {
	ctx := NewChatContext()

	message := ctx.AddMessage(ChatMessageArgs{
		Role: ChatRoleUser,
		Text: "hello",
	})

	if got, want := message.TextContent(), "hello"; got != want {
		t.Fatalf("AddMessage() text content = %q, want %q", got, want)
	}
}

func TestChatMessageTextContentIncludesInstructionsAndPlainText(t *testing.T) {
	message := &ChatMessage{
		Role: ChatRoleSystem,
		Content: []ChatContent{
			{Instructions: NewInstructions("voice instructions", "text instructions")},
			{Text: "plain text"},
			{Image: &ImageContent{Image: "https://example.com/image.jpg"}},
			{Audio: &AudioContent{Transcript: "spoken words"}},
		},
	}

	if got, want := message.TextContent(), "voice instructions\nplain text"; got != want {
		t.Fatalf("TextContent() = %q, want %q", got, want)
	}
}

func TestChatMessageTextContentPreservesEmptyStringParts(t *testing.T) {
	message := &ChatMessage{
		Role: ChatRoleSystem,
		Content: []ChatContent{
			{Text: ""},
			{Text: "instructions"},
		},
	}

	if got, want := message.TextContent(), "\ninstructions"; got != want {
		t.Fatalf("TextContent() = %q, want %q", got, want)
	}
}

func TestInstructionsPreserveVariantsAndSelectModality(t *testing.T) {
	instructions := NewInstructions("speak plainly", "write tersely")

	if got := instructions.String(); got != "speak plainly" {
		t.Fatalf("Instructions.String() = %q, want audio variant", got)
	}
	if got := instructions.AsModality("text").String(); got != "write tersely" {
		t.Fatalf("Instructions.AsModality(text) = %q, want text variant", got)
	}
	if got := instructions.AsModality("text").AsModality("audio").String(); got != "speak plainly" {
		t.Fatalf("Instructions modality round trip = %q, want audio variant", got)
	}
}

func TestInstructionsFormatPreservesNestedVariants(t *testing.T) {
	template := NewInstructions("Say: %s", "Write: %s")
	value := NewInstructions("hello out loud", "hello in text")

	formatted := template.Format(value)

	if got := formatted.String(); got != "Say: hello out loud" {
		t.Fatalf("formatted.String() = %q, want audio representation", got)
	}
	if got := formatted.AsModality("audio").String(); got != "Say: hello out loud" {
		t.Fatalf("formatted audio = %q, want audio variant", got)
	}
	if got := formatted.AsModality("text").String(); got != "Write: hello in text" {
		t.Fatalf("formatted text = %q, want text variant", got)
	}
}

func TestInstructionsFormatPreservesActiveRepresentation(t *testing.T) {
	template := NewInstructions("Say: %s", "Write: %s").AsModality("text")

	formatted := template.Format("hello")

	if got := formatted.String(); got != "Write: hello" {
		t.Fatalf("formatted.String() = %q, want active text representation", got)
	}
	if got := formatted.AsModality("audio").String(); got != "Say: hello" {
		t.Fatalf("formatted audio = %q, want audio variant", got)
	}
}

func TestInstructionsConcatPreservesVariants(t *testing.T) {
	left := NewInstructions("audio A", "text A").AsModality("text")
	right := NewInstructions(" audio B", " text B")

	combined := left.Concat(right)

	if got := combined.String(); got != "text A audio B" {
		t.Fatalf("combined.String() = %q, want active left plus active right", got)
	}
	if got := combined.AsModality("audio").String(); got != "audio A audio B" {
		t.Fatalf("combined audio = %q, want audio variants", got)
	}
	if got := combined.AsModality("text").String(); got != "text A text B" {
		t.Fatalf("combined text = %q, want text variants", got)
	}
}

func TestInstructionsAppendStringPreservesExplicitTextVariant(t *testing.T) {
	instructions := NewInstructions("audio", "text")

	appended := instructions.AppendString(" suffix")

	if got := appended.AsModality("audio").String(); got != "audio suffix" {
		t.Fatalf("appended audio = %q, want suffix", got)
	}
	if got := appended.AsModality("text").String(); got != "text suffix" {
		t.Fatalf("appended text = %q, want suffix", got)
	}
}

func TestInstructionsPrependStringPreservesExplicitTextVariant(t *testing.T) {
	instructions := NewInstructions("audio", "text").AsModality("text")

	prepended := instructions.PrependString("prefix ")

	if got := prepended.String(); got != "prefix text" {
		t.Fatalf("prepended.String() = %q, want active text representation with prefix", got)
	}
	if got := prepended.AsModality("audio").String(); got != "prefix audio" {
		t.Fatalf("prepended audio = %q, want prefix", got)
	}
	if got := prepended.AsModality("text").String(); got != "prefix text" {
		t.Fatalf("prepended text = %q, want prefix", got)
	}
}

func TestChatContextInstructionsSerializeAndRoundTrip(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{
			ID:   "system",
			Role: ChatRoleSystem,
			Content: []ChatContent{{
				Instructions: NewInstructions("audio instructions", "text instructions"),
			}},
		},
	}

	data := ctx.ToDict()
	items := data["items"].([]map[string]any)
	content := items[0]["content"].([]any)
	instructions := content[0].(map[string]any)
	if instructions["type"] != "instructions" || instructions["audio"] != "audio instructions" || instructions["text"] != "text instructions" {
		t.Fatalf("serialized instructions = %#v", instructions)
	}

	roundTrip, err := ChatContextFromDict(data)
	if err != nil {
		t.Fatalf("ChatContextFromDict() error = %v", err)
	}
	msg := roundTrip.Items[0].(*ChatMessage)
	if len(msg.Content) != 1 || msg.Content[0].Instructions == nil {
		t.Fatalf("round-trip content = %#v, want instructions", msg.Content)
	}
	if got := msg.Content[0].Instructions.AsModality("text").String(); got != "text instructions" {
		t.Fatalf("round-trip text instructions = %q, want text instructions", got)
	}
}

func TestChatContextInstructionsRoundTripPreservesAbsentTextVariant(t *testing.T) {
	ctx, err := ChatContextFromDict(map[string]any{
		"items": []any{
			map[string]any{
				"id":   "system",
				"type": "message",
				"role": "system",
				"content": []any{
					map[string]any{
						"type":  "instructions",
						"audio": "audio instructions",
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ChatContextFromDict() error = %v", err)
	}

	msg := ctx.Items[0].(*ChatMessage)
	if got := msg.Content[0].Instructions.AsModality("text").String(); got != "audio instructions" {
		t.Fatalf("round-trip fallback text = %q, want audio instructions", got)
	}

	data := ctx.ToDict()
	items := data["items"].([]map[string]any)
	content := items[0]["content"].([]any)
	instructions := content[0].(map[string]any)
	if _, ok := instructions["text"]; ok {
		t.Fatalf("serialized instructions = %#v, want text omitted when reference input omitted text", instructions)
	}
}

func TestChatContextInstructionsSerializeExplicitTextVariantMatchingAudio(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{
			ID:   "system",
			Role: ChatRoleSystem,
			Content: []ChatContent{{
				Instructions: NewInstructions("same instructions", "same instructions"),
			}},
		},
	}

	data := ctx.ToDict()
	items := data["items"].([]map[string]any)
	content := items[0]["content"].([]any)
	instructions := content[0].(map[string]any)
	if _, ok := instructions["text"]; !ok {
		t.Fatalf("serialized instructions = %#v, want explicit text field preserved", instructions)
	}
	if instructions["text"] != "same instructions" {
		t.Fatalf("instructions text = %#v, want same instructions", instructions["text"])
	}
}

func TestProviderFormatUsesActiveInstructionText(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{
			ID:   "system",
			Role: ChatRoleSystem,
			Content: []ChatContent{{
				Instructions: NewInstructions("audio instructions", "text instructions").AsModality("text"),
			}},
		},
	}

	formatted, _ := ctx.ToProviderFormat("openai")
	if got := formatted[0]["content"]; got != "text instructions" {
		t.Fatalf("openai content = %#v, want active instruction text", got)
	}
}

func TestChatContextToProviderFormatCanDisableDummyUserMessage(t *testing.T) {
	formats := []string{"google", "anthropic", "aws"}
	for _, format := range formats {
		t.Run(format, func(t *testing.T) {
			ctx := NewChatContext()
			ctx.Items = []ChatItem{
				&ChatMessage{
					ID:      "assistant",
					Role:    ChatRoleAssistant,
					Content: []ChatContent{{Text: "hello"}},
				},
			}

			messages, _ := ctx.ToProviderFormat(format, ChatContextProviderFormatOptions{
				InjectDummyUserMessage: boolPtr(false),
			})

			if len(messages) != 1 {
				t.Fatalf("len(messages) = %d, want 1: %#v", len(messages), messages)
			}
			if got := messages[0]["role"]; got == "user" {
				t.Fatalf("first role = %q, want no injected dummy user message: %#v", got, messages)
			}
		})
	}
}

func TestChatContextToProviderFormatEReturnsErrorForUnsupportedFormat(t *testing.T) {
	ctx := NewChatContext()

	messages, extra, err := ctx.ToProviderFormatE("unknown")

	if err == nil {
		t.Fatal("ToProviderFormatE() error = nil, want unsupported format error")
	}
	if messages != nil || extra != nil {
		t.Fatalf("ToProviderFormatE() messages=%#v extra=%#v, want nil outputs on error", messages, extra)
	}
	if got, want := err.Error(), "Unsupported provider format: unknown"; got != want {
		t.Fatalf("ToProviderFormatE() error = %q, want %q", got, want)
	}
}

func TestChatContextToProviderFormatEReturnsErrorForMalformedToolArguments(t *testing.T) {
	formats := []string{"google", "anthropic", "aws"}
	for _, format := range formats {
		t.Run(format, func(t *testing.T) {
			ctx := NewChatContext()
			ctx.Items = []ChatItem{
				&ChatMessage{ID: "assistant-turn", Role: ChatRoleAssistant, Content: []ChatContent{{Text: "checking"}}},
				&FunctionCall{ID: "assistant-turn/tool", CallID: "call_lookup", Name: "lookup", Arguments: `{"city":`},
				&FunctionCallOutput{ID: "output", CallID: "call_lookup", Name: "lookup", Output: "Paris"},
			}

			messages, extra, err := ctx.ToProviderFormatE(format)

			if err == nil {
				t.Fatal("ToProviderFormatE() error = nil, want malformed tool arguments error")
			}
			if messages != nil || extra != nil {
				t.Fatalf("ToProviderFormatE() messages=%#v extra=%#v, want nil outputs on error", messages, extra)
			}
		})
	}
}

func TestGoogleProviderFormatInjectsThoughtSignatures(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{ID: "assistant", Role: ChatRoleAssistant, Content: []ChatContent{{Text: "checking"}}},
		&FunctionCall{ID: "assistant/tool", CallID: "call_lookup", Name: "lookup", Arguments: `{"city":"Paris"}`},
		&FunctionCallOutput{ID: "output", CallID: "call_lookup", Name: "lookup", Output: "Paris"},
	}

	turns, _ := ctx.ToProviderFormat("google", ChatContextProviderFormatOptions{
		ThoughtSignatures: map[string][]byte{"call_lookup": []byte("signature")},
	})

	if len(turns) == 0 {
		t.Fatal("len(turns) = 0, want model turn with function_call")
	}
	parts := turns[0]["parts"].([]map[string]any)
	if len(parts) < 2 {
		t.Fatalf("model parts = %#v, want function_call part", parts)
	}
	if got, ok := parts[1]["thought_signature"].([]byte); !ok || string(got) != "signature" {
		t.Fatalf("thought_signature = %#v, want signature bytes", parts[1]["thought_signature"])
	}
}

func TestAnthropicProviderFormatCanInjectTrailingUserMessage(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{ID: "user", Role: ChatRoleUser, Content: []ChatContent{{Text: "hello"}}},
		&ChatMessage{ID: "assistant", Role: ChatRoleAssistant, Content: []ChatContent{{Text: "hi"}}},
	}

	messages, _ := ctx.ToProviderFormat("anthropic", ChatContextProviderFormatOptions{
		InjectTrailingUserMessage: boolPtr(true),
	})

	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want 3: %#v", len(messages), messages)
	}
	trailing := messages[2]
	if trailing["role"] != "user" {
		t.Fatalf("trailing message = %#v, want user role", trailing)
	}
	content := trailing["content"].([]map[string]any)
	if len(content) != 1 || content[0]["type"] != "text" || content[0]["text"] != " " {
		t.Fatalf("trailing content = %#v, want single blank text item", content)
	}
}

func TestChatContextInsertOrdersItemsByCreatedAt(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{ID: "middle", Role: ChatRoleUser, CreatedAt: time.Unix(20, 0)},
	}

	ctx.Insert(
		&ChatMessage{ID: "late", Role: ChatRoleUser, CreatedAt: time.Unix(30, 0)},
		&ChatMessage{ID: "early", Role: ChatRoleUser, CreatedAt: time.Unix(10, 0)},
	)

	if got, want := itemIDs(ctx.Items), "early,middle,late"; got != want {
		t.Fatalf("items = %q, want %q", got, want)
	}
}

func TestChatContextUpsertItemReplacesExistingItemByID(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{ID: "first", Role: ChatRoleUser, Content: []ChatContent{{Text: "old"}}},
		&ChatMessage{ID: "second", Role: ChatRoleAssistant, Content: []ChatContent{{Text: "kept"}}},
	}
	updated := &ChatMessage{ID: "first", Role: ChatRoleUser, Content: []ChatContent{{Text: "new"}}}

	if err := ctx.UpsertItem(updated); err != nil {
		t.Fatalf("UpsertItem() error = %v", err)
	}

	if len(ctx.Items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(ctx.Items))
	}
	if ctx.Items[0] != updated {
		t.Fatalf("items[0] = %#v, want updated item", ctx.Items[0])
	}
	if got, want := itemIDs(ctx.Items), "first,second"; got != want {
		t.Fatalf("items = %q, want %q", got, want)
	}
}

func TestChatContextUpsertItemAppendsMissingItem(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{ID: "first", Role: ChatRoleUser, Content: []ChatContent{{Text: "old"}}},
	}
	inserted := &FunctionCall{ID: "call", CallID: "call_lookup", Name: "lookup", Arguments: "{}"}

	if err := ctx.UpsertItem(inserted); err != nil {
		t.Fatalf("UpsertItem() error = %v", err)
	}

	if got, want := itemIDs(ctx.Items), "first,call"; got != want {
		t.Fatalf("items = %q, want %q", got, want)
	}
	if ctx.Items[1] != inserted {
		t.Fatalf("items[1] = %#v, want inserted item", ctx.Items[1])
	}
}

func TestChatContextUpsertItemRejectsTypeMismatchByDefault(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{ID: "item", Role: ChatRoleUser, Content: []ChatContent{{Text: "old"}}},
	}

	err := ctx.UpsertItem(&FunctionCall{ID: "item", CallID: "call_lookup", Name: "lookup"})

	if err == nil {
		t.Fatal("UpsertItem() error = nil, want type mismatch error")
	}
	if got, want := err.Error(), "Item type mismatch: function_call != message"; got != want {
		t.Fatalf("UpsertItem() error = %q, want %q", got, want)
	}
	if got, want := itemIDs(ctx.Items), "item"; got != want {
		t.Fatalf("items = %q, want %q", got, want)
	}
	if _, ok := ctx.Items[0].(*ChatMessage); !ok {
		t.Fatalf("items[0] = %T, want original *ChatMessage", ctx.Items[0])
	}
}

func TestChatContextUpsertItemCanAllowTypeMismatch(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{ID: "item", Role: ChatRoleUser, Content: []ChatContent{{Text: "old"}}},
	}
	replacement := &FunctionCall{ID: "item", CallID: "call_lookup", Name: "lookup"}

	if err := ctx.UpsertItem(replacement, ChatContextUpsertOptions{AllowTypeMismatch: true}); err != nil {
		t.Fatalf("UpsertItem() error = %v", err)
	}

	if ctx.Items[0] != replacement {
		t.Fatalf("items[0] = %#v, want replacement function call", ctx.Items[0])
	}
}

func TestChatContextLookupByID(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{ID: "first", Role: ChatRoleUser},
		&FunctionCall{ID: "call", CallID: "call_lookup", Name: "lookup"},
	}

	if got := ctx.GetByID("call"); got != ctx.Items[1] {
		t.Fatalf("GetByID() = %p, want %p", got, ctx.Items[1])
	}
	if got := ctx.GetByID("missing"); got != nil {
		t.Fatalf("GetByID(missing) = %#v, want nil", got)
	}
	if got := ctx.IndexByID("call"); got == nil || *got != 1 {
		t.Fatalf("IndexByID(call) = %#v, want 1", got)
	}
	if got := ctx.IndexByID("missing"); got != nil {
		t.Fatalf("IndexByID(missing) = %#v, want nil", got)
	}
}

func TestChatContextTruncateOnlyDropsLeadingFunctionItems(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{ID: "system", Role: ChatRoleSystem, Content: []ChatContent{{Text: "instructions"}}},
		&ChatMessage{ID: "old", Role: ChatRoleUser, Content: []ChatContent{{Text: "old"}}},
		&AgentConfigUpdate{ID: "config"},
		&FunctionCall{ID: "call", CallID: "call_lookup", Name: "lookup"},
		&FunctionCallOutput{ID: "output", CallID: "call_lookup", Name: "lookup", Output: "ok"},
		&ChatMessage{ID: "user", Role: ChatRoleUser, Content: []ChatContent{{Text: "hello"}}},
	}

	truncated := ctx.Truncate(4)

	if truncated != ctx {
		t.Fatalf("Truncate() = %p, want original context %p", truncated, ctx)
	}
	if got, want := itemIDs(ctx.Items), "system,config,call,output,user"; got != want {
		t.Fatalf("Truncate() item IDs = %q, want %q", got, want)
	}
}

func TestChatContextTruncateDropsLeadingFunctionSequence(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{ID: "system", Role: ChatRoleSystem, Content: []ChatContent{{Text: "instructions"}}},
		&ChatMessage{ID: "old", Role: ChatRoleUser, Content: []ChatContent{{Text: "old"}}},
		&FunctionCall{ID: "call", CallID: "call_lookup", Name: "lookup"},
		&FunctionCallOutput{ID: "output", CallID: "call_lookup", Name: "lookup", Output: "ok"},
		&ChatMessage{ID: "user", Role: ChatRoleUser, Content: []ChatContent{{Text: "hello"}}},
	}

	ctx.Truncate(3)

	if got, want := itemIDs(ctx.Items), "system,user"; got != want {
		t.Fatalf("Truncate() item IDs = %q, want %q", got, want)
	}
}

func TestChatContextTruncateZeroKeepsReferenceItems(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{ID: "system", Role: ChatRoleSystem, Content: []ChatContent{{Text: "instructions"}}},
		&ChatMessage{ID: "user", Role: ChatRoleUser, Content: []ChatContent{{Text: "hello"}}},
		&ChatMessage{ID: "assistant", Role: ChatRoleAssistant, Content: []ChatContent{{Text: "hi"}}},
	}

	ctx.Truncate(0)

	if got, want := itemIDs(ctx.Items), "system,user,assistant"; got != want {
		t.Fatalf("Truncate(0) item IDs = %q, want %q", got, want)
	}
}

func TestChatContextTruncateNegativeKeepsReferenceSliceBehavior(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{ID: "system", Role: ChatRoleSystem, Content: []ChatContent{{Text: "instructions"}}},
		&ChatMessage{ID: "old", Role: ChatRoleUser, Content: []ChatContent{{Text: "old"}}},
		&ChatMessage{ID: "user", Role: ChatRoleUser, Content: []ChatContent{{Text: "hello"}}},
		&ChatMessage{ID: "assistant", Role: ChatRoleAssistant, Content: []ChatContent{{Text: "hi"}}},
	}

	ctx.Truncate(-2)

	if got, want := itemIDs(ctx.Items), "system,user,assistant"; got != want {
		t.Fatalf("Truncate(-2) item IDs = %q, want %q", got, want)
	}
}

func TestChatContextIsEquivalentIgnoresTimestampsAndMetadata(t *testing.T) {
	left := NewChatContext()
	left.Items = []ChatItem{
		&ChatMessage{
			ID:          "message",
			Role:        ChatRoleAssistant,
			Content:     []ChatContent{{Text: "hello"}},
			Interrupted: true,
			Extra:       map[string]any{"ignored": "left"},
			CreatedAt:   time.Unix(10, 0),
		},
		&FunctionCall{
			ID:        "call",
			CallID:    "call_lookup",
			Name:      "lookup",
			Arguments: `{"city":"Paris"}`,
			Extra:     map[string]any{"ignored": "left"},
			CreatedAt: time.Unix(11, 0),
		},
		&FunctionCallOutput{
			ID:        "output",
			CallID:    "call_lookup",
			Name:      "lookup",
			Output:    "Paris",
			IsError:   true,
			CreatedAt: time.Unix(12, 0),
		},
		&AgentConfigUpdate{
			ID:        "config",
			CreatedAt: time.Unix(13, 0),
		},
	}
	right := NewChatContext()
	right.Items = []ChatItem{
		&ChatMessage{
			ID:          "message",
			Role:        ChatRoleAssistant,
			Content:     []ChatContent{{Text: "hello"}},
			Interrupted: true,
			Extra:       map[string]any{"ignored": "right"},
			CreatedAt:   time.Unix(20, 0),
		},
		&FunctionCall{
			ID:        "call",
			CallID:    "call_lookup",
			Name:      "lookup",
			Arguments: `{"city":"Paris"}`,
			Extra:     map[string]any{"ignored": "right"},
			CreatedAt: time.Unix(21, 0),
		},
		&FunctionCallOutput{
			ID:        "output",
			CallID:    "call_lookup",
			Name:      "lookup",
			Output:    "Paris",
			IsError:   true,
			CreatedAt: time.Unix(22, 0),
		},
		&AgentConfigUpdate{
			ID:        "config",
			CreatedAt: time.Unix(23, 0),
		},
	}

	if !left.IsEquivalent(right) {
		t.Fatal("IsEquivalent() = false, want true for matching essential fields")
	}
}

func TestToXMLRendersContentAndAttributes(t *testing.T) {
	got := ToXML("function_call", `{"city":"Paris"}`, map[string]any{
		"name":    "lookup",
		"call_id": "call_lookup",
	})

	want := "<function_call call_id=\"call_lookup\" name=\"lookup\">\n{\"city\":\"Paris\"}\n</function_call>"
	if got != want {
		t.Fatalf("ToXML() = %q, want %q", got, want)
	}

	if got := ToXML("empty", "", nil); got != "<empty />" {
		t.Fatalf("empty ToXML() = %q", got)
	}
}

func TestFunctionCallItemToMessageConvertsToolItemsToXMLMessages(t *testing.T) {
	call := &FunctionCall{
		CallID:    "call_lookup",
		Name:      "lookup",
		Arguments: `{"city":"Paris"}`,
		CreatedAt: time.Unix(10, 0),
	}
	callMsg := FunctionCallItemToMessage(call)
	wantCall := "<function_call name=\"lookup\" call_id=\"call_lookup\">\n{\"city\":\"Paris\"}\n</function_call>"
	if callMsg == nil || callMsg.Role != ChatRoleUser || callMsg.TextContent() != wantCall {
		t.Fatalf("FunctionCallItemToMessage(call) = %#v", callMsg)
	}
	if !strings.Contains(callMsg.TextContent(), `function_call name="lookup" call_id="call_lookup"`) {
		t.Fatalf("call message XML attribute order = %q, want reference name before call_id", callMsg.TextContent())
	}
	if callMsg.CreatedAt != call.CreatedAt || callMsg.Extra["is_function_call"] != true {
		t.Fatalf("call message metadata = %#v", callMsg)
	}

	output := &FunctionCallOutput{
		CallID:    "call_lookup",
		Name:      "lookup",
		Output:    "not found",
		IsError:   true,
		CreatedAt: time.Unix(11, 0),
	}
	outputMsg := FunctionCallItemToMessage(output)
	wantOutput := "<function_call_output call_id=\"call_lookup\" name=\"lookup\">\n<error>\nnot found\n</error>\n</function_call_output>"
	if outputMsg == nil || outputMsg.Role != ChatRoleAssistant || outputMsg.TextContent() != wantOutput {
		t.Fatalf("FunctionCallItemToMessage(output) = %#v, want %q", outputMsg, wantOutput)
	}
	if outputMsg.CreatedAt != output.CreatedAt || outputMsg.Extra["is_function_call_output"] != true {
		t.Fatalf("output message metadata = %#v", outputMsg)
	}
}

func TestChatContextIsEquivalentDetectsPayloadDifferences(t *testing.T) {
	tests := []struct {
		name  string
		left  ChatItem
		right ChatItem
	}{
		{
			name:  "message content",
			left:  &ChatMessage{ID: "message", Role: ChatRoleUser, Content: []ChatContent{{Text: "hello"}}},
			right: &ChatMessage{ID: "message", Role: ChatRoleUser, Content: []ChatContent{{Text: "goodbye"}}},
		},
		{
			name:  "message interrupted",
			left:  &ChatMessage{ID: "message", Role: ChatRoleAssistant, Content: []ChatContent{{Text: "hello"}}},
			right: &ChatMessage{ID: "message", Role: ChatRoleAssistant, Content: []ChatContent{{Text: "hello"}}, Interrupted: true},
		},
		{
			name:  "function call arguments",
			left:  &FunctionCall{ID: "call", CallID: "call_lookup", Name: "lookup", Arguments: `{"city":"Paris"}`},
			right: &FunctionCall{ID: "call", CallID: "call_lookup", Name: "lookup", Arguments: `{"city":"London"}`},
		},
		{
			name:  "function output error flag",
			left:  &FunctionCallOutput{ID: "output", CallID: "call_lookup", Name: "lookup", Output: "failed"},
			right: &FunctionCallOutput{ID: "output", CallID: "call_lookup", Name: "lookup", Output: "failed", IsError: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			left := NewChatContext()
			left.Items = []ChatItem{tt.left}
			right := NewChatContext()
			right.Items = []ChatItem{tt.right}

			if left.IsEquivalent(right) {
				t.Fatal("IsEquivalent() = true, want false")
			}
		})
	}
}

func TestChatContextToDictUsesReferenceItemShapeAndFilters(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{
			ID:   "message",
			Role: ChatRoleUser,
			Content: []ChatContent{
				{Text: "hello"},
				{Image: &ImageContent{ID: "image", Image: "https://example.test/image.png", InferenceDetail: "high"}},
				{Audio: &AudioContent{Transcript: "audio text"}},
			},
			CreatedAt: time.Unix(10, 0),
		},
		&FunctionCall{ID: "call", CallID: "call_lookup", Name: "lookup", Arguments: `{}`, CreatedAt: time.Unix(11, 0)},
		&FunctionCallOutput{ID: "output", CallID: "call_lookup", Name: "lookup", Output: "ok", CreatedAt: time.Unix(12, 0)},
		&AgentConfigUpdate{ID: "config", CreatedAt: time.Unix(13, 0)},
	}

	data := ctx.ToDict(ChatContextDictOptions{
		ExcludeFunctionCall: true,
		ExcludeConfigUpdate: true,
	})

	items, ok := data["items"].([]map[string]any)
	if !ok {
		t.Fatalf("items = %#v, want []map[string]any", data["items"])
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1: %#v", len(items), items)
	}
	message := items[0]
	if message["id"] != "message" || message["type"] != "message" || message["role"] != string(ChatRoleUser) {
		t.Fatalf("message identity fields = %#v", message)
	}
	if _, ok := message["created_at"]; ok {
		t.Fatalf("created_at present by default: %#v", message)
	}
	content, ok := message["content"].([]any)
	if !ok {
		t.Fatalf("content = %#v, want []any", message["content"])
	}
	if len(content) != 1 || content[0] != "hello" {
		t.Fatalf("content = %#v, want text-only content", content)
	}
}

func TestChatContextToDictPreservesEmptyStringContent(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{
			ID:   "message",
			Role: ChatRoleSystem,
			Content: []ChatContent{
				{Text: ""},
				{Text: "instructions"},
			},
		},
	}

	data := ctx.ToDict()
	items := data["items"].([]map[string]any)
	content, ok := items[0]["content"].([]any)
	if !ok {
		t.Fatalf("content = %#v, want []any", items[0]["content"])
	}
	if len(content) != 2 || content[0] != "" || content[1] != "instructions" {
		t.Fatalf("content = %#v, want empty string followed by instructions", content)
	}
}

func TestChatContextToDictIncludesAndExcludesMessageMetrics(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{
			ID:      "message",
			Role:    ChatRoleAssistant,
			Content: []ChatContent{{Text: "hello"}},
			Metrics: map[string]any{"llm_node_ttft": 0.25},
		},
	}

	data := ctx.ToDict()
	items := data["items"].([]map[string]any)
	metrics, ok := items[0]["metrics"].(map[string]any)
	if !ok {
		t.Fatalf("metrics = %#v, want map", items[0]["metrics"])
	}
	if metrics["llm_node_ttft"] != 0.25 {
		t.Fatalf("metrics = %#v, want llm_node_ttft", metrics)
	}

	withoutMetrics := ctx.ToDict(ChatContextDictOptions{ExcludeMetrics: true})
	filteredItems := withoutMetrics["items"].([]map[string]any)
	if _, ok := filteredItems[0]["metrics"]; ok {
		t.Fatalf("metrics present with ExcludeMetrics: %#v", filteredItems[0])
	}
}

func TestChatContextToDictOmitsReferenceNoneOptionalFields(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&FunctionCall{
			ID:        "call_item",
			CallID:    "call_lookup",
			Name:      "lookup",
			Arguments: `{}`,
			CreatedAt: time.Unix(10, 0),
		},
		&AgentHandoff{
			ID:         "handoff_item",
			NewAgentID: "new_agent",
			CreatedAt:  time.Unix(11, 0),
		},
	}

	items := ctx.ToDict(ChatContextDictOptions{IncludeTimestamp: true})["items"].([]map[string]any)
	if _, ok := items[0]["group_id"]; ok {
		t.Fatalf("function_call group_id = %#v, want omitted like reference exclude_none to_dict", items[0]["group_id"])
	}
	if _, ok := items[1]["old_agent_id"]; ok {
		t.Fatalf("agent_handoff old_agent_id = %#v, want omitted like reference exclude_none to_dict", items[1]["old_agent_id"])
	}
}

func TestChatContextMarshalJSONIncludesTimestampsForReports(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{
			ID:        "message",
			Role:      ChatRoleAssistant,
			Content:   []ChatContent{{Text: "hello"}},
			CreatedAt: time.Unix(10, 250000000),
		},
		&FunctionCall{ID: "call", CallID: "call_lookup", Name: "lookup", Arguments: `{"city":"Paris"}`, CreatedAt: time.Unix(11, 0)},
		&FunctionCallOutput{ID: "output", CallID: "call_lookup", Name: "lookup", Output: "Paris", IsError: false, CreatedAt: time.Unix(12, 0)},
		&AgentHandoff{ID: "handoff", NewAgentID: "next", CreatedAt: time.Unix(13, 0)},
	}

	data, err := json.Marshal(ctx)
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	var decoded struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal marshaled context: %v", err)
	}

	if len(decoded.Items) != 4 {
		t.Fatalf("len(items) = %d, want 4: %s", len(decoded.Items), data)
	}
	message := decoded.Items[0]
	if message["type"] != "message" || message["role"] != string(ChatRoleAssistant) {
		t.Fatalf("message = %#v", message)
	}
	if message["created_at"] != 10.25 {
		t.Fatalf("message created_at = %#v, want 10.25", message["created_at"])
	}
	if content, ok := message["content"].([]any); !ok || len(content) != 1 || content[0] != "hello" {
		t.Fatalf("message content = %#v", message["content"])
	}
	call := decoded.Items[1]
	if call["type"] != "function_call" || call["call_id"] != "call_lookup" || call["arguments"] != `{"city":"Paris"}` {
		t.Fatalf("function call = %#v", call)
	}
	output := decoded.Items[2]
	if output["type"] != "function_call_output" || output["is_error"] != false || output["output"] != "Paris" {
		t.Fatalf("function output = %#v", output)
	}
	handoff := decoded.Items[3]
	if handoff["type"] != "agent_handoff" || handoff["new_agent_id"] != "next" {
		t.Fatalf("handoff = %#v", handoff)
	}
}

func TestChatMessageMarshalJSONMatchesReferencePayload(t *testing.T) {
	confidence := 0.875
	message := &ChatMessage{
		ID:                   "message_item",
		Role:                 ChatRoleAssistant,
		Content:              []ChatContent{{Text: "hello"}, {Text: ""}},
		Interrupted:          true,
		TranscriptConfidence: &confidence,
		Extra:                map[string]any{"provider": "openai"},
		Metrics:              map[string]any{"ttft": 0.25},
		CreatedAt:            time.Unix(14, 500_000_000),
	}

	data, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("Marshal ChatMessage returned error: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal marshaled ChatMessage returned error: %v", err)
	}

	want := map[string]any{
		"id":                    "message_item",
		"type":                  "message",
		"role":                  "assistant",
		"content":               []any{"hello", ""},
		"interrupted":           true,
		"transcript_confidence": 0.875,
		"extra":                 map[string]any{"provider": "openai"},
		"metrics":               map[string]any{"ttft": 0.25},
		"created_at":            14.5,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("marshaled ChatMessage = %#v, want %#v", got, want)
	}
	if _, ok := got["TranscriptConfidence"]; ok {
		t.Fatalf("marshaled ChatMessage leaked Go field name TranscriptConfidence: %#v", got)
	}
}

func TestChatItemMarshalJSONMatchesReferencePayloads(t *testing.T) {
	groupID := "parallel_group"
	oldAgentID := "old_agent"
	instructions := "use the handoff"

	items := []struct {
		name string
		item ChatItem
		want map[string]any
	}{
		{
			name: "function_call",
			item: &FunctionCall{
				ID:        "call_item",
				CallID:    "call_lookup",
				Name:      "lookup",
				Arguments: `{"city":"Paris"}`,
				Extra:     map[string]any{"provider": "openai"},
				GroupID:   &groupID,
				CreatedAt: time.Unix(10, 250_000_000),
			},
			want: map[string]any{
				"id":         "call_item",
				"type":       "function_call",
				"call_id":    "call_lookup",
				"name":       "lookup",
				"arguments":  `{"city":"Paris"}`,
				"extra":      map[string]any{"provider": "openai"},
				"group_id":   "parallel_group",
				"created_at": 10.25,
			},
		},
		{
			name: "function_call_output",
			item: &FunctionCallOutput{
				ID:        "output_item",
				CallID:    "call_lookup",
				Name:      "lookup",
				Output:    "sunny",
				IsError:   false,
				CreatedAt: time.Unix(11, 500_000_000),
			},
			want: map[string]any{
				"id":         "output_item",
				"type":       "function_call_output",
				"name":       "lookup",
				"call_id":    "call_lookup",
				"output":     "sunny",
				"is_error":   false,
				"created_at": 11.5,
			},
		},
		{
			name: "agent_handoff",
			item: &AgentHandoff{
				ID:         "handoff_item",
				OldAgentID: &oldAgentID,
				NewAgentID: "new_agent",
				CreatedAt:  time.Unix(12, 750_000_000),
			},
			want: map[string]any{
				"id":           "handoff_item",
				"type":         "agent_handoff",
				"old_agent_id": "old_agent",
				"new_agent_id": "new_agent",
				"created_at":   12.75,
			},
		},
		{
			name: "agent_config_update",
			item: &AgentConfigUpdate{
				ID:           "config_item",
				Instructions: &instructions,
				ToolsAdded:   []string{"lookup"},
				ToolsRemoved: []string{"legacy"},
				CreatedAt:    time.Unix(13, 125_000_000),
			},
			want: map[string]any{
				"id":            "config_item",
				"type":          "agent_config_update",
				"instructions":  "use the handoff",
				"tools_added":   []any{"lookup"},
				"tools_removed": []any{"legacy"},
				"created_at":    13.125,
			},
		},
	}

	for _, tc := range items {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.item)
			if err != nil {
				t.Fatalf("Marshal %s returned error: %v", tc.name, err)
			}

			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("Unmarshal marshaled %s returned error: %v", tc.name, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("marshaled %s = %#v, want %#v", tc.name, got, tc.want)
			}
			if _, ok := got["CallID"]; ok {
				t.Fatalf("marshaled %s leaked Go field name CallID: %#v", tc.name, got)
			}
		})
	}
}

func TestChatItemMarshalJSONMatchesReferenceOptionalFields(t *testing.T) {
	items := []struct {
		name          string
		item          ChatItem
		optionalField string
	}{
		{
			name: "function_call_group",
			item: &FunctionCall{
				ID:        "call_item",
				CallID:    "call_lookup",
				Name:      "lookup",
				Arguments: `{}`,
				CreatedAt: time.Unix(10, 0),
			},
			optionalField: "group_id",
		},
		{
			name: "agent_handoff_old_agent",
			item: &AgentHandoff{
				ID:         "handoff_item",
				NewAgentID: "new_agent",
				CreatedAt:  time.Unix(11, 0),
			},
			optionalField: "old_agent_id",
		},
	}

	for _, tc := range items {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.item)
			if err != nil {
				t.Fatalf("Marshal %s returned error: %v", tc.name, err)
			}

			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("Unmarshal marshaled %s returned error: %v", tc.name, err)
			}
			if _, ok := got[tc.optionalField]; !ok {
				t.Fatalf("%s missing from marshaled %s: %s", tc.optionalField, tc.name, data)
			}
			if got[tc.optionalField] != nil {
				t.Fatalf("%s = %v, want JSON null; payload %s", tc.optionalField, got[tc.optionalField], data)
			}
		})
	}
}

func TestChatContentMarshalJSONMatchesReferencePayloads(t *testing.T) {
	width := 320
	content := []struct {
		name string
		item any
		want map[string]any
	}{
		{
			name: "instructions_audio_only",
			item: NewInstructions("voice instructions"),
			want: map[string]any{
				"type":  "instructions",
				"audio": "voice instructions",
			},
		},
		{
			name: "instructions_text_variant",
			item: NewInstructions("voice instructions", "text instructions").AsModality("text"),
			want: map[string]any{
				"type":  "instructions",
				"audio": "voice instructions",
				"text":  "text instructions",
			},
		},
		{
			name: "image_content",
			item: &ImageContent{
				ID:             "image_item",
				Image:          "https://example.test/image.png",
				InferenceWidth: &width,
			},
			want: map[string]any{
				"id":               "image_item",
				"type":             "image_content",
				"image":            "https://example.test/image.png",
				"inference_width":  320.0,
				"inference_height": nil,
				"inference_detail": "auto",
				"mime_type":        nil,
			},
		},
		{
			name: "audio_content",
			item: &AudioContent{
				Frames: []any{},
			},
			want: map[string]any{
				"type":       "audio_content",
				"frame":      []any{},
				"transcript": nil,
			},
		},
	}

	for _, tc := range content {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.item)
			if err != nil {
				t.Fatalf("Marshal %s returned error: %v", tc.name, err)
			}

			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("Unmarshal marshaled %s returned error: %v", tc.name, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("marshaled %s = %#v, want %#v", tc.name, got, tc.want)
			}
			if _, ok := got["InferenceWidth"]; ok {
				t.Fatalf("marshaled %s leaked Go field names: %#v", tc.name, got)
			}
			if _, ok := got["Audio"]; ok {
				t.Fatalf("marshaled %s leaked Go field names: %#v", tc.name, got)
			}
		})
	}
}

func TestChatContextUnmarshalJSONRestoresTypedItems(t *testing.T) {
	data := []byte(`{
		"items": [
			{
				"id": "message",
				"type": "message",
				"role": "assistant",
				"content": [
					"hello",
					{
						"id": "image",
						"type": "image_content",
						"image": "https://example.test/image.png",
						"inference_width": 320,
						"inference_height": 240,
						"inference_detail": "high",
						"mime_type": "image/png"
					},
					{
						"type": "audio_content",
						"frame": [],
						"transcript": "audio text"
					}
				],
				"interrupted": true,
				"transcript_confidence": 0.75,
				"extra": {"source": "test"},
				"metrics": {"llm_node_ttft": 0.5},
				"created_at": 10.25
			},
			{
				"id": "call",
				"type": "function_call",
				"call_id": "call_lookup",
				"name": "lookup",
				"arguments": "{\"city\":\"Paris\"}",
				"extra": {"provider": "test"},
				"group_id": "assistant-turn",
				"created_at": 11
			},
			{
				"id": "output",
				"type": "function_call_output",
				"call_id": "call_lookup",
				"name": "lookup",
				"output": "Paris",
				"is_error": true,
				"created_at": 12
			},
			{
				"id": "handoff",
				"type": "agent_handoff",
				"old_agent_id": "old",
				"new_agent_id": "new",
				"created_at": 13
			},
			{
				"id": "config",
				"type": "agent_config_update",
				"instructions": "be concise",
				"tools_added": ["lookup"],
				"tools_removed": ["search"],
				"created_at": 14
			}
		]
	}`)

	var ctx ChatContext
	if err := json.Unmarshal(data, &ctx); err != nil {
		t.Fatalf("UnmarshalJSON() error = %v", err)
	}

	if len(ctx.Items) != 5 {
		t.Fatalf("len(items) = %d, want 5", len(ctx.Items))
	}
	message, ok := ctx.Items[0].(*ChatMessage)
	if !ok {
		t.Fatalf("item[0] = %T, want *ChatMessage", ctx.Items[0])
	}
	if message.ID != "message" || message.Role != ChatRoleAssistant || !message.Interrupted {
		t.Fatalf("message fields = %#v", message)
	}
	if message.TranscriptConfidence == nil || *message.TranscriptConfidence != 0.75 {
		t.Fatalf("transcript confidence = %#v", message.TranscriptConfidence)
	}
	if message.Metrics["llm_node_ttft"] != 0.5 {
		t.Fatalf("metrics = %#v, want llm_node_ttft", message.Metrics)
	}
	if !message.CreatedAt.Equal(time.Unix(10, 250000000)) {
		t.Fatalf("message CreatedAt = %v, want unix 10.25", message.CreatedAt)
	}
	if len(message.Content) != 3 || message.Content[0].Text != "hello" {
		t.Fatalf("message content = %#v", message.Content)
	}
	if message.Content[1].Image == nil || message.Content[1].Image.ID != "image" || message.Content[1].Image.InferenceWidth == nil || *message.Content[1].Image.InferenceWidth != 320 {
		t.Fatalf("image content = %#v", message.Content[1].Image)
	}
	if message.Content[2].Audio == nil || message.Content[2].Audio.Transcript != "audio text" {
		t.Fatalf("audio content = %#v", message.Content[2].Audio)
	}

	call, ok := ctx.Items[1].(*FunctionCall)
	if !ok {
		t.Fatalf("item[1] = %T, want *FunctionCall", ctx.Items[1])
	}
	if call.CallID != "call_lookup" || call.Name != "lookup" || call.GroupID == nil || *call.GroupID != "assistant-turn" {
		t.Fatalf("function call = %#v", call)
	}

	output, ok := ctx.Items[2].(*FunctionCallOutput)
	if !ok {
		t.Fatalf("item[2] = %T, want *FunctionCallOutput", ctx.Items[2])
	}
	if output.Output != "Paris" || !output.IsError {
		t.Fatalf("function output = %#v", output)
	}

	handoff, ok := ctx.Items[3].(*AgentHandoff)
	if !ok {
		t.Fatalf("item[3] = %T, want *AgentHandoff", ctx.Items[3])
	}
	if handoff.OldAgentID == nil || *handoff.OldAgentID != "old" || handoff.NewAgentID != "new" {
		t.Fatalf("handoff = %#v", handoff)
	}

	config, ok := ctx.Items[4].(*AgentConfigUpdate)
	if !ok {
		t.Fatalf("item[4] = %T, want *AgentConfigUpdate", ctx.Items[4])
	}
	if config.Instructions == nil || *config.Instructions != "be concise" || !reflect.DeepEqual(config.ToolsAdded, []string{"lookup"}) || !reflect.DeepEqual(config.ToolsRemoved, []string{"search"}) {
		t.Fatalf("config = %#v", config)
	}
}

func TestChatContextUnmarshalJSONAcceptsAgentConfigInstructionObject(t *testing.T) {
	data := []byte(`{
		"items": [{
			"id": "config",
			"type": "agent_config_update",
			"instructions": {
				"type": "instructions",
				"audio": "speak plainly",
				"text": "write tersely"
			}
		}]
	}`)

	var ctx ChatContext
	if err := json.Unmarshal(data, &ctx); err != nil {
		t.Fatalf("UnmarshalJSON() error = %v", err)
	}

	config, ok := ctx.Items[0].(*AgentConfigUpdate)
	if !ok {
		t.Fatalf("item[0] = %T, want *AgentConfigUpdate", ctx.Items[0])
	}
	if config.Instructions == nil || *config.Instructions != "speak plainly" {
		t.Fatalf("Instructions = %#v, want audio instruction text", config.Instructions)
	}

	serialized := ctx.ToDict()
	items := serialized["items"].([]map[string]any)
	instructions, ok := items[0]["instructions"].(map[string]any)
	if !ok {
		t.Fatalf("serialized instructions = %#v, want instructions object", items[0]["instructions"])
	}
	if instructions["type"] != "instructions" || instructions["audio"] != "speak plainly" || instructions["text"] != "write tersely" {
		t.Fatalf("serialized instructions = %#v, want preserved variants", instructions)
	}
}

func TestChatContextImageContentDefaultsInferenceDetail(t *testing.T) {
	data := []byte(`{
		"items": [{
			"id": "message",
			"type": "message",
			"role": "user",
			"content": [{
				"id": "image",
				"type": "image_content",
				"image": "https://example.test/image.png"
			}]
		}]
	}`)

	var ctx ChatContext
	if err := json.Unmarshal(data, &ctx); err != nil {
		t.Fatalf("UnmarshalJSON() error = %v", err)
	}

	message := ctx.Items[0].(*ChatMessage)
	if got := message.Content[0].Image.InferenceDetail; got != "auto" {
		t.Fatalf("InferenceDetail = %q, want auto", got)
	}

	encoded := ctx.ToDict(ChatContextDictOptions{IncludeImage: true})
	items := encoded["items"].([]map[string]any)
	content := items[0]["content"].([]any)
	image := content[0].(map[string]any)
	if got := image["inference_detail"]; got != "auto" {
		t.Fatalf("serialized inference_detail = %#v, want auto", got)
	}
}

func TestChatContextImageContentDefaultsID(t *testing.T) {
	data := []byte(`{
		"items": [{
			"id": "message",
			"type": "message",
			"role": "user",
			"content": [{
				"type": "image_content",
				"image": "https://example.test/image.png"
			}]
		}]
	}`)

	var ctx ChatContext
	if err := json.Unmarshal(data, &ctx); err != nil {
		t.Fatalf("UnmarshalJSON() error = %v", err)
	}

	message := ctx.Items[0].(*ChatMessage)
	id := message.Content[0].Image.ID
	if !strings.HasPrefix(id, "img_") {
		t.Fatalf("ImageContent.ID = %q, want img_ prefix", id)
	}

	encoded := ctx.ToDict(ChatContextDictOptions{IncludeImage: true})
	items := encoded["items"].([]map[string]any)
	content := items[0]["content"].([]any)
	image := content[0].(map[string]any)
	if got := image["id"]; got != id {
		t.Fatalf("serialized id = %#v, want %q", got, id)
	}
}

func TestChatContextItemsDefaultIDs(t *testing.T) {
	data := []byte(`{
		"items": [
			{"type": "message", "role": "user", "content": ["hello"]},
			{"type": "function_call", "call_id": "call_lookup", "name": "lookup", "arguments": "{}"},
			{"type": "function_call_output", "call_id": "call_lookup", "name": "lookup", "output": "ok", "is_error": false},
			{"type": "agent_handoff", "new_agent_id": "next"},
			{"type": "agent_config_update", "instructions": "be concise"}
		]
	}`)

	var ctx ChatContext
	if err := json.Unmarshal(data, &ctx); err != nil {
		t.Fatalf("UnmarshalJSON() error = %v", err)
	}

	for i, item := range ctx.Items {
		if !strings.HasPrefix(item.GetID(), "item_") {
			t.Fatalf("item %d id = %q, want item_ prefix", i, item.GetID())
		}
	}

	encoded := ctx.ToDict()
	items := encoded["items"].([]map[string]any)
	for i, item := range items {
		if got := item["id"]; got != ctx.Items[i].GetID() {
			t.Fatalf("serialized item %d id = %#v, want %q", i, got, ctx.Items[i].GetID())
		}
	}
}

func TestChatContextItemsDefaultCreatedAt(t *testing.T) {
	data := []byte(`{
		"items": [
			{"type": "message", "role": "user", "content": ["hello"]},
			{"type": "function_call", "call_id": "call_lookup", "name": "lookup", "arguments": "{}"},
			{"type": "function_call_output", "call_id": "call_lookup", "name": "lookup", "output": "ok", "is_error": false},
			{"type": "agent_handoff", "new_agent_id": "next"},
			{"type": "agent_config_update", "instructions": "be concise"}
		]
	}`)

	var ctx ChatContext
	if err := json.Unmarshal(data, &ctx); err != nil {
		t.Fatalf("UnmarshalJSON() error = %v", err)
	}

	for i, item := range ctx.Items {
		if item.GetCreatedAt().IsZero() {
			t.Fatalf("item %d CreatedAt is zero, want generated timestamp", i)
		}
	}
}

func TestChatContextUnmarshalJSONRejectsUnknownItemType(t *testing.T) {
	data := []byte(`{"items":[{"id":"bad","type":"unknown"}]}`)

	var ctx ChatContext
	if err := json.Unmarshal(data, &ctx); err == nil {
		t.Fatal("UnmarshalJSON() error = nil, want error")
	}
}

func TestChatContextUnmarshalJSONRejectsMissingItems(t *testing.T) {
	for name, data := range map[string][]byte{
		"missing": []byte(`{}`),
		"null":    []byte(`{"items":null}`),
	} {
		t.Run(name, func(t *testing.T) {
			ctx := NewChatContext()
			ctx.Append(&ChatMessage{ID: "existing", Role: ChatRoleUser, Content: []ChatContent{{Text: "keep"}}})
			if err := json.Unmarshal(data, ctx); err == nil {
				t.Fatal("UnmarshalJSON() error = nil, want items error")
			}
			if len(ctx.Items) != 1 {
				t.Fatalf("len(items) after rejected UnmarshalJSON = %d, want existing item preserved", len(ctx.Items))
			}
		})
	}
}

func TestChatContextUnmarshalJSONRejectsAudioContentMissingFrame(t *testing.T) {
	var ctx ChatContext
	data := []byte(`{
		"items": [
			{
				"id": "message",
				"type": "message",
				"role": "user",
				"content": [
					{
						"type": "audio_content",
						"transcript": "audio text"
					}
				]
			}
		]
	}`)

	if err := json.Unmarshal(data, &ctx); err == nil {
		t.Fatal("Unmarshal ChatContext error = nil, want missing audio_content frame error")
	}
}

func TestChatContextFromDictRestoresContext(t *testing.T) {
	data := map[string]any{
		"items": []map[string]any{
			{
				"id":         "message",
				"type":       "message",
				"role":       "user",
				"content":    []any{"hello"},
				"created_at": 10.0,
			},
			{
				"id":         "call",
				"type":       "function_call",
				"call_id":    "call_lookup",
				"name":       "lookup",
				"arguments":  "{}",
				"created_at": 11.0,
			},
		},
	}

	ctx, err := ChatContextFromDict(data)
	if err != nil {
		t.Fatalf("ChatContextFromDict() error = %v", err)
	}

	if len(ctx.Items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(ctx.Items))
	}
	if msg, ok := ctx.Items[0].(*ChatMessage); !ok || msg.ID != "message" || msg.TextContent() != "hello" {
		t.Fatalf("item[0] = %#v, want message", ctx.Items[0])
	}
	if call, ok := ctx.Items[1].(*FunctionCall); !ok || call.CallID != "call_lookup" {
		t.Fatalf("item[1] = %#v, want function call", ctx.Items[1])
	}
}

func TestChatContextFromDictMethodReplacesReceiverItems(t *testing.T) {
	data := map[string]any{
		"items": []map[string]any{
			{
				"id":      "replacement",
				"type":    "message",
				"role":    "assistant",
				"content": []any{"ready"},
			},
		},
	}
	ctx := NewChatContext()
	ctx.Append(&ChatMessage{ID: "old", Role: ChatRoleUser, Content: []ChatContent{{Text: "old"}}})

	if err := ctx.FromDict(data); err != nil {
		t.Fatalf("FromDict() error = %v", err)
	}

	if len(ctx.Items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(ctx.Items))
	}
	msg, ok := ctx.Items[0].(*ChatMessage)
	if !ok || msg.ID != "replacement" || msg.TextContent() != "ready" {
		t.Fatalf("item[0] = %#v, want replacement assistant message", ctx.Items[0])
	}
}

func TestChatContextFromDictRejectsMissingItems(t *testing.T) {
	for name, data := range map[string]map[string]any{
		"missing": {},
		"nil":     {"items": nil},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ChatContextFromDict(data); err == nil {
				t.Fatal("ChatContextFromDict() error = nil, want items error")
			}

			ctx := NewChatContext()
			ctx.Append(&ChatMessage{ID: "existing", Role: ChatRoleUser, Content: []ChatContent{{Text: "keep"}}})
			if err := ctx.FromDict(data); err == nil {
				t.Fatal("FromDict() error = nil, want items error")
			}
			if len(ctx.Items) != 1 {
				t.Fatalf("len(items) after rejected FromDict = %d, want existing item preserved", len(ctx.Items))
			}
		})
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

func TestChatContextToOpenAIProviderFormatPreservesMultipleMatchedToolOutputs(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{ID: "assistant", Role: ChatRoleAssistant, Content: []ChatContent{{Text: "checking"}}},
		&FunctionCall{ID: "assistant/tool", CallID: "call_lookup", Name: "lookup", Arguments: `{"city":"Paris"}`},
		&FunctionCallOutput{ID: "output-1", CallID: "call_lookup", Name: "lookup", Output: "first"},
		&FunctionCallOutput{ID: "output-2", CallID: "call_lookup", Name: "lookup", Output: "second"},
	}

	messages, _ := ctx.ToProviderFormat("openai")

	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want assistant plus two tool outputs: %#v", len(messages), messages)
	}
	for i, want := range []string{"first", "second"} {
		msg := messages[i+1]
		if msg["role"] != "tool" || msg["tool_call_id"] != "call_lookup" || msg["content"] != want {
			t.Fatalf("tool output message %d = %#v, want content %q for call_lookup", i+1, msg, want)
		}
	}
}

func TestChatContextToOpenAIProviderFormatIncludesImageContent(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{
			ID:   "user",
			Role: ChatRoleUser,
			Content: []ChatContent{
				{Text: "describe this"},
				{Image: &ImageContent{
					Image:           "https://example.test/image.png",
					InferenceDetail: "high",
				}},
			},
		},
	}

	messages, _ := ctx.ToProviderFormat("openai")

	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1: %#v", len(messages), messages)
	}
	content, ok := messages[0]["content"].([]map[string]any)
	if !ok {
		t.Fatalf("content = %#v, want []map[string]any", messages[0]["content"])
	}
	if len(content) != 2 {
		t.Fatalf("len(content) = %d, want 2: %#v", len(content), content)
	}
	imageURL, ok := content[0]["image_url"].(map[string]any)
	if !ok || content[0]["type"] != "image_url" {
		t.Fatalf("image content = %#v, want image_url part", content[0])
	}
	if imageURL["url"] != "https://example.test/image.png" || imageURL["detail"] != "high" {
		t.Fatalf("image_url = %#v", imageURL)
	}
	if content[1]["type"] != "text" || content[1]["text"] != "describe this" {
		t.Fatalf("text content = %#v", content[1])
	}
}

func TestChatContextToOpenAIProviderFormatSerializesVideoFrameImage(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{
			ID:   "user",
			Role: ChatRoleUser,
			Content: []ChatContent{
				{Image: &ImageContent{
					Image: &images.VideoFrame{
						Width:  1,
						Height: 1,
						Format: "rgba",
						Data:   []byte{255, 0, 0, 255},
					},
					InferenceDetail: "low",
				}},
			},
		},
	}

	messages, _, err := ctx.ToProviderFormatE("openai")
	if err != nil {
		t.Fatalf("ToProviderFormatE() error = %v", err)
	}

	content := messages[0]["content"].([]map[string]any)
	imageURL := content[0]["image_url"].(map[string]any)
	url, _ := imageURL["url"].(string)
	if !strings.HasPrefix(url, "data:image/jpeg;base64,") {
		t.Fatalf("image URL = %q, want JPEG data URL", url)
	}
	if imageURL["detail"] != "low" {
		t.Fatalf("image detail = %#v, want low", imageURL["detail"])
	}
}

func TestChatContextToOpenAIProviderFormatReturnsImageSerializationError(t *testing.T) {
	for _, format := range []string{"openai", "openai.responses"} {
		t.Run(format, func(t *testing.T) {
			ctx := NewChatContext()
			ctx.Items = []ChatItem{
				&ChatMessage{
					ID:   "user",
					Role: ChatRoleUser,
					Content: []ChatContent{
						{Image: &ImageContent{Image: "data:image/png;base64,not-valid-base64"}},
					},
				},
			}

			messages, extra, err := ctx.ToProviderFormatE(format)

			if err == nil {
				t.Fatalf("ToProviderFormatE(%s) error = nil, want image serialization error", format)
			}
			if messages != nil || extra != nil {
				t.Fatalf("ToProviderFormatE(%s) messages=%#v extra=%#v, want nil outputs on error", format, messages, extra)
			}
			if !strings.Contains(err.Error(), "decode data URL image") {
				t.Fatalf("ToProviderFormatE(%s) error = %q, want decode data URL image error", format, err)
			}
		})
	}
}

func TestChatContextToOpenAIProviderFormatForwardsReferenceExtraContent(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{
			ID:      "user",
			Role:    ChatRoleUser,
			Content: []ChatContent{{Text: "hello"}},
			Extra: map[string]any{
				"google":  map[string]any{"thought_signature": "sig"},
				"ignored": "value",
			},
		},
		&ChatMessage{ID: "assistant", Role: ChatRoleAssistant, Content: []ChatContent{{Text: "checking"}}},
		&FunctionCall{
			ID:        "assistant/tool",
			CallID:    "call_lookup",
			Name:      "lookup",
			Arguments: `{}`,
			Extra: map[string]any{
				"xai":     map[string]any{"reasoning": "trace"},
				"ignored": "value",
			},
		},
		&FunctionCallOutput{ID: "output", CallID: "call_lookup", Name: "lookup", Output: "ok"},
	}

	messages, _ := ctx.ToProviderFormat("openai")

	userExtra, ok := messages[0]["extra_content"].(map[string]any)
	if !ok {
		t.Fatalf("user extra_content = %#v, want map", messages[0]["extra_content"])
	}
	if _, ok := userExtra["ignored"]; ok {
		t.Fatalf("user extra_content includes ignored key: %#v", userExtra)
	}
	if _, ok := userExtra["google"]; !ok {
		t.Fatalf("user extra_content = %#v, want google key", userExtra)
	}

	toolCalls := messages[1]["tool_calls"].([]map[string]any)
	toolExtra, ok := toolCalls[0]["extra_content"].(map[string]any)
	if !ok {
		t.Fatalf("tool extra_content = %#v, want map", toolCalls[0]["extra_content"])
	}
	if _, ok := toolExtra["ignored"]; ok {
		t.Fatalf("tool extra_content includes ignored key: %#v", toolExtra)
	}
	if _, ok := toolExtra["xai"]; !ok {
		t.Fatalf("tool extra_content = %#v, want xai key", toolExtra)
	}
}

func TestChatContextToOpenAIProviderFormatSkipsEmptyReferenceExtraContent(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{
			ID:      "user",
			Role:    ChatRoleUser,
			Content: []ChatContent{{Text: "hello"}},
			Extra: map[string]any{
				"google":  false,
				"livekit": "",
				"xai":     map[string]any{},
			},
		},
		&ChatMessage{ID: "assistant", Role: ChatRoleAssistant, Content: []ChatContent{{Text: "checking"}}},
		&FunctionCall{
			ID:        "assistant/tool",
			CallID:    "call_lookup",
			Name:      "lookup",
			Arguments: `{}`,
			Extra: map[string]any{
				"google":  []any{},
				"livekit": 0,
				"xai":     nil,
			},
		},
		&FunctionCallOutput{ID: "output", CallID: "call_lookup", Name: "lookup", Output: "ok"},
	}

	messages, _ := ctx.ToProviderFormat("openai")

	if _, ok := messages[0]["extra_content"]; ok {
		t.Fatalf("user extra_content = %#v, want omitted", messages[0]["extra_content"])
	}
	toolCalls := messages[1]["tool_calls"].([]map[string]any)
	if _, ok := toolCalls[0]["extra_content"]; ok {
		t.Fatalf("tool extra_content = %#v, want omitted", toolCalls[0]["extra_content"])
	}
}

func TestChatContextToOpenAIResponsesProviderFormat(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{
			ID:   "user",
			Role: ChatRoleUser,
			Content: []ChatContent{
				{Text: "describe this"},
				{Image: &ImageContent{
					Image:           "https://example.test/image.png",
					InferenceDetail: "low",
				}},
			},
		},
		&ChatMessage{
			ID:      "assistant-turn",
			Role:    ChatRoleAssistant,
			Content: []ChatContent{{Text: "checking"}},
			Extra: map[string]any{
				"openai": map[string]any{"phase": "commentary"},
			},
		},
		&FunctionCall{ID: "assistant-turn/tool", CallID: "call_lookup", Name: "lookup", Arguments: `{"city":"Paris"}`},
		&FunctionCallOutput{ID: "lookup-output", CallID: "call_lookup", Name: "lookup", Output: "Paris"},
	}

	items, extra := ctx.ToProviderFormat("openai.responses")

	if extra != nil {
		t.Fatalf("ToProviderFormat() extra = %#v, want nil", extra)
	}
	if len(items) != 4 {
		t.Fatalf("len(items) = %d, want 4: %#v", len(items), items)
	}
	content, ok := items[0]["content"].([]map[string]any)
	if !ok {
		t.Fatalf("responses content = %#v, want []map[string]any", items[0]["content"])
	}
	if content[0]["type"] != "input_image" || content[0]["image_url"] != "https://example.test/image.png" || content[0]["detail"] != "low" {
		t.Fatalf("responses image content = %#v", content[0])
	}
	if content[1]["type"] != "input_text" || content[1]["text"] != "describe this" {
		t.Fatalf("responses text content = %#v", content[1])
	}
	if items[1]["role"] != "assistant" || items[1]["content"] != "checking" {
		t.Fatalf("assistant item = %#v", items[1])
	}
	if items[1]["phase"] != "commentary" {
		t.Fatalf("assistant phase = %#v, want commentary", items[1]["phase"])
	}
	if items[2]["type"] != "function_call" || items[2]["call_id"] != "call_lookup" || items[2]["name"] != "lookup" || items[2]["arguments"] != `{"city":"Paris"}` {
		t.Fatalf("function call item = %#v", items[2])
	}
	if items[3]["type"] != "function_call_output" || items[3]["call_id"] != "call_lookup" || items[3]["output"] != "Paris" {
		t.Fatalf("function output item = %#v", items[3])
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

func TestChatContextToGoogleProviderFormatMapsTurnsAndSystemMessages(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{ID: "system", Role: ChatRoleSystem, Content: []ChatContent{{Text: "be concise"}}},
		&ChatMessage{ID: "user", Role: ChatRoleUser, Content: []ChatContent{{Text: "weather?"}}},
		&ChatMessage{ID: "assistant-turn", Role: ChatRoleAssistant, Content: []ChatContent{{Text: "checking"}}},
		&FunctionCall{ID: "assistant-turn/tool", CallID: "call_weather", Name: "weather", Arguments: `{"city":"Paris"}`},
		&FunctionCallOutput{ID: "weather-output", CallID: "call_weather", Name: "weather", Output: "sunny"},
	}

	turns, extra := ctx.ToProviderFormat("google")

	data, ok := extra.(map[string]any)
	if !ok {
		t.Fatalf("google extra = %#v, want map", extra)
	}
	if !reflect.DeepEqual(data["system_messages"], []string{"be concise"}) {
		t.Fatalf("system_messages = %#v", data["system_messages"])
	}
	if len(turns) != 3 {
		t.Fatalf("len(turns) = %d, want 3: %#v", len(turns), turns)
	}
	if turns[0]["role"] != "user" {
		t.Fatalf("first turn = %#v, want user", turns[0])
	}
	userParts := turns[0]["parts"].([]map[string]any)
	if userParts[0]["text"] != "weather?" {
		t.Fatalf("user parts = %#v", userParts)
	}
	if turns[1]["role"] != "model" {
		t.Fatalf("second turn = %#v, want model", turns[1])
	}
	modelParts := turns[1]["parts"].([]map[string]any)
	functionCall := modelParts[1]["function_call"].(map[string]any)
	if functionCall["id"] != "call_weather" || functionCall["name"] != "weather" {
		t.Fatalf("function_call = %#v", functionCall)
	}
	args := functionCall["args"].(map[string]any)
	if args["city"] != "Paris" {
		t.Fatalf("function args = %#v", args)
	}
	if turns[2]["role"] != "user" {
		t.Fatalf("third turn = %#v, want user tool response", turns[2])
	}
	toolParts := turns[2]["parts"].([]map[string]any)
	functionResponse := toolParts[0]["function_response"].(map[string]any)
	if functionResponse["id"] != "call_weather" || functionResponse["name"] != "weather" {
		t.Fatalf("function_response = %#v", functionResponse)
	}
	response := functionResponse["response"].(map[string]any)
	if response["output"] != "sunny" {
		t.Fatalf("function response payload = %#v", response)
	}
}

func TestChatContextToGoogleProviderFormatInlinesMidConversationInstructions(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{ID: "system", Role: ChatRoleSystem, Content: []ChatContent{{Text: "base instructions"}}},
		&ChatMessage{ID: "turn-instructions", Role: ChatRoleSystem, Content: []ChatContent{{Text: "use short sentences"}}},
	}

	turns, extra := ctx.ToProviderFormat("google")

	data := extra.(map[string]any)
	if !reflect.DeepEqual(data["system_messages"], []string{"base instructions"}) {
		t.Fatalf("system_messages = %#v", data["system_messages"])
	}
	if len(turns) != 1 {
		t.Fatalf("len(turns) = %d, want 1: %#v", len(turns), turns)
	}
	if turns[0]["role"] != "user" {
		t.Fatalf("turn = %#v, want user", turns[0])
	}
	parts := turns[0]["parts"].([]map[string]any)
	if parts[0]["text"] != "<instructions>\nuse short sentences\n</instructions>" {
		t.Fatalf("inline instruction part = %#v", parts[0])
	}
}

func TestChatContextToGoogleProviderFormatUsesInlineDataForDataURLImage(t *testing.T) {
	imageData := base64.StdEncoding.EncodeToString([]byte("png-bytes"))
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{
			ID:   "user",
			Role: ChatRoleUser,
			Content: []ChatContent{{Image: &ImageContent{
				Image: "data:image/png;base64," + imageData,
			}}},
		},
	}

	turns, _ := ctx.ToProviderFormat("google")

	parts := turns[0]["parts"].([]map[string]any)
	inlineData, ok := parts[0]["inline_data"].(map[string]any)
	if !ok {
		t.Fatalf("google image part = %#v, want inline_data", parts[0])
	}
	if !reflect.DeepEqual(inlineData["data"], []byte("png-bytes")) || inlineData["mime_type"] != "image/png" {
		t.Fatalf("inline_data = %#v", inlineData)
	}
}

func TestChatContextToGoogleProviderFormatReturnsImageSerializationError(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{
			ID:   "user",
			Role: ChatRoleUser,
			Content: []ChatContent{
				{Image: &ImageContent{Image: "data:image/png;base64,not-valid-base64"}},
			},
		},
	}

	turns, extra, err := ctx.ToProviderFormatE("google")

	if err == nil {
		t.Fatal("ToProviderFormatE(google) error = nil, want image serialization error")
	}
	if turns != nil || extra != nil {
		t.Fatalf("ToProviderFormatE(google) turns=%#v extra=%#v, want nil outputs on error", turns, extra)
	}
	if !strings.Contains(err.Error(), "decode data URL image") {
		t.Fatalf("ToProviderFormatE(google) error = %q, want decode data URL image error", err)
	}
}

func TestChatContextToAnthropicProviderFormatMapsTurnsAndSystemMessages(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{ID: "system", Role: ChatRoleSystem, Content: []ChatContent{{Text: "be concise"}}},
		&ChatMessage{ID: "assistant-turn", Role: ChatRoleAssistant, Content: []ChatContent{{Text: "checking"}}},
		&FunctionCall{ID: "assistant-turn/tool", CallID: "call_weather", Name: "weather", Arguments: `{"city":"Paris"}`},
		&FunctionCallOutput{ID: "weather-output", CallID: "call_weather", Name: "weather", Output: `["sunny"]`},
	}

	messages, extra := ctx.ToProviderFormat("anthropic")

	data, ok := extra.(map[string]any)
	if !ok {
		t.Fatalf("anthropic extra = %#v, want map", extra)
	}
	if !reflect.DeepEqual(data["system_messages"], []string{"be concise"}) {
		t.Fatalf("system_messages = %#v", data["system_messages"])
	}
	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want 3: %#v", len(messages), messages)
	}
	if messages[0]["role"] != "user" {
		t.Fatalf("first message = %#v, want dummy user", messages[0])
	}
	dummyContent := messages[0]["content"].([]map[string]any)
	if dummyContent[0]["type"] != "text" || dummyContent[0]["text"] != "(empty)" {
		t.Fatalf("dummy content = %#v", dummyContent)
	}
	if messages[1]["role"] != "assistant" {
		t.Fatalf("second message = %#v, want assistant", messages[1])
	}
	assistantContent := messages[1]["content"].([]map[string]any)
	if assistantContent[0]["type"] != "text" || assistantContent[0]["text"] != "checking" {
		t.Fatalf("assistant text = %#v", assistantContent[0])
	}
	toolUse := assistantContent[1]
	if toolUse["type"] != "tool_use" || toolUse["id"] != "call_weather" || toolUse["name"] != "weather" {
		t.Fatalf("tool use = %#v", toolUse)
	}
	input := toolUse["input"].(map[string]any)
	if input["city"] != "Paris" {
		t.Fatalf("tool input = %#v", input)
	}
	if messages[2]["role"] != "user" {
		t.Fatalf("third message = %#v, want user tool result", messages[2])
	}
	userContent := messages[2]["content"].([]map[string]any)
	toolResult := userContent[0]
	if toolResult["type"] != "tool_result" || toolResult["tool_use_id"] != "call_weather" || toolResult["is_error"] != false {
		t.Fatalf("tool result = %#v", toolResult)
	}
	if !reflect.DeepEqual(toolResult["content"], []any{"sunny"}) {
		t.Fatalf("tool result content = %#v", toolResult["content"])
	}
}

func TestChatContextToAnthropicProviderFormatUsesBase64ForDataURLImage(t *testing.T) {
	imageData := base64.StdEncoding.EncodeToString([]byte("gif-bytes"))
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{
			ID:   "user",
			Role: ChatRoleUser,
			Content: []ChatContent{{Image: &ImageContent{
				Image: "data:image/gif;base64," + imageData,
			}}},
		},
	}

	messages, _ := ctx.ToProviderFormat("anthropic")

	content := messages[0]["content"].([]map[string]any)
	source, ok := content[0]["source"].(map[string]any)
	if !ok || content[0]["type"] != "image" {
		t.Fatalf("anthropic image content = %#v, want image source", content[0])
	}
	if source["type"] != "base64" || source["data"] != imageData || source["media_type"] != "image/gif" {
		t.Fatalf("anthropic image source = %#v", source)
	}
}

func TestChatContextToAnthropicProviderFormatReturnsImageSerializationError(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{
			ID:   "user",
			Role: ChatRoleUser,
			Content: []ChatContent{
				{Image: &ImageContent{Image: "data:image/png;base64,not-valid-base64"}},
			},
		},
	}

	messages, extra, err := ctx.ToProviderFormatE("anthropic")

	if err == nil {
		t.Fatal("ToProviderFormatE(anthropic) error = nil, want image serialization error")
	}
	if messages != nil || extra != nil {
		t.Fatalf("ToProviderFormatE(anthropic) messages=%#v extra=%#v, want nil outputs on error", messages, extra)
	}
	if !strings.Contains(err.Error(), "decode data URL image") {
		t.Fatalf("ToProviderFormatE(anthropic) error = %q, want decode data URL image error", err)
	}
}

func TestChatContextToAWSProviderFormatMapsTurnsAndSystemMessages(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{ID: "system", Role: ChatRoleSystem, Content: []ChatContent{{Text: "be concise"}}},
		&ChatMessage{ID: "assistant-turn", Role: ChatRoleAssistant, Content: []ChatContent{{Text: "checking"}}},
		&FunctionCall{ID: "assistant-turn/tool", CallID: "call_weather", Name: "weather", Arguments: `{"city":"Paris"}`},
		&FunctionCallOutput{ID: "weather-output", CallID: "call_weather", Name: "weather", Output: "sunny"},
	}

	messages, extra := ctx.ToProviderFormat("aws")

	data, ok := extra.(map[string]any)
	if !ok {
		t.Fatalf("aws extra = %#v, want map", extra)
	}
	if !reflect.DeepEqual(data["system_messages"], []string{"be concise"}) {
		t.Fatalf("system_messages = %#v", data["system_messages"])
	}
	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want 3: %#v", len(messages), messages)
	}
	if messages[0]["role"] != "user" {
		t.Fatalf("first message = %#v, want dummy user", messages[0])
	}
	dummyContent := messages[0]["content"].([]map[string]any)
	if dummyContent[0]["text"] != "(empty)" {
		t.Fatalf("dummy content = %#v", dummyContent)
	}
	if messages[1]["role"] != "assistant" {
		t.Fatalf("second message = %#v, want assistant", messages[1])
	}
	assistantContent := messages[1]["content"].([]map[string]any)
	if assistantContent[0]["text"] != "checking" {
		t.Fatalf("assistant text = %#v", assistantContent[0])
	}
	toolUse := assistantContent[1]["toolUse"].(map[string]any)
	if toolUse["toolUseId"] != "call_weather" || toolUse["name"] != "weather" {
		t.Fatalf("toolUse = %#v", toolUse)
	}
	input := toolUse["input"].(map[string]any)
	if input["city"] != "Paris" {
		t.Fatalf("tool input = %#v", input)
	}
	if messages[2]["role"] != "user" {
		t.Fatalf("third message = %#v, want user tool result", messages[2])
	}
	userContent := messages[2]["content"].([]map[string]any)
	toolResult := userContent[0]["toolResult"].(map[string]any)
	if toolResult["toolUseId"] != "call_weather" || toolResult["status"] != "success" {
		t.Fatalf("toolResult = %#v", toolResult)
	}
	resultContent := toolResult["content"].([]map[string]any)
	if resultContent[0]["text"] != "sunny" {
		t.Fatalf("tool result content = %#v", resultContent)
	}
}

func TestChatContextToAWSProviderFormatIncludesInlineDataURLImage(t *testing.T) {
	imageData := base64.StdEncoding.EncodeToString([]byte("jpg-bytes"))
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{
			ID:   "user",
			Role: ChatRoleUser,
			Content: []ChatContent{{Image: &ImageContent{
				Image: "data:image/jpeg;base64," + imageData,
			}}},
		},
	}

	messages, _ := ctx.ToProviderFormat("aws")

	content := messages[0]["content"].([]map[string]any)
	imagePart, ok := content[0]["image"].(map[string]any)
	if !ok {
		t.Fatalf("aws image content = %#v, want image", content[0])
	}
	source := imagePart["source"].(map[string]any)
	if imagePart["format"] != "jpeg" || !reflect.DeepEqual(source["bytes"], []byte("jpg-bytes")) {
		t.Fatalf("aws image = %#v", imagePart)
	}
}

func TestChatContextToAWSProviderFormatRejectsExternalImageURL(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{
			ID:   "user",
			Role: ChatRoleUser,
			Content: []ChatContent{{Image: &ImageContent{
				Image: "https://example.com/image.jpg",
			}}},
		},
	}

	messages, extra, err := ctx.ToProviderFormatE("aws")

	if err == nil {
		t.Fatal("ToProviderFormatE(aws) error = nil, want external image URL error")
	}
	if messages != nil || extra != nil {
		t.Fatalf("ToProviderFormatE(aws) messages=%#v extra=%#v, want nil outputs on error", messages, extra)
	}
	if got, want := err.Error(), "external_url is not supported by AWS Bedrock."; got != want {
		t.Fatalf("ToProviderFormatE(aws) error = %q, want %q", got, want)
	}
	if strings.Contains(err.Error(), "external image URLs") {
		t.Fatalf("ToProviderFormatE(aws) error = %q, want reference external_url wording", err)
	}
}

func TestChatContextToMistralProviderFormatMapsEntriesAndInstructions(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{ID: "system", Role: ChatRoleSystem, Content: []ChatContent{{Text: "be concise"}}},
		&ChatMessage{
			ID:   "user",
			Role: ChatRoleUser,
			Content: []ChatContent{
				{Text: "describe"},
				{Image: &ImageContent{Image: "https://example.test/image.png"}},
			},
		},
		&ChatMessage{ID: "assistant-turn", Role: ChatRoleAssistant, Content: []ChatContent{{Text: "checking"}}},
		&FunctionCall{ID: "assistant-turn/tool", CallID: "call_weather", Name: "weather", Arguments: `{"city":"Paris"}`},
		&FunctionCallOutput{ID: "weather-output", CallID: "call_weather", Name: "weather", Output: "sunny"},
	}

	entries, extra := ctx.ToProviderFormat("mistralai")

	data, ok := extra.(map[string]any)
	if !ok {
		t.Fatalf("mistral extra = %#v, want map", extra)
	}
	if data["instructions"] != "be concise" {
		t.Fatalf("instructions = %#v", data["instructions"])
	}
	if len(entries) != 4 {
		t.Fatalf("len(entries) = %d, want 4: %#v", len(entries), entries)
	}
	if entries[0]["type"] != "message.input" || entries[0]["role"] != "user" {
		t.Fatalf("first entry = %#v", entries[0])
	}
	content := entries[0]["content"].([]map[string]any)
	if content[0]["type"] != "image_url" || content[0]["image_url"] != "https://example.test/image.png" {
		t.Fatalf("image content = %#v", content[0])
	}
	if content[1]["type"] != "text" || content[1]["text"] != "describe" {
		t.Fatalf("text content = %#v", content[1])
	}
	if entries[1]["type"] != "message.output" || entries[1]["role"] != "assistant" || entries[1]["content"] != "checking" {
		t.Fatalf("assistant entry = %#v", entries[1])
	}
	if entries[2]["type"] != "function.call" || entries[2]["tool_call_id"] != "call_weather" || entries[2]["name"] != "weather" || entries[2]["arguments"] != `{"city":"Paris"}` {
		t.Fatalf("function call entry = %#v", entries[2])
	}
	if entries[3]["type"] != "function.result" || entries[3]["tool_call_id"] != "call_weather" || entries[3]["result"] != "sunny" {
		t.Fatalf("function result entry = %#v", entries[3])
	}
}

func TestChatContextToMistralProviderFormatInstructionsUseTextPartsOnly(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{
			ID:   "system",
			Role: ChatRoleSystem,
			Content: []ChatContent{
				{Instructions: NewInstructions("voice instructions", "text instructions")},
				{Text: "plain text"},
			},
		},
	}

	_, extra := ctx.ToProviderFormat("mistralai")

	data, ok := extra.(map[string]any)
	if !ok {
		t.Fatalf("mistral extra = %#v, want map", extra)
	}
	if data["instructions"] != "plain text" {
		t.Fatalf("instructions = %#v, want plain text", data["instructions"])
	}
}

func TestChatContextToMistralProviderFormatReturnsImageSerializationError(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{
			ID:   "user",
			Role: ChatRoleUser,
			Content: []ChatContent{
				{Image: &ImageContent{Image: "data:image/png;base64,not-valid-base64"}},
			},
		},
	}

	entries, extra, err := ctx.ToProviderFormatE("mistralai")

	if err == nil {
		t.Fatal("ToProviderFormatE(mistralai) error = nil, want image serialization error")
	}
	if entries != nil || extra != nil {
		t.Fatalf("ToProviderFormatE(mistralai) entries=%#v extra=%#v, want nil outputs on error", entries, extra)
	}
	if !strings.Contains(err.Error(), "decode data URL image") {
		t.Fatalf("ToProviderFormatE(mistralai) error = %q, want decode data URL image error", err)
	}
}

func TestChatContextSummarizeCompactsOlderHistoryAndPreservesTail(t *testing.T) {
	ctx := NewChatContext()
	ctx.Items = []ChatItem{
		&ChatMessage{ID: "system", Role: ChatRoleSystem, Content: []ChatContent{{Text: "base instructions"}}, CreatedAt: time.Unix(1, 0)},
		&ChatMessage{ID: "old-user", Role: ChatRoleUser, Content: []ChatContent{{Text: "old question"}}, CreatedAt: time.Unix(2, 0)},
		&ChatMessage{ID: "old-assistant", Role: ChatRoleAssistant, Content: []ChatContent{{Text: "old answer"}}, CreatedAt: time.Unix(3, 0)},
		&FunctionCall{ID: "old-assistant/tool", CallID: "call_lookup", Name: "lookup", Arguments: `{"q":"old"}`, CreatedAt: time.Unix(4, 0)},
		&FunctionCallOutput{ID: "lookup-output", CallID: "call_lookup", Name: "lookup", Output: "old fact", CreatedAt: time.Unix(5, 0)},
		&ChatMessage{ID: "tail-user", Role: ChatRoleUser, Content: []ChatContent{{Text: "recent question"}}, CreatedAt: time.Unix(6, 0)},
		&ChatMessage{ID: "tail-assistant", Role: ChatRoleAssistant, Content: []ChatContent{{Text: "recent answer"}}, CreatedAt: time.Unix(7, 0)},
	}
	llm := &summaryTestLLM{response: "summary text"}

	result, err := ctx.Summarize(context.Background(), llm, ChatContextSummarizeOptions{KeepLastTurns: 1})
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if result != ctx {
		t.Fatalf("Summarize() = %p, want receiver %p", result, ctx)
	}
	if len(ctx.Items) != 4 {
		t.Fatalf("len(items) = %d, want 4: %q", len(ctx.Items), itemIDs(ctx.Items))
	}
	if ctx.Items[0].GetID() != "system" || ctx.Items[2].GetID() != "tail-user" || ctx.Items[3].GetID() != "tail-assistant" {
		t.Fatalf("items = %q, want system, summary, tail-user, tail-assistant", itemIDs(ctx.Items))
	}
	summary, ok := ctx.Items[1].(*ChatMessage)
	if !ok {
		t.Fatalf("summary item = %T, want *ChatMessage", ctx.Items[1])
	}
	if summary.Role != ChatRoleAssistant || summary.Extra["is_summary"] != true {
		t.Fatalf("summary fields = %#v", summary)
	}
	if summary.TextContent() != "<chat_history_summary>\nsummary text\n</chat_history_summary>" {
		t.Fatalf("summary text = %q", summary.TextContent())
	}
	if !summary.CreatedAt.Before(ctx.Items[2].GetCreatedAt()) {
		t.Fatalf("summary CreatedAt = %v, want before tail %v", summary.CreatedAt, ctx.Items[2].GetCreatedAt())
	}
	if len(llm.requests) != 1 {
		t.Fatalf("llm requests = %d, want 1", len(llm.requests))
	}
	prompt := llm.requests[0].Messages()[1].TextContent()
	if !strings.Contains(prompt, "<user>\nold question\n</user>") ||
		!strings.Contains(prompt, "<assistant>\nold answer\n</assistant>") ||
		!strings.Contains(prompt, "<function_call") ||
		!strings.Contains(prompt, "old fact") ||
		strings.Contains(prompt, "recent question") {
		t.Fatalf("summary prompt = %q", prompt)
	}
}

type summaryTestLLM struct {
	response string
	requests []*ChatContext
}

func (f *summaryTestLLM) Chat(_ context.Context, chatCtx *ChatContext, _ ...ChatOption) (LLMStream, error) {
	f.requests = append(f.requests, chatCtx)
	return &summaryTestStream{chunks: []*ChatChunk{{Delta: &ChoiceDelta{Content: f.response}}}}, nil
}

type summaryTestStream struct {
	chunks []*ChatChunk
	index  int
}

func (s *summaryTestStream) Next() (*ChatChunk, error) {
	if s.index >= len(s.chunks) {
		return nil, io.EOF
	}
	chunk := s.chunks[s.index]
	s.index++
	return chunk, nil
}

func (s *summaryTestStream) Close() error { return nil }
