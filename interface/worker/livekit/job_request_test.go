package livekit_test

import (
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lkprotocol "github.com/livekit/protocol/livekit"
)

func TestJobRequestAccessorsExposeJobFields(t *testing.T) {
	room := &lkprotocol.Room{Name: "room-a"}
	publisher := &lkprotocol.ParticipantInfo{Identity: "publisher-a"}
	job := &lkprotocol.Job{
		Id:          "job_request",
		Room:        room,
		Participant: publisher,
		AgentName:   "agent-a",
	}

	if got := workerlivekit.JobID(job); got != "job_request" {
		t.Fatalf("JobID() = %q, want job_request", got)
	}
	if got := workerlivekit.JobRoom(job); got != room {
		t.Fatal("JobRoom() did not return the job room")
	}
	if got := workerlivekit.JobPublisher(job); got != publisher {
		t.Fatal("JobPublisher() did not return the job participant")
	}
	if got := workerlivekit.JobAgentName(job); got != "agent-a" {
		t.Fatalf("JobAgentName() = %q, want agent-a", got)
	}
}

func TestJobRequestAccessorsHandleNilJob(t *testing.T) {
	if got := workerlivekit.JobID(nil); got != "" {
		t.Fatalf("JobID(nil) = %q, want empty", got)
	}
	if got := workerlivekit.JobRoom(nil); got != nil {
		t.Fatalf("JobRoom(nil) = %#v, want nil", got)
	}
	if got := workerlivekit.JobPublisher(nil); got != nil {
		t.Fatalf("JobPublisher(nil) = %#v, want nil", got)
	}
	if got := workerlivekit.JobAgentName(nil); got != "" {
		t.Fatalf("JobAgentName(nil) = %q, want empty", got)
	}
}

func TestJobAcceptIdentityDefaultsFromJobID(t *testing.T) {
	got := workerlivekit.JobAcceptIdentity(&lkprotocol.Job{Id: "job_accept"}, "")
	if got != "agent-job_accept" {
		t.Fatalf("JobAcceptIdentity() = %q, want default identity", got)
	}
}

func TestJobAcceptIdentityKeepsConfiguredIdentity(t *testing.T) {
	got := workerlivekit.JobAcceptIdentity(&lkprotocol.Job{Id: "job_accept"}, "custom-agent")
	if got != "custom-agent" {
		t.Fatalf("JobAcceptIdentity() = %q, want configured identity", got)
	}
}

func TestJobParticipantIdentityDefaultsFromJobID(t *testing.T) {
	got := workerlivekit.JobParticipantIdentity(&lkprotocol.Job{Id: "job_context"}, "")
	if got != "agent-job_context" {
		t.Fatalf("JobParticipantIdentity() = %q, want default identity", got)
	}
}

func TestJobParticipantIdentityKeepsAcceptedIdentity(t *testing.T) {
	got := workerlivekit.JobParticipantIdentity(&lkprotocol.Job{Id: "job_context"}, "accepted-agent")
	if got != "accepted-agent" {
		t.Fatalf("JobParticipantIdentity() = %q, want accepted identity", got)
	}
}

func TestJobParticipantIdentityHandlesNilJob(t *testing.T) {
	got := workerlivekit.JobParticipantIdentity(nil, "")
	if got != "" {
		t.Fatalf("JobParticipantIdentity(nil) = %q, want empty", got)
	}
}
