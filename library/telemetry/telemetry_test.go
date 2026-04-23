package telemetry

import (
	"context"
	"testing"
	"time"
)

func TestLogMetrics_NoPanic(t *testing.T) {
	// Verify that various metrics don't panic when logged
	metrics := []AgentMetrics{
		&LLMMetrics{
			TTFT:         0.5,
			PromptTokens: 100,
			Metadata:     &Metadata{ModelName: "gpt-4o", ModelProvider: "openai"},
		},
		&RealtimeModelMetrics{
			TTFT:        0.2,
			InputTokens: 50,
			InputTokenDetails: InputTokenDetails{
				CachedTokens: 10,
				CachedTokensDetails: &CachedTokenDetails{TextTokens: 10},
			},
			Metadata: &Metadata{ModelName: "gpt-4o-realtime", ModelProvider: "openai"},
		},
		&TTSMetrics{
			TTFB:          0.3,
			AudioDuration: 5.0,
			Metadata:      &Metadata{ModelName: "tts-1", ModelProvider: "openai"},
		},
		&STTMetrics{
			AudioDuration: 2.0,
			Metadata:      &Metadata{ModelName: "whisper-1", ModelProvider: "openai"},
		},
		&EOUMetrics{
			EndOfUtteranceDelay: 0.1,
			TranscriptionDelay:  0.05,
		},
	}

	for _, m := range metrics {
		LogMetrics(m)
	}
}

func TestRecordChatEvent_NoPanic(t *testing.T) {
	// Test when ChatLogger is nil
	ChatLogger = nil
	RecordChatEvent(context.Background(), "test", "hello", nil)
}

func TestShutdownLoggerProvider_NoPanic(t *testing.T) {
	loggerProvider = nil
	err := ShutdownLoggerProvider(context.Background())
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestInitLoggerProvider_InvalidEndpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	
	// This might not fail immediately because it just creates the provider
	_ = InitLoggerProvider(ctx, "http://invalid", nil)
	_ = ShutdownLoggerProvider(ctx)
}
