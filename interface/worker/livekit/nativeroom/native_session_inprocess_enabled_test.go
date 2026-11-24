//go:build (nativeroomintegration || nativereceiveintegration) && nativeinprocess

package nativeroom_test

import (
	"context"
	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/interface/worker"
	"github.com/cavos-io/rtp-agent/interface/worker/livekit/nativeroom"
	"os"
)

func startInProcessSession(ctx context.Context, job *worker.JobContext, session *agent.AgentSession, server *worker.AgentServer, cfg nativeReceiveConfig) error {
	transport := nativeroom.NewInProcess(job, session, nativeroom.Config{Library: cfg.library, Participant: os.Getenv("RTP_AGENT_NATIVE_PARTICIPANT")}, server.Options.APIKey, server.Options.APISecret)
	return job.StartSession(ctx, session, worker.StartSessionOptions{Transport: transport, RoomOptions: worker.RoomOptions{DisablePreConnectAudio: true}})
}
