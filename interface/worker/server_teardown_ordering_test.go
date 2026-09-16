package worker

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/livekit/protocol/livekit"
)

type blockingShutdownRoomIO struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *blockingShutdownRoomIO) GetCallback() *RoomCallback  { return nil }
func (r *blockingShutdownRoomIO) AttachRoom(*SDKRoom)         {}
func (r *blockingShutdownRoomIO) ReconcileParticipants()      {}
func (r *blockingShutdownRoomIO) Start(context.Context) error { return nil }
func (r *blockingShutdownRoomIO) StartRecorder(string, int) error {
	return nil
}
func (r *blockingShutdownRoomIO) StopRecorder() error                        { return nil }
func (r *blockingShutdownRoomIO) PopulateSessionReport(*agent.SessionReport) {}
func (r *blockingShutdownRoomIO) Close() error {
	return r.Shutdown(context.Background())
}
func (r *blockingShutdownRoomIO) BeginShutdown() {}
func (r *blockingShutdownRoomIO) Shutdown(context.Context) error {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return nil
}

func jobContextWithRecording(t *testing.T, jobID string) (*JobContext, string) {
	t.Helper()
	jobCtx := NewJobContext(&livekit.Job{
		Id:   jobID,
		Room: &livekit.Room{Sid: "RM_rec", Name: "room-rec"},
	}, "wss://livekit.example", "key", "secret")

	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	jobCtx.SetPrimarySession(session)

	audioPath := filepath.Join(jobCtx.SessionDirectory(), "recording.ogg")
	if err := os.WriteFile(audioPath, []byte("fake audio bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	startedAt := float64(1)
	jobCtx.Report = &agent.SessionReport{
		RecordingOptions:        agent.RecordingOptions{Audio: true},
		AudioRecordingPath:      &audioPath,
		AudioRecordingStartedAt: &startedAt,
	}
	return jobCtx, audioPath
}

func stubSessionReportUpload(t *testing.T, upload func(*agent.SessionReport)) {
	t.Helper()
	prevReport, prevRecording := uploadSessionReport, uploadSessionRecordingOnly
	uploadSessionReport = func(_ string, _ string, _ string, _ string, report *agent.SessionReport) error {
		upload(report)
		return nil
	}
	uploadSessionRecordingOnly = func(_ string, _ string, _ string, report *agent.SessionReport) error {
		upload(report)
		return nil
	}
	t.Cleanup(func() {
		uploadSessionReport, uploadSessionRecordingOnly = prevReport, prevRecording
	})
}

func TestCleanupDoesNotDeleteTheRecordingWhileItIsBeingUploaded(t *testing.T) {
	server := NewAgentServer(WorkerOptions{APIKey: "key", APISecret: "secret"})
	jobCtx, audioPath := jobContextWithRecording(t, "job-recording")

	uploadStarted := make(chan struct{})
	releaseUpload := make(chan struct{})
	stubSessionReportUpload(t, func(*agent.SessionReport) {
		close(uploadStarted)
		<-releaseUpload
	})

	finished := make(chan struct{})
	go func() {
		server.finishJob(jobCtx)
		close(finished)
	}()

	select {
	case <-uploadStarted:
	case <-time.After(time.Second):
		t.Fatal("the session report upload never ran")
	}
	if _, err := os.Stat(audioPath); err != nil {
		t.Fatalf("recording unavailable while upload owns it: %v", err)
	}
	select {
	case <-finished:
		t.Fatal("finishJob returned while upload still owned the recording")
	default:
	}

	close(releaseUpload)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("finishJob did not finish after upload released the recording")
	}
}

func TestFinishJobCleansUpAfterShutdownSessionEndAndUpload(t *testing.T) {
	server := NewAgentServer(WorkerOptions{APIKey: "key", APISecret: "secret"})
	jobCtx, audioPath := jobContextWithRecording(t, "job-cleanup-order")
	var mu sync.Mutex
	var events []string
	appendEvent := func(event string) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	}
	if err := jobCtx.AddShutdownCallback(func() { appendEvent("shutdown") }); err != nil {
		t.Fatalf("AddShutdownCallback() error = %v", err)
	}
	server.sessionEndFnc = func(*JobContext) error {
		if _, err := os.Stat(audioPath); err != nil {
			return err
		}
		appendEvent("session-end")
		return nil
	}
	stubSessionReportUpload(t, func(*agent.SessionReport) {
		if _, err := os.Stat(audioPath); err != nil {
			t.Errorf("recording unavailable during upload: %v", err)
		}
		appendEvent("upload")
	})

	server.finishJob(jobCtx)

	if _, err := os.Stat(audioPath); !os.IsNotExist(err) {
		t.Fatalf("recording exists after cleanup, stat error = %v", err)
	}
	appendEvent("cleanup")
	mu.Lock()
	got := append([]string(nil), events...)
	mu.Unlock()
	want := []string{"shutdown", "session-end", "upload", "cleanup"}
	if len(got) != len(want) {
		t.Fatalf("teardown events = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("teardown events = %#v, want %#v", got, want)
		}
	}
}

