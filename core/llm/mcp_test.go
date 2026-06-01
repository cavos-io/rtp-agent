package llm

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
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
