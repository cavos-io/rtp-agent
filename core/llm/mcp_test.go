package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMCPProxyToolReturnsVisibleToolErrorContent(t *testing.T) {
	server := NewMCPServerStdio("", nil)
	server.stdin = &fakeMCPWriteCloser{
		server: server,
		result: json.RawMessage(`{
			"content": [
				{"type": "text", "text": "bad input"},
				{"type": "text", "text": "try again"}
			],
			"isError": true
		}`),
	}
	tool := &mcpProxyTool{server: server, name: "lookup"}

	_, err := tool.Execute(context.Background(), `{"city":"Paris"}`)

	var toolErr ToolError
	if !errors.As(err, &toolErr) {
		t.Fatalf("error = %T %v, want ToolError", err, err)
	}
	if toolErr.Message != "bad input\ntry again" {
		t.Fatalf("ToolError message = %q, want visible MCP error content", toolErr.Message)
	}
}

func TestMCPProxyToolTreatsEmptyResultAsToolError(t *testing.T) {
	server := NewMCPServerStdio("", nil)
	server.stdin = &fakeMCPWriteCloser{
		server: server,
		result: json.RawMessage(`{"content": [], "isError": false}`),
	}
	tool := &mcpProxyTool{server: server, name: "lookup"}

	_, err := tool.Execute(context.Background(), `{}`)

	var toolErr ToolError
	if !errors.As(err, &toolErr) {
		t.Fatalf("error = %T %v, want ToolError", err, err)
	}
	want := "Tool 'lookup' completed without producing a result. This might indicate an issue with internal processing."
	if toolErr.Message != want {
		t.Fatalf("ToolError message = %q, want %q", toolErr.Message, want)
	}
}

func TestMCPProxyToolReportsUnavailableServer(t *testing.T) {
	server := NewMCPServerStdio("", nil)
	tool := &mcpProxyTool{server: server, name: "lookup"}

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("Execute() panicked for unavailable MCP server: %v", recovered)
		}
	}()

	_, err := tool.Execute(context.Background(), `{}`)

	var toolErr ToolError
	if !errors.As(err, &toolErr) {
		t.Fatalf("error = %T %v, want ToolError", err, err)
	}
	if toolErr.Message != "Tool invocation failed: internal service is unavailable. Please check that the MCPServer is still running." {
		t.Fatalf("ToolError message = %q, want unavailable service explanation", toolErr.Message)
	}
}

