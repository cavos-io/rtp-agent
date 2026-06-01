package inference

import (
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestChatOptionsForModelDropsUnsupportedReasoningParams(t *testing.T) {
	options := chatOptionsForModel("openai/gpt-5", []llm.ChatOption{
		llm.WithExtraParams(map[string]any{
			"temperature":           0.7,
			"top_p":                 0.8,
			"presence_penalty":      0.1,
			"frequency_penalty":     0.2,
			"n":                     2,
			"reasoning_effort":      "low",
			"max_completion_tokens": 128,
		}),
	})

	if _, ok := options.ExtraParams["temperature"]; ok {
		t.Fatal("ExtraParams contains temperature, want dropped for gpt-5")
	}
	if _, ok := options.ExtraParams["top_p"]; ok {
		t.Fatal("ExtraParams contains top_p, want dropped for gpt-5")
	}
	if _, ok := options.ExtraParams["presence_penalty"]; ok {
		t.Fatal("ExtraParams contains presence_penalty, want dropped for gpt-5")
	}
	if options.ExtraParams["reasoning_effort"] != "low" {
		t.Fatalf("ExtraParams[reasoning_effort] = %v, want low", options.ExtraParams["reasoning_effort"])
	}
	if options.ExtraParams["max_completion_tokens"] != 128 {
		t.Fatalf("ExtraParams[max_completion_tokens] = %v, want 128", options.ExtraParams["max_completion_tokens"])
	}
}

func TestChatOptionsForModelKeepsParamsForNonReasoningModels(t *testing.T) {
	options := chatOptionsForModel("openai/gpt-4o", []llm.ChatOption{
		llm.WithExtraParams(map[string]any{
			"temperature": 0.7,
		}),
	})

	if options.ExtraParams["temperature"] != 0.7 {
		t.Fatalf("ExtraParams[temperature] = %v, want 0.7", options.ExtraParams["temperature"])
	}
}
