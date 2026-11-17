//go:build !nativeinprocess && nativeroomintegration

package main

import (
	"context"
	"errors"
	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/interface/worker"
	wl "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lk "github.com/livekit/server-sdk-go/v2"
	"testing"
)

func startChatTestSession(context.Context, *worker.JobContext, *agent.AgentSession, *worker.AgentServer, nativeReceiveConfig, chan<- wl.TextInputEvent) error {
	return errors.New("chat test requires nativeinprocess")
}
func verifyNativeChat(t *testing.T, _ context.Context, _ *lk.Room, _ <-chan wl.TextInputEvent, _ string) {
	t.Fatal("chat test requires nativeinprocess")
}
