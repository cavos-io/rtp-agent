package stt

import (
	"math"
	"sort"
	"sync"
	"time"
)

// StreamWallTiming maps cumulative provider audio offsets to frame receipt time.
type StreamWallTiming interface {
	WallTimeAt(audioSeconds float64) (time.Time, bool)
	// Start anchors use the frame after a shared boundary; end anchors use the frame before it.
	WallTimeAtStart(audioSeconds float64) (time.Time, bool)
}

type audioClockFrame struct {
	start, end float64
	wallEnd    time.Time
}

// AudioClock retains frame intervals, rather than interpolating across DTX gaps.
// The zero value is ready for use. Offsets must use the provider's audio timeline.
type AudioClock struct {
	mu          sync.Mutex
	frames      []audioClockFrame
	head, count int
}

func (c *AudioClock) Record(start, duration float64, wallEnd time.Time) {
	if start < 0 || duration <= 0 || math.IsNaN(start+duration) || math.IsInf(start+duration, 0) || wallEnd.IsZero() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Two minutes of history, capped at 12,000 frames even for tiny input frames.
	if c.frames == nil {
		c.frames = make([]audioClockFrame, 12000)
	}
	for c.count > 0 && (c.count == len(c.frames) || c.frames[c.head].wallEnd.Before(wallEnd.Add(-120*time.Second))) {
		c.head = (c.head + 1) % len(c.frames)
		c.count--
	}
	// A reconnect can discard a locally buffered tail. Replace that tail when
	// the new connection starts again at the last transmitted audio offset.
	for c.count > 0 {
		i := (c.head + c.count - 1) % len(c.frames)
		f := &c.frames[i]
		if f.end <= start+1e-6 {
			break
		}
		if f.start < start {
			f.wallEnd = f.wallEnd.Add(time.Duration((start - f.end) * float64(time.Second)))
			f.end = start
			break
		}
		c.count--
	}
	c.frames[(c.head+c.count)%len(c.frames)] = audioClockFrame{start, start + duration, wallEnd}
	c.count++
}

func (c *AudioClock) WallTimeAt(offset float64) (time.Time, bool) {
	return c.wallTimeAt(offset, false)
}
func (c *AudioClock) WallTimeAtStart(offset float64) (time.Time, bool) {
	return c.wallTimeAt(offset, true)
}
func (c *AudioClock) wallTimeAt(offset float64, start bool) (time.Time, bool) {
	if math.IsNaN(offset) || math.IsInf(offset, 0) {
		return time.Time{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Subtraction of Unix-second anchors can lose fractions of a microsecond.
	const epsilon = 1e-6
	i := sort.Search(c.count, func(i int) bool { return c.frames[(c.head+i)%len(c.frames)].end+epsilon >= offset })
	if i == c.count {
		return time.Time{}, false
	}
	if start && i+1 < c.count && c.frames[(c.head+i+1)%len(c.frames)].start <= offset+epsilon {
		i++
	}
	f := c.frames[(c.head+i)%len(c.frames)]
	if offset < f.start-epsilon {
		return time.Time{}, false
	}
	offset = max(f.start, min(f.end, offset))
	return f.wallEnd.Add(time.Duration((offset - f.end) * float64(time.Second))), true
}
