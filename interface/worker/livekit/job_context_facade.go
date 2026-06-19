package livekit

import (
	"context"

	"github.com/cavos-io/rtp-agent/core/agent"
)

func JobContextInferenceHeaders(job *Job) map[string]string {
	return JobInferenceHeaders(job)
}

func JobContextParticipantIdentity(job *Job, fallbackIdentity string) string {
	return JobParticipantIdentity(job, fallbackIdentity)
}

func JobContextLocalParticipantIdentity(token string, fallbackIdentity string) string {
	return LocalParticipantIdentity(token, fallbackIdentity)
}

func JobContextTokenClaims(token string) (*ClaimGrants, error) {
	return TokenClaims(token)
}

func JobContextJobID(job *Job) string {
	return JobID(job)
}

func JobContextAvatarStartInfo(job *Job, url string, token string, agentIdentity string) agent.AvatarStartInfo {
	return JobAvatarStartInfo(job, url, token, agentIdentity)
}

func JobContextRoom(job *Job) *Room {
	return JobRoom(job)
}

func JobContextPublisher(job *Job) *ParticipantInfo {
	return JobPublisher(job)
}

func JobContextLocalParticipant(room *SDKRoom) *LocalParticipant {
	return RoomLocalParticipant(room)
}

func NewJobContextSessionReport(job *Job) (*agent.SessionReport, *agent.Tagger) {
	return NewJobSessionReport(job)
}

func JobContextLogFields(job *Job) map[string]any {
	return JobLogContextFields(job)
}

func PopulateJobContextSessionReport(report *agent.SessionReport, job *Job) {
	PopulateSessionReportWithJobMetadata(report, job)
}

func JobContextNormalizeConnectOptions(options ...ConnectOptions) ConnectOptions {
	return NormalizeConnectOptions(options...)
}

func JobContextJoinPreparedRoom(ctx context.Context, opts AcceptedJobRoomConnectOptions) error {
	return JoinPreparedRoom(ctx, PreparedRoomConnectOptionsFromAcceptedJob(opts))
}

func JobContextRemoteParticipantViews(room *SDKRoom) []RemoteParticipantView {
	return RoomRemoteParticipantViews(room)
}

func JobContextRoomName(job *Job) string {
	return JobRoomName(job)
}

func JobContextApplyAutoSubscribeToRoom(room *SDKRoom, mode string) []RemoteTrackSubscriptionResult {
	return ApplyAutoSubscribeToRoom(room, mode)
}

func JobContextRoomCallbackWithHandlers(cb *RoomCallback, handlers RoomCallbackHandlers) *RoomCallback {
	return RoomCallbackWithHandlers(cb, handlers)
}

func JobContextParticipantInfoFromRemoteParticipant(participant RemoteParticipantView) *ParticipantInfo {
	return ParticipantInfoFromRemoteParticipant(participant)
}

func JobContextUpsertParticipantInfo(participants []*ParticipantInfo, participant *ParticipantInfo) []*ParticipantInfo {
	return UpsertParticipantInfo(participants, participant)
}
