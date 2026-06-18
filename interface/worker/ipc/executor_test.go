package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/livekit/protocol/livekit"
)

func TestJobExecutorsReportUnavailableStatusBeforeLaunch(t *testing.T) {
	tests := []struct {
		name     string
		executor JobExecutor
	}{
		{name: "thread", executor: NewThreadJobExecutor("exec-thread-idle", func() error { return nil })},
		{name: "process", executor: NewProcessJobExecutor("exec-process-idle")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.executor.Status(); got != JobStatus("") {
				t.Fatalf("Status() before launch = %q, want unavailable zero status", got)
			}
		})
	}
}

func TestProcessJobExecutorDefaultPingCadenceMatchesReference(t *testing.T) {
	if got := processPingInterval; got != 2500*time.Millisecond {
		t.Fatalf("processPingInterval = %v, want 2.5s reference job process heartbeat", got)
	}
	if got := processPingTimeout; got != 60*time.Second {
		t.Fatalf("processPingTimeout = %v, want 60s reference job process heartbeat timeout", got)
	}
}

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

	env, err := ProcessJobEnv([]string{"PATH=/bin"}, "exec-a", info)
	if err != nil {
		t.Fatalf("ProcessJobEnv: %v", err)
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

	env, err := ProcessJobEnv([]string{
		"LIVEKIT_AGENT_PROCESS_ID=old-exec",
		"LIVEKIT_AGENT_JOB_JSON={\"id\":\"old-job\"}",
		"LIVEKIT_AGENT_RUNNING_JOB_JSON={\"worker_id\":\"old-worker\"}",
		"PATH=/bin",
	}, "exec-new", info)
	if err != nil {
		t.Fatalf("ProcessJobEnv: %v", err)
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

func TestProcessJobEnvPreservesUnrelatedBaseEnv(t *testing.T) {
	env, err := ProcessJobEnv([]string{"PATH=/bin", "CUSTOM=value"}, "exec-a", RunningJobInfo{
		Job: &livekit.Job{Id: "job-a"},
	})
	if err != nil {
		t.Fatalf("ProcessJobEnv: %v", err)
	}

	values := envMap(env)
	if values["CUSTOM"] != "value" {
		t.Fatalf("CUSTOM = %q, want preserved base env", values["CUSTOM"])
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
	env, err := ProcessJobEnv(nil, "exec-a", info)
	if err != nil {
		t.Fatalf("ProcessJobEnv: %v", err)
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

func TestProcessJobExecutorCloseSendsShutdownRequestBeforeKill(t *testing.T) {
	oldKill := processKill
	oldGraceTimeout := processShutdownGraceTimeout
	processShutdownGraceTimeout = 0
	defer func() {
		processKill = oldKill
		processShutdownGraceTimeout = oldGraceTimeout
	}()

	var output bytes.Buffer
	done := make(chan struct{})
	shutdownSeenBeforeKill := false
	processKill = func(*os.Process) error {
		msg, err := ReadMessage(&output)
		if err == nil && msg.Type == MessageTypeShutdownRequest {
			shutdownSeenBeforeKill = true
		}
		close(done)
		return nil
	}

	executor := NewProcessJobExecutor("exec-process-close-shutdown")
	executor.mu.Lock()
	executor.started = true
	executor.status = JobStatusRunning
	executor.done = done
	executor.cmd = &exec.Cmd{Process: &os.Process{Pid: 12345}}
	executor.pingWriter = &output
	executor.mu.Unlock()

	if err := executor.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !shutdownSeenBeforeKill {
		t.Fatal("Close() did not send ShutdownRequest before killing process")
	}
}

func TestProcessJobExecutorCloseWaitsForShutdownExitBeforeKill(t *testing.T) {
	oldKill := processKill
	defer func() { processKill = oldKill }()

	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()

	done := make(chan struct{})
	shutdownSeen := make(chan struct{})
	var closeDone sync.Once
	closeProcessDone := func() {
		closeDone.Do(func() {
			close(done)
		})
	}

	go func() {
		msg, err := ReadMessage(reader)
		if err == nil && msg.Type == MessageTypeShutdownRequest {
			close(shutdownSeen)
			closeProcessDone()
		}
	}()

	killCalled := false
	processKill = func(*os.Process) error {
		<-shutdownSeen
		killCalled = true
		closeProcessDone()
		return nil
	}

	executor := NewProcessJobExecutor("exec-process-close-wait-shutdown")
	executor.mu.Lock()
	executor.started = true
	executor.status = JobStatusRunning
	executor.done = done
	executor.cmd = &exec.Cmd{Process: &os.Process{Pid: 12345}}
	executor.pingWriter = writer
	executor.mu.Unlock()

	if err := executor.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if killCalled {
		t.Fatal("Close() killed process even though it exited after ShutdownRequest")
	}
	if got := executor.Status(); got != JobStatusFailed {
		t.Fatalf("Status() after Close = %q, want %q", got, JobStatusFailed)
	}
}

func TestProcessJobExecutorCloseExtendsExitWaitAfterShutdownAck(t *testing.T) {
	oldKill := processKill
	oldGraceTimeout := processShutdownGraceTimeout
	processShutdownGraceTimeout = 20 * time.Millisecond
	defer func() {
		processKill = oldKill
		processShutdownGraceTimeout = oldGraceTimeout
	}()

	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()

	done := make(chan struct{})
	shutdownSeen := make(chan struct{})
	var closeDone sync.Once
	closeProcessDone := func() {
		closeDone.Do(func() {
			close(done)
		})
	}

	go func() {
		msg, err := ReadMessage(reader)
		if err == nil && msg.Type == MessageTypeShutdownRequest {
			close(shutdownSeen)
		}
	}()

	killCalled := make(chan struct{}, 1)
	processKill = func(*os.Process) error {
		killCalled <- struct{}{}
		closeProcessDone()
		return nil
	}

	executor := NewProcessJobExecutor("exec-process-close-ack-extends-exit")
	executor.mu.Lock()
	executor.started = true
	executor.status = JobStatusRunning
	executor.done = done
	executor.cmd = &exec.Cmd{Process: &os.Process{Pid: 12345}}
	executor.pingWriter = writer
	executor.mu.Unlock()

	closeResult := make(chan error, 1)
	go func() {
		closeResult <- executor.Close(context.Background())
	}()

	select {
	case <-shutdownSeen:
	case <-time.After(time.Second):
		t.Fatal("Close() did not send ShutdownRequest")
	}

	time.Sleep(15 * time.Millisecond)
	executor.HandleShutdownRequestAck(ShutdownRequestAck{})
	executor.HandleShuttingDown(ShuttingDown{})

	time.Sleep(10 * time.Millisecond)
	select {
	case <-killCalled:
		t.Fatal("Close() killed process at original grace deadline after shutdown ack")
	default:
	}

	closeProcessDone()
	select {
	case err := <-closeResult:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() did not return after process exit")
	}
}

func TestProcessJobExecutorCloseRespectsContextWhenKillBlocks(t *testing.T) {
	oldCommandContext := processCommandContext
	oldKill := processKill
	processCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sleep", "10")
	}
	killStarted := make(chan struct{})
	releaseKill := make(chan struct{})
	processKill = func(*os.Process) error {
		close(killStarted)
		<-releaseKill
		return nil
	}
	defer func() {
		processCommandContext = oldCommandContext
		processKill = oldKill
		close(releaseKill)
	}()

	executor := NewProcessJobExecutor("exec-process-close-blocked-kill")
	if err := executor.LaunchJob(context.Background(), &livekit.Job{Id: "job-process-close-blocked-kill"}); err != nil {
		t.Fatalf("LaunchJob() error = %v", err)
	}
	waitForProcessExecutorCommand(t, executor)
	defer func() {
		executor.mu.Lock()
		cmd := executor.cmd
		executor.mu.Unlock()
		if cmd != nil && cmd.Process != nil {
			_ = oldKill(cmd.Process)
		}
	}()

	closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- executor.Close(closeCtx)
	}()

	select {
	case <-killStarted:
	case <-time.After(time.Second):
		t.Fatal("process kill did not start")
	}

	select {
	case err := <-closeDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close() error = %v, want context deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() did not return after context deadline while process kill was blocked")
	}
	if got := executor.Status(); got != JobStatusFailed {
		t.Fatalf("Status() after close kill timeout = %q, want %q", got, JobStatusFailed)
	}
}

func TestProcessJobExecutorLaunchExecutableFailureDoesNotStart(t *testing.T) {
	oldExecutable := processExecutable
	processExecutable = func() (string, error) {
		return "", errors.New("missing executable")
	}
	defer func() { processExecutable = oldExecutable }()

	executor := NewProcessJobExecutor("exec-launch-error")
	err := executor.LaunchJob(context.Background(), &livekit.Job{Id: "job-launch-error"})
	if err == nil {
		t.Fatal("LaunchJob() error = nil, want executable error")
	}
	if executor.Started() {
		t.Fatal("Started() = true after executable lookup failed")
	}
	if got := executor.Status(); got != JobStatusFailed {
		t.Fatalf("Status() after executable lookup failed = %q, want %q", got, JobStatusFailed)
	}
	if err := executor.Close(context.Background()); err != nil {
		t.Fatalf("Close() after executable lookup failed error = %v", err)
	}
}

func TestProcessJobExecutorPingMarksFailedWhenProcessMissing(t *testing.T) {
	oldPingInterval := processPingInterval
	oldProcessSignal := processSignal
	oldKill := processKill
	processPingInterval = time.Millisecond
	processSignal = func(*os.Process, os.Signal) error {
		return errors.New("process missing")
	}
	killed := make(chan struct{}, 1)
	processKill = func(*os.Process) error {
		killed <- struct{}{}
		return nil
	}
	defer func() {
		processPingInterval = oldPingInterval
		processSignal = oldProcessSignal
		processKill = oldKill
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
				select {
				case <-killed:
					return
				case <-time.After(time.Second):
					t.Fatal("process was not killed after failed ping")
				}
			}
		}
	}
}

func TestProcessJobExecutorPingWritesPingRequest(t *testing.T) {
	oldPingInterval := processPingInterval
	oldProcessSignal := processSignal
	oldNow := processNow
	oldTimeMS := processTimeMS
	processPingInterval = time.Millisecond
	processSignal = func(*os.Process, os.Signal) error { return nil }
	now := time.UnixMilli(12345)
	processNow = func() time.Time { return now }
	processTimeMS = func() int64 { return 67890 }
	defer func() {
		processPingInterval = oldPingInterval
		processSignal = oldProcessSignal
		processNow = oldNow
		processTimeMS = oldTimeMS
	}()

	var output bytes.Buffer
	executor := NewProcessJobExecutor("exec-ping-write")
	executor.mu.Lock()
	executor.status = JobStatusRunning
	executor.cmd = &exec.Cmd{Process: &os.Process{Pid: 12345}}
	executor.pingWriter = &output
	executor.lastPong = now
	executor.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go executor.pingTask(ctx)

	deadline := time.After(time.Second)
	for output.Len() == 0 {
		select {
		case <-deadline:
			t.Fatal("ping task did not write a ping request")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()

	msg, err := ReadMessage(&output)
	if err != nil {
		t.Fatalf("ReadMessage ping: %v", err)
	}
	if msg.Type != MessageTypePingRequest {
		t.Fatalf("ping message Type = %q, want %q", msg.Type, MessageTypePingRequest)
	}
	payload, err := DecodePayload(msg)
	if err != nil {
		t.Fatalf("DecodePayload ping: %v", err)
	}
	ping, ok := payload.(*PingRequest)
	if !ok {
		t.Fatalf("ping payload = %T, want *PingRequest", payload)
	}
	if ping.Timestamp != 67890 {
		t.Fatalf("PingRequest.Timestamp = %d, want 67890", ping.Timestamp)
	}
}

func TestProcessJobExecutorPingTimeoutKillsProcessWhenPongMissing(t *testing.T) {
	oldPingInterval := processPingInterval
	oldPingTimeout := processPingTimeout
	oldProcessSignal := processSignal
	oldKill := processKill
	oldNow := processNow
	processPingInterval = time.Millisecond
	processPingTimeout = time.Millisecond
	processSignal = func(*os.Process, os.Signal) error { return nil }
	now := time.Unix(10, 0)
	processNow = func() time.Time { return now }
	killed := make(chan struct{}, 1)
	processKill = func(*os.Process) error {
		killed <- struct{}{}
		return nil
	}
	defer func() {
		processPingInterval = oldPingInterval
		processPingTimeout = oldPingTimeout
		processSignal = oldProcessSignal
		processKill = oldKill
		processNow = oldNow
	}()

	executor := NewProcessJobExecutor("exec-ping-timeout")
	executor.mu.Lock()
	executor.status = JobStatusRunning
	executor.cmd = &exec.Cmd{Process: &os.Process{Pid: 12345}}
	executor.pingWriter = io.Discard
	executor.lastPong = now.Add(-time.Second)
	executor.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go executor.pingTask(ctx)

	select {
	case <-killed:
	case <-time.After(time.Second):
		t.Fatal("process was not killed after pong timeout")
	}
	if got := executor.Status(); got != JobStatusFailed {
		t.Fatalf("Status() = %q, want %q", got, JobStatusFailed)
	}
}

func TestProcessJobExecutorPongResetsPingTimeout(t *testing.T) {
	oldNow := processNow
	now := time.Unix(20, 0)
	processNow = func() time.Time { return now }
	defer func() { processNow = oldNow }()

	executor := NewProcessJobExecutor("exec-pong")
	executor.lastPong = now.Add(-time.Hour)

	executor.HandlePong(PongResponse{Timestamp: 123})

	if !executor.lastPong.Equal(now) {
		t.Fatalf("lastPong = %v, want %v", executor.lastPong, now)
	}
}

func TestProcessJobExecutorPingDoesNotHoldLockWhileKilling(t *testing.T) {
	oldPingInterval := processPingInterval
	oldProcessSignal := processSignal
	oldKill := processKill
	processPingInterval = time.Millisecond
	processSignal = func(*os.Process, os.Signal) error {
		return errors.New("process missing")
	}
	killStarted := make(chan struct{})
	releaseKill := make(chan struct{})
	processKill = func(*os.Process) error {
		close(killStarted)
		<-releaseKill
		return nil
	}
	defer func() {
		processPingInterval = oldPingInterval
		processSignal = oldProcessSignal
		processKill = oldKill
	}()
	defer close(releaseKill)

	executor := NewProcessJobExecutor("exec-ping-lock")
	executor.mu.Lock()
	executor.status = JobStatusRunning
	executor.cmd = &exec.Cmd{Process: &os.Process{Pid: 12345}}
	executor.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go executor.pingTask(ctx)

	select {
	case <-killStarted:
	case <-time.After(time.Second):
		t.Fatal("process kill did not start after failed ping")
	}

	statusCh := make(chan JobStatus, 1)
	go func() {
		statusCh <- executor.Status()
	}()

	select {
	case got := <-statusCh:
		if got != JobStatusFailed {
			t.Fatalf("Status() while kill is blocked = %q, want %q", got, JobStatusFailed)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Status() blocked while process kill was running")
	}
}

func TestProcessJobExecutorPingFailureIsNotOverwrittenByCleanExit(t *testing.T) {
	oldPingInterval := processPingInterval
	oldProcessSignal := processSignal
	oldKill := processKill
	oldCommandContext := processCommandContext
	processPingInterval = time.Millisecond
	processSignal = func(*os.Process, os.Signal) error {
		return errors.New("process missing")
	}
	processKill = func(*os.Process) error {
		return nil
	}
	processCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sleep", "0.05")
	}
	defer func() {
		processPingInterval = oldPingInterval
		processSignal = oldProcessSignal
		processKill = oldKill
		processCommandContext = oldCommandContext
	}()

	executor := NewProcessJobExecutor("exec-ping-clean-exit")
	if err := executor.LaunchJob(context.Background(), &livekit.Job{Id: "job-ping-clean-exit"}); err != nil {
		t.Fatalf("LaunchJob() error = %v", err)
	}
	if err := executor.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got := executor.Status(); got != JobStatusFailed {
		t.Fatalf("Status() after ping failure and clean exit = %q, want %q", got, JobStatusFailed)
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
