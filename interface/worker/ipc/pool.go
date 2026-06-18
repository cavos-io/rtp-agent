package ipc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/google/uuid"
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
	started         bool
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
	switch p.executorType {
	case ExecutorTypeProcess:
		return NewProcessJobExecutor(id)
	case ExecutorTypeThread:
		return NewThreadJobExecutor(id, p.entrypoint)
	default:
		return nil
	}
}

func (p *ProcPool) validateExecutorType() error {
	switch p.executorType {
	case ExecutorTypeThread, ExecutorTypeProcess:
		return nil
	default:
		return fmt.Errorf("unsupported job executor: %s", p.executorType)
	}
}

func (p *ProcPool) LaunchJob(ctx context.Context, job *workerlivekit.Job) error {
	return p.LaunchRunningJob(ctx, RunningJobInfo{Job: job})
}

func (p *ProcPool) Start(ctx context.Context) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrProcPoolClosed
	}
	p.started = true

	closedExecutors := p.pruneFinishedExecutorsLocked()
	warmedExecutors, err := p.warmIdleExecutorsLocked()
	p.mu.Unlock()

	p.emitMany(ProcPoolEventProcessClosed, closedExecutors)
	if err != nil {
		return err
	}
	return p.emitWarmedExecutors(ctx, warmedExecutors)
}

func (p *ProcPool) LaunchRunningJob(ctx context.Context, info RunningJobInfo) error {
	var lastErr error
	for attempt := 0; attempt < maxLaunchAttempts; attempt++ {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return ErrProcPoolClosed
		}

		closedExecutors := p.pruneFinishedExecutorsLocked()
		if err := p.validateExecutorType(); err != nil {
			p.mu.Unlock()
			p.emitMany(ProcPoolEventProcessClosed, closedExecutors)
			return err
		}
		executor := p.idleExecutorLocked()
		executorWarmed := executor != nil
		if executor == nil && len(p.executors) >= p.maxProcesses {
			p.mu.Unlock()
			p.emitMany(ProcPoolEventProcessClosed, closedExecutors)
			return fmt.Errorf("proc pool exhausted, max capacity reached")
		}

		if executor == nil {
			executor = p.createExecutorLocked()
		}
		id := executor.ID()
		p.mu.Unlock()

		p.emitMany(ProcPoolEventProcessClosed, closedExecutors)
		if !executorWarmed {
			p.emit(ProcPoolEventProcessCreated, executor)
		}

		err := executor.LaunchRunningJob(ctx, info)
		if err != nil {
			p.mu.Lock()
			delete(p.executors, id)
			p.mu.Unlock()

			closeCtx, cancel := p.closeContext()
			_ = executor.Close(closeCtx)
			cancel()
			p.emit(ProcPoolEventProcessClosed, executor)
			lastErr = err
			continue
		}

		if !executorWarmed {
			p.emit(ProcPoolEventProcessStarted, executor)
			p.emit(ProcPoolEventProcessReady, executor)
		}
		logger.Logger.Infow("Launched job", "executor_type", p.executorType, "executor_id", id, "job_id", info.Job.GetId())
		p.emit(ProcPoolEventJobLaunched, executor)
		p.mu.Lock()
		warmedExecutors, err := p.warmIdleExecutorsLocked()
		p.mu.Unlock()
		if err == nil {
			_ = p.emitWarmedExecutors(ctx, warmedExecutors)
		}
		return nil
	}

	return lastErr
}

func (p *ProcPool) warmIdleExecutorsLocked() ([]JobExecutor, error) {
	if err := p.validateExecutorType(); err != nil {
		return nil, err
	}
	idleNeeded := p.targetIdle - p.idleExecutorCountLocked()
	capacity := p.maxProcesses - len(p.executors)
	if idleNeeded > capacity {
		idleNeeded = capacity
	}
	if idleNeeded < 0 {
		idleNeeded = 0
	}

	warmedExecutors := make([]JobExecutor, 0, idleNeeded)
	for i := 0; i < idleNeeded; i++ {
		executor := p.createExecutorLocked()
		warmedExecutors = append(warmedExecutors, executor)
	}
	return warmedExecutors, nil
}

func (p *ProcPool) emitWarmedExecutors(ctx context.Context, executors []JobExecutor) error {
	for _, executor := range executors {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		p.emit(ProcPoolEventProcessCreated, executor)
		p.emit(ProcPoolEventProcessStarted, executor)
		p.emit(ProcPoolEventProcessReady, executor)
	}
	return nil
}

func (p *ProcPool) createExecutorLocked() JobExecutor {
	id := "exec_" + uuid.NewString()[:8]
	executor := p.executorFactory(id)
	p.executors[executor.ID()] = executor
	return executor
}

