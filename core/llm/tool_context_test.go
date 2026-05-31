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

type testProviderTool struct {
	testTool
}

func (t *testProviderTool) IsProviderTool() bool { return true }

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

func TestToolContextSeparatesAndSortsProviderTools(t *testing.T) {
	providerZ := &testProviderTool{testTool: testTool{id: "zeta-provider", name: "zeta-provider"}}
	providerA := &testProviderTool{testTool: testTool{id: "alpha-provider", name: "alpha-provider"}}
	function := &testTool{id: "lookup", name: "lookup"}
	ctx := EmptyToolContext()

	if err := ctx.UpdateTools([]interface{}{providerZ, function, providerA}); err != nil {
		t.Fatalf("UpdateTools() error = %v", err)
	}

	if len(ctx.FunctionTools()) != 1 || ctx.GetFunctionTool("lookup") != function {
		t.Fatalf("FunctionTools() = %#v, want only lookup function tool", ctx.FunctionTools())
	}
	providerTools := ctx.ProviderTools()
	if len(providerTools) != 2 || providerTools[0] != providerA || providerTools[1] != providerZ {
		t.Fatalf("ProviderTools() = %#v, want sorted provider tools", providerTools)
	}

	flattened := ctx.Flatten()
	if len(flattened) != 3 || flattened[0] != function || flattened[1] != providerA || flattened[2] != providerZ {
		t.Fatalf("Flatten() = %#v, want function tools followed by sorted provider tools", flattened)
	}
}

func TestToolContextEqualUsesToolIdentity(t *testing.T) {
	lookup := &testTool{id: "lookup", name: "lookup"}
	provider := &testProviderTool{testTool: testTool{id: "provider", name: "provider"}}
	left := NewToolContext([]interface{}{lookup, provider})
	right := NewToolContext([]interface{}{provider, lookup})

	if !left.Equal(right) {
		t.Fatal("Equal() = false, want true for same tool identities")
	}

	otherLookup := &testTool{id: "lookup-other", name: "lookup"}
	other := NewToolContext([]interface{}{otherLookup, provider})
	if left.Equal(other) {
		t.Fatal("Equal() = true, want false for same function name backed by a different tool")
	}
}
