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

func TestSharedJobContextDoesNotCallLiveKitInfoHelpersDirectly(t *testing.T) {
	forbiddenCalls := []string{
		"workerlivekit.JobInferenceHeaders(",
		"workerlivekit.JobParticipantIdentity(",
		"workerlivekit.LocalParticipantIdentity(",
		"workerlivekit.TokenClaims(",
		"workerlivekit.JobID(",
		"workerlivekit.JobAvatarStartInfo(",
		"workerlivekit.JobRoom(",
		"workerlivekit.JobPublisher(",
		"workerlivekit.RoomLocalParticipant(",
		"workerlivekit.NewJobSessionReport(",
		"workerlivekit.JobLogContextFields(",
		"workerlivekit.PopulateSessionReportWithJobMetadata(",
		"workerlivekit.NormalizeConnectOptions(",
		"workerlivekit.JoinPreparedRoom(",
		"workerlivekit.PreparedRoomConnectOptionsFromAcceptedJob(",
		"workerlivekit.RoomRemoteParticipantViews(",
		"workerlivekit.JobRoomName(",
		"workerlivekit.ApplyAutoSubscribeToRoom(",
		"workerlivekit.RoomCallbackWithHandlers(",
		"workerlivekit.ParticipantInfoFromRemoteParticipant(",
		"workerlivekit.UpsertParticipantInfo(",
		"workerlivekit.WaitForParticipant(",
		"workerlivekit.WaitForAgent(",
		"workerlivekit.WaitForTrackPublication(",
		"workerlivekit.WaitForTrackPublicationWithOptions(",
		"workerlivekit.WaitForParticipantAttribute(",
		"workerlivekit.ParticipantEntrypointRegistrationPlan(",
		"workerlivekit.ParticipantEntrypointTaskPlan(",
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