func TestMCPServerHTTPListsAndExecutesTools(t *testing.T) {
	var sawInitialize bool
	var sawToolCall bool
	httpClient := newMCPTestHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("Authorization header = %q, want bearer token", got)
		}
		var req jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		switch req.Method {
		case "initialize":
			sawInitialize = true
			writeMCPHTTPResponse(t, w, req.ID, map[string]any{"protocolVersion": "2024-11-05"})
		case "initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeMCPHTTPResponse(t, w, req.ID, map[string]any{
				"tools": []map[string]any{
					{"name": "lookup", "description": "lookup tool", "inputSchema": map[string]any{"type": "object"}},
					{"name": "ignored", "description": "ignored tool", "inputSchema": map[string]any{"type": "object"}},
				},
			})
		case "tools/call":
			sawToolCall = true
			writeMCPHTTPResponse(t, w, req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": "Paris"}},
				"isError": false,
			})
		default:
			t.Fatalf("unexpected MCP method %q", req.Method)
		}
	}))

	server := NewMCPServerHTTP("https://mcp.test/rpc")
	server.client = httpClient
	server.TransportType = "streamable_http"
	server.AllowedTools = []string{"lookup"}
	server.Headers = map[string]string{"Authorization": "Bearer token"}

	if server.URL != "https://mcp.test/rpc" {
		t.Fatalf("URL = %q, want constructor URL", server.URL)
	}
	if err := server.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	tools, err := server.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools) != 1 || tools[0].Name() != "lookup" {
		t.Fatalf("tools = %#v, want only allowed lookup tool", tools)
	}
	output, err := tools[0].Execute(context.Background(), `{"city":"Paris"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output != `{"text":"Paris","type":"text"}` {
		t.Fatalf("Execute() output = %q, want serialized MCP content", output)
	}
	if !sawInitialize || !sawToolCall {
		t.Fatalf("sawInitialize=%v sawToolCall=%v, want both", sawInitialize, sawToolCall)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestMCPServerHTTPDetectsReferenceTransportFromURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "streamable http mcp path", url: "https://mcp.test/mcp", want: "streamable_http"},
		{name: "streamable http upper path trailing slash", url: "https://mcp.test/API/MCP/", want: "streamable_http"},
		{name: "sse path", url: "https://mcp.test/sse", want: "sse"},
		{name: "backward compatible default", url: "https://mcp.test/rpc", want: "sse"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := NewMCPServerHTTP(tt.url)

			if server.TransportType != tt.want {
				t.Fatalf("TransportType = %q, want %q", server.TransportType, tt.want)
			}
		})
	}
}

func TestMCPServerHTTPInitializedReflectsLifecycle(t *testing.T) {
	httpClient := newMCPTestHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		switch req.Method {
		case "initialize":
			writeMCPHTTPResponse(t, w, req.ID, map[string]any{"protocolVersion": "2024-11-05"})
		case "initialized":
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Fatalf("unexpected MCP method %q", req.Method)
		}
	}))

	server := NewMCPServerHTTP("https://mcp.test/rpc")
	server.client = httpClient

	if server.Initialized() {
		t.Fatal("Initialized() = true before Initialize, want false")
	}
	if err := server.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if !server.Initialized() {
		t.Fatal("Initialized() = false after Initialize, want true")
	}
	if err := server.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if server.Initialized() {
		t.Fatal("Initialized() = true after Close, want false")
	}
}

func TestMCPServerHTTPSetHeadersAppliesToSubsequentRequests(t *testing.T) {
	httpClient := newMCPTestHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		switch req.Method {
		case "initialize":
			if got := r.Header.Get("Authorization"); got != "Bearer first" {
				t.Fatalf("initialize Authorization = %q, want first token", got)
			}
			writeMCPHTTPResponse(t, w, req.ID, map[string]any{"protocolVersion": "2024-11-05"})
		case "initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if got := r.Header.Get("Authorization"); got != "Bearer second" {
				t.Fatalf("tools/list Authorization = %q, want updated token", got)
			}
			if got := r.Header.Get("X-Scope"); got != "tools" {
				t.Fatalf("tools/list X-Scope = %q, want updated scope", got)
			}
			writeMCPHTTPResponse(t, w, req.ID, map[string]any{
				"tools": []map[string]any{},
			})
		default:
			t.Fatalf("unexpected MCP method %q", req.Method)
		}
	}))

	server := NewMCPServerHTTP("https://mcp.test/rpc")
	server.client = httpClient
	server.Headers = map[string]string{"Authorization": "Bearer first"}

	if err := server.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	server.SetHeaders(map[string]string{
		"Authorization": "Bearer second",
		"X-Scope":       "tools",
	})
	if _, err := server.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
}

func TestMCPServersRejectListToolsBeforeInitialize(t *testing.T) {
	tests := []struct {
		name   string
		server MCPServer
	}{
		{name: "http", server: NewMCPServerHTTP("https://mcp.test/mcp")},
		{name: "stdio", server: NewMCPServerStdio("mcp-server", nil)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.server.ListTools(context.Background())

			if err == nil {
				t.Fatal("ListTools() error = nil, want uninitialized MCPServer error")
			}
			if got, want := err.Error(), "MCPServer isn't initialized"; got != want {
				t.Fatalf("ListTools() error = %q, want %q", got, want)
			}
		})
	}
}

func newMCPTestHTTPClient(handler http.Handler) *http.Client {
	return &http.Client{
		Transport: mcpTestRoundTripper(func(req *http.Request) (*http.Response, error) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			resp := recorder.Result()
			if resp.Body == nil {
				resp.Body = io.NopCloser(strings.NewReader(""))
			}
			return resp, nil
		}),
	}
}

type mcpTestRoundTripper func(*http.Request) (*http.Response, error)

func (f mcpTestRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestMCPToolsetSetupInitializesServerAndFlattensTools(t *testing.T) {
	lookup := &testTool{id: "lookup", name: "lookup"}
	server := &fakeMCPServer{tools: []Tool{lookup}}
	toolset := NewMCPToolset("mcp-tools", server)

	got, err := toolset.Setup(context.Background(), false)
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	if got != toolset {
		t.Fatalf("Setup() = %p, want receiver %p", got, toolset)
	}
	if server.initializeCalls != 1 {
		t.Fatalf("Initialize calls = %d, want 1", server.initializeCalls)
	}
	if server.listToolsCalls != 1 {
		t.Fatalf("ListTools calls = %d, want 1", server.listToolsCalls)
	}

	ctx := NewToolContext([]interface{}{toolset})
	if got := ctx.GetFunctionTool("lookup"); got != lookup {
		t.Fatalf("GetFunctionTool(lookup) = %p, want MCP tool %p", got, lookup)
	}
	if toolsets := ctx.Toolsets(); len(toolsets) != 1 || toolsets[0] != toolset {
		t.Fatalf("Toolsets() = %#v, want MCP toolset", toolsets)
	}
}

func TestMCPToolsetSetupReloadInvalidatesServerCache(t *testing.T) {
	first := &testTool{id: "first", name: "first"}
	second := &testTool{id: "second", name: "second"}
	server := &fakeMCPServer{
		initialized: true,
		toolBatches: [][]Tool{
			{first},
			{second},
		},
	}
	toolset := NewMCPToolset("mcp-tools", server)

	if _, err := toolset.Setup(context.Background(), false); err != nil {
		t.Fatalf("first Setup() error = %v", err)
	}
	if _, err := toolset.Setup(context.Background(), false); err != nil {
		t.Fatalf("cached Setup() error = %v", err)
	}
	if server.listToolsCalls != 1 {
		t.Fatalf("ListTools calls = %d, want cached setup to avoid reload", server.listToolsCalls)
	}

	if _, err := toolset.Setup(context.Background(), true); err != nil {
		t.Fatalf("reload Setup() error = %v", err)
	}
	if server.invalidateCalls != 1 {
		t.Fatalf("InvalidateCache calls = %d, want 1 on reload", server.invalidateCalls)
	}
	if server.listToolsCalls != 2 {
		t.Fatalf("ListTools calls = %d, want reload to fetch tools again", server.listToolsCalls)
	}
	tools := toolset.Tools()
	if len(tools) != 1 || tools[0] != second {
		t.Fatalf("Tools() = %#v, want reloaded second tool", tools)
	}
}

func TestMCPToolsetFilterUpdatesToolsetState(t *testing.T) {
	keep := &testTool{id: "keep", name: "keep"}
	drop := &testTool{id: "drop", name: "drop"}
	server := &fakeMCPServer{tools: []Tool{keep, drop}}
	toolset := NewMCPToolset("mcp-tools", server)

	if _, err := toolset.Setup(context.Background(), false); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	toolset.FilterTools(func(tool Tool) bool {
		return tool.Name() == "keep"
	})
	if tools := toolset.Tools(); len(tools) != 1 || tools[0] != keep {
		t.Fatalf("filtered Tools() = %#v, want keep tool only", tools)
	}
}

func writeMCPHTTPResponse(t *testing.T, w http.ResponseWriter, id int64, result any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	}); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func TestMCPProxyToolSerializesSuccessfulContentResults(t *testing.T) {
	tests := []struct {
		name   string
		result json.RawMessage
		want   string
	}{
		{
			name:   "single item",
			result: json.RawMessage(`{"content":[{"type":"text","text":"Paris"}],"isError":false}`),
			want:   `{"type":"text","text":"Paris"}`,
		},
		{
			name: "multiple items",
			result: json.RawMessage(`{
				"content": [
					{"type": "text", "text": "Paris"},
					{"type": "resource", "uri": "file:///weather.txt"}
				],
				"isError": false
			}`),
			want: `[{"type":"text","text":"Paris"},{"type":"resource","uri":"file:///weather.txt"}]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := NewMCPServerStdio("", nil)
			server.stdin = &fakeMCPWriteCloser{
				server: server,
				result: tt.result,
			}
			tool := &mcpProxyTool{server: server, name: "lookup"}

			got, err := tool.Execute(context.Background(), `{}`)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("Execute() output = %q, want serialized MCP content %q", got, tt.want)
			}
		})
	}
}

