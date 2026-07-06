package anthropic

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/adapter/browser"
	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestComputerToolExposesComputerUseTool(t *testing.T) {
	actions := browser.NewPageActions()
	toolset := NewComputerTool(actions, 1024, 768)

	tools := toolset.Tools()
	if len(tools) != 1 {
		t.Fatalf("Tools length = %d, want 1", len(tools))
	}
	tool := tools[0]
	if tool.ID() != "computer" || tool.Name() != "computer" {
		t.Fatalf("tool identity = %q/%q, want computer/computer", tool.ID(), tool.Name())
	}
	if tool.Description() == "" {
		t.Fatal("tool description is empty")
	}
	params := tool.Parameters()
	if params["type"] != "object" {
		t.Fatalf("Parameters type = %#v, want object", params["type"])
	}
	out, err := tool.Execute(context.Background(), `{"action":"left_click","coordinate":[10,20]}`)
	if err != nil {
		t.Fatalf("Execute error = %v, want nil", err)
	}
	if !strings.Contains(out, "(no frame available yet)") {
		t.Fatalf("Execute output = %q, want no-frame content", out)
	}
	events := actions.Events()
	if len(events) != 3 {
		t.Fatalf("len(events) = %d, want mouse move/down/up", len(events))
	}
	if events[0].Type != "mouse_move" || events[0].X != 10 || events[0].Y != 20 {
		t.Fatalf("event[0] = %#v, want mouse_move at 10,20", events[0])
	}
}

func TestComputerToolProviderNameExecutesReferenceToolCall(t *testing.T) {
	actions := browser.NewPageActions()
	toolset := NewComputerTool(actions, 1024, 768)
	tools := toolset.Tools()
	toolCtx := llm.NewToolContext([]interface{}{tools[0]})

	result := llm.ExecuteFunctionCall(context.Background(), &llm.FunctionToolCall{
		Name:      "computer",
		CallID:    "call_computer",
		Arguments: `{"action":"left_click","coordinate":[10,20]}`,
	}, toolCtx)

	if result.RawError != nil {
		t.Fatalf("ExecuteFunctionCall RawError = %v, want nil", result.RawError)
	}
	if result.FncCallOut == nil || result.FncCallOut.IsError {
		t.Fatalf("FncCallOut = %#v, want successful computer output", result.FncCallOut)
	}
	events := actions.Events()
	if len(events) != 3 {
		t.Fatalf("len(events) = %d, want mouse move/down/up", len(events))
	}
}

