package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/livekit/protocol/livekit"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type sharedTraceProcessor struct {
	*tracetest.SpanRecorder
	shutdowns atomic.Int32
	flushes   atomic.Int32
}

func (p *sharedTraceProcessor) Shutdown(context.Context) error   { p.shutdowns.Add(1); return nil }
func (p *sharedTraceProcessor) ForceFlush(context.Context) error { p.flushes.Add(1); return nil }

func TestServerSharedTracerLifecycle(t *testing.T) {
	ctx := context.Background()
	processor := &sharedTraceProcessor{SpanRecorder: tracetest.NewSpanRecorder()}
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(processor))
	server := NewAgentServer(WorkerOptions{})
	if err := server.SetTracerProvider(provider); err != nil {
		t.Fatal(err)
	}
	server.entrypointFnc = func(job *JobContext) error {
		if err := job.SetTraceMetadata(attribute.String("session.id", job.JobID())); err != nil {
			return err
		}
		if err := job.ensureObservability(ctx); err != nil {
			return err
		}
		_, span := telemetry.StartSpan(job.observability.Context(ctx), job.JobID())
		span.End()
		return nil
	}
	for _, id := range []string{"first", "second"} {
		job := NewJobContext(&livekit.Job{Id: id, Room: &livekit.Room{}}, "", "", "")
		job.fakeJob = true
		if err := server.runJobEntrypoint(job); err != nil {
			t.Fatal(err)
		}
		if err := job.FinalizeObservability(ctx); err != nil {
			t.Fatal(err)
		}
		if processor.shutdowns.Load() != 0 {
			t.Fatal("job shut down shared provider")
		}
		_ = job.onCleanUp()
	}
	if len(processor.Ended()) < 2 {
		t.Fatal("second job could not export")
	}
	for range 2 {
		if err := server.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if processor.shutdowns.Load() != 1 {
		t.Fatal("provider shutdown was not exactly once")
	}
	if err := server.beginRun(); err == nil {
		t.Fatal("closed server restarted")
	}
}

func TestServerSharedTracerWaitsForJobTeardown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	processor := &sharedTraceProcessor{SpanRecorder: tracetest.NewSpanRecorder()}
	server := NewAgentServer(WorkerOptions{})
	if err := server.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(processor))); err != nil {
		t.Fatal(err)
	}
	job := NewJobContext(&livekit.Job{Id: "closing", Room: &livekit.Room{}}, "", "", "")
	job.fakeJob = true
	server.entrypointFnc = func(*JobContext) error { return nil }
	if err := server.runJobEntrypoint(job); err != nil {
		t.Fatal(err)
	}
	server.activeJobs[job.JobID()] = job
	entered, release := make(chan struct{}), make(chan struct{})
	if err := job.AddShutdownCallback(func() { close(entered); <-release }); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Close(ctx) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if processor.shutdowns.Load() != 0 {
		t.Error("provider closed before callback completed")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, span := range processor.Ended() {
		names[span.Name()] = true
	}
	for _, name := range []string{"job_shutdown", "shutdown_callback", "session_end_upload"} {
		if !names[name] {
			t.Errorf("missing final span %s", name)
		}
	}
}

func TestServerSharedTracerCloseTimeout(t *testing.T) {
	processor := &sharedTraceProcessor{SpanRecorder: tracetest.NewSpanRecorder()}
	server := NewAgentServer(WorkerOptions{})
	if err := server.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(processor))); err != nil {
		t.Fatal(err)
	}
	server.finishingJobs["blocked"] = &JobContext{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := server.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close = %v", err)
	}
	if processor.shutdowns.Load() != 1 || processor.flushes.Load() != 1 {
		t.Fatal("timed-out teardown skipped provider cleanup")
	}
}

func TestServerSetTracerProviderValidatesLifecycle(t *testing.T) {
	ctx := context.Background()
	if err := NewAgentServer(WorkerOptions{}).SetTracerProvider(nil); err == nil {
		t.Fatal("nil provider was accepted")
	}
	for _, test := range []struct {
		name    string
		prepare func(*AgentServer) error
	}{
		{"already configured", func(server *AgentServer) error {
			return server.SetTracerProvider(sdktrace.NewTracerProvider())
		}},
		{"execution started", func(server *AgentServer) error {
			return server.beginRun()
		}},
		{"closed", func(server *AgentServer) error {
			return server.Close(ctx)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := NewAgentServer(WorkerOptions{})
			t.Cleanup(func() { _ = server.Close(ctx) })
			if err := test.prepare(server); err != nil {
				t.Fatal(err)
			}
			provider := sdktrace.NewTracerProvider()
			t.Cleanup(func() { _ = provider.Shutdown(ctx) })
			if err := server.SetTracerProvider(provider); err == nil {
				t.Fatal("provider was accepted after configuration or execution ended")
			}
			_, span := provider.Tracer("test").Start(ctx, "rejected provider")
			defer span.End()
			if !span.IsRecording() {
				t.Fatal("rejected provider ownership was consumed")
			}
		})
	}
}
