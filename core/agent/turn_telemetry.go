package agent

import (
	"context"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/library/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Endpointing tasks can be replaced while a provider is still returning. Each
// attempt owns only its detection span; the logical wait survives rearming.
type eouTraceAttempt struct {
	wait      *eouWaitTrace
	detection trace.Span
}

type eouWaitTrace struct {
	span, user           trace.Span
	started, floor       time.Time
	rearms, notCommitted int
	attempt              *eouTraceAttempt
}

type turnTelemetry struct {
	mu      sync.Mutex
	wait    *eouWaitTrace
	user    trace.SpanID
	resumes int
}

func (t *turnTelemetry) begin(ctx context.Context, user trace.Span, started time.Time, source string, delay float64) *eouTraceAttempt {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	if started.IsZero() || started.After(now) {
		started = now
	}
	if t.wait != nil && t.wait.user.SpanContext().SpanID() != user.SpanContext().SpanID() {
		t.endLocked("dropped", now)
	}
	if t.user != user.SpanContext().SpanID() {
		t.user, t.resumes = user.SpanContext().SpanID(), 0
	}
	attrs := []attribute.KeyValue{attribute.String(telemetry.AttrEOUSource, source), attribute.Float64(telemetry.AttrEOUDelay, delay)}
	if t.wait == nil {
		_, span := telemetry.StartSpan(trace.ContextWithSpan(ctx, user), "eou_wait", trace.WithTimestamp(started), trace.WithAttributes(attrs...))
		t.wait = &eouWaitTrace{span: span, user: user, started: started}
	} else {
		t.endDetectionLocked("", now)
		t.wait.rearms++
		t.wait.floor = now
		t.wait.span.AddEvent("rearmed", trace.WithTimestamp(now), trace.WithAttributes(attribute.String(telemetry.AttrEOUSource, source)))
		t.wait.span.SetAttributes(attrs...)
	}
	a := &eouTraceAttempt{wait: t.wait}
	t.wait.attempt = a
	return a
}

func (t *turnTelemetry) currentLocked(a *eouTraceAttempt) bool {
	return a != nil && t.wait == a.wait && t.wait.attempt == a
}

func (t *turnTelemetry) startDetection(ctx context.Context, a *eouTraceAttempt) (context.Context, trace.Span) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.currentLocked(a) || ctx.Err() != nil {
		span := trace.SpanFromContext(context.Background())
		return trace.ContextWithSpan(ctx, span), span
	}
	ctx, span := telemetry.StartSpan(trace.ContextWithSpan(ctx, t.wait.span), "eou_detection")
	a.detection = span
	return ctx, span
}

func (t *turnTelemetry) detectionDone(a *eouTraceAttempt, delay float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.currentLocked(a) {
		t.endDetectionLocked("", time.Now())
		t.wait.span.SetAttributes(attribute.Float64(telemetry.AttrEOUDelay, delay))
	}
}

func (t *turnTelemetry) notCommitted(a *eouTraceAttempt) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.currentLocked(a) {
		t.wait.notCommitted++
	}
}

func (t *turnTelemetry) finish(a *eouTraceAttempt, outcome string, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.currentLocked(a) {
		t.endLocked(outcome, at)
	}
}

func (t *turnTelemetry) finishCurrent(outcome string, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.endLocked(outcome, at)
}

func (t *turnTelemetry) endDetectionLocked(outcome string, at time.Time) {
	if t.wait == nil || t.wait.attempt == nil || t.wait.attempt.detection == nil {
		return
	}
	s := t.wait.attempt.detection
	if outcome != "" {
		s.AddEvent("superseded", trace.WithTimestamp(at), trace.WithAttributes(attribute.String(telemetry.AttrEOUOutcome, outcome)))
	}
	s.End(trace.WithTimestamp(at))
	t.wait.attempt.detection = nil
	t.wait.floor = at
}

func (t *turnTelemetry) endLocked(outcome string, at time.Time) {
	w := t.wait
	if w == nil {
		return
	}
	if at.IsZero() {
		at = time.Now()
	}
	requested := at
	t.endDetectionLocked(outcome, time.Now())
	if at.Before(w.started) {
		at = w.started
	}
	if at.Before(w.floor) {
		at = w.floor
	}
	if outcome == "user_resumed" {
		if requested.Before(w.started) {
			requested = w.started
		}
		w.span.AddEvent("user_resumed", trace.WithTimestamp(requested))
		t.resumes++
		w.user.SetAttributes(attribute.Int(telemetry.AttrEOUResumeCount, t.resumes))
	}
	w.span.SetAttributes(
		attribute.String(telemetry.AttrEOUOutcome, outcome),
		attribute.Float64(telemetry.AttrEOUWaitDuration, float64(at.UnixNano()-w.started.UnixNano())/float64(time.Second)),
		attribute.Int(telemetry.AttrEOURearmCount, w.rearms),
		attribute.Int(telemetry.AttrEOUNotCommittedCount, w.notCommitted),
	)
	w.span.End(trace.WithTimestamp(at))
	t.wait = nil
}

func (a *AgentActivity) beginEOUTrace(ctx context.Context, info EndOfTurnInfo, source string) *eouTraceAttempt {
	started := time.Now()
	if info.StoppedSpeakingAt != nil {
		started = unixSecondsToTime(*info.StoppedSpeakingAt)
	}
	anchor := started
	if info.StartedSpeakingAt != nil {
		if speech := unixSecondsToTime(*info.StartedSpeakingAt); speech.Before(anchor) {
			anchor = speech
		}
	}
	// A rejected/empty backchannel must not reopen its already closed user turn.
	if info.NewTranscript == "" && source != "manual" {
		a.Session.mu.Lock()
		active := a.Session.userTurnSpan != nil
		a.Session.mu.Unlock()
		if !active {
			return nil
		}
	}
	user, userStarted := a.Session.ensureUserTurnSpanAt(ctx, anchor)
	if started.Before(userStarted) {
		started = userStarted
	}
	return a.turnTelemetry.begin(ctx, user, started, source, a.minEndpointingDelay())
}
