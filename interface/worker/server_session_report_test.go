package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/livekit/protocol/livekit"
)

func serverCountingSessionEnd(t *testing.T) (*AgentServer, *atomic.Int64) {
	t.Helper()
	server := NewAgentServer(WorkerOptions{})
	var calls atomic.Int64
	server.sessionEndFnc = func(*JobContext) error {
		calls.Add(1)
		return nil
	}
	return server, &calls
}

func TestSessionReportIsGeneratedWhenTheJobFinishesNormally(t *testing.T) {
	server, calls := serverCountingSessionEnd(t)
	jobCtx := NewJobContext(&livekit.Job{Id: "job-normal"}, "", "", "")

	server.finishJob(jobCtx)

	if got := calls.Load(); got != 1 {
		t.Fatalf("session end calls = %d, want 1", got)
	}
}

func TestSessionReportIsGeneratedWhenTheServerTerminatesTheJob(t *testing.T) {
	server, calls := serverCountingSessionEnd(t)
	jobCtx := NewJobContext(&livekit.Job{Id: "job-terminated"}, "", "", "")

	server.mu.Lock()
	server.activeJobs[jobCtx.Job.Id] = jobCtx
	server.mu.Unlock()

	server.handleTermination(&livekit.JobTermination{JobId: jobCtx.Job.Id})

	if got := calls.Load(); got != 1 {
		t.Fatalf("session end calls = %d, want 1", got)
	}
}

func TestSessionReportIsGeneratedOnlyOncePerJob(t *testing.T) {
	server, calls := serverCountingSessionEnd(t)
	jobCtx := NewJobContext(&livekit.Job{Id: "job-double"}, "", "", "")

	server.mu.Lock()
	server.activeJobs[jobCtx.Job.Id] = jobCtx
	server.mu.Unlock()

	server.finishJob(jobCtx)
	server.handleTermination(&livekit.JobTermination{JobId: jobCtx.Job.Id})

	if got := calls.Load(); got != 1 {
		t.Fatalf("session end calls = %d, want exactly 1", got)
	}
}

func TestSessionReportHasContentWhenTerminationRunsInAGoroutine(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})

	reports := make(chan *agent.SessionReport, 1)
	server.sessionEndFnc = func(jobCtx *JobContext) error {
		report, err := jobCtx.MakeSessionReport()
		if err != nil {
			t.Errorf("MakeSessionReport() error = %v", err)
			reports <- nil
			return err
		}
		reports <- report
		return nil
	}

	jobCtx := NewJobContext(&livekit.Job{
		Id:   "job-with-history",
		Room: &livekit.Room{Sid: "RM_history", Name: "room-history"},
	}, "wss://livekit.example", "key", "secret")

	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	session.ChatCtx.Append(&llm.ChatMessage{
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "halo, mau cek tagihan"}},
	})
	session.ChatCtx.Append(&llm.ChatMessage{
		Role:    llm.ChatRoleAssistant,
		Content: []llm.ChatContent{{Text: "baik, saya cek dulu"}},
	})
	jobCtx.SetPrimarySession(session)

	server.mu.Lock()
	server.activeJobs[jobCtx.Job.Id] = jobCtx
	server.mu.Unlock()

	server.handleMessage(context.Background(), &livekit.ServerMessage{
		Message: &livekit.ServerMessage_Termination{
			Termination: &livekit.JobTermination{JobId: jobCtx.Job.Id},
		},
	})

	select {
	case report := <-reports:
		if report == nil {
			t.Fatal("no report was produced")
		}
		if report.JobID != "job-with-history" || report.Room != "room-history" {
			t.Fatalf("report identity = %q/%q, want job-with-history/room-history", report.JobID, report.Room)
		}
		if got := len(report.ChatHistory.Items); got != 2 {
			t.Fatalf("chat history items = %d, want 2", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("session report was never produced through the goroutine path")
	}
}
