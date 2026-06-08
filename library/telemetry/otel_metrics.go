package telemetry

import (
	"context"
	"strconv"

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
	case *InterruptionMetrics:
		attrs := otelModelAttrs(ev.Metadata)
		addInt64Counter(ctx, meter, "lk.agents.usage.interruption_num_requests", int64(ev.NumRequests), attrs)
	}
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
