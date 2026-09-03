package worker

import (
	"testing"
	"time"

	"github.com/livekit/protocol/livekit"
)

func withShortTeardownTimeout(t *testing.T, timeout time.Duration) {
	t.Helper()
	previous := teardownStepTimeout
	teardownStepTimeout = timeout
	t.Cleanup(func() { teardownStepTimeout = previous })
}

func TestFinishJobLeavesBehindAHangingShutdown(t *testing.T) {
	withShortTeardownTimeout(t, 100*time.Millisecond)

	server := NewAgentServer(WorkerOptions{})
	jobCtx := NewJobContext(&livekit.Job{Id: "job-stuck"}, "", "", "")

	release := make(chan struct{})
	defer close(release)
	shutdownStarted := make(chan struct{})
	if err := jobCtx.AddShutdownCallback(func(string) {
		close(shutdownStarted)
		<-release
	}); err != nil {
		t.Fatalf("AddShutdownCallback() error = %v", err)
	}

	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		server.finishJob(jobCtx)
		done <- time.Since(start)
	}()

	select {
	case <-shutdownStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown callback never ran")
	}

	select {
	case elapsed := <-done:
		if elapsed > time.Second {
			t.Fatalf("finishJob() took %v, want it to stop waiting at the timeout", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("finishJob() is held by a hanging teardown step")
	}

	if got := server.StuckTeardownSteps(); got == 0 {
		t.Fatal("the abandoned step was not counted")
	}
}

func TestHandleTerminationSurvivesAHangingShutdownOnce(t *testing.T) {
	withShortTeardownTimeout(t, 100*time.Millisecond)

	server := NewAgentServer(WorkerOptions{})
	jobCtx := NewJobContext(&livekit.Job{Id: "job-once"}, "", "", "")

	release := make(chan struct{})
	defer close(release)
	if err := jobCtx.AddShutdownCallback(func(string) { <-release }); err != nil {
		t.Fatalf("AddShutdownCallback() error = %v", err)
	}

	server.mu.Lock()
	server.activeJobs[jobCtx.Job.Id] = jobCtx
	server.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		server.handleTermination(&livekit.JobTermination{JobId: jobCtx.Job.Id})
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleTermination is held by the sync.Once of a hanging Shutdown")
	}
}

func TestFinishJobWaitsForAHealthyShutdown(t *testing.T) {
	withShortTeardownTimeout(t, 2*time.Second)

	server := NewAgentServer(WorkerOptions{})
	jobCtx := NewJobContext(&livekit.Job{Id: "job-healthy"}, "", "", "")

	finished := make(chan struct{})
	if err := jobCtx.AddShutdownCallback(func(string) {
		time.Sleep(50 * time.Millisecond)
		close(finished)
	}); err != nil {
		t.Fatalf("AddShutdownCallback() error = %v", err)
	}

	server.finishJob(jobCtx)

	select {
	case <-finished:
	default:
		t.Fatal("finishJob() did not wait for a healthy teardown to complete")
	}
	if got := server.StuckTeardownSteps(); got != 0 {
		t.Fatalf("StuckTeardownSteps() = %d, want 0 for a healthy teardown", got)
	}
}
