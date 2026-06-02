package browser

import (
	"bytes"
	"sync"
)

const (
	MouseButtonLeft   = 0
	MouseButtonMiddle = 1
	MouseButtonRight  = 2
)

// PageActionEvent is the provider-agnostic input event emitted by PageActions.
type PageActionEvent struct {
	Type       string
	X          int
	Y          int
	Button     int
	Down       bool
	ClickCount int
	Modifiers  string
	DeltaX     int
	DeltaY     int
	Text       string
	Duration   float64
}

// PageActions records the typed input API expected by computer-use tools.
type PageActions struct {
	mu        sync.Mutex
	events    []PageActionEvent
	lastFrame []byte
	closed    bool
}

func NewPageActions() *PageActions {
	return &PageActions{}
}

func (p *PageActions) LeftClick(x, y int, modifiers string) {
	p.mouseMove(x, y)
	p.mouseClick(x, y, MouseButtonLeft, true, 1, modifiers)
	p.mouseClick(x, y, MouseButtonLeft, false, 1, modifiers)
}

func (p *PageActions) RightClick(x, y int) {
	p.mouseMove(x, y)
	p.mouseClick(x, y, MouseButtonRight, true, 1, "")
	p.mouseClick(x, y, MouseButtonRight, false, 1, "")
}

func (p *PageActions) DoubleClick(x, y int) {
	p.mouseMove(x, y)
	for count := 1; count <= 2; count++ {
		p.mouseClick(x, y, MouseButtonLeft, true, count, "")
		p.mouseClick(x, y, MouseButtonLeft, false, count, "")
	}
}

func (p *PageActions) TripleClick(x, y int) {
	p.mouseMove(x, y)
	for count := 1; count <= 3; count++ {
		p.mouseClick(x, y, MouseButtonLeft, true, count, "")
		p.mouseClick(x, y, MouseButtonLeft, false, count, "")
	}
}

func (p *PageActions) MiddleClick(x, y int) {
	p.mouseMove(x, y)
	p.mouseClick(x, y, MouseButtonMiddle, true, 1, "")
	p.mouseClick(x, y, MouseButtonMiddle, false, 1, "")
}

func (p *PageActions) MouseMove(x, y int) {
	p.mouseMove(x, y)
}

func (p *PageActions) LeftClickDrag(sx, sy, ex, ey int) {
	p.mouseMove(sx, sy)
	p.mouseClick(sx, sy, MouseButtonLeft, true, 1, "")
	p.mouseMove(ex, ey)
	p.mouseClick(ex, ey, MouseButtonLeft, false, 1, "")
}

func (p *PageActions) LeftMouseDown(x, y int) {
	p.mouseMove(x, y)
	p.mouseClick(x, y, MouseButtonLeft, true, 1, "")
}

func (p *PageActions) LeftMouseUp(x, y int) {
	p.mouseMove(x, y)
	p.mouseClick(x, y, MouseButtonLeft, false, 1, "")
}

func (p *PageActions) Scroll(x, y int, direction string, amount int) {
	if amount == 0 {
		amount = 3
	}
	pixels := amount * 120
	deltaX, deltaY := 0, 0
	switch direction {
	case "up":
		deltaY = pixels
	case "left":
		deltaX = pixels
	case "right":
		deltaX = -pixels
	default:
		deltaY = -pixels
	}
	p.mouseMove(x, y)
	p.record(PageActionEvent{Type: "mouse_wheel", X: x, Y: y, DeltaX: deltaX, DeltaY: deltaY})
}

func (p *PageActions) TypeText(text string) {
	p.record(PageActionEvent{Type: "type_text", Text: text})
}

func (p *PageActions) Key(key string) {
	p.record(PageActionEvent{Type: "key", Text: key})
}

func (p *PageActions) HoldKey(key string, duration float64) {
	p.record(PageActionEvent{Type: "hold_key", Text: key, Duration: duration})
}

func (p *PageActions) Wait() {
	p.record(PageActionEvent{Type: "wait"})
}

func (p *PageActions) LastFrame() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return bytes.Clone(p.lastFrame)
}

func (p *PageActions) SetLastFrame(frame []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastFrame = bytes.Clone(frame)
}

func (p *PageActions) Close() {
	p.record(PageActionEvent{Type: "close"})
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
}

func (p *PageActions) Events() []PageActionEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]PageActionEvent(nil), p.events...)
}

func (p *PageActions) mouseMove(x, y int) {
	p.record(PageActionEvent{Type: "mouse_move", X: x, Y: y})
}

func (p *PageActions) mouseClick(x, y, button int, down bool, clickCount int, modifiers string) {
	p.record(PageActionEvent{
		Type:       "mouse_click",
		X:          x,
		Y:          y,
		Button:     button,
		Down:       down,
		ClickCount: clickCount,
		Modifiers:  modifiers,
	})
}

func (p *PageActions) record(event PageActionEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed && event.Type != "close" {
		return
	}
	p.events = append(p.events, event)
}
