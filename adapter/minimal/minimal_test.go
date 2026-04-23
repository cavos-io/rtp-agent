package minimal

import (
	"testing"
)

func TestMinimalLLM_Initialization(t *testing.T) {
	l := NewMinimalLLM("apiKey", "model")
	if l == nil {
		t.Fatal("NewMinimalLLM returned nil")
	}
}
