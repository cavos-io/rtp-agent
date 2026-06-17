package livekit

import lkprotocol "github.com/livekit/protocol/livekit"

func JobTypeForWorkerType(workerType string) lkprotocol.JobType {
	switch workerType {
	case "publisher":
		return lkprotocol.JobType_JT_PUBLISHER
	default:
		return lkprotocol.JobType_JT_ROOM
	}
}
