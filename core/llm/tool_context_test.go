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

type closableTestToolset struct {
	testToolset
	closeCalls int
}

func (s *closableTestToolset) Close() error {
	s.closeCalls++
	return nil
}

type closableNestedTestToolset struct {
	testTool
	tools      []Tool
	closeCalls int
	onClose    func() error
}

func (s *closableNestedTestToolset) Tools() []Tool { return s.tools }

func (s *closableNestedTestToolset) Close() error {
	s.closeCalls++
	if s.onClose != nil {
		return s.onClose()
	}
	return nil
}

type nestedTestToolset struct {
	testTool
	tools []Tool
}

func (s *nestedTestToolset) Tools() []Tool { return s.tools }

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

func requireToolContextErrorString(t *testing.T, op string, err error, want string) {
	t.Helper()
	if op == "" {
		t.Fatal("operation label must be set")
	}
	if err == nil {
		t.Fatalf("%s error = nil, want %q", op, want)
	}
	if got := err.Error(); got != want {
		t.Fatalf("%s error = %q, want %q", op, got, want)
	}
}

func TestToolContextEmptyMatchesReferenceConstructor(t *testing.T) {
	var receiver ToolContext
	ctx := receiver.Empty()
	if ctx == nil {
		t.Fatal("ToolContext.Empty() = nil, want context")
	}
	if len(ctx.FunctionTools()) != 0 {
		t.Fatalf("len(ToolContext.Empty().FunctionTools()) = %d, want 0", len(ctx.FunctionTools()))
	}
	if len(ctx.ProviderTools()) != 0 {
		t.Fatalf("len(ToolContext.Empty().ProviderTools()) = %d, want 0", len(ctx.ProviderTools()))
	}
	if len(ctx.Toolsets()) != 0 {
		t.Fatalf("len(ToolContext.Empty().Toolsets()) = %d, want 0", len(ctx.Toolsets()))
	}

	tool := &testTool{id: "lookup", name: "lookup"}
	if err := ctx.AddTool(tool); err != nil {
		t.Fatalf("ToolContext.Empty().AddTool() error = %v, want nil", err)
	}
	if got := ctx.GetFunctionTool("lookup"); got != tool {
		t.Fatalf("ToolContext.Empty().GetFunctionTool() = %p, want %p", got, tool)
	}
}

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

	requireToolContextErrorString(t, "UpdateTools()", err, "duplicate function name: lookup")
}

func TestToolContextAddToolUpdatesFlattenedTools(t *testing.T) {
	lookup := &testTool{id: "lookup", name: "lookup"}
	provider := &testProviderTool{testTool: testTool{id: "provider", name: "provider"}}
	nestedProvider := &testProviderTool{testTool: testTool{id: "nested-provider", name: "nested-provider"}}
	weather := &testTool{id: "weather", name: "weather"}
	toolset := &testToolset{id: "set", tools: []Tool{weather, nestedProvider}}
	ctx := NewToolContext([]interface{}{lookup})

	if err := ctx.AddTool(provider); err != nil {
		t.Fatalf("AddTool(provider) error = %v", err)
	}
	if err := ctx.AddTool(toolset); err != nil {
		t.Fatalf("AddTool(toolset) error = %v", err)
	}

	if got := ctx.GetFunctionTool("lookup"); got != lookup {
		t.Fatalf("GetFunctionTool(lookup) = %p, want %p", got, lookup)
	}
	if got := ctx.GetFunctionTool("weather"); got != weather {
		t.Fatalf("GetFunctionTool(weather) = %p, want %p", got, weather)
	}
	providerTools := ctx.ProviderTools()
	if len(providerTools) != 2 || providerTools[0] != provider || providerTools[1] != nestedProvider {
		t.Fatalf("ProviderTools() = %#v, want added provider before nested provider", providerTools)
	}
	toolsets := ctx.Toolsets()
	if len(toolsets) != 1 || toolsets[0] != toolset {
		t.Fatalf("Toolsets() = %#v, want added toolset", toolsets)
	}
}

func TestToolContextAddToolRejectsDifferentToolWithSameName(t *testing.T) {
	ctx := NewToolContext([]interface{}{&testTool{id: "lookup-a", name: "lookup"}})

	err := ctx.AddTool(&testTool{id: "lookup-b", name: "lookup"})

	requireToolContextErrorString(t, "AddTool()", err, "duplicate function name: lookup")
}

func TestToolContextCloseClosesToolsets(t *testing.T) {
	lookup := &testTool{id: "lookup", name: "lookup"}
	toolset := &closableTestToolset{
		testToolset: testToolset{id: "tools", tools: []Tool{lookup}},
	}
	ctx := NewToolContext([]interface{}{toolset})

	if err := ctx.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if toolset.closeCalls != 1 {
		t.Fatalf("toolset Close calls = %d, want 1", toolset.closeCalls)
	}
}

func TestToolContextCloseClosesNestedToolsetsOnce(t *testing.T) {
	child := &closableNestedTestToolset{
		testTool: testTool{id: "child", name: "child"},
		tools:    []Tool{&testTool{id: "lookup", name: "lookup"}},
	}
	parent := &closableNestedTestToolset{
		testTool: testTool{id: "parent", name: "parent"},
		tools:    []Tool{child},
		onClose:  child.Close,
	}
	ctx := NewToolContext([]interface{}{parent})

	if err := ctx.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if parent.closeCalls != 1 {
		t.Fatalf("parent Close calls = %d, want 1", parent.closeCalls)
	}
	if child.closeCalls != 1 {
		t.Fatalf("child Close calls = %d, want 1 via parent close", child.closeCalls)
	}
}

