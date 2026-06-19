package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	workeripc "github.com/cavos-io/rtp-agent/interface/worker/ipc"
	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	"github.com/cavos-io/rtp-agent/library/inference"
	"github.com/cavos-io/rtp-agent/library/logger"
)

var currentJobContexts sync.Map

const errNoJobContext = "no job context found, are you running this code inside a job entrypoint?"

func init() {
	inference.SetHeadersProvider(currentInferenceContextHeaders)
}

func currentInferenceContextHeaders() map[string]string {
	ctx, ok := GetJobContext()
	if !ok || ctx == nil || ctx.Job == nil {
		return nil
	}
	return workerlivekit.JobContextInferenceHeaders(ctx.Job)
}

type jobContextStack struct {
	mu    sync.Mutex
	stack []*JobContext
}

// GetJobContext returns the JobContext currently executing on this goroutine.
//
// This mirrors LiveKit Agents' get_job_context helper for code that runs inside
// a worker job entrypoint. Go does not have Python contextvars, so newly spawned
// goroutines should receive the JobContext explicitly instead of relying on this
// helper.
func GetJobContext() (*JobContext, bool) {
	id, ok := currentGoroutineID()
	if !ok {
		return nil, false
	}
	value, ok := currentJobContexts.Load(id)
	if !ok {
		return nil, false
	}
	stack := value.(*jobContextStack)
	stack.mu.Lock()
	defer stack.mu.Unlock()
	if len(stack.stack) == 0 {
		return nil, false
	}
	return stack.stack[len(stack.stack)-1], true
}

// GetCurrentJobContext is an alias for GetJobContext kept for reference parity
// with LiveKit Agents' get_current_job_context name.
func GetCurrentJobContext() (*JobContext, bool) {
	return GetJobContext()
}

func RequireJobContext() (*JobContext, error) {
	ctx, ok := GetJobContext()
	if !ok || ctx == nil {
		return nil, errors.New(errNoJobContext)
	}
	return ctx, nil
}

func RequireCurrentJobContext() (*JobContext, error) {
	return RequireJobContext()
}

func runWithJobContext(ctx *JobContext, fn func() error) error {
	if fn == nil {
		return nil
	}
	id, ok := currentGoroutineID()
	if !ok {
		return fn()
	}
	value, _ := currentJobContexts.LoadOrStore(id, &jobContextStack{})
	stack := value.(*jobContextStack)
	stack.mu.Lock()
	stack.stack = append(stack.stack, ctx)
	stack.mu.Unlock()
	defer popCurrentJobContext(id, stack)
	return fn()
}

func popCurrentJobContext(id uint64, stack *jobContextStack) {
	stack.mu.Lock()
	if len(stack.stack) > 0 {
		stack.stack = stack.stack[:len(stack.stack)-1]
	}
	empty := len(stack.stack) == 0
	stack.mu.Unlock()
	if empty {
		currentJobContexts.Delete(id)
	}
}

