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
