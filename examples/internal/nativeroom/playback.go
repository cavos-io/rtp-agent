package nativeroom

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/audio"
	model "github.com/cavos-io/rtp-agent/core/audio/model"
)

// playback accounts for the native *output source* queue, analogous to RTC
// AudioSource.wait_for_playout. It is not a receiver playout policy or a remote
// acoustic acknowledgement. Capture calls serialize with clear and flush.
type playbackCompletion struct {
	result agent.AudioPlaybackResult
	done   bool
}

type playback struct {
	completion *playbackCompletion
	samples    uint64
	startedAt  time.Time
	announced  bool
	mu         sync.Mutex
	epoch      atomic.Uint64
	ctx        context.Context
	send       func(context.Context, string, []byte) error
	record     func(*model.AudioFrame)
	fail       func(error)
	resampler  *audio.StreamingResampler
	deadline   time.Time
	total      time.Duration
	active     bool
	result     agent.AudioPlaybackResult
	err        error
	changed    chan struct{}
	listeners  map[uint64]func(time.Time)
	listenerID uint64
}

func newPlayback(ctx context.Context, send func(context.Context, string, []byte) error, record func(*model.AudioFrame), fail func(error)) *playback {
	return &playback{ctx: ctx, send: send, record: record, fail: fail, changed: make(chan struct{}), listeners: make(map[uint64]func(time.Time))}
}
func (p *playback) notifyLocked() { close(p.changed); p.changed = make(chan struct{}) }
func (p *playback) errorLocked(err error) error {
	if err != nil {
		p.err = err
		p.notifyLocked()
		p.fail(err)
	}
	return err
}
func (p *playback) captureLocked(ctx context.Context, f *model.AudioFrame, epoch uint64) error {
	if f == nil {
		return nil
	}
	for offset := 0; offset < len(f.Data); {
		if p.epoch.Load() != epoch {
			return context.Canceled
		}
		end := min(offset+2400, len(f.Data))
		data := f.Data[offset:end]
		started := time.Now()
		if err := p.send(ctx, "capture", data); err != nil {
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return err
			}
			return p.errorLocked(err)
		}
		if p.startedAt.IsZero() {
			p.startedAt = started
		}
		duration := time.Duration(len(data)/2) * time.Second / 24000
		if p.deadline.Before(started) {
			p.deadline = started
		}
		p.deadline = p.deadline.Add(duration)
		p.samples += uint64(len(data) / 2)
		p.total = time.Duration(p.samples) * time.Second / 24000
		p.record(&model.AudioFrame{Data: data, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: uint32(len(data) / 2)})
		offset = end
	}
	p.notifyLocked()
	return nil
}
func (p *playback) Publish(ctx context.Context, f *model.AudioFrame) error {
	if f == nil {
		return nil
	}
	if f.SampleRate == 0 || f.SampleRate > 192000 || f.NumChannels == 0 || f.NumChannels > 2 || f.SamplesPerChannel == 0 || uint64(len(f.Data)) != uint64(f.SamplesPerChannel)*uint64(f.NumChannels)*2 {
		return errors.New("invalid native output PCM format")
	}
	epoch := p.epoch.Load()
	p.mu.Lock()
	if epoch != p.epoch.Load() {
		p.mu.Unlock()
		return context.Canceled
	}
	if err := ctx.Err(); err != nil {
		p.mu.Unlock()
		return err
	}
	if p.err != nil {
		err := p.err
		p.mu.Unlock()
		return err
	}
	first := !p.active
	if first {
		p.completion = &playbackCompletion{}
		p.total = 0
		p.samples = 0
		p.startedAt = time.Time{}
		p.announced = false
		p.deadline = time.Time{}
		p.result = agent.AudioPlaybackResult{}
		p.active = true
	}
	mono := f
	if f.NumChannels == 2 {
		mono = &model.AudioFrame{SampleRate: f.SampleRate, NumChannels: 1, SamplesPerChannel: f.SamplesPerChannel, Data: make([]byte, int(f.SamplesPerChannel)*2)}
		for i := range int(f.SamplesPerChannel) {
			a := int32(int16(binary.LittleEndian.Uint16(f.Data[i*4:])))
			b := int32(int16(binary.LittleEndian.Uint16(f.Data[i*4+2:])))
			binary.LittleEndian.PutUint16(mono.Data[i*2:], uint16(int16((a+b)/2)))
		}
	}
	if p.resampler == nil {
		p.resampler, _ = audio.NewStreamingResampler(mono.SampleRate, 24000, 1)
	}
	converted, err := p.resampler.Push(mono)
	if err == nil {
		err = p.captureLocked(ctx, converted, epoch)
	} else {
		p.errorLocked(err)
	}
	listeners := []func(time.Time){}
	if !p.announced && !p.startedAt.IsZero() && err == nil {
		p.announced = true
		for _, cb := range p.listeners {
			listeners = append(listeners, cb)
		}
	}
	startedAt := p.startedAt
	p.mu.Unlock()
	for _, cb := range listeners {
		cb(startedAt)
	}
	return err
}
func (p *playback) Flush() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.resampler != nil {
		p.captureLocked(p.ctx, p.resampler.Flush(), p.epoch.Load())
		p.resampler = nil
	}
}
func (p *playback) ClearBuffer() {
	p.epoch.Add(1)
	p.mu.Lock()
	defer p.mu.Unlock()
	remaining := max(time.Until(p.deadline), 0)
	position := max(p.total-remaining, 0)
	if err := p.send(p.ctx, "clear", nil); err != nil {
		p.errorLocked(err)
	}
	p.resampler = nil
	p.deadline = time.Time{}
	p.active = false
	p.result = agent.AudioPlaybackResult{PlaybackPosition: position, Interrupted: true}
	if p.completion != nil {
		p.completion.result = p.result
		p.completion.done = true
	}
	p.notifyLocked()
}
func (p *playback) WaitForPlayout(ctx context.Context) (agent.AudioPlaybackResult, error) {
	p.mu.Lock()
	target := p.completion
	p.mu.Unlock()
	return p.waitForSegment(ctx, target)
}
func (p *playback) waitForSegment(ctx context.Context, target *playbackCompletion) (agent.AudioPlaybackResult, error) {
	for {
		p.mu.Lock()
		if p.err != nil {
			err := p.err
			p.mu.Unlock()
			return agent.AudioPlaybackResult{}, err
		}
		if target == nil {
			p.mu.Unlock()
			return agent.AudioPlaybackResult{}, nil
		}
		if target.done {
			result := target.result
			p.mu.Unlock()
			return result, nil
		}
		wait := time.Until(p.deadline)
		if wait <= 0 {
			p.result = agent.AudioPlaybackResult{PlaybackPosition: p.total}
			target.result = p.result
			target.done = true
			p.active = false
			result := p.result
			p.mu.Unlock()
			return result, nil
		}
		changed := p.changed
		p.mu.Unlock()
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-changed:
		case <-ctx.Done():
			timer.Stop()
			return agent.AudioPlaybackResult{}, ctx.Err()
		case <-p.ctx.Done():
			timer.Stop()
			return agent.AudioPlaybackResult{}, p.ctx.Err()
		}
		timer.Stop()
	}
}
func (p *playback) OnPlaybackStarted(cb func(time.Time)) func() {
	if cb == nil {
		return func() {}
	}
	p.mu.Lock()
	p.listenerID++
	id := p.listenerID
	p.listeners[id] = cb
	p.mu.Unlock()
	return func() { p.mu.Lock(); delete(p.listeners, id); p.mu.Unlock() }
}
