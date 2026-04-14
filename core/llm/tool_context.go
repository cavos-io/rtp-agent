package llm

import (
	"context"
	"errors"
	"fmt"
	"reflect"
)

var ErrStopResponse = errors.New("stop response")

type ToolContext struct {
	tools         []interface{} // Tool | Toolset
	functionTools map[string]Tool
	providerTools []ProviderTool
	toolsets      []Toolset
}

// ProviderTool represents a tool that is evaluated or passed raw to a provider.
type ProviderTool interface {
	IsProviderTool() bool
}

// RawFunctionTool represents a tool defined by a raw JSON Schema.
type RawFunctionTool struct {
	ToolName        string
	ToolDescription string
	ToolParameters  map[string]any
	ExecuteFunc     func(ctx context.Context, args map[string]any) (any, error)
}

func (t *RawFunctionTool) ID() string                 { return t.ToolName }
func (t *RawFunctionTool) Name() string           { return t.ToolName }
func (t *RawFunctionTool) Description() string    { return t.ToolDescription }
func (t *RawFunctionTool) Parameters() map[string]any { return t.ToolParameters }
func (t *RawFunctionTool) Execute(ctx context.Context, args any) (any, error) {
	if t.ExecuteFunc != nil {
		m, _ := args.(map[string]any)
		return t.ExecuteFunc(ctx, m)
	}
	return nil, nil
}

func NewToolContext(tools []interface{}) *ToolContext {
	ctx := &ToolContext{}
	_ = ctx.UpdateTools(tools)
	return ctx
}

func (c *ToolContext) ParseFunctionTools(format string) []map[string]any {
	out := make([]map[string]any, 0)
	for _, t := range c.functionTools {
		switch format {
		case "openai":
			out = append(out, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name(),
					"description": t.Description(),
					"parameters":  t.Parameters(),
				},
			})
		case "anthropic":
			out = append(out, map[string]any{
				"name":         t.Name(),
				"description":  t.Description(),
				"input_schema": t.Parameters(),
			})
		case "google":
			// Google Gemini format
			out = append(out, map[string]any{
				"name":        t.Name(),
				"description": t.Description(),
				"parameters":  t.Parameters(),
			})
		case "aws":
			// AWS Bedrock toolSpec format
			out = append(out, map[string]any{
				"toolSpec": map[string]any{
					"name":        t.Name(),
					"description": t.Description(),
					"inputSchema": map[string]any{
						"json": t.Parameters(),
					},
				},
			})
		default:
			out = append(out, map[string]any{
				"name":        t.Name(),
				"description": t.Description(),
				"parameters":  t.Parameters(),
			})
		}
	}
	return out
}

func EmptyToolContext() *ToolContext {
	return NewToolContext([]interface{}{})
}

func (c *ToolContext) FunctionTools() map[string]Tool {
	m := make(map[string]Tool)
	for k, v := range c.functionTools {
		m[k] = v
	}
	return m
}

func (c *ToolContext) ProviderTools() []ProviderTool {
	arr := make([]ProviderTool, len(c.providerTools))
	copy(arr, c.providerTools)
	return arr
}

func (c *ToolContext) Toolsets() []Toolset {
	arr := make([]Toolset, len(c.toolsets))
	copy(arr, c.toolsets)
	return arr
}

func (c *ToolContext) Flatten() []Tool {
	tools := make([]Tool, 0)
	for _, t := range c.functionTools {
		tools = append(tools, t)
	}
	// Provider tools are handled separately when passed to specific LLMs
	return tools
}

func (c *ToolContext) GetFunctionTool(name string) Tool {
	if t, ok := c.functionTools[name]; ok {
		return t
	}
	return nil
}

func (c *ToolContext) UpdateTools(tools []interface{}) error {
	c.tools = tools
	c.functionTools = make(map[string]Tool)
	c.providerTools = make([]ProviderTool, 0)
	c.toolsets = make([]Toolset, 0)

	var addTool func(tool interface{}) error
	addTool = func(tool interface{}) error {
		if t, ok := tool.(Toolset); ok {
			for _, childTool := range t.Tools() {
				if err := addTool(childTool); err != nil {
					return err
				}
			}
			c.toolsets = append(c.toolsets, t)
			return nil
		}

		if pt, ok := tool.(ProviderTool); ok {
			c.providerTools = append(c.providerTools, pt)
			return nil
		}

		if t, ok := tool.(Tool); ok {
			name := t.Name()
			if _, exists := c.functionTools[name]; exists {
				return fmt.Errorf("duplicate function name: %s", name)
			}
			c.functionTools[name] = t
			return nil
		}

		return fmt.Errorf("unknown tool type: %v", reflect.TypeOf(tool))
	}

	for _, t := range tools {
		if err := addTool(t); err != nil {
			return err
		}
	}
	return nil
}

func (c *ToolContext) Copy() *ToolContext {
	toolsCopy := make([]interface{}, len(c.tools))
	copy(toolsCopy, c.tools)
	return NewToolContext(toolsCopy)
}
