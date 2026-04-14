package llm

import (
	"errors"
	"fmt"
	"reflect"
)

var ErrStopResponse = errors.New("stop response")

type ToolContext struct {
	tools          []interface{} // Tool | Toolset
	functionTools  map[string]Tool
	providerTools  []ProviderTool
	toolsets       []Toolset
}

// ProviderTool represents a tool that is evaluated or passed raw to a provider.
type ProviderTool interface {
	IsProviderTool() bool
}

func NewToolContext(tools []interface{}) *ToolContext {
	ctx := &ToolContext{}
	ctx.UpdateTools(tools)
	return ctx
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
