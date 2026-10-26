package telemetry

import (
	"context"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestSharedProviderJobIsolation(t *testing.T) {
	ctx := context.Background()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer provider.Shutdown(ctx)
	jobs := make([]*JobObservability, 2)
	for i, id := range []string{"first", "second"} {
		var err error
		jobs[i], err = NewJobObservability(ctx, JobObservabilityConfig{JobID: id, TracerProvider: provider, BorrowTracerProvider: true, TraceMetadata: []attribute.KeyValue{attribute.String("session.id", id)}})
		if err != nil {
			t.Fatal(err)
		}
	}
	for i, job := range jobs {
		_, span := StartSpan(job.Context(ctx), "job")
		span.End()
		if err := job.Shutdown(ctx); err != nil {
			t.Fatal(err)
		}
		if len(recorder.Ended()) != i+1 {
			t.Fatal("shared provider stopped before all jobs finished")
		}
	}
	for i, id := range []string{"first", "second"} {
		attrs := testSpanAttributes(recorder.Ended()[i])
		if attrs["session.id"] != id || attrs["job_id"] != id {
			t.Fatalf("metadata leaked: %v", attrs)
		}
	}
}

func TestSharedProviderConcurrentContentPolicy(t *testing.T) {
	ctx := context.Background()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer provider.Shutdown(ctx)
	var wg sync.WaitGroup
	for _, allow := range []bool{false, true} {
		job, err := NewJobObservability(ctx, JobObservabilityConfig{TracerProvider: provider, BorrowTracerProvider: true, TracingOptions: TracingOptions{AllowPII: &allow}})
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer job.Shutdown(ctx)
			name := "redacted"
			if allow {
				name = "allowed"
			}
			for range 20 {
				_, span := StartSpan(job.Context(ctx), name, trace.WithAttributes(attribute.String(AttrUserInput, "secret")))
				span.SetAttributes(attribute.String(AttrResponseText, "reply"))
				span.End()
			}
		}()
	}
	wg.Wait()
	for _, span := range recorder.Ended() {
		attrs := testSpanAttributes(span)
		if (attrs[AttrUserInput] != "") != (span.Name() == "allowed") || (attrs[AttrResponseText] != "") != (span.Name() == "allowed") {
			t.Fatalf("policy crossed jobs: %s %v", span.Name(), attrs)
		}
	}
	if len(recorder.Ended()) != 40 {
		t.Fatal("missing concurrent spans")
	}
}
