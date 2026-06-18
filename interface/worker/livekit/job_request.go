package livekit

import (
	"context"
	"maps"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
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

type Job = lkprotocol.Job

func JobAcceptArgumentsForJob(job *lkprotocol.Job, args JobAcceptArguments) JobAcceptArguments {
	args.Identity = JobAcceptIdentity(job, args.Identity)
	return args
}

func DefaultJobRejectArguments() JobRejectArguments {
	return JobRejectArguments{Terminate: true}
}

type JobRequest struct {
	Job *lkprotocol.Job

	acceptFnc func(JobAcceptArguments) error
	rejectFnc func(JobRejectArguments) error
}

func NewJobRequest(
	job *lkprotocol.Job,
	accept func(JobAcceptArguments) error,
	reject func(JobRejectArguments) error,
) *JobRequest {
	return &JobRequest{
		Job:       job,
		acceptFnc: accept,
		rejectFnc: reject,
	}
}

func (r *JobRequest) ID() string {
	return JobID(r.Job)
}

func (r *JobRequest) Room() *lkprotocol.Room {
	return JobRoom(r.Job)
}

func (r *JobRequest) Publisher() *lkprotocol.ParticipantInfo {
	return JobPublisher(r.Job)
}

func (r *JobRequest) AgentName() string {
	return JobAgentName(r.Job)
}

func (r *JobRequest) Accept(args ...JobAcceptArguments) error {
	acceptArgs := JobAcceptArguments{}
	if len(args) > 0 {
		acceptArgs = args[0]
	}
	acceptArgs = JobAcceptArgumentsForJob(r.Job, acceptArgs)
	if r.acceptFnc != nil {
		return r.acceptFnc(acceptArgs)
	}
	return nil
}

func (r *JobRequest) Reject(args ...JobRejectArguments) error {
	rejectArgs := DefaultJobRejectArguments()
	if len(args) > 0 {
		rejectArgs = args[0]
	}
	if r.rejectFnc != nil {
		return r.rejectFnc(rejectArgs)
	}
	return nil
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

func PopulateSessionReportWithJobMetadata(report *agent.SessionReport, job *lkprotocol.Job) {
	if report == nil {
		return
	}
	info := JobSessionReportInfo(job)
	report.JobID = info.JobID
	report.RoomID = info.RoomID
	report.Room = info.Room
}

func NewJobSessionReport(job *lkprotocol.Job) (*agent.SessionReport, *agent.Tagger) {
	report := agent.NewSessionReport()
	tagger := agent.NewTagger()
	report.Tagger = tagger
	PopulateSessionReportWithJobMetadata(report, job)
	return report, tagger
}

func AllRecordingOptions() agent.RecordingOptions {
	return agent.RecordingOptions{
		Audio:      true,
		Traces:     true,
		Logs:       true,
		Transcript: true,
	}
}

func ShouldUploadJobSessionReport(job *lkprotocol.Job, fakeJob bool, report *agent.SessionReport) bool {
	if job == nil || fakeJob || report == nil {
		return false
	}
	return HasSessionRecordingOption(report.RecordingOptions) || HasSessionEvaluationReport(report)
}

func HasSessionRecordingOption(options agent.RecordingOptions) bool {
	return options.Audio || options.Traces || options.Logs || options.Transcript
}

func HasSessionEvaluationReport(report *agent.SessionReport) bool {
	if report == nil || report.Tagger == nil {
		return false
	}
	return report.Tagger.Outcome() != "" || len(report.Tagger.Evaluations()) > 0
}

func JobLogContextFields(job *lkprotocol.Job) map[string]any {
	info := JobSessionReportInfo(job)
	return map[string]any{
		"job_id": info.JobID,
		"room":   info.Room,
	}
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

type JobAssignment = lkprotocol.JobAssignment

type RunningJobInfo struct {
	AcceptArguments JobAcceptArguments `json:"accept_arguments"`
	Job             *lkprotocol.Job    `json:"job"`
	URL             string             `json:"url"`
	Token           string             `json:"token"`
	WorkerID        string             `json:"worker_id"`
	FakeJob         bool               `json:"fake_job"`
}

type RunningJobInfoOptions struct {
	AcceptArguments JobAcceptArguments
	Job             *lkprotocol.Job
	URL             string
	Token           string
	WorkerID        string
	FakeJob         bool
}

func RunningJobInfoSnapshot(opts RunningJobInfoOptions) RunningJobInfo {
	return CloneRunningJobInfo(RunningJobInfo(opts))
}

func CloneRunningJobInfo(info RunningJobInfo) RunningJobInfo {
	info.AcceptArguments.Attributes = maps.Clone(info.AcceptArguments.Attributes)
	return info
}

func JobAssignmentInfo(req *JobAssignment, defaultURL string) AssignmentInfo {
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

func PopPendingAccept(pending map[string]JobAcceptArguments, jobID string) (JobAcceptArguments, bool) {
	args, ok := pending[jobID]
	if !ok {
		return JobAcceptArguments{}, false
	}
	delete(pending, jobID)
	return args, true
}

type PendingAssignmentTimer interface {
	Stop() bool
}

func StopPendingAssignmentTimer[T PendingAssignmentTimer](pending map[string]T, jobID string) {
	timer, ok := pending[jobID]
	if !ok {
		return
	}
	timer.Stop()
	delete(pending, jobID)
}

type PendingAcceptStoreOptions struct {
	Pending   map[string]JobAcceptArguments
	Timers    map[string]*time.Timer
	JobID     string
	Args      JobAcceptArguments
	Timeout   time.Duration
	OnTimeout func(jobID string, timer *time.Timer)
}

func StorePendingAccept(opts PendingAcceptStoreOptions) {
	StopPendingAssignmentTimer(opts.Timers, opts.JobID)
	opts.Pending[opts.JobID] = opts.Args
	var timer *time.Timer
	timer = time.AfterFunc(opts.Timeout, func() {
		if opts.OnTimeout != nil {
			opts.OnTimeout(opts.JobID, timer)
		}
	})
	opts.Timers[opts.JobID] = timer
}

func ExpirePendingAccept(
	pending map[string]JobAcceptArguments,
	timers map[string]*time.Timer,
	jobID string,
	timer *time.Timer,
) bool {
	if timers[jobID] != timer {
		return false
	}
	delete(pending, jobID)
	delete(timers, jobID)
	return true
}

func AcceptPendingAssignment[T PendingAssignmentTimer](
	pending map[string]JobAcceptArguments,
	timers map[string]T,
	jobID string,
) (JobAcceptArguments, bool) {
	args, ok := PopPendingAccept(pending, jobID)
	if !ok {
		return JobAcceptArguments{}, false
	}
	StopPendingAssignmentTimer(timers, jobID)
	return args, true
}

type TerminationInfo struct {
	JobID string
}

type JobTermination = lkprotocol.JobTermination

func JobTerminationInfo(req *JobTermination) TerminationInfo {
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
