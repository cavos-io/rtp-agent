package worker

import (
	"context"
	"fmt"
	"sync"

	"github.com/cavos-io/conversation-worker/core/agent"
	"github.com/cavos-io/conversation-worker/library/logger"
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
	Job               *livekit.Job
	Room              *lksdk.Room
	Report            *agent.SessionReport
	AcceptArguments   JobAcceptArguments
	WorkerID          string
	shutdownCallbacks []func(string)
	shutdownOnce      sync.Once

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
	return agentIdentityForJobID(c.Job.Id)
}

func (c *JobContext) IsFakeJob() bool {
	return c.fakeJob
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
	return client.CreateSIPParticipant(ctx, &livekit.CreateSIPParticipantRequest{
		RoomName:            c.Job.Room.Name,
		ParticipantIdentity: identity,
		ParticipantName:     name,
		SipTrunkId:          trunkID,
		SipCallTo:           callTo,
	})
}

// TransferSIPParticipant transfers a SIP participant to another number.
func (c *JobContext) TransferSIPParticipant(ctx context.Context, identity string, transferTo string, playDialtone bool) error {
	if c.IsFakeJob() {
		logger.Logger.Warnw("job context TransferSIPParticipant is skipped for fake jobs", nil)
		return nil
	}
	client := lksdk.NewSIPClient(c.url, c.apiKey, c.apiSecret)
	_, err := client.TransferSIPParticipant(ctx, &livekit.TransferSIPParticipantRequest{
		ParticipantIdentity: identity,
		RoomName:            c.Job.Room.Name,
		TransferTo:          transferTo,
		PlayDialtone:        playDialtone,
	})
	return err
}
