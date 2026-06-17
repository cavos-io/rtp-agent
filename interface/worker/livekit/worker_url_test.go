package livekit_test

import (
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
)

func TestAgentWebSocketURLPreservesBasePath(t *testing.T) {
	got, err := workerlivekit.AgentWebSocketURL("https://livekit.example/project-a", "")
	if err != nil {
		t.Fatalf("AgentWebSocketURL() error = %v", err)
	}

	want := "wss://livekit.example/project-a/agent"
	if got != want {
		t.Fatalf("AgentWebSocketURL() = %q, want %q", got, want)
	}
}

func TestAgentWebSocketURLAddsWorkerToken(t *testing.T) {
	got, err := workerlivekit.AgentWebSocketURL("wss://livekit.example/project-a/", "cloud token")
	if err != nil {
		t.Fatalf("AgentWebSocketURL() error = %v", err)
	}

	want := "wss://livekit.example/project-a/agent?worker_token=cloud+token"
	if got != want {
		t.Fatalf("AgentWebSocketURL() = %q, want %q", got, want)
	}
}
