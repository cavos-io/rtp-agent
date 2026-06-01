package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/livekit/protocol/livekit"
)

func TestThreadJobExecutorMarksPanicFailed(t *testing.T) {
	executor := NewThreadJobExecutor("exec-panic", func() error {
		panic("entrypoint panic")
	})

	if err := executor.LaunchJob(context.Background(), &livekit.Job{Id: "job-panic"}); err != nil {
		t.Fatalf("LaunchJob() error = %v", err)
	}

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatalf("executor status = %q, want %q", executor.Status(), JobStatusFailed)
		case <-ticker.C:
			if executor.Status() == JobStatusFailed {
				return
			}
		}
	}
}

func TestThreadJobExecutorCloseWaitsForEntrypoint(t *testing.T) {
	release := make(chan struct{})
	executor := NewThreadJobExecutor("exec-close", func() error {
		<-release
		return nil
	})

	if err := executor.LaunchJob(context.Background(), &livekit.Job{Id: "job-close"}); err != nil {
		t.Fatalf("LaunchJob() error = %v", err)
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- executor.Close(context.Background())
	}()

	select {
	case err := <-closeDone:
		t.Fatalf("Close() returned before entrypoint completed: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() did not return after entrypoint completed")
	}
}

func TestProcessJobEnvCarriesRunningJobInfo(t *testing.T) {
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

	env, err := processJobEnv([]string{"PATH=/bin"}, "exec-a", info)
	if err != nil {
		t.Fatalf("processJobEnv: %v", err)
	}

	values := envMap(env)
	if values["LIVEKIT_AGENT_PROCESS_ID"] != "exec-a" {
		t.Fatalf("process id = %q, want exec-a", values["LIVEKIT_AGENT_PROCESS_ID"])
	}

	var job livekit.Job
	if err := json.Unmarshal([]byte(values["LIVEKIT_AGENT_JOB_JSON"]), &job); err != nil {
		t.Fatalf("decode LIVEKIT_AGENT_JOB_JSON: %v", err)
	}
	if job.GetId() != "job-a" {
		t.Fatalf("job id = %q, want job-a", job.GetId())
	}

	var running RunningJobInfo
	if err := json.Unmarshal([]byte(values["LIVEKIT_AGENT_RUNNING_JOB_JSON"]), &running); err != nil {
		t.Fatalf("decode LIVEKIT_AGENT_RUNNING_JOB_JSON: %v", err)
	}
	if running.Job.GetId() != "job-a" {
		t.Fatalf("running job id = %q, want job-a", running.Job.GetId())
	}
	if running.AcceptArguments.Identity != "agent-job-a" {
		t.Fatalf("identity = %q, want agent-job-a", running.AcceptArguments.Identity)
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

func TestProcessJobEnvReplacesStaleAssignmentValues(t *testing.T) {
	info := RunningJobInfo{
		Job:      &livekit.Job{Id: "job-new"},
		WorkerID: "worker-new",
	}

	env, err := processJobEnv([]string{
		"LIVEKIT_AGENT_PROCESS_ID=old-exec",
		"LIVEKIT_AGENT_JOB_JSON={\"id\":\"old-job\"}",
		"LIVEKIT_AGENT_RUNNING_JOB_JSON={\"worker_id\":\"old-worker\"}",
		"PATH=/bin",
	}, "exec-new", info)
	if err != nil {
		t.Fatalf("processJobEnv: %v", err)
	}

	values := envMap(env)
	if values["LIVEKIT_AGENT_PROCESS_ID"] != "exec-new" {
		t.Fatalf("process id = %q, want exec-new", values["LIVEKIT_AGENT_PROCESS_ID"])
	}

	var running RunningJobInfo
	if err := json.Unmarshal([]byte(values["LIVEKIT_AGENT_RUNNING_JOB_JSON"]), &running); err != nil {
		t.Fatalf("decode running job: %v", err)
	}
	if running.WorkerID != "worker-new" {
		t.Fatalf("WorkerID = %q, want worker-new", running.WorkerID)
	}
	if countEnvKey(env, "LIVEKIT_AGENT_PROCESS_ID") != 1 {
		t.Fatalf("process id env count = %d, want 1", countEnvKey(env, "LIVEKIT_AGENT_PROCESS_ID"))
	}
	if countEnvKey(env, "LIVEKIT_AGENT_JOB_JSON") != 1 {
		t.Fatalf("job json env count = %d, want 1", countEnvKey(env, "LIVEKIT_AGENT_JOB_JSON"))
	}
	if countEnvKey(env, "LIVEKIT_AGENT_RUNNING_JOB_JSON") != 1 {
		t.Fatalf("running job env count = %d, want 1", countEnvKey(env, "LIVEKIT_AGENT_RUNNING_JOB_JSON"))
	}
	if values["PATH"] != "/bin" {
		t.Fatalf("PATH = %q, want preserved base env", values["PATH"])
	}
}

func TestRunningJobInfoFromEnvPrefersFullAssignment(t *testing.T) {
	info := RunningJobInfo{
		AcceptArguments: JobAcceptArguments{Identity: "agent-job-a"},
		Job:             &livekit.Job{Id: "job-a"},
		URL:             "wss://livekit.example",
		Token:           "room-token",
		WorkerID:        "worker-a",
		FakeJob:         true,
	}
	env, err := processJobEnv(nil, "exec-a", info)
	if err != nil {
		t.Fatalf("processJobEnv: %v", err)
	}

	running, err := RunningJobInfoFromEnv(envMap(env))
	if err != nil {
		t.Fatalf("RunningJobInfoFromEnv: %v", err)
	}
	if running.Job.GetId() != "job-a" {
		t.Fatalf("job id = %q, want job-a", running.Job.GetId())
	}
	if running.AcceptArguments.Identity != "agent-job-a" {
		t.Fatalf("identity = %q, want agent-job-a", running.AcceptArguments.Identity)
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

func TestRunningJobInfoFromEnvFallsBackToLegacyJobJSON(t *testing.T) {
	jobData, err := json.Marshal(&livekit.Job{Id: "job-legacy"})
	if err != nil {
		t.Fatalf("marshal job: %v", err)
	}

	running, err := RunningJobInfoFromEnv(map[string]string{
		"LIVEKIT_AGENT_JOB_JSON": string(jobData),
	})
	if err != nil {
		t.Fatalf("RunningJobInfoFromEnv: %v", err)
	}
	if running.Job.GetId() != "job-legacy" {
		t.Fatalf("job id = %q, want job-legacy", running.Job.GetId())
	}
	if running.AcceptArguments.Identity != "" {
		t.Fatalf("identity = %q, want empty fallback", running.AcceptArguments.Identity)
	}
}

func TestProcessJobExecutorCloseWaitsForProcessExit(t *testing.T) {
	oldCommandContext := processCommandContext
	processCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sleep", "10")
	}
	defer func() { processCommandContext = oldCommandContext }()

	executor := NewProcessJobExecutor("exec-process-close")
	if err := executor.LaunchJob(context.Background(), &livekit.Job{Id: "job-process-close"}); err != nil {
		t.Fatalf("LaunchJob() error = %v", err)
	}
	waitForProcessExecutorCommand(t, executor)

	if err := executor.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got := executor.Status(); got != JobStatusFailed {
		t.Fatalf("Status() after Close = %q, want %q", got, JobStatusFailed)
	}
}

func TestProcessJobExecutorPingMarksFailedWhenProcessMissing(t *testing.T) {
	oldPingInterval := processPingInterval
	oldProcessSignal := processSignal
	processPingInterval = time.Millisecond
	processSignal = func(*os.Process, os.Signal) error {
		return errors.New("process missing")
	}
	defer func() {
		processPingInterval = oldPingInterval
		processSignal = oldProcessSignal
	}()

	executor := NewProcessJobExecutor("exec-ping")
	executor.mu.Lock()
	executor.status = JobStatusRunning
	executor.cmd = &exec.Cmd{Process: &os.Process{Pid: 12345}}
	executor.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go executor.pingTask(ctx)

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatalf("Status() = %q, want %q", executor.Status(), JobStatusFailed)
		case <-ticker.C:
			if executor.Status() == JobStatusFailed {
				return
			}
		}
	}
}

func waitForProcessExecutorCommand(t *testing.T, executor *ProcessJobExecutor) {
	t.Helper()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			t.Fatal("process executor command was not started")
		case <-ticker.C:
			executor.mu.Lock()
			started := executor.cmd != nil && executor.cmd.Process != nil
			executor.mu.Unlock()
			if started {
				return
			}
		}
	}
}

func countEnvKey(env []string, want string) int {
	count := 0
	for _, item := range env {
		key, _, ok := strings.Cut(item, "=")
		if ok && key == want {
			count++
		}
	}
	return count
}

func envMap(env []string) map[string]string {
	values := make(map[string]string, len(env))
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	return values
}
