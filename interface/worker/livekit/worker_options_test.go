package livekit_test

import (
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
)

func TestResolveWorkerConnectionOptionsUsesEnvWhenUnset(t *testing.T) {
	env := map[string]string{
		workerlivekit.WorkerURLEnvVar:       "wss://livekit.example",
		workerlivekit.WorkerAPIKeyEnvVar:    "env-key",
		workerlivekit.WorkerAPISecretEnvVar: "env-secret",
		"LIVEKIT_WORKER_TOKEN":              "env-worker-token",
		"LIVEKIT_AGENT_NAME":                "env-agent",
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

func TestWorkerLogLevelFromEnvUsesLiveKitLogLevelEnv(t *testing.T) {
	got := workerlivekit.WorkerLogLevelFromEnv(func(key string) string {
		if key == workerlivekit.WorkerLogLevelEnvVar {
			return "warn"
		}
		return ""
	})

	if got != "warn" {
		t.Fatalf("WorkerLogLevelFromEnv() = %q, want warn", got)
	}
}

func TestValidateWorkerConnectionOptionsRequiresURLKeyAndSecret(t *testing.T) {
	tests := []struct {
		name string
		opts workerlivekit.WorkerConnectionOptions
		want string
	}{
		{
			name: "missing URL",
			want: "ws_url is required, or set LIVEKIT_URL environment variable",
		},
		{
			name: "missing API key",
			opts: workerlivekit.WorkerConnectionOptions{WSURL: "wss://livekit.example"},
			want: "api_key is required, or set LIVEKIT_API_KEY environment variable",
		},
		{
			name: "missing API secret",
			opts: workerlivekit.WorkerConnectionOptions{
				WSURL:  "wss://livekit.example",
				APIKey: "api-key",
			},
			want: "api_secret is required, or set LIVEKIT_API_SECRET environment variable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := workerlivekit.ValidateWorkerConnectionOptions(tt.opts)
			if err == nil {
				t.Fatal("ValidateWorkerConnectionOptions() error = nil, want error")
			}
			if err.Error() != tt.want {
				t.Fatalf("ValidateWorkerConnectionOptions() error = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

func TestValidateWorkerConnectionOptionsAcceptsCompleteOptions(t *testing.T) {
	err := workerlivekit.ValidateWorkerConnectionOptions(workerlivekit.WorkerConnectionOptions{
		WSURL:     "wss://livekit.example",
		APIKey:    "api-key",
		APISecret: "api-secret",
	})
	if err != nil {
		t.Fatalf("ValidateWorkerConnectionOptions() error = %v, want nil", err)
	}
}
