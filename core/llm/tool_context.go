package llm

import (
	"fmt"
	"reflect"
	"sort"
)

type ToolContext struct {
	tools         []interface{} // Tool | Toolset
	functionTools map[string]Tool
	providerTools []Tool
	toolsets      []Toolset
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

func (c *ToolContext) ProviderTools() []Tool {
	arr := make([]Tool, len(c.providerTools))
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
	names := make([]string, 0, len(c.functionTools))
	for name := range c.functionTools {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		tools = append(tools, c.functionTools[name])
	}
	tools = append(tools, c.providerTools...)
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
	c.providerTools = make([]Tool, 0)
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

		if t, ok := tool.(Tool); ok {
			// In Go parity, we don't differentiate ProviderTool and FunctionTool
			// by base class as easily, we can assume all non-Toolset interfaces
			// are function tools if they satisfy Tool interface.
			// The original python distinguishes ProviderTool.

			// For now, treat all standalone tools as function tools.
			name := t.Name()
			if existing, exists := c.functionTools[name]; exists {
				if sameTool(existing, t) {
					return nil
				}
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

func sameTool(a, b Tool) bool {
	aValue := reflect.ValueOf(a)
	bValue := reflect.ValueOf(b)
	if !aValue.IsValid() || !bValue.IsValid() {
		return !aValue.IsValid() && !bValue.IsValid()
	}
	if aValue.Type() != bValue.Type() || !aValue.Type().Comparable() {
		return false
	}
	return a == b
}

func (c *ToolContext) Copy() *ToolContext {
	toolsCopy := make([]interface{}, len(c.tools))
	copy(toolsCopy, c.tools)
	return NewToolContext(toolsCopy)
}
