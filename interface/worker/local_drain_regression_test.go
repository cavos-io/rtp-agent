package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/livekit/protocol/livekit"
)

type orderedShutdownRoom struct {
	blockingShutdownRoomIO
	stopped atomic.Bool
}

func (r *orderedShutdownRoom) BeginShutdown()                 { r.stopped.Store(true) }
func (r *orderedShutdownRoom) Shutdown(context.Context) error { r.stopped.Store(true); return nil }

func TestCustomTransportStopsAfterSessionCleanup(t *testing.T) {
	for _, custom := range []bool{false, true} {
		name := "disconnected-pion"
		if custom {
			name = "custom"
		}
		t.Run(name, func(t *testing.T) {
			job := NewJobContext(&livekit.Job{Id: name}, "", "", "")
			room := &orderedShutdownRoom{}
			session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
			called := false
			session.AddPreCloseCallback(func() {
				called = true
				if got := room.stopped.Load(); got == custom {
					t.Errorf("transport stopped before session cleanup = %v, custom = %v", got, custom)
				}
			})
			job.SetPrimarySession(session)
			job.SetPrimaryRoomIO(room)
			job.customRoomTransport.Store(custom)
			<-job.startShutdown("test")
			if !called || !room.stopped.Load() {
				t.Fatal("session or transport cleanup missing")
			}
		})
	}
}

func TestLocalReportWaitsForConcurrentBackgroundFinalizer(t *testing.T) {
	server := NewAgentServer(WorkerOptions{SessionEndTimeoutSeconds: 0.01})
	started := make(chan *JobContext, 1)
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	callbackDone := make(chan struct{})
	path := filepath.Join(t.TempDir(), "report.json")
	if err := server.RTCSession(func(job *JobContext) error { started <- job; return nil }, nil, func(job *JobContext) error {
		<-release
		job.Report.Room = "finalized-room"
		close(callbackDone)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		result <- server.ExecuteLocalJobWithOptions(context.Background(), "test", "agent", LocalJobOptions{FakeJob: true, SessionReportPath: path})
	}()
	var job *JobContext
	select {
	case job = <-started:
	case <-time.After(time.Second):
		t.Fatal("entrypoint not started")
	}
	// Own finalization outside ExecuteLocalJob, as forced server drain does.
	server.finishJob(job)
	select {
	case err := <-result:
		t.Fatalf("local report returned before finalization: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	unblock()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("report did not finish")
	}
	<-callbackDone
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report agent.SessionReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Room != "finalized-room" {
		t.Fatalf("saved partial report: %s", data)
	}
}

func TestLocalReportTimeoutDoesNotSerializePartialReport(t *testing.T) {
	previousWait := localEntrypointCloseWait
	localEntrypointCloseWait = 20 * time.Millisecond
	defer func() { localEntrypointCloseWait = previousWait }()
	server := NewAgentServer(WorkerOptions{SessionEndTimeoutSeconds: 0.001})
	started := make(chan *JobContext, 1)
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	path := filepath.Join(t.TempDir(), "report.json")
	if err := server.RTCSession(func(job *JobContext) error { started <- job; return nil }, nil, func(job *JobContext) error {
		<-release
		job.Report.Room = "late-finalization"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		result <- server.ExecuteLocalJobWithOptions(context.Background(), "test", "agent", LocalJobOptions{FakeJob: true, SessionReportPath: path})
	}()
	var job *JobContext
	select {
	case job = <-started:
	case <-time.After(time.Second):
		t.Fatal("entrypoint not started")
	}
	server.finishJob(job)
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "finalization did not settle") {
			t.Fatalf("expected explicit report timeout, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("local report wait was not bounded")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("partial report exists: %v", err)
	}
	unblock()
	select {
	case <-job.finishDone:
	case <-time.After(time.Second):
		t.Fatal("finalizer did not finish after release")
	}
}
