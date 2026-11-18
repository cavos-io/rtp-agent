package nativeroom

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/interface/worker"
	lk "github.com/livekit/protocol/livekit"
)

func TestCloseDuringStartupReapsHelper(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "helper")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nread config\nprintf '%s\\n' '{\"event\":\"duplex_protocol\",\"version\":2}'\nread command\n"), 0700); err != nil {
		t.Fatal(err)
	}
	job := worker.NewJobContext(&lk.Job{Id: "native-startup", Room: &lk.Room{Name: "test"}}, "", "", "")
	defer os.RemoveAll(job.SessionDirectory())
	room := New(job, agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{}), Config{Binary: binary, Token: "test-token", Participant: "caller", TrackSID: "track"}, "", "")
	done := make(chan error, 1)
	go func() { done <- room.Start(context.Background()) }()
	deadline := time.Now().Add(time.Second)
	for {
		room.mu.Lock()
		child := room.child
		room.mu.Unlock()
		if child != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper did not start")
		}
		time.Sleep(time.Millisecond)
	}
	if err := room.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed startup succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("startup did not stop")
	}
	room.mu.Lock()
	child := room.child
	room.mu.Unlock()
	select {
	case <-child.done:
	default:
		t.Fatal("helper not reaped")
	}
}

func TestJobShutdownKeepsNativeRoomOpenForSessionCleanup(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "helper")
	script := "#!/bin/sh\nread config\ncat <<'WIRE'\n" + readyMessages + "WIRE\nread command\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	job := worker.NewJobContext(&lk.Job{Id: "native-cleanup-order", Room: &lk.Room{Name: "test"}}, "", "", "")
	defer os.RemoveAll(job.SessionDirectory())
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	room := New(job, session, Config{Binary: binary, Token: "test-token", Participant: "caller", TrackSID: "track"}, "", "")
	if err := job.StartSession(context.Background(), session, worker.StartSessionOptions{Transport: room}); err != nil {
		t.Fatal(err)
	}
	checked := make(chan bool, 1)
	// Application cleanup is registered after StartSession, as downstream hold is.
	session.AddPreCloseCallback(func() { room.mu.Lock(); checked <- room.closed; room.mu.Unlock() })
	job.Shutdown("test")
	select {
	case closed := <-checked:
		if closed {
			t.Fatal("native room closed before application session cleanup")
		}
	case <-time.After(time.Second):
		t.Fatal("session cleanup did not run")
	}
	room.mu.Lock()
	closed, child := room.closed, room.child
	room.mu.Unlock()
	if !closed {
		t.Fatal("job shutdown left transport open")
	}
	select {
	case <-child.done:
	case <-time.After(time.Second):
		t.Fatal("job shutdown did not reap transport")
	}
}
