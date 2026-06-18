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

func TestAvailabilityRequestAliasUsesLiveKitProtocolRequest(t *testing.T) {
	job := &lkprotocol.Job{Id: "job_available"}
	req := &workerlivekit.AvailabilityRequest{Job: job}
	protocolReq := (*lkprotocol.AvailabilityRequest)(req)

	if protocolReq.GetJob() != job {
		t.Fatal("AvailabilityRequest alias did not preserve protocol job")
	}
}

func TestAvailabilityOptionsShareJobRequestArgumentTypes(t *testing.T) {
	accept := workerlivekit.JobAcceptArguments{Identity: "agent-a"}
	reject := workerlivekit.JobRejectArguments{Terminate: true}

	var availabilityAccept workerlivekit.AvailabilityAcceptOptions = accept
	var availabilityReject workerlivekit.AvailabilityRejectOptions = reject

	if availabilityAccept.Identity != "agent-a" {
		t.Fatalf("availability accept identity = %q, want agent-a", availabilityAccept.Identity)
	}
	if !availabilityReject.Terminate {
		t.Fatal("availability reject terminate = false, want true")
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

func TestAvailabilityResponderAcceptStoresAndSendsResponse(t *testing.T) {
	req := &lkprotocol.AvailabilityRequest{Job: &lkprotocol.Job{Id: "job_accept"}}
	var storedJobID string
	var storedArgs workerlivekit.JobAcceptArguments
	var sent *lkprotocol.WorkerMessage
	responder := workerlivekit.NewAvailabilityResponder(workerlivekit.AvailabilityResponderOptions{
		Request:   req,
		AgentName: "sales-agent",
		StoreAccept: func(jobID string, args workerlivekit.JobAcceptArguments) {
			storedJobID = jobID
			storedArgs = args
		},
		Send: func(msg *lkprotocol.WorkerMessage) error {
			sent = msg
			return nil
		},
	})

	err := responder.JobRequest().Accept(workerlivekit.JobAcceptArguments{
		Name:     "Agent Name",
		Identity: "custom-agent",
		Metadata: "metadata",
		Attributes: map[string]string{
			"tier": "gold",
		},
	})
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if !responder.Answered() {
		t.Fatal("Answered() = false, want true")
	}
	if storedJobID != "job_accept" {
		t.Fatalf("stored jobID = %q, want job_accept", storedJobID)
	}
	if storedArgs.Identity != "custom-agent" {
		t.Fatalf("stored identity = %q, want custom-agent", storedArgs.Identity)
	}
	availability := sent.GetAvailability()
	if availability.GetParticipantAttributes()[workerlivekit.ParticipantAttributeAgentName] != "sales-agent" {
		t.Fatalf("agent attribute = %q, want sales-agent", availability.GetParticipantAttributes()[workerlivekit.ParticipantAttributeAgentName])
	}
	if availability.GetParticipantAttributes()["tier"] != "gold" {
		t.Fatalf("tier attribute = %q, want gold", availability.GetParticipantAttributes()["tier"])
	}
}

func TestAvailabilityResponderRejectIfUnansweredSendsOnlyOnce(t *testing.T) {
	req := &lkprotocol.AvailabilityRequest{Job: &lkprotocol.Job{Id: "job_reject"}}
	var sent []*lkprotocol.WorkerMessage
	responder := workerlivekit.NewAvailabilityResponder(workerlivekit.AvailabilityResponderOptions{
		Request: req,
		Send: func(msg *lkprotocol.WorkerMessage) error {
			sent = append(sent, msg)
			return nil
		},
	})

	err := responder.RejectIfUnanswered(workerlivekit.JobRejectArguments{Terminate: false})
	if err != nil {
		t.Fatalf("RejectIfUnanswered() error = %v", err)
	}
	err = responder.RejectIfUnanswered(workerlivekit.JobRejectArguments{Terminate: true})
	if err != nil {
		t.Fatalf("second RejectIfUnanswered() error = %v", err)
	}
	if len(sent) != 1 {
		t.Fatalf("sent responses = %d, want 1", len(sent))
	}
	availability := sent[0].GetAvailability()
	if availability.GetJobId() != "job_reject" {
		t.Fatalf("JobId = %q, want job_reject", availability.GetJobId())
	}
	if availability.GetTerminate() {
		t.Fatal("Terminate = true, want false")
	}
}

func TestAnswerAvailabilityRequestRejectsWhenUnavailable(t *testing.T) {
	req := &lkprotocol.AvailabilityRequest{Job: &lkprotocol.Job{Id: "job_busy"}}
	var reserveCalls int
	var sent []*lkprotocol.WorkerMessage

	workerlivekit.AnswerAvailabilityRequest(workerlivekit.AvailabilityAnswerOptions{
		Request: req,
		AvailableForJob: func() bool {
			return false
		},
		ReserveSlot: func() {
			reserveCalls++
		},
		Send: func(msg *lkprotocol.WorkerMessage) error {
			sent = append(sent, msg)
			return nil
		},
	})

	if reserveCalls != 0 {
		t.Fatalf("ReserveSlot calls = %d, want 0", reserveCalls)
	}
	if len(sent) != 1 {
		t.Fatalf("sent responses = %d, want 1", len(sent))
	}
	availability := sent[0].GetAvailability()
	if availability.GetAvailable() {
		t.Fatal("availability.Available = true, want false")
	}
	if availability.GetTerminate() {
		t.Fatal("availability.Terminate = true, want false")
	}
}

func TestAnswerAvailabilityRequestDefaultsToAcceptAndReleasesReservedSlot(t *testing.T) {
	req := &lkprotocol.AvailabilityRequest{Job: &lkprotocol.Job{Id: "job_default"}}
	var reserveCalls int
	var releaseCalls int
	var storedJobID string
	var sent []*lkprotocol.WorkerMessage

	workerlivekit.AnswerAvailabilityRequest(workerlivekit.AvailabilityAnswerOptions{
		Request:   req,
		AgentName: "sales-agent",
		AvailableForJob: func() bool {
			return true
		},
		ReserveSlot: func() {
			reserveCalls++
		},
		ReleaseSlot: func() {
			releaseCalls++
		},
		StoreAccept: func(jobID string, _ workerlivekit.JobAcceptArguments) {
			storedJobID = jobID
		},
		Send: func(msg *lkprotocol.WorkerMessage) error {
			sent = append(sent, msg)
			return nil
		},
	})

	if reserveCalls != 1 {
		t.Fatalf("ReserveSlot calls = %d, want 1", reserveCalls)
	}
	if releaseCalls != 1 {
		t.Fatalf("ReleaseSlot calls = %d, want 1", releaseCalls)
	}
	if storedJobID != "job_default" {
		t.Fatalf("stored jobID = %q, want job_default", storedJobID)
	}
	if len(sent) != 1 {
		t.Fatalf("sent responses = %d, want 1", len(sent))
	}
	availability := sent[0].GetAvailability()
	if !availability.GetAvailable() {
		t.Fatal("availability.Available = false, want true")
	}
	if availability.GetParticipantAttributes()[workerlivekit.ParticipantAttributeAgentName] != "sales-agent" {
		t.Fatalf("agent attribute = %q, want sales-agent", availability.GetParticipantAttributes()[workerlivekit.ParticipantAttributeAgentName])
	}
}
