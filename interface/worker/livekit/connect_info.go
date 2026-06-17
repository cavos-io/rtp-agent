package livekit

import (
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

func ConnectOptionsForAutoSubscribe(mode string) []lksdk.ConnectOption {
	return []lksdk.ConnectOption{
		lksdk.WithAutoSubscribe(AutoSubscribeSDKEnabled(mode)),
	}
}
