package worker

import (
	"time"

	workeripc "github.com/cavos-io/rtp-agent/interface/worker/ipc"
	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
)

var livekitJobAssignmentInfo = workerlivekit.JobAssignmentInfo

func livekitAcceptServerPendingAssignment(
	pending map[string]JobAcceptArguments,
	timers map[string]*time.Timer,
	jobID string,
) (JobAcceptArguments, bool) {
	return workerlivekit.AcceptServerPendingAssignment(pending, timers, jobID)
}

var livekitJobTerminationInfo = workerlivekit.JobTerminationInfo

var livekitServerJobTerminationPlanForActiveJob = workerlivekit.ServerJobTerminationPlanForActiveJob

var livekitDefaultServerFakeLocalJobOptions = workerlivekit.DefaultServerFakeLocalJobOptions

var livekitPrepareServerLocalJobRunOptions = workerlivekit.PrepareServerLocalJobRunOptions

var livekitServerLocalJobExecutorPlan = workerlivekit.ServerLocalJobExecutorPlan

var livekitServerLocalJobSessionReportPath = workerlivekit.ServerLocalJobSessionReportPath

var livekitServerJobFinishPlan = workerlivekit.ServerJobFinishPlan

var livekitServerJobSessionReportUploadPlan = workerlivekit.ServerJobSessionReportUploadPlan

var livekitServerJobSessionEndPlan = workerlivekit.ServerJobSessionEndPlan

var livekitServerLocalJobContextSetupPlan = workerlivekit.ServerLocalJobContextSetupPlan

var livekitServerRunningJobInfoSnapshot = workerlivekit.ServerRunningJobInfoSnapshot

func livekitRunningJobInfoToIPC(info workerlivekit.RunningJobInfo) workeripc.RunningJobInfo {
	return workerlivekit.ToIPCRunningJobInfo(info)
}

func livekitRunningJobInfoFromIPC(info workeripc.RunningJobInfo) (workerlivekit.RunningJobInfo, error) {
	return workerlivekit.FromIPCRunningJobInfo(info)
}

func livekitRunningJobInfosFromIPC(infos []workeripc.RunningJobInfo) ([]workerlivekit.RunningJobInfo, error) {
	return workerlivekit.FromIPCRunningJobInfos(infos)
}

var livekitRefreshServerRunningJobsForReload = workerlivekit.RefreshServerRunningJobsForReload

var livekitServerReloadedJobContextValues = workerlivekit.ServerReloadedJobContextValues

var livekitServerRecordingOptions = workerlivekit.ServerRecordingOptions

var livekitServerRunningJobContextValues = workerlivekit.ServerRunningJobContextValues

var livekitRunServerRunningJobEntrypointLifecycle = workerlivekit.RunServerRunningJobEntrypointLifecycle

var livekitRunServerReloadedJobEntrypointLifecycle = workerlivekit.RunServerReloadedJobEntrypointLifecycle

var livekitServerMigratableRunningJobIDs = workerlivekit.ServerMigratableRunningJobIDs

var livekitServerAssignmentContextValues = workerlivekit.ServerAssignmentContextValues

var livekitRunServerJobEntrypointLifecycle = workerlivekit.RunServerJobEntrypointLifecycle
