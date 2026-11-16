//go:build !nativeinprocess

package main

import (
	"context"
	"errors"
	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/interface/worker"
)

const inProcessAvailable = false

func startInProcessSession(context.Context, *worker.JobContext, *agent.AgentSession, *worker.AgentServer, nativeReceiveConfig) error {
	return errors.New("in-process native not compiled")
}
func closeInProcessRuntime() {}
