package livekit_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

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

func TestJobAliasUsesLiveKitProtocolJob(t *testing.T) {
	job := &workerlivekit.Job{Id: "job-alias"}

	if job.GetId() != "job-alias" {
		t.Fatalf("Job.GetId() = %q, want job-alias", job.GetId())
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

func TestCloneRunningJobInfoCopiesAcceptAttributes(t *testing.T) {
	info := workerlivekit.RunningJobInfo{
		AcceptArguments: workerlivekit.JobAcceptArguments{
			Attributes: map[string]string{"tier": "gold"},
		},
		Job: &lkprotocol.Job{Id: "job-a"},
	}

	clone := workerlivekit.CloneRunningJobInfo(info)
	clone.AcceptArguments.Attributes["tier"] = "platinum"

	if info.AcceptArguments.Attributes["tier"] != "gold" {
		t.Fatalf("original tier = %q, want gold", info.AcceptArguments.Attributes["tier"])
	}
	if clone.Job != info.Job {
		t.Fatal("CloneRunningJobInfo changed job pointer, want shallow job copy")
	}
}

func TestRunningJobInfoSnapshotCopiesAcceptAttributes(t *testing.T) {
	accept := workerlivekit.JobAcceptArguments{
		Name:       "Agent A",
		Identity:   "agent-a",
		Metadata:   "metadata-a",
		Attributes: map[string]string{"tier": "gold"},
	}
	job := &lkprotocol.Job{Id: "job-a"}

	info := workerlivekit.RunningJobInfoSnapshot(workerlivekit.RunningJobInfoOptions{
		AcceptArguments: accept,
		Job:             job,
		URL:             "wss://livekit.example",
		Token:           "room-token",
		WorkerID:        "worker-a",
		FakeJob:         true,
	})
	info.AcceptArguments.Attributes["tier"] = "platinum"

	if accept.Attributes["tier"] != "gold" {
		t.Fatalf("original tier = %q, want gold", accept.Attributes["tier"])
	}
	if info.Job != job {
		t.Fatal("RunningJobInfoSnapshot changed job pointer, want shallow job copy")
	}
	if info.URL != "wss://livekit.example" || info.Token != "room-token" || info.WorkerID != "worker-a" || !info.FakeJob {
		t.Fatalf("RunningJobInfoSnapshot() = %#v, want assignment fields preserved", info)
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

func TestNewJobSessionReportInitializesReportTaggerAndMetadata(t *testing.T) {
	report, tagger := workerlivekit.NewJobSessionReport(&lkprotocol.Job{
		Id: "job-report",
		Room: &lkprotocol.Room{
			Sid:  "RM_report",
			Name: "room-report",
		},
	})

	if report == nil {
		t.Fatal("NewJobSessionReport() report = nil")
	}
	if tagger == nil {
		t.Fatal("NewJobSessionReport() tagger = nil")
	}
	if report.Tagger != tagger {
		t.Fatal("NewJobSessionReport() report.Tagger does not match returned tagger")
	}
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

func TestAllRecordingOptionsEnablesReferenceReportCapture(t *testing.T) {
	options := workerlivekit.AllRecordingOptions()

	if options != (agent.RecordingOptions{Audio: true, Traces: true, Logs: true, Transcript: true}) {
		t.Fatalf("AllRecordingOptions() = %#v, want all recording options enabled", options)
	}
}

func TestShouldUploadJobSessionReportUsesRecordingOptions(t *testing.T) {
	report := agent.NewSessionReport()
	report.RecordingOptions = agent.RecordingOptions{Logs: true}

	if !workerlivekit.ShouldUploadJobSessionReport(&lkprotocol.Job{Id: "job-report"}, false, report) {
		t.Fatal("ShouldUploadJobSessionReport(logs-only) = false, want true")
	}
}

func TestShouldUploadJobSessionReportSkipsNilFakeOrEmptyReport(t *testing.T) {
	report := agent.NewSessionReport()
	report.RecordingOptions = agent.RecordingOptions{Logs: true}

	if workerlivekit.ShouldUploadJobSessionReport(nil, false, report) {
		t.Fatal("ShouldUploadJobSessionReport(nil job) = true, want false")
	}
	if workerlivekit.ShouldUploadJobSessionReport(&lkprotocol.Job{Id: "job-fake"}, true, report) {
		t.Fatal("ShouldUploadJobSessionReport(fake job) = true, want false")
	}
	if workerlivekit.ShouldUploadJobSessionReport(&lkprotocol.Job{Id: "job-empty"}, false, nil) {
		t.Fatal("ShouldUploadJobSessionReport(nil report) = true, want false")
	}
	if workerlivekit.ShouldUploadJobSessionReport(&lkprotocol.Job{Id: "job-empty"}, false, agent.NewSessionReport()) {
		t.Fatal("ShouldUploadJobSessionReport(empty report) = true, want false")
	}
}

func TestShouldSkipExternalAPIForFakeJobSkipsFakeJobsOnly(t *testing.T) {
	if !workerlivekit.ShouldSkipExternalAPIForFakeJob(true) {
		t.Fatal("ShouldSkipExternalAPIForFakeJob(true) = false, want true")
	}

	if workerlivekit.ShouldSkipExternalAPIForFakeJob(false) {
		t.Fatal("ShouldSkipExternalAPIForFakeJob(false) = true, want false")
	}
}

func TestShouldUploadJobSessionReportUsesEvaluationOrOutcome(t *testing.T) {
	report := agent.NewSessionReport()
	report.Tagger = agent.NewTagger()
	report.Tagger.Evaluation(&agent.EvaluationResult{Judgments: map[string]string{"helpfulness": "pass"}})

	if !workerlivekit.ShouldUploadJobSessionReport(&lkprotocol.Job{Id: "job-eval"}, false, report) {
		t.Fatal("ShouldUploadJobSessionReport(evaluation-only) = false, want true")
	}

	report = agent.NewSessionReport()
	report.Tagger = agent.NewTagger()
	report.Tagger.Success("completed")

	if !workerlivekit.ShouldUploadJobSessionReport(&lkprotocol.Job{Id: "job-outcome"}, false, report) {
		t.Fatal("ShouldUploadJobSessionReport(outcome-only) = false, want true")
	}
}

func TestJobLogContextFieldsExposeLiveKitJobMetadata(t *testing.T) {
	fields := workerlivekit.JobLogContextFields(&lkprotocol.Job{
		Id:   "job-log",
		Room: &lkprotocol.Room{Name: "room-log"},
	})

	if fields["job_id"] != "job-log" {
		t.Fatalf("job_id = %q, want job-log", fields["job_id"])
	}
	if fields["room"] != "room-log" {
		t.Fatalf("room = %q, want room-log", fields["room"])
	}
}

func TestJobLogContextFieldsReturnsStableKeysForNilJob(t *testing.T) {
	fields := workerlivekit.JobLogContextFields(nil)

	if fields["job_id"] != "" {
		t.Fatalf("job_id = %q, want empty", fields["job_id"])
	}
	if fields["room"] != "" {
		t.Fatalf("room = %q, want empty", fields["room"])
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

func TestAssignmentContextValuesPreservesAssignmentFieldsAndAcceptArgs(t *testing.T) {
	job := &lkprotocol.Job{Id: "job-assigned", EnableRecording: true}
	values := workerlivekit.AssignmentContextValues(workerlivekit.AssignmentContextValueOptions{
		Assignment: workerlivekit.AssignmentInfo{
			Job:             job,
			JobID:           "job-assigned",
			URL:             "wss://assignment.example",
			Token:           "assignment-token",
			EnableRecording: true,
		},
		AcceptArguments: workerlivekit.JobAcceptArguments{
			Name:       "Agent Name",
			Identity:   "agent-a",
			Metadata:   "metadata",
			Attributes: map[string]string{"tier": "gold"},
		},
		WorkerID: "worker-a",
	})

	if values.Job != job {
		t.Fatal("AssignmentContextValues().Job did not preserve job")
	}
	if values.JobID != "job-assigned" {
		t.Fatalf("JobID = %q, want job-assigned", values.JobID)
	}
	if values.URL != "wss://assignment.example" {
		t.Fatalf("URL = %q, want assignment URL", values.URL)
	}
	if values.Token != "assignment-token" {
		t.Fatalf("Token = %q, want assignment token", values.Token)
	}
	if values.WorkerID != "worker-a" {
		t.Fatalf("WorkerID = %q, want worker-a", values.WorkerID)
	}
	if values.AcceptArguments.Identity != "agent-a" {
		t.Fatalf("AcceptArguments.Identity = %q, want agent-a", values.AcceptArguments.Identity)
	}
	if values.AcceptArguments.Attributes["tier"] != "gold" {
		t.Fatalf("AcceptArguments.Attributes[tier] = %q, want gold", values.AcceptArguments.Attributes["tier"])
	}
	if !values.EnableRecording {
		t.Fatal("EnableRecording = false, want true")
	}
}

func TestJobAssignmentAliasUsesLiveKitProtocolAssignment(t *testing.T) {
	job := &lkprotocol.Job{Id: "job-a"}
	assignment := &workerlivekit.JobAssignment{Job: job}
	protocolAssignment := (*lkprotocol.JobAssignment)(assignment)

	if protocolAssignment.GetJob() != job {
		t.Fatal("JobAssignment alias did not preserve protocol job")
	}
}

func TestRunningJobInfoCarriesLiveKitJobAndReloadFields(t *testing.T) {
	info := workerlivekit.RunningJobInfo{
		AcceptArguments: workerlivekit.JobAcceptArguments{
			Identity: "agent-job-a",
		},
		Job:      &lkprotocol.Job{Id: "job-a"},
		URL:      "wss://livekit.example",
		Token:    "room-token",
		WorkerID: "worker-a",
		FakeJob:  true,
	}

	if info.Job.GetId() != "job-a" {
		t.Fatalf("Job.Id = %q, want job-a", info.Job.GetId())
	}
	if info.AcceptArguments.Identity != "agent-job-a" {
		t.Fatalf("AcceptArguments.Identity = %q, want agent-job-a", info.AcceptArguments.Identity)
	}
	if info.URL != "wss://livekit.example" {
		t.Fatalf("URL = %q, want wss://livekit.example", info.URL)
	}
	if info.Token != "room-token" {
		t.Fatalf("Token = %q, want room-token", info.Token)
	}
	if info.WorkerID != "worker-a" {
		t.Fatalf("WorkerID = %q, want worker-a", info.WorkerID)
	}
	if !info.FakeJob {
		t.Fatal("FakeJob = false, want true")
	}
}

func TestRunningJobContextValuesResolveOverrideURLWorkerAndRecording(t *testing.T) {
	job := &lkprotocol.Job{Id: "job-running", EnableRecording: true}
	values := workerlivekit.RunningJobContextValues(workerlivekit.RunningJobContextValueOptions{
		Info: workerlivekit.RunningJobInfo{
			Job:     job,
			URL:     "wss://info.example",
			Token:   "room-token",
			FakeJob: true,
			AcceptArguments: workerlivekit.JobAcceptArguments{
				Name:       "Agent Name",
				Identity:   "agent-a",
				Metadata:   "metadata",
				Attributes: map[string]string{"tier": "gold"},
			},
		},
		OverrideURL:     "wss://override.example",
		DefaultWorkerID: "worker-default",
	})

	if values.Job != job {
		t.Fatal("RunningJobContextValues().Job did not preserve job")
	}
	if values.JobID != "job-running" {
		t.Fatalf("JobID = %q, want job-running", values.JobID)
	}
	if values.URL != "wss://override.example" {
		t.Fatalf("URL = %q, want override URL", values.URL)
	}
	if values.Token != "room-token" {
		t.Fatalf("Token = %q, want room-token", values.Token)
	}
	if values.WorkerID != "worker-default" {
		t.Fatalf("WorkerID = %q, want default worker", values.WorkerID)
	}
	if values.AcceptArguments.Identity != "agent-a" {
		t.Fatalf("AcceptArguments.Identity = %q, want agent-a", values.AcceptArguments.Identity)
	}
	if values.AcceptArguments.Attributes["tier"] != "gold" {
		t.Fatalf("AcceptArguments.Attributes[tier] = %q, want gold", values.AcceptArguments.Attributes["tier"])
	}
	if !values.FakeJob {
		t.Fatal("FakeJob = false, want true")
	}
	if !values.EnableRecording {
		t.Fatal("EnableRecording = false, want true")
	}
}

func TestRunningJobContextValuesPreservesInfoURLAndWorker(t *testing.T) {
	values := workerlivekit.RunningJobContextValues(workerlivekit.RunningJobContextValueOptions{
		Info: workerlivekit.RunningJobInfo{
			Job:      &lkprotocol.Job{Id: "job-running"},
			URL:      "wss://info.example",
			WorkerID: "worker-info",
		},
		DefaultWorkerID: "worker-default",
	})

	if values.URL != "wss://info.example" {
		t.Fatalf("URL = %q, want info URL", values.URL)
	}
	if values.WorkerID != "worker-info" {
		t.Fatalf("WorkerID = %q, want info worker", values.WorkerID)
	}
}

func TestPopPendingAcceptReturnsAndDeletesAcceptedArgs(t *testing.T) {
	pending := map[string]workerlivekit.JobAcceptArguments{
		"job-a": {Identity: "agent-a"},
		"job-b": {Identity: "agent-b"},
	}

	args, ok := workerlivekit.PopPendingAccept(pending, "job-a")

	if !ok {
		t.Fatal("PopPendingAccept() ok = false, want true")
	}
	if args.Identity != "agent-a" {
		t.Fatalf("Identity = %q, want agent-a", args.Identity)
	}
	if _, exists := pending["job-a"]; exists {
		t.Fatal("job-a remained in pending accepts")
	}
	if pending["job-b"].Identity != "agent-b" {
		t.Fatalf("job-b identity = %q, want agent-b", pending["job-b"].Identity)
	}
}

func TestPopPendingAcceptMissingJobLeavesPendingAccepts(t *testing.T) {
	pending := map[string]workerlivekit.JobAcceptArguments{
		"job-b": {Identity: "agent-b"},
	}

	_, ok := workerlivekit.PopPendingAccept(pending, "job-a")

	if ok {
		t.Fatal("PopPendingAccept() ok = true, want false")
	}
	if pending["job-b"].Identity != "agent-b" {
		t.Fatalf("job-b identity = %q, want agent-b", pending["job-b"].Identity)
	}
}

type fakePendingAssignmentTimer struct {
	stopped bool
}

func (f *fakePendingAssignmentTimer) Stop() bool {
	f.stopped = true
	return true
}

func TestStopPendingAssignmentTimerStopsAndDeletesTimer(t *testing.T) {
	timer := &fakePendingAssignmentTimer{}
	pending := map[string]*fakePendingAssignmentTimer{
		"job-a": timer,
		"job-b": {},
	}

	workerlivekit.StopPendingAssignmentTimer(pending, "job-a")

	if !timer.stopped {
		t.Fatal("timer stopped = false, want true")
	}
	if _, exists := pending["job-a"]; exists {
		t.Fatal("job-a timer remained pending")
	}
	if _, exists := pending["job-b"]; !exists {
		t.Fatal("job-b timer was removed")
	}
}

func TestStopPendingAssignmentTimerMissingJobLeavesTimers(t *testing.T) {
	pending := map[string]*fakePendingAssignmentTimer{
		"job-b": {},
	}

	workerlivekit.StopPendingAssignmentTimer(pending, "job-a")

	if _, exists := pending["job-b"]; !exists {
		t.Fatal("job-b timer was removed")
	}
}

func TestStorePendingAcceptStoresArgsAndReplacesTimer(t *testing.T) {
	oldTimer := time.AfterFunc(time.Hour, func() {})
	t.Cleanup(func() { oldTimer.Stop() })
	pending := map[string]workerlivekit.JobAcceptArguments{}
	timers := map[string]*time.Timer{"job-a": oldTimer}

	workerlivekit.StorePendingAccept(workerlivekit.PendingAcceptStoreOptions{
		Pending: pending,
		Timers:  timers,
		JobID:   "job-a",
		Args:    workerlivekit.JobAcceptArguments{Identity: "agent-a"},
		Timeout: time.Hour,
	})
	t.Cleanup(func() { timers["job-a"].Stop() })

	if pending["job-a"].Identity != "agent-a" {
		t.Fatalf("pending identity = %q, want agent-a", pending["job-a"].Identity)
	}
	if timers["job-a"] == nil {
		t.Fatal("timer is nil, want pending assignment timer")
	}
	if timers["job-a"] == oldTimer {
		t.Fatal("timer was not replaced")
	}
	if oldTimer.Stop() {
		t.Fatal("old timer Stop() = true, want already stopped by replacement")
	}
}

func TestExpirePendingAcceptDeletesOnlyMatchingTimer(t *testing.T) {
	timer := time.AfterFunc(time.Hour, func() {})
	otherTimer := time.AfterFunc(time.Hour, func() {})
	t.Cleanup(func() {
		timer.Stop()
		otherTimer.Stop()
	})
	pending := map[string]workerlivekit.JobAcceptArguments{"job-a": {Identity: "agent-a"}}
	timers := map[string]*time.Timer{"job-a": timer}

	if workerlivekit.ExpirePendingAccept(pending, timers, "job-a", otherTimer) {
		t.Fatal("ExpirePendingAccept(other timer) = true, want false")
	}
	if pending["job-a"].Identity != "agent-a" {
		t.Fatalf("pending identity = %q, want agent-a", pending["job-a"].Identity)
	}
	if timers["job-a"] != timer {
		t.Fatal("timer changed after non-matching expire")
	}

	if !workerlivekit.ExpirePendingAccept(pending, timers, "job-a", timer) {
		t.Fatal("ExpirePendingAccept(matching timer) = false, want true")
	}
	if _, exists := pending["job-a"]; exists {
		t.Fatal("job-a remained in pending accepts")
	}
	if _, exists := timers["job-a"]; exists {
		t.Fatal("job-a timer remained pending")
	}
}

func TestAcceptPendingAssignmentReturnsArgsAndStopsTimer(t *testing.T) {
	timer := &fakePendingAssignmentTimer{}
	pending := map[string]workerlivekit.JobAcceptArguments{
		"job-a": {Identity: "agent-a"},
		"job-b": {Identity: "agent-b"},
	}
	timers := map[string]*fakePendingAssignmentTimer{
		"job-a": timer,
		"job-b": {},
	}

	args, ok := workerlivekit.AcceptPendingAssignment(pending, timers, "job-a")

	if !ok {
		t.Fatal("AcceptPendingAssignment() ok = false, want true")
	}
	if args.Identity != "agent-a" {
		t.Fatalf("Identity = %q, want agent-a", args.Identity)
	}
	if _, exists := pending["job-a"]; exists {
		t.Fatal("job-a remained in pending accepts")
	}
	if !timer.stopped {
		t.Fatal("timer stopped = false, want true")
	}
	if _, exists := timers["job-a"]; exists {
		t.Fatal("job-a timer remained pending")
	}
	if pending["job-b"].Identity != "agent-b" {
		t.Fatalf("job-b identity = %q, want agent-b", pending["job-b"].Identity)
	}
	if _, exists := timers["job-b"]; !exists {
		t.Fatal("job-b timer was removed")
	}
}

func TestAcceptPendingAssignmentMissingJobLeavesState(t *testing.T) {
	pending := map[string]workerlivekit.JobAcceptArguments{
		"job-b": {Identity: "agent-b"},
	}
	timers := map[string]*fakePendingAssignmentTimer{
		"job-b": {},
	}

	_, ok := workerlivekit.AcceptPendingAssignment(pending, timers, "job-a")

	if ok {
		t.Fatal("AcceptPendingAssignment() ok = true, want false")
	}
	if pending["job-b"].Identity != "agent-b" {
		t.Fatalf("job-b identity = %q, want agent-b", pending["job-b"].Identity)
	}
	if _, exists := timers["job-b"]; !exists {
		t.Fatal("job-b timer was removed")
	}
}

func TestJobTerminationInfoExposesJobID(t *testing.T) {
	info := workerlivekit.JobTerminationInfo(&lkprotocol.JobTermination{JobId: "job-stop"})

	if info.JobID != "job-stop" {
		t.Fatalf("JobTerminationInfo().JobID = %q, want job-stop", info.JobID)
	}
}

func TestJobTerminationPlanForActiveJobTerminatesAndFinishes(t *testing.T) {
	plan := workerlivekit.JobTerminationPlanForActiveJob(true)

	if !plan.MarkTerminated {
		t.Fatal("MarkTerminated = false, want true")
	}
	if !plan.Shutdown {
		t.Fatal("Shutdown = false, want true")
	}
	if !plan.WaitEntrypoint {
		t.Fatal("WaitEntrypoint = false, want true")
	}
	if !plan.Finish {
		t.Fatal("Finish = false, want true")
	}
}

func TestJobTerminationPlanForMissingJobDoesNothing(t *testing.T) {
	plan := workerlivekit.JobTerminationPlanForActiveJob(false)

	if plan.MarkTerminated {
		t.Fatal("MarkTerminated = true, want false")
	}
	if plan.Shutdown {
		t.Fatal("Shutdown = true, want false")
	}
	if plan.WaitEntrypoint {
		t.Fatal("WaitEntrypoint = true, want false")
	}
	if plan.Finish {
		t.Fatal("Finish = true, want false")
	}
}

func TestJobTerminationAliasUsesLiveKitProtocolTermination(t *testing.T) {
	termination := &workerlivekit.JobTermination{JobId: "job-stop"}
	protocolTermination := (*lkprotocol.JobTermination)(termination)

	if protocolTermination.GetJobId() != "job-stop" {
		t.Fatalf("JobTermination alias job id = %q, want job-stop", protocolTermination.GetJobId())
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
