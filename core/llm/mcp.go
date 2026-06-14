package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
)

type MCPServer interface {
	Initialize(ctx context.Context) error
	Initialized() bool
	InvalidateCache()
	ListTools(ctx context.Context) ([]Tool, error)
	Close() error
}

type mcpRequestSender interface {
	sendRequest(ctx context.Context, method string, params interface{}) (*jsonRPCResponse, error)
}

type mcpAvailability interface {
	Initialized() bool
}

const mcpMaxStdioLineBytes = 16 * 1024 * 1024

type MCPServerHTTP struct {
	URL           string
	TransportType string
	AllowedTools  []string
	Headers       map[string]string

	client      *http.Client
	msgID       atomic.Int64
	initialized bool
	cacheDirty  bool
	toolsCache  []Tool
	initState   *mcpInitializeState
	closeSeq    uint64
	mu          sync.Mutex
}

type mcpInitializeState struct {
	done chan struct{}
	err  error
}

func NewMCPServerHTTP(url string) *MCPServerHTTP {
	return &MCPServerHTTP{
		URL:           url,
		TransportType: detectMCPHTTPTransportType(url),
		client:        http.DefaultClient,
		cacheDirty:    true,
	}
}

func detectMCPHTTPTransportType(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	path := rawURL
	if err == nil {
		path = parsed.Path
	}
	path = strings.TrimRight(strings.ToLower(path), "/")
	if strings.HasSuffix(path, "/mcp") {
		return "streamable_http"
	}
	return "sse"
}

func (s *MCPServerHTTP) SetHTTPClient(client *http.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if client == nil {
		s.client = http.DefaultClient
		return
	}
	s.client = client
}

func (s *MCPServerHTTP) httpClientSnapshot() *http.Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return http.DefaultClient
	}
	return s.client
}

func (s *MCPServerHTTP) Initialize(ctx context.Context) error {
	s.mu.Lock()
	if s.initialized {
		s.mu.Unlock()
		return nil
	}
	if s.initState != nil {
		state := s.initState
		s.mu.Unlock()
		select {
		case <-state.done:
			return state.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	state := &mcpInitializeState{done: make(chan struct{})}
	s.initState = state
	startCloseSeq := s.closeSeq
	s.mu.Unlock()

	err := s.initialize(ctx)

	s.mu.Lock()
	state.err = err
	if err == nil && s.closeSeq == startCloseSeq {
		s.initialized = true
	}
	if s.initState == state {
		s.initState = nil
	}
	close(state.done)
	s.mu.Unlock()
	return err
}

func (s *MCPServerHTTP) initialize(ctx context.Context) error {
	params := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"clientInfo": map[string]interface{}{
			"name":    "conversation-worker",
			"version": "1.0.0",
		},
		"capabilities": map[string]interface{}{},
	}
	if _, err := s.sendRequest(ctx, "initialize", params); err != nil {
		return fmt.Errorf("initialize failed: %w", err)
	}
	if err := s.sendNotification(ctx, "initialized", map[string]interface{}{}); err != nil {
		return fmt.Errorf("initialized notification failed: %w", err)
	}
	return nil
}

func (s *MCPServerHTTP) ListTools(ctx context.Context) ([]Tool, error) {
	if !s.Initialized() {
		return nil, fmt.Errorf("MCPServer isn't initialized")
	}
	if tools, ok := s.cachedTools(); ok {
		return tools, nil
	}

	resp, err := s.sendRequest(ctx, "tools/list", map[string]interface{}{})
	if err != nil {
		return nil, fmt.Errorf("tools/list failed: %w", err)
	}
	tools, err := parseMCPTools(resp.Result, s)
	if err != nil {
		return nil, err
	}
	tools = filterMCPToolsByName(tools, s.AllowedTools)
	s.setToolsCache(tools)
	return tools, nil
}

func (s *MCPServerHTTP) InvalidateCache() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cacheDirty = true
	s.toolsCache = nil
}

func (s *MCPServerHTTP) Close() error {
	s.InvalidateCache()
	s.mu.Lock()
	s.closeSeq++
	s.initialized = false
	client := s.client
	s.mu.Unlock()
	if client != nil {
		client.CloseIdleConnections()
	}
	return nil
}

