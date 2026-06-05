package llm

import (
	"fmt"
	"reflect"
	"sort"
)

type ToolContext struct {
	tools         []interface{} // Tool | Toolset
	functionTools map[string]Tool
	providerTools []ProviderTool
	toolsets      []Toolset
}

func NewToolContext(tools []interface{}) *ToolContext {
	ctx := &ToolContext{}
	if err := ctx.UpdateTools(tools); err != nil {
		panic(err)
	}
	return ctx
}

func EmptyToolContext() *ToolContext {
	return (*ToolContext)(nil).Empty()
}

func (*ToolContext) Empty() *ToolContext {
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
	names := make([]string, 0, len(c.functionTools))
	for name := range c.functionTools {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		tools = append(tools, c.functionTools[name])
	}
	for _, tool := range c.providerTools {
		tools = append(tools, tool)
	}
	return tools
}

func (c *ToolContext) GetFunctionTool(name string) Tool {
	if t, ok := c.functionTools[name]; ok {
		return t
	}
	return nil
}

func (c *ToolContext) AddTool(tool interface{}) error {
	return c.addToolValue(tool, true)
}

func (c *ToolContext) addToolValue(tool interface{}, topLevel bool) error {
	if t, ok := tool.(ProviderTool); ok {
		c.providerTools = append(c.providerTools, t)
		sort.Slice(c.providerTools, func(i, j int) bool {
			return c.providerTools[i].ID() < c.providerTools[j].ID()
		})
		if topLevel {
			c.tools = append(c.tools, tool)
		}
		return nil
	}

	if t, ok := tool.(Toolset); ok {
		for _, childTool := range t.Tools() {
			if err := c.addToolValue(childTool, false); err != nil {
				return err
			}
		}
		c.toolsets = append(c.toolsets, t)
		if topLevel {
			c.tools = append(c.tools, tool)
		}
		return nil
	}
	if t, ok := tool.(Tool); ok {
		if err := c.addTool(t); err != nil {
			return err
		}
		if topLevel {
			c.tools = append(c.tools, tool)
		}
		return nil
	}

	return fmt.Errorf("unknown tool type: %v", reflect.TypeOf(tool))
}

func (c *ToolContext) UpdateTools(tools []interface{}) error {
	c.tools = make([]interface{}, 0, len(tools))
	c.functionTools = make(map[string]Tool)
	c.providerTools = make([]ProviderTool, 0)
	c.toolsets = make([]Toolset, 0)

	for _, t := range tools {
		if err := c.AddTool(t); err != nil {
			return err
		}
	}
	sort.Slice(c.providerTools, func(i, j int) bool {
		return c.providerTools[i].ID() < c.providerTools[j].ID()
	})
	return nil
}

func (c *ToolContext) addTool(tool Tool) error {
	name := tool.Name()
	if existing, exists := c.functionTools[name]; exists {
		if sameTool(existing, tool) {
			return nil
		}
		return fmt.Errorf("duplicate function name: %s", name)
	}
	c.functionTools[name] = tool
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

func (c *ToolContext) Equal(other *ToolContext) bool {
	if c == other {
		return true
	}
	if other == nil {
		return false
	}
	if len(c.functionTools) != len(other.functionTools) {
		return false
	}
	for name, tool := range c.functionTools {
		if other.functionTools[name] != tool {
			return false
		}
	}
	if len(c.providerTools) != len(other.providerTools) {
		return false
	}
	for _, tool := range c.providerTools {
		found := false
		for _, otherTool := range other.providerTools {
			if otherTool == tool {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if len(c.toolsets) != len(other.toolsets) {
		return false
	}
	for _, toolset := range c.toolsets {
		found := false
		for _, otherToolset := range other.toolsets {
			if otherToolset == toolset {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
