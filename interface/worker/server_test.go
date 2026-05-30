package worker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

func TestRegisterWorkerRequestIncludesDefaultPermissions(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})

	register := server.registerWorkerRequest().GetRegister()
	if register == nil {
		t.Fatal("register worker message is nil")
	}

	permissions := register.GetAllowedPermissions()
	if permissions == nil {
		t.Fatal("register.AllowedPermissions = nil, want default permissions")
	}
	if !permissions.CanPublish {
		t.Fatal("permissions.CanPublish = false, want true")
	}
	if !permissions.CanSubscribe {
		t.Fatal("permissions.CanSubscribe = false, want true")
	}
	if !permissions.CanPublishData {
		t.Fatal("permissions.CanPublishData = false, want true")
	}
	if !permissions.CanUpdateMetadata {
		t.Fatal("permissions.CanUpdateMetadata = false, want true")
	}
	if permissions.Hidden {
		t.Fatal("permissions.Hidden = true, want false")
	}
	if !permissions.Agent {
		t.Fatal("permissions.Agent = false, want true")
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

	resp := availabilityResponseForReject(req, JobRejectArguments{Terminate: true})
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
	if !availability.Terminate {
		t.Fatal("availability.Terminate = false, want true")
	}
}

func TestAvailabilityResponseRejectCanAvoidTermination(t *testing.T) {
	req := &livekit.AvailabilityRequest{
		Job: &livekit.Job{Id: "job_requeue"},
	}

	resp := availabilityResponseForReject(req, JobRejectArguments{Terminate: false})
	availability := resp.GetAvailability()
	if availability.Terminate {
		t.Fatal("availability.Terminate = true, want false")
	}
}

func TestHandleAvailabilityRejectsWhenDraining(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	sentCh := make(chan *livekit.WorkerMessage, 1)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}

	server.draining = true
	server.handleAvailability(context.Background(), &livekit.AvailabilityRequest{
		Job: &livekit.Job{Id: "job_drain_reject"},
	})

	msg := receiveWorkerMessage(t, sentCh)
	availability := msg.GetAvailability()
	if availability == nil {
		t.Fatal("availability response is nil")
	}
	if availability.Available {
		t.Fatal("availability.Available = true, want false")
	}
	if availability.JobId != "job_drain_reject" {
		t.Fatalf("availability.JobId = %q, want job_drain_reject", availability.JobId)
	}
	if availability.Terminate {
		t.Fatal("availability.Terminate = true, want false")
	}
}

func TestHandleAvailabilityRejectsWhenRequestCallbackDoesNotAnswer(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	sentCh := make(chan *livekit.WorkerMessage, 1)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}
	server.requestFnc = func(req *JobRequest) error {
		return nil
	}

	server.handleAvailability(context.Background(), &livekit.AvailabilityRequest{
		Job: &livekit.Job{Id: "job_no_answer"},
	})

	msg := receiveWorkerMessage(t, sentCh)
	availability := msg.GetAvailability()
	if availability == nil {
		t.Fatal("availability response is nil")
	}
	if availability.Available {
		t.Fatal("availability.Available = true, want false")
	}
	if availability.JobId != "job_no_answer" {
		t.Fatalf("availability.JobId = %q, want job_no_answer", availability.JobId)
	}
	if availability.Terminate {
		t.Fatal("availability.Terminate = true, want false")
	}
}

func TestHandleRegisterReportsActiveJobs(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	sentCh := make(chan *livekit.WorkerMessage, 1)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}
	jobCtx := NewJobContext(&livekit.Job{Id: "job_active"}, "", "", "")
	server.mu.Lock()
	server.activeJobs[jobCtx.Job.Id] = jobCtx
	server.mu.Unlock()

	server.handleMessage(context.Background(), &livekit.ServerMessage{
		Message: &livekit.ServerMessage_Register{
			Register: &livekit.RegisterWorkerResponse{WorkerId: "worker-a"},
		},
	})

	msg := receiveWorkerMessage(t, sentCh)
	migrate := msg.GetMigrateJob()
	if migrate == nil {
		t.Fatal("migrate job message is nil")
	}
	if len(migrate.JobIds) != 1 || migrate.JobIds[0] != "job_active" {
		t.Fatalf("MigrateJob.JobIds = %v, want [job_active]", migrate.JobIds)
	}
}