func (s *MCPServerHTTP) Initialized() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.initialized
}

func (s *MCPServerHTTP) cachedTools() ([]Tool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cacheDirty || s.toolsCache == nil {
		return nil, false
	}
	tools := make([]Tool, len(s.toolsCache))
	copy(tools, s.toolsCache)
	return tools, true
}

func (s *MCPServerHTTP) setToolsCache(tools []Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolsCache = make([]Tool, len(tools))
	copy(s.toolsCache, tools)
	s.cacheDirty = false
}

func (s *MCPServerHTTP) SetHeaders(headers map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Headers = cloneStringMap(headers)
}

func (s *MCPServerHTTP) headersSnapshot() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneStringMap(s.Headers)
}

func (s *MCPServerHTTP) sendRequest(ctx context.Context, method string, params interface{}) (*jsonRPCResponse, error) {
	id := s.msgID.Add(1)
	return s.postJSONRPC(ctx, &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	})
}

func (s *MCPServerHTTP) sendNotification(ctx context.Context, method string, params interface{}) error {
	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	_, err := s.postJSONRPCValue(ctx, req)
	return err
}

func (s *MCPServerHTTP) postJSONRPC(ctx context.Context, req *jsonRPCRequest) (*jsonRPCResponse, error) {
	return s.postJSONRPCValue(ctx, req)
}

