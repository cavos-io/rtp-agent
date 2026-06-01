package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
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
	Job() *livekit.Job
	RunningJob() *RunningJobInfo
	LaunchJob(ctx context.Context, job *livekit.Job) error
	LaunchRunningJob(ctx context.Context, info RunningJobInfo) error
	Close(ctx context.Context) error
}

type ThreadJobExecutor struct {
	id     string
	status JobStatus
	mu     sync.Mutex

	entrypoint func() error
	job        *livekit.Job
	runningJob *RunningJobInfo
	started    bool
	done       chan struct{}
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

func (e *ThreadJobExecutor) Job() *livekit.Job {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.job
}

func (e *ThreadJobExecutor) RunningJob() *RunningJobInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.runningJob
}

func (e *ThreadJobExecutor) LaunchJob(ctx context.Context, job *livekit.Job) error {
	return e.LaunchRunningJob(ctx, RunningJobInfo{Job: job})
}

func (e *ThreadJobExecutor) LaunchRunningJob(ctx context.Context, info RunningJobInfo) error {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return fmt.Errorf("executor already started")
	}
	e.job = info.Job
	e.runningJob = &info
	e.started = true
	e.done = make(chan struct{})
	e.status = JobStatusRunning
	e.mu.Unlock()

	go func() {
		status := JobStatusSuccess
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Logger.Errorw("Job entrypoint panicked", fmt.Errorf("%v", recovered), "job_id", info.Job.GetId())
				status = JobStatusFailed
			}
			e.mu.Lock()
			e.status = status
			close(e.done)
			e.mu.Unlock()
		}()

		if err := e.entrypoint(); err != nil {
			logger.Logger.Errorw("Job entrypoint failed", err, "job_id", info.Job.GetId())
			status = JobStatusFailed
		}
	}()

	return nil
}

func (e *ThreadJobExecutor) Close(ctx context.Context) error {
	e.mu.Lock()
	done := e.done
	started := e.started
	e.mu.Unlock()
	if !started || done == nil {
		return nil
	}

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type ProcessJobExecutor struct {
	id         string
	status     JobStatus
	mu         sync.Mutex
	started    bool
	cmd        *exec.Cmd
	job        *livekit.Job
	runningJob *RunningJobInfo

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

func (e *ProcessJobExecutor) Job() *livekit.Job {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.job
}

func (e *ProcessJobExecutor) RunningJob() *RunningJobInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.runningJob
}

func (e *ProcessJobExecutor) LaunchJob(ctx context.Context, job *livekit.Job) error {
	return e.LaunchRunningJob(ctx, RunningJobInfo{Job: job})
}

func (e *ProcessJobExecutor) LaunchRunningJob(ctx context.Context, info RunningJobInfo) error {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return fmt.Errorf("executor already started")
	}
	e.started = true
	e.status = JobStatusRunning
	e.job = info.Job
	e.runningJob = &info
	e.lastPong = time.Now()
	e.mu.Unlock()

	exe, err := os.Executable()
	if err != nil {
		e.mu.Lock()
		e.status = JobStatusFailed
		e.mu.Unlock()
		return err
	}

	env, err := processJobEnv(os.Environ(), e.id, info)
	if err != nil {
		e.mu.Lock()
		e.status = JobStatusFailed
		e.mu.Unlock()
		return err
	}

	// We pass the job details via environment variables for parity with Python's IPC/subprocess launch
	cmd := exec.CommandContext(ctx, exe, "start")
	cmd.Env = env
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
			logger.Logger.Errorw("Job process failed", err, "job_id", info.Job.GetId(), "exec_id", e.id)
			e.status = JobStatusFailed
		} else {
			e.status = JobStatusSuccess
		}
	}()

	return nil
}

func processJobEnv(baseEnv []string, processID string, info RunningJobInfo) ([]string, error) {
	jobJSON, err := json.Marshal(info.Job)
	if err != nil {
		return nil, err
	}
	runningJobJSON, err := json.Marshal(info)
	if err != nil {
		return nil, err
	}

	env := upsertEnv(baseEnv, "LIVEKIT_AGENT_PROCESS_ID", processID)
	env = upsertEnv(env, "LIVEKIT_AGENT_JOB_JSON", string(jobJSON))
	env = upsertEnv(env, "LIVEKIT_AGENT_RUNNING_JOB_JSON", string(runningJobJSON))
	return env, nil
}

func upsertEnv(env []string, key string, value string) []string {
	next := make([]string, 0, len(env)+1)
	for _, item := range env {
		name, _, ok := strings.Cut(item, "=")
		if ok && name == key {
			continue
		}
		next = append(next, item)
	}
	return append(next, fmt.Sprintf("%s=%s", key, value))
}

func RunningJobInfoFromEnv(env map[string]string) (RunningJobInfo, error) {
	if raw := env["LIVEKIT_AGENT_RUNNING_JOB_JSON"]; raw != "" {
		var info RunningJobInfo
		if err := json.Unmarshal([]byte(raw), &info); err != nil {
			return RunningJobInfo{}, err
		}
		return info, nil
	}

	rawJob := env["LIVEKIT_AGENT_JOB_JSON"]
	var job livekit.Job
	if err := json.Unmarshal([]byte(rawJob), &job); err != nil {
		return RunningJobInfo{}, err
	}
	return RunningJobInfo{Job: &job}, nil
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
