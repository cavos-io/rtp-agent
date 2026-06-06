package agent

import (
	"context"

	"github.com/cavos-io/rtp-agent/core/llm"
)

type MockToolFunc func(ctx context.Context, args string) (string, error)

type mockToolsContextKey struct{}

type mockToolsByAgent map[*Agent]map[string]MockToolFunc

func MockTools(ctx context.Context, agent AgentInterface, mocks map[string]MockToolFunc) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	baseAgent := baseAgentForMockTools(agent)
	if baseAgent == nil || len(mocks) == 0 {
		return ctx
	}

	updated := make(mockToolsByAgent)
	if current, ok := ctx.Value(mockToolsContextKey{}).(mockToolsByAgent); ok {
		for registeredAgent, registeredMocks := range current {
			updated[registeredAgent] = copyMockToolFuncs(registeredMocks)
		}
	}
	updated[baseAgent] = copyMockToolFuncs(mocks)
	return context.WithValue(ctx, mockToolsContextKey{}, updated)
}

func mockToolContext(ctx context.Context, toolCtx *llm.ToolContext, session *AgentSession, name string) *llm.ToolContext {
	if toolCtx == nil || session == nil || toolCtx.GetFunctionTool(name) == nil {
		return toolCtx
	}
	ctx = contextWithSessionMockTools(ctx, session)
	mock, ok := mockToolFunc(ctx, session.Agent, name)
	if !ok {
		return toolCtx
	}
	return llm.NewToolContext([]interface{}{mockFunctionTool{name: name, execute: mock}})
}

func contextWithSessionMockTools(ctx context.Context, session *AgentSession) context.Context {
	if session == nil || len(session.Options.MockTools) == 0 {
		return ctx
	}
	return MockTools(ctx, session.Agent, session.Options.MockTools)
}

func mockToolFunc(ctx context.Context, agent AgentInterface, name string) (MockToolFunc, bool) {
	if ctx == nil {
		return nil, false
	}
	baseAgent := baseAgentForMockTools(agent)
	if baseAgent == nil {
		return nil, false
	}
	registered, ok := ctx.Value(mockToolsContextKey{}).(mockToolsByAgent)
	if !ok {
		return nil, false
	}
	mocks := registered[baseAgent]
	if mocks == nil {
		return nil, false
	}
	mock, ok := mocks[name]
	return mock, ok && mock != nil
}

func baseAgentForMockTools(agent AgentInterface) *Agent {
	if agent == nil {
		return nil
	}
	return agent.GetAgent()
}

func copyMockToolFuncs(mocks map[string]MockToolFunc) map[string]MockToolFunc {
	copied := make(map[string]MockToolFunc, len(mocks))
	for name, mock := range mocks {
		if mock != nil {
			copied[name] = mock
		}
	}
	return copied
}

type mockFunctionTool struct {
	name    string
	execute MockToolFunc
}

func (t mockFunctionTool) ID() string { return t.name }

func (t mockFunctionTool) Name() string { return t.name }

func (t mockFunctionTool) Description() string { return "" }

func (t mockFunctionTool) Parameters() map[string]any { return nil }

func (t mockFunctionTool) Execute(ctx context.Context, args string) (string, error) {
	return t.execute(ctx, args)
}