func TestMCPServerStdioCachesListedToolsUntilInvalidated(t *testing.T) {
	server := NewMCPServerStdio("", nil)
	writer := &fakeMCPWriteCloser{
		server: server,
		results: []json.RawMessage{
			json.RawMessage(`{"tools":[{"name":"first","description":"first tool","inputSchema":{"type":"object"}}]}`),
			json.RawMessage(`{"tools":[{"name":"second","description":"second tool","inputSchema":{"type":"object"}}]}`),
		},
	}
	server.stdin = writer

	tools, err := server.ListTools(context.Background())
	if err != nil {
		t.Fatalf("first ListTools() error = %v", err)
	}
	cachedTools, err := server.ListTools(context.Background())
	if err != nil {
		t.Fatalf("cached ListTools() error = %v", err)
	}

	if writer.writes != 1 {
		t.Fatalf("tools/list calls = %d, want 1 before invalidation", writer.writes)
	}
	if len(tools) != 1 || tools[0].Name() != "first" {
		t.Fatalf("tools = %#v, want first tool", tools)
	}
	if len(cachedTools) != 1 || cachedTools[0].Name() != "first" {
		t.Fatalf("cached tools = %#v, want cached first tool", cachedTools)
	}

	server.InvalidateCache()
	reloadedTools, err := server.ListTools(context.Background())
	if err != nil {
		t.Fatalf("reloaded ListTools() error = %v", err)
	}

	if writer.writes != 2 {
		t.Fatalf("tools/list calls = %d, want 2 after invalidation", writer.writes)
	}
	if len(reloadedTools) != 1 || reloadedTools[0].Name() != "second" {
		t.Fatalf("reloaded tools = %#v, want second tool", reloadedTools)
	}
}

