package nativereceive

import (
	"sync/atomic"
	"time"
)

// Stats observes IPC flow only. It never changes native playout or PCM.
type Stats struct {
	received, delivered, waits atomic.Uint64
	maxWait, maxConsume        atomic.Int64
}

func recordMax(value *atomic.Int64, duration time.Duration) {
	for previous := value.Load(); int64(duration) > previous; previous = value.Load() {
		if value.CompareAndSwap(previous, int64(duration)) {
			return
		}
	}
}
func (s *Stats) Snapshot() map[string]any {
	return map[string]any{"received_frames": s.received.Load(), "completed_callbacks": s.delivered.Load(), "queue_waits": s.waits.Load(), "max_queue_wait_ms": float64(s.maxWait.Load()) / 1e6, "max_consume_ms": float64(s.maxConsume.Load()) / 1e6}
}
