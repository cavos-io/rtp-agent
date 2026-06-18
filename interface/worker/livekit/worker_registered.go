package livekit

import lkprotocol "github.com/livekit/protocol/livekit"

type WorkerRegisteredHandler func(workerID string, serverInfo *lkprotocol.ServerInfo)

type WorkerRegisteredEvent struct {
	WorkerID   string
	ServerInfo *lkprotocol.ServerInfo
}

func WorkerRegisteredEventFromRegisterDispatch(info RegisterMessageInfo) WorkerRegisteredEvent {
	return WorkerRegisteredEvent(info)
}
