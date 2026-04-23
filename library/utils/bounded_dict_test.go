package utils

import (
	"testing"
)

func TestBoundedDict(t *testing.T) {
	d := NewBoundedDict[string, int](2)

	d.Set("a", 1)
	d.Set("b", 2)
	
	if v, ok := d.Get("a"); !ok || v != 1 {
		t.Errorf("Expected 1, got %v", v)
	}

	// Should evict "b" since "a" was accessed last
	d.Set("c", 3)

	if _, ok := d.Get("b"); ok {
		t.Errorf("Expected 'b' to be evicted")
	}
	if v, ok := d.Get("a"); !ok || v != 1 {
		t.Errorf("Expected 'a' to be present")
	}
	if v, ok := d.Get("c"); !ok || v != 3 {
		t.Errorf("Expected 'c' to be present")
	}
}
