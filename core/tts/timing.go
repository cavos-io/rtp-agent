package tts

import (
	"context"
	"sync"
	"time"
)

type synthesisTimingKey struct{}

// SynthesisTiming records the first provider submission for one inference.
type SynthesisTiming struct {
	mu        sync.Mutex
	startedAt time.Time
}

func WithSynthesisTiming(ctx context.Context) (context.Context, *SynthesisTiming) {
	timing := &SynthesisTiming{}
	return context.WithValue(ctx, synthesisTimingKey{}, timing), timing
}

// MarkSynthesisStarted belongs immediately before sending nonempty text to a
// provider, after local word/sentence buffering and connection initialization.
func MarkSynthesisStarted(ctx context.Context) {
	if ctx == nil {
		return
	}
	timing, _ := ctx.Value(synthesisTimingKey{}).(*SynthesisTiming)
	if timing == nil {
		return
	}
	timing.mu.Lock()
	defer timing.mu.Unlock()
	if timing.startedAt.IsZero() {
		timing.startedAt = time.Now()
	}
}

func (t *SynthesisTiming) StartedAt() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.startedAt
}
