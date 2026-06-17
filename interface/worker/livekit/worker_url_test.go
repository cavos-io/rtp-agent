package livekit_test

import (
	"net/http"
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

func TestWorkerWebSocketDialerUsesHTTPProxy(t *testing.T) {
	dialer, err := workerlivekit.WorkerWebSocketDialer("http://proxy.example:8080")
	if err != nil {
		t.Fatalf("WorkerWebSocketDialer() error = %v", err)
	}
	if dialer.Proxy == nil {
		t.Fatal("WorkerWebSocketDialer().Proxy = nil, want configured proxy")
	}

	proxyURL, err := dialer.Proxy(&http.Request{})
	if err != nil {
		t.Fatalf("dialer.Proxy() error = %v", err)
	}
	if proxyURL.String() != "http://proxy.example:8080" {
		t.Fatalf("dialer.Proxy() = %q, want configured proxy", proxyURL.String())
	}
}

func TestWorkerWebSocketDialerRejectsInvalidProxy(t *testing.T) {
	if _, err := workerlivekit.WorkerWebSocketDialer("://bad proxy"); err == nil {
		t.Fatal("WorkerWebSocketDialer() error = nil, want invalid proxy error")
	}
}