func TestToolContextCloseRecursesIntoPlainNestedToolsets(t *testing.T) {
	child := &closableNestedTestToolset{
		testTool: testTool{id: "child", name: "child"},
		tools:    []Tool{&testTool{id: "lookup", name: "lookup"}},
	}
	parent := &nestedTestToolset{
		testTool: testTool{id: "parent", name: "parent"},
		tools:    []Tool{child},
	}
	ctx := NewToolContext([]interface{}{parent})

	if err := ctx.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if child.closeCalls != 1 {
		t.Fatalf("child Close calls = %d, want 1 through plain parent recursion", child.closeCalls)
	}
}

func TestToolContextSyncFlattenedPreservesToolsetLifecycle(t *testing.T) {
	lookup := &testTool{id: "lookup", name: "lookup"}
	weather := &testTool{id: "weather", name: "weather"}
	replacement := &testTool{id: "replacement", name: "replacement"}
	toolset := &closableTestToolset{
		testToolset: testToolset{id: "tools", tools: []Tool{lookup, weather}},
	}
	ctx := NewToolContext([]interface{}{toolset})

	if err := ctx.SyncFlattened([]Tool{weather, replacement}); err != nil {
		t.Fatalf("SyncFlattened() error = %v", err)
	}

	if got := ctx.GetFunctionTool("lookup"); got != nil {
		t.Fatalf("GetFunctionTool(lookup) = %p, want nil after flattened removal", got)
	}
	if got := ctx.GetFunctionTool("weather"); got != weather {
		t.Fatalf("GetFunctionTool(weather) = %p, want %p", got, weather)
	}
	if got := ctx.GetFunctionTool("replacement"); got != replacement {
		t.Fatalf("GetFunctionTool(replacement) = %p, want %p", got, replacement)
	}
	toolsets := ctx.Toolsets()
	if len(toolsets) != 1 || toolsets[0] != toolset {
		t.Fatalf("Toolsets() = %#v, want original toolset preserved", toolsets)
	}

	if err := ctx.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if toolset.closeCalls != 1 {
		t.Fatalf("toolset Close calls = %d, want preserved toolset closed once", toolset.closeCalls)
	}
}

func TestNewToolContextPanicsOnDuplicateFunctionName(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("NewToolContext() did not panic, want duplicate function name panic")
		} else if err, ok := r.(error); !ok {
			t.Fatalf("NewToolContext() panic = %T %v, want error", r, r)
		} else {
			requireToolContextErrorString(t, "NewToolContext() panic", err, "duplicate function name: lookup")
		}
	}()

	NewToolContext([]interface{}{
		&testTool{id: "lookup-a", name: "lookup"},
		&testTool{id: "lookup-b", name: "lookup"},
	})
}

func TestNewToolContextPanicsOnUnknownToolType(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("NewToolContext() did not panic, want unknown tool type panic")
		} else if err, ok := r.(error); !ok {
			t.Fatalf("NewToolContext() panic = %T %v, want error", r, r)
		} else {
			requireToolContextErrorString(t, "NewToolContext() panic", err, "unknown tool type: string")
		}
	}()

	NewToolContext([]interface{}{"not-a-tool"})
}

func TestToolContextUpdateToolsRejectsNonComparableDuplicateName(t *testing.T) {
	ctx := EmptyToolContext()

	err := ctx.UpdateTools([]interface{}{
		nonComparableTool{id: "lookup-a", name: "lookup", labels: []string{"a"}},
		nonComparableTool{id: "lookup-b", name: "lookup", labels: []string{"b"}},
	})

	requireToolContextErrorString(t, "UpdateTools()", err, "duplicate function name: lookup")
}

func TestToolContextFlattenPreservesFunctionToolInsertionOrder(t *testing.T) {
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
	if got := ctx.GetFunctionTool("alpha"); got == nil || got.Name() != "alpha" {
		t.Fatalf("GetFunctionTool(alpha) = %#v, want alpha tool after preserving insertion order", got)
	}

	want := []string{"zeta", "alpha", "middle"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("Flatten() names = %v, want %v", names, want)
	}
}

func TestToolContextSeparatesAndPreservesProviderToolOrder(t *testing.T) {
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
	if len(providerTools) != 2 || providerTools[0] != providerZ || providerTools[1] != providerA {
		t.Fatalf("ProviderTools() = %#v, want provider insertion order", providerTools)
	}
	providerTools[0] = providerA
	if copied := ctx.ProviderTools(); copied[0] != providerZ {
		t.Fatalf("ProviderTools() returned mutable backing slice; first provider = %#v, want zeta provider", copied[0])
	}

	flattened := ctx.Flatten()
	if len(flattened) != 3 || flattened[0] != function || flattened[1] != providerZ || flattened[2] != providerA {
		t.Fatalf("Flatten() = %#v, want function tools followed by provider insertion order", flattened)
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

func TestToolContextEqualHandlesNilReceiverLikeReference(t *testing.T) {
	var nilCtx *ToolContext
	empty := EmptyToolContext()

	if !nilCtx.Equal(nil) {
		t.Fatal("nil ToolContext Equal(nil) = false, want true for same absent context")
	}
	if nilCtx.Equal(empty) {
		t.Fatal("nil ToolContext Equal(empty) = true, want false like reference non-ToolContext comparison")
	}
	if empty.Equal(nilCtx) {
		t.Fatal("empty ToolContext Equal(nil) = true, want false")
	}
}
