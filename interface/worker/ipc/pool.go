package ipc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/library/logger"
	"github.com/google/uuid"
	"github.com/livekit/protocol/livekit"
)

type ExecutorType string

const (
	ExecutorTypeThread  ExecutorType = "thread"
	ExecutorTypeProcess ExecutorType = "process"
)

type ProcPoolEvent string

const (
	ProcPoolEventProcessCreated ProcPoolEvent = "process_created"
	ProcPoolEventProcessStarted ProcPoolEvent = "process_started"
	ProcPoolEventProcessReady   ProcPoolEvent = "process_ready"
	ProcPoolEventJobLaunched    ProcPoolEvent = "process_job_launched"
	ProcPoolEventProcessClosed  ProcPoolEvent = "process_closed"
)

const maxLaunchAttempts = 3

var ErrProcPoolClosed = errors.New("proc pool closed")

type ProcPool struct {
	maxProcesses    int
	executors       map[string]JobExecutor
	mu              sync.Mutex
	entrypoint      func() error
	executorType    ExecutorType
	closeTimeout    time.Duration
	closed          bool
	targetIdle      int
	handlers        map[ProcPoolEvent][]func(JobExecutor)
	executorFactory func(id string) JobExecutor
}

func NewProcPool(maxProcesses int, executorType ExecutorType, entrypoint func() error) *ProcPool {
	pool := &ProcPool{
		maxProcesses: maxProcesses,
		executors:    make(map[string]JobExecutor),
		entrypoint:   entrypoint,
		executorType: executorType,
		closeTimeout: 5 * time.Second,
		handlers:     make(map[ProcPoolEvent][]func(JobExecutor)),
	}
	pool.executorFactory = pool.newExecutor
	return pool
}

func (p *ProcPool) newExecutor(id string) JobExecutor {
	if p.executorType == ExecutorTypeProcess {
		return NewProcessJobExecutor(id)
	}
	return NewThreadJobExecutor(id, p.entrypoint)
}

func (p *ProcPool) LaunchJob(ctx context.Context, job *livekit.Job) error {
	return p.LaunchRunningJob(ctx, RunningJobInfo{Job: job})
}

func (p *ProcPool) LaunchRunningJob(ctx context.Context, info RunningJobInfo) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrProcPoolClosed
	}

	var lastErr error
	for attempt := 0; attempt < maxLaunchAttempts; attempt++ {
		if len(p.executors) >= p.maxProcesses {
			return fmt.Errorf("proc pool exhausted, max capacity reached")
		}

		id := "exec_" + uuid.NewString()[:8]
		executor := p.executorFactory(id)
		p.executors[id] = executor
		p.emit(ProcPoolEventProcessCreated, executor)

		err := executor.LaunchRunningJob(ctx, info)
		if err != nil {
			delete(p.executors, id)
			closeCtx, cancel := p.closeContext()
			_ = executor.Close(closeCtx)
			cancel()
			p.emit(ProcPoolEventProcessClosed, executor)
			lastErr = err
			continue
		}

		p.emit(ProcPoolEventProcessStarted, executor)
		p.emit(ProcPoolEventProcessReady, executor)
		logger.Logger.Infow("Launched job", "executor_type", p.executorType, "executor_id", id, "job_id", info.Job.GetId())
		p.emit(ProcPoolEventJobLaunched, executor)
		return nil
	}

	return lastErr
}

func (p *ProcPool) GetExecutors() []JobExecutor {
	p.mu.Lock()
	defer p.mu.Unlock()

	executors := make([]JobExecutor, 0, len(p.executors))
	for _, e := range p.executors {
		executors = append(executors, e)
	}
	return executors
}

func (p *ProcPool) SetCloseTimeout(timeout time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeTimeout = timeout
}

func (p *ProcPool) On(event ProcPoolEvent, handler func(JobExecutor)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.handlers == nil {
		p.handlers = make(map[ProcPoolEvent][]func(JobExecutor))
	}
	p.handlers[event] = append(p.handlers[event], handler)
}

func (p *ProcPool) SetTargetIdleProcesses(numIdleProcesses int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.targetIdle = clampInt(numIdleProcesses, 0, p.maxProcesses)
}

func (p *ProcPool) TargetIdleProcesses() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.targetIdle
}

func (p *ProcPool) GetByJobID(jobID string) JobExecutor {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, executor := range p.executors {
		job := executor.Job()
		if job != nil && job.Id == jobID {
			return executor
		}
	}
	return nil
}

func (p *ProcPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.closed = true
	ctx, cancel := p.closeContext()
	defer cancel()

	for _, e := range p.executors {
		_ = e.Close(ctx)
		p.emit(ProcPoolEventProcessClosed, e)
	}
	p.executors = make(map[string]JobExecutor)
	return nil
}

func (p *ProcPool) closeContext() (context.Context, context.CancelFunc) {
	closeTimeout := p.closeTimeout
	if closeTimeout <= 0 {
		closeTimeout = 5 * time.Second
	}
	return context.WithTimeout(context.Background(), closeTimeout)
}

func clampInt(value int, min int, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func (p *ProcPool) emit(event ProcPoolEvent, executor JobExecutor) {
	for _, handler := range p.handlers[event] {
		handler(executor)
	}
}
