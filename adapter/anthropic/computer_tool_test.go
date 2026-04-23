package anthropic

import (
	"context"
	"testing"

)

type mockPageActions struct {
	lastFrame []byte
}

func (m *mockPageActions) LeftClick(x, y int, modifiers string) {}
func (m *mockPageActions) RightClick(x, y int)                 {}
func (m *mockPageActions) DoubleClick(x, y int)                {}
func (m *mockPageActions) TypeText(text string)                {}
func (m *mockPageActions) Key(key string)                      {}
func (m *mockPageActions) Wait()                               {}
func (m *mockPageActions) LastFrame() []byte                   { return m.lastFrame }

func TestComputerTool_Execute(t *testing.T) {
	mock := &mockPageActions{lastFrame: []byte("screenshot data")}
	c := NewComputerTool(mock, 1024, 768)

	ctx := context.Background()
	
	// Test type action
	res, err := c.Execute(ctx, "type", map[string]interface{}{"text": "hello"})
	if err != nil {
		t.Fatalf("Execute type failed: %v", err)
	}
	if len(res) == 0 || res[0]["type"] != "image" {
		t.Errorf("Expected image result, got %v", res)
	}

	// Test click action with coordinates
	res, err = c.Execute(ctx, "left_click", map[string]interface{}{
		"coordinate": []interface{}{float64(100), float64(200)},
	})
	if err != nil {
		t.Fatalf("Execute left_click failed: %v", err)
	}
	
	// Test invalid action
	_, err = c.Execute(ctx, "invalid", nil)
	if err == nil {
		t.Error("Expected error for invalid action, got nil")
	}
}

func TestComputerUseTool_Properties(t *testing.T) {
	width, height := 1920, 1080
	tool := newComputerUseTool(width, height)
	
	if tool.Name() != "computer_use" {
		t.Errorf("Expected 'computer_use', got %q", tool.Name())
	}
	
	schema := tool.(interface {
		ProviderSchema(string) map[string]any
	}).ProviderSchema("anthropic")
	if schema["display_width_px"] != width {
		t.Errorf("Expected width %d, got %v", width, schema["display_width_px"])
	}
}
