package ipc

import (
	"context"
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

const maxLaunchAttempts = 3

type ProcPool struct {
	maxProcesses    int
	executors       map[string]JobExecutor
	mu              sync.Mutex
	entrypoint      func() error
	executorType    ExecutorType
	closeTimeout    time.Duration
	executorFactory func(id string) JobExecutor
}

func NewProcPool(maxProcesses int, executorType ExecutorType, entrypoint func() error) *ProcPool {
	pool := &ProcPool{
		maxProcesses: maxProcesses,
		executors:    make(map[string]JobExecutor),
		entrypoint:   entrypoint,
		executorType: executorType,
		closeTimeout: 5 * time.Second,
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
	p.mu.Lock()
	defer p.mu.Unlock()

	var lastErr error
	for attempt := 0; attempt < maxLaunchAttempts; attempt++ {
		if len(p.executors) >= p.maxProcesses {
			return fmt.Errorf("proc pool exhausted, max capacity reached")
		}

		id := "exec_" + uuid.NewString()[:8]
		executor := p.executorFactory(id)
		p.executors[id] = executor

		err := executor.LaunchJob(ctx, job)
		if err != nil {
			delete(p.executors, id)
			lastErr = err
			continue
		}

		logger.Logger.Infow("Launched job", "executor_type", p.executorType, "executor_id", id, "job_id", job.Id)
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

	closeTimeout := p.closeTimeout
	if closeTimeout <= 0 {
		closeTimeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()

	for _, e := range p.executors {
		_ = e.Close(ctx)
	}
	p.executors = make(map[string]JobExecutor)
	return nil
}
