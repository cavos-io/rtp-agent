package livekit

import lksdk "github.com/livekit/server-sdk-go/v2"

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
