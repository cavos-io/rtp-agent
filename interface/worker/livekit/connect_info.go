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

func ConnectOptionsForAutoSubscribe(mode string) []lksdk.ConnectOption {
	return []lksdk.ConnectOption{
		lksdk.WithAutoSubscribe(AutoSubscribeSDKEnabled(mode)),
	}
}
