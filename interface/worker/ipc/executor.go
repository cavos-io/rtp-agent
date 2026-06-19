package ipc

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cavos-io/rtp-agent/library/logger"
	mathutil "github.com/cavos-io/rtp-agent/library/math"
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
	Job() Job
	RunningJob() *RunningJobInfo
	LaunchJob(ctx context.Context, job Job) error
	LaunchRunningJob(ctx context.Context, info RunningJobInfo) error
	Close(ctx context.Context) error
}

var processExecutable = os.Executable
var processCommandContext = exec.CommandContext
var processPingInterval = 2500 * time.Millisecond
var processPingTimeout = 60 * time.Second
var processShutdownGraceTimeout = 5 * time.Second
var processNow = time.Now
var processTimeMS = mathutil.TimeMS
var processSignal = func(process *os.Process, signal os.Signal) error {
	return process.Signal(signal)
}
var processKill = func(process *os.Process) error {
	return process.Kill()
}

type ThreadJobExecutor struct {
	id     string
	status JobStatus
	mu     sync.Mutex

	entrypoint func() error
	job        Job
	runningJob *RunningJobInfo
	started    bool
	done       chan struct{}
}

func NewThreadJobExecutor(id string, entrypoint func() error) *ThreadJobExecutor {
	return &ThreadJobExecutor{
		id:         id,
		entrypoint: entrypoint,
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

func (e *ThreadJobExecutor) Job() Job {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.job
}

func (e *ThreadJobExecutor) RunningJob() *RunningJobInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.runningJob
}

func (e *ThreadJobExecutor) LaunchJob(ctx context.Context, job Job) error {
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
				logger.Logger.Errorw("Job entrypoint panicked", fmt.Errorf("%v", recovered), "job_id", JobID(info.Job))
				status = JobStatusFailed
			}
			e.mu.Lock()
			e.status = status
			close(e.done)
			e.mu.Unlock()
		}()

		if err := e.entrypoint(); err != nil {
			logger.Logger.Errorw("Job entrypoint failed", err, "job_id", JobID(info.Job))
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
	job        Job
	runningJob *RunningJobInfo
	done       chan struct{}

	lastPong   time.Time
	pingWriter io.Writer

	shutdownAck      chan struct{}
	shutdownAcked    bool
	shuttingDown     chan struct{}
	shuttingDownSeen bool
}

func NewProcessJobExecutor(id string) *ProcessJobExecutor {
	return &ProcessJobExecutor{
		id:           id,
		shutdownAck:  make(chan struct{}),
		shuttingDown: make(chan struct{}),
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

func (e *ProcessJobExecutor) Job() Job {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.job
}

func (e *ProcessJobExecutor) RunningJob() *RunningJobInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.runningJob
}

func (e *ProcessJobExecutor) LaunchJob(ctx context.Context, job Job) error {
	return e.LaunchRunningJob(ctx, RunningJobInfo{Job: job})
}

func (e *ProcessJobExecutor) HandlePong(PongResponse) {
	e.mu.Lock()
	e.lastPong = processNow()
	e.mu.Unlock()
}

func (e *ProcessJobExecutor) HandleShutdownRequestAck(ShutdownRequestAck) {
	e.mu.Lock()
	if e.shutdownAck == nil {
		e.shutdownAck = make(chan struct{})
	}
	if !e.shutdownAcked {
		e.shutdownAcked = true
		close(e.shutdownAck)
	}
	e.mu.Unlock()
}

func (e *ProcessJobExecutor) HandleShuttingDown(ShuttingDown) {
	e.mu.Lock()
	if e.shuttingDown == nil {
		e.shuttingDown = make(chan struct{})
	}
	if !e.shuttingDownSeen {
		e.shuttingDownSeen = true
		close(e.shuttingDown)
	}
	e.mu.Unlock()
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
	e.done = make(chan struct{})
	e.lastPong = time.Now()
	e.shutdownAck = make(chan struct{})
	e.shutdownAcked = false
	e.shuttingDown = make(chan struct{})
	e.shuttingDownSeen = false
	e.mu.Unlock()

	exe, err := processExecutable()
	if err != nil {
		e.mu.Lock()
		e.status = JobStatusFailed
		e.started = false
		e.done = nil
		e.mu.Unlock()
		return err
	}

	env, err := ProcessJobEnv(os.Environ(), e.id, info)
	if err != nil {
		e.mu.Lock()
		e.status = JobStatusFailed
		e.started = false
		e.done = nil
		e.mu.Unlock()
		return err
	}

	// We pass the job details via environment variables for parity with Python's IPC/subprocess launch
	cmd := processCommandContext(ctx, exe, "start")
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
		defer close(e.done)
		if err != nil {
			logger.Logger.Errorw("Job process failed", err, "job_id", JobID(info.Job), "exec_id", e.id)
			e.status = JobStatusFailed
		} else if e.status == JobStatusRunning {
			e.status = JobStatusSuccess
		}
	}()

	return nil
}

