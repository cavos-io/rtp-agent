package agent

import (
	"context"
	"math"
	"time"

	"github.com/cavos-io/rtp-agent/library/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func (ma *MultimodalAgent) rememberRealtimeSpan(id string, span trace.Span) {
	if id == "" {
		return
	}
	ma.mu.Lock()
	defer ma.mu.Unlock()
	if ma.traceClosed {
		return
	}
	if ma.realtimeSpans == nil {
		ma.realtimeSpans = make(map[string]trace.Span)
	}
	if _, ok := ma.realtimeSpans[id]; !ok {
		ma.realtimeSpanIDs = append(ma.realtimeSpanIDs, id)
	}
	ma.realtimeSpans[id] = span
	for len(ma.realtimeSpanIDs) > 100 {
		delete(ma.realtimeSpans, ma.realtimeSpanIDs[0])
		ma.realtimeSpanIDs = ma.realtimeSpanIDs[1:]
	}
}

func (ma *MultimodalAgent) recordRealtimeMetrics(metrics telemetry.AgentMetrics) {
	m, ok := metrics.(*telemetry.RealtimeModelMetrics)
	if !ok || m == nil {
		return
	}
	ma.mu.Lock()
	defer ma.mu.Unlock()
	span := ma.realtimeSpans[m.RequestID]
	delete(ma.realtimeSpans, m.RequestID)
	for i, id := range ma.realtimeSpanIDs {
		if id == m.RequestID {
			ma.realtimeSpanIDs = append(ma.realtimeSpanIDs[:i], ma.realtimeSpanIDs[i+1:]...)
			break
		}
	}
	ctx := ma.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if span == nil {
		return
	}
	if !span.IsRecording() {
		ctx = trace.ContextWithSpan(ctx, span)
		_, child := telemetry.StartSpan(ctx, "realtime_metrics")
		defer child.End()
		span = child
	}
	attrs := []attribute.KeyValue{
		attribute.String(telemetry.AttrGenAIOperationName, "generate_content"), attribute.String("lk.realtime_model_metrics", marshalTraceJSON(m)),
		attribute.Int(telemetry.AttrGenAIUsageInputTokens, m.InputTokens), attribute.Int(telemetry.AttrGenAIUsageOutputTokens, m.OutputTokens), attribute.Int(telemetry.AttrGenAIUsageCacheRead, m.InputTokenDetails.CachedTokens),
		attribute.Int("gen_ai.usage.text.input_tokens", m.InputTokenDetails.TextTokens), attribute.Int("gen_ai.usage.audio.input_tokens", m.InputTokenDetails.AudioTokens), attribute.Int("gen_ai.usage.image.input_tokens", m.InputTokenDetails.ImageTokens), attribute.Int("gen_ai.usage.text.output_tokens", m.OutputTokenDetails.TextTokens), attribute.Int("gen_ai.usage.audio.output_tokens", m.OutputTokenDetails.AudioTokens),
		attribute.Int("gen_ai.usage.input_text_tokens", m.InputTokenDetails.TextTokens), attribute.Int("gen_ai.usage.input_audio_tokens", m.InputTokenDetails.AudioTokens), attribute.Int(telemetry.AttrGenAIInputCachedTokens, m.InputTokenDetails.CachedTokens), attribute.Int("gen_ai.usage.output_text_tokens", m.OutputTokenDetails.TextTokens), attribute.Int("gen_ai.usage.output_audio_tokens", m.OutputTokenDetails.AudioTokens),
	}
	if cached := m.InputTokenDetails.CachedTokensDetails; cached != nil {
		attrs = append(attrs, attribute.Int("gen_ai.usage.text.cache_read.input_tokens", cached.TextTokens), attribute.Int("gen_ai.usage.audio.cache_read.input_tokens", cached.AudioTokens), attribute.Int("gen_ai.usage.image.cache_read.input_tokens", cached.ImageTokens))
	}
	validTTFT := m.TTFT >= 0 && !math.IsNaN(m.TTFT) && !math.IsInf(m.TTFT, 0)
	if validTTFT && !m.Timestamp.IsZero() {
		attrs = append(attrs, attribute.String("langfuse.completion_start_time", m.Timestamp.Add(time.Duration(m.TTFT*float64(time.Second))).UTC().Format(time.RFC3339Nano)))
	}

	if m.Metadata != nil {
		attrs = append(attrs, attribute.String(telemetry.AttrGenAIProviderName, normalizeGenAIProvider(m.Metadata.ModelProvider)), attribute.String(telemetry.AttrGenAIRequestModel, m.Metadata.ModelName), attribute.String(telemetry.AttrGenAIResponseModel, m.Metadata.ModelName))
	}
	if m.RequestID != "" {
		attrs = append(attrs, attribute.String(telemetry.AttrGenAIResponseID, m.RequestID))
	}
	if validTTFT {
		attrs = append(attrs, attribute.Float64(telemetry.AttrGenAIResponseTTFC, m.TTFT))
	}
	span.SetAttributes(attrs...)
}

func (ma *MultimodalAgent) endRealtimeSpan(span trace.Span) {
	ma.mu.Lock()
	defer ma.mu.Unlock()
	span.End()
}
