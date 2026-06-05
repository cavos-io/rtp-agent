package telemetry

import "testing"

func TestUsageSummaryLLMTokenAliasesMatchPromptAndCompletion(t *testing.T) {
	summary := UsageSummary{LLMPromptTokens: 3, LLMCompletionTokens: 5}

	if got := summary.LLMInputTokens(); got != 3 {
		t.Fatalf("LLMInputTokens() = %d, want prompt tokens 3", got)
	}
	if got := summary.LLMOutputTokens(); got != 5 {
		t.Fatalf("LLMOutputTokens() = %d, want completion tokens 5", got)
	}

	summary.SetLLMInputTokens(7)
	summary.SetLLMOutputTokens(11)
	if summary.LLMPromptTokens != 7 {
		t.Fatalf("LLMPromptTokens = %d, want setter value 7", summary.LLMPromptTokens)
	}
	if summary.LLMCompletionTokens != 11 {
		t.Fatalf("LLMCompletionTokens = %d, want setter value 11", summary.LLMCompletionTokens)
	}
}

func TestUsageSummaryNilSettersAreNoops(t *testing.T) {
	var summary *UsageSummary
	summary.SetLLMInputTokens(7)
	summary.SetLLMOutputTokens(11)
}

func TestModelUsageCollectorAggregatesByProviderAndModel(t *testing.T) {
	collector := NewModelUsageCollector()
	collector.Collect(&LLMMetrics{
		PromptTokens:       3,
		PromptCachedTokens: 1,
		CompletionTokens:   5,
		Metadata:           &Metadata{ModelProvider: "openai", ModelName: "gpt"},
	})
	collector.Collect(&RealtimeModelMetrics{
		InputTokens:  7,
		OutputTokens: 11,
		Duration:     2.5,
		InputTokenDetails: InputTokenDetails{
			TextTokens:   4,
			AudioTokens:  2,
			ImageTokens:  1,
			CachedTokens: 3,
			CachedTokensDetails: &CachedTokenDetails{
				TextTokens:  1,
				AudioTokens: 1,
				ImageTokens: 1,
			},
		},
		OutputTokenDetails: OutputTokenDetails{TextTokens: 9, AudioTokens: 2},
		Metadata:           &Metadata{ModelProvider: "openai", ModelName: "gpt"},
	})
	collector.Collect(&TTSMetrics{
		InputTokens:     13,
		OutputTokens:    17,
		CharactersCount: 19,
		AudioDuration:   1.5,
		Metadata:        &Metadata{ModelProvider: "cartesia", ModelName: "sonic"},
	})
	collector.Collect(&STTMetrics{
		InputTokens:   23,
		OutputTokens:  29,
		AudioDuration: 3.5,
		Metadata:      &Metadata{ModelProvider: "deepgram", ModelName: "nova"},
	})
	collector.Collect(&InterruptionMetrics{
		NumRequests: 5,
		Metadata:    &Metadata{ModelProvider: "livekit", ModelName: "adaptive"},
	})

	usage := collector.Usage().ModelUsage
	if len(usage) != 4 {
		t.Fatalf("ModelUsage length = %d, want 4", len(usage))
	}

	llmUsage := findModelUsage[*LLMModelUsage](usage, "openai", "gpt")
	if llmUsage == nil {
		t.Fatal("missing LLMModelUsage for openai/gpt")
	}
	if llmUsage.InputTokens != 10 || llmUsage.InputCachedTokens != 4 || llmUsage.OutputTokens != 16 {
		t.Fatalf("LLM usage tokens = %#v, want input=10 cached=4 output=16", llmUsage)
	}
	if llmUsage.InputTextTokens != 4 || llmUsage.InputCachedTextTokens != 1 || llmUsage.InputAudioTokens != 2 || llmUsage.InputCachedAudioTokens != 1 || llmUsage.InputImageTokens != 1 || llmUsage.InputCachedImageTokens != 1 {
		t.Fatalf("LLM multimodal usage = %#v, want detailed input token counts", llmUsage)
	}
	if llmUsage.OutputTextTokens != 9 || llmUsage.OutputAudioTokens != 2 || llmUsage.SessionDuration != 2.5 {
		t.Fatalf("LLM output/session usage = %#v, want text=9 audio=2 duration=2.5", llmUsage)
	}

	ttsUsage := findModelUsage[*TTSModelUsage](usage, "cartesia", "sonic")
	if ttsUsage == nil || ttsUsage.InputTokens != 13 || ttsUsage.OutputTokens != 17 || ttsUsage.CharactersCount != 19 || ttsUsage.AudioDuration != 1.5 {
		t.Fatalf("TTS usage = %#v, want token/character/audio counts", ttsUsage)
	}

	sttUsage := findModelUsage[*STTModelUsage](usage, "deepgram", "nova")
	if sttUsage == nil || sttUsage.InputTokens != 23 || sttUsage.OutputTokens != 29 || sttUsage.AudioDuration != 3.5 {
		t.Fatalf("STT usage = %#v, want token/audio counts", sttUsage)
	}

	interruptionUsage := findModelUsage[*InterruptionModelUsage](usage, "livekit", "adaptive")
	if interruptionUsage == nil || interruptionUsage.TotalRequests != 5 {
		t.Fatalf("Interruption usage = %#v, want request count", interruptionUsage)
	}
}

func TestModelUsageCollectorFlattenReturnsCopies(t *testing.T) {
	collector := NewModelUsageCollector()
	collector.Collect(&LLMMetrics{
		PromptTokens: 3,
		Metadata:     &Metadata{ModelProvider: "openai", ModelName: "gpt"},
	})

	usage := collector.Flatten()
	llmUsage := findModelUsage[*LLMModelUsage](usage, "openai", "gpt")
	if llmUsage == nil {
		t.Fatal("missing LLMModelUsage")
	}
	llmUsage.InputTokens = 999

	fresh := findModelUsage[*LLMModelUsage](collector.Flatten(), "openai", "gpt")
	if fresh == nil || fresh.InputTokens != 3 {
		t.Fatalf("collector internal usage changed through flattened copy: %#v", fresh)
	}
}

func findModelUsage[T ModelUsage](usage []ModelUsage, provider string, model string) T {
	var zero T
	for _, item := range usage {
		candidate, ok := item.(T)
		if !ok {
			continue
		}
		switch typed := any(candidate).(type) {
		case *LLMModelUsage:
			if typed.Provider == provider && typed.Model == model {
				return candidate
			}
		case *TTSModelUsage:
			if typed.Provider == provider && typed.Model == model {
				return candidate
			}
		case *STTModelUsage:
			if typed.Provider == provider && typed.Model == model {
				return candidate
			}
		case *InterruptionModelUsage:
			if typed.Provider == provider && typed.Model == model {
				return candidate
			}
		}
	}
	return zero
}

func TestTTSMetricsCarriesTokenAndConnectionMetadata(t *testing.T) {
	metrics := &TTSMetrics{
		InputTokens:      3,
		OutputTokens:     5,
		AcquireTime:      0.25,
		ConnectionReused: true,
	}

	if metrics.InputTokens != 3 {
		t.Fatalf("InputTokens = %d, want 3", metrics.InputTokens)
	}
	if metrics.OutputTokens != 5 {
		t.Fatalf("OutputTokens = %d, want 5", metrics.OutputTokens)
	}
	if metrics.AcquireTime != 0.25 {
		t.Fatalf("AcquireTime = %v, want 0.25", metrics.AcquireTime)
	}
	if !metrics.ConnectionReused {
		t.Fatal("ConnectionReused = false, want true")
	}
}