func TestAcceptedAvailabilityExpiresWithoutAssignment(t *testing.T) {
	oldTimeout := assignmentTimeout
	assignmentTimeout = 10 * time.Millisecond
	t.Cleanup(func() {
		assignmentTimeout = oldTimeout
	})

	server := NewAgentServer(WorkerOptions{})
	sentCh := make(chan *livekit.WorkerMessage, 1)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}

	job := &livekit.Job{Id: "job_assignment_timeout", Room: &livekit.Room{Name: "room-a"}}
	server.handleAvailability(context.Background(), &livekit.AvailabilityRequest{Job: job})
	availability := receiveWorkerMessage(t, sentCh).GetAvailability()
	if availability == nil || !availability.Available {
		t.Fatal("availability response was not accepted")
	}

	deadline := time.After(time.Second)
	for {
		server.mu.Lock()
		_, pending := server.pendingAccepts[job.Id]
		server.mu.Unlock()
		if !pending {
			return
		}

		select {
		case <-deadline:
			t.Fatal("accepted arguments remained pending after assignment timeout")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func TestAssignmentPreservesAcceptedParticipantIdentity(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		return nil
	}
	server.requestFnc = func(req *JobRequest) error {
		return req.Accept(JobAcceptArguments{Identity: "custom-agent"})
	}
	startedCh := make(chan *JobContext, 1)
	server.entrypointFnc = func(ctx *JobContext) error {
		startedCh <- ctx
		return nil
	}

	job := &livekit.Job{Id: "job_custom_identity", Room: &livekit.Room{Name: "room-a"}}
	server.handleAvailability(context.Background(), &livekit.AvailabilityRequest{Job: job})
	server.handleAssignment(context.Background(), &livekit.JobAssignment{Job: job})

	select {
	case jobCtx := <-startedCh:
		if jobCtx.AcceptArguments.Identity != "custom-agent" {
			t.Fatalf("AcceptArguments.Identity = %q, want custom-agent", jobCtx.AcceptArguments.Identity)
		}
		if got := jobCtx.ParticipantIdentity(); got != "custom-agent" {
			t.Fatalf("ParticipantIdentity() = %q, want custom-agent", got)
		}
	case <-time.After(time.Second):
		t.Fatal("assignment entrypoint did not run")
	}

	server.mu.Lock()
	_, pending := server.pendingAccepts[job.Id]
	server.mu.Unlock()
	if pending {
		t.Fatal("accepted arguments remained pending after assignment")
	}
}

func TestAssignmentUsesAssignmentURLWhenProvided(t *testing.T) {
	server := NewAgentServer(WorkerOptions{WSRL: "wss://worker.example"})
	startedCh := make(chan *JobContext, 1)
	server.entrypointFnc = func(ctx *JobContext) error {
		startedCh <- ctx
		return nil
	}

	assignmentURL := "wss://assignment.example"
	job := &livekit.Job{Id: "job_assignment_url", Room: &livekit.Room{Name: "room-a"}}
	server.handleAssignment(context.Background(), &livekit.JobAssignment{
		Job: job,
		Url: &assignmentURL,
	})

	select {
	case jobCtx := <-startedCh:
		if jobCtx.url != assignmentURL {
			t.Fatalf("jobCtx.url = %q, want assignment URL", jobCtx.url)
		}
	case <-time.After(time.Second):
		t.Fatal("assignment entrypoint did not run")
	}
}

func TestAssignmentRecordsRegisteredWorkerID(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	startedCh := make(chan *JobContext, 1)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		return nil
	}
	server.entrypointFnc = func(ctx *JobContext) error {
		startedCh <- ctx
		return nil
	}

	server.handleMessage(context.Background(), &livekit.ServerMessage{
		Message: &livekit.ServerMessage_Register{
			Register: &livekit.RegisterWorkerResponse{WorkerId: "worker-a"},
		},
	})

	job := &livekit.Job{Id: "job_worker_id", Room: &livekit.Room{Name: "room-a"}}
	server.handleAssignment(context.Background(), &livekit.JobAssignment{Job: job})

	select {
	case jobCtx := <-startedCh:
		if jobCtx.WorkerID != "worker-a" {
			t.Fatalf("jobCtx.WorkerID = %q, want worker-a", jobCtx.WorkerID)
		}
	case <-time.After(time.Second):
		t.Fatal("assignment entrypoint did not run")
	}
}

