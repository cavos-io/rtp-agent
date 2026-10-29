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

type playbackTiming struct {
	mu        sync.Mutex
	notified  bool
	armed     bool
	startedAt time.Time
}

func observePlaybackTiming(session *AgentSession) (*playbackTiming, func()) {
	timing := &playbackTiming{}
	if session != nil {
		if output, ok := session.AudioPlaybackController().(AudioPlaybackStartNotifier); ok {
			timing.notified = true
			unsubscribe := output.OnPlaybackStarted(func(at time.Time) {
				timing.mu.Lock()
				defer timing.mu.Unlock()
				if timing.armed && timing.startedAt.IsZero() && !at.IsZero() {
					timing.startedAt = at
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
