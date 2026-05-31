package openai

import (
	"testing"

	"github.com/cavos-io/conversation-worker/core/llm"
)

func TestBuildOpenAIChatCompletionRequestAppliesExtraParams(t *testing.T) {
	req := buildOpenAIChatCompletionRequest("gpt-4o", llm.NewChatContext(), &llm.ChatOptions{
		ParallelToolCalls: true,
		ExtraParams: map[string]any{
			"temperature":           0.7,
			"top_p":                 0.8,
			"presence_penalty":      0.1,
			"frequency_penalty":     0.2,
			"n":                     2,
			"max_completion_tokens": 128,
			"reasoning_effort":      "low",
			"metadata":              map[string]any{"trace": "abc"},
		},
	})

	if req.Temperature != 0.7 {
		t.Fatalf("Temperature = %v, want 0.7", req.Temperature)
	}
	if req.TopP != 0.8 {
		t.Fatalf("TopP = %v, want 0.8", req.TopP)
	}
	if req.PresencePenalty != 0.1 {
		t.Fatalf("PresencePenalty = %v, want 0.1", req.PresencePenalty)
	}
	if req.FrequencyPenalty != 0.2 {
		t.Fatalf("FrequencyPenalty = %v, want 0.2", req.FrequencyPenalty)
	}
	if req.N != 2 {
		t.Fatalf("N = %d, want 2", req.N)
	}
	if req.MaxCompletionTokens != 128 {
		t.Fatalf("MaxCompletionTokens = %d, want 128", req.MaxCompletionTokens)
	}
	if req.ReasoningEffort != "low" {
		t.Fatalf("ReasoningEffort = %q, want low", req.ReasoningEffort)
	}
	if req.Metadata["trace"] != "abc" {
		t.Fatalf("Metadata[trace] = %q, want abc", req.Metadata["trace"])
	}
}
