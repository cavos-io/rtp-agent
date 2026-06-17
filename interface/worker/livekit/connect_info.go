package livekit

import (
	"context"

	lkprotocol "github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

type ConnectInfoOptions struct {
	APIKey                string
	APISecret             string
	RoomName              string
	ParticipantName       string
	ParticipantIdentity   string
	ParticipantMetadata   string
	ParticipantAttributes map[string]string
}

func ConnectInfo(opts ConnectInfoOptions) lksdk.ConnectInfo {
	return lksdk.ConnectInfo{
		APIKey:                opts.APIKey,
		APISecret:             opts.APISecret,
		RoomName:              opts.RoomName,
		ParticipantName:       opts.ParticipantName,
		ParticipantIdentity:   opts.ParticipantIdentity,
		ParticipantKind:       lksdk.ParticipantAgent,
		ParticipantMetadata:   opts.ParticipantMetadata,
		ParticipantAttributes: opts.ParticipantAttributes,
	}
}

func JobConnectInfo(job *lkprotocol.Job, opts ConnectInfoOptions) lksdk.ConnectInfo {
	opts.RoomName = JobRoomName(job)
	return ConnectInfo(opts)
}

type RoomConnector struct {
	ConnectWithToken func(string, string, *lksdk.RoomCallback, ...lksdk.ConnectOption) (*lksdk.Room, error)
	Connect          func(string, lksdk.ConnectInfo, *lksdk.RoomCallback, ...lksdk.ConnectOption) (*lksdk.Room, error)
	JoinWithToken    func(context.Context, *lksdk.Room, string, string, ...lksdk.ConnectOption) error
	Join             func(context.Context, *lksdk.Room, string, lksdk.ConnectInfo, ...lksdk.ConnectOption) error
}

type RoomConnectOptions struct {
	URL           string
	Token         string
	Job           *lkprotocol.Job
	APIKey        string
	APISecret     string
	Accept        ConnectInfoOptions
	AutoSubscribe string
	Callback      *lksdk.RoomCallback
	Connector     RoomConnector
}

func ConnectRoom(_ context.Context, opts RoomConnectOptions) (*lksdk.Room, error) {
	connector := opts.Connector
	if connector.ConnectWithToken == nil {
		connector.ConnectWithToken = lksdk.ConnectToRoomWithToken
	}
	if connector.Connect == nil {
		connector.Connect = lksdk.ConnectToRoom
	}
	connectOptions := ConnectOptionsForAutoSubscribe(opts.AutoSubscribe)
	if opts.Token != "" {
		return connector.ConnectWithToken(opts.URL, opts.Token, opts.Callback, connectOptions...)
	}
	infoOpts := opts.Accept
	infoOpts.APIKey = opts.APIKey
	infoOpts.APISecret = opts.APISecret
	info := JobConnectInfo(opts.Job, infoOpts)
	return connector.Connect(opts.URL, info, opts.Callback, connectOptions...)
}

type PreparedRoomConnectOptions struct {
	Room          *lksdk.Room
	URL           string
	Token         string
	Job           *lkprotocol.Job
	APIKey        string
	APISecret     string
	Accept        ConnectInfoOptions
	AutoSubscribe string
	Connector     RoomConnector
}

func JoinPreparedRoom(ctx context.Context, opts PreparedRoomConnectOptions) error {
	connector := opts.Connector
	if connector.JoinWithToken == nil {
		connector.JoinWithToken = func(ctx context.Context, room *lksdk.Room, url string, token string, options ...lksdk.ConnectOption) error {
			return room.JoinWithContextAndToken(ctx, url, token, options...)
		}
	}
	if connector.Join == nil {
		connector.Join = func(ctx context.Context, room *lksdk.Room, url string, info lksdk.ConnectInfo, options ...lksdk.ConnectOption) error {
			return room.JoinWithContext(ctx, url, info, options...)
		}
	}
	connectOptions := ConnectOptionsForAutoSubscribe(opts.AutoSubscribe)
	if opts.Token != "" {
		return connector.JoinWithToken(ctx, opts.Room, opts.URL, opts.Token, connectOptions...)
	}
	infoOpts := opts.Accept
	infoOpts.APIKey = opts.APIKey
	infoOpts.APISecret = opts.APISecret
	info := JobConnectInfo(opts.Job, infoOpts)
	return connector.Join(ctx, opts.Room, opts.URL, info, connectOptions...)
}

func ConnectOptionsForAutoSubscribe(mode string) []lksdk.ConnectOption {
	return []lksdk.ConnectOption{
		lksdk.WithAutoSubscribe(AutoSubscribeSDKEnabled(mode)),
	}
}
