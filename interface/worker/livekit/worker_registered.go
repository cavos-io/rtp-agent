package livekit

import lkprotocol "github.com/livekit/protocol/livekit"

type WorkerRegisteredHandler func(workerID string, serverInfo *lkprotocol.ServerInfo)
