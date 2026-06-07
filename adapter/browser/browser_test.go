package browser

import (
	"reflect"
	"testing"
)

func TestBrowserPluginDownloadFilesIsGoPageActionsNoop(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.browser" {
		t.Fatalf("plugin title = %q, want rtp-agent.plugins.browser", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("plugin version = %q, want reference version", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.browser" {
		t.Fatalf("plugin package = %q, want rtp-agent.plugins.browser", PluginPackage)
	}
	if err := (Plugin{}).DownloadFiles(); err != nil {
		t.Fatalf("DownloadFiles() error = %v, want nil for Go PageActions adapter", err)
	}
}

func TestPageActionsRecordReferenceMouseActions(t *testing.T) {
	actions := NewPageActions()

	actions.LeftClick(10, 20, "shift")
	actions.RightClick(11, 21)
	actions.DoubleClick(12, 22)
	actions.TripleClick(13, 23)
	actions.MiddleClick(14, 24)
	actions.MouseMove(15, 25)
	actions.LeftClickDrag(1, 2, 16, 26)
	actions.LeftMouseDown(17, 27)
	actions.LeftMouseUp(18, 28)
	actions.Scroll(19, 29, "up", 4)

	got := actions.Events()
	want := []PageActionEvent{
		{Type: "mouse_move", X: 10, Y: 20},
		{Type: "mouse_click", X: 10, Y: 20, Button: MouseButtonLeft, Down: true, ClickCount: 1, Modifiers: "shift"},
		{Type: "mouse_click", X: 10, Y: 20, Button: MouseButtonLeft, Down: false, ClickCount: 1, Modifiers: "shift"},
		{Type: "mouse_move", X: 11, Y: 21},
		{Type: "mouse_click", X: 11, Y: 21, Button: MouseButtonRight, Down: true, ClickCount: 1},
		{Type: "mouse_click", X: 11, Y: 21, Button: MouseButtonRight, Down: false, ClickCount: 1},
		{Type: "mouse_move", X: 12, Y: 22},
		{Type: "mouse_click", X: 12, Y: 22, Button: MouseButtonLeft, Down: true, ClickCount: 1},
		{Type: "mouse_click", X: 12, Y: 22, Button: MouseButtonLeft, Down: false, ClickCount: 1},
		{Type: "mouse_click", X: 12, Y: 22, Button: MouseButtonLeft, Down: true, ClickCount: 2},
		{Type: "mouse_click", X: 12, Y: 22, Button: MouseButtonLeft, Down: false, ClickCount: 2},
		{Type: "mouse_move", X: 13, Y: 23},
		{Type: "mouse_click", X: 13, Y: 23, Button: MouseButtonLeft, Down: true, ClickCount: 1},
		{Type: "mouse_click", X: 13, Y: 23, Button: MouseButtonLeft, Down: false, ClickCount: 1},
		{Type: "mouse_click", X: 13, Y: 23, Button: MouseButtonLeft, Down: true, ClickCount: 2},
		{Type: "mouse_click", X: 13, Y: 23, Button: MouseButtonLeft, Down: false, ClickCount: 2},
		{Type: "mouse_click", X: 13, Y: 23, Button: MouseButtonLeft, Down: true, ClickCount: 3},
		{Type: "mouse_click", X: 13, Y: 23, Button: MouseButtonLeft, Down: false, ClickCount: 3},
		{Type: "mouse_move", X: 14, Y: 24},
		{Type: "mouse_click", X: 14, Y: 24, Button: MouseButtonMiddle, Down: true, ClickCount: 1},
		{Type: "mouse_click", X: 14, Y: 24, Button: MouseButtonMiddle, Down: false, ClickCount: 1},
		{Type: "mouse_move", X: 15, Y: 25},
		{Type: "mouse_move", X: 1, Y: 2},
		{Type: "mouse_click", X: 1, Y: 2, Button: MouseButtonLeft, Down: true, ClickCount: 1},
		{Type: "mouse_move", X: 16, Y: 26},
		{Type: "mouse_click", X: 16, Y: 26, Button: MouseButtonLeft, Down: false, ClickCount: 1},
		{Type: "mouse_move", X: 17, Y: 27},
		{Type: "mouse_click", X: 17, Y: 27, Button: MouseButtonLeft, Down: true, ClickCount: 1},
		{Type: "mouse_move", X: 18, Y: 28},
		{Type: "mouse_click", X: 18, Y: 28, Button: MouseButtonLeft, Down: false, ClickCount: 1},
		{Type: "mouse_move", X: 19, Y: 29},
		{Type: "mouse_wheel", X: 19, Y: 29, DeltaY: 480},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}

func TestPageActionsRecordKeyboardNavigationAndFrames(t *testing.T) {
	actions := NewPageActions()

	actions.TypeText("Hi")
	actions.Key("ctrl+l")
	actions.HoldKey("shift", 1.25)
	actions.Wait()
	actions.SetLastFrame([]byte("png"))
	frame := actions.LastFrame()
	frame[0] = 'x'
	actions.Close()
	actions.TypeText("ignored")

	got := actions.Events()
	want := []PageActionEvent{
		{Type: "type_text", Text: "Hi"},
		{Type: "key", Text: "ctrl+l"},
		{Type: "hold_key", Text: "shift", Duration: 1.25},
		{Type: "wait"},
		{Type: "close"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
	if string(actions.LastFrame()) != "png" {
		t.Fatalf("LastFrame = %q, want defensive copy", string(actions.LastFrame()))
	}
}

func TestPageActionsEventsReturnsCopyAndCloseIsTerminal(t *testing.T) {
	actions := NewPageActions()

	actions.TypeText("first")
	events := actions.Events()
	if len(events) != 1 {
		t.Fatalf("len(Events()) = %d, want 1", len(events))
	}
	events[0].Text = "mutated"

	fresh := actions.Events()
	if fresh[0].Text != "first" {
		t.Fatalf("Events() returned mutable backing storage: %q", fresh[0].Text)
	}

	actions.Close()
	actions.Close()
	actions.Key("ignored")
	actions.LeftClick(10, 20, "")

	got := actions.Events()
	want := []PageActionEvent{
		{Type: "type_text", Text: "first"},
		{Type: "close"},
		{Type: "close"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events after close = %#v, want %#v", got, want)
	}
}
