package agent

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
)

const (
	sttRecoveryGrace   = time.Second
	sttRecoveryTimeout = 4 * time.Second
	sttRecoveryAudio   = 8 * time.Second
)

type STTRecoveryFactory func() (stt.STT, error)

type sttRecoveryJob struct {
	id       uint64
	epoch    uint64
	frames   []*model.AudioFrame
	language string
	started  time.Time
	ctx      context.Context
	cancel   context.CancelFunc
}

type sttRecoveryResult struct {
	job   sttRecoveryJob
	event *stt.SpeechEvent
}

type sttRecoveryCoordinator struct {
	ctx       context.Context
	cancel    context.CancelFunc
	factory   STTRecoveryFactory
	jobs      chan sttRecoveryJob
	results   chan sttRecoveryResult
	nextID    atomic.Uint64
	mu        sync.Mutex
	pending   map[uint64]sttRecoveryJob
	winner    map[uint64]recoveryWinner
	closeOnce sync.Once
	done      chan struct{}
	grace     time.Duration
	timeout   time.Duration
}

type recoveryWinner uint8

const (
	recoveryWinnerPrimary recoveryWinner = iota + 1
	recoveryWinnerFallback
)

func newSTTRecoveryCoordinator(ctx context.Context, factory STTRecoveryFactory) *sttRecoveryCoordinator {
	ctx, cancel := context.WithCancel(ctx)
	r := &sttRecoveryCoordinator{
		ctx: ctx, cancel: cancel, factory: factory,
		jobs: make(chan sttRecoveryJob, 8), results: make(chan sttRecoveryResult, 1), pending: make(map[uint64]sttRecoveryJob), winner: make(map[uint64]recoveryWinner), done: make(chan struct{}),
		grace: sttRecoveryGrace, timeout: sttRecoveryTimeout,
	}
	go r.run()
	return r
}

func (r *sttRecoveryCoordinator) Schedule(epoch uint64, frames []*model.AudioFrame, language string, started time.Time) {
	if r == nil || r.factory == nil || epoch == 0 || len(frames) == 0 {
		return
	}
	ctx, cancel := context.WithCancel(r.ctx)
	job := sttRecoveryJob{
		id: r.nextID.Add(1), epoch: epoch, frames: newestRecoveryAudio(frames, sttRecoveryAudio),
		language: language, started: started, ctx: ctx, cancel: cancel,
	}
	if len(job.frames) == 0 {
		cancel()
		return
	}
	r.mu.Lock()
	if _, ok := r.winner[epoch]; ok {
		r.mu.Unlock()
		cancel()
		return
	}
	if previous, ok := r.pending[epoch]; ok {
		previous.cancel()
	}
	r.pending[epoch] = job
	r.mu.Unlock()
	select {
	case r.jobs <- job:
	case <-r.ctx.Done():
		cancel()
	}
}

func (r *sttRecoveryCoordinator) PrimaryEvidence(epoch uint64) bool {
	if r == nil || epoch == 0 {
		return true
	}
	r.mu.Lock()
	if winner, ok := r.winner[epoch]; ok {
		r.mu.Unlock()
		return winner == recoveryWinnerPrimary
	}
	r.winner[epoch] = recoveryWinnerPrimary
	if job, ok := r.pending[epoch]; ok {
		job.cancel()
		delete(r.pending, epoch)
	}
	r.mu.Unlock()
	return true
}

func (r *sttRecoveryCoordinator) Close() {
	if r == nil {
		return
	}
	r.closeOnce.Do(func() {
		r.cancel()
		select {
		case <-r.done:
		case <-time.After(r.timeout):
		}
	})
}

func (r *sttRecoveryCoordinator) run() {
	defer close(r.done)
	for {
		select {
		case <-r.ctx.Done():
			return
		case job := <-r.jobs:
			timer := time.NewTimer(r.grace)
			select {
			case <-timer.C:
			case <-job.ctx.Done():
				timer.Stop()
				continue
			case <-r.ctx.Done():
				timer.Stop()
				return
			}
			ctx, cancel := context.WithTimeout(job.ctx, r.timeout)
			event, err := recognizeRecovery(ctx, r.factory, job.frames, job.language)
			cancel()
			claimed := err == nil && event != nil && r.claimRecovery(job)
			r.finish(job)
			if claimed {
				select {
				case r.results <- sttRecoveryResult{job: job, event: event}:
				case <-r.ctx.Done():
				}
			}
		}
	}
}

func (r *sttRecoveryCoordinator) claimRecovery(job sttRecoveryJob) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.winner[job.epoch]; ok {
		return false
	}
	pending, ok := r.pending[job.epoch]
	if !ok || pending.id != job.id {
		return false
	}
	r.winner[job.epoch] = recoveryWinnerFallback
	return true
}

func (r *sttRecoveryCoordinator) finish(job sttRecoveryJob) {
	r.mu.Lock()
	if pending, ok := r.pending[job.epoch]; ok && pending.id == job.id {
		delete(r.pending, job.epoch)
	}
	r.mu.Unlock()
	job.cancel()
}

func recognizeRecovery(ctx context.Context, factory STTRecoveryFactory, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	provider, err := factory()
	if err != nil {
		return nil, err
	}
	if provider == nil {
		return nil, fmt.Errorf("STT recovery factory returned nil provider")
	}
	defer stt.Close(provider)
	if !provider.Capabilities().Streaming {
		event, err := provider.Recognize(ctx, frames, language)
		if err != nil || !nonblankRecoveryFinal(event) {
			return nil, err
		}
		return event, nil
	}
	stream, err := provider.Stream(ctx, language)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	for _, frame := range frames {
		if err := stream.PushFrame(frame); err != nil {
			return nil, err
		}
	}
	if ending, ok := stream.(stt.InputEnding); ok {
		if err := ending.EndInput(); err != nil {
			return nil, err
		}
	} else if err := stream.Flush(); err != nil {
		return nil, err
	}
	for {
		event, err := stream.Next()
		if err != nil {
			if err == io.EOF {
				return nil, nil
			}
			return nil, err
		}
		if nonblankRecoveryFinal(event) {
			return event, nil
		}
	}
}

func nonblankRecoveryFinal(event *stt.SpeechEvent) bool {
	return event != nil && event.Type == stt.SpeechEventFinalTranscript && len(event.Alternatives) > 0 && strings.TrimSpace(event.Alternatives[0].Text) != ""
}

func newestRecoveryAudio(frames []*model.AudioFrame, limit time.Duration) []*model.AudioFrame {
	var kept []*model.AudioFrame
	var duration time.Duration
	for i := len(frames) - 1; i >= 0; i-- {
		frame := frames[i]
		if frame == nil || frame.SampleRate == 0 {
			continue
		}
		frameDuration := time.Duration(float64(time.Second) * float64(frame.SamplesPerChannel) / float64(frame.SampleRate))
		if len(kept) > 0 && duration+frameDuration > limit {
			break
		}
		clone := *frame
		clone.Data = append([]byte(nil), frame.Data...)
		kept = append(kept, &clone)
		duration += frameDuration
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	return kept
}
