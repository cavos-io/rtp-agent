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
	status     JobStatus
	launchErr  error
	closeCtx   context.Context
	closeCalls int
	launches   int
}

func (e *fakeJobExecutor) ID() string { return e.id }

func (e *fakeJobExecutor) Status() JobStatus {
	if e.status != "" {
		return e.status
	}
	return JobStatusRunning
}

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

func TestProcPoolActiveJobsReturnsRunningAssignments(t *testing.T) {
	runningA := RunningJobInfo{
		AcceptArguments: JobAcceptArguments{
			Identity:   "agent-job-a",
			Attributes: map[string]string{"tier": "gold"},
		},
		Job:      &livekit.Job{Id: "job-a"},
		URL:      "wss://livekit.example",
		Token:    "token-a",
		WorkerID: "worker-a",
		FakeJob:  true,
	}
	runningB := RunningJobInfo{
		AcceptArguments: JobAcceptArguments{
			Identity: "agent-job-b",
		},
		Job:      &livekit.Job{Id: "job-b"},
		URL:      "wss://livekit.example",
		Token:    "token-b",
		WorkerID: "worker-a",
	}
	pool := &ProcPool{
		executors: map[string]JobExecutor{
			"exec-a": &fakeJobExecutor{id: "exec-a", runningJob: &runningA},
			"exec-b": &fakeJobExecutor{id: "exec-b", runningJob: &runningB},
			"idle":   &fakeJobExecutor{id: "idle"},
		},
	}

	activeJobs := pool.ActiveJobs()
	if len(activeJobs) != 2 {
		t.Fatalf("ActiveJobs() len = %d, want 2", len(activeJobs))
	}

	got := map[string]RunningJobInfo{}
	for _, info := range activeJobs {
		got[info.Job.GetId()] = info
	}
	if got["job-a"].AcceptArguments.Identity != "agent-job-a" {
		t.Fatalf("job-a identity = %q, want agent-job-a", got["job-a"].AcceptArguments.Identity)
	}
	if got["job-a"].Token != "token-a" {
		t.Fatalf("job-a token = %q, want token-a", got["job-a"].Token)
	}
	if !got["job-a"].FakeJob {
		t.Fatal("job-a FakeJob = false, want true")
	}
	if got["job-b"].AcceptArguments.Identity != "agent-job-b" {
		t.Fatalf("job-b identity = %q, want agent-job-b", got["job-b"].AcceptArguments.Identity)
	}

	activeJobs[0].Token = "mutated"
	if got := pool.ActiveJobs(); got[0].Token == "mutated" {
		t.Fatal("mutating ActiveJobs() result changed stored running job")
	}

	for i := range activeJobs {
		if activeJobs[i].Job.GetId() == "job-a" {
			activeJobs[i].AcceptArguments.Attributes["tier"] = "platinum"
		}
	}
	refreshed := pool.ActiveJobs()
	for _, info := range refreshed {
		if info.Job.GetId() == "job-a" && info.AcceptArguments.Attributes["tier"] != "gold" {
			t.Fatalf("mutating ActiveJobs() attributes changed stored running job to %q, want gold", info.AcceptArguments.Attributes["tier"])
		}
	}
}

func TestProcPoolActiveJobsSkipsCompletedExecutors(t *testing.T) {
	completed := RunningJobInfo{Job: &livekit.Job{Id: "job-done"}}
	running := RunningJobInfo{Job: &livekit.Job{Id: "job-running"}}
	pool := &ProcPool{
		executors: map[string]JobExecutor{
			"done":    &fakeJobExecutor{id: "done", job: completed.Job, runningJob: &completed, status: JobStatusSuccess},
			"running": &fakeJobExecutor{id: "running", job: running.Job, runningJob: &running, status: JobStatusRunning},
		},
	}

	activeJobs := pool.ActiveJobs()

	if len(activeJobs) != 1 {
		t.Fatalf("ActiveJobs() len = %d, want only running job", len(activeJobs))
	}
	if activeJobs[0].Job.GetId() != "job-running" {
		t.Fatalf("ActiveJobs()[0].Job.Id = %q, want job-running", activeJobs[0].Job.GetId())
	}
}

