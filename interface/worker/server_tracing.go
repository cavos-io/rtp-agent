package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cavos-io/rtp-agent/library/logger"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// SetTracerProvider transfers ownership of a shared provider to the server.
// Configure it before running jobs. All jobs share this provider.
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
			remaining := len(s.activeJobs) + len(s.finishingJobs) + s.entrypoints
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
		if s.sessionTransport != nil {
			if s.closeErr == nil {
				s.closeErr = s.closeSessionTransport()
			} else {
				// A caller deadline must not dispose FFI beneath unfinished jobs.
				go func() {
					ticker := time.NewTicker(10 * time.Millisecond)
					defer ticker.Stop()
					for range ticker.C {
						s.mu.Lock()
						remaining := len(s.activeJobs) + len(s.finishingJobs) + s.entrypoints
						s.mu.Unlock()
						if remaining == 0 {
							if err := s.closeSessionTransport(); err != nil {
								logger.Logger.Errorw("worker session transport cleanup failed", err)
							}
							return
						}
					}
				}()
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

func (s *AgentServer) closeSessionTransport() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.sessionTransport.Close(ctx)
}
