package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
)

type MCPServer interface {
	Initialize(ctx context.Context) error
	ListTools(ctx context.Context) ([]Tool, error)
	Close() error
}

type MCPServerHTTP struct {
	URL           string
	TransportType string
	AllowedTools  []string
	Headers       map[string]string
}

func NewMCPServerHTTP(url string) *MCPServerHTTP {
	return &MCPServerHTTP{
		URL: url,
	}
}

func (s *MCPServerHTTP) Initialize(ctx context.Context) error {
	// SSE/HTTP is complex, leaving as unsupported for now, focusing on Stdio
	return fmt.Errorf("HTTP MCP client not fully supported natively in Go yet")
}

func (s *MCPServerHTTP) ListTools(ctx context.Context) ([]Tool, error) {
	return nil, fmt.Errorf("HTTP MCP client not fully supported natively in Go yet")
}

func (s *MCPServerHTTP) Close() error {
	return nil
}

type MCPServerStdio struct {
	Command string
	Args    []string
	Env     map[string]string
	Cwd     string

	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	
	msgID   atomic.Int64
	pending map[int64]chan *jsonRPCResponse
	mu      sync.Mutex
}

func NewMCPServerStdio(command string, args []string) *MCPServerStdio {
	return &MCPServerStdio{
		Command: command,
		Args:    args,
		pending: make(map[int64]chan *jsonRPCResponse),
	}
}

type jsonRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *MCPServerStdio) Initialize(ctx context.Context) error {
	s.cmd = exec.CommandContext(ctx, s.Command, s.Args...)
	s.cmd.Dir = s.Cwd

	var err error
	if s.stdin, err = s.cmd.StdinPipe(); err != nil {
		return err
	}
	if s.stdout, err = s.cmd.StdoutPipe(); err != nil {
		return err
	}

	if err := s.cmd.Start(); err != nil {
		return err
	}

	go s.readLoop()

	// Send Initialize request
	params := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"clientInfo": map[string]interface{}{
			"name":    "conversation-worker",
			"version": "1.0.0",
		},
		"capabilities": map[string]interface{}{},
	}
	
	_, err = s.sendRequest(ctx, "initialize", params)
	if err != nil {
		return fmt.Errorf("initialize failed: %w", err)
	}

	// Send initialized notification
	_ = s.sendNotification("initialized", map[string]interface{}{})

	return nil
}

func (s *MCPServerStdio) ListTools(ctx context.Context) ([]Tool, error) {
	resp, err := s.sendRequest(ctx, "tools/list", map[string]interface{}{})
	if err != nil {
		return nil, fmt.Errorf("tools/list failed: %w", err)
	}

	var result struct {
		Tools []struct {
			Name        string                 `json:"name"`
			Description string                 `json:"description"`
			InputSchema map[string]interface{} `json:"inputSchema"`
		} `json:"tools"`
	}

	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to parse tools/list result: %w", err)
	}

	var tools []Tool
	for _, t := range result.Tools {
		tools = append(tools, &mcpProxyTool{
			server:      s,
			name:        t.Name,
			description: t.Description,
			parameters:  t.InputSchema,
		})
	}

	return tools, nil
}

func (s *MCPServerStdio) Close() error {
	if s.stdin != nil {
		s.stdin.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		return s.cmd.Process.Kill()
	}
	return nil
}

func (s *MCPServerStdio) sendRequest(ctx context.Context, method string, params interface{}) (*jsonRPCResponse, error) {
	id := s.msgID.Add(1)
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	b = append(b, '\n')

	ch := make(chan *jsonRPCResponse, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}()

	if _, err := s.stdin.Write(b); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp, nil
	}
}

func (s *MCPServerStdio) sendNotification(method string, params interface{}) error {
	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = s.stdin.Write(b)
	return err
}

func (s *MCPServerStdio) readLoop() {
	scanner := bufio.NewScanner(s.stdout)
	for scanner.Scan() {
		line := scanner.Bytes()
		var resp jsonRPCResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			continue // Ignore non-JSON or unparseable lines (e.g., raw stdout logs)
		}

		if resp.ID != 0 {
			s.mu.Lock()
			if ch, ok := s.pending[resp.ID]; ok {
				ch <- &resp
			}
			s.mu.Unlock()
		}
	}
}

type mcpProxyTool struct {
	server      *MCPServerStdio
	name        string
	description string
	parameters  map[string]interface{}
}

func (t *mcpProxyTool) ID() string          { return t.name }
func (t *mcpProxyTool) Name() string        { return t.name }
func (t *mcpProxyTool) Description() string { return t.description }
func (t *mcpProxyTool) Parameters() map[string]any {
	return t.parameters
}

func (t *mcpProxyTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	resp, err := t.server.sendRequest(ctx, "tools/call", map[string]interface{}{
		"name":      t.name,
		"arguments": args,
	})
	if err != nil {
		return "", err
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}

	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", err
	}

	if result.IsError {
		return "", fmt.Errorf("tool error")
	}

	if len(result.Content) > 0 {
		return result.Content[0].Text, nil
	}
	return "", nil
}

func (t *mcpProxyTool) ParseFunctionTools(format string) (map[string]interface{}, error) {
	return t.parameters, nil
}