func TestMCPServerStdioPreservesToolMetaInFunctionSchema(t *testing.T) {
	server := NewMCPServerStdio("", nil)
	server.stdin = &fakeMCPWriteCloser{
		server: server,
		result: json.RawMessage(`{
			"tools": [{
				"name": "lookup",
				"description": "lookup tool",
				"inputSchema": {"type": "object"},
				"meta": {"title": "Lookup", "readOnlyHint": true}
			}]
		}`),
	}

	tools, err := server.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(tools))
	}

	proxy, ok := tools[0].(*mcpProxyTool)
	if !ok {
		t.Fatalf("tool type = %T, want *mcpProxyTool", tools[0])
	}
	schema, err := proxy.ParseFunctionTools("")
	if err != nil {
		t.Fatalf("ParseFunctionTools() error = %v", err)
	}
	meta, ok := schema["meta"].(map[string]interface{})
	if !ok {
		t.Fatalf("schema meta = %#v, want MCP meta object", schema["meta"])
	}
	if meta["title"] != "Lookup" || meta["readOnlyHint"] != true {
		t.Fatalf("schema meta = %#v, want MCP meta fields", meta)
	}
}

func TestMCPServerStdioInitializePassesEnvAndCwd(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "mcp-env-cwd-test.sh")
	script := `#!/bin/sh
if [ "$MCP_TEST_TOKEN" != "expected-token" ]; then
  exit 2
fi
if [ "$(pwd)" != "$MCP_TEST_CWD" ]; then
  exit 3
fi
while IFS= read -r line; do
  case "$line" in
    *'"id":1'*)
      printf '{"jsonrpc":"2.0","id":1,"result":{}}\n'
      ;;
  esac
done
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write helper script: %v", err)
	}

	server := NewMCPServerStdio("sh", []string{scriptPath})
	server.Cwd = tmpDir
	server.Env = map[string]string{
		"MCP_TEST_TOKEN": "expected-token",
		"MCP_TEST_CWD":   tmpDir,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	defer server.Close()

	if err := server.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
}

type fakeMCPWriteCloser struct {
	server  *MCPServerStdio
	result  json.RawMessage
	results []json.RawMessage
	writes  int
}

func (w *fakeMCPWriteCloser) Write(p []byte) (int, error) {
	var req jsonRPCRequest
	if err := json.Unmarshal(p, &req); err != nil {
		return 0, err
	}

	w.server.mu.Lock()
	ch := w.server.pending[req.ID]
	w.server.mu.Unlock()
	result := w.result
	if len(w.results) > 0 {
		idx := w.writes
		if idx >= len(w.results) {
			idx = len(w.results) - 1
		}
		result = w.results[idx]
	}
	w.writes++
	ch <- &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  result,
	}

	return len(p), nil
}

func (w *fakeMCPWriteCloser) Close() error {
	return nil
}

type fakeMCPServer struct {
	initialized     bool
	tools           []Tool
	toolBatches     [][]Tool
	initializeCalls int
	invalidateCalls int
	listToolsCalls  int
	closeCalls      int
}

func (s *fakeMCPServer) Initialize(context.Context) error {
	s.initializeCalls++
	s.initialized = true
	return nil
}

func (s *fakeMCPServer) Initialized() bool {
	return s.initialized
}

func (s *fakeMCPServer) InvalidateCache() {
	s.invalidateCalls++
}

func (s *fakeMCPServer) ListTools(context.Context) ([]Tool, error) {
	s.listToolsCalls++
	if len(s.toolBatches) > 0 {
		idx := s.listToolsCalls - 1
		if idx >= len(s.toolBatches) {
			idx = len(s.toolBatches) - 1
		}
		return s.toolBatches[idx], nil
	}
	return s.tools, nil
}

func (s *fakeMCPServer) Close() error {
	s.closeCalls++
	s.initialized = false
	return nil
}
