package worker

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"sync"

	"github.com/cavos-io/conversation-worker/core/agent"
	"github.com/cavos-io/conversation-worker/library/logger"
	"github.com/cavos-io/conversation-worker/library/utils"
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

type ParticipantEntrypoint func(*JobContext, *livekit.ParticipantInfo)

type participantEntrypointRegistration struct {
	entrypoint ParticipantEntrypoint
	kinds      []livekit.ParticipantInfo_Kind
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

func (r *JobRequest) Accept(args JobAcceptArguments) error {
	if args.Identity == "" && r.Job != nil {
		args.Identity = agentIdentityForJobID(r.Job.Id)
	}
	if r.acceptFnc != nil {
		return r.acceptFnc(args)
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
	AcceptArguments        JobAcceptArguments
	WorkerID               string
	shutdownCallbacks      []func(string)
	shutdownOnce           sync.Once
	participantEntrypoints []participantEntrypointRegistration

	apiKey    string
	apiSecret string
	url       string
	token     string
	fakeJob   bool
}

func NewJobContext(job *livekit.Job, url string, apiKey string, apiSecret string) *JobContext {
	return &JobContext{
		Job:       job,
		url:       url,
		apiKey:    apiKey,
		apiSecret: apiSecret,
		Report:    agent.NewSessionReport(),
	}
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

func (c *JobContext) Connect(ctx context.Context, cb *lksdk.RoomCallback) error {
	if c.Room != nil {
		return nil
	}
	cb = c.roomCallbackWithEntrypoints(cb)
	if c.token != "" {
		room, err := lksdk.ConnectToRoomWithToken(c.url, c.token, cb)
		if err != nil {
			return err
		}
		c.Room = room
		logger.Logger.Infow("Connected to room", "room", c.Job.Room.Name)
		return nil
	}

	room, err := lksdk.ConnectToRoom(c.url, c.connectInfo(), cb)
	if err != nil {
		return err
	}
	c.Room = room
	logger.Logger.Infow("Connected to room", "room", c.Job.Room.Name)
	return nil
}

func (c *JobContext) roomCallbackWithEntrypoints(cb *lksdk.RoomCallback) *lksdk.RoomCallback {
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
	return wrapped
}

func (c *JobContext) participantAvailable(participant remoteParticipantView) {
	c.runParticipantEntrypoints(participantInfoFromRemoteParticipant(participant))
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
	c.participantEntrypoints = append(c.participantEntrypoints, participantEntrypointRegistration{
		entrypoint: entrypoint,
		kinds:      append([]livekit.ParticipantInfo_Kind(nil), kinds...),
	})
	return nil
}

func (c *JobContext) WaitForParticipant(
	ctx context.Context,
	identity string,
	kinds ...livekit.ParticipantInfo_Kind,
) (*lksdk.RemoteParticipant, error) {
	if c.Room == nil {
		if err := c.Connect(ctx, nil); err != nil {
			return nil, err
		}
	}
	return utils.WaitForParticipant(ctx, c.Room, identity, defaultParticipantWaitKinds(kinds)...)
}

func defaultParticipantWaitKinds(kinds []livekit.ParticipantInfo_Kind) []livekit.ParticipantInfo_Kind {
	if len(kinds) > 0 {
		return kinds
	}
	return defaultParticipantEntrypointKinds
}

func (c *JobContext) runParticipantEntrypoints(participant *livekit.ParticipantInfo) {
	if participant == nil {
		return
	}
	for _, registered := range c.participantEntrypoints {
		if !participantEntrypointMatchesKind(registered.kinds, participant.Kind) {
			continue
		}
		registered.entrypoint(c, participant)
	}
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

func (c *JobContext) Shutdown(reason string) {
	c.shutdownOnce.Do(func() {
		for _, callback := range c.shutdownCallbacks {
			callback(reason)
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
	client := lksdk.NewRoomServiceClient(c.url, c.apiKey, c.apiSecret)
	return client.DeleteRoom(ctx, &livekit.DeleteRoomRequest{
		Room: roomName,
	})
}

// AddSIPParticipant adds a SIP participant to the room.
func (c *JobContext) AddSIPParticipant(ctx context.Context, callTo string, trunkID string, identity string, name string) (*livekit.SIPParticipantInfo, error) {
	if c.IsFakeJob() {
		logger.Logger.Warnw("job context AddSIPParticipant is skipped for fake jobs", nil)
		return &livekit.SIPParticipantInfo{}, nil
	}
	client := lksdk.NewSIPClient(c.url, c.apiKey, c.apiSecret)
	return client.CreateSIPParticipant(ctx, c.createSIPParticipantRequest(callTo, trunkID, identity, name))
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
func (c *JobContext) TransferSIPParticipant(ctx context.Context, identity string, transferTo string, playDialtone bool) error {
	return c.TransferSIPParticipantByParticipant(ctx, identity, transferTo, playDialtone)
}

func (c *JobContext) TransferSIPParticipantByParticipant(ctx context.Context, participant any, transferTo string, playDialtone bool) error {
	if c.IsFakeJob() {
		logger.Logger.Warnw("job context TransferSIPParticipant is skipped for fake jobs", nil)
		return nil
	}
	identity, err := transferSIPParticipantIdentity(participant)
	if err != nil {
		return err
	}
	client := lksdk.NewSIPClient(c.url, c.apiKey, c.apiSecret)
	_, err = client.TransferSIPParticipant(ctx, c.transferSIPParticipantRequest(identity, transferTo, playDialtone))
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
