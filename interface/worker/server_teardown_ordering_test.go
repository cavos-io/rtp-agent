package worker

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/livekit/protocol/livekit"
)

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

	var audioGoneDuringUpload atomic.Bool
	uploadStarted := make(chan struct{})
	stubSessionReportUpload(t, func(*agent.SessionReport) {
		close(uploadStarted)
		time.Sleep(400 * time.Millisecond)
		if _, err := os.Stat(audioPath); os.IsNotExist(err) {
			audioGoneDuringUpload.Store(true)
		}
	})

	server.finishJob(jobCtx)

	select {
	case <-uploadStarted:
	default:
		t.Fatal("the session report upload never ran")
	}
	if audioGoneDuringUpload.Load() {
		t.Fatal("the recording was deleted while it was still being uploaded")
	}
}
