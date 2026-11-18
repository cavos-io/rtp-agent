package app

import (
	"context"
	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/interface/worker"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionTransportEnvironmentSelection(t *testing.T) {
	for _, key := range []string{"RTP_AGENT_LIVEKIT_TRANSPORT", "RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS", "RTP_AGENT_EXPERIMENTAL_NATIVE_ROOM", "RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE"} {
		t.Setenv(key, "")
	}
	if sessionTransportFactoryFromEnv() != nil {
		t.Fatal("Pion is not default")
	}
	t.Setenv("RTP_AGENT_LIVEKIT_TRANSPORT", "pion")
	if sessionTransportFactoryFromEnv() != nil {
		t.Fatal("Pion created a native provider")
	}
	for _, selection := range []string{"unknown", "native"} {
		t.Setenv("RTP_AGENT_LIVEKIT_TRANSPORT", selection)
		t.Setenv("LIVEKIT_LIB_PATH", "")
		f := sessionTransportFactoryFromEnv()
		if f == nil {
			t.Fatal("invalid selection fell back to Pion")
		}
		if _, err := f(worker.WorkerOptions{Transport: worker.WorkerTransportLiveKit}); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
	lib := filepath.Join(t.TempDir(), "lib")
	if err := os.WriteFile(lib, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIVEKIT_LIB_PATH", lib)
	f := sessionTransportFactoryFromEnv()
	if _, err := f(worker.WorkerOptions{Transport: worker.WorkerTransportAgora}); err == nil {
		t.Fatal("native accepted Agora worker")
	}
	t.Setenv("RTP_AGENT_LIVEKIT_TRANSPORT", "pion")
	t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS", "1")
	if _, err := sessionTransportFactoryFromEnv()(worker.WorkerOptions{}); err == nil {
		t.Fatal("conflicting flags accepted")
	}
}

// All app consumers must use the inherited provider, not only the starter.
func TestAppUsesWorkerManagedSessionTransport(t *testing.T) {
	room := &appTestSessionTransport{}
	p := &appTestSessionProvider{room: room}
	server := worker.NewAgentServer(worker.WorkerOptions{SessionTransportFactory: func(worker.WorkerOptions) (worker.SessionTransportProvider, error) { return p, nil }})
	base := agent.NewAgent("test")
	base.LLM = &fakeAppLLM{}
	a := &App{Server: server, Agent: base, Session: agent.NewAgentSession(base, nil, agent.AgentSessionOptions{}), Config: AppConfig{AppTools: []string{"send_dtmf"}}}
	if err := server.RTCSession(func(job *worker.JobContext) error {
		if err := a.runSession(job); err != nil {
			return err
		}
		if job.PrimaryRoomIO() != room || a.RoomIO != nil || job.RoomConnected() {
			t.Error("app bypassed worker provider")
		}
		job.Shutdown("test complete")
		return nil
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.ExecuteLocalJobWithOptions(ctx, "managed-test", "agent", worker.LocalJobOptions{FakeJob: true, SessionReportPath: filepath.Join(t.TempDir(), "report.json")}); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if !room.started || !p.closed {
		t.Fatal("managed startup/shutdown missing")
	}
	found := false
	for _, tool := range base.Tools {
		if tool.ID() == "send_dtmf_events" || tool.ID() == "send_dtmf" {
			found = true
		}
	}
	if !found {
		t.Fatal("managed app omitted DTMF tool")
	}
}

type appTestSessionProvider struct {
	room   *appTestSessionTransport
	closed bool
}

func (p *appTestSessionProvider) NewSession(*worker.JobContext, *agent.AgentSession, worker.StartSessionOptions) (worker.SessionRoomIO, error) {
	return p.room, nil
}
func (p *appTestSessionProvider) Close(context.Context) error { p.closed = true; return nil }

type appTestSessionTransport struct{ started bool }

func (*appTestSessionTransport) GetCallback() *worker.RoomCallback          { return nil }
func (*appTestSessionTransport) AttachRoom(*worker.SDKRoom)                 {}
func (*appTestSessionTransport) ReconcileParticipants()                     {}
func (r *appTestSessionTransport) Start(context.Context) error              { r.started = true; return nil }
func (*appTestSessionTransport) StartRecorder(string, int) error            { return nil }
func (*appTestSessionTransport) StopRecorder() error                        { return nil }
func (*appTestSessionTransport) PopulateSessionReport(*agent.SessionReport) {}
func (*appTestSessionTransport) Close() error                               { return nil }
func (*appTestSessionTransport) PublishDTMF(int32, string) error            { return nil }