func TestComputerToolRegistersAsReferenceToolset(t *testing.T) {
	actions := browser.NewPageActions()
	toolset := NewComputerTool(actions, 1024, 768)

	var toolCtx *llm.ToolContext
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("NewToolContext panic = %v, want Anthropic ComputerTool accepted as Toolset", r)
			}
		}()
		toolCtx = llm.NewToolContext([]interface{}{toolset})
	}()

	toolsets := toolCtx.Toolsets()
	if len(toolsets) != 1 || toolsets[0].ID() != "computer" {
		t.Fatalf("Toolsets() = %#v, want Anthropic computer toolset", toolsets)
	}

	result := llm.ExecuteFunctionCall(context.Background(), &llm.FunctionToolCall{
		Name:      "computer",
		CallID:    "call_computer",
		Arguments: `{"action":"left_click","coordinate":[10,20]}`,
	}, toolCtx)

	if result.RawError != nil {
		t.Fatalf("ExecuteFunctionCall RawError = %v, want nil", result.RawError)
	}
	if result.FncCallOut == nil || result.FncCallOut.IsError {
		t.Fatalf("FncCallOut = %#v, want successful computer output", result.FncCallOut)
	}
	events := actions.Events()
	if len(events) != 3 {
		t.Fatalf("len(events) = %d, want mouse move/down/up", len(events))
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
		{name: "missing coordinate", action: "left_click", args: map[string]interface{}{}, wantErr: "Missing required argument"},
		{name: "invalid coordinate", action: "left_click", args: map[string]interface{}{"coordinate": "10,20"}, wantErr: "invalid coordinate"},
		{name: "non numeric coordinate", action: "left_click", args: map[string]interface{}{"coordinate": []interface{}{"x", float64(20)}}, wantErr: "coordinates must be numbers"},
		{name: "missing text", action: "type", args: map[string]interface{}{}, wantErr: "Missing required argument"},
		{name: "invalid scroll amount", action: "scroll", args: map[string]interface{}{"coordinate": []interface{}{float64(10), float64(20)}, "scroll_amount": "lots"}, wantErr: "scroll_amount must be a number"},
		{name: "invalid hold duration", action: "hold_key", args: map[string]interface{}{"text": "Shift", "duration": "long"}, wantErr: "duration must be a number"},
		{name: "unknown", action: "unknown", args: map[string]interface{}{}, wantErr: "Unknown computer_use action"},
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

func TestComputerToolUsesReferenceErrorText(t *testing.T) {
	toolset := NewComputerTool(browser.NewPageActions(), 1024, 768)

	cases := []struct {
		name    string
		action  string
		args    map[string]interface{}
		wantErr string
	}{
		{name: "missing coordinate", action: "left_click", args: map[string]interface{}{}, wantErr: "Missing required argument: 'coordinate'"},
		{name: "missing start coordinate", action: "left_click_drag", args: map[string]interface{}{"coordinate": []interface{}{float64(10), float64(20)}}, wantErr: "Missing required argument: 'start_coordinate'"},
		{name: "missing text", action: "type", args: map[string]interface{}{}, wantErr: "Missing required argument: 'text'"},
		{name: "unknown action", action: "unknown", args: map[string]interface{}{}, wantErr: "Unknown computer_use action: 'unknown'"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := toolset.Execute(context.Background(), tc.action, tc.args)
			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("Execute error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestComputerToolAcceptsReferenceIntegerCoordinates(t *testing.T) {
	actions := browser.NewPageActions()
	toolset := NewComputerTool(actions, 1024, 768)

	_, err := toolset.Execute(context.Background(), "left_click", map[string]interface{}{
		"coordinate": []interface{}{10, 20},
	})
	if err != nil {
		t.Fatalf("Execute error = %v, want nil for integer coordinates", err)
	}

	events := actions.Events()
	if len(events) == 0 || events[0].X != 10 || events[0].Y != 20 {
		t.Fatalf("events = %#v, want first event at 10,20", events)
	}
}

func TestComputerToolAcceptsReferenceNumericStrings(t *testing.T) {
	actions := browser.NewPageActions()
	toolset := NewComputerTool(actions, 1024, 768)

	_, err := toolset.Execute(context.Background(), "scroll", map[string]interface{}{
		"coordinate":       []interface{}{"10", "20"},
		"scroll_amount":    "4",
		"scroll_direction": "up",
	})
	if err != nil {
		t.Fatalf("scroll Execute error = %v, want nil for numeric strings", err)
	}
	_, err = toolset.Execute(context.Background(), "hold_key", map[string]interface{}{
		"text":     "Shift",
		"duration": "1.25",
	})
	if err != nil {
		t.Fatalf("hold_key Execute error = %v, want nil for numeric duration string", err)
	}

	events := actions.Events()
	if len(events) < 3 {
		t.Fatalf("events = %#v, want scroll move/wheel and hold_key", events)
	}
	if events[0].Type != "mouse_move" || events[0].X != 10 || events[0].Y != 20 {
		t.Fatalf("scroll move event = %#v, want mouse_move at 10,20", events[0])
	}
	if events[1].Type != "mouse_wheel" || events[1].DeltaY != 480 {
		t.Fatalf("scroll wheel event = %#v, want up amount 4", events[1])
	}
	if events[2].Type != "hold_key" || events[2].Duration != 1.25 {
		t.Fatalf("hold_key event = %#v, want duration 1.25", events[2])
	}
}

func TestComputerToolUnknownScrollDirectionMatchesReferenceNoopWheel(t *testing.T) {
	actions := browser.NewPageActions()
	toolset := NewComputerTool(actions, 1024, 768)

	_, err := toolset.Execute(context.Background(), "scroll", map[string]interface{}{
		"coordinate":       []interface{}{float64(10), float64(20)},
		"scroll_amount":    float64(4),
		"scroll_direction": "diagonal",
	})
	if err != nil {
		t.Fatalf("scroll Execute error = %v, want nil for unknown scroll direction", err)
	}

	events := actions.Events()
	if len(events) < 2 {
		t.Fatalf("events = %#v, want mouse move and wheel", events)
	}
	if events[1].Type != "mouse_wheel" || events[1].DeltaX != 0 || events[1].DeltaY != 0 {
		t.Fatalf("wheel event = %#v, want zero delta for unknown scroll direction", events[1])
	}
}

func TestComputerToolNonStringScrollDirectionMatchesReferenceNoopWheel(t *testing.T) {
	actions := browser.NewPageActions()
	toolset := NewComputerTool(actions, 1024, 768)

	_, err := toolset.Execute(context.Background(), "scroll", map[string]interface{}{
		"coordinate":       []interface{}{float64(10), float64(20)},
		"scroll_amount":    float64(4),
		"scroll_direction": nil,
	})
	if err != nil {
		t.Fatalf("scroll Execute error = %v, want nil for non-string scroll direction", err)
	}

	events := actions.Events()
	if len(events) < 2 {
		t.Fatalf("events = %#v, want mouse move and wheel", events)
	}
	if events[1].Type != "mouse_wheel" || events[1].DeltaX != 0 || events[1].DeltaY != 0 {
		t.Fatalf("wheel event = %#v, want zero delta for non-string scroll direction", events[1])
	}
}

func TestComputerToolZeroScrollAmountMatchesReferenceNoopWheel(t *testing.T) {
	actions := browser.NewPageActions()
	toolset := NewComputerTool(actions, 1024, 768)

	_, err := toolset.Execute(context.Background(), "scroll", map[string]interface{}{
		"coordinate":       []interface{}{float64(10), float64(20)},
		"scroll_amount":    float64(0),
		"scroll_direction": "down",
	})
	if err != nil {
		t.Fatalf("scroll Execute error = %v, want nil for zero scroll amount", err)
	}

	events := actions.Events()
	if len(events) < 2 {
		t.Fatalf("events = %#v, want mouse move and wheel", events)
	}
	if events[1].Type != "mouse_wheel" || events[1].DeltaX != 0 || events[1].DeltaY != 0 {
		t.Fatalf("wheel event = %#v, want zero delta for explicit zero scroll amount", events[1])
	}
}

func TestComputerToolAcceptsReferenceTypedCoordinateSlices(t *testing.T) {
	actions := browser.NewPageActions()
	toolset := NewComputerTool(actions, 1024, 768)

	_, err := toolset.Execute(context.Background(), "mouse_move", map[string]interface{}{
		"coordinate": []float64{10, 20},
	})
	if err != nil {
		t.Fatalf("float coordinate Execute error = %v, want nil", err)
	}
	_, err = toolset.Execute(context.Background(), "mouse_move", map[string]interface{}{
		"coordinate": []string{"30", "40"},
	})
	if err != nil {
		t.Fatalf("string coordinate Execute error = %v, want nil", err)
	}

	events := actions.Events()
	if len(events) < 2 {
		t.Fatalf("events = %#v, want two mouse moves", events)
	}
	if events[0].X != 10 || events[0].Y != 20 {
		t.Fatalf("event[0] = %#v, want 10,20", events[0])
	}
	if events[1].X != 30 || events[1].Y != 40 {
		t.Fatalf("event[1] = %#v, want 30,40", events[1])
	}
}

func TestComputerToolPostActionDelayHonorsContextCancel(t *testing.T) {
	toolset := NewComputerTool(browser.NewPageActions(), 1024, 768)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err := toolset.Execute(ctx, "screenshot", map[string]interface{}{})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed >= postActionDelay {
		t.Fatalf("Execute elapsed = %v, want return before post-action delay %v", elapsed, postActionDelay)
	}
}

func TestComputerToolWaitActionUsesReferenceDelay(t *testing.T) {
	toolset := NewComputerTool(browser.NewPageActions(), 1024, 768)

	start := time.Now()
	_, err := toolset.Execute(context.Background(), "wait", map[string]interface{}{})

	if err != nil {
		t.Fatalf("Execute error = %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Fatalf("Execute elapsed = %v, want reference wait before screenshot", elapsed)
	}
}

func TestComputerToolHoldKeyUsesReferenceDuration(t *testing.T) {
	toolset := NewComputerTool(browser.NewPageActions(), 1024, 768)

	start := time.Now()
	_, err := toolset.Execute(context.Background(), "hold_key", map[string]interface{}{
		"text":     "Shift",
		"duration": float64(0.75),
	})

	if err != nil {
		t.Fatalf("Execute error = %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed < 700*time.Millisecond {
		t.Fatalf("Execute elapsed = %v, want reference hold duration before screenshot", elapsed)
	}
}

func TestComputerToolTypeTextUsesReferenceCharacterDelay(t *testing.T) {
	toolset := NewComputerTool(browser.NewPageActions(), 1024, 768)

	start := time.Now()
	_, err := toolset.Execute(context.Background(), "type", map[string]interface{}{
		"text": "abcdefghijklmnopqrst",
	})

	if err != nil {
		t.Fatalf("Execute error = %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed < 450*time.Millisecond {
		t.Fatalf("Execute elapsed = %v, want reference per-character type delay before screenshot", elapsed)
	}
}

func TestComputerToolDragUsesReferenceSettlingDelay(t *testing.T) {
	toolset := NewComputerTool(browser.NewPageActions(), 1024, 768)

	start := time.Now()
	_, err := toolset.Execute(context.Background(), "left_click_drag", map[string]interface{}{
		"start_coordinate": []interface{}{float64(1), float64(2)},
		"coordinate":       []interface{}{float64(10), float64(20)},
	})

	if err != nil {
		t.Fatalf("Execute error = %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed < 400*time.Millisecond {
		t.Fatalf("Execute elapsed = %v, want reference drag settling delay before screenshot", elapsed)
	}
}

func TestComputerToolCloseClosesPageActions(t *testing.T) {
	actions := browser.NewPageActions()
	toolset := NewComputerTool(actions, 1024, 768)

	toolset.Close()
	actions.TypeText("ignored")

	events := actions.Events()
	if len(events) != 1 || events[0].Type != "close" {
		t.Fatalf("events = %#v, want only close", events)
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
