package agent

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// pauseTrackingOutput timestamps every output pause, whoever issues it, so the caption
// clock can leave paused time out of what the caller heard.
type pauseTrackingOutput struct {
	AudioOutputController
	mu       sync.Mutex
	pausedAt time.Time
	paused   time.Duration
}

func (p *pauseTrackingOutput) PauseAudioOutput() {
	p.mu.Lock()
	if p.pausedAt.IsZero() {
		p.pausedAt = time.Now()
	}
	p.mu.Unlock()
	p.AudioOutputController.PauseAudioOutput()
}

func (p *pauseTrackingOutput) ResumeAudioOutput() {
	p.mu.Lock()
	if !p.pausedAt.IsZero() {
		p.paused += time.Since(p.pausedAt)
		p.pausedAt = time.Time{}
	}
	p.mu.Unlock()
	p.AudioOutputController.ResumeAudioOutput()
}

func (p *pauseTrackingOutput) pausedTotal() time.Duration {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pausedAt.IsZero() {
		return p.paused
	}
	return p.paused + time.Since(p.pausedAt)
}

// playing is called when a frame was just written, so output is not paused: it closes a
// pause the transport ended on its own (some unpause on clear) and returns the total.
// ponytail: a pause racing the very first frame of a reply is closed too and goes uncounted.
func (p *pauseTrackingOutput) playing() time.Duration {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.pausedAt.IsZero() {
		p.paused += time.Since(p.pausedAt)
		p.pausedAt = time.Time{}
	}
	return p.paused
}

type playbackTiming struct {
	mu            sync.Mutex
	notified      bool
	armed         bool
	startedAt     time.Time
	pauses        *pauseTrackingOutput
	pausedAtStart time.Duration
}

func observePlaybackTiming(session *AgentSession) (*playbackTiming, func()) {
	timing := &playbackTiming{}
	if session != nil {
		timing.pauses, _ = session.AudioOutputController().(*pauseTrackingOutput)
		if output, ok := session.AudioPlaybackController().(AudioPlaybackStartNotifier); ok {
			timing.notified = true
			unsubscribe := output.OnPlaybackStarted(func(at time.Time) {
				timing.mu.Lock()
				defer timing.mu.Unlock()
				if timing.armed && timing.startedAt.IsZero() && !at.IsZero() {
					timing.startedAt = at
					timing.pausedAtStart = timing.pauses.playing()
				}
			})
			if unsubscribe != nil {
				return timing, unsubscribe
			}
		}
	}
	return timing, func() {}
}

func (t *playbackTiming) arm() {
	t.mu.Lock()
	t.armed = true
	t.mu.Unlock()
}

// started reports whether the transport confirmed that audio reached the wire.
func (t *playbackTiming) started() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.notified && !t.startedAt.IsZero()
}

// playedFor is how long audio has been playing: wall time since the first written frame,
// minus output pauses since then. Zero before the first write.
func (t *playbackTiming) playedFor() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.startedAt.IsZero() {
		return 0
	}
	return time.Since(t.startedAt) - (t.pauses.pausedTotal() - t.pausedAtStart)
}

func (t *playbackTiming) published() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.armed && !t.notified && t.startedAt.IsZero() {
		t.startedAt = time.Now()
	}
	return t.startedAt
}

func finalizeTTSPlaybackTiming(data *TTSGenerationData) {
	if data == nil || data.stopPlaybackTiming == nil {
		return
	}
	data.stopPlaybackTiming()
	data.stopPlaybackTiming = nil
	if at := data.playbackTiming.published(); !at.IsZero() {
		data.StartedSpeakingAt = timeToUnixSeconds(at)
	}
}

func (s *AgentSession) claimAnsweredUserTurn(stopped float64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if stopped <= s.lastAnsweredUserSpeechAt {
		return false
	}
	s.lastAnsweredUserSpeechAt = stopped
	return true
}

func (s *AgentSession) measureAssistantE2E(ctx context.Context, metrics map[string]any, ttsGen *TTSGenerationData, user *llm.ChatMessage, interrupted bool) map[string]any {
	dimensions := s.turnMetricDimensions
	dimensions.Interrupted = interrupted
	stopped, ok := userSpeechStop(user)
	if !ok {
		telemetry.RecordE2EOutcome(ctx, telemetry.E2EOutcomeMissingUserSpeechStop, dimensions)
		return metrics
	}
	if !s.claimAnsweredUserTurn(stopped) {
		telemetry.RecordE2EOutcome(ctx, telemetry.E2EOutcomeDuplicateUserTurn, dimensions)
		return metrics
	}
	telemetry.RecordE2EOutcome(ctx, telemetry.E2EOutcomeEligible, dimensions)
	if ttsGen == nil || !ttsGen.ForwardedAudio {
		telemetry.RecordE2EOutcome(ctx, telemetry.E2EOutcomeNoAudioPublished, dimensions)
		return metrics
	}
	started := ttsGen.StartedSpeakingAt
	if started == 0 {
		telemetry.RecordE2EOutcome(ctx, telemetry.E2EOutcomeMissingAudioStart, dimensions)
		return metrics
	}
	duration := started - stopped
	if started < 0 || math.IsNaN(duration) || math.IsInf(duration, 0) || duration < 0 {
		telemetry.RecordE2EOutcome(ctx, telemetry.E2EOutcomeInvalidDuration, dimensions)
		return metrics
	}
	if metrics == nil {
		metrics = make(map[string]any)
	}
	metrics["e2e_latency"] = duration
	trace.SpanFromContext(ctx).SetAttributes(attribute.Float64(telemetry.AttrE2ELatency, duration))
	telemetry.RecordE2EOutcome(ctx, telemetry.E2EOutcomeEmitted, dimensions)
	return metrics
}

func userSpeechStop(user *llm.ChatMessage) (float64, bool) {
	if user == nil {
		return 0, false
	}
	stopped, ok := user.Metrics["stopped_speaking_at"].(float64)
	return stopped, ok
}