func (e *ProcessJobExecutor) pingTask(ctx context.Context) {
	ticker := time.NewTicker(processPingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var killProcess *os.Process
			var pingWriter io.Writer
			var pingTimestamp int64

			e.mu.Lock()
			if e.status != JobStatusRunning {
				e.mu.Unlock()
				return
			}

			if e.cmd != nil && e.cmd.Process != nil {
				if err := processSignal(e.cmd.Process, syscall.Signal(0)); err != nil {
					logger.Logger.Warnw("Job process unresponsive", err, "exec_id", e.id)
					e.status = JobStatusFailed
					killProcess = e.cmd.Process
					e.mu.Unlock()
					_ = processKill(killProcess)
					return
				}
			}
			if e.pingWriter != nil {
				now := processNow()
				if !e.lastPong.IsZero() && now.Sub(e.lastPong) > processPingTimeout {
					logger.Logger.Warnw("Job process missed pong timeout", nil, "exec_id", e.id)
					e.status = JobStatusFailed
					if e.cmd != nil {
						killProcess = e.cmd.Process
					}
					e.mu.Unlock()
					if killProcess != nil {
						_ = processKill(killProcess)
					}
					return
				}
				pingWriter = e.pingWriter
				pingTimestamp = processTimeMS()
			}
			e.mu.Unlock()

			if pingWriter != nil {
				msg, err := NewMessage(&PingRequest{Timestamp: pingTimestamp})
				if err == nil {
					err = WriteMessage(pingWriter, msg)
				}
				if err != nil {
					logger.Logger.Warnw("Job process ping failed", err, "exec_id", e.id)
					e.mu.Lock()
					if e.status == JobStatusRunning {
						e.status = JobStatusFailed
					}
					if e.cmd != nil {
						killProcess = e.cmd.Process
					}
					e.mu.Unlock()
					if killProcess != nil {
						_ = processKill(killProcess)
					}
					return
				}
			}
		}
	}
}

func (e *ProcessJobExecutor) Close(ctx context.Context) error {
	e.mu.Lock()
	cmd := e.cmd
	done := e.done
	started := e.started
	shutdownWriter := e.pingWriter
	shutdownAck := e.shutdownAck
	shuttingDown := e.shuttingDown
	e.mu.Unlock()
	if !started || done == nil {
		return nil
	}

	if cmd != nil && cmd.Process != nil {
		shutdownSent := false
		if shutdownWriter != nil {
			msg, err := NewMessage(&ShutdownRequest{})
			if err == nil {
				err = WriteMessage(shutdownWriter, msg)
			}
			if err != nil {
				logger.Logger.Warnw("failed to send job process shutdown request", err, "exec_id", e.id)
			} else {
				shutdownSent = true
			}
		}

		e.mu.Lock()
		if e.status == JobStatusRunning {
			e.status = JobStatusFailed
		}
		e.mu.Unlock()

		if shutdownSent {
			acked := false
			if processShutdownGraceTimeout > 0 {
				graceTimer := time.NewTimer(processShutdownGraceTimeout)
				select {
				case <-done:
					graceTimer.Stop()
					return nil
				case <-shutdownAck:
					acked = true
					graceTimer.Stop()
				case <-ctx.Done():
					graceTimer.Stop()
				case <-graceTimer.C:
				}
			}
			if acked && processShutdownGraceTimeout > 0 {
				shuttingDownTimer := time.NewTimer(processShutdownGraceTimeout)
				select {
				case <-done:
					shuttingDownTimer.Stop()
					return nil
				case <-shuttingDown:
					shuttingDownTimer.Stop()
				case <-ctx.Done():
					shuttingDownTimer.Stop()
				case <-shuttingDownTimer.C:
				}
			}
			if acked && processShutdownGraceTimeout > 0 {
				exitTimer := time.NewTimer(processShutdownGraceTimeout)
				select {
				case <-done:
					exitTimer.Stop()
					return nil
				case <-ctx.Done():
					exitTimer.Stop()
				case <-exitTimer.C:
				}
			}
		}

		killDone := make(chan error, 1)
		go func() {
			killDone <- processKill(cmd.Process)
		}()
		select {
		case err := <-killDone:
			if err != nil && !strings.Contains(err.Error(), "process already finished") {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