func TestAssignmentSendsRunningJobStatus(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	sentCh := make(chan *livekit.WorkerMessage, 1)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}

	job := &livekit.Job{Id: "job_running_status", Room: &livekit.Room{Name: "room-a"}}
	server.handleAssignment(context.Background(), &livekit.JobAssignment{Job: job})

	msg := receiveWorkerMessage(t, sentCh)
	update := msg.GetUpdateJob()
	if update == nil {
		t.Fatal("update job message is nil")
	}
	if update.JobId != "job_running_status" {
		t.Fatalf("UpdateJob.JobId = %q, want job_running_status", update.JobId)
	}
	if update.Status != livekit.JobStatus_JS_RUNNING {
		t.Fatalf("UpdateJob.Status = %v, want JS_RUNNING", update.Status)
	}
}

func TestAssignmentReportsSuccessWhenEntrypointCompletes(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	sentCh := make(chan *livekit.WorkerMessage, 2)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}
	server.entrypointFnc = func(ctx *JobContext) error {
		return nil
	}

	job := &livekit.Job{Id: "job_success_status", Room: &livekit.Room{Name: "room-a"}}
	server.handleAssignment(context.Background(), &livekit.JobAssignment{Job: job})

	assertJobStatusMessage(t, receiveWorkerMessage(t, sentCh), "job_success_status", livekit.JobStatus_JS_RUNNING)
	assertJobStatusMessage(t, receiveWorkerMessage(t, sentCh), "job_success_status", livekit.JobStatus_JS_SUCCESS)

	server.mu.Lock()
	_, exists := server.activeJobs[job.Id]
	server.mu.Unlock()
	if exists {
		t.Fatal("assigned job remained in activeJobs after successful entrypoint completion")
	}
}

func TestAssignmentReportsFailureWhenEntrypointFails(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	sentCh := make(chan *livekit.WorkerMessage, 2)
	server.workerMessageSink = func(msg *livekit.WorkerMessage) error {
		sentCh <- msg
		return nil
	}
	server.entrypointFnc = func(ctx *JobContext) error {
		return errors.New("entrypoint failed")
	}

	job := &livekit.Job{Id: "job_failed_status", Room: &livekit.Room{Name: "room-a"}}
	server.handleAssignment(context.Background(), &livekit.JobAssignment{Job: job})

	assertJobStatusMessage(t, receiveWorkerMessage(t, sentCh), "job_failed_status", livekit.JobStatus_JS_RUNNING)
	assertJobStatusMessage(t, receiveWorkerMessage(t, sentCh), "job_failed_status", livekit.JobStatus_JS_FAILED)

	server.mu.Lock()
	_, exists := server.activeJobs[job.Id]
	server.mu.Unlock()
	if exists {
		t.Fatal("assigned job remained in activeJobs after failed entrypoint completion")
	}
}

