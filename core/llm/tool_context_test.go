package llm

import (
	"context"
	"reflect"
	"testing"
)

type mockTool struct {
	id   string
	name string
}

func (m *mockTool) ID() string                 { return m.id }
func (m *mockTool) Name() string               { return m.name }
func (m *mockTool) Description() string        { return "desc" }
func (m *mockTool) Parameters() map[string]any { return nil }
func (m *mockTool) Execute(ctx context.Context, args any) (any, error) {
	return "ok", nil
}

func TestToolContext_UpdateTools(t *testing.T) {
	t1 := &mockTool{id: "t1", name: "tool1"}
	t2 := &mockTool{id: "t2", name: "tool2"}

	ctx := NewToolContext([]interface{}{t1, t2})

	if len(ctx.functionTools) != 2 {
		t.Errorf("Expected 2 tools, got %d", len(ctx.functionTools))
	}

	err := ctx.UpdateTools([]interface{}{t1, t1}) // Duplicate
	if err == nil {
		t.Errorf("Expected error on duplicate tool name")
	}
}

func TestBuildJSONSchema(t *testing.T) {
	type TestArgs struct {
		Query string `json:"query" description:"The search query"`
		Count int    `json:"count"`
	}

	schema := BuildJSONSchema(reflect.TypeOf(TestArgs{}))
	
	if schema["type"] != "object" {
		t.Errorf("Expected type object, got %v", schema["type"])
	}

	properties := schema["properties"].(map[string]any)
	if _, ok := properties["query"]; !ok {
		t.Errorf("Missing query property")
	}
	
	query := properties["query"].(map[string]any)
	if query["type"] != "string" {
		t.Errorf("Expected string type for query, got %v", query["type"])
	}
	if query["description"] != "The search query" {
		t.Errorf("Incorrect description")
	}
}

func TestBuildFunctionTool(t *testing.T) {
	fn := func(ctx context.Context, args struct {
		Name string `json:"name"`
	}) (string, error) {
		return "Hello " + args.Name, nil
	}

	tool, err := BuildFunctionTool(fn, "hello", "Greet someone")
	if err != nil {
		t.Fatalf("BuildFunctionTool failed: %v", err)
	}

	if tool.Name() != "hello" {
		t.Errorf("Expected name 'hello', got %s", tool.Name())
	}

	res, err := tool.Execute(context.Background(), map[string]any{"name": "World"})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if res != "Hello World" {
		t.Errorf("Expected 'Hello World', got %v", res)
	}
}
