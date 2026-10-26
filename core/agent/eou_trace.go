package agent

import (
	"context"
	"time"

	"github.com/cavos-io/rtp-agent/library/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type eouWaitTrace struct {
	span, detection trace.Span
	started, floor  time.Time
	rearms          int
}

func (a *AgentActivity) beginEOUWait(ctx context.Context, info EndOfTurnInfo, sources ...string) context.Context {
	if a.Session != nil {
		anchor := time.Now()
		if info.StartedSpeakingAt != nil {
			anchor = unixSecondsToTime(*info.StartedSpeakingAt)
		} else if info.StoppedSpeakingAt != nil {
			anchor = unixSecondsToTime(*info.StoppedSpeakingAt)
		}
		ctx = trace.ContextWithSpan(ctx, a.Session.ensureUserTurnSpan(ctx, anchor))
	}
	a.eouMu.Lock()
	defer a.eouMu.Unlock()
	now := time.Now()
	source := "vad"
	if a.hasSTTModel() {
		source = "stt"
	}
	if len(sources) > 0 {
		source = sources[0]
	}
	if w := a.eouTrace; w != nil && w.span.IsRecording() {
		w.rearms++
		w.floor = now
		w.span.AddEvent("rearmed", trace.WithTimestamp(now), trace.WithAttributes(attribute.String("lk.eou.source", source)))
		w.span.SetAttributes(attribute.String("lk.eou.source", source))
		return trace.ContextWithSpan(ctx, w.span)
	}
	start := now
	if info.StoppedSpeakingAt != nil {
		start = unixSecondsToTime(*info.StoppedSpeakingAt)
		if start.After(now) {
			start = now
		}
	}
	ctx, span := telemetry.StartSpan(ctx, "eou_wait", trace.WithTimestamp(start), trace.WithAttributes(attribute.String("lk.eou.source", source), attribute.Float64(telemetry.AttrEOUDelay, a.minEndpointingDelay())))
	a.eouTrace = &eouWaitTrace{span: span, started: start}
	return ctx
}

func (a *AgentActivity) startEOUPrediction(ctx context.Context) (context.Context, trace.Span) {
	a.eouMu.Lock()
	defer a.eouMu.Unlock()
	if a.eouTrace == nil || a.eouTrace.span.SpanContext().SpanID() != trace.SpanFromContext(ctx).SpanContext().SpanID() {
		return ctx, trace.SpanFromContext(context.Background())
	}
	if w := a.eouTrace; w != nil && w.detection != nil {
		w.floor = time.Now()
		w.detection.AddEvent("superseded")
		w.detection.End(trace.WithTimestamp(w.floor))
	}
	ctx, span := telemetry.StartSpan(ctx, "eou_detection")
	if w := a.eouTrace; w != nil {
		w.detection = span
	}
	return ctx, span
}

func (a *AgentActivity) finishEOUPrediction(span trace.Span) {
	a.eouMu.Lock()
	defer a.eouMu.Unlock()
	if w := a.eouTrace; w != nil && w.detection != nil && w.detection.SpanContext().SpanID() == span.SpanContext().SpanID() {
		w.detection = nil
		w.floor = time.Now()
	}
	span.End()
}

func (a *AgentActivity) endEOUWait(outcome string, ended time.Time) {
	a.eouMu.Lock()
	defer a.eouMu.Unlock()
	a.endEOUWaitLocked(outcome, ended)
}

// Caller holds eouMu so an old task cannot close a newer wait.
func (a *AgentActivity) endEOUWaitLocked(outcome string, ended time.Time) {
	w := a.eouTrace
	a.eouTrace = nil
	if w == nil {
		return
	}
	if w.detection != nil && w.detection.IsRecording() {
		w.floor = time.Now()
		w.detection.AddEvent("superseded", trace.WithAttributes(attribute.String("lk.eou.outcome", outcome)))
		w.detection.End(trace.WithTimestamp(w.floor))
	}
	if ended.IsZero() {
		ended = time.Now()
	}
	requested := ended
	if ended.Before(w.started) {
		ended = w.started
	}
	if ended.Before(w.floor) {
		ended = w.floor
	}
	if outcome == "user_resumed" {
		if requested.Before(w.started) {
			requested = w.started
		}
		w.span.AddEvent("user_resumed", trace.WithTimestamp(requested))
	}
	w.span.SetAttributes(attribute.String("lk.eou.outcome", outcome), attribute.Float64("lk.eou.wait_duration", ended.Sub(w.started).Seconds()), attribute.Int("lk.eou.rearm_count", w.rearms), attribute.Int("lk.eou.not_committed_count", 0))
	w.span.End(trace.WithTimestamp(ended))
}
