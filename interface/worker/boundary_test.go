package worker

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerProductionCodeUsesLiveKitSubpackageForLiveKitImports(t *testing.T) {
	root := "."
	forbidden := []string{
		`"github.com/livekit/`,
		`lkprotocol "github.com/livekit/`,
		`lksdk "github.com/livekit/`,
		`auth "github.com/livekit/`,
	}

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path == "livekit" {
				return filepath.SkipDir
			}
			return nil
		}
		if path == "livekit_worker_contracts.go" {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, forbiddenImport := range forbidden {
			if strings.Contains(string(data), forbiddenImport) {
				t.Fatalf("%s imports %s; route LiveKit SDK/protocol usage through interface/worker/livekit", path, forbiddenImport)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk worker files: %v", err)
	}
}

func TestSharedWorkerDoesNotBuildLiveKitStatusMessagesDirectly(t *testing.T) {
	forbiddenCalls := []string{
		"workerlivekit.AnswerAvailabilityRequest(",
		"workerlivekit.ApplyWorkerEnv(",
		"workerlivekit.ExchangeInitialRegisterWebSocket(",
		"workerlivekit.MigratableRunningJobIDs(",
		"workerlivekit.MigrateRunningJobsMessage(",
		"workerlivekit.OpenWorkerWebSocket(",
		"workerlivekit.JobRunningMessage(",
		"workerlivekit.JobStatusMessage(",
		"workerlivekit.ServerRegisterWorkerMessage(",
		"workerlivekit.ServerAvailableWorkerStatusMessage(",
		"workerlivekit.ServerDrainingWorkerStatusMessage(",
		"workerlivekit.ServerJobStatusMessage(",
		"workerlivekit.ServerJobRunningMessage(",
		"workerlivekit.ServerMigrateRunningJobsMessage(",
		"workerlivekit.ExchangeInitialServerRegisterWebSocket(",
		"workerlivekit.RunServerMessageLoop(",
		"workerlivekit.OpenServerWorkerWebSocket(",
		"workerlivekit.RouteServerWorkerMessage(",
		"workerlivekit.WriteServerWorkerMessageWebSocket(",
		"workerlivekit.AvailabilityInfo(",
		"workerlivekit.AnswerServerAvailabilityRequest(",
		"workerlivekit.StoreServerPendingAccept(",
		"workerlivekit.ExpireServerPendingAccept(",
		"workerlivekit.JobAssignmentInfo(",
		"workerlivekit.AcceptServerPendingAssignment(",
		"workerlivekit.JobTerminationInfo(",
		"workerlivekit.ServerJobTerminationPlanForActiveJob(",
		"workerlivekit.DefaultServerFakeLocalJobOptions(",
		"workerlivekit.PrepareServerLocalJobRunOptions(",
		"workerlivekit.ServerLocalJobExecutorPlan(",
		"workerlivekit.ServerLocalJobSessionReportPath(",
		"workerlivekit.ServerJobFinishPlan(",
		"workerlivekit.ServerJobSessionReportUploadPlan(",
		"workerlivekit.ServerJobSessionEndPlan(",
		"workerlivekit.ServerLocalJobContextSetupPlan(",
		"workerlivekit.ServerLogLevelFromEnv(",
		"workerlivekit.DefaultServerWorkerPermissions(",
		"workerlivekit.ResolveServerConnectionOptions(",
		"workerlivekit.WorkerRuntimeMetadata(",
		"workerlivekit.ResolveServerAgentNameFromEnv(",
		"workerlivekit.ValidateServerConnectionOptions(",
		"workerlivekit.ApplyServerConnectionEnv(",
		"workerlivekit.ServerRunningJobInfoSnapshot(",
		"workerlivekit.RefreshServerRunningJobsForReload(",
		"workerlivekit.ServerReloadedJobContextValues(",
		"workerlivekit.ServerRecordingOptions(",
		"workerlivekit.ServerRunningJobContextValues(",
		"workerlivekit.RunServerRunningJobEntrypointLifecycle(",
		"workerlivekit.RunServerReloadedJobEntrypointLifecycle(",
		"workerlivekit.ServerMigratableRunningJobIDs(",
		"workerlivekit.ServerAssignmentContextValues(",
		"workerlivekit.RunServerJobEntrypointLifecycle(",
		"workerlivekit.WriteWorkerMessageWebSocket(",
		"workerlivekit.WorkerStatusUpdateMessage(",
		"workerlivekit.RegisterWorkerMessage(",
		"workerlivekit.RouteServerMessage(",
		"workerlivekit.RunWorkerMessageLoop(",
		"workerlivekit.StorePendingAccept(",
		"workerlivekit.ExpirePendingAccept(",
		"workerlivekit.AcceptPendingAssignment(",
		"workerlivekit.RunningJobInfoSnapshot(",
		"workerlivekit.RefreshRunningJobsForReload(",
		"workerlivekit.RunningJobContextValues(",
		"workerlivekit.ReloadedJobContextValues(",
		"workerlivekit.RunRunningJobEntrypointLifecycle(",
		"workerlivekit.RunReloadedJobEntrypointLifecycle(",
		"workerlivekit.RunJobEntrypointLifecycle(",
		"workerlivekit.AssignmentContextValues(",
		"workerlivekit.JobTerminationPlanForActiveJob(",
		"workerlivekit.WorkerLogLevelFromEnv(",
		"workerlivekit.DefaultWorkerPermissions(",
		"workerlivekit.ResolveWorkerConnectionOptions(",
		"workerlivekit.ResolveAgentNameFromEnv(",
		"workerlivekit.AllRecordingOptions(",
		"workerlivekit.DefaultFakeLocalJobOptions(",
		"workerlivekit.PrepareLocalJobRunOptions(",
		"workerlivekit.LocalJobExecutorPlan(",
		"workerlivekit.LocalJobSessionReportPath(",
		"workerlivekit.JobFinishPlan(",
		"workerlivekit.JobSessionReportUploadPlan(",
		"workerlivekit.JobSessionEndPlan(",
		"workerlivekit.LocalJobContextSetupPlan(",
		"workerlivekit.ValidateWorkerConnectionOptions(",
	}

	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path == "livekit" {
				return filepath.SkipDir
			}
			return nil
		}
		if path == "livekit_worker_contracts.go" {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, forbiddenCall := range forbiddenCalls {
			if strings.Contains(string(data), forbiddenCall) {
				t.Fatalf("%s calls %s; use the LiveKit server message facade", path, forbiddenCall)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk worker files: %v", err)
	}
}

func TestSharedWorkerDoesNotExposeLiveKitWorkerMessageDirectly(t *testing.T) {
	data, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	forbidden := []string{
		"*workerlivekit.WorkerMessage",
		"*workerlivekit.ServerMessage",
		"*workerlivekit.AvailabilityRequest",
		"*workerlivekit.JobAssignment",
		"*workerlivekit.JobTermination",
		"*workerlivekit.ServerInfo",
		"opts workerlivekit.WorkerWebSocketOpenOptions",
		"(workerlivekit.WorkerWebSocketOpenResult",
		"result workerlivekit.EntrypointResult",
		"status workerlivekit.JobStatus",
		") workerlivekit.JobSessionReportUploadPlanResult",
		"workerlivekit.JobAcceptArguments",
		"event workerlivekit.WorkerRegisteredEvent",
		"workerlivekit.ServerConnectionResolveOptions{",
		"workerlivekit.ServerConnectionOptions{",
		"workerlivekit.ServerConnectionEnvOptions{",
		"workerlivekit.AgentNameEnvOptions{",
		"workerlivekit.WorkerRuntimeMetadataOptions{",
		"workerlivekit.ServerRegisterWorkerMessageOptions{",
		"workerlivekit.ServerAvailableWorkerStatusMessageOptions{",
		"workerlivekit.ServerMessageLoopOptions{",
		"workerlivekit.ServerMessageRouteOptions{",
		"workerlivekit.AvailabilityAnswerOptions{",
		"workerlivekit.PendingAcceptStoreOptions{",
		"workerlivekit.RunningJobInfoOptions{",
		"workerlivekit.ReloadedJobContextValueOptions{",
		"workerlivekit.RunningJobContextValueOptions{",
		"workerlivekit.RunningJobEntrypointLifecycleOptions{",
		"workerlivekit.ReloadedJobEntrypointLifecycleOptions{",
		"workerlivekit.AssignmentContextValueOptions{",
		"workerlivekit.JobEntrypointLifecycleOptions{",
		"workerlivekit.JobSessionReportUploadPlanOptions{",
		"workerlivekit.JobSessionEndPlanOptions{",
		"workerlivekit.LocalJobContextSetupPlanOptions{",
	}
	for _, direct := range forbidden {
		if strings.Contains(string(data), direct) {
			t.Fatalf("server.go exposes %s directly; use worker message contracts", direct)
		}
	}
}

func TestServerKeepsLiveKitCompatibilityAliasesOutOfImplementation(t *testing.T) {
	data, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "type ") && strings.Contains(line, "= workerlivekit.") {
			t.Fatalf("server.go owns LiveKit compatibility alias %q; move it to shared worker contracts", line)
		}
		if strings.HasPrefix(line, "WorkerType") && strings.Contains(line, "= workerlivekit.") {
			t.Fatalf("server.go owns LiveKit compatibility const %q; move it to shared worker contracts", line)
		}
	}
}