func currentGoroutineID() (uint64, bool) {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	line := buf[:n]
	line = bytes.TrimPrefix(line, []byte("goroutine "))
	idField, _, ok := bytes.Cut(line, []byte(" "))
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseUint(string(idField), 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

type JobAcceptArguments = workerlivekit.JobAcceptArguments

type JobRejectArguments = workerlivekit.JobRejectArguments

type JobExecutorType = workeripc.ExecutorType

const (
	JobExecutorTypeThread  JobExecutorType = workeripc.ExecutorTypeThread
	JobExecutorTypeProcess JobExecutorType = workeripc.ExecutorTypeProcess
)

type JobProcess struct {
	executorType  JobExecutorType
	pid           int
	userdata      map[any]any
	userArguments any
	httpProxy     string
}

func NewJobProcess(executorType JobExecutorType, userArguments any, httpProxy string) *JobProcess {
	if executorType == "" {
		executorType = JobExecutorTypeThread
	}
	return &JobProcess{
		executorType:  executorType,
		pid:           os.Getpid(),
		userdata:      make(map[any]any),
		userArguments: userArguments,
		httpProxy:     httpProxy,
	}
}

func (p *JobProcess) ExecutorType() JobExecutorType {
	if p == nil {
		return ""
	}
	return p.executorType
}

func (p *JobProcess) PID() int {
	if p == nil {
		return 0
	}
	return p.pid
}

func (p *JobProcess) Userdata() map[any]any {
	if p == nil {
		return nil
	}
	if p.userdata == nil {
		p.userdata = make(map[any]any)
	}
	return p.userdata
}

func (p *JobProcess) UserArguments() any {
	if p == nil {
		return nil
	}
	return p.userArguments
}

func (p *JobProcess) HTTPProxy() string {
	if p == nil {
		return ""
	}
	return p.httpProxy
}

type JobRoomServiceAPI = workerlivekit.JobRoomServiceAPI
type JobSIPAPI = workerlivekit.JobSIPAPI
type JobAPI = workerlivekit.JobAPI

func NewJobAPI(url string, apiKey string, apiSecret string) *JobAPI {
	return workerlivekit.NewJobContextAPI(url, apiKey, apiSecret)
}

type AutoSubscribe = workerlivekit.AutoSubscribe

const (
	AutoSubscribeSubscribeAll  = workerlivekit.AutoSubscribeSubscribeAll
	AutoSubscribeSubscribeNone = workerlivekit.AutoSubscribeSubscribeNone
	AutoSubscribeAudioOnly     = workerlivekit.AutoSubscribeAudioOnly
	AutoSubscribeVideoOnly     = workerlivekit.AutoSubscribeVideoOnly
)

type ConnectOptions = workerlivekit.ConnectOptions

type ParticipantInfo = workerlivekit.ParticipantInfo

type ParticipantInfoKind = workerlivekit.ParticipantInfoKind

type ParticipantTaskKey = workerlivekit.ParticipantTaskKey

type ParticipantEntrypoint func(*JobContext, *ParticipantInfo)

type TrackPublicationWaitOptions = workerlivekit.TrackPublicationWaitOptions

type participantEntrypointRegistration struct {
	entrypoint ParticipantEntrypoint
	kinds      []ParticipantInfoKind
}

type JobRequest = workerlivekit.JobRequest

type JobContext struct {
	Job                    *workerlivekit.Job
	Room                   *workerlivekit.SDKRoom
	Report                 *agent.SessionReport
	AcceptArguments        JobAcceptArguments
	tagger                 *agent.Tagger
	workerID               string
	process                *JobProcess
	primarySession         *agent.AgentSession
	sessionDirectory       string
	logContextFields       map[string]any
	recordingInitialized   bool
	shutdownCallbacks      []func(string)
	shutdownOnce           sync.Once
	shutdownDone           chan struct{}
	entrypointStarted      atomic.Bool
	entrypointDone         chan struct{}
	entrypointDoneOnce     sync.Once
	terminated             atomic.Bool
	finishOnce             sync.Once
	participantEntrypoints []participantEntrypointRegistration
	availableParticipants  []*ParticipantInfo
	participantTasks       map[ParticipantTaskKey]struct{}
	participantTasksMu     sync.Mutex

	api       *JobAPI
	apiKey    string
	apiSecret string
	url       string
	token     string
	fakeJob   bool
}

func NewJobContext(job *workerlivekit.Job, url string, apiKey string, apiSecret string) *JobContext {
	report, tagger := workerlivekit.NewJobContextSessionReport(job)
	return &JobContext{
		Job:              job,
		url:              url,
		apiKey:           apiKey,
		apiSecret:        apiSecret,
		Report:           report,
		tagger:           tagger,
		process:          NewJobProcess(JobExecutorTypeThread, nil, ""),
		shutdownDone:     make(chan struct{}),
		entrypointDone:   make(chan struct{}),
		logContextFields: workerlivekit.JobContextLogFields(job),
	}
}

func (c *JobContext) Tagger() *agent.Tagger {
	if c == nil {
		return nil
	}
	if c.tagger == nil {
		c.tagger = agent.NewTagger()
	}
	return c.tagger
}

func (c *JobContext) WorkerID() string {
	if c == nil {
		return ""
	}
	return c.workerID
}

func (c *JobContext) InitRecording(options agent.RecordingOptions) {
	if c == nil {
		return
	}
	if c.recordingInitialized {
		return
	}
	c.recordingInitialized = true
	if c.Report == nil {
		c.Report = agent.NewSessionReport()
		c.Report.Tagger = c.Tagger()
	}
	c.Report.RecordingOptions = options
}

func (c *JobContext) API() *JobAPI {
	if c == nil {
		return nil
	}
	if c.api == nil {
		c.api = NewJobAPI(c.url, c.apiKey, c.apiSecret)
	}
	return c.api
}

func (c *JobContext) ParticipantIdentity() string {
	return workerlivekit.JobContextParticipantIdentity(c.Job, c.AcceptArguments.Identity)
}

func (c *JobContext) LocalParticipantIdentity() string {
	return workerlivekit.JobContextLocalParticipantIdentity(c.token, c.ParticipantIdentity())
}

func (c *JobContext) TokenClaims() (*workerlivekit.ClaimGrants, error) {
	return workerlivekit.JobContextTokenClaims(c.token)
}

func (c *JobContext) JobID() string {
	return workerlivekit.JobContextJobID(c.Job)
}

func (c *JobContext) IsFakeJob() bool {
	return c.fakeJob
}

func (c *JobContext) SetSessionDirectory(path string) {
	c.sessionDirectory = path
}

func (c *JobContext) SessionDirectory() string {
	return c.sessionDirectory
}

func (c *JobContext) LogContextFields() map[string]any {
	if c.logContextFields == nil {
		c.logContextFields = make(map[string]any)
	}
	return c.logContextFields
}

func (c *JobContext) SetLogContextFields(fields map[string]any) {
	c.logContextFields = fields
	if c.logContextFields == nil {
		c.logContextFields = make(map[string]any)
	}
}

func (c *JobContext) Proc() *JobProcess {
	if c.process == nil {
		c.process = NewJobProcess(JobExecutorTypeThread, nil, "")
	}
	return c.process
}

func (c *JobContext) SetPrimarySession(session *agent.AgentSession) {
	c.primarySession = session
}

func (c *JobContext) PrimarySession() (*agent.AgentSession, error) {
	if c.primarySession == nil {
		//lint:ignore ST1005 match LiveKit Agents primary_session RuntimeError message
		return nil, fmt.Errorf("No AgentSession was started for this job")
	}
	return c.primarySession, nil
}

func (c *JobContext) MakeSessionReport(sessions ...*agent.AgentSession) (*agent.SessionReport, error) {
	var session *agent.AgentSession
	if len(sessions) > 0 {
		session = sessions[0]
	} else {
		session = c.primarySession
	}
	if session == nil {
		//lint:ignore ST1005 match LiveKit Agents make_session_report RuntimeError message
		return nil, fmt.Errorf("Cannot prepare report, no AgentSession was found")
	}

	report := agent.NewSessionReport(session)
	workerlivekit.PopulateJobContextSessionReport(report, c.Job)
	if c.Report != nil {
		report.RecordingOptions = c.Report.RecordingOptions
		report.AudioRecordingPath = c.Report.AudioRecordingPath
		report.AudioRecordingStartedAt = c.Report.AudioRecordingStartedAt
		report.Duration = c.Report.Duration
	}
	report.Tagger = c.Tagger()
	c.Report = report
	return report, nil
}

func (c *JobContext) AvatarStartInfo() agent.AvatarStartInfo {
	return workerlivekit.JobContextAvatarStartInfo(c.Job, c.url, c.token, c.LocalParticipantIdentity())
}

func (c *JobContext) RoomInfo() *workerlivekit.Room {
	return workerlivekit.JobContextRoom(c.Job)
}

func (c *JobContext) PublisherInfo() *ParticipantInfo {
	return workerlivekit.JobContextPublisher(c.Job)
}

func (c *JobContext) Agent() *workerlivekit.LocalParticipant {
	if c == nil {
		return nil
	}
	return workerlivekit.JobContextLocalParticipant(c.Room)
}

var jobContextNewRoom = workerlivekit.NewJobContextRoom

var jobContextRoomConnector workerlivekit.RoomConnector

func (c *JobContext) NewRoom(cb *workerlivekit.RoomCallback, options ...ConnectOptions) *workerlivekit.SDKRoom {
	opts := workerlivekit.JobContextNormalizeConnectOptions(options...)
	return jobContextNewRoom(c.roomCallbackWithEntrypoints(cb, opts.AutoSubscribe))
}

func (c *JobContext) Connect(ctx context.Context, cb *workerlivekit.RoomCallback, options ...ConnectOptions) error {
	if c.Room != nil {
		return nil
	}
	opts := workerlivekit.JobContextNormalizeConnectOptions(options...)
	room := c.NewRoom(cb, opts)
	return c.ConnectPreparedRoom(ctx, room, opts)
}

func (c *JobContext) ConnectPreparedRoom(ctx context.Context, room *workerlivekit.SDKRoom, options ...ConnectOptions) error {
	if c.Room != nil {
		return nil
	}
	if room == nil {
		return fmt.Errorf("prepared room is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	opts := workerlivekit.JobContextNormalizeConnectOptions(options...)
	if err := workerlivekit.JobContextJoinPreparedRoom(ctx, workerlivekit.AcceptedJobRoomConnectOptions{
		Room:          room,
		URL:           c.url,
		Token:         c.token,
		Job:           c.Job,
		APIKey:        c.apiKey,
		APISecret:     c.apiSecret,
		AutoSubscribe: string(opts.AutoSubscribe),
		Connector:     jobContextRoomConnector,
		Accept:        c.AcceptArguments,
		Identity:      c.ParticipantIdentity(),
	}); err != nil {
		return err
	}
	c.Room = room
	c.participantsAvailable(workerlivekit.JobContextRemoteParticipantViews(room))
	c.applyAutoSubscribeOptions(opts.AutoSubscribe)
	logger.Logger.Infow("Connected to room", "room", workerlivekit.JobContextRoomName(c.Job))
	return nil
}

func (c *JobContext) applyAutoSubscribeOptions(mode AutoSubscribe) {
	for _, result := range workerlivekit.JobContextApplyAutoSubscribeToRoom(c.Room, string(mode)) {
		if result.Err != nil {
			logger.Logger.Warnw("failed to subscribe remote track", result.Err, "trackSid", result.TrackSID)
		}
	}
}

func (c *JobContext) roomCallbackWithEntrypoints(cb *workerlivekit.RoomCallback, autoSubscribe AutoSubscribe) *workerlivekit.RoomCallback {
	return workerlivekit.JobContextRoomCallbackWithHandlers(cb, workerlivekit.RoomCallbackHandlers{
		AutoSubscribe:          string(autoSubscribe),
		OnParticipantConnected: c.participantAvailable,
		OnTrackSubscribeError: func(result workerlivekit.RemoteTrackSubscriptionResult) {
			logger.Logger.Warnw("failed to subscribe published remote track", result.Err, "trackSid", result.TrackSID)
		},
	})
}

func (c *JobContext) participantAvailable(participant workerlivekit.RemoteParticipantView) {
	info := workerlivekit.JobContextParticipantInfoFromRemoteParticipant(participant)
	if info == nil {
		return
	}
	c.rememberAvailableParticipant(info)
	c.scheduleParticipantEntrypoints(info)
}

func (c *JobContext) rememberAvailableParticipant(info *ParticipantInfo) {
	c.availableParticipants = workerlivekit.JobContextUpsertParticipantInfo(c.availableParticipants, info)
}

func (c *JobContext) participantsAvailable(participants []workerlivekit.RemoteParticipantView) {
	for _, participant := range participants {
		c.participantAvailable(participant)
	}
}

func (c *JobContext) AddShutdownCallback(callback any) error {
	switch cb := callback.(type) {
	case func():
		c.shutdownCallbacks = append(c.shutdownCallbacks, func(string) {
			cb()
		})
	case func(string):
		c.shutdownCallbacks = append(c.shutdownCallbacks, cb)
	default:
		return fmt.Errorf("shutdown callback must be func() or func(string)")
	}
	return nil
}

func (c *JobContext) AddParticipantEntrypoint(entrypoint ParticipantEntrypoint, kinds ...ParticipantInfoKind) error {
	if entrypoint == nil {
		return fmt.Errorf("participant entrypoint must not be nil")
	}
	entrypointPointer := reflect.ValueOf(entrypoint).Pointer()
	registeredEntrypoints := make([]uintptr, 0, len(c.participantEntrypoints))
	for _, registered := range c.participantEntrypoints {
		registeredEntrypoints = append(registeredEntrypoints, reflect.ValueOf(registered.entrypoint).Pointer())
	}
	plan, err := workerlivekit.JobContextParticipantEntrypointRegistrationPlan(workerlivekit.ParticipantEntrypointRegistrationOptions{
		Entrypoint:            entrypointPointer,
		RegisteredEntrypoints: registeredEntrypoints,
		Kinds:                 kinds,
	})
	if err != nil {
		return err
	}
	registration := participantEntrypointRegistration{
		entrypoint: entrypoint,
		kinds:      plan.Kinds,
	}
	c.participantEntrypoints = append(c.participantEntrypoints, registration)
	c.scheduleParticipantEntrypointForExistingParticipants(registration)
	return nil
}

func (c *JobContext) scheduleParticipantEntrypointForExistingParticipants(registration participantEntrypointRegistration) {
	for _, participant := range c.availableParticipants {
		c.scheduleParticipantEntrypoint(registration, participant)
	}
}

func (c *JobContext) WaitForParticipant(
	ctx context.Context,
	identity string,
	kinds ...ParticipantInfoKind,
) (*workerlivekit.RemoteParticipant, error) {
	if err := c.ensureRoomConnected(ctx); err != nil {
		return nil, err
	}
	return workerlivekit.JobContextWaitForParticipant(ctx, c.Room, identity, kinds...)
}

func (c *JobContext) WaitForAgent(
	ctx context.Context,
	agentName ...string,
) (*workerlivekit.RemoteParticipant, error) {
	if err := c.ensureRoomConnected(ctx); err != nil {
		return nil, err
	}
	return workerlivekit.JobContextWaitForAgent(ctx, c.Room, agentName...)
}

func (c *JobContext) WaitForTrackPublication(
	ctx context.Context,
	identity string,
	kinds ...workerlivekit.TrackType,
) (*workerlivekit.RemoteTrackPublication, error) {
	if err := c.ensureRoomConnected(ctx); err != nil {
		return nil, err
	}
	return workerlivekit.JobContextWaitForTrackPublication(ctx, c.Room, identity, kinds...)
}

func (c *JobContext) WaitForTrackPublicationWithOptions(
	ctx context.Context,
	options TrackPublicationWaitOptions,
) (*workerlivekit.RemoteTrackPublication, error) {
	if err := c.ensureRoomConnected(ctx); err != nil {
		return nil, err
	}
	return workerlivekit.JobContextWaitForTrackPublicationWithOptions(ctx, c.Room, options)
}

func (c *JobContext) WaitForParticipantAttribute(
	ctx context.Context,
	identity string,
	attribute string,
	value string,
) error {
	if err := c.ensureRoomConnected(ctx); err != nil {
		return err
	}
	return workerlivekit.JobContextWaitForParticipantAttribute(ctx, c.Room, identity, attribute, value)
}

func (c *JobContext) ensureRoomConnected(ctx context.Context) error {
	if c.Room != nil {
		return nil
	}
	return c.Connect(ctx, nil)
}

func (c *JobContext) scheduleParticipantEntrypoints(participant *ParticipantInfo) {
	if participant == nil {
		return
	}
	for _, registered := range c.participantEntrypoints {
		c.scheduleParticipantEntrypoint(registered, participant)
	}
}

func (c *JobContext) scheduleParticipantEntrypoint(registration participantEntrypointRegistration, participant *ParticipantInfo) {
	plan := workerlivekit.JobContextParticipantEntrypointTaskPlan(
		participant,
		registration.kinds,
		reflect.ValueOf(registration.entrypoint).Pointer(),
	)
	if !plan.Schedule {
		return
	}
	c.participantTasksMu.Lock()
	if c.participantTasks == nil {
		c.participantTasks = make(map[ParticipantTaskKey]struct{})
	}
	if _, ok := c.participantTasks[plan.TaskKey]; ok {
		logger.Logger.Warnw("participant entrypoint already running for participant", nil, "participant", plan.Participant.Identity)
	}
	c.participantTasks[plan.TaskKey] = struct{}{}
	c.participantTasksMu.Unlock()

	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Logger.Errorw("Participant entrypoint panicked", fmt.Errorf("%v", recovered), "participant", plan.Participant.Identity)
			}
			c.participantTasksMu.Lock()
			delete(c.participantTasks, plan.TaskKey)
			c.participantTasksMu.Unlock()
		}()
		_ = runWithJobContext(c, func() error {
			registration.entrypoint(c, participant)
			return nil
		})
	}()
}

func (c *JobContext) Shutdown(reasons ...string) {
	reason := ""
	if len(reasons) > 0 {
		reason = reasons[0]
	}
	c.shutdownOnce.Do(func() {
		if c.shutdownDone == nil {
			c.shutdownDone = make(chan struct{})
		}
		for _, callback := range c.shutdownCallbacks {
			func(callback func(string)) {
				defer func() {
					if recovered := recover(); recovered != nil {
						logger.Logger.Errorw("Shutdown callback panicked", fmt.Errorf("%v", recovered), "job_id", c.JobID())
					}
				}()
				callback(reason)
			}(callback)
		}
		if c.Room != nil {
			c.Room.Disconnect()
		}
		close(c.shutdownDone)
	})
}

func (c *JobContext) ShutdownDone() <-chan struct{} {
	if c == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	if c.shutdownDone == nil {
		c.shutdownDone = make(chan struct{})
	}
	return c.shutdownDone
}

func (c *JobContext) markEntrypointStarted() {
	if c == nil {
		return
	}
	if c.entrypointDone == nil {
		c.entrypointDone = make(chan struct{})
	}
	c.entrypointStarted.Store(true)
}

func (c *JobContext) markEntrypointDone() {
	if c == nil || !c.entrypointStarted.Load() {
		return
	}
	if c.entrypointDone == nil {
		c.entrypointDone = make(chan struct{})
	}
	c.entrypointDoneOnce.Do(func() {
		close(c.entrypointDone)
	})
}

func (c *JobContext) waitForEntrypointDone(timeout time.Duration) bool {
	if c == nil || !c.entrypointStarted.Load() {
		return true
	}
	if c.entrypointDone == nil {
		c.entrypointDone = make(chan struct{})
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-c.entrypointDone:
		return true
	case <-timer.C:
		return false
	}
}

func (c *JobContext) markTerminated() {
	if c != nil {
		c.terminated.Store(true)
	}
}

func (c *JobContext) Terminated() bool {
	return c != nil && c.terminated.Load()
}

// DeleteRoom deletes the room and disconnects all participants.
func (c *JobContext) DeleteRoom(ctx context.Context, roomName string) (*workerlivekit.DeleteRoomResponse, error) {
	if plan := workerlivekit.JobContextDeleteRoomPlan(c.IsFakeJob()); plan.Skip {
		logger.Logger.Warnw("job context DeleteRoom is skipped for fake jobs", nil)
		return plan.Response, nil
	}
	resp, warnErr := workerlivekit.JobContextDeleteRoomBestEffort(ctx, c.API().RoomService, c.Job, roomName)
	if warnErr != nil {
		logger.Logger.Warnw("error while deleting room", warnErr)
	}
	return resp, nil
}

func (c *JobContext) MoveParticipant(ctx context.Context, room string, identity string, destinationRoom string) error {
	if plan := workerlivekit.JobContextMoveParticipantPlan(c.IsFakeJob()); plan.Skip {
		logger.Logger.Warnw("job context MoveParticipant is skipped for fake jobs", nil)
		return nil
	}
	return workerlivekit.JobContextMoveParticipant(ctx, c.API().RoomService, c.Job, room, identity, destinationRoom)
}

// AddSIPParticipant adds a SIP participant to the room.
func (c *JobContext) AddSIPParticipant(ctx context.Context, callTo string, trunkID string, identity string, names ...string) (*workerlivekit.SIPParticipantInfo, error) {
	if plan := workerlivekit.JobContextSIPCreateParticipantPlan(c.IsFakeJob()); plan.Skip {
		logger.Logger.Warnw("job context AddSIPParticipant is skipped for fake jobs", nil)
		return plan.Info, nil
	}
	return workerlivekit.JobContextCreateSIPParticipantWithNames(ctx, c.API().SIP, c.Job, callTo, trunkID, identity, names...)
}

func (c *JobContext) CreateSIPParticipant(ctx context.Context, req *workerlivekit.SIPCreateParticipantRequest) (*workerlivekit.SIPParticipantInfo, error) {
	if plan := workerlivekit.JobContextSIPCreateParticipantPlan(c.IsFakeJob()); plan.Skip {
		logger.Logger.Warnw("job context CreateSIPParticipant is skipped for fake jobs", nil)
		return plan.Info, nil
	}
	return workerlivekit.JobContextCreateSIPParticipantWithRequest(ctx, c.API().SIP, req)
}

// TransferSIPParticipant transfers a SIP participant to another number.
func (c *JobContext) TransferSIPParticipant(ctx context.Context, identity string, transferTo string, playDialtones ...bool) error {
	return c.TransferSIPParticipantByParticipant(ctx, identity, transferTo, playDialtones...)
}

func (c *JobContext) TransferSIPParticipantByParticipant(ctx context.Context, participant any, transferTo string, playDialtones ...bool) error {
	if plan := workerlivekit.JobContextSIPTransferParticipantPlan(c.IsFakeJob()); plan.Skip {
		logger.Logger.Warnw("job context TransferSIPParticipant is skipped for fake jobs", nil)
		return nil
	}
	return workerlivekit.JobContextTransferSIPParticipantByParticipant(ctx, c.API().SIP, c.Job, participant, transferTo, playDialtones...)
}
