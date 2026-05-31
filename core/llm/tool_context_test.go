package llm

import (
	"context"
	"strings"
	"testing"
)

type testTool struct {
	id   string
	name string
}

func (t *testTool) ID() string { return t.id }

func (t *testTool) Name() string { return t.name }

func (t *testTool) Description() string { return "" }

func (t *testTool) Parameters() map[string]any { return nil }

func (t *testTool) Execute(context.Context, string) (string, error) { return "", nil }

type testToolset struct {
	id    string
	tools []Tool
}

func (s *testToolset) ID() string { return s.id }

func (s *testToolset) Tools() []Tool { return s.tools }

type nonComparableTool struct {
	id     string
	name   string
	labels []string
}

func (t nonComparableTool) ID() string { return t.id }

func (t nonComparableTool) Name() string { return t.name }

func (t nonComparableTool) Description() string { return "" }

func (t nonComparableTool) Parameters() map[string]any { return nil }

func (t nonComparableTool) Execute(context.Context, string) (string, error) { return "", nil }

func TestToolContextUpdateToolsAllowsSameToolInstanceDuplicate(t *testing.T) {
	tool := &testTool{id: "lookup", name: "lookup"}
	toolset := &testToolset{id: "tools", tools: []Tool{tool}}
	ctx := EmptyToolContext()

	if err := ctx.UpdateTools([]interface{}{tool, toolset}); err != nil {
		t.Fatalf("UpdateTools() error = %v", err)
	}

	functionTools := ctx.FunctionTools()
	if len(functionTools) != 1 {
		t.Fatalf("len(FunctionTools()) = %d, want 1", len(functionTools))
	}
	if got := ctx.GetFunctionTool("lookup"); got != tool {
		t.Fatalf("GetFunctionTool() = %p, want %p", got, tool)
	}
}

func TestToolContextUpdateToolsRejectsDifferentToolsWithSameName(t *testing.T) {
	ctx := EmptyToolContext()

	err := ctx.UpdateTools([]interface{}{
		&testTool{id: "lookup-a", name: "lookup"},
		&testTool{id: "lookup-b", name: "lookup"},
	})

	if err == nil {
		t.Fatal("UpdateTools() error = nil, want duplicate function name error")
	}
	if !strings.Contains(err.Error(), "duplicate function name: lookup") {
		t.Fatalf("UpdateTools() error = %q, want duplicate function name", err)
	}
}

func TestToolContextUpdateToolsRejectsNonComparableDuplicateName(t *testing.T) {
	ctx := EmptyToolContext()

	err := ctx.UpdateTools([]interface{}{
		nonComparableTool{id: "lookup-a", name: "lookup", labels: []string{"a"}},
		nonComparableTool{id: "lookup-b", name: "lookup", labels: []string{"b"}},
	})

	if err == nil {
		t.Fatal("UpdateTools() error = nil, want duplicate function name error")
	}
	if !strings.Contains(err.Error(), "duplicate function name: lookup") {
		t.Fatalf("UpdateTools() error = %q, want duplicate function name", err)
	}
}

func TestToolContextFlattenSortsFunctionToolsByName(t *testing.T) {
	ctx := EmptyToolContext()
	if err := ctx.UpdateTools([]interface{}{
		&testTool{id: "zeta", name: "zeta"},
		&testTool{id: "alpha", name: "alpha"},
		&testTool{id: "middle", name: "middle"},
	}); err != nil {
		t.Fatalf("UpdateTools() error = %v", err)
	}

	flattened := ctx.Flatten()
	names := make([]string, 0, len(flattened))
	for _, tool := range flattened {
		names = append(names, tool.Name())
	}

	want := []string{"alpha", "middle", "zeta"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("Flatten() names = %v, want %v", names, want)
	}
}