func (s *MCPServerHTTP) postJSONRPCValue(ctx context.Context, value interface{}) (*jsonRPCResponse, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	for key, value := range s.headersSnapshot() {
		httpReq.Header.Set(key, value)
	}

	resp, err := s.httpClientSnapshot().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent {
		return &jsonRPCResponse{}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP MCP request failed with status %d", resp.StatusCode)
	}

	var decoded jsonRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	if decoded.Error != nil {
		return nil, fmt.Errorf("rpc error %d: %s", decoded.Error.Code, decoded.Error.Message)
	}
	return &decoded, nil
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

type MCPServerStdio struct {
	Command string
	Args    []string
	Env     map[string]string
	Cwd     string

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	msgID   atomic.Int64
	pending map[int64]chan *jsonRPCResponse
	mu      sync.Mutex
	writeMu sync.Mutex

	cacheDirty bool
	toolsCache []Tool
	initState  *mcpInitializeState
}

type MCPToolset struct {
	id          string
	mcpServer   MCPServer
	initialized bool
	tools       []Tool
	mu          sync.Mutex
}

func NewMCPToolset(id string, server MCPServer) *MCPToolset {
	return &MCPToolset{id: id, mcpServer: server}
}

func (t *MCPToolset) ID() string {
	if t == nil {
		return ""
	}
	return t.id
}

func (t *MCPToolset) Tools() []Tool {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	tools := make([]Tool, len(t.tools))
	copy(tools, t.tools)
	return tools
}

func (t *MCPToolset) Setup(ctx context.Context, reload bool) (*MCPToolset, error) {
	if t == nil {
		return nil, fmt.Errorf("MCP toolset is nil")
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if !reload && t.initialized {
		return t, nil
	}
	if t.mcpServer == nil {
		return nil, fmt.Errorf("MCP toolset %q has no server", t.id)
	}
	if !t.mcpServer.Initialized() {
		if err := t.mcpServer.Initialize(ctx); err != nil {
			return nil, err
		}
	} else if reload {
		t.mcpServer.InvalidateCache()
	}

	tools, err := t.mcpServer.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	t.tools = make([]Tool, len(tools))
	copy(t.tools, tools)
	t.initialized = true
	return t, nil
}

func (t *MCPToolset) FilterTools(filter func(Tool) bool) *MCPToolset {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if filter == nil {
		return t
	}
	filtered := make([]Tool, 0, len(t.tools))
	for _, tool := range t.tools {
		if tool != nil && filter(tool) {
			filtered = append(filtered, tool)
		}
	}
	t.tools = filtered
	return t
}

func (t *MCPToolset) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	server := t.mcpServer
	t.initialized = false
	t.tools = nil
	t.mu.Unlock()

	if server != nil {
		return server.Close()
	}
	return nil
}

func NewMCPServerStdio(command string, args []string) *MCPServerStdio {
	return &MCPServerStdio{
		Command:    command,
		Args:       args,
		pending:    make(map[int64]chan *jsonRPCResponse),
		cacheDirty: true,
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

type mcpToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
	raw  json.RawMessage
}

func (c *mcpToolContent) UnmarshalJSON(data []byte) error {
	type alias mcpToolContent
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*c = mcpToolContent(decoded)
	c.raw = append(c.raw[:0], data...)
	return nil
}

func (c mcpToolContent) visibleText() string {
	if c.Type == "text" {
		return c.Text
	}
	return string(c.raw)
}

func serializeMCPToolContent(content []mcpToolContent) (string, error) {
	if len(content) == 1 {
		return string(content[0].raw), nil
	}
	items := make([]json.RawMessage, 0, len(content))
	for _, item := range content {
		items = append(items, item.raw)
	}
	data, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (s *MCPServerStdio) Initialize(ctx context.Context) error {
	s.mu.Lock()
	if s.initState != nil {
		state := s.initState
		s.mu.Unlock()
		select {
		case <-state.done:
			return state.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if s.stdin != nil {
		s.mu.Unlock()
		return nil
	}
	state := &mcpInitializeState{done: make(chan struct{})}
	s.initState = state
	s.mu.Unlock()

	err := s.initialize(ctx)

	s.mu.Lock()
	state.err = err
	if err != nil {
		_ = s.closeTransportLocked()
	}
	if s.initState == state {
		s.initState = nil
	}
	close(state.done)
	s.mu.Unlock()
	return err
}

func (s *MCPServerStdio) initialize(ctx context.Context) error {
	s.cmd = exec.CommandContext(context.Background(), s.Command, s.Args...)
	s.cmd.Dir = s.Cwd
	if len(s.Env) > 0 {
		env := os.Environ()
		for key, value := range s.Env {
			env = append(env, fmt.Sprintf("%s=%s", key, value))
		}
		s.cmd.Env = env
	}

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

	if err := s.sendNotification("initialized", map[string]interface{}{}); err != nil {
		return fmt.Errorf("initialized notification failed: %w", err)
	}

	return nil
}

func (s *MCPServerStdio) ListTools(ctx context.Context) ([]Tool, error) {
	if !s.Initialized() {
		return nil, fmt.Errorf("MCPServer isn't initialized")
	}
	if tools, ok := s.cachedTools(); ok {
		return tools, nil
	}

	resp, err := s.sendRequest(ctx, "tools/list", map[string]interface{}{})
	if err != nil {
		return nil, fmt.Errorf("tools/list failed: %w", err)
	}

	tools, err := parseMCPTools(resp.Result, s)
	if err != nil {
		return nil, err
	}
	s.setToolsCache(tools)
	return tools, nil
}

func (s *MCPServerStdio) InvalidateCache() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cacheDirty = true
	s.toolsCache = nil
}

func (s *MCPServerStdio) cachedTools() ([]Tool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cacheDirty || s.toolsCache == nil {
		return nil, false
	}
	tools := make([]Tool, len(s.toolsCache))
	copy(tools, s.toolsCache)
	return tools, true
}

func (s *MCPServerStdio) setToolsCache(tools []Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolsCache = make([]Tool, len(tools))
	copy(s.toolsCache, tools)
	s.cacheDirty = false
}

func (s *MCPServerStdio) Initialized() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stdin != nil && s.initState == nil
}

func (s *MCPServerStdio) Close() error {
	s.InvalidateCache()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeTransportLocked()
}

func (s *MCPServerStdio) closeTransportLocked() error {
	var firstErr error
	if s.stdin != nil {
		if err := s.stdin.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.stdin = nil
	}
	if s.stdout != nil {
		if err := s.stdout.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.stdout = nil
	}
	if s.cmd != nil && s.cmd.Process != nil {
		cmd := s.cmd
		if err := s.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) && firstErr == nil {
			firstErr = err
		}
		_ = cmd.Wait()
	}
	s.cmd = nil
	return firstErr
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
	stdin := s.stdin
	if stdin == nil {
		s.mu.Unlock()
		return nil, errors.New("MCP stdio transport closed")
	}
	s.pending[id] = ch
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}()

	s.writeMu.Lock()
	_, err = stdin.Write(b)
	s.writeMu.Unlock()
	if err != nil {
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
	s.mu.Lock()
	stdin := s.stdin
	s.mu.Unlock()
	if stdin == nil {
		return errors.New("MCP stdio transport closed")
	}
	s.writeMu.Lock()
	_, err = stdin.Write(b)
	s.writeMu.Unlock()
	return err
}

func (s *MCPServerStdio) readLoop() {
	defer s.handleTransportClosed("MCP stdio transport closed")
	scanner := bufio.NewScanner(s.stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), mcpMaxStdioLineBytes)
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

func (s *MCPServerStdio) handleTransportClosed(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ch := range s.pending {
		delete(s.pending, id)
		select {
		case ch <- &jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &jsonRPCError{Code: -32000, Message: message}}:
		default:
		}
	}
	s.cacheDirty = true
	s.toolsCache = nil
	_ = s.closeTransportLocked()
}

type mcpProxyTool struct {
	server      mcpRequestSender
	name        string
	description string
	parameters  map[string]interface{}
	meta        map[string]interface{}
}

func (t *mcpProxyTool) ID() string          { return t.name }
func (t *mcpProxyTool) Name() string        { return t.name }
func (t *mcpProxyTool) Description() string { return t.description }
func (t *mcpProxyTool) Parameters() map[string]any {
	return t.parameters
}

func (t *mcpProxyTool) Execute(ctx context.Context, args string) (string, error) {
	var parsedArgs map[string]interface{}
	if err := json.Unmarshal([]byte(args), &parsedArgs); err != nil {
		return "", err
	}

	if t.server == nil {
		return "", NewToolError("Tool invocation failed: internal service is unavailable. Please check that the MCPServer is still running.")
	}
	if availability, ok := t.server.(mcpAvailability); ok && !availability.Initialized() {
		return "", NewToolError("Tool invocation failed: internal service is unavailable. Please check that the MCPServer is still running.")
	}

	resp, err := t.server.sendRequest(ctx, "tools/call", map[string]interface{}{
		"name":      t.name,
		"arguments": parsedArgs,
	})
	if err != nil {
		return "", err
	}

	var result struct {
		Content []mcpToolContent `json:"content"`
		IsError bool             `json:"isError"`
	}

	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", err
	}

	if result.IsError {
		parts := make([]string, 0, len(result.Content))
		for _, part := range result.Content {
			parts = append(parts, part.visibleText())
		}
		return "", NewToolError(strings.Join(parts, "\n"))
	}

	if len(result.Content) == 0 {
		return "", NewToolError(fmt.Sprintf("Tool '%s' completed without producing a result. This might indicate an issue with internal processing.", t.name))
	}
	return serializeMCPToolContent(result.Content)
}

