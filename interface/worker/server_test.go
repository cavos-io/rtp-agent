package worker

import "testing"

func TestNewAgentServerLoadsLiveKitOptionsFromEnvironment(t *testing.T) {
	t.Setenv("LIVEKIT_URL", "wss://livekit.example")
	t.Setenv("LIVEKIT_API_KEY", "env-key")
	t.Setenv("LIVEKIT_API_SECRET", "env-secret")
	t.Setenv("LIVEKIT_AGENT_NAME", "env-agent")
	t.Setenv("HTTPS_PROXY", "https://proxy.example")
	t.Setenv("HTTP_PROXY", "http://proxy.example")

	server := NewAgentServer(WorkerOptions{})

	if server.Options.WSRL != "wss://livekit.example" {
		t.Fatalf("WSRL = %q, want env LIVEKIT_URL", server.Options.WSRL)
	}
	if server.Options.APIKey != "env-key" {
		t.Fatalf("APIKey = %q, want env LIVEKIT_API_KEY", server.Options.APIKey)
	}
	if server.Options.APISecret != "env-secret" {
		t.Fatalf("APISecret = %q, want env LIVEKIT_API_SECRET", server.Options.APISecret)
	}
	if server.Options.AgentName != "env-agent" {
		t.Fatalf("AgentName = %q, want env LIVEKIT_AGENT_NAME", server.Options.AgentName)
	}
	if server.Options.HTTPProxy != "https://proxy.example" {
		t.Fatalf("HTTPProxy = %q, want env HTTPS_PROXY", server.Options.HTTPProxy)
	}
}

func TestNewAgentServerExplicitOptionsOverrideEnvironment(t *testing.T) {
	t.Setenv("LIVEKIT_URL", "wss://env.example")
	t.Setenv("LIVEKIT_API_KEY", "env-key")
	t.Setenv("LIVEKIT_API_SECRET", "env-secret")
	t.Setenv("LIVEKIT_AGENT_NAME", "env-agent")
	t.Setenv("HTTPS_PROXY", "https://env-proxy.example")

	server := NewAgentServer(WorkerOptions{
		AgentName: "explicit-agent",
		WSRL:      "wss://explicit.example",
		APIKey:    "explicit-key",
		APISecret: "explicit-secret",
		HTTPProxy: "https://explicit-proxy.example",
	})

	if server.Options.WSRL != "wss://explicit.example" {
		t.Fatalf("WSRL = %q, want explicit value", server.Options.WSRL)
	}
	if server.Options.APIKey != "explicit-key" {
		t.Fatalf("APIKey = %q, want explicit value", server.Options.APIKey)
	}
	if server.Options.APISecret != "explicit-secret" {
		t.Fatalf("APISecret = %q, want explicit value", server.Options.APISecret)
	}
	if server.Options.AgentName != "explicit-agent" {
		t.Fatalf("AgentName = %q, want explicit value", server.Options.AgentName)
	}
	if server.Options.HTTPProxy != "https://explicit-proxy.example" {
		t.Fatalf("HTTPProxy = %q, want explicit value", server.Options.HTTPProxy)
	}
}

func TestNewAgentServerPrefersWSURLAliasOverDeprecatedWSRL(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		WSURL: "wss://canonical.example",
		WSRL:  "wss://legacy.example",
	})

	if server.Options.WSRL != "wss://canonical.example" {
		t.Fatalf("WSRL = %q, want canonical WSURL value", server.Options.WSRL)
	}
	if server.Options.WSURL != "wss://canonical.example" {
		t.Fatalf("WSURL = %q, want canonical WSURL value", server.Options.WSURL)
	}
}
