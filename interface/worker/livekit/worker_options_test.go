package livekit_test

import (
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
)

func TestResolveWorkerConnectionOptionsUsesEnvWhenUnset(t *testing.T) {
	env := map[string]string{
		"LIVEKIT_URL":          "wss://livekit.example",
		"LIVEKIT_API_KEY":      "env-key",
		"LIVEKIT_API_SECRET":   "env-secret",
		"LIVEKIT_WORKER_TOKEN": "env-worker-token",
		"LIVEKIT_AGENT_NAME":   "env-agent",
	}

	got := workerlivekit.ResolveWorkerConnectionOptions(workerlivekit.WorkerConnectionOptions{
		Getenv: func(key string) string {
			return env[key]
		},
	})

	if got.WSURL != "wss://livekit.example" {
		t.Fatalf("WSURL = %q, want env URL", got.WSURL)
	}
	if got.APIKey != "env-key" {
		t.Fatalf("APIKey = %q, want env key", got.APIKey)
	}
	if got.APISecret != "env-secret" {
		t.Fatalf("APISecret = %q, want env secret", got.APISecret)
	}
	if got.WorkerToken != "env-worker-token" {
		t.Fatalf("WorkerToken = %q, want env worker token", got.WorkerToken)
	}
	if got.AgentName != "env-agent" {
		t.Fatalf("AgentName = %q, want env agent", got.AgentName)
	}
	if !got.AgentNameIsEnv {
		t.Fatal("AgentNameIsEnv = false, want true when loaded from env")
	}
}

func TestResolveWorkerConnectionOptionsPreservesExplicitValues(t *testing.T) {
	got := workerlivekit.ResolveWorkerConnectionOptions(workerlivekit.WorkerConnectionOptions{
		WSURL:       "wss://canonical.example",
		LegacyWSURL: "wss://legacy.example",
		APIKey:      "explicit-key",
		APISecret:   "explicit-secret",
		WorkerToken: "explicit-token",
		AgentName:   "explicit-agent",
		Getenv: func(string) string {
			return "env-value"
		},
	})

	if got.WSURL != "wss://canonical.example" {
		t.Fatalf("WSURL = %q, want explicit canonical URL", got.WSURL)
	}
	if got.APIKey != "explicit-key" {
		t.Fatalf("APIKey = %q, want explicit key", got.APIKey)
	}
	if got.APISecret != "explicit-secret" {
		t.Fatalf("APISecret = %q, want explicit secret", got.APISecret)
	}
	if got.WorkerToken != "explicit-token" {
		t.Fatalf("WorkerToken = %q, want explicit token", got.WorkerToken)
	}
	if got.AgentName != "explicit-agent" {
		t.Fatalf("AgentName = %q, want explicit agent", got.AgentName)
	}
	if got.AgentNameIsEnv {
		t.Fatal("AgentNameIsEnv = true, want false for explicit agent")
	}
}

func TestResolveWorkerConnectionOptionsFallsBackToLegacyWSURL(t *testing.T) {
	got := workerlivekit.ResolveWorkerConnectionOptions(workerlivekit.WorkerConnectionOptions{
		LegacyWSURL: "wss://legacy.example",
		Getenv: func(string) string {
			return "env-value"
		},
	})

	if got.WSURL != "wss://legacy.example" {
		t.Fatalf("WSURL = %q, want legacy URL", got.WSURL)
	}
}