func parseMCPTools(data json.RawMessage, server mcpRequestSender) ([]Tool, error) {
	var result struct {
		Tools []struct {
			Name        string                 `json:"name"`
			Description string                 `json:"description"`
			InputSchema map[string]interface{} `json:"inputSchema"`
			Meta        map[string]interface{} `json:"meta"`
		} `json:"tools"`
	}

	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse tools/list result: %w", err)
	}

	var tools []Tool
	for _, t := range result.Tools {
		tools = append(tools, &mcpProxyTool{
			server:      server,
			name:        t.Name,
			description: t.Description,
			parameters:  t.InputSchema,
			meta:        t.Meta,
		})
	}
	return tools, nil
}

func filterMCPToolsByName(tools []Tool, allowed []string) []Tool {
	if len(allowed) == 0 {
		return tools
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		name = strings.TrimSpace(name)
		if name != "" {
			allowedSet[name] = struct{}{}
		}
	}
	if len(allowedSet) == 0 {
		return nil
	}
	filtered := make([]Tool, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		if _, ok := allowedSet[tool.Name()]; ok {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

func (t *mcpProxyTool) ParseFunctionTools(format string) (map[string]interface{}, error) {
	schema := map[string]interface{}{
		"name":        t.name,
		"description": t.description,
		"parameters":  t.parameters,
	}
	if len(t.meta) > 0 {
		schema["meta"] = t.meta
	}
	return schema, nil
}
