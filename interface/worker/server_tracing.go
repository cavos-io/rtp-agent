package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// SetTracerProvider transfers ownership of a shared provider to the server.
// Configure it before running jobs. Job-specific providers override this default.
func (s *AgentServer) SetTracerProvider(provider *sdktrace.TracerProvider) error {
	if provider == nil {
		return fmt.Errorf("tracer provider is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.executionStarted || s.started() || len(s.activeJobs) != 0 || len(s.finishingJobs) != 0 {
		return fmt.Errorf("tracer provider must be set before server execution")
	}
	if s.tracerProvider != nil {
		return fmt.Errorf("tracer provider is already set")
	}
	s.tracerProvider = provider
	return nil
}

// Close stops remaining jobs and finalizes server-owned tracing exactly once.
// Call after Run returns. The caller's deadline bounds job teardown; tracing gets
// a separate bounded export window even when teardown times out.
func (s *AgentServer) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.draining = true
		jobs := make([]*JobContext, 0, len(s.activeJobs))
		for _, job := range s.activeJobs {
			jobs = append(jobs, job)
		}
		for _, timer := range s.pendingTimers {
			timer.Stop()
		}
		s.mu.Unlock()
		for _, job := range jobs {
			go s.finishJob(job)
		}
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
	wait:
		for {
			s.mu.Lock()
			remaining := len(s.activeJobs) + len(s.finishingJobs)
			s.mu.Unlock()
			if remaining == 0 {
				break
			}
			select {
			case <-ctx.Done():
				s.closeErr = ctx.Err()
				break wait
			case <-ticker.C:
			}
		}
		if s.tracerProvider != nil {
			exportCtx, cancel := context.WithTimeout(context.Background(), observabilityFinalizeTimeout)
			defer cancel()
			s.closeErr = errors.Join(s.closeErr, s.tracerProvider.ForceFlush(exportCtx), s.tracerProvider.Shutdown(exportCtx))
		}
	})
	return s.closeErr
}
