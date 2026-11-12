package stt

import (
	"testing"
	"time"
)

func TestAudioClockPreservesGapsAndBoundsHistory(t *testing.T) {
	var c AudioClock
	c.Record(0, 1, time.Unix(101, 0))
	c.Record(1, 1, time.Unix(132, 0))
	for _, tc := range []struct{ offset, want float64 }{{0, 100}, {0.5, 100.5}, {1, 101}, {1.5, 131.5}, {2, 132}} {
		got, ok := c.WallTimeAt(tc.offset)
		if !ok || float64(got.UnixNano())/1e9 != tc.want {
			t.Fatalf("offset %v = %v,%v want %v", tc.offset, got, ok, tc.want)
		}
	}
	c.Record(2, 1, time.Unix(300, 0))
	if _, ok := c.WallTimeAt(0.5); ok {
		t.Fatal("expired history retained")
	}
	if _, ok := c.WallTimeAt(4); ok {
		t.Fatal("unrecorded audio extrapolated")
	}
}

func TestAudioClockRingAndReconnectTail(t *testing.T) {
	var c AudioClock
	for i := 0; i < 12005; i++ {
		c.Record(float64(i), 1, time.Unix(100, 0))
	}
	if _, ok := c.WallTimeAt(0.5); ok {
		t.Fatal("ring retained overwritten frame")
	}
	if _, ok := c.WallTimeAt(12004.5); !ok {
		t.Fatal("ring lost newest frame")
	}
	c.Record(12004.25, 0.5, time.Unix(132, 0))
	got, ok := c.WallTimeAt(12004.5)
	if !ok || float64(got.UnixNano())/1e9 != 131.75 {
		t.Fatalf("discarded reconnect tail used: %v %v", got, ok)
	}
}
