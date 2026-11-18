//go:build integration

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	model "github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/examples/starter/internal/nativereceive"
	"github.com/cavos-io/rtp-agent/interface/worker"
	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	"github.com/cavos-io/rtp-agent/interface/worker/livekit/nativeroom"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/livekit/protocol/auth"
	lkproto "github.com/livekit/protocol/livekit"
)

func startStarterSession(ctx context.Context, job *worker.JobContext, session *agent.AgentSession, server *worker.AgentServer, config nativeReceiveConfig) error {
	if config.inProcess {
		return startInProcessSession(ctx, job, session, server, config)
	}
	if config.primary {
		transport := nativeroom.New(job, session, nativeroom.Config{Binary: config.binary, Library: config.library, Participant: os.Getenv("RTP_AGENT_NATIVE_PARTICIPANT")}, server.Options.APIKey, server.Options.APISecret)
		return job.StartSession(ctx, session, worker.StartSessionOptions{Transport: transport})
	}
	if !config.enabled {
		return job.StartSession(ctx, session)
	}
	if server.Options.APIKey == "" || server.Options.APISecret == "" {
		return errors.New("experimental native receive needs LiveKit API key/secret to mint its receive-only participant token")
	}
	identity := os.Getenv("RTP_AGENT_NATIVE_PARTICIPANT")
	if err := job.StartSession(ctx, session, worker.StartSessionOptions{
		RoomOptions:    worker.RoomOptions{DisableAudioInput: true, ParticipantIdentity: identity},
		ConnectOptions: []worker.ConnectOptions{{AutoSubscribe: workerlivekit.AutoSubscribeSubscribeNone}},
	}); err != nil {
		return err
	}
	startup, cancelStartup := context.WithTimeout(ctx, 30*time.Second)
	defer cancelStartup()
	participant, err := job.WaitForParticipant(startup, identity)
	if err != nil {
		return fmt.Errorf("native receive caller: %w", err)
	}
	publication, err := job.WaitForTrackPublication(startup, participant.Identity(), lkproto.TrackType_AUDIO)
	if err != nil {
		return fmt.Errorf("native receive audio publication: %w", err)
	}
	no, yes := false, true
	token, err := auth.NewAccessToken(server.Options.APIKey, server.Options.APISecret).
		SetIdentity(job.LocalParticipantIdentity() + "-native-receive").SetKind(lkproto.ParticipantInfo_AGENT).SetValidFor(time.Hour).
		SetVideoGrant(&auth.VideoGrant{RoomJoin: true, Room: job.Room.Name(), Hidden: true, CanPublish: &no, CanPublishData: &no, CanSubscribe: &yes}).ToJWT()
	if err != nil {
		return errors.New("mint native receive participant token")
	}
	receiveCtx, cancel := context.WithCancel(ctx)
	ready := make(chan struct{}, 1)
	done := make(chan struct{})
	var receiveErr error
	var samples atomic.Uint64
	stats := new(nativereceive.Stats)
	go func() {
		defer close(done)
		receiveErr = nativereceive.Run(receiveCtx, nativereceive.Options{Stats: stats, Binary: config.binary, Library: config.library, URL: job.AvatarStartInfo().LiveKitURL, Token: token, Participant: participant.Identity(), TrackSID: publication.SID()}, ready, func(c context.Context, frame *model.AudioFrame) {
			if c.Err() != nil {
				return
			}
			if samples.Add(uint64(frame.SamplesPerChannel)) == uint64(frame.SamplesPerChannel) {
				logger.Logger.Infow("experimental native receive first PCM", "participant", participant.Identity(), "track", publication.SID())
			}
			if session.InputAudioMuted() {
				return
			}
			if rio, ok := job.PrimaryRoomIO().(*workerlivekit.RoomIO); ok && rio.Recorder != nil {
				rio.Recorder.RecordInput(frame)
			}
			session.OnAudioFrame(c, frame)
		})
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(4 * time.Second):
				logger.Logger.Errorw("native receive shutdown did not settle", nil)
			}
			logger.Logger.Infow("experimental native receive stopped", "pcm_seconds", float64(samples.Load())/24000, "ipc", stats.Snapshot())
		})
	}
	session.AddPreCloseCallback(stop)
	if err := job.AddShutdownCallback(stop); err != nil {
		stop()
		return err
	}
	select {
	case <-ready:
	case <-done:
		stop()
		if receiveErr == nil {
			receiveErr = errors.New("helper stopped before subscription")
		}
		return fmt.Errorf("native receive startup: %w", receiveErr)
	case <-startup.Done():
		stop()
		return fmt.Errorf("native receive startup: %w", startup.Err())
	}
	logger.Logger.Infow("experimental native receive enabled", "participant", participant.Identity(), "track", publication.SID(), "sample_rate", 24000, "frame_ms", 50)
	go func() {
		<-done
		if receiveCtx.Err() == nil {
			if errors.Is(receiveErr, nativereceive.ErrSourceEnded) {
				logger.Logger.Infow("experimental native receive source ended", "reason", receiveErr.Error(), "ipc", stats.Snapshot())
				job.Shutdown("native receive source ended")
				return
			}
			logger.Logger.Errorw("experimental native receive failed", receiveErr, "ipc", stats.Snapshot())
			job.Shutdown("experimental native receive failed")
		}
	}()
	return nil
}
