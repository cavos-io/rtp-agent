package telemetry

import (
	"testing"
)

func TestUsageCollector(t *testing.T) {
	c := NewUsageCollector()

	// Add LLM metrics
	c.Collect(&LLMMetrics{
		PromptTokens:     100,
		CompletionTokens: 50,
	})

	// Add TTS metrics
	c.Collect(&TTSMetrics{
		CharactersCount: 200,
		AudioDuration:   10.5,
	})

	summary := c.GetSummary()
	if summary.LLMPromptTokens != 100 {
		t.Errorf("Expected 100 prompt tokens, got %d", summary.LLMPromptTokens)
	}
	if summary.LLMCompletionTokens != 50 {
		t.Errorf("Expected 50 completion tokens, got %d", summary.LLMCompletionTokens)
	}
	if summary.TTSCharactersCount != 200 {
		t.Errorf("Expected 200 chars, got %d", summary.TTSCharactersCount)
	}
	if summary.TTSAudioDuration != 10.5 {
		t.Errorf("Expected 10.5 duration, got %v", summary.TTSAudioDuration)
	}
}

func TestMetricRegistry(t *testing.T) {
	r := NewMetricRegistry()
	labels := MetricLabels{AgentName: "agent1", RoomName: "room1", ParticipantIdentity: "p1"}
	
	c1 := r.GetUsageCollector(labels)
	c2 := r.GetUsageCollector(labels)

	if c1 != c2 {
		t.Errorf("Expected same collector instance for same labels")
	}

	labels2 := MetricLabels{AgentName: "agent1", RoomName: "room1", ParticipantIdentity: "p2"}
	c3 := r.GetUsageCollector(labels2)
	if c1 == c3 {
		t.Errorf("Expected different collector instance for different labels")
	}
}
