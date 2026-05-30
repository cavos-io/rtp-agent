package ipc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/livekit/protocol/livekit"
)

type fakeJobExecutor struct {
	id         string
	job        *livekit.Job
	runningJob *RunningJobInfo
	launchErr  error
	closeCtx   context.Context
	closeCalls int
	launches   int
}

func (e *fakeJobExecutor) ID() string { return e.id }

func (e *fakeJobExecutor) Status() JobStatus { return JobStatusRunning }

func (e *fakeJobExecutor) Started() bool { return e.job != nil }

func (e *fakeJobExecutor) Job() *livekit.Job { return e.job }

func (e *fakeJobExecutor) RunningJob() *RunningJobInfo { return e.runningJob }

func (e *fakeJobExecutor) LaunchJob(ctx context.Context, job *livekit.Job) error {
	return e.LaunchRunningJob(ctx, RunningJobInfo{Job: job})
}

func (e *fakeJobExecutor) LaunchRunningJob(ctx context.Context, info RunningJobInfo) error {
	e.launches++
	if e.launchErr != nil {
		return e.launchErr
	}
	e.job = info.Job
	e.runningJob = &info
	return nil
}

func (e *fakeJobExecutor) Close(ctx context.Context) error {
	e.closeCtx = ctx
	e.closeCalls++
	return nil
}

func TestProcPoolGetByJobIDFindsRunningExecutor(t *testing.T) {
	executor := &fakeJobExecutor{
		id:  "exec-a",
		job: &livekit.Job{Id: "job-a"},
	}
	pool := &ProcPool{
		executors: map[string]JobExecutor{executor.id: executor},
	}

	got := pool.GetByJobID("job-a")
	if got == nil {
		t.Fatal("GetByJobID returned nil, want executor")
	}
	if got.ID() != "exec-a" {
		t.Fatalf("executor ID = %q, want exec-a", got.ID())
	}
	if pool.GetByJobID("missing") != nil {
		t.Fatal("GetByJobID returned executor for missing job")
	}
}

func TestProcPoolCloseUsesConfiguredTimeout(t *testing.T) {
	executor := &fakeJobExecutor{id: "exec-a"}
	pool := &ProcPool{
		executors:    map[string]JobExecutor{executor.id: executor},
		closeTimeout: 25 * time.Millisecond,
	}

	if err := pool.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if executor.closeCalls != 1 {
		t.Fatalf("closeCalls = %d, want 1", executor.closeCalls)
	}
	deadline, ok := executor.closeCtx.Deadline()
	if !ok {
		t.Fatal("Close context has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > 25*time.Millisecond {
		t.Fatalf("Close deadline remaining = %v, want within configured timeout", remaining)
	}
}

func TestProcPoolTargetIdleProcesses(t *testing.T) {
	pool := NewProcPool(4, ExecutorTypeThread, nil)

	if got := pool.TargetIdleProcesses(); got != 0 {
		t.Fatalf("TargetIdleProcesses = %d, want 0", got)
	}

	pool.SetTargetIdleProcesses(2)
	if got := pool.TargetIdleProcesses(); got != 2 {
		t.Fatalf("TargetIdleProcesses = %d, want 2", got)
	}

	pool.SetTargetIdleProcesses(10)
	if got := pool.TargetIdleProcesses(); got != 4 {
		t.Fatalf("TargetIdleProcesses after high value = %d, want capped max", got)
	}

	pool.SetTargetIdleProcesses(-1)
	if got := pool.TargetIdleProcesses(); got != 0 {
		t.Fatalf("TargetIdleProcesses after negative value = %d, want 0", got)
	}
}

func TestProcPoolLaunchAfterCloseIsRejected(t *testing.T) {
	var created int
	pool := NewProcPool(1, ExecutorTypeThread, nil)
	pool.executorFactory = func(id string) JobExecutor {
		created++
		return &fakeJobExecutor{id: id}
	}

	if err := pool.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	err := pool.LaunchJob(context.Background(), &livekit.Job{Id: "job-a"})
	if err == nil {
		t.Fatal("LaunchJob error = nil, want closed pool error")
	}
	if !errors.Is(err, ErrProcPoolClosed) {
		t.Fatalf("LaunchJob error = %v, want ErrProcPoolClosed", err)
	}
	if created != 0 {
		t.Fatalf("created executors = %d, want 0", created)
	}
}

func TestProcPoolLaunchJobRetriesWithFreshExecutor(t *testing.T) {
	first := &fakeJobExecutor{id: "exec-a", launchErr: errors.New("launch failed")}
	second := &fakeJobExecutor{id: "exec-b"}
	executors := []*fakeJobExecutor{first, second}
	var created int

	pool := NewProcPool(1, ExecutorTypeThread, nil)
	pool.executorFactory = func(id string) JobExecutor {
		executor := executors[created]
		created++
		return executor
	}

	err := pool.LaunchJob(context.Background(), &livekit.Job{Id: "job-a"})
	if err != nil {
		t.Fatalf("LaunchJob: %v", err)
	}
	if created != 2 {
		t.Fatalf("created executors = %d, want 2", created)
	}
	if first.launches != 1 {
		t.Fatalf("first launches = %d, want 1", first.launches)
	}
	if first.closeCalls != 1 {
		t.Fatalf("first closeCalls = %d, want 1", first.closeCalls)
	}
	if second.launches != 1 {
		t.Fatalf("second launches = %d, want 1", second.launches)
	}
	if pool.GetByJobID("job-a") == nil {
		t.Fatal("GetByJobID returned nil for retried job")
	}

	gotExecutors := pool.GetExecutors()
	if len(gotExecutors) != 1 {
		t.Fatalf("executors len = %d, want 1", len(gotExecutors))
	}
	if gotExecutors[0].ID() != "exec-b" {
		t.Fatalf("remaining executor ID = %q, want exec-b", gotExecutors[0].ID())
	}
}

func TestProcPoolLaunchRunningJobPreservesAssignmentInfo(t *testing.T) {
	executor := &fakeJobExecutor{id: "exec-a"}
	pool := NewProcPool(1, ExecutorTypeThread, nil)
	pool.executorFactory = func(id string) JobExecutor { return executor }

	info := RunningJobInfo{
		AcceptArguments: JobAcceptArguments{
			Name:     "support",
			Identity: "agent-job-a",
			Metadata: `{"tier":"gold"}`,
		},
		Job:      &livekit.Job{Id: "job-a"},
		URL:      "wss://livekit.example",
		Token:    "room-token",
		WorkerID: "worker-a",
		FakeJob:  true,
	}

	if err := pool.LaunchRunningJob(context.Background(), info); err != nil {
		t.Fatalf("LaunchRunningJob: %v", err)
	}

	running := executor.RunningJob()
	if running == nil {
		t.Fatal("RunningJob = nil, want assignment info")
	}
	if running.AcceptArguments.Identity != "agent-job-a" {
		t.Fatalf("Identity = %q, want agent-job-a", running.AcceptArguments.Identity)
	}
	if running.URL != "wss://livekit.example" {
		t.Fatalf("URL = %q, want room URL", running.URL)
	}
	if running.Token != "room-token" {
		t.Fatalf("Token = %q, want room token", running.Token)
	}
	if running.WorkerID != "worker-a" {
		t.Fatalf("WorkerID = %q, want worker-a", running.WorkerID)
	}
	if !running.FakeJob {
		t.Fatal("FakeJob = false, want true")
	}
}
