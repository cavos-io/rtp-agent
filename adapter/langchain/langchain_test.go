package langchain

import (
	"testing"
)

func TestLangchainLLM_Initialization(t *testing.T) {
	l := NewLangchainLLM("apiKey", "model")
	if l == nil {
		t.Fatal("NewLangchainLLM returned nil")
	}
}
