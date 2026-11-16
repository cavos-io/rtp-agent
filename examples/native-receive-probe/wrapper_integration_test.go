//go:build nativeinprocess

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	native "native-receive-probe/nativeinprocess"
)

func TestNativeWrapperLocal(t *testing.T) {
	url, out := os.Getenv("PROBE_INPROCESS_URL"), os.Getenv("PROBE_INPROCESS_OUT")
	if url == "" || out == "" {
		t.Fatal("local URL and new output directory required")
	}
	if e := os.Mkdir(out, 0755); e != nil {
		t.Fatal(e)
	}
	file, e := os.Create(filepath.Join(out, "summary.jsonl"))
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	emit := func(v any) {
		t.Helper()
		if e := json.NewEncoder(file).Encode(v); e != nil {
			t.Fatal(e)
		}
	}
	rt, e := native.Open(os.Getenv("LIVEKIT_LIB_PATH"))
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if e := rt.Close(ctx); e != nil {
			t.Error(e)
		}
	}()
	if _, e = native.Open(os.Getenv("LIVEKIT_LIB_PATH")); e == nil {
		t.Fatal("second runtime owner accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	type observed struct {
		samples, markers, last, maxGap atomic.Int64
		done                           chan struct{}
		errs                           chan error
	}
	consume := func(s *native.Session, hz, other float64) *observed {
		o := &observed{done: make(chan struct{}), errs: make(chan error, 1)}
		go func() {
			defer close(o.done)
			for f := range s.Frames() {
				if last := o.last.Swap(f.At); last != 0 {
					gap := f.At - last
					if gap > o.maxGap.Load() {
						o.maxGap.Store(gap)
					}
				}
				o.samples.Add(int64(len(f.PCM)))
				own, alien := processAmplitude(f.PCM, 24000, hz), processAmplitude(f.PCM, 24000, other)
				if alien > 1000 && alien > own*3 {
					o.errs <- errors.New("cross-room PCM")
					return
				}
				if own > 1000 && own > alien*3 {
					o.markers.Add(1)
				}
				if e := s.Capture(ctx, f.PCM); e != nil {
					if errors.Is(e, native.ErrClosed) || errors.Is(e, context.Canceled) {
						return
					}
					o.errs <- e
					return
				}
			}
		}()
		return o
	}
	wait := func(predicate func() bool) {
		t.Helper()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for !predicate() {
			select {
			case <-ctx.Done():
				t.Fatal("test timeout")
			case <-tick.C:
			}
		}
	}
	open := func(label string, hz, other float64) (*native.Session, *processPublisher) {
		t.Helper()
		room := fmt.Sprintf("wrapper-%s-%d", label, time.Now().UnixNano())
		p := startProcessPublisher(t, url, room, hz, other)
		jwt, e := token(room, "native-agent")
		if e != nil {
			t.Fatal(e)
		}
		s, e := rt.Connect(ctx, url, jwt)
		if e != nil {
			t.Fatal(e)
		}
		return s, p
	}
	a, ap := open("stalled", 600, 1000)
	b, bp := open("healthy", 1000, 600)
	bo := consume(b, 1000, 600)
	if e = b.WaitReady(ctx); e != nil {
		t.Fatal(e)
	}
	wait(func() bool { return bo.markers.Load() >= 25 && bp.markers.Load() >= 25 })
	select {
	case <-a.Done():
	case <-ctx.Done():
		t.Fatal("stalled consumer did not fail")
	}
	if !errors.Is(a.Err(), native.ErrBackpressure) {
		t.Fatal("overflow error hidden", a.Err())
	}
	if len(a.Frames()) != 25 {
		t.Fatal("queue bound changed", len(a.Frames()))
	}
	before := bo.samples.Load()
	start := time.Now()
	time.Sleep(2 * time.Second)
	pcm := float64(bo.samples.Load()-before) / 24000
	wall := time.Since(start).Seconds()
	if pcm < wall-.25 || pcm > wall+.25 || bo.maxGap.Load() > 250_000_000 {
		t.Fatalf("healthy room continuity pcm=%f wall=%f gap=%d", pcm, wall, bo.maxGap.Load())
	}
	emit(map[string]any{"event": "stalled_session", "error": a.Err().Error(), "queue_frames": len(a.Frames()), "healthy_pcm_seconds": pcm, "wall_seconds": wall, "healthy_output_markers": bp.markers.Load(), "healthy_max_gap_ms": float64(bo.maxGap.Load()) / 1e6})
	ap.finish()
	// Real invalid-token failure must be visible and must not close B.
	failed, e := rt.Connect(ctx, url, "invalid-token")
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-failed.Done():
	case <-ctx.Done():
		t.Fatal("failed connect timeout")
	}
	if failed.Err() == nil {
		t.Fatal("failed native connect not reported")
	}
	emit(map[string]any{"event": "invalid_token", "failed": true})
	if e = b.Close(ctx); e != nil {
		t.Fatal(e)
	}
	<-bo.done
	bp.finish()
	select {
	case e := <-bo.errs:
		t.Fatal(e)
	default:
	}
	for i := 0; i < 3; i++ {
		s, p := open(fmt.Sprintf("repeat-%d", i), 600, 1000)
		o := consume(s, 600, 1000)
		wait(func() bool { return o.markers.Load() >= 50 && p.markers.Load() >= 50 })
		// Concurrent close callers must join the same native cleanup, not dispose twice.
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for j := 0; j < 2; j++ {
			wg.Add(1)
			go func() { defer wg.Done(); errs <- s.Close(ctx) }()
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			if e != nil {
				t.Fatal(e)
			}
		}
		<-o.done
		p.finish()
		select {
		case e := <-o.errs:
			t.Fatal(e)
		default:
		}
		if p.wrong.Load() != 0 {
			t.Fatal("wrong output marker")
		}
		emit(map[string]any{"event": "repeat", "cycle": i, "pcm_seconds": float64(o.samples.Load()) / 24000, "output_markers": p.markers.Load()})
	}
	c, cp := open("shutdown", 600, 1000)
	co := consume(c, 600, 1000)
	wait(func() bool { return co.markers.Load() >= 50 && cp.markers.Load() >= 50 })
	at := time.Now()
	if e = rt.Close(ctx); e != nil {
		t.Fatal(e)
	}
	<-co.done
	cp.finish()
	select {
	case e := <-co.errs:
		t.Fatal(e)
	default:
	}
	emit(map[string]any{"event": "runtime_shutdown", "duration_ms": float64(time.Since(at)) / float64(time.Millisecond)})
	if _, e = rt.Connect(ctx, url, "unused"); !errors.Is(e, native.ErrClosed) {
		t.Fatal("connect after shutdown accepted", e)
	}
}
