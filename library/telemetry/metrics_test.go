package telemetry

import "testing"

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
