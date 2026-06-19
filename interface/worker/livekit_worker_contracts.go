package worker

import workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"

type WorkerType = workerlivekit.WorkerType

const (
	WorkerTypeRoom      = workerlivekit.WorkerTypeRoom
	WorkerTypePublisher = workerlivekit.WorkerTypePublisher
)

type WorkerPermissions = workerlivekit.WorkerPermissions

type WorkerMessage = workerlivekit.WorkerMessage

type ServerMessage = workerlivekit.ServerMessage

type AvailabilityRequest = workerlivekit.AvailabilityRequest

type JobAssignment = workerlivekit.JobAssignment

type JobTermination = workerlivekit.JobTermination

type ServerInfo = workerlivekit.ServerInfo

type WorkerWebSocketOpenOptions = workerlivekit.WorkerWebSocketOpenOptions

type WorkerWebSocketOpenResult = workerlivekit.WorkerWebSocketOpenResult

type ServerConnectionResolveOptions = workerlivekit.ServerConnectionResolveOptions

type ServerConnectionOptions = workerlivekit.ServerConnectionOptions

type ServerConnectionEnvOptions = workerlivekit.ServerConnectionEnvOptions

type AgentNameEnvOptions = workerlivekit.AgentNameEnvOptions

type WorkerRuntimeMetadataOptions = workerlivekit.WorkerRuntimeMetadataOptions

type ServerRegisterWorkerMessageOptions = workerlivekit.ServerRegisterWorkerMessageOptions

type ServerAvailableWorkerStatusMessageOptions = workerlivekit.ServerAvailableWorkerStatusMessageOptions

type ServerMessageLoopOptions = workerlivekit.ServerMessageLoopOptions

type ServerMessageRouteOptions = workerlivekit.ServerMessageRouteOptions

type AvailabilityAnswerOptions = workerlivekit.AvailabilityAnswerOptions

type PendingAcceptStoreOptions = workerlivekit.PendingAcceptStoreOptions

type RunningJobInfoOptions = workerlivekit.RunningJobInfoOptions

type ReloadedJobContextValueOptions = workerlivekit.ReloadedJobContextValueOptions

type RunningJobContextValueOptions = workerlivekit.RunningJobContextValueOptions

type RunningJobEntrypointLifecycleOptions = workerlivekit.RunningJobEntrypointLifecycleOptions

type ReloadedJobEntrypointLifecycleOptions = workerlivekit.ReloadedJobEntrypointLifecycleOptions

type AssignmentContextValueOptions = workerlivekit.AssignmentContextValueOptions

type JobEntrypointLifecycleOptions = workerlivekit.JobEntrypointLifecycleOptions

type JobSessionReportUploadPlanOptions = workerlivekit.JobSessionReportUploadPlanOptions

type JobSessionEndPlanOptions = workerlivekit.JobSessionEndPlanOptions

type LocalJobContextSetupPlanOptions = workerlivekit.LocalJobContextSetupPlanOptions

type EntrypointResult = workerlivekit.EntrypointResult

type JobStatus = workerlivekit.JobStatus

type JobSessionReportUploadPlanResult = workerlivekit.JobSessionReportUploadPlanResult

type WorkerRegisteredHandler = workerlivekit.WorkerRegisteredHandler

type WorkerRegisteredEvent = workerlivekit.WorkerRegisteredEvent

type LocalJobOptions = workerlivekit.LocalJobOptions
