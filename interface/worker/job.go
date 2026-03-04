package worker

import (
	"context"

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

type JobRequest struct {
	Job *livekit.Job

	acceptFnc func(JobAcceptArguments) error
	rejectFnc func() error
}

func (r *JobRequest) Accept(args JobAcceptArguments) error {
	if r.acceptFnc != nil {
		return r.acceptFnc(args)
	}
	return nil
}

func (r *JobRequest) Reject() error {
	if r.rejectFnc != nil {
		return r.rejectFnc()
	}
	return nil
}

type JobContext struct {
	Job    *livekit.Job
	Room   *lksdk.Room
	Report *agent.SessionReport

	apiKey    string
	apiSecret string
	url       string
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

func (c *JobContext) Connect(ctx context.Context, cb *lksdk.RoomCallback) error {
	room, err := lksdk.ConnectToRoom(c.url, lksdk.ConnectInfo{
		APIKey:              c.apiKey,
		APISecret:           c.apiSecret,
		RoomName:            c.Job.Room.Name,
		ParticipantIdentity: "agent-" + c.Job.Id[:8],
	}, cb)
	if err != nil {
		return err
	}
	c.Room = room
	logger.Logger.Infow("Connected to room", "room", c.Job.Room.Name)
	return nil
}

func (c *JobContext) Shutdown(reason string) {
	if c.Room != nil {
		c.Room.Disconnect()
	}
}

// DeleteRoom deletes the room and disconnects all participants.
func (c *JobContext) DeleteRoom(ctx context.Context, roomName string) (*livekit.DeleteRoomResponse, error) {
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
	client := lksdk.NewSIPClient(c.url, c.apiKey, c.apiSecret)
	_, err := client.TransferSIPParticipant(ctx, &livekit.TransferSIPParticipantRequest{
		ParticipantIdentity: identity,
		RoomName:            c.Job.Room.Name,
		TransferTo:          transferTo,
		PlayDialtone:        playDialtone,
	})
	return err
}
