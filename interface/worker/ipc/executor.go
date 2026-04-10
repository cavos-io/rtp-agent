package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/livekit/protocol/livekit"
)

type JobStatus string

const (
	JobStatusRunning JobStatus = "running"
	JobStatusFailed  JobStatus = "failed"
	JobStatusSuccess JobStatus = "success"
)

type JobExecutor interface {
	ID() string
	Status() JobStatus
	Started() bool
	LaunchJob(ctx context.Context, job *livekit.Job) error
	Close(ctx context.Context) error
}

type ThreadJobExecutor struct {
	id     string
	status JobStatus
	mu     sync.Mutex

	entrypoint func() error
	job        *livekit.Job
	started    bool
}

func NewThreadJobExecutor(id string, entrypoint func() error) *ThreadJobExecutor {
	return &ThreadJobExecutor{
		id:         id,
		entrypoint: entrypoint,
		status:     JobStatusSuccess,
	}
}

func (e *ThreadJobExecutor) ID() string {
	return e.id
}

func (e *ThreadJobExecutor) Status() JobStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.status
}

func (e *ThreadJobExecutor) Started() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.started
}

func (e *ThreadJobExecutor) LaunchJob(ctx context.Context, job *livekit.Job) error {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return fmt.Errorf("executor already started")
	}
	e.job = job
	e.started = true
	e.status = JobStatusRunning
	e.mu.Unlock()

	go func() {
		err := e.entrypoint()
		e.mu.Lock()
		if err != nil {
			logger.Logger.Errorw("Job entrypoint failed", err, "job_id", job.Id)
			e.status = JobStatusFailed
		} else {
			e.status = JobStatusSuccess
		}
		e.mu.Unlock()
	}()

	return nil
}

func (e *ThreadJobExecutor) Close(ctx context.Context) error {
	// Goroutines cannot be killed externally, must be handled via context in entrypoint
	return nil
}

type ProcessJobExecutor struct {
	id      string
	status  JobStatus
	mu      sync.Mutex
	started bool
	cmd     *exec.Cmd
	
	lastPong time.Time
}

func NewProcessJobExecutor(id string) *ProcessJobExecutor {
	return &ProcessJobExecutor{
		id: id,
	}
}

func (e *ProcessJobExecutor) ID() string {
	return e.id
}

func (e *ProcessJobExecutor) Status() JobStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.status
}

func (e *ProcessJobExecutor) Started() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.started
}

func (e *ProcessJobExecutor) LaunchJob(ctx context.Context, job *livekit.Job) error {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return fmt.Errorf("executor already started")
	}
	e.started = true
	e.status = JobStatusRunning
	e.lastPong = time.Now()
	e.mu.Unlock()

	exe, err := os.Executable()
	if err != nil {
		e.mu.Lock()
		e.status = JobStatusFailed
		e.mu.Unlock()
		return err
	}

	jobJSON, err := json.Marshal(job)
	if err != nil {
		e.mu.Lock()
		e.status = JobStatusFailed
		e.mu.Unlock()
		return err
	}

	// We pass the job details via environment variables for parity with Python's IPC/subprocess launch
	cmd := exec.CommandContext(ctx, exe, "start")
	cmd.Env = append(os.Environ(), 
		fmt.Sprintf("LIVEKIT_AGENT_PROCESS_ID=%s", e.id),
		fmt.Sprintf("LIVEKIT_AGENT_JOB_JSON=%s", string(jobJSON)),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	e.mu.Lock()
	e.cmd = cmd
	e.mu.Unlock()

	go e.pingTask(ctx)

	go func() {
		err := cmd.Run()
		e.mu.Lock()
		defer e.mu.Unlock()
		if err != nil {
			logger.Logger.Errorw("Job process failed", err, "job_id", job.Id, "exec_id", e.id)
			e.status = JobStatusFailed
		} else {
			e.status = JobStatusSuccess
		}
	}()

	return nil
}

func (e *ProcessJobExecutor) pingTask(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.mu.Lock()
			if e.status != JobStatusRunning {
				e.mu.Unlock()
				return
			}
			
			// In a full implementation, we would send a ping message via a pipe
			// and wait for a pong. 
			// For basic parity, we'll check if the process is still alive.
			if e.cmd != nil && e.cmd.Process != nil {
				// check if process exists
				if err := e.cmd.Process.Signal(syscall.Signal(0)); err != nil {
					logger.Logger.Warnw("Job process unresponsive", err, "exec_id", e.id)
					// Handle unresponsiveness
				}
			}
			e.mu.Unlock()
		}
	}
}

func (e *ProcessJobExecutor) Close(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cmd != nil && e.cmd.Process != nil {
		return e.cmd.Process.Kill()
	}
	return nil
}
