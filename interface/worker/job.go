package worker

import (
	"context"
	"fmt"
	"maps"
	"os"
	"reflect"
	"sync"

	"github.com/cavos-io/rtp-agent/core/agent"
	workeripc "github.com/cavos-io/rtp-agent/interface/worker/ipc"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/utils"
	"github.com/go-jose/go-jose/v3/jwt"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

type JobAcceptArguments struct {
	Name       string
	Identity   string
	Metadata   string
	Attributes map[string]string
}

type JobRejectArguments struct {
	Terminate bool
}

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

type JobAPI struct {
	RoomService *lksdk.RoomServiceClient
	SIP         *lksdk.SIPClient
}

func NewJobAPI(url string, apiKey string, apiSecret string) *JobAPI {
	return &JobAPI{
		RoomService: lksdk.NewRoomServiceClient(url, apiKey, apiSecret),
		SIP:         lksdk.NewSIPClient(url, apiKey, apiSecret),
	}
}

type AutoSubscribe string

const (
	AutoSubscribeSubscribeAll  AutoSubscribe = "subscribe_all"
	AutoSubscribeSubscribeNone AutoSubscribe = "subscribe_none"
	AutoSubscribeAudioOnly     AutoSubscribe = "audio_only"
	AutoSubscribeVideoOnly     AutoSubscribe = "video_only"
)

type ConnectOptions struct {
	AutoSubscribe AutoSubscribe
}

type ParticipantEntrypoint func(*JobContext, *livekit.ParticipantInfo)

type participantEntrypointRegistration struct {
	entrypoint ParticipantEntrypoint
	kinds      []livekit.ParticipantInfo_Kind
}

type participantEntrypointTaskKey struct {
	identity   string
	entrypoint uintptr
}

type remoteParticipantView interface {
	SID() string
	Identity() string
	Name() string
	Kind() lksdk.ParticipantKind
	Metadata() string
	Attributes() map[string]string
}

var defaultParticipantEntrypointKinds = []livekit.ParticipantInfo_Kind{
	livekit.ParticipantInfo_CONNECTOR,
	livekit.ParticipantInfo_SIP,
	livekit.ParticipantInfo_STANDARD,
}

const defaultSIPParticipantName = "SIP-participant"

type JobRequest struct {
	Job *livekit.Job

	acceptFnc func(JobAcceptArguments) error
	rejectFnc func(JobRejectArguments) error
}

func (r *JobRequest) ID() string {
	if r.Job == nil {
		return ""
	}
	return r.Job.Id
}

func (r *JobRequest) Room() *livekit.Room {
	if r.Job == nil {
		return nil
	}
	return r.Job.Room
}

func (r *JobRequest) Publisher() *livekit.ParticipantInfo {
	if r.Job == nil {
		return nil
	}
	return r.Job.Participant
}

func (r *JobRequest) AgentName() string {
	if r.Job == nil {
		return ""
	}
	return r.Job.AgentName
}

func (r *JobRequest) Accept(args ...JobAcceptArguments) error {
	acceptArgs := JobAcceptArguments{}
	if len(args) > 0 {
		acceptArgs = args[0]
	}
	if acceptArgs.Identity == "" && r.Job != nil {
		acceptArgs.Identity = agentIdentityForJobID(r.Job.Id)
	}
	if r.acceptFnc != nil {
		return r.acceptFnc(acceptArgs)
	}
	return nil
}

func (r *JobRequest) Reject(args ...JobRejectArguments) error {
	rejectArgs := JobRejectArguments{Terminate: true}
	if len(args) > 0 {
		rejectArgs = args[0]
	}
	if r.rejectFnc != nil {
		return r.rejectFnc(rejectArgs)
	}
	return nil
}

type JobContext struct {
	Job                    *livekit.Job
	Room                   *lksdk.Room
	Report                 *agent.SessionReport
	Tagger                 *agent.Tagger
	AcceptArguments        JobAcceptArguments
	WorkerID               string
	process                *JobProcess
	primarySession         *agent.AgentSession
	sessionDirectory       string
	logContextFields       map[string]any
	shutdownCallbacks      []func(string)
	shutdownOnce           sync.Once
	finishOnce             sync.Once
	participantEntrypoints []participantEntrypointRegistration
	availableParticipants  []*livekit.ParticipantInfo
	participantTasks       map[participantEntrypointTaskKey]struct{}
	participantTasksMu     sync.Mutex

	api       *JobAPI
	apiKey    string
	apiSecret string
	url       string
	token     string
	fakeJob   bool
}

func NewJobContext(job *livekit.Job, url string, apiKey string, apiSecret string) *JobContext {
	report := agent.NewSessionReport()
	tagger := agent.NewTagger()
	report.Tagger = tagger
	if job != nil {
		report.JobID = job.GetId()
		if room := job.GetRoom(); room != nil {
			report.RoomID = room.GetSid()
			report.Room = room.GetName()
		}
	}
	return &JobContext{
		Job:       job,
		url:       url,
		apiKey:    apiKey,
		apiSecret: apiSecret,
		Report:    report,
		Tagger:    tagger,
		process:   NewJobProcess(JobExecutorTypeThread, nil, ""),
		logContextFields: map[string]any{
			"job_id": report.JobID,
			"room":   report.Room,
		},
	}
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
	if c.AcceptArguments.Identity != "" {
		return c.AcceptArguments.Identity
	}
	if c.Job == nil {
		return ""
	}
	return agentIdentityForJobID(c.Job.Id)
}

func (c *JobContext) LocalParticipantIdentity() string {
	claims, err := c.TokenClaims()
	if err == nil && claims.Identity != "" {
		return claims.Identity
	}
	return c.ParticipantIdentity()
}

func (c *JobContext) TokenClaims() (*auth.ClaimGrants, error) {
	tok, err := jwt.ParseSigned(c.token)
	if err != nil {
		return nil, err
	}
	claims := &auth.ClaimGrants{}
	if err := tok.UnsafeClaimsWithoutVerification(claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func (c *JobContext) JobID() string {
	if c.Job == nil {
		return ""
	}
	return c.Job.Id
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
		return nil, fmt.Errorf("no primary AgentSession was started for this job")
	}
	return c.primarySession, nil
}

func (c *JobContext) MakeSessionReport(sessions ...*agent.AgentSession) (*agent.SessionReport, error) {
	var session *agent.AgentSession
	if len(sessions) > 0 {
		session = sessions[0]
	} else {
		var err error
		session, err = c.PrimarySession()
		if err != nil {
			return nil, err
		}
	}
	if session == nil {
		return nil, fmt.Errorf("cannot prepare report, no AgentSession was found")
	}

	report := agent.NewSessionReport(session)
	if c.Job != nil {
		report.JobID = c.Job.GetId()
		if room := c.Job.GetRoom(); room != nil {
			report.RoomID = room.GetSid()
			report.Room = room.GetName()
		}
	}
	if c.Report != nil {
		report.RecordingOptions = c.Report.RecordingOptions
		report.AudioRecordingPath = c.Report.AudioRecordingPath
		report.AudioRecordingStartedAt = c.Report.AudioRecordingStartedAt
		report.Duration = c.Report.Duration
	}
	report.Tagger = c.Tagger
	c.Report = report
	return report, nil
}

func (c *JobContext) AvatarStartInfo() agent.AvatarStartInfo {
	return agent.AvatarStartInfo{
		LiveKitURL:   c.url,
		LiveKitToken: c.token,
	}
}

func (c *JobContext) RoomInfo() *livekit.Room {
	if c.Job == nil {
		return nil
	}
	return c.Job.Room
}

func (c *JobContext) PublisherInfo() *livekit.ParticipantInfo {
	if c.Job == nil {
		return nil
	}
	return c.Job.Participant
}

func (c *JobContext) connectInfo() lksdk.ConnectInfo {
	return lksdk.ConnectInfo{
		APIKey:                c.apiKey,
		APISecret:             c.apiSecret,
		RoomName:              c.Job.Room.Name,
		ParticipantName:       c.AcceptArguments.Name,
		ParticipantIdentity:   c.ParticipantIdentity(),
		ParticipantKind:       lksdk.ParticipantAgent,
		ParticipantMetadata:   c.AcceptArguments.Metadata,
		ParticipantAttributes: c.AcceptArguments.Attributes,
	}
}

func (c *JobContext) Connect(ctx context.Context, cb *lksdk.RoomCallback, options ...ConnectOptions) error {
	if c.Room != nil {
		return nil
	}
	opts := normalizeConnectOptions(options...)
	cb = c.roomCallbackWithEntrypoints(cb, opts.AutoSubscribe)
	connectOptions := []lksdk.ConnectOption{
		lksdk.WithAutoSubscribe(autoSubscribeSDKEnabled(opts.AutoSubscribe)),
	}
	if c.token != "" {
		room, err := lksdk.ConnectToRoomWithToken(c.url, c.token, cb, connectOptions...)
		if err != nil {
			return err
		}
		c.Room = room
		c.participantsAvailable(remoteParticipantsAsViews(room.GetRemoteParticipants()))
		c.applyAutoSubscribeOptions(opts.AutoSubscribe)
		logger.Logger.Infow("Connected to room", "room", c.Job.Room.Name)
		return nil
	}

	room, err := lksdk.ConnectToRoom(c.url, c.connectInfo(), cb, connectOptions...)
	if err != nil {
		return err
	}
	c.Room = room
	c.participantsAvailable(remoteParticipantsAsViews(room.GetRemoteParticipants()))
	c.applyAutoSubscribeOptions(opts.AutoSubscribe)
	logger.Logger.Infow("Connected to room", "room", c.Job.Room.Name)
	return nil
}

func normalizeConnectOptions(options ...ConnectOptions) ConnectOptions {
	opts := ConnectOptions{AutoSubscribe: AutoSubscribeSubscribeAll}
	if len(options) > 0 {
		opts = options[0]
	}
	if opts.AutoSubscribe == "" {
		opts.AutoSubscribe = AutoSubscribeSubscribeAll
	}
	return opts
}

func autoSubscribeSDKEnabled(mode AutoSubscribe) bool {
	return normalizeConnectOptions(ConnectOptions{AutoSubscribe: mode}).AutoSubscribe == AutoSubscribeSubscribeAll
}

func shouldAutoSubscribeTrack(mode AutoSubscribe, kind lksdk.TrackKind) bool {
	switch normalizeConnectOptions(ConnectOptions{AutoSubscribe: mode}).AutoSubscribe {
	case AutoSubscribeAudioOnly:
		return kind == lksdk.TrackKindAudio
	case AutoSubscribeVideoOnly:
		return kind == lksdk.TrackKindVideo
	default:
		return false
	}
}

func (c *JobContext) applyAutoSubscribeOptions(mode AutoSubscribe) {
	if c.Room == nil {
		return
	}
	for _, participant := range c.Room.GetRemoteParticipants() {
		for _, publication := range participant.TrackPublications() {
			remotePublication, ok := publication.(*lksdk.RemoteTrackPublication)
			if ok && shouldAutoSubscribeTrack(mode, remotePublication.Kind()) {
				if err := remotePublication.SetSubscribed(true); err != nil {
					logger.Logger.Warnw("failed to subscribe remote track", err, "trackSid", remotePublication.SID())
				}
			}
		}
	}
}

func (c *JobContext) roomCallbackWithEntrypoints(cb *lksdk.RoomCallback, autoSubscribe AutoSubscribe) *lksdk.RoomCallback {
	wrapped := lksdk.NewRoomCallback()
	wrapped.Merge(cb)
	onParticipantConnected := wrapped.OnParticipantConnected
	wrapped.OnParticipantConnected = func(participant *lksdk.RemoteParticipant) {
		if onParticipantConnected != nil {
			onParticipantConnected(participant)
		}
		if participant != nil {
			c.participantAvailable(participant)
		}
	}
	onTrackPublished := wrapped.OnTrackPublished
	wrapped.OnTrackPublished = func(publication *lksdk.RemoteTrackPublication, participant *lksdk.RemoteParticipant) {
		if onTrackPublished != nil {
			onTrackPublished(publication, participant)
		}
		if publication != nil && shouldAutoSubscribeTrack(autoSubscribe, publication.Kind()) {
			if err := publication.SetSubscribed(true); err != nil {
				logger.Logger.Warnw("failed to subscribe published remote track", err, "trackSid", publication.SID())
			}
		}
	}
	return wrapped
}

func (c *JobContext) participantAvailable(participant remoteParticipantView) {
	info := participantInfoFromRemoteParticipant(participant)
	if info == nil {
		return
	}
	c.rememberAvailableParticipant(info)
	c.scheduleParticipantEntrypoints(info)
}

func (c *JobContext) rememberAvailableParticipant(info *livekit.ParticipantInfo) {
	for i, participant := range c.availableParticipants {
		if participant.Identity == info.Identity {
			c.availableParticipants[i] = info
			return
		}
	}
	c.availableParticipants = append(c.availableParticipants, info)
}

func (c *JobContext) participantsAvailable(participants []remoteParticipantView) {
	for _, participant := range participants {
		c.participantAvailable(participant)
	}
}

func remoteParticipantsAsViews(participants []*lksdk.RemoteParticipant) []remoteParticipantView {
	views := make([]remoteParticipantView, 0, len(participants))
	for _, participant := range participants {
		if participant != nil {
			views = append(views, participant)
		}
	}
	return views
}

func participantInfoFromRemoteParticipant(participant remoteParticipantView) *livekit.ParticipantInfo {
	if participant == nil {
		return nil
	}
	return &livekit.ParticipantInfo{
		Sid:        participant.SID(),
		Identity:   participant.Identity(),
		Name:       participant.Name(),
		Kind:       livekit.ParticipantInfo_Kind(participant.Kind()),
		Metadata:   participant.Metadata(),
		Attributes: maps.Clone(participant.Attributes()),
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

func (c *JobContext) AddParticipantEntrypoint(entrypoint ParticipantEntrypoint, kinds ...livekit.ParticipantInfo_Kind) error {
	if entrypoint == nil {
		return fmt.Errorf("participant entrypoint must not be nil")
	}
	for _, registered := range c.participantEntrypoints {
		if reflect.ValueOf(registered.entrypoint).Pointer() == reflect.ValueOf(entrypoint).Pointer() {
			return fmt.Errorf("participant entrypoints cannot be added more than once")
		}
	}
	if len(kinds) == 0 {
		kinds = defaultParticipantEntrypointKinds
	}
	registration := participantEntrypointRegistration{
		entrypoint: entrypoint,
		kinds:      append([]livekit.ParticipantInfo_Kind(nil), kinds...),
	}
	c.participantEntrypoints = append(c.participantEntrypoints, registration)
	c.scheduleParticipantEntrypointForExistingParticipants(registration)
	return nil
}

func (c *JobContext) scheduleParticipantEntrypointForExistingParticipants(registration participantEntrypointRegistration) {
	for _, participant := range c.availableParticipants {
		if !participantEntrypointMatchesKind(registration.kinds, participant.Kind) {
			continue
		}
		c.scheduleParticipantEntrypoint(registration, participant)
	}
}

func (c *JobContext) WaitForParticipant(
	ctx context.Context,
	identity string,
	kinds ...livekit.ParticipantInfo_Kind,
) (*lksdk.RemoteParticipant, error) {
	if err := c.ensureRoomConnected(ctx); err != nil {
		return nil, err
	}
	return utils.WaitForParticipant(ctx, c.Room, identity, defaultParticipantWaitKinds(kinds)...)
}

func (c *JobContext) WaitForAgent(
	ctx context.Context,
	agentName ...string,
) (*lksdk.RemoteParticipant, error) {
	if err := c.ensureRoomConnected(ctx); err != nil {
		return nil, err
	}
	return utils.WaitForAgent(ctx, c.Room, agentName...)
}

func (c *JobContext) WaitForTrackPublication(
	ctx context.Context,
	identity string,
	kinds ...livekit.TrackType,
) (*lksdk.RemoteTrackPublication, error) {
	if err := c.ensureRoomConnected(ctx); err != nil {
		return nil, err
	}
	return utils.WaitForTrackPublication(ctx, c.Room, identity, kinds...)
}

func (c *JobContext) WaitForTrackPublicationWithOptions(
	ctx context.Context,
	options utils.TrackPublicationWaitOptions,
) (*lksdk.RemoteTrackPublication, error) {
	if err := c.ensureRoomConnected(ctx); err != nil {
		return nil, err
	}
	return utils.WaitForTrackPublicationWithOptions(ctx, c.Room, options)
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
	return utils.WaitForParticipantAttribute(ctx, c.Room, identity, attribute, value)
}

func (c *JobContext) ensureRoomConnected(ctx context.Context) error {
	if c.Room != nil {
		return nil
	}
	return c.Connect(ctx, nil)
}

func defaultParticipantWaitKinds(kinds []livekit.ParticipantInfo_Kind) []livekit.ParticipantInfo_Kind {
	if len(kinds) > 0 {
		return kinds
	}
	return defaultParticipantEntrypointKinds
}

func (c *JobContext) scheduleParticipantEntrypoints(participant *livekit.ParticipantInfo) {
	if participant == nil {
		return
	}
	for _, registered := range c.participantEntrypoints {
		if !participantEntrypointMatchesKind(registered.kinds, participant.Kind) {
			continue
		}
		c.scheduleParticipantEntrypoint(registered, participant)
	}
}

func (c *JobContext) scheduleParticipantEntrypoint(registration participantEntrypointRegistration, participant *livekit.ParticipantInfo) {
	if participant == nil {
		return
	}
	key := participantEntrypointTaskKey{
		identity:   participant.Identity,
		entrypoint: reflect.ValueOf(registration.entrypoint).Pointer(),
	}
	c.participantTasksMu.Lock()
	if c.participantTasks == nil {
		c.participantTasks = make(map[participantEntrypointTaskKey]struct{})
	}
	if _, ok := c.participantTasks[key]; ok {
		logger.Logger.Warnw("participant entrypoint already running for participant", nil, "participant", participant.Identity)
	}
	c.participantTasks[key] = struct{}{}
	c.participantTasksMu.Unlock()

	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Logger.Errorw("Participant entrypoint panicked", fmt.Errorf("%v", recovered), "participant", participant.Identity)
			}
			c.participantTasksMu.Lock()
			delete(c.participantTasks, key)
			c.participantTasksMu.Unlock()
		}()
		registration.entrypoint(c, participant)
	}()
}

func participantEntrypointMatchesKind(kinds []livekit.ParticipantInfo_Kind, kind livekit.ParticipantInfo_Kind) bool {
	if len(kinds) == 0 {
		return true
	}
	for _, allowed := range kinds {
		if allowed == kind {
			return true
		}
	}
	return false
}

func (c *JobContext) Shutdown(reasons ...string) {
	reason := ""
	if len(reasons) > 0 {
		reason = reasons[0]
	}
	c.shutdownOnce.Do(func() {
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
	})
}

// DeleteRoom deletes the room and disconnects all participants.
func (c *JobContext) DeleteRoom(ctx context.Context, roomName string) (*livekit.DeleteRoomResponse, error) {
	if c.IsFakeJob() {
		logger.Logger.Warnw("job context DeleteRoom is skipped for fake jobs", nil)
		return &livekit.DeleteRoomResponse{}, nil
	}
	if roomName == "" {
		roomName = c.Job.Room.Name
	}
	return c.API().RoomService.DeleteRoom(ctx, &livekit.DeleteRoomRequest{
		Room: roomName,
	})
}

// AddSIPParticipant adds a SIP participant to the room.
func (c *JobContext) AddSIPParticipant(ctx context.Context, callTo string, trunkID string, identity string, names ...string) (*livekit.SIPParticipantInfo, error) {
	if c.IsFakeJob() {
		logger.Logger.Warnw("job context AddSIPParticipant is skipped for fake jobs", nil)
		return &livekit.SIPParticipantInfo{}, nil
	}
	name := ""
	if len(names) > 0 {
		name = names[0]
	}
	return c.API().SIP.CreateSIPParticipant(ctx, c.createSIPParticipantRequest(callTo, trunkID, identity, name))
}

func (c *JobContext) createSIPParticipantRequest(callTo string, trunkID string, identity string, name string) *livekit.CreateSIPParticipantRequest {
	if name == "" {
		name = defaultSIPParticipantName
	}
	return &livekit.CreateSIPParticipantRequest{
		RoomName:            c.Job.Room.Name,
		ParticipantIdentity: identity,
		ParticipantName:     name,
		SipTrunkId:          trunkID,
		SipCallTo:           callTo,
	}
}

// TransferSIPParticipant transfers a SIP participant to another number.
func (c *JobContext) TransferSIPParticipant(ctx context.Context, identity string, transferTo string, playDialtones ...bool) error {
	return c.TransferSIPParticipantByParticipant(ctx, identity, transferTo, playDialtones...)
}

func (c *JobContext) TransferSIPParticipantByParticipant(ctx context.Context, participant any, transferTo string, playDialtones ...bool) error {
	if c.IsFakeJob() {
		logger.Logger.Warnw("job context TransferSIPParticipant is skipped for fake jobs", nil)
		return nil
	}
	identity, err := transferSIPParticipantIdentity(participant)
	if err != nil {
		return err
	}
	playDialtone := false
	if len(playDialtones) > 0 {
		playDialtone = playDialtones[0]
	}
	_, err = c.API().SIP.TransferSIPParticipant(ctx, c.transferSIPParticipantRequest(identity, transferTo, playDialtone))
	return err
}

func transferSIPParticipantIdentity(participant any) (string, error) {
	switch p := participant.(type) {
	case string:
		return p, nil
	case remoteParticipantView:
		if p.Kind() != lksdk.ParticipantSIP {
			return "", fmt.Errorf("participant must be a SIP participant")
		}
		return p.Identity(), nil
	default:
		return "", fmt.Errorf("participant must be a SIP participant or identity string")
	}
}

func (c *JobContext) transferSIPParticipantRequest(identity string, transferTo string, playDialtone bool) *livekit.TransferSIPParticipantRequest {
	return &livekit.TransferSIPParticipantRequest{
		ParticipantIdentity: identity,
		RoomName:            c.Job.Room.Name,
		TransferTo:          transferTo,
		PlayDialtone:        playDialtone,
	}
}
