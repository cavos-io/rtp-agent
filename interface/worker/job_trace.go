package worker

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"time"

	"github.com/cavos-io/rtp-agent/library/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type jobDispatchTimeline struct{ ReceivedAt, AcceptedAt, AssignedAt, LaunchedAt, EntrypointAt time.Time }

func (c *JobContext) jobTraceContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if c.observability == nil {
		return ctx
	}
	ctx = c.observability.Context(ctx)
	c.traceMu.Lock()
	defer c.traceMu.Unlock()
	if c.jobSpan == nil {
		started := c.dispatchTimeline.ReceivedAt
		if started.IsZero() {
			started = c.dispatchTimeline.EntrypointAt
		}
		if started.IsZero() {
			started = time.Now()
		}
		rootCtx := trace.ContextWithSpanContext(ctx, trace.SpanContext{})
		_, c.jobSpan = telemetry.StartSpan(rootCtx, "job_entrypoint", trace.WithTimestamp(started), trace.WithAttributes(attribute.String(telemetry.AttrJobID, c.JobID()), attribute.String(telemetry.AttrAgentName, c.Job.GetAgentName()), attribute.String(telemetry.AttrRoomName, c.Job.GetRoom().GetName()), attribute.String("lk.room_sid", c.Job.GetRoom().GetSid()), attribute.String("lk.job.worker_id", c.WorkerID())))
		c.recordDispatchTimelineLocked()
		if !c.entrypointReturnedAt.IsZero() {
			c.jobSpan.AddEvent("entrypoint_returned", trace.WithTimestamp(c.entrypointReturnedAt))
		}
	}
	return trace.ContextWithSpan(ctx, c.jobSpan)
}

func (c *JobContext) recordDispatchTimelineLocked() {
	stages := []struct {
		name string
		at   time.Time
	}{{"job_received", c.dispatchTimeline.ReceivedAt}, {"job_accepted", c.dispatchTimeline.AcceptedAt}, {"job_assigned", c.dispatchTimeline.AssignedAt}, {"process_assigned", c.dispatchTimeline.LaunchedAt}, {"entrypoint_started", c.dispatchTimeline.EntrypointAt}}
	keys := []string{"lk.job.accept_latency", "lk.job.assignment_latency", "lk.job.launch_latency", "lk.job.entrypoint_latency"}
	for i, stage := range stages {
		if stage.at.IsZero() {
			continue
		}
		c.jobSpan.AddEvent(stage.name, trace.WithTimestamp(stage.at))
		if i > 0 && !stages[i-1].at.IsZero() && !stage.at.Before(stages[i-1].at) {
			c.jobSpan.SetAttributes(attribute.Float64(keys[i-1], stage.at.Sub(stages[i-1].at).Seconds()))
		}
	}
	if !stages[0].at.IsZero() && !stages[4].at.IsZero() && !stages[4].at.Before(stages[0].at) {
		c.jobSpan.SetAttributes(attribute.Float64("lk.job.dispatch_latency", stages[4].at.Sub(stages[0].at).Seconds()))
	}
}

func (c *JobContext) shutdownTraceContext(ctx context.Context) context.Context {
	ctx = c.jobTraceContext(ctx)
	c.traceMu.Lock()
	defer c.traceMu.Unlock()
	if c.shutdownTraceCtx != nil {
		return trace.ContextWithSpan(ctx, c.shutdownSpan)
	}
	return ctx
}

func (c *JobContext) beginShutdownTrace(reason string) context.Context {
	// Teardown must not enable Cloud reporting for a job that never used it.
	if c.serverTracerProvider != nil {
		if err := c.ensureObservability(context.Background()); err != nil {
			c.Logger().Warnw("failed to initialize shutdown tracing", err)
		}
	}
	ctx := c.jobTraceContext(context.Background())
	c.traceMu.Lock()
	defer c.traceMu.Unlock()
	if c.shutdownSpan == nil {
		c.shutdownTraceCtx, c.shutdownSpan = telemetry.StartSpan(ctx, "job_shutdown", trace.WithAttributes(attribute.String("lk.shutdown.reason", reason)))
	}
	return c.shutdownTraceCtx
}

func (c *JobContext) endJobTrace() {
	c.traceMu.Lock()
	defer c.traceMu.Unlock()
	if c.shutdownSpan != nil {
		c.shutdownSpan.End()
	}
	if c.jobSpan != nil {
		c.jobSpan.End()
	}
}

func callbackTraceName(callback any) string {
	value := reflect.ValueOf(callback)
	if value.IsValid() && value.Kind() == reflect.Func {
		if f := runtime.FuncForPC(value.Pointer()); f != nil {
			return f.Name()
		}
	}
	return "callback"
}

func recordJobSpanError(span trace.Span, err error) {
	if err != nil {
		span.SetAttributes(attribute.String(telemetry.AttrErrorType, fmt.Sprintf("%T", err)))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
}

func dispatchTimestamp(at time.Time) float64 {
	if at.IsZero() {
		return 0
	}
	return float64(at.UnixNano()) / 1e9
}
func timelineFromSeconds(received, accepted, assigned, launched float64) jobDispatchTimeline {
	from := func(value float64) time.Time {
		if value == 0 {
			return time.Time{}
		}
		seconds := int64(value)
		return time.Unix(seconds, int64((value-float64(seconds))*1e9))
	}
	return jobDispatchTimeline{ReceivedAt: from(received), AcceptedAt: from(accepted), AssignedAt: from(assigned), LaunchedAt: from(launched)}
}
