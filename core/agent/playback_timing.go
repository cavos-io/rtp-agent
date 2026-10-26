package agent

import (
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
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

func (s *AgentSession) claimE2EMetric(metrics map[string]any, user *llm.ChatMessage) {
	if _, ok := metrics["e2e_latency"]; !ok || user == nil {
		return
	}
	stopped, ok := user.Metrics["stopped_speaking_at"].(float64)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if stopped <= s.lastAnsweredUserSpeechAt {
		delete(metrics, "e2e_latency")
		return
	}
	s.lastAnsweredUserSpeechAt = stopped
}
