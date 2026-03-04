package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cavos-io/conversation-worker/adapter/browser"
	"github.com/cavos-io/conversation-worker/core/llm"
)

const postActionDelay = 300 * time.Millisecond

type ComputerTool struct {
	actions *browser.PageActions
	width   int
	height  int
	tool    llm.Tool
}

func NewComputerTool(actions *browser.PageActions, width int, height int) *ComputerTool {
	return &ComputerTool{
		actions: actions,
		width:   width,
		height:  height,
		tool:    newComputerUseTool(width, height),
	}
}

func (c *ComputerTool) Tools() []llm.Tool {
	return []llm.Tool{c.tool}
}

func (c *ComputerTool) Execute(ctx context.Context, action string, args map[string]interface{}) ([]map[string]interface{}, error) {
	switch action {
	case "screenshot":
		// Do nothing, just take screenshot at the end
	case "left_click":
		x, y, err := requireCoordinate(args, "coordinate")
		if err != nil {
			return nil, err
		}
		modifiers, _ := args["text"].(string)
		c.actions.LeftClick(x, y, modifiers)
	case "right_click":
		x, y, err := requireCoordinate(args, "coordinate")
		if err != nil {
			return nil, err
		}
		c.actions.RightClick(x, y)
	case "double_click":
		x, y, err := requireCoordinate(args, "coordinate")
		if err != nil {
			return nil, err
		}
		c.actions.DoubleClick(x, y)
	case "type":
		text, ok := args["text"].(string)
		if !ok {
			return nil, fmt.Errorf("missing required argument: 'text'")
		}
		c.actions.TypeText(text)
	case "key":
		text, ok := args["text"].(string)
		if !ok {
			return nil, fmt.Errorf("missing required argument: 'text'")
		}
		c.actions.Key(text)
	case "wait":
		c.actions.Wait()
	default:
		return nil, fmt.Errorf("unknown computer_use action: %s", action)
	}

	time.Sleep(postActionDelay)

	frame := c.actions.LastFrame()
	if frame == nil {
		return []map[string]interface{}{
			{"type": "text", "text": "(no frame available yet)"},
		}, nil
	}

	return screenshotContent(frame), nil
}

func requireCoordinate(args map[string]interface{}, key string) (int, int, error) {
	val, ok := args[key]
	if !ok {
		return 0, 0, fmt.Errorf("missing required argument: %q", key)
	}
	coords, ok := val.([]interface{})
	if !ok || len(coords) < 2 {
		return 0, 0, fmt.Errorf("invalid coordinate format")
	}
	
	x, okX := coords[0].(float64)
	y, okY := coords[1].(float64)
	if !okX || !okY {
		return 0, 0, fmt.Errorf("coordinates must be numbers")
	}
	
	return int(x), int(y), nil
}

func screenshotContent(frame []byte) []map[string]interface{} {
	b64Data := base64.StdEncoding.EncodeToString(frame)
	return []map[string]interface{}{
		{
			"type": "image",
			"source": map[string]interface{}{
				"type":       "base64",
				"media_type": "image/png",
				"data":       b64Data,
			},
		},
	}
}

type computerUseTool struct {
	width  int
	height int
}

func newComputerUseTool(width, height int) llm.Tool {
	return &computerUseTool{width: width, height: height}
}

func (t *computerUseTool) ID() string {
	return "computer"
}

func (t *computerUseTool) Name() string {
	return "computer_use"
}

func (t *computerUseTool) Description() string {
	return "Use a computer to browse the web or interact with applications"
}

func (t *computerUseTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{"type": "string"},
			"coordinate": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
			"text": map[string]any{"type": "string"},
		},
		"required": []string{"action"},
	}
}

func (t *computerUseTool) Execute(ctx context.Context, args string) (string, error) {
	var parsedArgs map[string]interface{}
	json.Unmarshal([]byte(args), &parsedArgs)
	
	// The computer tool relies on the external dispatch handler for actual execution, 
	// typically intercepting the command before it reaches this basic Execute call,
	// or calling Execute on the Toolset directly.
	return "Action dispatched", nil
}
