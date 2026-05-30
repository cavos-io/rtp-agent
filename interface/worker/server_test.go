package worker

import (
	"testing"

	"github.com/livekit/protocol/livekit"
)

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

func TestWorkerTypeMapsToLiveKitJobType(t *testing.T) {
	tests := []struct {
		name       string
		workerType WorkerType
		want       livekit.JobType
	}{
		{
			name:       "default",
			workerType: "",
			want:       livekit.JobType_JT_ROOM,
		},
		{
			name:       "room",
			workerType: WorkerTypeRoom,
			want:       livekit.JobType_JT_ROOM,
		},
		{
			name:       "publisher",
			workerType: WorkerTypePublisher,
			want:       livekit.JobType_JT_PUBLISHER,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workerTypeToJobType(tt.workerType); got != tt.want {
				t.Fatalf("workerTypeToJobType(%q) = %v, want %v", tt.workerType, got, tt.want)
			}
		})
	}
}

func TestRegisterWorkerRequestUsesConfiguredWorkerType(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		AgentName:  "publisher-agent",
		WorkerType: WorkerTypePublisher,
	})

	req := server.registerWorkerRequest()
	register := req.GetRegister()
	if register == nil {
		t.Fatal("register worker message is nil")
	}
	if register.Type != livekit.JobType_JT_PUBLISHER {
		t.Fatalf("register.Type = %v, want %v", register.Type, livekit.JobType_JT_PUBLISHER)
	}
	if register.AgentName != "publisher-agent" {
		t.Fatalf("register.AgentName = %q, want %q", register.AgentName, "publisher-agent")
	}
}

func TestAgentIdentityForJobIDUsesFullJobID(t *testing.T) {
	jobID := "job_123456789"
	want := "agent-" + jobID

	if got := agentIdentityForJobID(jobID); got != want {
		t.Fatalf("agentIdentityForJobID(%q) = %q, want %q", jobID, got, want)
	}
}

func TestAgentIdentityForJobIDHandlesShortJobID(t *testing.T) {
	jobID := "abc"
	want := "agent-abc"

	if got := agentIdentityForJobID(jobID); got != want {
		t.Fatalf("agentIdentityForJobID(%q) = %q, want %q", jobID, got, want)
	}
}

func TestAvailabilityResponseAcceptsWithDefaultIdentity(t *testing.T) {
	req := &livekit.AvailabilityRequest{
		Job: &livekit.Job{Id: "job_abc123"},
	}

	resp := availabilityResponseForAccept(req, JobAcceptArguments{}, "")
	availability := resp.GetAvailability()
	if availability == nil {
		t.Fatal("availability response is nil")
	}
	if !availability.Available {
		t.Fatal("availability.Available = false, want true")
	}
	if availability.JobId != "job_abc123" {
		t.Fatalf("availability.JobId = %q, want %q", availability.JobId, "job_abc123")
	}
	if availability.ParticipantIdentity != "agent-job_abc123" {
		t.Fatalf("availability.ParticipantIdentity = %q, want default identity", availability.ParticipantIdentity)
	}
}

func TestAvailabilityResponseAcceptUsesCustomArguments(t *testing.T) {
	req := &livekit.AvailabilityRequest{
		Job: &livekit.Job{Id: "job_custom"},
	}

	resp := availabilityResponseForAccept(req, JobAcceptArguments{
		Name:     "Agent Name",
		Identity: "custom-agent",
		Metadata: "custom-metadata",
		Attributes: map[string]string{
			"tier": "gold",
		},
	}, "sales-agent")

	availability := resp.GetAvailability()
	if availability.ParticipantIdentity != "custom-agent" {
		t.Fatalf("availability.ParticipantIdentity = %q, want custom identity", availability.ParticipantIdentity)
	}
	if availability.ParticipantName != "Agent Name" {
		t.Fatalf("availability.ParticipantName = %q, want custom name", availability.ParticipantName)
	}
	if availability.ParticipantMetadata != "custom-metadata" {
		t.Fatalf("availability.ParticipantMetadata = %q, want custom metadata", availability.ParticipantMetadata)
	}
	if availability.ParticipantAttributes["tier"] != "gold" {
		t.Fatalf("availability.ParticipantAttributes[tier] = %q, want gold", availability.ParticipantAttributes["tier"])
	}
	if availability.ParticipantAttributes["lk.agent.name"] != "sales-agent" {
		t.Fatalf("availability.ParticipantAttributes[lk.agent.name] = %q, want sales-agent", availability.ParticipantAttributes["lk.agent.name"])
	}
}

func TestAvailabilityResponseRejectsJob(t *testing.T) {
	req := &livekit.AvailabilityRequest{
		Job: &livekit.Job{Id: "job_reject"},
	}

	resp := availabilityResponseForReject(req)
	availability := resp.GetAvailability()
	if availability == nil {
		t.Fatal("availability response is nil")
	}
	if availability.Available {
		t.Fatal("availability.Available = true, want false")
	}
	if availability.JobId != "job_reject" {
		t.Fatalf("availability.JobId = %q, want %q", availability.JobId, "job_reject")
	}
}
