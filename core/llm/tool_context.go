package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
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
	Name() string
	ProviderSchema(format string) map[string]any
}

func BuildJSONSchema(t reflect.Type) map[string]any {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return map[string]any{"type": "object"}
	}

	properties := make(map[string]any)
	required := make([]string, 0)

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" { // Skip unexported fields
			continue
		}

		name := field.Tag.Get("json")
		if name == "" || name == "-" {
			name = field.Name
		}
		// handle omitempty
		if idx := strings.Index(name, ","); idx != -1 {
			name = name[:idx]
		}

		prop := map[string]any{}
		switch field.Type.Kind() {
		case reflect.String:
			prop["type"] = "string"
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			prop["type"] = "integer"
		case reflect.Float32, reflect.Float64:
			prop["type"] = "number"
		case reflect.Bool:
			prop["type"] = "boolean"
		case reflect.Struct:
			prop = BuildJSONSchema(field.Type)
		case reflect.Slice:
			prop["type"] = "array"
			prop["items"] = map[string]any{"type": "string"} // simplistic
		default:
			prop["type"] = "object"
		}

		desc := field.Tag.Get("description")
		if desc != "" {
			prop["description"] = desc
		}

		properties[name] = prop
		required = append(required, name)
	}

	return map[string]any{
		"type":       "object",
		"properties": properties,
		"required":   required,
	}
}

// BuildFunctionTool uses reflection to build a Tool from a Go function, extracting its signature into a JSON schema.
func BuildFunctionTool(fn any, name, description string) (Tool, error) {
	fnVal := reflect.ValueOf(fn)
	if fnVal.Kind() != reflect.Func {
		return nil, fmt.Errorf("expected func, got %v", fnVal.Kind())
	}
	fnType := fnVal.Type()

	var parameters map[string]any

	// Check if the last argument (excluding error) is a struct
	argIdx := -1
	for i := 0; i < fnType.NumIn(); i++ {
		inType := fnType.In(i)
		if inType.String() == "context.Context" {
			continue
		}
		argIdx = i
		break
	}

	if argIdx != -1 {
		inType := fnType.In(argIdx)
		if inType.Kind() == reflect.Ptr {
			inType = inType.Elem()
		}
		if inType.Kind() == reflect.Struct {
			parameters = BuildJSONSchema(inType)
		}
	}

	if parameters == nil {
		// Fallback to simple positional arguments
		properties := make(map[string]any)
		required := make([]string, 0)

		for i := 0; i < fnType.NumIn(); i++ {
			inType := fnType.In(i)
			if i == 0 && inType.String() == "context.Context" {
				continue
			}

			argName := fmt.Sprintf("arg%d", i)
			prop := map[string]any{}

			switch inType.Kind() {
			case reflect.String:
				prop["type"] = "string"
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				prop["type"] = "integer"
			case reflect.Float32, reflect.Float64:
				prop["type"] = "number"
			case reflect.Bool:
				prop["type"] = "boolean"
			default:
				prop["type"] = "object"
			}

			properties[argName] = prop
			required = append(required, argName)
		}

		parameters = map[string]any{
			"type":       "object",
			"properties": properties,
			"required":   required,
		}
	}

	return &RawFunctionTool{
		ToolName:        name,
		ToolDescription: description,
		ToolParameters:  parameters,
		ExecuteFunc: func(ctx context.Context, args map[string]any) (any, error) {
			in := make([]reflect.Value, fnType.NumIn())
			for i := 0; i < fnType.NumIn(); i++ {
				inType := fnType.In(i)
				if inType.String() == "context.Context" {
					in[i] = reflect.ValueOf(ctx)
					continue
				}

				// If it's a struct arg
				if inType.Kind() == reflect.Struct || (inType.Kind() == reflect.Ptr && inType.Elem().Kind() == reflect.Struct) {
					isPtr := inType.Kind() == reflect.Ptr
					structType := inType
					if isPtr {
						structType = inType.Elem()
					}
					structVal := reflect.New(structType).Elem()

					// simplistic unmarshal from map
					data, _ := json.Marshal(args)
					_ = json.Unmarshal(data, structVal.Addr().Interface())

					if isPtr {
						in[i] = structVal.Addr()
					} else {
						in[i] = structVal
					}
					continue
				}

				argName := fmt.Sprintf("arg%d", i)
				if val, ok := args[argName]; ok {
					in[i] = reflect.ValueOf(val).Convert(inType)
				} else {
					in[i] = reflect.Zero(inType)
				}
			}
			out := fnVal.Call(in)
			if len(out) > 0 {
				errIdx := len(out) - 1
				if !out[errIdx].IsNil() {
					if err, ok := out[errIdx].Interface().(error); ok {
						return nil, err
					}
				}
				if len(out) > 1 {
					return out[0].Interface(), nil
				}
			}
			return nil, nil
		},
	}, nil
}
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
		case "openai.responses":
			out = append(out, map[string]any{
				"type":        "function",
				"name":        t.Name(),
				"description": t.Description(),
				"parameters":  t.Parameters(),
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

	for _, pt := range c.providerTools {
		if schema := pt.ProviderSchema(format); schema != nil {
			out = append(out, schema)
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

func (c *ToolContext) Flatten() []interface{} {
	tools := make([]interface{}, 0)
	for _, t := range c.functionTools {
		tools = append(tools, t)
	}
	for _, t := range c.providerTools {
		tools = append(tools, t)
	}
	return tools
}

func (c *ToolContext) GetFunctionTool(name string) Tool {
	if t, ok := c.functionTools[name]; ok {
		return t
	}
	return nil
}

func (c *ToolContext) Equal(other *ToolContext) bool {
	if other == nil {
		return false
	}
	if len(c.functionTools) != len(other.functionTools) {
		return false
	}
	for name, t := range c.functionTools {
		if otherT, ok := other.functionTools[name]; !ok || t != otherT {
			return false
		}
	}
	if len(c.providerTools) != len(other.providerTools) {
		return false
	}
	for i, t := range c.providerTools {
		if other.providerTools[i] != t {
			return false
		}
	}
	return true
}

func (c *ToolContext) Merge(other *ToolContext) {
	if other == nil {
		return
	}
	for name, t := range other.functionTools {
		if _, exists := c.functionTools[name]; !exists {
			c.functionTools[name] = t
		}
	}
	for _, pt := range other.providerTools {
		found := false
		for _, existing := range c.providerTools {
			if existing.Name() == pt.Name() {
				found = true
				break
			}
		}
		if !found {
			c.providerTools = append(c.providerTools, pt)
		}
	}
	for _, ts := range other.toolsets {
		found := false
		for _, existing := range c.toolsets {
			if existing.ID() == ts.ID() {
				found = true
				break
			}
		}
		if !found {
			c.toolsets = append(c.toolsets, ts)
		}
	}
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

// FlattenTools recursively unwraps Toolsets and returns a slice of Tool and ProviderTool.
func FlattenTools(tools []interface{}) []interface{} {
	out := make([]interface{}, 0)
	var add func(t interface{})
	add = func(t interface{}) {
		if ts, ok := t.(Toolset); ok {
			for _, child := range ts.Tools() {
				add(child)
			}
			return
		}
		if _, ok := t.(Tool); ok {
			out = append(out, t)
			return
		}
		if _, ok := t.(ProviderTool); ok {
			out = append(out, t)
			return
		}
		// If it's something else, append it anyway to let adapters deal with it or ignore
		out = append(out, t)
	}
	for _, t := range tools {
		add(t)
	}
	return out
}
