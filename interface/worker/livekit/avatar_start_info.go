package livekit

import "github.com/cavos-io/rtp-agent/core/agent"

type AvatarStartInfoOptions struct {
	URL           string
	Token         string
	RoomName      string
	AgentIdentity string
}

func AvatarStartInfo(opts AvatarStartInfoOptions) agent.AvatarStartInfo {
	return agent.AvatarStartInfo{
		LiveKitURL:    opts.URL,
		LiveKitToken:  opts.Token,
		RoomName:      opts.RoomName,
		AgentIdentity: opts.AgentIdentity,
	}
}
