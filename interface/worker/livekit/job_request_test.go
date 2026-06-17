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

func TestLocalRoomJobUsesFakeJobPrefixAndRoomInfo(t *testing.T) {
	room := &lkprotocol.Room{Name: "configured-room", Sid: "SRM_configured"}
	job := workerlivekit.LocalRoomJob(workerlivekit.LocalRoomJobOptions{
		RoomName: "ignored-room",
		RoomInfo: room,
		FakeJob:  true,
		NewID: func(prefix string) string {
			return prefix + "id"
		},
	})

	if job.Id != "mock-job-id" {
		t.Fatalf("Job.Id = %q, want mock-job-id", job.Id)
	}
	if job.Room != room {
		t.Fatal("Job.Room did not use configured room info")
	}
	if job.Type != lkprotocol.JobType_JT_ROOM {
		t.Fatalf("Job.Type = %v, want JT_ROOM", job.Type)
	}
}

func TestLocalRoomJobBuildsRoomWhenRoomInfoMissing(t *testing.T) {
	job := workerlivekit.LocalRoomJob(workerlivekit.LocalRoomJobOptions{
		RoomName: "local-room",
		NewID: func(prefix string) string {
			return prefix + "id"
		},
	})

	if job.Id != "job-id" {
		t.Fatalf("Job.Id = %q, want job-id", job.Id)
	}
	if job.GetRoom().GetName() != "local-room" {
		t.Fatalf("Job.Room.Name = %q, want local-room", job.GetRoom().GetName())
	}
	if job.GetRoom().GetSid() != "SRM_id" {
		t.Fatalf("Job.Room.Sid = %q, want SRM_id", job.GetRoom().GetSid())
	}
	if job.Type != lkprotocol.JobType_JT_ROOM {
		t.Fatalf("Job.Type = %v, want JT_ROOM", job.Type)
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

func TestMoveParticipantRequestUsesExplicitDestinationRoom(t *testing.T) {
	req := workerlivekit.MoveParticipantRequest(
		&lkprotocol.Job{Room: &lkprotocol.Room{Name: "caller-room"}},
		"human-room",
		"human-agent-sip",
		"destination-room",
	)

	if req.Room != "human-room" {
		t.Fatalf("MoveParticipantRequest.Room = %q, want human-room", req.Room)
	}
	if req.Identity != "human-agent-sip" {
		t.Fatalf("MoveParticipantRequest.Identity = %q, want human-agent-sip", req.Identity)
	}
	if req.DestinationRoom != "destination-room" {
		t.Fatalf("MoveParticipantRequest.DestinationRoom = %q, want destination-room", req.DestinationRoom)
	}
}

func TestMoveParticipantRequestDefaultsDestinationRoomFromJob(t *testing.T) {
	req := workerlivekit.MoveParticipantRequest(
		&lkprotocol.Job{Room: &lkprotocol.Room{Name: "caller-room"}},
		"human-room",
		"human-agent-sip",
		"",
	)

	if req.DestinationRoom != "caller-room" {
		t.Fatalf("MoveParticipantRequest.DestinationRoom = %q, want caller-room", req.DestinationRoom)
	}
}
