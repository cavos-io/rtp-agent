package nativeroom

import (
	"sync"
	"time"
)

// Continuity measures application callback delivery using the parent's monotonic
// clock. It observes PCM; it never schedules, drops, or synthesizes frames.
type Continuity struct {
	WallSeconds       float64 `json:"wall_seconds"`
	PCMSeconds        float64 `json:"pcm_seconds"`
	TotalPCMSeconds   float64 `json:"total_pcm_seconds"`
	MaxGapMS          float64 `json:"max_gap_ms"`
	SinceLastFrameMS  float64 `json:"since_last_frame_ms"`
	FirstFrameDelayMS float64 `json:"first_frame_delay_ms"`
}
type continuity struct {
	mu                           sync.Mutex
	started, window, last, first time.Time
	samples, windowSamples       uint64
	maxGap                       time.Duration
}

func (d *continuity) frame(now time.Time, samples uint32) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.first.IsZero() {
		d.first = now
	}
	if !d.last.IsZero() {
		d.maxGap = max(d.maxGap, now.Sub(d.last))
	}
	d.last = now
	d.samples += uint64(samples)
}
func (d *continuity) snapshot(now time.Time) Continuity {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.window.IsZero() {
		d.window = d.started
	}
	s := Continuity{WallSeconds: now.Sub(d.window).Seconds(), PCMSeconds: float64(d.samples-d.windowSamples) / 24000, TotalPCMSeconds: float64(d.samples) / 24000, MaxGapMS: float64(d.maxGap) / float64(time.Millisecond)}
	if !d.last.IsZero() {
		s.SinceLastFrameMS = float64(now.Sub(d.last)) / float64(time.Millisecond)
	}
	if !d.first.IsZero() {
		s.FirstFrameDelayMS = float64(d.first.Sub(d.started)) / float64(time.Millisecond)
	}
	d.window = now
	d.windowSamples = d.samples
	d.maxGap = 0
	return s
}

// Cleanup is published only after the child has been waited and the PCM
// consumer has stopped. Native shutdown flags are reported by the helper.
type Cleanup struct {
	NativeCleanupMS    float64 `json:"native_cleanup_ms"`
	PID                int     `json:"pid"`
	Reaped             bool    `json:"reaped"`
	ExitCode           int     `json:"exit_code"`
	KillAttempted      bool    `json:"kill_attempted"`
	CleanupMS          float64 `json:"cleanup_ms"`
	ShutdownSeen       bool    `json:"shutdown_seen"`
	DisconnectCallback bool    `json:"disconnect_callback"`
	HandlesReleased    bool    `json:"handles_released"`
	RuntimeDisposed    bool    `json:"runtime_disposed"`
	CallbackOverflow   bool    `json:"callback_overflow"`
}
