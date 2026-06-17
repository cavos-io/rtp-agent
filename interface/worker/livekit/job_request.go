package livekit

import (
	"github.com/cavos-io/rtp-agent/library/math"
	lkprotocol "github.com/livekit/protocol/livekit"
)

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
