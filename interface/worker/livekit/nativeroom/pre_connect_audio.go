//go:build nativeinprocess

package nativeroom

import (
	"context"
	"errors"
	model "github.com/cavos-io/rtp-agent/core/audio/model"
	wl "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	native "github.com/cavos-io/rtp-agent/interface/worker/livekit/nativeinprocess"
	"github.com/cavos-io/rtp-agent/library/logger"
	"time"
)

// Replay startup audio before live input. Drain live PCM while waiting so the
// runtime's short delivery queue cannot overflow during the startup grace period.
func replayPreConnect(ctx context.Context, frames <-chan native.Frame, buffered <-chan native.PreConnectAudio, timeout time.Duration, aux <-chan native.RecordingFrame, record func(native.RecordingFrame), consume func(context.Context, *model.AudioFrame)) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	pending := make([]native.Frame, 0, 150)
	var startup *native.PreConnectAudio
wait:
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case frame, ok := <-aux:
			if !ok {
				aux = nil
			} else if record != nil {
				record(frame)
			}
		case <-timer.C:
			break wait
		case audio, ok := <-buffered:
			if ok {
				startup = &audio
			}
			break wait
		case frame, ok := <-frames:
			if !ok {
				return native.ErrSourceEnded
			}
			if len(pending) >= 1500 {
				return errors.New("pre-connect live audio buffer exceeded 30 seconds")
			}
			pending = append(pending, frame)
		}
	}
	if startup != nil && time.Since(startup.Received) <= timeout {
		decoded, err := wl.DecodePreConnectAudio(startup.Chunks, startup.Info.GetMimeType(), startup.Info.GetAttributes())
		if err != nil {
			logger.Logger.Warnw("native.pre_connect.decode_failed", err)
		} else {
			for _, f := range decoded {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				consume(ctx, f)
			}
		}
	}
	for _, f := range pending {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		consume(ctx, nativePCMFrame(f.PCM))
	}
	return nil
}
