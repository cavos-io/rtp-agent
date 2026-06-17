package livekit

import (
	"github.com/cavos-io/rtp-agent/core/agent"
	lkprotocol "github.com/livekit/protocol/livekit"
)

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

func JobAvatarStartInfo(job *lkprotocol.Job, url string, token string, agentIdentity string) agent.AvatarStartInfo {
	opts := AvatarStartInfoOptions{
		URL:           url,
		Token:         token,
		AgentIdentity: agentIdentity,
	}
	if job != nil && job.Room != nil {
		opts.RoomName = job.Room.Name
	}
	return AvatarStartInfo(opts)
}
