package livekit_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	"github.com/cavos-io/rtp-agent/library/inference"
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

func TestJobRequestAcceptInvokesCallbackWithDefaultIdentity(t *testing.T) {
	var got workerlivekit.JobAcceptArguments
	req := workerlivekit.NewJobRequest(&lkprotocol.Job{Id: "job_accept"}, func(args workerlivekit.JobAcceptArguments) error {
		got = args
		return nil
	}, nil)

	if err := req.Accept(); err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if got.Identity != "agent-job_accept" {
		t.Fatalf("Accept() Identity = %q, want agent-job_accept", got.Identity)
	}
}

func TestJobRequestRejectInvokesCallbackWithDefaultTerminate(t *testing.T) {
	var got workerlivekit.JobRejectArguments
	req := workerlivekit.NewJobRequest(nil, nil, func(args workerlivekit.JobRejectArguments) error {
		got = args
		return nil
	})

	if err := req.Reject(); err != nil {
		t.Fatalf("Reject() error = %v", err)
	}
	if !got.Terminate {
		t.Fatal("Reject() Terminate = false, want true")
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

func TestJobRequestArgumentsOwnLiveKitAcceptRejectShape(t *testing.T) {
	accept := workerlivekit.JobAcceptArguments{
		Name:       "Agent A",
		Identity:   "agent-a",
		Metadata:   "metadata-a",
		Attributes: map[string]string{"tier": "gold"},
	}
	reject := workerlivekit.JobRejectArguments{Terminate: true}

	if accept.Identity != "agent-a" {
		t.Fatalf("accept identity = %q, want agent-a", accept.Identity)
	}
	if accept.Attributes["tier"] != "gold" {
		t.Fatalf("accept tier = %q, want gold", accept.Attributes["tier"])
	}
	if !reject.Terminate {
		t.Fatal("reject terminate = false, want true")
	}
}

func TestJobAcceptArgumentsUseIPCWireJSONNames(t *testing.T) {
	data, err := json.Marshal(workerlivekit.JobAcceptArguments{
		Name:       "Agent A",
		Identity:   "agent-a",
		Metadata:   "metadata-a",
		Attributes: map[string]string{"tier": "gold"},
	})
	if err != nil {
		t.Fatalf("Marshal(JobAcceptArguments) error = %v", err)
	}

	want := `{"name":"Agent A","identity":"agent-a","metadata":"metadata-a","attributes":{"tier":"gold"}}`
	if string(data) != want {
		t.Fatalf("JobAcceptArguments JSON = %s, want %s", data, want)
	}
}

func TestJobAcceptArgumentsForJobDefaultsIdentity(t *testing.T) {
	got := workerlivekit.JobAcceptArgumentsForJob(&lkprotocol.Job{Id: "job-123"}, workerlivekit.JobAcceptArguments{})

	if got.Identity != "agent-job-123" {
		t.Fatalf("Identity = %q, want agent-job-123", got.Identity)
	}
}

func TestJobAcceptArgumentsForJobPreservesProvidedIdentity(t *testing.T) {
	got := workerlivekit.JobAcceptArgumentsForJob(&lkprotocol.Job{Id: "job-123"}, workerlivekit.JobAcceptArguments{
		Identity: "custom-agent",
	})

	if got.Identity != "custom-agent" {
		t.Fatalf("Identity = %q, want custom-agent", got.Identity)
	}
}

func TestJobRejectArgumentsDefaultTerminates(t *testing.T) {
	got := workerlivekit.DefaultJobRejectArguments()

	if !got.Terminate {
		t.Fatal("Terminate = false, want true")
	}
}

func TestJobRuntimeInfoExposesJobIDAndRecordingFlag(t *testing.T) {
	info := workerlivekit.JobRuntimeInfo(&lkprotocol.Job{
		Id:              "job-runtime",
		EnableRecording: true,
	})

	if info.JobID != "job-runtime" {
		t.Fatalf("JobRuntimeInfo().JobID = %q, want job-runtime", info.JobID)
	}
	if !info.EnableRecording {
		t.Fatal("JobRuntimeInfo().EnableRecording = false, want true")
	}
}

func TestJobSessionReportInfoExposesJobAndRoomMetadata(t *testing.T) {
	info := workerlivekit.JobSessionReportInfo(&lkprotocol.Job{
		Id: "job-report",
		Room: &lkprotocol.Room{
			Sid:  "RM_report",
			Name: "room-report",
		},
	})

	if info.JobID != "job-report" {
		t.Fatalf("JobSessionReportInfo().JobID = %q, want job-report", info.JobID)
	}
	if info.RoomID != "RM_report" {
		t.Fatalf("JobSessionReportInfo().RoomID = %q, want RM_report", info.RoomID)
	}
	if info.Room != "room-report" {
		t.Fatalf("JobSessionReportInfo().Room = %q, want room-report", info.Room)
	}
}

func TestPopulateSessionReportWithJobMetadataCopiesLiveKitJobFields(t *testing.T) {
	report := agent.NewSessionReport()

	workerlivekit.PopulateSessionReportWithJobMetadata(report, &lkprotocol.Job{
		Id: "job-report",
		Room: &lkprotocol.Room{
			Sid:  "RM_report",
			Name: "room-report",
		},
	})

	if report.JobID != "job-report" {
		t.Fatalf("Report.JobID = %q, want job-report", report.JobID)
	}
	if report.RoomID != "RM_report" {
		t.Fatalf("Report.RoomID = %q, want RM_report", report.RoomID)
	}
	if report.Room != "room-report" {
		t.Fatalf("Report.Room = %q, want room-report", report.Room)
	}
}

func TestJobMetricInfoExposesRoomName(t *testing.T) {
	info := workerlivekit.JobMetricInfo(&lkprotocol.Job{
		Room: &lkprotocol.Room{Name: "metrics-room"},
	})

	if info.RoomName != "metrics-room" {
		t.Fatalf("JobMetricInfo().RoomName = %q, want metrics-room", info.RoomName)
	}
}

func TestJobInferenceHeadersExposeLiveKitJobMetadata(t *testing.T) {
	headers := workerlivekit.JobInferenceHeaders(&lkprotocol.Job{
		Id: "job-inference",
		Room: &lkprotocol.Room{
			Sid: "RM_inference",
		},
	})

	if headers[inference.HeaderJobID] != "job-inference" {
		t.Fatalf("JobInferenceHeaders()[HeaderJobID] = %q, want job-inference", headers[inference.HeaderJobID])
	}
	if headers[inference.HeaderRoomID] != "RM_inference" {
		t.Fatalf("JobInferenceHeaders()[HeaderRoomID] = %q, want RM_inference", headers[inference.HeaderRoomID])
	}
}

func TestJobAssignmentInfoUsesAssignmentURLTokenAndRecordingFlag(t *testing.T) {
	job := &lkprotocol.Job{Id: "job-a", EnableRecording: true}
	assignmentURL := "wss://assignment.example"
	info := workerlivekit.JobAssignmentInfo(&lkprotocol.JobAssignment{
		Job:   job,
		Url:   &assignmentURL,
		Token: "assignment-token",
	}, "wss://default.example")

	if info.Job != job {
		t.Fatal("JobAssignmentInfo().Job did not preserve assignment job")
	}
	if info.JobID != "job-a" {
		t.Fatalf("JobAssignmentInfo().JobID = %q, want job-a", info.JobID)
	}
	if info.URL != "wss://assignment.example" {
		t.Fatalf("JobAssignmentInfo().URL = %q, want assignment URL", info.URL)
	}
	if info.Token != "assignment-token" {
		t.Fatalf("JobAssignmentInfo().Token = %q, want assignment token", info.Token)
	}
	if !info.EnableRecording {
		t.Fatal("JobAssignmentInfo().EnableRecording = false, want true")
	}
}

func TestJobAssignmentInfoDefaultsURLWhenAssignmentURLMissing(t *testing.T) {
	info := workerlivekit.JobAssignmentInfo(&lkprotocol.JobAssignment{
		Job: &lkprotocol.Job{Id: "job-a"},
	}, "wss://default.example")

	if info.URL != "wss://default.example" {
		t.Fatalf("JobAssignmentInfo().URL = %q, want default URL", info.URL)
	}
}

func TestJobTerminationInfoExposesJobID(t *testing.T) {
	info := workerlivekit.JobTerminationInfo(&lkprotocol.JobTermination{JobId: "job-stop"})

	if info.JobID != "job-stop" {
		t.Fatalf("JobTerminationInfo().JobID = %q, want job-stop", info.JobID)
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

func TestLocalJobInfoBuildsExecutorIDFromJobID(t *testing.T) {
	info := workerlivekit.LocalJobInfo(&lkprotocol.Job{Id: "job-local"})

	if info.JobID != "job-local" {
		t.Fatalf("LocalJobInfo().JobID = %q, want job-local", info.JobID)
	}
	if info.ExecutorID != "local_job-local" {
		t.Fatalf("LocalJobInfo().ExecutorID = %q, want local_job-local", info.ExecutorID)
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

func TestMoveParticipantCallsRoomAPI(t *testing.T) {
	api := &fakeMoveParticipantAPI{}
	err := workerlivekit.MoveParticipant(context.Background(), api, &lkprotocol.Job{
		Room: &lkprotocol.Room{Name: "caller-room"},
	}, "human-room", "human-agent-sip", "")
	if err != nil {
		t.Fatalf("MoveParticipant() error = %v", err)
	}
	if api.request == nil {
		t.Fatal("MoveParticipant() did not call room API")
	}
	if api.request.Room != "human-room" {
		t.Fatalf("MoveParticipant().Room = %q, want human-room", api.request.Room)
	}
	if api.request.Identity != "human-agent-sip" {
		t.Fatalf("MoveParticipant().Identity = %q, want human-agent-sip", api.request.Identity)
	}
	if api.request.DestinationRoom != "caller-room" {
		t.Fatalf("MoveParticipant().DestinationRoom = %q, want caller-room", api.request.DestinationRoom)
	}
}

func TestMoveParticipantReturnsRoomAPIError(t *testing.T) {
	wantErr := errors.New("move failed")
	api := &fakeMoveParticipantAPI{err: wantErr}

	err := workerlivekit.MoveParticipant(context.Background(), api, nil, "room-a", "caller-a", "room-b")
	if !errors.Is(err, wantErr) {
		t.Fatalf("MoveParticipant() error = %v, want %v", err, wantErr)
	}
}

type fakeMoveParticipantAPI struct {
	request *lkprotocol.MoveParticipantRequest
	err     error
}

func (f *fakeMoveParticipantAPI) MoveParticipant(_ context.Context, req *lkprotocol.MoveParticipantRequest) (*lkprotocol.MoveParticipantResponse, error) {
	f.request = req
	if f.err != nil {
		return nil, f.err
	}
	return &lkprotocol.MoveParticipantResponse{}, nil
}
