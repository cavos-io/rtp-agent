package worker

import (
	"os"
	"strings"
	"testing"
)

func TestNormalizeWorkerTransportDefaultsToLiveKit(t *testing.T) {
	if got := NormalizeWorkerTransport(""); got != WorkerTransportLiveKit {
		t.Fatalf("NormalizeWorkerTransport(\"\") = %q, want %q", got, WorkerTransportLiveKit)
	}
}

func TestNormalizeWorkerTransportAcceptsAgora(t *testing.T) {
	if got := NormalizeWorkerTransport(" AGORA "); got != WorkerTransportAgora {
		t.Fatalf("NormalizeWorkerTransport(\" AGORA \") = %q, want %q", got, WorkerTransportAgora)
	}
}

func TestValidateWorkerTransportRejectsUnknown(t *testing.T) {
	err := ValidateWorkerTransport("matrix")
	if err == nil {
		t.Fatal("ValidateWorkerTransport() error = nil, want unknown transport error")
	}
	if !strings.Contains(err.Error(), "unknown worker transport") {
		t.Fatalf("ValidateWorkerTransport() error = %q, want unknown worker transport", err.Error())
	}
}

func TestValidateWorkerTransportAcceptsKnownTransports(t *testing.T) {
	tests := []struct {
		name      string
		transport WorkerTransport
	}{
		{name: "default livekit", transport: ""},
		{name: "livekit", transport: WorkerTransportLiveKit},
		{name: "agora", transport: WorkerTransportAgora},
		{name: "trimmed agora", transport: WorkerTransport(" agora ")},
		{name: "upper livekit", transport: WorkerTransport("LIVEKIT")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateWorkerTransport(tt.transport); err != nil {
				t.Fatalf("ValidateWorkerTransport(%q) error = %v", tt.transport, err)
			}
		})
	}
}

func TestSharedWorkerOptionsDoNotOwnAgoraProviderOptions(t *testing.T) {
	files := []string{"transport.go", "server.go"}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		source := string(data)
		if strings.Contains(source, "type AgoraOptions") {
			t.Fatalf("%s defines Agora provider options; keep provider config in interface/worker/agora or app wiring", file)
		}
		if strings.Contains(source, "Agora          AgoraOptions") {
			t.Fatalf("%s exposes Agora provider options on shared WorkerOptions", file)
		}
	}
}

func TestResolveWorkerOptionsDefaultsTransportToLiveKit(t *testing.T) {
	opts := resolveWorkerOptions(WorkerOptions{})
	if opts.Transport != WorkerTransportLiveKit {
		t.Fatalf("Transport = %q, want %q", opts.Transport, WorkerTransportLiveKit)
	}
}

func TestResolveWorkerOptionsDoesNotReadLiveKitEnvForAgora(t *testing.T) {
	t.Setenv("LIVEKIT_URL", "wss://livekit-env.example")
	t.Setenv("LIVEKIT_API_KEY", "livekit-key")
	t.Setenv("LIVEKIT_API_SECRET", "livekit-secret")

	opts := resolveWorkerOptions(WorkerOptions{
		Transport: WorkerTransportAgora,
		Agora: AgoraOptions{
			AppID:   "agora-app",
			Channel: "support",
		},
	})

	if opts.WSRL != "" || opts.WSURL != "" {
		t.Fatalf("LiveKit URL fields = WSRL %q WSURL %q, want empty for Agora", opts.WSRL, opts.WSURL)
	}
	if opts.APIKey != "" || opts.APISecret != "" {
		t.Fatalf("LiveKit credentials = key %q secret %q, want empty for Agora", opts.APIKey, opts.APISecret)
	}
	if opts.Permissions != nil {
		t.Fatal("Permissions = non-nil LiveKit permissions, want nil for Agora")
	}
}

func TestResolveWorkerOptionsDoesNotReadLiveKitLogLevelEnvForAgora(t *testing.T) {
	t.Setenv("LIVEKIT_DEV_MODE", "0")
	t.Setenv("LIVEKIT_LOG_LEVEL", "debug")

	opts := resolveWorkerOptions(WorkerOptions{
		Transport: WorkerTransportAgora,
		Agora: AgoraOptions{
			AppID:   "agora-app",
			Channel: "support",
		},
	})

	if opts.LogLevel != defaultProdLogLevel {
		t.Fatalf("LogLevel = %q, want default prod log level for Agora", opts.LogLevel)
	}
}

func TestResolveWorkerOptionsDoesNotReadLiveKitDevModeEnvForAgora(t *testing.T) {
	t.Setenv("LIVEKIT_DEV_MODE", "1")

	opts := resolveWorkerOptions(WorkerOptions{
		Transport: WorkerTransportAgora,
		Agora: AgoraOptions{
			AppID:   "agora-app",
			Channel: "support",
		},
	})

	if opts.DevMode {
		t.Fatal("DevMode = true, want false for Agora")
	}
	if opts.LogLevel != defaultProdLogLevel {
		t.Fatalf("LogLevel = %q, want prod default when Agora ignores LIVEKIT_DEV_MODE", opts.LogLevel)
	}
}

func TestResolveWorkerOptionsDoesNotDefaultLiveKitWorkerTypeForAgora(t *testing.T) {
	opts := resolveWorkerOptions(WorkerOptions{
		Transport: WorkerTransportAgora,
		Agora: AgoraOptions{
			AppID:   "agora-app",
			Channel: "support",
		},
	})

	if opts.WorkerType != "" {
		t.Fatalf("WorkerType = %q, want empty for Agora", opts.WorkerType)
	}
}
