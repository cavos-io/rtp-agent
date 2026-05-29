package worker

import (
	"context"
	"path/filepath"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/library/logger"
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
	rejectFnc func(terminate bool) error
}

func (r *JobRequest) Accept(args JobAcceptArguments) error {
	if r.acceptFnc != nil {
		return r.acceptFnc(args)
	}
	return nil
}

// Reject rejects the job. terminate defaults to true, matching Python's reject(terminate=True).
func (r *JobRequest) Reject(terminate ...bool) error {
	t := true
	if len(terminate) > 0 {
		t = terminate[0]
	}
	if r.rejectFnc != nil {
		return r.rejectFnc(t)
	}
	return nil
}

type JobContext struct {
	Job    *livekit.Job
	Room   *Room
	Report *agent.SessionReport

	APIKey           string
	APISecret        string
	URL              string
	SessionDirectory string
}

func NewJobContext(job *livekit.Job, url string, apiKey string, apiSecret string) *JobContext {
	return &JobContext{
		Job:              job,
		Room:             NewRoom(),
		URL:              url,
		APIKey:           apiKey,
		APISecret:        apiSecret,
		Report:           agent.NewSessionReport(),
		SessionDirectory: filepath.Join("recordings", job.Id),
	}
}

func (c *JobContext) Connect(ctx context.Context) error {
	err := c.Room.JoinWithContext(ctx, c.URL, lksdk.ConnectInfo{
		APIKey:              c.APIKey,
		APISecret:           c.APISecret,
		RoomName:            c.Job.Room.Name,
		ParticipantIdentity: "agent-" + c.Job.Id[:8],
		ParticipantName:     "Cavos Agent",
		ParticipantKind:     lksdk.ParticipantAgent,
	})
	if err != nil {
		return err
	}
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
	client := lksdk.NewRoomServiceClient(c.URL, c.APIKey, c.APISecret)
	return client.DeleteRoom(ctx, &livekit.DeleteRoomRequest{
		Room: roomName,
	})
}

// AddSIPParticipant adds a SIP participant to the room.
func (c *JobContext) AddSIPParticipant(ctx context.Context, callTo string, trunkID string, identity string, name string) (*livekit.SIPParticipantInfo, error) {
	client := lksdk.NewSIPClient(c.URL, c.APIKey, c.APISecret)
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
	client := lksdk.NewSIPClient(c.URL, c.APIKey, c.APISecret)
	_, err := client.TransferSIPParticipant(ctx, &livekit.TransferSIPParticipantRequest{
		ParticipantIdentity: identity,
		RoomName:            c.Job.Room.Name,
		TransferTo:          transferTo,
		PlayDialtone:        playDialtone,
	})
	return err
}