func TestAssignmentPreservesAssignmentToken(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	startedCh := make(chan *JobContext, 1)
	server.entrypointFnc = func(ctx *JobContext) error {
		startedCh <- ctx
		return nil
	}

	job := &livekit.Job{Id: "job_assignment_token", Room: &livekit.Room{Name: "room-a"}}
	server.handleAssignment(context.Background(), &livekit.JobAssignment{
		Job:   job,
		Token: "assignment-token",
	})

	select {
	case jobCtx := <-startedCh:
		if jobCtx.token != "assignment-token" {
			t.Fatalf("jobCtx.token = %q, want assignment-token", jobCtx.token)
		}
	case <-time.After(time.Second):
		t.Fatal("assignment entrypoint did not run")
	}
}

func TestJobRequestRejectDefaultsToTerminate(t *testing.T) {
	var got JobRejectArguments
	req := &JobRequest{
		rejectFnc: func(args JobRejectArguments) error {
			got = args
			return nil
		},
	}

	if err := req.Reject(); err != nil {
		t.Fatalf("Reject() error = %v", err)
	}
	if !got.Terminate {
		t.Fatal("Reject() Terminate = false, want true")
	}
}

func TestJobRequestAcceptDefaultsIdentityBeforeCallback(t *testing.T) {
	var got JobAcceptArguments
	req := &JobRequest{
		Job: &livekit.Job{Id: "job_identity"},
		acceptFnc: func(args JobAcceptArguments) error {
			got = args
			return nil
		},
	}

	if err := req.Accept(JobAcceptArguments{}); err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if got.Identity != "agent-job_identity" {
		t.Fatalf("Accept() Identity = %q, want default identity", got.Identity)
	}
}

func TestValidateRunPreconditionsRequiresRTCSession(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		WSRL:      "wss://livekit.example",
		APIKey:    "key",
		APISecret: "secret",
	})

	err := server.validateRunPreconditions()
	if err == nil {
		t.Fatal("validateRunPreconditions() error = nil, want missing RTC session error")
	}
	if !strings.Contains(err.Error(), "No RTC session entrypoint") {
		t.Fatalf("validateRunPreconditions() error = %q, want RTC session message", err.Error())
	}
}

func TestValidateRunPreconditionsRequiresCredentialsAfterRTCSession(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	server.RTCSession(func(ctx *JobContext) error { return nil }, nil, nil)

	err := server.validateRunPreconditions()
	if err == nil {
		t.Fatal("validateRunPreconditions() error = nil, want missing credentials error")
	}
	if !strings.Contains(err.Error(), "missing LiveKit credentials") {
		t.Fatalf("validateRunPreconditions() error = %q, want credentials message", err.Error())
	}
}

func TestRTCSessionRejectsSecondRegistration(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})

	if err := server.RTCSession(func(ctx *JobContext) error { return nil }, nil, nil); err != nil {
		t.Fatalf("first RTCSession() error = %v", err)
	}

	err := server.RTCSession(func(ctx *JobContext) error { return nil }, nil, nil)
	if err == nil {
		t.Fatal("second RTCSession() error = nil, want duplicate registration error")
	}
	if !strings.Contains(err.Error(), "only supports registering one rtc_session") {
		t.Fatalf("second RTCSession() error = %q, want duplicate registration message", err.Error())
	}
}

func TestNewJobContextDefaultsParticipantIdentity(t *testing.T) {
	job := &livekit.Job{Id: "job_default"}
	ctx := NewJobContext(job, "wss://livekit.example", "key", "secret")

	if got := ctx.ParticipantIdentity(); got != "agent-job_default" {
		t.Fatalf("ParticipantIdentity() = %q, want default job identity", got)
	}
}

func TestLocalJobContextUsesProvidedParticipantIdentity(t *testing.T) {
	ctx := newLocalJobContext("room-a", "agent-custom", WorkerOptions{})

	if got := ctx.ParticipantIdentity(); got != "agent-custom" {
		t.Fatalf("ParticipantIdentity() = %q, want provided local identity", got)
	}
	if ctx.Job.Room.Name != "room-a" {
		t.Fatalf("Job.Room.Name = %q, want room-a", ctx.Job.Room.Name)
	}
}

