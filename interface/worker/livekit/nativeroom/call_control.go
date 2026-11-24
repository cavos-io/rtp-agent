//go:build nativeinprocess

package nativeroom

import (
	"context"
	"errors"
	"github.com/cavos-io/rtp-agent/library/logger"
)

func (r *Room) nativeControl() (nativeCallControl, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errors.New("native room closed")
	}
	if r.child == nil || r.child.callControl == nil {
		return nil, errors.New("native room not connected")
	}
	return r.child.callControl, nil
}

// RemoteParticipantIdentities returns a snapshot from the native room, excluding self.
func (r *Room) RemoteParticipantIdentities() []string {
	c, err := r.nativeControl()
	if err != nil {
		return nil
	}
	return c.ParticipantIdentities()
}
func (r *Room) BeginHold(ctx context.Context) error {
	c, err := r.nativeControl()
	if err != nil {
		return err
	}
	if err := c.BeginHold(ctx); err != nil {
		logger.Logger.Warnw("native.hold.publish_failed", err, "participant", r.Config.Participant)
		return err
	}
	r.holdFrames.Store(0)
	r.holdSamples.Store(0)
	logger.Logger.Infow("native.hold.published", "participant", r.Config.Participant, "track_name", "hold-audio", "sample_rate", 24000)
	return nil
}
func (r *Room) EndHold(ctx context.Context) error {
	c, err := r.nativeControl()
	if err != nil {
		return err
	}
	err = c.EndHold(ctx)
	logger.Logger.Infow("native.hold.stopped", "participant", r.Config.Participant, "frames", r.holdFrames.Load(), "pcm_seconds", float64(r.holdSamples.Load())/24000, "clear_succeeded", err == nil)
	if err != nil {
		logger.Logger.Warnw("native.hold.clear_failed", err, "participant", r.Config.Participant)
	}
	return err
}

// CaptureHold accepts 24 kHz mono PCM on an independent output source.
func (r *Room) CaptureHold(ctx context.Context, pcm []int16) error {
	c, err := r.nativeControl()
	if err != nil {
		return err
	}
	if err := c.CaptureHold(ctx, pcm); err != nil {
		logger.Logger.Warnw("native.hold.capture_failed", err, "participant", r.Config.Participant, "frames", r.holdFrames.Load())
		return err
	}
	samples := r.holdSamples.Add(uint64(len(pcm)))
	frames := r.holdFrames.Add(1)
	if frames == 1 || frames%500 == 0 {
		peak := 0
		for _, sample := range pcm {
			value := int(sample)
			if value < 0 {
				value = -value
			}
			if value > peak {
				peak = value
			}
		}
		logger.Logger.Infow("native.hold.pcm_delivered", "participant", r.Config.Participant, "frames", frames, "pcm_seconds", float64(samples)/24000, "frame_peak_abs", peak)
	}
	return nil
}
func (r *Room) SetOutputAudioEnabled(ctx context.Context, enabled bool) error {
	c, err := r.nativeControl()
	if err != nil {
		return err
	}
	return c.SetOutputEnabled(ctx, enabled)
}
func (r *Room) SetInputAudioEnabled(ctx context.Context, enabled bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.Session == nil {
		return errors.New("native room has no session")
	}
	r.Session.SetInputAudioMuted(!enabled)
	return nil
}

func (r *Room) PublishData(ctx context.Context, topic string, data []byte) error {
	c, err := r.nativeControl()
	if err != nil {
		return err
	}
	return c.PublishData(ctx, topic, data)
}
