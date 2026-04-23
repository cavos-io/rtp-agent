package anthropic

import (
	"context"
	"testing"
)

type mockPageActions struct {
	clicked bool
	typed   string
}

func (m *mockPageActions) LeftClick(x, y int, modifiers string) { m.clicked = true }
func (m *mockPageActions) RightClick(x, y int)                   {}
func (m *mockPageActions) DoubleClick(x, y int)                  {}
func (m *mockPageActions) TypeText(text string)                  { m.typed = text }
func (m *mockPageActions) Key(key string)                        {}
func (m *mockPageActions) Wait()                                 {}
func (m *mockPageActions) LastFrame() []byte                     { return []byte("fake frame") }

func TestComputerTool(t *testing.T) {
	mock := &mockPageActions{}
	ct := NewComputerTool(mock, 1024, 768)
	
	// Test click
	_, err := ct.Execute(context.Background(), "left_click", map[string]interface{}{
		"coordinate": []interface{}{100.0, 200.0},
	})
	if err != nil {
		t.Fatalf("LeftClick failed: %v", err)
	}
	if !mock.clicked {
		t.Error("Expected click to be called")
	}
	
	// Test type
	_, err = ct.Execute(context.Background(), "type", map[string]interface{}{
		"text": "hello",
	})
	if err != nil {
		t.Fatalf("Type failed: %v", err)
	}
	if mock.typed != "hello" {
		t.Errorf("Expected hello, got %s", mock.typed)
	}
	
	// Test schema
	tool := ct.Tools()[0].(interface{ ProviderSchema(string) map[string]any })
	schema := tool.ProviderSchema("anthropic")
	if schema["display_width_px"] != 1024 {
		t.Errorf("Expected width 1024, got %v", schema["display_width_px"])
	}
}
