package anthropic

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/adapter/browser"
)

func TestComputerToolExposesComputerUseTool(t *testing.T) {
	toolset := NewComputerTool(browser.NewPageActions(), 1024, 768)

	tools := toolset.Tools()
	if len(tools) != 1 {
		t.Fatalf("Tools length = %d, want 1", len(tools))
	}
	tool := tools[0]
	if tool.ID() != "computer" || tool.Name() != "computer_use" {
		t.Fatalf("tool identity = %q/%q, want computer/computer_use", tool.ID(), tool.Name())
	}
	if tool.Description() == "" {
		t.Fatal("tool description is empty")
	}
	params := tool.Parameters()
	if params["type"] != "object" {
		t.Fatalf("Parameters type = %#v, want object", params["type"])
	}
	if out, err := tool.Execute(context.Background(), `{"action":"screenshot"}`); err != nil || out != "Action dispatched" {
		t.Fatalf("Execute = %q/%v, want dispatch acknowledgement", out, err)
	}
}

func TestComputerToolDispatchesReferenceActionsWithoutFrame(t *testing.T) {
	toolset := NewComputerTool(browser.NewPageActions(), 1024, 768)

	cases := []struct {
		name   string
		action string
		args   map[string]interface{}
	}{
		{name: "screenshot", action: "screenshot", args: map[string]interface{}{}},
		{name: "left click", action: "left_click", args: map[string]interface{}{"coordinate": []interface{}{float64(10), float64(20)}}},
		{name: "right click", action: "right_click", args: map[string]interface{}{"coordinate": []interface{}{float64(10), float64(20)}}},
		{name: "double click", action: "double_click", args: map[string]interface{}{"coordinate": []interface{}{float64(10), float64(20)}}},
		{name: "triple click", action: "triple_click", args: map[string]interface{}{"coordinate": []interface{}{float64(10), float64(20)}}},
		{name: "middle click", action: "middle_click", args: map[string]interface{}{"coordinate": []interface{}{float64(10), float64(20)}}},
		{name: "mouse move", action: "mouse_move", args: map[string]interface{}{"coordinate": []interface{}{float64(10), float64(20)}}},
		{name: "drag", action: "left_click_drag", args: map[string]interface{}{"start_coordinate": []interface{}{float64(1), float64(2)}, "coordinate": []interface{}{float64(10), float64(20)}}},
		{name: "mouse down", action: "left_mouse_down", args: map[string]interface{}{"coordinate": []interface{}{float64(10), float64(20)}}},
		{name: "mouse up", action: "left_mouse_up", args: map[string]interface{}{"coordinate": []interface{}{float64(10), float64(20)}}},
		{name: "scroll default", action: "scroll", args: map[string]interface{}{"coordinate": []interface{}{float64(10), float64(20)}}},
		{name: "scroll explicit", action: "scroll", args: map[string]interface{}{"coordinate": []interface{}{float64(10), float64(20)}, "scroll_direction": "up", "scroll_amount": float64(4)}},
		{name: "type", action: "type", args: map[string]interface{}{"text": "hello"}},
		{name: "key", action: "key", args: map[string]interface{}{"text": "Enter"}},
		{name: "hold key default", action: "hold_key", args: map[string]interface{}{"text": "Shift"}},
		{name: "hold key explicit", action: "hold_key", args: map[string]interface{}{"text": "Shift", "duration": float64(1.25)}},
		{name: "wait", action: "wait", args: map[string]interface{}{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content, err := toolset.Execute(context.Background(), tc.action, tc.args)
			if err != nil {
				t.Fatalf("Execute(%q) error = %v, want nil", tc.action, err)
			}
			if len(content) != 1 || content[0]["type"] != "text" || content[0]["text"] != "(no frame available yet)" {
				t.Fatalf("content = %#v, want no-frame text block", content)
			}
		})
	}
}

func TestComputerToolValidatesRequiredArguments(t *testing.T) {
	toolset := NewComputerTool(browser.NewPageActions(), 1024, 768)

	cases := []struct {
		name    string
		action  string
		args    map[string]interface{}
		wantErr string
	}{
		{name: "missing coordinate", action: "left_click", args: map[string]interface{}{}, wantErr: "missing required argument"},
		{name: "invalid coordinate", action: "left_click", args: map[string]interface{}{"coordinate": "10,20"}, wantErr: "invalid coordinate"},
		{name: "non numeric coordinate", action: "left_click", args: map[string]interface{}{"coordinate": []interface{}{"x", float64(20)}}, wantErr: "coordinates must be numbers"},
		{name: "missing text", action: "type", args: map[string]interface{}{}, wantErr: "missing required argument"},
		{name: "invalid scroll amount", action: "scroll", args: map[string]interface{}{"coordinate": []interface{}{float64(10), float64(20)}, "scroll_amount": "lots"}, wantErr: "scroll_amount must be a number"},
		{name: "invalid hold duration", action: "hold_key", args: map[string]interface{}{"text": "Shift", "duration": "long"}, wantErr: "duration must be a number"},
		{name: "unknown", action: "unknown", args: map[string]interface{}{}, wantErr: "unknown computer_use action"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := toolset.Execute(context.Background(), tc.action, tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Execute error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestRequireCoordinateAndScreenshotContent(t *testing.T) {
	x, y, err := requireCoordinate(map[string]interface{}{
		"coordinate": []interface{}{float64(12), float64(34)},
	}, "coordinate")
	if err != nil {
		t.Fatalf("requireCoordinate error = %v, want nil", err)
	}
	if x != 12 || y != 34 {
		t.Fatalf("coordinate = %d,%d, want 12,34", x, y)
	}

	content := screenshotContent([]byte("png"))
	source, ok := content[0]["source"].(map[string]interface{})
	if !ok {
		t.Fatalf("source = %T, want map", content[0]["source"])
	}
	if source["media_type"] != "image/png" {
		t.Fatalf("media_type = %#v, want image/png", source["media_type"])
	}
	if source["data"] != base64.StdEncoding.EncodeToString([]byte("png")) {
		t.Fatalf("data = %#v, want base64 png payload", source["data"])
	}
}
