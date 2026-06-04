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
