package livekit

import lkprotocol "github.com/livekit/protocol/livekit"

type ServerInfo = lkprotocol.ServerInfo

type WorkerRegisteredHandler func(workerID string, serverInfo *ServerInfo)

type WorkerRegisteredEvent struct {
	WorkerID   string
	ServerInfo *ServerInfo
}

func WorkerRegisteredEventFromRegisterDispatch(info RegisterMessageInfo) WorkerRegisteredEvent {
	return WorkerRegisteredEvent(info)
}