func (p *ProcPool) idleExecutorLocked() JobExecutor {
	for _, executor := range p.executors {
		if !executor.Started() {
			return executor
		}
	}
	return nil
}

func (p *ProcPool) idleExecutorCountLocked() int {
	count := 0
	for _, executor := range p.executors {
		if !executor.Started() {
			count++
		}
	}
	return count
}

func (p *ProcPool) pruneFinishedExecutorsLocked() []JobExecutor {
	closedExecutors := make([]JobExecutor, 0)
	for id, executor := range p.executors {
		if !executor.Started() || executor.Status() == JobStatusRunning {
			continue
		}
		delete(p.executors, id)
		closedExecutors = append(closedExecutors, executor)
	}
	return closedExecutors
}

func (p *ProcPool) GetExecutors() []JobExecutor {
	p.mu.Lock()
	closedExecutors := p.pruneFinishedExecutorsLocked()

	executors := make([]JobExecutor, 0, len(p.executors))
	for _, e := range p.executors {
		executors = append(executors, e)
	}
	p.mu.Unlock()

	p.emitMany(ProcPoolEventProcessClosed, closedExecutors)
	return executors
}

func (p *ProcPool) ActiveJobs() []RunningJobInfo {
	p.mu.Lock()
	closedExecutors := p.pruneFinishedExecutorsLocked()

	jobs := make([]RunningJobInfo, 0, len(p.executors))
	for _, executor := range p.executors {
		runningJob := executor.RunningJob()
		if runningJob == nil {
			continue
		}
		jobs = append(jobs, cloneRunningJobInfo(*runningJob))
	}
	p.mu.Unlock()

	p.emitMany(ProcPoolEventProcessClosed, closedExecutors)
	return jobs
}

func cloneRunningJobInfo(info RunningJobInfo) RunningJobInfo {
	return workerlivekit.CloneRunningJobInfo(info)
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
	p.targetIdle = numIdleProcesses
}

func (p *ProcPool) TargetIdleProcesses() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.targetIdle
}

func (p *ProcPool) GetByJobID(jobID string) JobExecutor {
	p.mu.Lock()
	closedExecutors := p.pruneFinishedExecutorsLocked()

	for _, executor := range p.executors {
		job := executor.Job()
		if job != nil && job.Id == jobID {
			p.mu.Unlock()
			p.emitMany(ProcPoolEventProcessClosed, closedExecutors)
			return executor
		}
	}
	p.mu.Unlock()

	p.emitMany(ProcPoolEventProcessClosed, closedExecutors)
	return nil
}

func (p *ProcPool) Close() error {
	p.mu.Lock()
	if !p.started && len(p.executors) == 0 {
		p.mu.Unlock()
		return nil
	}
	p.closed = true

	executors := make([]JobExecutor, 0, len(p.executors))
	for _, e := range p.executors {
		executors = append(executors, e)
	}
	p.executors = make(map[string]JobExecutor)
	closeTimeout := p.closeTimeout
	p.mu.Unlock()

	ctx, cancel := closeContext(closeTimeout)
	defer cancel()
	for _, e := range executors {
		_ = e.Close(ctx)
		p.emit(ProcPoolEventProcessClosed, e)
	}
	return nil
}

func (p *ProcPool) closeContext() (context.Context, context.CancelFunc) {
	p.mu.Lock()
	closeTimeout := p.closeTimeout
	p.mu.Unlock()
	return closeContext(closeTimeout)
}

func closeContext(closeTimeout time.Duration) (context.Context, context.CancelFunc) {
	if closeTimeout <= 0 {
		closeTimeout = 5 * time.Second
	}
	return context.WithTimeout(context.Background(), closeTimeout)
}

func (p *ProcPool) emit(event ProcPoolEvent, executor JobExecutor) {
	if executor == nil {
		return
	}

	p.mu.Lock()
	handlers := append([]func(JobExecutor){}, p.handlers[event]...)
	p.mu.Unlock()
	for _, handler := range handlers {
		callProcPoolHandler(event, handler, executor)
	}
}

func callProcPoolHandler(event ProcPoolEvent, handler func(JobExecutor), executor JobExecutor) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Logger.Warnw("ProcPool event handler failed", fmt.Errorf("panic: %v", recovered), "event", event, "executor_id", executor.ID())
		}
	}()
	handler(executor)
}

func (p *ProcPool) emitMany(event ProcPoolEvent, executors []JobExecutor) {
	for _, executor := range executors {
		p.emit(event, executor)
	}
}