func TestExecuteLocalJobCleansUpAndRunsSessionEnd(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	startedCh := make(chan *JobContext, 1)
	sessionEndCh := make(chan *JobContext, 1)

	if err := server.RTCSession(
		func(ctx *JobContext) error {
			startedCh <- ctx
			return nil
		},
		nil,
		func(ctx *JobContext) error {
			sessionEndCh <- ctx
			return nil
		},
	); err != nil {
		t.Fatalf("RTCSession() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- server.ExecuteLocalJob(ctx, "room-a", "agent-local")
	}()

	var jobCtx *JobContext
	select {
	case jobCtx = <-startedCh:
	case <-time.After(time.Second):
		t.Fatal("local job entrypoint did not run")
	}

	cancel()

	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("ExecuteLocalJob() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ExecuteLocalJob() did not return after context cancellation")
	}

	select {
	case endedCtx := <-sessionEndCh:
		if endedCtx != jobCtx {
			t.Fatal("session end callback received a different job context")
		}
	case <-time.After(time.Second):
		t.Fatal("session end callback did not run")
	}

	server.mu.Lock()
	_, exists := server.activeJobs[jobCtx.Job.Id]
	server.mu.Unlock()
	if exists {
		t.Fatal("local job remained in activeJobs after completion")
	}
}

func TestDrainWaitsForActiveJobs(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	jobCtx := NewJobContext(&livekit.Job{Id: "job_drain"}, "", "", "")

	server.mu.Lock()
	server.activeJobs[jobCtx.Job.Id] = jobCtx
	server.mu.Unlock()

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- server.Drain(context.Background())
	}()

	drainingDeadline := time.After(time.Second)
	for !server.Draining() {
		select {
		case <-drainingDeadline:
			t.Fatal("server.Draining() = false, want true")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	select {
	case err := <-doneCh:
		t.Fatalf("Drain() returned before active job finished: %v", err)
	default:
	}

	server.finishJob(jobCtx)

	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("Drain() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Drain() did not return after active job finished")
	}
}

func TestHandleTerminationRunsJobShutdownCallbacks(t *testing.T) {
	server := NewAgentServer(WorkerOptions{})
	jobCtx := NewJobContext(&livekit.Job{Id: "job_shutdown"}, "", "", "")
	shutdownCh := make(chan string, 1)
	if err := jobCtx.AddShutdownCallback(func(reason string) {
		shutdownCh <- reason
	}); err != nil {
		t.Fatalf("AddShutdownCallback() error = %v", err)
	}

	server.mu.Lock()
	server.activeJobs[jobCtx.Job.Id] = jobCtx
	server.mu.Unlock()

	server.handleTermination(&livekit.JobTermination{JobId: jobCtx.Job.Id})

	select {
	case reason := <-shutdownCh:
		if reason != "" {
			t.Fatalf("shutdown reason = %q, want empty reason", reason)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown callback did not run")
	}
}

func receiveWorkerMessage(t *testing.T, receivedCh <-chan *livekit.WorkerMessage) *livekit.WorkerMessage {
	t.Helper()

	select {
	case msg := <-receivedCh:
		return msg
	case <-time.After(time.Second):
		t.Fatal("worker message was not sent")
		return nil
	}
}

func assertJobStatusMessage(t *testing.T, msg *livekit.WorkerMessage, jobID string, status livekit.JobStatus) {
	t.Helper()

	update := msg.GetUpdateJob()
	if update == nil {
		t.Fatal("update job message is nil")
	}
	if update.JobId != jobID {
		t.Fatalf("UpdateJob.JobId = %q, want %s", update.JobId, jobID)
	}
	if update.Status != status {
		t.Fatalf("UpdateJob.Status = %v, want %v", update.Status, status)
	}
}
