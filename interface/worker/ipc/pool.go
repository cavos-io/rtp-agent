package ipc

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/google/uuid"
	"github.com/livekit/protocol/livekit"
)

type ExecutorType string

const (
	ExecutorTypeThread  ExecutorType = "thread"
	ExecutorTypeProcess ExecutorType = "process"
)

type ProcPool struct {
	maxProcesses int
	executors    map[string]JobExecutor
	mu           sync.Mutex
	entrypoint   func() error
	executorType ExecutorType
}

func NewProcPool(maxProcesses int, executorType ExecutorType, entrypoint func() error) *ProcPool {
	return &ProcPool{
		maxProcesses: maxProcesses,
		executors:    make(map[string]JobExecutor),
		entrypoint:   entrypoint,
		executorType: executorType,
	}
}

func (p *ProcPool) LaunchJob(ctx context.Context, job *livekit.Job) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.executors) >= p.maxProcesses {
		return fmt.Errorf("proc pool exhausted, max capacity reached")
	}

	id := "exec_" + uuid.NewString()[:8]
	var executor JobExecutor
	if p.executorType == ExecutorTypeProcess {
		executor = NewProcessJobExecutor(id)
	} else {
		executor = NewThreadJobExecutor(id, p.entrypoint)
	}
	
	p.executors[id] = executor

	err := executor.LaunchJob(ctx, job)
	if err != nil {
		delete(p.executors, id)
		return err
	}

	logger.Logger.Infow("Launched job", "executor_type", p.executorType, "executor_id", id, "job_id", job.Id)
	return nil
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

func (p *ProcPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, e := range p.executors {
		_ = e.Close(ctx)
	}
	p.executors = make(map[string]JobExecutor)
	return nil
}

