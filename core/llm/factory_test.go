package llm

import (
	"context"
	"testing"
)

type factoryMockLLM struct{}

func (m *factoryMockLLM) Chat(ctx context.Context, chatCtx *ChatContext, opts ...ChatOption) (LLMStream, error) {
	return nil, nil
}

func TestLLMFactory(t *testing.T) {
	Register("test-provider", func(model string) (LLM, error) {
		if model == "test-model" {
			return &factoryMockLLM{}, nil
		}
		return nil, nil
	})
	
	llm, err := FromModelString("test-provider:test-model")
	if err != nil {
		t.Fatalf("FromModelString failed: %v", err)
	}
	if llm == nil {
		t.Fatal("Expected LLM instance, got nil")
	}
	
	_, err = FromModelString("invalid")
	if err == nil {
		t.Error("Expected error for invalid model string, got nil")
	}
	
	_, err = FromModelString("unknown:model")
	if err == nil {
		t.Error("Expected error for unknown provider, got nil")
	}
}