func TestSharedJobContextDoesNotCallLiveKitInfoHelpersDirectly(t *testing.T) {
	forbiddenCalls := []string{
		"workerlivekit.JobInferenceHeaders(",
		"workerlivekit.JobContextInferenceHeaders(",
		"workerlivekit.NewJobAPI(",
		"workerlivekit.NewJobContextAPI(",
		"workerlivekit.JobParticipantIdentity(",
		"workerlivekit.JobContextParticipantIdentity(",
		"workerlivekit.LocalParticipantIdentity(",
		"workerlivekit.JobContextLocalParticipantIdentity(",
		"workerlivekit.TokenClaims(",
		"workerlivekit.JobContextTokenClaims(",
		"workerlivekit.JobID(",
		"workerlivekit.JobContextJobID(",
		"workerlivekit.JobAvatarStartInfo(",
		"workerlivekit.JobContextAvatarStartInfo(",
		"workerlivekit.JobRoom(",
		"workerlivekit.JobContextRoom(",
		"workerlivekit.JobPublisher(",
		"workerlivekit.JobContextPublisher(",
		"workerlivekit.RoomLocalParticipant(",
		"workerlivekit.JobContextLocalParticipant(",
		"workerlivekit.NewJobSessionReport(",
		"workerlivekit.NewJobContextSessionReport(",
		"workerlivekit.JobLogContextFields(",
		"workerlivekit.JobContextLogFields(",
		"workerlivekit.PopulateSessionReportWithJobMetadata(",
		"workerlivekit.PopulateJobContextSessionReport(",
		"workerlivekit.NormalizeConnectOptions(",
		"workerlivekit.JobContextNormalizeConnectOptions(",
		"workerlivekit.JoinPreparedRoom(",
		"workerlivekit.JobContextJoinPreparedRoom(",
		"workerlivekit.AcceptedJobRoomConnectOptions{",
		"workerlivekit.PreparedRoomConnectOptionsFromAcceptedJob(",
		"workerlivekit.RoomRemoteParticipantViews(",
		"workerlivekit.JobContextRemoteParticipantViews(",
		"workerlivekit.JobRoomName(",
		"workerlivekit.JobContextRoomName(",
		"workerlivekit.ApplyAutoSubscribeToRoom(",
		"workerlivekit.JobContextApplyAutoSubscribeToRoom(",
		"workerlivekit.RoomCallbackWithHandlers(",
		"workerlivekit.JobContextRoomCallbackWithHandlers(",
		"workerlivekit.RoomCallbackHandlers{",
		"workerlivekit.RemoteTrackSubscriptionResult",
		"workerlivekit.NewRoom",
		"workerlivekit.NewJobContextRoom",
		"workerlivekit.RoomConnector",
		"workerlivekit.ParticipantInfoFromRemoteParticipant(",
		"workerlivekit.JobContextParticipantInfoFromRemoteParticipant(",
		"workerlivekit.UpsertParticipantInfo(",
		"workerlivekit.JobContextUpsertParticipantInfo(",
		"workerlivekit.WaitForParticipant(",
		"workerlivekit.JobContextWaitForParticipant(",
		"workerlivekit.WaitForAgent(",
		"workerlivekit.JobContextWaitForAgent(",
		"workerlivekit.WaitForTrackPublication(",
		"workerlivekit.JobContextWaitForTrackPublication(",
		"workerlivekit.WaitForTrackPublicationWithOptions(",
		"workerlivekit.JobContextWaitForTrackPublicationWithOptions(",
		"workerlivekit.WaitForParticipantAttribute(",
		"workerlivekit.JobContextWaitForParticipantAttribute(",
		"workerlivekit.ParticipantEntrypointRegistrationPlan(",
		"workerlivekit.JobContextParticipantEntrypointRegistrationPlan(",
		"workerlivekit.ParticipantEntrypointRegistrationOptions{",
		"workerlivekit.ParticipantEntrypointTaskPlan(",
		"workerlivekit.JobContextParticipantEntrypointTaskPlan(",
		"workerlivekit.DeleteRoomPlan(",
		"workerlivekit.DeleteRoomBestEffort(",
		"workerlivekit.MoveParticipantPlan(",
		"workerlivekit.MoveParticipant(",
		"workerlivekit.SIPCreateParticipantPlan(",
		"workerlivekit.CreateSIPParticipantWithNames(",
		"workerlivekit.CreateSIPParticipantWithRequest(",
		"workerlivekit.SIPTransferParticipantPlan(",
		"workerlivekit.TransferSIPParticipantByParticipant(",
	}

	data, err := os.ReadFile("job.go")
	if err != nil {
		t.Fatalf("read job.go: %v", err)
	}
	for _, forbiddenCall := range forbiddenCalls {
		if strings.Contains(string(data), forbiddenCall) {
			t.Fatalf("job.go calls %s; use the LiveKit job context facade", forbiddenCall)
		}
	}
}

