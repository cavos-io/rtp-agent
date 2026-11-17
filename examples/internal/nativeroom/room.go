package nativeroom

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	model "github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/interface/worker"
	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	"github.com/cavos-io/rtp-agent/library/logger"
	lkproto "github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

// Room owns one native connection and uses the worker-assigned primary token. It never
// connects the prepared Pion room or creates a second agent identity.
type Room struct {
	continuity        continuity
	connect           func(context.Context, Config, func(context.Context, *model.AudioFrame)) (*client, error)
	mu                sync.Mutex
	closed            bool
	cancel            context.CancelFunc
	Config            Config
	APIKey, APISecret string
	Job               *worker.JobContext
	Session           *agent.AgentSession
	DeleteRoomOnClose bool
	recorder          *workerlivekit.RecorderIO
	child             *client
	playback          *playback
	closeOnce         sync.Once
	closeCaptions     func(context.Context) error
	samples           atomic.Uint64
}

func New(job *worker.JobContext, session *agent.AgentSession, cfg Config, key, secret string) *Room {
	return &Room{Config: cfg, APIKey: key, APISecret: secret, Job: job, Session: session, recorder: workerlivekit.NewRecorderIO(session)}
}
func (r *Room) GetCallback() *worker.RoomCallback         { return nil }
func (r *Room) AttachRoom(*worker.SDKRoom)                {}
func (r *Room) ReconcileParticipants()                    {}
func (r *Room) StartRecorder(path string, rate int) error { return r.recorder.Start(path, rate) }
func (r *Room) StopRecorder() error                       { return r.recorder.Stop() }
func (r *Room) PopulateSessionReport(report *agent.SessionReport) {
	r.recorder.PopulateSessionReport(report)
}
func (r *Room) Start(ctx context.Context) error {
	run, cancelRun := context.WithCancel(ctx)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		cancelRun()
		return context.Canceled
	}
	r.cancel = cancelRun
	r.mu.Unlock()
	ctx = run
	cfg := r.Config
	info := r.Job.AvatarStartInfo()
	if cfg.URL == "" {
		cfg.URL = info.LiveKitURL
	}
	if cfg.Token == "" {
		cfg.Token = info.LiveKitToken
	}
	if cfg.Token == "" {
		return errors.New("native room requires the assigned primary room token")
	}
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if cfg.Participant == "" || cfg.TrackSID == "" {
		api := lksdk.NewRoomServiceClient(cfg.URL, r.APIKey, r.APISecret)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for cfg.TrackSID == "" {
			list, err := api.ListParticipants(startup, &lkproto.ListParticipantsRequest{Room: info.RoomName})
			if err != nil {
				return fmt.Errorf("native room caller discovery: %w", err)
			}
			var candidates []struct{ identity, sid string }
			for _, p := range list.Participants {
				if p.Identity == info.AgentIdentity || (p.Kind != lkproto.ParticipantInfo_STANDARD && p.Kind != lkproto.ParticipantInfo_SIP) || (cfg.Participant != "" && cfg.Participant != p.Identity) {
					continue
				}
				for _, t := range p.Tracks {
					if t.Type == lkproto.TrackType_AUDIO && !t.Muted {
						candidates = append(candidates, struct{ identity, sid string }{p.Identity, t.Sid})
					}
				}
			}
			if len(candidates) > 1 {
				return errors.New("native room caller/track selection is ambiguous; select RTP_AGENT_NATIVE_PARTICIPANT")
			}
			if len(candidates) == 1 {
				cfg.Participant = candidates[0].identity
				cfg.TrackSID = candidates[0].sid
				break
			}
			select {
			case <-ticker.C:
			case <-startup.Done():
				return startup.Err()
			}
		}
	}
	r.continuity.started = time.Now()
	connect := r.connect
	if connect == nil {
		connect = launch
	}
	child, err := connect(ctx, cfg, func(c context.Context, f *model.AudioFrame) {
		r.continuity.frame(time.Now(), f.SamplesPerChannel)
		r.samples.Add(uint64(f.SamplesPerChannel))
		if !r.Session.InputAudioMuted() {
			r.recorder.RecordInput(f)
			r.Session.OnAudioFrame(c, f)
		}
	})
	if err != nil {
		return err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		child.close()
		return context.Canceled
	}
	r.child = child
	r.mu.Unlock()
	select {
	case <-child.ready:
	case <-child.done:
		return child.err
	case <-startup.Done():
		child.close()
		return startup.Err()
	}
	if child.startCaptions != nil {
		closeCaptions, err := child.startCaptions(r.Session, info.AgentIdentity)
		if err != nil {
			child.close()
			return err
		}
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = closeCaptions(closeCtx)
			return context.Canceled
		}
		r.closeCaptions = closeCaptions
		r.mu.Unlock()
	}
	r.playback = newPlayback(child.ctx, child.command, r.recorder.RecordOutput, child.cancel)
	r.Session.EnsureAssistant().SetPublishAudio(r.playback.Publish)
	r.Session.SetAudioPlaybackController(r.playback)
	r.Session.SetAudioOutputController(r.playback)
	r.Session.AddPreCloseCallback(func() { r.Close() })
	if err := r.Job.AddShutdownCallback(func() { r.Close() }); err != nil {
		r.Close()
		return err
	}
	logger.Logger.Infow("experimental native primary room enabled", "in_process", child.cleanup.Mode == "inprocess", "participant", cfg.Participant, "track", cfg.TrackSID, "identity", info.AgentIdentity, "sample_rate", 24000)
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		observing := true
		for observing {
			select {
			case <-ticker.C:
				logger.Logger.Infow("experimental native primary PCM continuity", "pid", child.cleanup.PID, "continuity", r.continuity.snapshot(time.Now()))
			case <-child.done:
				observing = false
			}
		}
		logger.Logger.Infow("experimental native primary PCM final", "continuity", r.continuity.snapshot(time.Now()))
		if child.cleanup.Mode == "inprocess" {
			logger.Logger.Infow("experimental native in-process cleanup", "cleanup", child.cleanup)
		} else {
			logger.Logger.Infow("experimental native primary helper cleanup", "cleanup", child.cleanup)
		}
		// An explicit source departure is normal; unexpected EOF and nonzero exits
		// remain failures, including errors reported during native resource cleanup.
		if errors.Is(child.err, ErrSourceEnded) {
			logger.Logger.Infow("experimental native primary source ended", "reason", child.err.Error())
			r.Job.Shutdown("native primary source ended")
		} else if cause := context.Cause(child.ctx); !errors.Is(cause, context.Canceled) {
			logger.Logger.Errorw("experimental native primary failed", child.err)
			r.Job.Shutdown("native primary failed")
		}
	}()
	return nil
}
func (r *Room) BeginShutdown() {
	r.mu.Lock()
	r.closed = true
	if r.cancel != nil {
		r.cancel()
	}
	r.mu.Unlock()
}
func (r *Room) Close() error {
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		closeCaptions := r.closeCaptions
		r.mu.Unlock()
		if closeCaptions != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			if err := closeCaptions(ctx); err != nil {
				logger.Logger.Warnw("native captions shutdown failed", err)
			}
			cancel()
		}
		r.BeginShutdown()
		r.mu.Lock()
		child := r.child
		r.mu.Unlock()
		if child != nil {
			if err := child.close(); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, ErrSourceEnded) {
				logger.Logger.Errorw("experimental native primary cleanup failed", err)
			}
		}
		if err := r.recorder.Stop(); err != nil {
			logger.Logger.Errorw("native primary recording stop failed", err)
		}
		logger.Logger.Infow("experimental native primary stopped", "pcm_seconds", float64(r.samples.Load())/24000)
		if r.DeleteRoomOnClose {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if _, err := r.Job.DeleteRoom(ctx, r.Job.AvatarStartInfo().RoomName); err != nil {
				logger.Logger.Errorw("native primary delete room failed", err)
			}
		}
	})
	return nil
}

// CleanupDiagnostics returns a stable snapshot only once the child is reaped.
func (r *Room) CleanupDiagnostics() (Cleanup, bool) {
	r.mu.Lock()
	child := r.child
	r.mu.Unlock()
	if child == nil {
		return Cleanup{}, false
	}
	select {
	case <-child.done:
		return child.cleanup, true
	default:
		return Cleanup{}, false
	}
}
