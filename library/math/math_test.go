package math

import (
	"testing"
)

func TestExpFilter(t *testing.T) {
	f := NewExpFilter(0.5, 100.0)
	
	// First sample should be the initial value
	if f.Apply(1.0, 10.0) != 10.0 {
		t.Errorf("Expected 10.0, got %v", f.Filtered())
	}

	// Second sample with alpha 0.5
	// 0.5*10 + 0.5*20 = 15
	if f.Apply(1.0, 20.0) != 15.0 {
		t.Errorf("Expected 15.0, got %v", f.Filtered())
	}

	// Max value check
	f.Apply(1.0, 200.0)
	if f.Filtered() > 100.0 {
		t.Errorf("Expected max 100.0, got %v", f.Filtered())
	}
}

func TestMovingAverage(t *testing.T) {
	m := NewMovingAverage(3)
	
	m.AddSample(10)
	if m.GetAvg() != 10 {
		t.Errorf("Expected 10, got %v", m.GetAvg())
	}

	m.AddSample(20)
	if m.GetAvg() != 15 {
		t.Errorf("Expected 15, got %v", m.GetAvg())
	}

	m.AddSample(30)
	if m.GetAvg() != 20 {
		t.Errorf("Expected 20, got %v", m.GetAvg())
	}

	// Should slide out 10
	m.AddSample(40)
	if m.GetAvg() != 30 { // (20+30+40)/3 = 30
		t.Errorf("Expected 30, got %v", m.GetAvg())
	}
}
