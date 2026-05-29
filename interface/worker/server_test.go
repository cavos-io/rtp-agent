package worker

import (
	"testing"
	"time"
)

func TestAgentServerNumActiveJobs(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	server.activeJobs["job-1"] = &JobContext{}
	server.activeJobs["job-2"] = &JobContext{}

	if got := server.NumActiveJobs(); got != 2 {
		t.Fatalf("NumActiveJobs() = %d, want 2", got)
	}
}

func TestAgentServerCurrentLoadUsesLoadFn(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		LoadFn: func(server *AgentServer) float64 {
			return float64(server.NumActiveJobs()) / 4.0
		},
	})
	server.activeJobs["job-1"] = &JobContext{}
	server.activeJobs["job-2"] = &JobContext{}

	if got := server.currentLoad(); got != 0.5 {
		t.Fatalf("currentLoad() = %v, want 0.5", got)
	}
}

func TestAgentServerCurrentLoadClampsRange(t *testing.T) {
	server := NewAgentServer(WorkerOptions{LoadFn: func(*AgentServer) float64 { return 2.0 }})
	if got := server.currentLoad(); got != 1.0 {
		t.Fatalf("currentLoad() high clamp = %v, want 1.0", got)
	}

	server.Options.LoadFn = func(*AgentServer) float64 { return -1.0 }
	if got := server.currentLoad(); got != 0.0 {
		t.Fatalf("currentLoad() low clamp = %v, want 0.0", got)
	}
}

func TestAgentServerDrain(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	server.activeJobs["job-1"] = &JobContext{}

	done := make(chan error, 1)
	go func() {
		done <- server.Drain(t.Context())
	}()

	// Should be waiting
	select {
	case err := <-done:
		t.Fatalf("Drain finished early with err: %v", err)
	case <-time.After(100 * time.Millisecond):
		// OK
	}

	// Finish the job
	server.mu.Lock()
	delete(server.activeJobs, "job-1")
	server.mu.Unlock()
	server.cond.Broadcast()

	// Should finish now
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Drain failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Drain timed out after job finished")
	}
}
