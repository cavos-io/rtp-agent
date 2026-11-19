package nativeroom

import (
	"testing"
	"time"
)

func TestContinuityWindowsAndStall(t *testing.T) {
	start := time.Now()
	d := continuity{started: start}
	for i := 1; i <= 200; i++ {
		d.frame(start.Add(time.Duration(i)*50*time.Millisecond), 1200)
	}
	s := d.snapshot(start.Add(10 * time.Second))
	if s.WallSeconds != 10 || s.PCMSeconds != 10 || s.MaxGapMS != 50 || s.FirstFrameDelayMS != 50 {
		t.Fatalf("%+v", s)
	}
	s = d.snapshot(start.Add(20 * time.Second))
	if s.PCMSeconds != 0 || s.TotalPCMSeconds != 10 || s.SinceLastFrameMS != 10000 {
		t.Fatalf("stall hidden: %+v", s)
	}
	d.frame(start.Add(21*time.Second), 1200)
	s = d.snapshot(start.Add(30 * time.Second))
	if s.PCMSeconds != .05 || s.MaxGapMS != 11000 {
		t.Fatalf("gap hidden: %+v", s)
	}
}
