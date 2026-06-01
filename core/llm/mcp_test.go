package llm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
	if toolErr.Message == "" {
		t.Fatal("ToolError message is empty, want explanation for empty MCP result")
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

func TestMCPServerHTTPReportsUnsupportedNativeClient(t *testing.T) {
	server := NewMCPServerHTTP("https://example.com/mcp")
	server.TransportType = "streamable_http"
	server.AllowedTools = []string{"lookup"}
	server.Headers = map[string]string{"Authorization": "Bearer token"}

	if server.URL != "https://example.com/mcp" {
		t.Fatalf("URL = %q, want constructor URL", server.URL)
	}
	if err := server.Initialize(context.Background()); err == nil {
		t.Fatal("Initialize() error = nil, want unsupported native client error")
	}
	if tools, err := server.ListTools(context.Background()); err == nil || tools != nil {
		t.Fatalf("ListTools() = %#v, %v; want nil tools and unsupported native client error", tools, err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
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
