package math

import (
	"testing"
)

func TestExpFilter(t *testing.T) {
	f := NewExpFilter(0.5, 10.0)
	
	// First sample should be returned as is
	if res := f.Apply(1.0, 5.0); res != 5.0 {
		t.Errorf("Expected 5.0, got %v", res)
	}
	
	// Second sample: alpha^1 * 5.0 + (1-alpha^1) * 7.0 = 0.5*5 + 0.5*7 = 6.0
	if res := f.Apply(1.0, 7.0); res != 6.0 {
		t.Errorf("Expected 6.0, got %v", res)
	}
	
	// Max value capping
	if res := f.Apply(1.0, 20.0); res > 10.0 {
		t.Errorf("Expected max 10.0, got %v", res)
	}
	
	f.Reset(0.1)
	if f.alpha != 0.1 {
		t.Errorf("Expected alpha 0.1, got %v", f.alpha)
	}
	if f.Filtered() != -1.0 {
		t.Errorf("Expected filtered -1.0 after reset, got %v", f.Filtered())
	}
}

func TestMovingAverage(t *testing.T) {
	m := NewMovingAverage(3)
	
	m.AddSample(1.0)
	if avg := m.GetAvg(); avg != 1.0 {
		t.Errorf("Expected 1.0, got %v", avg)
	}
	
	m.AddSample(2.0)
	if avg := m.GetAvg(); avg != 1.5 {
		t.Errorf("Expected 1.5, got %v", avg)
	}
	
	m.AddSample(3.0)
	if avg := m.GetAvg(); avg != 2.0 {
		t.Errorf("Expected 2.0, got %v", avg)
	}
	
	// Should wrap around
	m.AddSample(4.0) // Hist: [4, 2, 3], Sum: 9, Count: 4, Size: 3 -> Avg: 3.0
	if avg := m.GetAvg(); avg != 3.0 {
		t.Errorf("Expected 3.0, got %v", avg)
	}
	
	m.Reset()
	if avg := m.GetAvg(); avg != 0 {
		t.Errorf("Expected 0 after reset, got %v", avg)
	}
	if m.Size() != 0 {
		t.Errorf("Expected size 0 after reset, got %d", m.Size())
	}
}
