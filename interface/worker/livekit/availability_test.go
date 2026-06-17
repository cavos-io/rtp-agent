package livekit_test

import (
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lkprotocol "github.com/livekit/protocol/livekit"
)

func TestAvailabilityResponseAcceptsWithDefaultIdentity(t *testing.T) {
	resp := workerlivekit.AvailabilityResponseForAccept(&lkprotocol.AvailabilityRequest{
		Job: &lkprotocol.Job{Id: "job_abc123"},
	}, workerlivekit.AvailabilityAcceptOptions{}, "")

	availability := resp.GetAvailability()
	if availability == nil {
		t.Fatal("availability response is nil")
	}
	if !availability.Available {
		t.Fatal("availability.Available = false, want true")
	}
	if availability.JobId != "job_abc123" {
		t.Fatalf("availability.JobId = %q, want job_abc123", availability.JobId)
	}
	if availability.ParticipantIdentity != "agent-job_abc123" {
		t.Fatalf("availability.ParticipantIdentity = %q, want default identity", availability.ParticipantIdentity)
	}
	if availability.ParticipantAttributes[workerlivekit.ParticipantAttributeAgentName] != "" {
		t.Fatalf("agent attribute = %q, want empty", availability.ParticipantAttributes[workerlivekit.ParticipantAttributeAgentName])
	}
}

func TestAvailabilityInfoExposesJobAndJobID(t *testing.T) {
	job := &lkprotocol.Job{Id: "job_available"}
	info := workerlivekit.AvailabilityInfo(&lkprotocol.AvailabilityRequest{Job: job})

	if info.Job != job {
		t.Fatal("AvailabilityInfo().Job did not preserve request job")
	}
	if info.JobID != "job_available" {
		t.Fatalf("AvailabilityInfo().JobID = %q, want job_available", info.JobID)
	}
}

func TestAvailabilityResponseAcceptUsesCustomArguments(t *testing.T) {
	resp := workerlivekit.AvailabilityResponseForAccept(&lkprotocol.AvailabilityRequest{
		Job: &lkprotocol.Job{Id: "job_custom"},
	}, workerlivekit.AvailabilityAcceptOptions{
		Name:     "Agent Name",
		Identity: "custom-agent",
		Metadata: "custom-metadata",
		Attributes: map[string]string{
			"tier": "gold",
		},
	}, "sales-agent")

	availability := resp.GetAvailability()
	if availability.JobId != "job_custom" {
		t.Fatalf("availability.JobId = %q, want job_custom", availability.JobId)
	}
	if availability.ParticipantIdentity != "custom-agent" {
		t.Fatalf("ParticipantIdentity = %q, want custom-agent", availability.ParticipantIdentity)
	}
	if availability.ParticipantName != "Agent Name" {
		t.Fatalf("ParticipantName = %q, want Agent Name", availability.ParticipantName)
	}
	if availability.ParticipantMetadata != "custom-metadata" {
		t.Fatalf("ParticipantMetadata = %q, want custom-metadata", availability.ParticipantMetadata)
	}
	if availability.ParticipantAttributes["tier"] != "gold" {
		t.Fatalf("ParticipantAttributes[tier] = %q, want gold", availability.ParticipantAttributes["tier"])
	}
	if availability.ParticipantAttributes[workerlivekit.ParticipantAttributeAgentName] != "sales-agent" {
		t.Fatalf("agent attribute = %q, want sales-agent", availability.ParticipantAttributes[workerlivekit.ParticipantAttributeAgentName])
	}
}

func TestAvailabilityResponseRejectsJob(t *testing.T) {
	resp := workerlivekit.AvailabilityResponseForReject(&lkprotocol.AvailabilityRequest{
		Job: &lkprotocol.Job{Id: "job_reject"},
	}, workerlivekit.AvailabilityRejectOptions{Terminate: true})

	availability := resp.GetAvailability()
	if availability == nil {
		t.Fatal("availability response is nil")
	}
	if availability.Available {
		t.Fatal("availability.Available = true, want false")
	}
	if availability.JobId != "job_reject" {
		t.Fatalf("availability.JobId = %q, want job_reject", availability.JobId)
	}
	if !availability.Terminate {
		t.Fatal("availability.Terminate = false, want true")
	}
}

func TestAvailabilityResponseRejectCanAvoidTermination(t *testing.T) {
	resp := workerlivekit.AvailabilityResponseForReject(&lkprotocol.AvailabilityRequest{
		Job: &lkprotocol.Job{Id: "job_reject"},
	}, workerlivekit.AvailabilityRejectOptions{Terminate: false})

	if resp.GetAvailability().Terminate {
		t.Fatal("availability.Terminate = true, want false")
	}
}
