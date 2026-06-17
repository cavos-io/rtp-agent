package livekit_test

import (
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lkprotocol "github.com/livekit/protocol/livekit"
)

func TestWorkerRegisteredHandlerReceivesLiveKitServerInfo(t *testing.T) {
	serverInfo := &lkprotocol.ServerInfo{}
	var gotWorkerID string
	var gotServerInfo *lkprotocol.ServerInfo

	var handler workerlivekit.WorkerRegisteredHandler = func(workerID string, info *lkprotocol.ServerInfo) {
		gotWorkerID = workerID
		gotServerInfo = info
	}

	handler("worker-a", serverInfo)

	if gotWorkerID != "worker-a" {
		t.Fatalf("workerID = %q, want worker-a", gotWorkerID)
	}
	if gotServerInfo != serverInfo {
		t.Fatalf("serverInfo = %p, want %p", gotServerInfo, serverInfo)
	}
}
