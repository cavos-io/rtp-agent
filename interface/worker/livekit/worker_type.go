package livekit

import lkprotocol "github.com/livekit/protocol/livekit"

type WorkerType string

const (
	WorkerTypeRoom      WorkerType = "room"
	WorkerTypePublisher WorkerType = "publisher"
)

func JobTypeForWorkerType(workerType string) lkprotocol.JobType {
	switch WorkerType(workerType) {
	case WorkerTypePublisher:
		return lkprotocol.JobType_JT_PUBLISHER
	default:
		return lkprotocol.JobType_JT_ROOM
	}
}

func JobTypeNameForWorkerType(workerType string) string {
	return lkprotocol.JobType_name[int32(JobTypeForWorkerType(workerType))]
}
