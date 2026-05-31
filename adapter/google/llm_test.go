package google

import (
	"context"
	"testing"

	"github.com/cavos-io/conversation-worker/core/llm"
	"google.golang.org/genai"
)

type googleRequestTestTool struct{}

func (googleRequestTestTool) ID() string          { return "lookup" }
func (googleRequestTestTool) Name() string        { return "lookup" }
func (googleRequestTestTool) Description() string { return "look up information" }
func (googleRequestTestTool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
		},
		"required": []string{"query"},
	}
}
func (googleRequestTestTool) Execute(context.Context, string) (string, error) {
	return "", nil
}

func TestBuildGoogleContentsGroupsToolCallsWithResponses(t *testing.T) {
	ctx := llm.NewChatContext()
	groupID := "assistant-turn"
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: groupID, Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "checking"}}},
		&llm.FunctionCall{ID: groupID + "/tool-1", CallID: "call_lookup", Name: "lookup", Arguments: `{"city":"Paris"}`},
		&llm.FunctionCall{ID: groupID + "/tool-2", CallID: "call_weather", Name: "weather", Arguments: `{"city":"Paris"}`},
		&llm.FunctionCallOutput{ID: "lookup-output", CallID: "call_lookup", Name: "lookup", Output: "Paris"},
		&llm.FunctionCallOutput{ID: "weather-output", CallID: "call_weather", Name: "weather", Output: "sunny"},
	}

	contents, systemText := buildGoogleContents(ctx)

	if systemText != "" {
		t.Fatalf("systemText = %q, want empty", systemText)
	}
	if len(contents) != 2 {
		t.Fatalf("len(contents) = %d, want 2: %#v", len(contents), contents)
	}
	if contents[0].Role != genai.RoleModel {
		t.Fatalf("first role = %q, want model", contents[0].Role)
	}
	assertGoogleTextPart(t, contents[0].Parts, 0, "checking")
	assertGoogleFunctionCallPart(t, contents[0].Parts, 1, "call_lookup", "lookup")
	assertGoogleFunctionCallPart(t, contents[0].Parts, 2, "call_weather", "weather")
	if contents[1].Role != genai.RoleUser {
		t.Fatalf("second role = %q, want user", contents[1].Role)
	}
	assertGoogleFunctionResponsePart(t, contents[1].Parts, 0, "call_lookup", "lookup", "Paris")
	assertGoogleFunctionResponsePart(t, contents[1].Parts, 1, "call_weather", "weather", "sunny")
}

func TestBuildGoogleContentsFiltersUnmatchedToolItems(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
		&llm.FunctionCall{ID: "orphan-call", CallID: "call_missing_output", Name: "lookup", Arguments: `{}`},
		&llm.FunctionCallOutput{ID: "orphan-output", CallID: "call_missing_call", Name: "lookup", Output: "ignored"},
	}

	contents, _ := buildGoogleContents(ctx)

	if len(contents) != 1 {
		t.Fatalf("len(contents) = %d, want 1: %#v", len(contents), contents)
	}
	if contents[0].Role != genai.RoleUser {
		t.Fatalf("role = %q, want user", contents[0].Role)
	}
	assertGoogleTextPart(t, contents[0].Parts, 0, "hello")
}

func TestBuildGoogleContentsCollectsSystemText(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "system", Role: llm.ChatRoleSystem, Content: []llm.ChatContent{{Text: "base"}}},
		&llm.ChatMessage{ID: "developer", Role: llm.ChatRoleDeveloper, Content: []llm.ChatContent{{Text: "dev"}}},
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}

	contents, systemText := buildGoogleContents(ctx)

	if systemText != "base\ndev\n" {
		t.Fatalf("systemText = %q, want base/dev", systemText)
	}
	if len(contents) != 1 {
		t.Fatalf("len(contents) = %d, want 1", len(contents))
	}
	assertGoogleTextPart(t, contents[0].Parts, 0, "hello")
}

func TestBuildGoogleContentsInjectsDummyUserAfterModelTurn(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "assistant", Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "done"}}},
	}

	contents, _ := buildGoogleContents(ctx)

	if len(contents) != 2 {
		t.Fatalf("len(contents) = %d, want 2: %#v", len(contents), contents)
	}
	if contents[0].Role != genai.RoleModel {
		t.Fatalf("first role = %q, want model", contents[0].Role)
	}
	assertGoogleTextPart(t, contents[0].Parts, 0, "done")
	if contents[1].Role != genai.RoleUser {
		t.Fatalf("second role = %q, want dummy user", contents[1].Role)
	}
	assertGoogleTextPart(t, contents[1].Parts, 0, ".")
}

func TestBuildGoogleFunctionDeclarationKeepsStringRequiredFields(t *testing.T) {
	declaration := buildGoogleFunctionDeclaration(googleRequestTestTool{})

	if declaration.Name != "lookup" {
		t.Fatalf("Name = %q, want lookup", declaration.Name)
	}
	if declaration.Parameters == nil {
		t.Fatalf("Parameters is nil")
	}
	if len(declaration.Parameters.Required) != 1 || declaration.Parameters.Required[0] != "query" {
		t.Fatalf("Required = %#v, want query", declaration.Parameters.Required)
	}
	if declaration.Parameters.Properties["query"] == nil {
		t.Fatalf("query property missing: %#v", declaration.Parameters.Properties)
	}
}

func assertGoogleTextPart(t *testing.T, parts []*genai.Part, index int, want string) {
	t.Helper()
	if len(parts) <= index {
		t.Fatalf("len(parts) = %d, want index %d", len(parts), index)
	}
	if parts[index].Text != want {
		t.Fatalf("text part[%d] = %q, want %q", index, parts[index].Text, want)
	}
}

func assertGoogleFunctionCallPart(t *testing.T, parts []*genai.Part, index int, wantID, wantName string) {
	t.Helper()
	if len(parts) <= index {
		t.Fatalf("len(parts) = %d, want index %d", len(parts), index)
	}
	call := parts[index].FunctionCall
	if call == nil {
		t.Fatalf("part[%d] has nil FunctionCall", index)
	}
	if call.ID != wantID {
		t.Fatalf("function call id = %q, want %q", call.ID, wantID)
	}
	if call.Name != wantName {
		t.Fatalf("function call name = %q, want %q", call.Name, wantName)
	}
}

func assertGoogleFunctionResponsePart(t *testing.T, parts []*genai.Part, index int, wantID, wantName, wantOutput string) {
	t.Helper()
	if len(parts) <= index {
		t.Fatalf("len(parts) = %d, want index %d", len(parts), index)
	}
	response := parts[index].FunctionResponse
	if response == nil {
		t.Fatalf("part[%d] has nil FunctionResponse", index)
	}
	if response.ID != wantID {
		t.Fatalf("function response id = %q, want %q", response.ID, wantID)
	}
	if response.Name != wantName {
		t.Fatalf("function response name = %q, want %q", response.Name, wantName)
	}
	if response.Response["output"] != wantOutput {
		t.Fatalf("function response output = %#v, want %q", response.Response["output"], wantOutput)
	}
}
