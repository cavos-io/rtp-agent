package worker

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/livekit/protocol/livekit"
)

type testTransportProvider struct {
	calls     atomic.Int32
	closes    atomic.Int32
	closed    chan struct{}
	transport SessionRoomIO
	err       error
}

func (p *testTransportProvider) NewSession(*JobContext, *agent.AgentSession, StartSessionOptions) (SessionRoomIO, error) {
	p.calls.Add(1)
	return p.transport, p.err
}
func (p *testTransportProvider) Close(context.Context) error {
	if p.closes.Add(1) == 1 && p.closed != nil {
		close(p.closed)
	}
	return nil
}
func TestWorkerSessionTransportSelection(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		job := NewJobContext(&livekit.Job{Id: "provider", Room: &livekit.Room{Name: "room"}}, "ws://unused", "key", "secret")
		defer os.RemoveAll(job.SessionDirectory())
		selected := &testSessionTransport{}
		provider := &testTransportProvider{transport: selected}
		server := NewAgentServer(WorkerOptions{SessionTransportFactory: func(WorkerOptions) (SessionTransportProvider, error) { return provider, nil }})
		server.entrypointFnc = func(job *JobContext) error {
			opts := StartSessionOptions{}
			if explicit {
				opts.Transport = selected
				provider.err = errors.New("must not be used")
			}
			return job.StartSession(context.Background(), agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{}), opts)
		}
		if err := server.runJobEntrypoint(job); err != nil {
			t.Fatal(err)
		}
		want := int32(1)
		if explicit {
			want = 0
		}
		if provider.calls.Load() != want || selected.starts != 1 || job.PrimaryRoomIO() != selected || job.RoomConnected() {
			t.Fatal("wrong provider selection or ownership")
		}
		<-job.startShutdown("done")
		if err := server.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := server.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if provider.closes.Load() != 1 {
			t.Fatal("provider not closed once")
		}
	}
	if s := NewAgentServer(WorkerOptions{}); s.sessionTransport != nil {
		t.Fatal("default Pion acquired provider")
	}
}
func TestWorkerProviderWaitsAfterCloseDeadline(t *testing.T) {
	p := &testTransportProvider{closed: make(chan struct{})}
	server := NewAgentServer(WorkerOptions{SessionTransportFactory: func(WorkerOptions) (SessionTransportProvider, error) { return p, nil }})
	server.finishingJobs["blocked"] = &JobContext{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := server.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if p.closes.Load() != 0 {
		t.Fatal("provider closed beneath finalizer")
	}
	server.mu.Lock()
	delete(server.finishingJobs, "blocked")
	server.mu.Unlock()
	select {
	case <-p.closed:
	case <-time.After(time.Second):
		t.Fatal("provider not released after finalizer")
	}
	if p.closes.Load() != 1 {
		t.Fatal("unexpected release count")
	}
}
func TestWorkerProviderFactoryFailure(t *testing.T) {
	failure := errors.New("unsupported transport")
	server := NewAgentServer(WorkerOptions{SessionTransportFactory: func(WorkerOptions) (SessionTransportProvider, error) { return nil, failure }})
	if err := server.validateUnregisteredRunPreconditions(); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err := server.validateRunPreconditions(); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err := server.runJobEntrypoint(&JobContext{}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err := server.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerProviderWaitsForEntrypoint(t *testing.T) {
	p := &testTransportProvider{closed: make(chan struct{})}
	server := NewAgentServer(WorkerOptions{SessionTransportFactory: func(WorkerOptions) (SessionTransportProvider, error) { return p, nil }})
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	server.entrypointFnc = func(*JobContext) error { close(entered); <-release; return nil }
	job := NewJobContext(&livekit.Job{Id: "entrypoint"}, "", "", "")
	job.fakeJob = true
	go func() { done <- server.runJobEntrypoint(job) }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := server.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if p.closes.Load() != 0 {
		t.Fatal("disposed beneath entrypoint")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.closed:
	case <-time.After(time.Second):
		t.Fatal("entrypoint return did not release provider")
	}
	if err := server.runJobEntrypoint(job); err == nil {
		t.Fatal("closed server accepted entrypoint")
	}
}
