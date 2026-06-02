package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cavos-io/rtp-agent/adapter/browser"
	"github.com/cavos-io/rtp-agent/core/llm"
)

const postActionDelay = 300 * time.Millisecond

type ComputerTool struct {
	actions *browser.PageActions
	width   int
	height  int
	tool    llm.Tool
}

func NewComputerTool(actions *browser.PageActions, width int, height int) *ComputerTool {
	toolset := &ComputerTool{
		actions: actions,
		width:   width,
		height:  height,
	}
	toolset.tool = newComputerUseTool(toolset, width, height)
	return toolset
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
	case "triple_click":
		x, y, err := requireCoordinate(args, "coordinate")
		if err != nil {
			return nil, err
		}
		c.actions.TripleClick(x, y)
	case "middle_click":
		x, y, err := requireCoordinate(args, "coordinate")
		if err != nil {
			return nil, err
		}
		c.actions.MiddleClick(x, y)
	case "mouse_move":
		x, y, err := requireCoordinate(args, "coordinate")
		if err != nil {
			return nil, err
		}
		c.actions.MouseMove(x, y)
	case "left_click_drag":
		sx, sy, err := requireCoordinate(args, "start_coordinate")
		if err != nil {
			return nil, err
		}
		ex, ey, err := requireCoordinate(args, "coordinate")
		if err != nil {
			return nil, err
		}
		c.actions.LeftClickDrag(sx, sy, ex, ey)
	case "left_mouse_down":
		x, y, err := requireCoordinate(args, "coordinate")
		if err != nil {
			return nil, err
		}
		c.actions.LeftMouseDown(x, y)
	case "left_mouse_up":
		x, y, err := requireCoordinate(args, "coordinate")
		if err != nil {
			return nil, err
		}
		c.actions.LeftMouseUp(x, y)
	case "scroll":
		x, y, err := requireCoordinate(args, "coordinate")
		if err != nil {
			return nil, err
		}
		direction, _ := args["scroll_direction"].(string)
		if direction == "" {
			direction = "down"
		}
		amount := 3
		if rawAmount, ok := args["scroll_amount"]; ok {
			parsedAmount, err := requireInt(rawAmount, "scroll_amount")
			if err != nil {
				return nil, err
			}
			amount = parsedAmount
		}
		c.actions.Scroll(x, y, direction, amount)
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
	case "hold_key":
		text, ok := args["text"].(string)
		if !ok {
			return nil, fmt.Errorf("missing required argument: 'text'")
		}
		duration := 0.5
		if rawDuration, ok := args["duration"]; ok {
			parsedDuration, err := requireFloat(rawDuration, "duration")
			if err != nil {
				return nil, err
			}
			duration = parsedDuration
		}
		c.actions.HoldKey(text, duration)
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

func requireInt(val interface{}, key string) (int, error) {
	num, err := requireFloat(val, key)
	if err != nil {
		return 0, err
	}
	return int(num), nil
}

func requireFloat(val interface{}, key string) (float64, error) {
	switch v := val.(type) {
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	default:
		return 0, fmt.Errorf("%s must be a number", key)
	}
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
	dispatcher *ComputerTool
	width      int
	height     int
}

func newComputerUseTool(dispatcher *ComputerTool, width, height int) llm.Tool {
	return &computerUseTool{dispatcher: dispatcher, width: width, height: height}
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

func (t *computerUseTool) AnthropicToolSpec() map[string]interface{} {
	return map[string]interface{}{
		"type":              "computer_20251124",
		"name":              "computer",
		"display_width_px":  t.width,
		"display_height_px": t.height,
		"display_number":    1,
	}
}

func (t *computerUseTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":     map[string]any{"type": "string"},
			"coordinate": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
			"text":       map[string]any{"type": "string"},
		},
		"required": []string{"action"},
	}
}

func (t *computerUseTool) Execute(ctx context.Context, args string) (string, error) {
	var parsedArgs map[string]interface{}
	if err := json.Unmarshal([]byte(args), &parsedArgs); err != nil {
		return "", err
	}
	action, ok := parsedArgs["action"].(string)
	if !ok || action == "" {
		return "", fmt.Errorf("missing required argument: 'action'")
	}
	content, err := t.dispatcher.Execute(ctx, action, parsedArgs)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(content)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
