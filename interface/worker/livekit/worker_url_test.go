package livekit_test

import (
	"strings"
	"testing"
	"time"

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

func TestWorkerConnectInfoBuildsURLAndAuthHeader(t *testing.T) {
	info, err := workerlivekit.WorkerConnectInfo(workerlivekit.WorkerConnectOptions{
		WSURL:       "https://livekit.example/project-a",
		WorkerToken: "cloud-token",
		APIKey:      "api-key",
		APISecret:   "api-secret",
		TTL:         time.Hour,
	})
	if err != nil {
		t.Fatalf("WorkerConnectInfo() error = %v", err)
	}

	if info.URL != "wss://livekit.example/project-a/agent?worker_token=cloud-token" {
		t.Fatalf("WorkerConnectInfo().URL = %q, want LiveKit worker URL", info.URL)
	}
	if got := info.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
		t.Fatalf("WorkerConnectInfo().Header Authorization = %q, want bearer token", got)
	}
}
