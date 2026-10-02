package google

import (
	"bytes"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

// googleSTTAudioReplayBuffer keeps audio that Google has not acknowledged with
// a final result. Callers serialize access with the owning stream mutex.
type googleSTTAudioReplayBuffer struct {
	maxDuration time.Duration
	frames      []*model.AudioFrame
	duration    time.Duration

	rpcActive            bool
	rpcEvictedDuration   time.Duration
	rpcAcknowledgedAudio time.Duration
	rpcLastResultEnd     time.Duration
}

func newGoogleSTTAudioReplayBuffer(maxDuration time.Duration) *googleSTTAudioReplayBuffer {
	return &googleSTTAudioReplayBuffer{maxDuration: maxDuration}
}

func (b *googleSTTAudioReplayBuffer) Append(frame *model.AudioFrame) {
	if b == nil || frame == nil || len(frame.Data) == 0 {
		return
	}
	clone := *frame
	clone.Data = bytes.Clone(frame.Data)
	b.frames = append(b.frames, &clone)
	b.duration += googleSTTAudioFrameDuration(&clone)
	b.trimOverflow()
}

func (b *googleSTTAudioReplayBuffer) BeginRPC() {
	if b == nil {
		return
	}
	b.rpcActive = true
	b.rpcEvictedDuration = 0
	b.rpcAcknowledgedAudio = 0
	b.rpcLastResultEnd = 0
}

func (b *googleSTTAudioReplayBuffer) FramesFrom(index int) []*model.AudioFrame {
	if b == nil || index < 0 || index >= len(b.frames) {
		return nil
	}
	frames := make([]*model.AudioFrame, 0, len(b.frames)-index)
	for _, frame := range b.frames[index:] {
		clone := *frame
		clone.Data = bytes.Clone(frame.Data)
		frames = append(frames, &clone)
	}
	return frames
}

func (b *googleSTTAudioReplayBuffer) Acknowledge(resultEnd time.Duration) {
	if b == nil || resultEnd <= 0 || resultEnd <= b.rpcLastResultEnd {
		return
	}
	b.rpcLastResultEnd = resultEnd
	target := resultEnd - b.rpcEvictedDuration
	if target <= b.rpcAcknowledgedAudio {
		return
	}
	toDrop := target - b.rpcAcknowledgedAudio
	for len(b.frames) > 0 {
		duration := googleSTTAudioFrameDuration(b.frames[0])
		if duration <= 0 || duration > toDrop {
			return
		}
		b.frames = b.frames[1:]
		b.duration -= duration
		b.rpcAcknowledgedAudio += duration
		toDrop -= duration
	}
}

func (b *googleSTTAudioReplayBuffer) trimOverflow() {
	for b.maxDuration > 0 && b.duration > b.maxDuration && len(b.frames) > 0 {
		duration := googleSTTAudioFrameDuration(b.frames[0])
		b.frames = b.frames[1:]
		b.duration -= duration
		if b.rpcActive {
			b.rpcEvictedDuration += duration
		}
	}
}

func googleSTTAudioFrameDuration(frame *model.AudioFrame) time.Duration {
	if frame == nil || frame.SampleRate == 0 || frame.SamplesPerChannel == 0 {
		return 0
	}
	return time.Duration(frame.SamplesPerChannel) * time.Second / time.Duration(frame.SampleRate)
}
