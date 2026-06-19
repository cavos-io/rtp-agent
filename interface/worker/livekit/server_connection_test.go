package livekit_test

import (
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
)

func TestValidateServerConnectionOptionsUsesLiveKitRequirements(t *testing.T) {
	err := workerlivekit.ValidateServerConnectionOptions(workerlivekit.ServerConnectionOptions{
		WSURL:     "wss://livekit.example",
		APIKey:    "api-key",
		APISecret: "api-secret",
	})
	if err != nil {
		t.Fatalf("ValidateServerConnectionOptions() error = %v, want nil", err)
	}
}

func TestApplyServerConnectionEnvExportsLiveKitEnv(t *testing.T) {
	values := map[string]string{}

	workerlivekit.ApplyServerConnectionEnv(workerlivekit.ServerConnectionEnvOptions{
		ServerConnectionOptions: workerlivekit.ServerConnectionOptions{
			WSURL:     "wss://livekit.example",
			APIKey:    "api-key",
			APISecret: "api-secret",
		},
		Setenv: func(key string, value string) error {
			values[key] = value
			return nil
		},
	})

	if values[workerlivekit.WorkerURLEnvVar] != "wss://livekit.example" {
		t.Fatalf("LIVEKIT_URL = %q, want room URL", values[workerlivekit.WorkerURLEnvVar])
	}
	if values[workerlivekit.WorkerAPIKeyEnvVar] != "api-key" {
		t.Fatalf("LIVEKIT_API_KEY = %q, want api-key", values[workerlivekit.WorkerAPIKeyEnvVar])
	}
	if values[workerlivekit.WorkerAPISecretEnvVar] != "api-secret" {
		t.Fatalf("LIVEKIT_API_SECRET = %q, want api-secret", values[workerlivekit.WorkerAPISecretEnvVar])
	}
}
