package llm

import (
	"errors"
	"fmt"
	"reflect"
)

type ToolContext struct {
	tools             []interface{} // Tool | Toolset
	functionTools     map[string]Tool
	functionToolOrder []string
	providerTools     []ProviderTool
	toolsets          []Toolset
}

type closeableToolset interface {
	Close() error
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

func (c *ToolContext) Close() error {
	if c == nil {
		return nil
	}
	var errs []error
	for _, toolset := range c.toolsets {
		if closeable, ok := toolset.(closeableToolset); ok {
			if err := closeable.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (c *ToolContext) Flatten() []Tool {
	tools := make([]Tool, 0, len(c.functionTools)+len(c.providerTools))
	for _, name := range c.functionToolOrder {
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
	return c.addToolValue(tool, true, nil)
}

func (c *ToolContext) addToolValue(tool interface{}, topLevel bool, exclude []Tool) error {
	if t, ok := tool.(ProviderTool); ok {
		if toolExcluded(t, exclude) {
			return nil
		}
		c.providerTools = append(c.providerTools, t)
		if topLevel {
			c.tools = append(c.tools, tool)
		}
		return nil
	}

	if t, ok := tool.(Toolset); ok {
		for _, childTool := range t.Tools() {
			if err := c.addToolValue(childTool, false, exclude); err != nil {
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
		if toolExcluded(t, exclude) {
			return nil
		}
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
	return c.updateTools(tools, nil)
}

func (c *ToolContext) updateTools(tools []interface{}, exclude []Tool) error {
	c.tools = make([]interface{}, 0, len(tools))
	c.functionTools = make(map[string]Tool)
	c.functionToolOrder = make([]string, 0)
	c.providerTools = make([]ProviderTool, 0)
	c.toolsets = make([]Toolset, 0)

	for _, t := range tools {
		if err := c.addToolValue(t, true, exclude); err != nil {
			return err
		}
	}
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
	c.functionToolOrder = append(c.functionToolOrder, name)
	return nil
}

func toolExcluded(tool Tool, exclude []Tool) bool {
	for _, excluded := range exclude {
		if sameTool(tool, excluded) {
			return true
		}
	}
	return false
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

func (c *ToolContext) SyncFlattened(tools []Tool) error {
	current := c.Flatten()
	if sameToolSet(current, tools) {
		return nil
	}

	added := make([]interface{}, 0)
	for _, tool := range tools {
		if !toolInSlice(tool, current) {
			added = append(added, tool)
		}
	}

	removed := make([]Tool, 0)
	for _, tool := range current {
		if !toolInSlice(tool, tools) {
			removed = append(removed, tool)
		}
	}

	structured := make([]interface{}, 0, len(c.tools)+len(added))
	for _, tool := range c.tools {
		functionTool, ok := tool.(Tool)
		if ok && toolExcluded(functionTool, removed) {
			continue
		}
		structured = append(structured, tool)
	}
	structured = append(structured, added...)
	return c.updateTools(structured, removed)
}

func sameToolSet(left, right []Tool) bool {
	if len(left) != len(right) {
		return false
	}
	for _, tool := range left {
		if !toolInSlice(tool, right) {
			return false
		}
	}
	return true
}

func toolInSlice(tool Tool, tools []Tool) bool {
	for _, candidate := range tools {
		if sameTool(tool, candidate) {
			return true
		}
	}
	return false
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