func TestTimedOutDrainRetainsArtifactsUntilDrainReturns(t *testing.T) {
	server := NewAgentServer(WorkerOptions{APIKey: "key", APISecret: "secret"})
	jobCtx, audioPath := jobContextWithRecording(t, "job-stuck-drain")
	jobCtx.shutdownTimeout = 10 * time.Millisecond
	roomIO := &blockingShutdownRoomIO{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	jobCtx.SetPrimaryRoomIO(roomIO)
	stubSessionReportUpload(t, func(*agent.SessionReport) {})
	server.mu.Lock()
	server.activeJobs[jobCtx.JobID()] = jobCtx
	server.mu.Unlock()

	foregroundDone := make(chan struct{})
	go func() {
		server.finishJob(jobCtx)
		close(foregroundDone)
	}()

	select {
	case <-roomIO.started:
	case <-time.After(time.Second):
		t.Fatal("shutdown drain did not start")
	}
	select {
	case <-foregroundDone:
	case <-time.After(time.Second):
		close(roomIO.release)
		t.Fatal("foreground shutdown exceeded its deadline")
	}
	if _, err := os.Stat(audioPath); err != nil {
		close(roomIO.release)
		t.Fatalf("artifact removed while timed-out drain still owned it: %v", err)
	}
	if got := server.inflightJobCount(); got != 1 {
		close(roomIO.release)
		t.Fatalf("inflight jobs = %d, want timed-out teardown tracked", got)
	}

	close(roomIO.release)
	waitForFileRemoval(t, audioPath)
	waitForNoInflightJobs(t, server)
}

func TestTimedOutSessionEndRetainsArtifactsUntilCallbackReturns(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		APIKey:                   "key",
		APISecret:                "secret",
		SessionEndTimeoutSeconds: 0.01,
	})
	jobCtx, audioPath := jobContextWithRecording(t, "job-stuck-session-end")
	callbackStarted := make(chan struct{})
	releaseCallback := make(chan struct{})
	server.sessionEndFnc = func(*JobContext) error {
		close(callbackStarted)
		<-releaseCallback
		return nil
	}
	stubSessionReportUpload(t, func(*agent.SessionReport) {})
	server.mu.Lock()
	server.activeJobs[jobCtx.JobID()] = jobCtx
	server.mu.Unlock()

	foregroundDone := make(chan struct{})
	go func() {
		server.finishJob(jobCtx)
		close(foregroundDone)
	}()

	select {
	case <-callbackStarted:
	case <-time.After(time.Second):
		t.Fatal("session-end callback did not start")
	}
	select {
	case <-foregroundDone:
	case <-time.After(time.Second):
		close(releaseCallback)
		t.Fatal("session-end foreground wait exceeded its deadline")
	}
	if _, err := os.Stat(audioPath); err != nil {
		close(releaseCallback)
		t.Fatalf("artifact removed while timed-out session-end callback still owned it: %v", err)
	}
	if got := server.inflightJobCount(); got != 1 {
		close(releaseCallback)
		t.Fatalf("inflight jobs = %d, want timed-out teardown tracked", got)
	}

	close(releaseCallback)
	waitForFileRemoval(t, audioPath)
	waitForNoInflightJobs(t, server)
}

func TestSessionEndPanicStillCleansUpArtifacts(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	jobCtx, audioPath := jobContextWithRecording(t, "job-session-end-panic")
	server.sessionEndFnc = func(*JobContext) error {
		panic("session end panic")
	}

	server.finishJob(jobCtx)

	if _, err := os.Stat(audioPath); !os.IsNotExist(err) {
		t.Fatalf("recording exists after recovered session-end panic, stat error = %v", err)
	}
}

func waitForFileRemoval(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("artifact was not removed after all consumers released ownership: %s", path)
}

func waitForNoInflightJobs(t *testing.T, server *AgentServer) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if server.inflightJobCount() == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("inflight jobs = %d after teardown completed, want 0", server.inflightJobCount())
}
