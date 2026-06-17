package livekit

import (
	"context"

	"github.com/cavos-io/rtp-agent/library/inference"
	"github.com/cavos-io/rtp-agent/library/math"
	lkprotocol "github.com/livekit/protocol/livekit"
)

type JobAcceptArguments struct {
	Name       string            `json:"name"`
	Identity   string            `json:"identity"`
	Metadata   string            `json:"metadata"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type JobRejectArguments struct {
	Terminate bool
}

func JobID(job *lkprotocol.Job) string {
	if job == nil {
		return ""
	}
	return job.Id
}

func JobRoom(job *lkprotocol.Job) *lkprotocol.Room {
	if job == nil {
		return nil
	}
	return job.Room
}

func JobRoomName(job *lkprotocol.Job) string {
	room := JobRoom(job)
	if room == nil {
		return ""
	}
	return room.Name
}

func JobPublisher(job *lkprotocol.Job) *lkprotocol.ParticipantInfo {
	if job == nil {
		return nil
	}
	return job.Participant
}

func JobAgentName(job *lkprotocol.Job) string {
	if job == nil {
		return ""
	}
	return job.AgentName
}

type RuntimeJobInfo struct {
	JobID           string
	EnableRecording bool
}

func JobRuntimeInfo(job *lkprotocol.Job) RuntimeJobInfo {
	if job == nil {
		return RuntimeJobInfo{}
	}
	return RuntimeJobInfo{
		JobID:           job.Id,
		EnableRecording: job.GetEnableRecording(),
	}
}

type SessionReportInfo struct {
	JobID  string
	RoomID string
	Room   string
}

func JobSessionReportInfo(job *lkprotocol.Job) SessionReportInfo {
	if job == nil {
		return SessionReportInfo{}
	}
	info := SessionReportInfo{JobID: job.GetId()}
	if room := job.GetRoom(); room != nil {
		info.RoomID = room.GetSid()
		info.Room = room.GetName()
	}
	return info
}

type MetricInfo struct {
	RoomName string
}

func JobMetricInfo(job *lkprotocol.Job) MetricInfo {
	if job == nil {
		return MetricInfo{}
	}
	return MetricInfo{RoomName: JobRoomName(job)}
}

func JobInferenceHeaders(job *lkprotocol.Job) map[string]string {
	if job == nil {
		return nil
	}
	headers := map[string]string{}
	if jobID := job.GetId(); jobID != "" {
		headers[inference.HeaderJobID] = jobID
	}
	if room := job.GetRoom(); room != nil && room.GetSid() != "" {
		headers[inference.HeaderRoomID] = room.GetSid()
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

type AssignmentInfo struct {
	Job             *lkprotocol.Job
	JobID           string
	URL             string
	Token           string
	EnableRecording bool
}

func JobAssignmentInfo(req *lkprotocol.JobAssignment, defaultURL string) AssignmentInfo {
	if req == nil {
		return AssignmentInfo{URL: defaultURL}
	}
	jobURL := defaultURL
	if req.GetUrl() != "" {
		jobURL = req.GetUrl()
	}
	return AssignmentInfo{
		Job:             req.Job,
		JobID:           JobID(req.Job),
		URL:             jobURL,
		Token:           req.GetToken(),
		EnableRecording: req.Job.GetEnableRecording(),
	}
}

type TerminationInfo struct {
	JobID string
}

func JobTerminationInfo(req *lkprotocol.JobTermination) TerminationInfo {
	if req == nil {
		return TerminationInfo{}
	}
	return TerminationInfo{JobID: req.JobId}
}

type LocalRoomJobOptions struct {
	RoomName string
	RoomInfo *lkprotocol.Room
	FakeJob  bool
	NewID    func(prefix string) string
}

func LocalRoomJob(opts LocalRoomJobOptions) *lkprotocol.Job {
	newID := opts.NewID
	if newID == nil {
		newID = math.ShortUUID
	}
	jobIDPrefix := "job-"
	if opts.FakeJob {
		jobIDPrefix = "mock-job-"
	}
	room := opts.RoomInfo
	if room == nil {
		room = &lkprotocol.Room{
			Name: opts.RoomName,
			Sid:  newID("SRM_"),
		}
	}
	return &lkprotocol.Job{
		Id:   newID(jobIDPrefix),
		Room: room,
		Type: lkprotocol.JobType_JT_ROOM,
	}
}

type LocalJobRuntimeInfo struct {
	JobID      string
	ExecutorID string
}

func LocalJobInfo(job *lkprotocol.Job) LocalJobRuntimeInfo {
	jobID := JobID(job)
	return LocalJobRuntimeInfo{
		JobID:      jobID,
		ExecutorID: "local_" + jobID,
	}
}

func JobAcceptIdentity(job *lkprotocol.Job, identity string) string {
	if identity != "" || job == nil {
		return identity
	}
	return AgentIdentityForJobID(job.Id)
}

func JobParticipantIdentity(job *lkprotocol.Job, acceptedIdentity string) string {
	if acceptedIdentity != "" || job == nil {
		return acceptedIdentity
	}
	return AgentIdentityForJobID(job.Id)
}

func MoveParticipantRequest(job *lkprotocol.Job, room string, identity string, destinationRoom string) *lkprotocol.MoveParticipantRequest {
	if destinationRoom == "" && job != nil && job.Room != nil {
		destinationRoom = job.Room.Name
	}
	return &lkprotocol.MoveParticipantRequest{
		Room:            room,
		Identity:        identity,
		DestinationRoom: destinationRoom,
	}
}

type MoveParticipantAPI interface {
	MoveParticipant(context.Context, *lkprotocol.MoveParticipantRequest) (*lkprotocol.MoveParticipantResponse, error)
}

func MoveParticipant(ctx context.Context, api MoveParticipantAPI, job *lkprotocol.Job, room string, identity string, destinationRoom string) error {
	if api == nil {
		return nil
	}
	_, err := api.MoveParticipant(ctx, MoveParticipantRequest(job, room, identity, destinationRoom))
	return err
}
