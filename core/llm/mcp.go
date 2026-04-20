package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
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

	rpc          *rpcManager
	httpClient   *http.Client
	sseURL       string
	sseCancel    context.CancelFunc
}

func NewMCPServerHTTP(url string) *MCPServerHTTP {
	return &MCPServerHTTP{
		URL:        url,
		httpClient: &http.Client{},
		rpc:        newRPCManager(),
	}
}

func (s *MCPServerHTTP) Initialize(ctx context.Context) error {
	// Establish SSE connection
	req, err := http.NewRequestWithContext(ctx, "GET", s.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range s.Headers {
		req.Header.Set(k, v)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return fmt.Errorf("failed to connect to MCP SSE: %s", resp.Status)
	}

	// The first SSE event usually contains the "endpoint" for POST requests
	// but we'll wait for the "endpoint" event if the spec requires it.
	// For now assume the base URL is used if no endpoint event.
	
	sseCtx, cancel := context.WithCancel(context.Background())
	s.sseCancel = cancel

	go s.readSSELoop(sseCtx, resp.Body)

	// Send Initialize request (simulated over HTTP POST to the base URL)
	params := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"clientInfo": map[string]interface{}{
			"name":    "conversation-worker",
			"version": "1.0.0",
		},
		"capabilities": map[string]interface{}{},
	}
	
	_, err = s.sendRPC(ctx, "initialize", params)
	if err != nil {
		return fmt.Errorf("initialize failed: %w", err)
	}

	return nil
}

func (s *MCPServerHTTP) readSSELoop(ctx context.Context, body io.ReadCloser) {
	defer body.Close()
	reader := bufio.NewScanner(body)
	var currentEvent string

	for reader.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
			line := reader.Text()
			if line == "" {
				currentEvent = ""
				continue
			}
			if strings.HasPrefix(line, "event: ") {
				currentEvent = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") {
				data := strings.TrimPrefix(line, "data: ")
				if currentEvent == "endpoint" {
					s.sseURL = data
				} else if currentEvent == "message" || currentEvent == "" {
					var rpcResp jsonRPCResponse
					if err := json.Unmarshal([]byte(data), &rpcResp); err == nil && rpcResp.ID != 0 {
						s.rpc.handleResponse(&rpcResp)
					}
				}
			}
		}
	}
}

func (s *MCPServerHTTP) sendRPC(ctx context.Context, method string, params interface{}) (*jsonRPCResponse, error) {
	id := s.rpc.nextID()
	reqBody := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	
	b, _ := json.Marshal(reqBody)
	
	targetURL := s.URL
	if s.sseURL != "" {
		targetURL = s.sseURL
	}
	
	req, err := http.NewRequestWithContext(ctx, "POST", targetURL, strings.NewReader(string(b)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	
	ch := s.rpc.registerPending(id)
	defer s.rpc.unregisterPending(id)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rpc request failed: %s", resp.Status)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		return r, nil
	}
}

func (s *MCPServerHTTP) ListTools(ctx context.Context) ([]Tool, error) {
	resp, err := s.sendRPC(ctx, "tools/list", map[string]interface{}{})
	if err != nil {
		return nil, err
	}

	var result struct {
		Tools []struct {
			Name        string                 `json:"name"`
			Description string                 `json:"description"`
			InputSchema map[string]interface{} `json:"inputSchema"`
		} `json:"tools"`
	}

	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, err
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

func (s *MCPServerHTTP) Close() error {
	if s.sseCancel != nil {
		s.sseCancel()
	}
	return nil
}

type MCPServerStdio struct {
	Command string
	Args    []string
	Env     map[string]string
	Cwd     string

	rpc     *rpcManager
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
}

func NewMCPServerStdio(command string, args []string) *MCPServerStdio {
	return &MCPServerStdio{
		Command: command,
		Args:    args,
		rpc:     newRPCManager(),
	}
}

type rpcManager struct {
	msgID   atomic.Int64
	pending map[int64]chan *jsonRPCResponse
	mu      sync.Mutex
}

func newRPCManager() *rpcManager {
	return &rpcManager{
		pending: make(map[int64]chan *jsonRPCResponse),
	}
}

func (m *rpcManager) nextID() int64 {
	return m.msgID.Add(1)
}

func (m *rpcManager) registerPending(id int64) chan *jsonRPCResponse {
	ch := make(chan *jsonRPCResponse, 1)
	m.mu.Lock()
	m.pending[id] = ch
	m.mu.Unlock()
	return ch
}

func (m *rpcManager) unregisterPending(id int64) {
	m.mu.Lock()
	delete(m.pending, id)
	m.mu.Unlock()
}

func (m *rpcManager) handleResponse(resp *jsonRPCResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch, ok := m.pending[resp.ID]; ok {
		ch <- resp
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
	id := s.rpc.nextID()
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

	ch := s.rpc.registerPending(id)
	defer s.rpc.unregisterPending(id)

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
			s.rpc.handleResponse(&resp)
		}
	}
}

type mcpRequester interface {
	sendRPC(ctx context.Context, method string, params interface{}) (*jsonRPCResponse, error)
}

func (s *MCPServerStdio) sendRPC(ctx context.Context, method string, params interface{}) (*jsonRPCResponse, error) {
	return s.sendRequest(ctx, method, params)
}

type mcpProxyTool struct {
	server      mcpRequester
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

func (t *mcpProxyTool) Execute(ctx context.Context, args any) (any, error) {
	m, _ := args.(map[string]any)
	resp, err := t.server.sendRPC(ctx, "tools/call", map[string]interface{}{
		"name":      t.name,
		"arguments": m,
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

