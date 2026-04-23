package llm

import (
	"context"
	"encoding/json"
	"testing"
)

type mockMCPRequester struct {
	nextResp *jsonRPCResponse
	err      error
}

func (m *mockMCPRequester) sendRPC(ctx context.Context, method string, params interface{}) (*jsonRPCResponse, error) {
	return m.nextResp, m.err
}

func TestMCPProxyTool_Execute(t *testing.T) {
	mock := &mockMCPRequester{
		nextResp: &jsonRPCResponse{
			Result: json.RawMessage(`{"content": [{"type": "text", "text": "hello"}], "isError": false}`),
		},
	}
	
	tool := &mcpProxyTool{
		server:      mock,
		name:        "test_tool",
		description: "desc",
		parameters:  map[string]interface{}{"type": "object"},
	}
	
	res, err := tool.Execute(context.Background(), map[string]any{"arg": 1})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if res != "hello" {
		t.Errorf("Expected hello, got %v", res)
	}
}

func TestRPCManager(t *testing.T) {
	m := newRPCManager()
	id := m.nextID()
	if id != 1 {
		t.Errorf("Expected 1, got %d", id)
	}
	
	ch := m.registerPending(id)
	
	resp := &jsonRPCResponse{ID: id, JSONRPC: "2.0"}
	m.handleResponse(resp)
	
	got := <-ch
	if got != resp {
		t.Error("HandleResponse failed to deliver to pending channel")
	}
	
	m.unregisterPending(id)
}
