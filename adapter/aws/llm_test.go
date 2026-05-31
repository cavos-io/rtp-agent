package aws

import (
	"context"
	"testing"

	awstypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/cavos-io/conversation-worker/core/llm"
)

type awsRequestTestTool struct{}

func (awsRequestTestTool) ID() string          { return "lookup" }
func (awsRequestTestTool) Name() string        { return "lookup" }
func (awsRequestTestTool) Description() string { return "look up information" }
func (awsRequestTestTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
		},
		"required": []string{"query"},
	}
}
func (awsRequestTestTool) Execute(context.Context, string) (string, error) {
	return "", nil
}

func TestBuildAWSMessagesGroupsToolCallsWithOutputs(t *testing.T) {
	ctx := llm.NewChatContext()
	groupID := "assistant-turn"
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: groupID, Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "checking"}}},
		&llm.FunctionCall{ID: groupID + "/tool-1", CallID: "call_lookup", Name: "lookup", Arguments: `{"city":"Paris"}`},
		&llm.FunctionCall{ID: groupID + "/tool-2", CallID: "call_weather", Name: "weather", Arguments: `{"city":"Paris"}`},
		&llm.FunctionCallOutput{ID: "lookup-output", CallID: "call_lookup", Name: "lookup", Output: "Paris"},
		&llm.FunctionCallOutput{ID: "weather-output", CallID: "call_weather", Name: "weather", Output: "sunny"},
	}

	messages, systemText := buildAWSMessages(ctx)

	if systemText != "" {
		t.Fatalf("systemText = %q, want empty", systemText)
	}
	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want 3: %#v", len(messages), messages)
	}
	if messages[0].Role != awstypes.ConversationRoleUser {
		t.Fatalf("first role = %q, want user", messages[0].Role)
	}
	assertTextBlock(t, messages[0].Content, 0, "(empty)")
	if messages[1].Role != awstypes.ConversationRoleAssistant {
		t.Fatalf("second role = %q, want assistant", messages[1].Role)
	}
	assertTextBlock(t, messages[1].Content, 0, "checking")
	assertToolUseBlock(t, messages[1].Content, 1, "call_lookup", "lookup")
	assertToolUseBlock(t, messages[1].Content, 2, "call_weather", "weather")
	if messages[2].Role != awstypes.ConversationRoleUser {
		t.Fatalf("third role = %q, want user", messages[2].Role)
	}
	assertToolResultBlock(t, messages[2].Content, 0, "call_lookup", awstypes.ToolResultStatusSuccess)
	assertToolResultBlock(t, messages[2].Content, 1, "call_weather", awstypes.ToolResultStatusSuccess)
}

func TestBuildAWSMessagesFiltersUnmatchedToolItems(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
		&llm.FunctionCall{ID: "orphan-call", CallID: "call_missing_output", Name: "lookup", Arguments: `{}`},
		&llm.FunctionCallOutput{ID: "orphan-output", CallID: "call_missing_call", Name: "lookup", Output: "ignored"},
	}

	messages, _ := buildAWSMessages(ctx)

	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1: %#v", len(messages), messages)
	}
	if messages[0].Role != awstypes.ConversationRoleUser {
		t.Fatalf("role = %q, want user", messages[0].Role)
	}
	assertTextBlock(t, messages[0].Content, 0, "hello")
}

func TestBuildAWSToolConfigMapsNamedToolChoice(t *testing.T) {
	config := buildAWSToolConfig(&llm.ChatOptions{
		Tools: []llm.Tool{awsRequestTestTool{}},
		ToolChoice: map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": "lookup",
			},
		},
	})

	if config == nil {
		t.Fatalf("tool config is nil")
	}
	choice, ok := config.ToolChoice.(*awstypes.ToolChoiceMemberTool)
	if !ok {
		t.Fatalf("ToolChoice = %T, want tool member", config.ToolChoice)
	}
	if choice.Value.Name == nil || *choice.Value.Name != "lookup" {
		t.Fatalf("ToolChoice name = %#v, want lookup", choice.Value.Name)
	}
}

func TestBuildAWSToolConfigDropsToolsForNoneChoice(t *testing.T) {
	config := buildAWSToolConfig(&llm.ChatOptions{
		Tools:      []llm.Tool{awsRequestTestTool{}},
		ToolChoice: "none",
	})

	if config != nil {
		t.Fatalf("tool config = %#v, want nil", config)
	}
}

func TestBuildAWSMessagesCollectsSystemText(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "system", Role: llm.ChatRoleSystem, Content: []llm.ChatContent{{Text: "base"}}},
		&llm.ChatMessage{ID: "developer", Role: llm.ChatRoleDeveloper, Content: []llm.ChatContent{{Text: "dev"}}},
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}

	messages, systemText := buildAWSMessages(ctx)

	if systemText != "base\ndev\n" {
		t.Fatalf("systemText = %q, want base/dev", systemText)
	}
	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1", len(messages))
	}
	assertTextBlock(t, messages[0].Content, 0, "hello")
}

func assertTextBlock(t *testing.T, blocks []awstypes.ContentBlock, index int, want string) {
	t.Helper()
	if len(blocks) <= index {
		t.Fatalf("len(blocks) = %d, want index %d", len(blocks), index)
	}
	block, ok := blocks[index].(*awstypes.ContentBlockMemberText)
	if !ok {
		t.Fatalf("block[%d] = %T, want text", index, blocks[index])
	}
	if block.Value != want {
		t.Fatalf("text block[%d] = %q, want %q", index, block.Value, want)
	}
}

func assertToolUseBlock(t *testing.T, blocks []awstypes.ContentBlock, index int, wantID, wantName string) {
	t.Helper()
	if len(blocks) <= index {
		t.Fatalf("len(blocks) = %d, want index %d", len(blocks), index)
	}
	block, ok := blocks[index].(*awstypes.ContentBlockMemberToolUse)
	if !ok {
		t.Fatalf("block[%d] = %T, want tool use", index, blocks[index])
	}
	if block.Value.ToolUseId == nil || *block.Value.ToolUseId != wantID {
		t.Fatalf("tool use id = %#v, want %q", block.Value.ToolUseId, wantID)
	}
	if block.Value.Name == nil || *block.Value.Name != wantName {
		t.Fatalf("tool use name = %#v, want %q", block.Value.Name, wantName)
	}
}

func assertToolResultBlock(t *testing.T, blocks []awstypes.ContentBlock, index int, wantID string, wantStatus awstypes.ToolResultStatus) {
	t.Helper()
	if len(blocks) <= index {
		t.Fatalf("len(blocks) = %d, want index %d", len(blocks), index)
	}
	block, ok := blocks[index].(*awstypes.ContentBlockMemberToolResult)
	if !ok {
		t.Fatalf("block[%d] = %T, want tool result", index, blocks[index])
	}
	if block.Value.ToolUseId == nil || *block.Value.ToolUseId != wantID {
		t.Fatalf("tool result id = %#v, want %q", block.Value.ToolUseId, wantID)
	}
	if block.Value.Status != wantStatus {
		t.Fatalf("tool result status = %q, want %q", block.Value.Status, wantStatus)
	}
}
