package worker

import (
	"time"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
)

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

type JobAcceptArguments = workerlivekit.JobAcceptArguments

type JobRejectArguments = workerlivekit.JobRejectArguments

type JobRoomServiceAPI = workerlivekit.JobRoomServiceAPI

type JobSIPAPI = workerlivekit.JobSIPAPI

type JobAPI = workerlivekit.JobAPI

type AutoSubscribe = workerlivekit.AutoSubscribe

const (
	AutoSubscribeSubscribeAll  = workerlivekit.AutoSubscribeSubscribeAll
	AutoSubscribeSubscribeNone = workerlivekit.AutoSubscribeSubscribeNone
	AutoSubscribeAudioOnly     = workerlivekit.AutoSubscribeAudioOnly
	AutoSubscribeVideoOnly     = workerlivekit.AutoSubscribeVideoOnly
)

type ConnectOptions = workerlivekit.ConnectOptions

type Job = workerlivekit.Job

type SDKRoom = workerlivekit.SDKRoom

type Room = workerlivekit.Room

type LocalParticipant = workerlivekit.LocalParticipant

type RoomCallback = workerlivekit.RoomCallback

type RemoteParticipantView = workerlivekit.RemoteParticipantView

type RemoteParticipant = workerlivekit.RemoteParticipant

type TrackType = workerlivekit.TrackType

type RemoteTrackPublication = workerlivekit.RemoteTrackPublication

type ClaimGrants = workerlivekit.ClaimGrants

type DeleteRoomResponse = workerlivekit.DeleteRoomResponse

type SIPParticipantInfo = workerlivekit.SIPParticipantInfo

type SIPCreateParticipantRequest = workerlivekit.SIPCreateParticipantRequest

type ParticipantInfo = workerlivekit.ParticipantInfo

type ParticipantInfoKind = workerlivekit.ParticipantInfoKind

type ParticipantTaskKey = workerlivekit.ParticipantTaskKey

type TrackPublicationWaitOptions = workerlivekit.TrackPublicationWaitOptions

type JobRequest = workerlivekit.JobRequest

var livekitServerRegisterWorkerMessage = workerlivekit.ServerRegisterWorkerMessage

var livekitServerAvailableWorkerStatusMessage = workerlivekit.ServerAvailableWorkerStatusMessage

var livekitServerDrainingWorkerStatusMessage = workerlivekit.ServerDrainingWorkerStatusMessage

var livekitServerJobStatusMessage = workerlivekit.ServerJobStatusMessage

var livekitServerJobRunningMessage = workerlivekit.ServerJobRunningMessage

var livekitServerMigrateRunningJobsMessage = workerlivekit.ServerMigrateRunningJobsMessage

var livekitExchangeInitialServerRegisterWebSocket = workerlivekit.ExchangeInitialServerRegisterWebSocket

var livekitRunServerMessageLoop = workerlivekit.RunServerMessageLoop

var livekitOpenServerWorkerWebSocket = workerlivekit.OpenServerWorkerWebSocket

var livekitRouteServerWorkerMessage = workerlivekit.RouteServerWorkerMessage

var livekitWriteServerWorkerMessageWebSocket = workerlivekit.WriteServerWorkerMessageWebSocket

var livekitAvailabilityInfo = workerlivekit.AvailabilityInfo

var livekitAnswerServerAvailabilityRequest = workerlivekit.AnswerServerAvailabilityRequest

var livekitStoreServerPendingAccept = workerlivekit.StoreServerPendingAccept

var livekitExpireServerPendingAccept = workerlivekit.ExpireServerPendingAccept

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