func TestJobKeepsLiveKitCompatibilityAliasesOutOfImplementation(t *testing.T) {
	data, err := os.ReadFile("job.go")
	if err != nil {
		t.Fatalf("read job.go: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "type ") && strings.Contains(line, "= workerlivekit.") {
			t.Fatalf("job.go owns LiveKit compatibility alias %q; move it to shared worker contracts", line)
		}
		if strings.HasPrefix(line, "AutoSubscribe") && strings.Contains(line, "= workerlivekit.") {
			t.Fatalf("job.go owns LiveKit compatibility const %q; move it to shared worker contracts", line)
		}
	}
}

func TestSharedJobContextDoesNotExposeParticipantInternalsDirectly(t *testing.T) {
	data, err := os.ReadFile("job.go")
	if err != nil {
		t.Fatalf("read job.go: %v", err)
	}
	forbidden := []string{
		"*workerlivekit.ParticipantInfo)",
		"[]workerlivekit.ParticipantInfoKind",
		"[]*workerlivekit.ParticipantInfo",
		"map[workerlivekit.ParticipantTaskKey]",
	}
	for _, direct := range forbidden {
		if strings.Contains(string(data), direct) {
			t.Fatalf("job.go exposes %s directly; use worker participant contracts", direct)
		}
	}
}

