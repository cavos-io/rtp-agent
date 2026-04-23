package browser

import (
	"context"
	"testing"
)

func TestBrowserAgent_Start(t *testing.T) {
	a := NewBrowserAgent("apiKey")
	err := a.Start(context.Background())
	if err != nil {
		t.Errorf("Start failed: %v", err)
	}
}

func TestPageActions(t *testing.T) {
	p := NewPageActions()
	p.LeftClick(0, 0, "")
	p.LastFrame()
	p.Close()
}