func TestProcPoolGetExecutorsSkipsCompletedExecutors(t *testing.T) {
	completed := &fakeJobExecutor{
		id:     "done",
		job:    &livekit.Job{Id: "job-done"},
		status: JobStatusSuccess,
	}
	running := &fakeJobExecutor{
		id:     "running",
		job:    &livekit.Job{Id: "job-running"},
		status: JobStatusRunning,
	}
	pool := &ProcPool{
		executors: map[string]JobExecutor{
			completed.id: completed,
			running.id:   running,
		},
	}

	executors := pool.GetExecutors()

	if len(executors) != 1 {
		t.Fatalf("GetExecutors() len = %d, want only running executor", len(executors))
	}
	if executors[0].ID() != "running" {
		t.Fatalf("GetExecutors()[0].ID() = %q, want running", executors[0].ID())
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
	if got := pool.TargetIdleProcesses(); got != 10 {
		t.Fatalf("TargetIdleProcesses after high value = %d, want raw configured value", got)
	}

	pool.SetTargetIdleProcesses(-1)
	if got := pool.TargetIdleProcesses(); got != -1 {
		t.Fatalf("TargetIdleProcesses after negative value = %d, want raw configured value", got)
	}
}

func TestProcPoolHighTargetIdleProcessesStillWarmsOnlyCapacity(t *testing.T) {
	pool := NewProcPool(2, ExecutorTypeThread, nil)
	pool.SetTargetIdleProcesses(10)

	if err := pool.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if got := len(pool.GetExecutors()); got != 2 {
		t.Fatalf("warmed executors = %d, want capped process capacity", got)
	}
	if got := pool.TargetIdleProcesses(); got != 10 {
		t.Fatalf("TargetIdleProcesses = %d, want raw configured value", got)
	}
}

func TestProcPoolStartWarmsTargetIdleExecutors(t *testing.T) {
	executors := []*fakeJobExecutor{
		{id: "exec-a"},
		{id: "exec-b"},
	}
	var created int
	pool := NewProcPool(3, ExecutorTypeThread, nil)
	pool.SetTargetIdleProcesses(2)
	pool.executorFactory = func(id string) JobExecutor {
		executor := executors[created]
		created++
		return executor
	}

	var events []ProcPoolEvent
	for _, event := range []ProcPoolEvent{
		ProcPoolEventProcessCreated,
		ProcPoolEventProcessStarted,
		ProcPoolEventProcessReady,
	} {
		event := event
		pool.On(event, func(executor JobExecutor) {
			events = append(events, event)
			if executor.Started() {
				t.Fatalf("%s executor %q is running a job, want warmed idle executor", event, executor.ID())
			}
		})
	}

	if err := pool.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := pool.Start(context.Background()); err != nil {
		t.Fatalf("second Start: %v", err)
	}

	if created != 2 {
		t.Fatalf("created executors = %d, want 2", created)
	}
	gotExecutors := pool.GetExecutors()
	if len(gotExecutors) != 2 {
		t.Fatalf("executors len = %d, want 2", len(gotExecutors))
	}
	for _, executor := range executors {
		if executor.launches != 0 {
			t.Fatalf("%s launches = %d, want 0", executor.id, executor.launches)
		}
	}

	wantEvents := []ProcPoolEvent{
		ProcPoolEventProcessCreated,
		ProcPoolEventProcessStarted,
		ProcPoolEventProcessReady,
		ProcPoolEventProcessCreated,
		ProcPoolEventProcessStarted,
		ProcPoolEventProcessReady,
	}
	if len(events) != len(wantEvents) {
		t.Fatalf("events = %v, want %v", events, wantEvents)
	}
	for i := range wantEvents {
		if events[i] != wantEvents[i] {
			t.Fatalf("events = %v, want %v", events, wantEvents)
		}
	}
}

func TestProcPoolLaunchUsesWarmedIdleExecutor(t *testing.T) {
	idle := &fakeJobExecutor{id: "idle"}
	var created int
	pool := NewProcPool(1, ExecutorTypeThread, nil)
	pool.SetTargetIdleProcesses(1)
	pool.executorFactory = func(id string) JobExecutor {
		created++
		return idle
	}

	eventCounts := map[ProcPoolEvent]int{}
	for _, event := range []ProcPoolEvent{
		ProcPoolEventProcessCreated,
		ProcPoolEventProcessStarted,
		ProcPoolEventProcessReady,
		ProcPoolEventJobLaunched,
	} {
		event := event
		pool.On(event, func(executor JobExecutor) {
			if executor.ID() != "idle" {
				t.Fatalf("%s executor = %q, want idle", event, executor.ID())
			}
			eventCounts[event]++
		})
	}

	if err := pool.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := pool.LaunchJob(context.Background(), &livekit.Job{Id: "job-a"}); err != nil {
		t.Fatalf("LaunchJob: %v", err)
	}

	if created != 1 {
		t.Fatalf("created executors = %d, want warmed executor only", created)
	}
	if idle.launches != 1 {
		t.Fatalf("idle launches = %d, want 1", idle.launches)
	}
	if idle.Job().GetId() != "job-a" {
		t.Fatalf("idle job = %q, want job-a", idle.Job().GetId())
	}
	for _, event := range []ProcPoolEvent{
		ProcPoolEventProcessCreated,
		ProcPoolEventProcessStarted,
		ProcPoolEventProcessReady,
		ProcPoolEventJobLaunched,
	} {
		if eventCounts[event] != 1 {
			t.Fatalf("%s count = %d, want 1", event, eventCounts[event])
		}
	}
}

func TestProcPoolLaunchRefillsTargetIdleAfterUsingWarmedExecutor(t *testing.T) {
	running := &fakeJobExecutor{id: "running"}
	replacement := &fakeJobExecutor{id: "replacement"}
	executors := []*fakeJobExecutor{running, replacement}
	var created int
	pool := NewProcPool(2, ExecutorTypeThread, nil)
	pool.SetTargetIdleProcesses(1)
	pool.executorFactory = func(id string) JobExecutor {
		executor := executors[created]
		created++
		return executor
	}

	ready := map[string]int{}
	pool.On(ProcPoolEventProcessReady, func(executor JobExecutor) {
		ready[executor.ID()]++
	})

	if err := pool.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := pool.LaunchJob(context.Background(), &livekit.Job{Id: "job-a"}); err != nil {
		t.Fatalf("LaunchJob: %v", err)
	}

	if created != 2 {
		t.Fatalf("created executors = %d, want warmed executor plus replacement", created)
	}
	if running.launches != 1 {
		t.Fatalf("running launches = %d, want 1", running.launches)
	}
	if replacement.launches != 0 {
		t.Fatalf("replacement launches = %d, want idle replacement", replacement.launches)
	}
	if replacement.Started() {
		t.Fatal("replacement Started() = true, want warmed idle executor")
	}
	if ready["running"] != 1 || ready["replacement"] != 1 {
		t.Fatalf("ready events = %#v, want one warm-ready event for running and replacement", ready)
	}

	gotExecutors := pool.GetExecutors()
	if len(gotExecutors) != 2 {
		t.Fatalf("executors len = %d, want running plus idle replacement", len(gotExecutors))
	}
	if pool.GetByJobID("job-a") != running {
		t.Fatal("GetByJobID(job-a) did not return warmed executor used for launch")
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

func TestProcPoolLaunchReusesCapacityAfterExecutorCompletes(t *testing.T) {
	completed := make(chan struct{}, 2)
	pool := NewProcPool(1, ExecutorTypeThread, func() error {
		completed <- struct{}{}
		return nil
	})

	if err := pool.LaunchJob(context.Background(), &livekit.Job{Id: "job-a"}); err != nil {
		t.Fatalf("first LaunchJob: %v", err)
	}
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("first job did not complete")
	}
	waitForProcPoolExecutorStatus(t, pool, JobStatusSuccess)

	if err := pool.LaunchJob(context.Background(), &livekit.Job{Id: "job-b"}); err != nil {
		t.Fatalf("second LaunchJob after completed executor: %v", err)
	}
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("second job did not complete")
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

func waitForProcPoolExecutorStatus(t *testing.T, pool *ProcPool, status JobStatus) {
	t.Helper()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			t.Fatalf("executor did not reach status %q", status)
		case <-ticker.C:
			pool.mu.Lock()
			for _, executor := range pool.executors {
				if executor.Status() == status {
					pool.mu.Unlock()
					return
				}
			}
			pool.mu.Unlock()
		}
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

func TestProcPoolEmitsJobLaunchedEvent(t *testing.T) {
	executor := &fakeJobExecutor{id: "exec-a"}
	pool := NewProcPool(1, ExecutorTypeThread, nil)
	pool.executorFactory = func(id string) JobExecutor { return executor }

	var launched []string
	pool.On(ProcPoolEventJobLaunched, func(executor JobExecutor) {
		launched = append(launched, executor.ID())
	})

	if err := pool.LaunchJob(context.Background(), &livekit.Job{Id: "job-a"}); err != nil {
		t.Fatalf("LaunchJob: %v", err)
	}

	if len(launched) != 1 {
		t.Fatalf("launched events = %d, want 1", len(launched))
	}
	if launched[0] != "exec-a" {
		t.Fatalf("launched executor = %q, want exec-a", launched[0])
	}
}

func TestProcPoolEventHandlersCanInspectPool(t *testing.T) {
	executor := &fakeJobExecutor{id: "exec-a"}
	pool := NewProcPool(1, ExecutorTypeThread, nil)
	pool.executorFactory = func(id string) JobExecutor { return executor }

	pool.On(ProcPoolEventJobLaunched, func(JobExecutor) {
		if got := pool.GetByJobID("job-a"); got == nil {
			t.Fatal("GetByJobID() from event handler returned nil")
		}
	})

	done := make(chan error, 1)
	go func() {
		done <- pool.LaunchJob(context.Background(), &livekit.Job{Id: "job-a"})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("LaunchJob: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("LaunchJob deadlocked while event handler inspected pool")
	}
}

func TestProcPoolEventHandlerPanicDoesNotBlockOtherHandlers(t *testing.T) {
	executor := &fakeJobExecutor{id: "exec-a"}
	pool := NewProcPool(1, ExecutorTypeThread, nil)
	pool.executorFactory = func(id string) JobExecutor { return executor }

	var calls int
	pool.On(ProcPoolEventJobLaunched, func(JobExecutor) {
		panic("proc pool handler failed")
	})
	pool.On(ProcPoolEventJobLaunched, func(got JobExecutor) {
		if got.ID() != "exec-a" {
			t.Fatalf("executor = %q, want exec-a", got.ID())
		}
		calls++
	})

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("LaunchJob panic = %v, want event handler panic isolated", recovered)
		}
	}()

	if err := pool.LaunchJob(context.Background(), &livekit.Job{Id: "job-a"}); err != nil {
		t.Fatalf("LaunchJob: %v", err)
	}

	if calls != 1 {
		t.Fatalf("remaining handler calls = %d, want 1", calls)
	}
}

func TestProcPoolEmitsProcessLifecycleEventsOnLaunch(t *testing.T) {
	executor := &fakeJobExecutor{id: "exec-a"}
	pool := NewProcPool(1, ExecutorTypeThread, nil)
	pool.executorFactory = func(id string) JobExecutor { return executor }

	var events []ProcPoolEvent
	for _, event := range []ProcPoolEvent{
		ProcPoolEventProcessCreated,
		ProcPoolEventProcessStarted,
		ProcPoolEventProcessReady,
	} {
		event := event
		pool.On(event, func(executor JobExecutor) {
			if executor.ID() != "exec-a" {
				t.Fatalf("%s executor = %q, want exec-a", event, executor.ID())
			}
			events = append(events, event)
		})
	}

	if err := pool.LaunchJob(context.Background(), &livekit.Job{Id: "job-a"}); err != nil {
		t.Fatalf("LaunchJob: %v", err)
	}

	wantEvents := []ProcPoolEvent{
		ProcPoolEventProcessCreated,
		ProcPoolEventProcessStarted,
		ProcPoolEventProcessReady,
	}
	if len(events) != len(wantEvents) {
		t.Fatalf("events = %v, want %v", events, wantEvents)
	}
	for i := range wantEvents {
		if events[i] != wantEvents[i] {
			t.Fatalf("events = %v, want %v", events, wantEvents)
		}
	}
}

func TestProcPoolEmitsProcessClosedEventForFailedLaunch(t *testing.T) {
	pool := NewProcPool(1, ExecutorTypeThread, nil)
	created := 0
	pool.executorFactory = func(id string) JobExecutor {
		created++
		return &fakeJobExecutor{id: id, launchErr: errors.New("launch failed")}
	}

	var closed []string
	pool.On(ProcPoolEventProcessClosed, func(executor JobExecutor) {
		closed = append(closed, executor.ID())
	})

	err := pool.LaunchJob(context.Background(), &livekit.Job{Id: "job-a"})
	if err == nil {
		t.Fatal("LaunchJob error = nil, want launch failure")
	}

	if created != maxLaunchAttempts {
		t.Fatalf("created executors = %d, want %d", created, maxLaunchAttempts)
	}
	if len(closed) != maxLaunchAttempts {
		t.Fatalf("closed events = %d, want %d", len(closed), maxLaunchAttempts)
	}
	for i, executorID := range closed {
		if executorID == "" {
			t.Fatalf("closed[%d] executor ID is empty", i)
		}
	}
}

func TestProcPoolEmitsProcessClosedEvent(t *testing.T) {
	executor := &fakeJobExecutor{id: "exec-a"}
	pool := NewProcPool(1, ExecutorTypeThread, nil)
	pool.executors[executor.id] = executor

	var closed []string
	pool.On(ProcPoolEventProcessClosed, func(executor JobExecutor) {
		closed = append(closed, executor.ID())
	})

	if err := pool.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if len(closed) != 1 {
		t.Fatalf("closed events = %d, want 1", len(closed))
	}
	if closed[0] != "exec-a" {
		t.Fatalf("closed executor = %q, want exec-a", closed[0])
	}
}