func TestSharedJobContextDoesNotExposeRoomInternalsDirectly(t *testing.T) {
	data, err := os.ReadFile("job.go")
	if err != nil {
		t.Fatalf("read job.go: %v", err)
	}
	forbidden := []string{
		"*workerlivekit.Job",
		"*workerlivekit.SDKRoom",
		"*workerlivekit.Room",
		"*workerlivekit.LocalParticipant",
		"*workerlivekit.RoomCallback",
		"participant workerlivekit.RemoteParticipantView",
		"[]workerlivekit.RemoteParticipantView",
		"*workerlivekit.RemoteParticipant",
		"kinds ...workerlivekit.TrackType",
		"*workerlivekit.RemoteTrackPublication",
	}
	for _, direct := range forbidden {
		if strings.Contains(string(data), direct) {
			t.Fatalf("job.go exposes %s directly; use worker room contracts", direct)
		}
	}
}

func TestSharedJobContextDoesNotExposeJobAPIResultInternalsDirectly(t *testing.T) {
	data, err := os.ReadFile("job.go")
	if err != nil {
		t.Fatalf("read job.go: %v", err)
	}
	forbidden := []string{
		"*workerlivekit.ClaimGrants",
		"*workerlivekit.DeleteRoomResponse",
		"*workerlivekit.SIPParticipantInfo",
		"*workerlivekit.SIPCreateParticipantRequest",
	}
	for _, direct := range forbidden {
		if strings.Contains(string(data), direct) {
			t.Fatalf("job.go exposes %s directly; use worker API result contracts", direct)
		}
	}
}
