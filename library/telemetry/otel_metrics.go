package telemetry

import (
	"context"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const otelMetricsMeterName = "livekit-agents"

func CollectOTelUsage(metrics AgentMetrics) {
	CollectOTelUsageWithContext(context.Background(), metrics)
}

func CollectOTelUsageWithContext(ctx context.Context, metrics AgentMetrics) {
	if metrics == nil {
		return
	}

	meter := otel.Meter(otelMetricsMeterName)
	switch ev := metrics.(type) {
	case *LLMMetrics:
		attrs := otelModelAttrs(ev.Metadata)
		addInt64Counter(ctx, meter, "lk.agents.usage.llm_input_tokens", int64(ev.PromptTokens), attrs)
		addInt64Counter(ctx, meter, "lk.agents.usage.llm_input_cached_tokens", int64(ev.PromptCachedTokens), attrs)
		addInt64Counter(ctx, meter, "lk.agents.usage.llm_output_tokens", int64(ev.CompletionTokens), attrs)
	case *RealtimeModelMetrics:
		attrs := otelModelAttrs(ev.Metadata)
		addInt64Counter(ctx, meter, "lk.agents.usage.llm_input_tokens", int64(ev.InputTokens), attrs)
		addInt64Counter(ctx, meter, "lk.agents.usage.llm_input_cached_tokens", int64(ev.InputTokenDetails.CachedTokens), attrs)
		addInt64Counter(ctx, meter, "lk.agents.usage.llm_output_tokens", int64(ev.OutputTokens), attrs)
		addInt64Counter(ctx, meter, "lk.agents.usage.llm_input_audio_tokens", int64(ev.InputTokenDetails.AudioTokens), attrs)
		addInt64Counter(ctx, meter, "lk.agents.usage.llm_input_text_tokens", int64(ev.InputTokenDetails.TextTokens), attrs)
		addInt64Counter(ctx, meter, "lk.agents.usage.llm_output_audio_tokens", int64(ev.OutputTokenDetails.AudioTokens), attrs)
		addInt64Counter(ctx, meter, "lk.agents.usage.llm_output_text_tokens", int64(ev.OutputTokenDetails.TextTokens), attrs)
		addFloat64Counter(ctx, meter, "lk.agents.usage.llm_session_duration", ev.SessionDuration, attrs)
		recordAcquireTime(ctx, meter, ev.AcquireTime, ev.ConnectionReused, ev.Metadata)
	case *TTSMetrics:
		attrs := otelModelAttrs(ev.Metadata)
		addInt64Counter(ctx, meter, "lk.agents.usage.tts_characters", int64(ev.CharactersCount), attrs)
		addFloat64Counter(ctx, meter, "lk.agents.usage.tts_audio_duration", ev.AudioDuration, attrs)
		recordAcquireTime(ctx, meter, ev.AcquireTime, ev.ConnectionReused, ev.Metadata)
	case *STTMetrics:
		attrs := otelModelAttrs(ev.Metadata)
		addFloat64Counter(ctx, meter, "lk.agents.usage.stt_audio_duration", ev.AudioDuration, attrs)
		recordAcquireTime(ctx, meter, ev.AcquireTime, ev.ConnectionReused, ev.Metadata)
	case *InterruptionMetrics:
		attrs := otelModelAttrs(ev.Metadata)
		addInt64Counter(ctx, meter, "lk.agents.usage.interruption_num_requests", int64(ev.NumRequests), attrs)
	}
}

func RecordOTelTurnMetrics(report map[string]any) {
	RecordOTelTurnMetricsWithContext(context.Background(), report)
}

func RecordOTelTurnMetricsWithContext(ctx context.Context, report map[string]any) {
	if len(report) == 0 {
		return
	}

	meter := otel.Meter(otelMetricsMeterName)
	llmAttrs := otelReportMetadataAttrs(report["llm_metadata"])
	ttsAttrs := otelReportMetadataAttrs(report["tts_metadata"])
	sttAttrs := otelReportMetadataAttrs(report["stt_metadata"])

	recordReportFloat64Histogram(ctx, meter, "lk.agents.turn.e2e_latency", report, "e2e_latency", llmAttrs)
	recordReportFloat64Histogram(ctx, meter, "lk.agents.turn.llm_ttft", report, "llm_node_ttft", llmAttrs)
	recordReportFloat64Histogram(ctx, meter, "lk.agents.turn.tts_ttfb", report, "tts_node_ttfb", ttsAttrs)
	recordReportFloat64Histogram(ctx, meter, "lk.agents.turn.transcription_delay", report, "transcription_delay", sttAttrs)
	recordReportFloat64Histogram(ctx, meter, "lk.agents.turn.end_of_turn_delay", report, "end_of_turn_delay", sttAttrs)
	recordReportFloat64Histogram(ctx, meter, "lk.agents.turn.on_user_turn_completed_delay", report, "on_user_turn_completed_delay", sttAttrs)
}

func otelModelAttrs(metadata *Metadata) []attribute.KeyValue {
	if metadata == nil {
		return nil
	}

	attrs := make([]attribute.KeyValue, 0, 2)
	if metadata.ModelProvider != "" {
		attrs = append(attrs, attribute.String("model_provider", metadata.ModelProvider))
	}
	if metadata.ModelName != "" {
		attrs = append(attrs, attribute.String("model_name", metadata.ModelName))
	}
	return attrs
}

func otelReportMetadataAttrs(value any) []attribute.KeyValue {
	metadata, ok := value.(map[string]any)
	if !ok {
		return nil
	}

	attrs := make([]attribute.KeyValue, 0, 2)
	if provider, ok := metadata["model_provider"].(string); ok && provider != "" {
		attrs = append(attrs, attribute.String("model_provider", provider))
	}
	if model, ok := metadata["model_name"].(string); ok && model != "" {
		attrs = append(attrs, attribute.String("model_name", model))
	}
	return attrs
}

func reportFloat(report map[string]any, key string) (float64, bool) {
	switch value := report[key].(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	case time.Duration:
		return value.Seconds(), true
	default:
		return 0, false
	}
}

func addInt64Counter(ctx context.Context, meter metric.Meter, name string, value int64, attrs []attribute.KeyValue) {
	if value == 0 {
		return
	}
	counter, err := meter.Int64Counter(name)
	if err != nil {
		return
	}
	counter.Add(ctx, value, metric.WithAttributes(attrs...))
}

func addFloat64Counter(ctx context.Context, meter metric.Meter, name string, value float64, attrs []attribute.KeyValue) {
	if value == 0 {
		return
	}
	counter, err := meter.Float64Counter(name)
	if err != nil {
		return
	}
	counter.Add(ctx, value, metric.WithAttributes(attrs...))
}

func recordFloat64Histogram(ctx context.Context, meter metric.Meter, name string, value float64, ok bool, attrs []attribute.KeyValue) {
	if !ok {
		return
	}
	histogram, err := meter.Float64Histogram(name, metric.WithUnit("s"))
	if err != nil {
		return
	}
	histogram.Record(ctx, value, metric.WithAttributes(attrs...))
}

func recordReportFloat64Histogram(ctx context.Context, meter metric.Meter, name string, report map[string]any, key string, attrs []attribute.KeyValue) {
	value, ok := reportFloat(report, key)
	recordFloat64Histogram(ctx, meter, name, value, ok, attrs)
}

func recordAcquireTime(ctx context.Context, meter metric.Meter, acquireTime float64, connectionReused bool, metadata *Metadata) {
	if acquireTime <= 0 {
		return
	}
	attrs := otelModelAttrs(metadata)
	attrs = append(attrs, attribute.String("connection_reused", strconv.FormatBool(connectionReused)))
	histogram, err := meter.Float64Histogram("lk.agents.connection.acquire_time", metric.WithUnit("s"))
	if err != nil {
		return
	}
	histogram.Record(ctx, acquireTime, metric.WithAttributes(attrs...))
}
