package langchain

import (
	"testing"

	corellm "github.com/cavos-io/rtp-agent/core/llm"
)

func TestLLMConstructorContract(t *testing.T) {
	var _ corellm.LLM = (*LLM)(nil)
	provider := NewLLM("key", "")
	if provider.inner == nil {
		t.Fatal("NewLLM() inner provider = nil")
	}
	if provider.Provider() != "LangChain" {
		t.Fatalf("Provider() = %q, want LangChain", provider.Provider())
	}
}
