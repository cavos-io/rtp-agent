package llm

import (
	"encoding/json"
	"reflect"
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
						"transcript": "audio text"
					}
				],
				"interrupted": true,
				"transcript_confidence": 0.75,
				"extra": {"source": "test"},
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

func TestChatContextUnmarshalJSONRejectsUnknownItemType(t *testing.T) {
	data := []byte(`{"items":[{"id":"bad","type":"unknown"}]}`)

	var ctx ChatContext
	if err := json.Unmarshal(data, &ctx); err == nil {
		t.Fatal("UnmarshalJSON() error = nil, want error")
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
