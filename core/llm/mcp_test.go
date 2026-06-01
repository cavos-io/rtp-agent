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

type fakeMCPWriteCloser struct {
	server *MCPServerStdio
	result json.RawMessage
}

func (w *fakeMCPWriteCloser) Write(p []byte) (int, error) {
	var req jsonRPCRequest
	if err := json.Unmarshal(p, &req); err != nil {
		return 0, err
	}

	w.server.mu.Lock()
	ch := w.server.pending[req.ID]
	w.server.mu.Unlock()
	ch <- &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  w.result,
	}

	return len(p), nil
}

func (w *fakeMCPWriteCloser) Close() error {
	return nil
}
