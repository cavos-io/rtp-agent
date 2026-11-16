//go:build nativeinprocess

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	native "native-receive-probe/nativeinprocess"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeWrapperCompatibility(t *testing.T) {
	serverURL, out := os.Getenv("PROBE_INPROCESS_URL"), os.Getenv("PROBE_INPROCESS_OUT")
	target, e := url.Parse(strings.Replace(serverURL, "ws://", "http://", 1))
	if e != nil || target.Hostname() != "127.0.0.1" {
		t.Fatal("local URL required")
	}
	if e = os.Mkdir(out, 0755); e != nil {
		t.Fatal(e)
	}
	log, e := os.Create(filepath.Join(out, "summary.jsonl"))
	if e != nil {
		t.Fatal(e)
	}
	defer log.Close()
	emit := func(v any) {
		t.Helper()
		if e := json.NewEncoder(log).Encode(v); e != nil {
			t.Fatal(e)
		}
	}
	conns := make(chan io.ReadCloser, 8)
	var blocked atomic.Bool
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ModifyResponse = func(r *http.Response) error {
		if r.StatusCode == 101 {
			select {
			case conns <- r.Body:
			default:
				return errors.New("signal connection burst")
			}
		}
		return nil
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if blocked.Load() {
			http.Error(w, "controlled outage", 503)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	rt, e := native.Open(os.Getenv("LIVEKIT_LIB_PATH"))
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := rt.Close(ctx); e != nil {
			t.Error(e)
		}
	}()
	room := fmt.Sprintf("wrapper-compat-%d", time.Now().UnixNano())
	pub := startProcessPublisher(t, serverURL, room, 600, 1000)
	jwt, e := token(room, "native-agent")
	if e != nil {
		t.Fatal(e)
	}
	s, e := rt.Connect(ctx, strings.Replace(server.URL, "http://", "ws://", 1), jwt)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.WaitReady(ctx); e != nil {
		t.Fatal(e)
	}
	var samples, last, maxGap, markers atomic.Int64
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		for f := range s.Frames() {
			samples.Add(int64(len(f.PCM)))
			if prev := last.Swap(f.At); prev != 0 && f.At-prev > maxGap.Load() {
				maxGap.Store(f.At - prev)
			}
			if processAmplitude(f.PCM, 24000, 600) > 1000 {
				markers.Add(1)
			}
		}
	}()
	wait := func(f func() bool) {
		t.Helper()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for !f() {
			select {
			case <-s.Done():
				t.Fatal("session ended", s.Err())
			case <-ctx.Done():
				t.Fatal("test timeout")
			case <-ticker.C:
			}
		}
	}
	startProducer := func(hz float64) (context.CancelFunc, chan error) {
		run, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			frame := make([]int16, 480)
			for i := range frame {
				frame[i] = int16(10000 * math.Sin(2*math.Pi*hz*float64(i)/24000))
			}
			tick := time.NewTicker(20 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-run.Done():
					done <- run.Err()
					return
				case <-tick.C:
					if e := s.Capture(run, frame); e != nil {
						done <- e
						return
					}
				}
			}
		}()
		return stop, done
	}
	stop, producerDone := startProducer(600)
	defer stop()
	wait(func() bool { return markers.Load() >= 30 && pub.markers.Load() >= 30 })
	if e = s.Pause(ctx); e != nil {
		t.Fatal(e)
	}
	time.Sleep(250 * time.Millisecond) // allow already transmitted packets/decoder tail to drain
	before, pcmBefore := pub.markers.Load(), samples.Load()
	time.Sleep(300 * time.Millisecond)
	pausedGrowth := pub.markers.Load() - before
	if pausedGrowth > 2 {
		t.Fatalf("output continued during pause: %d marker frames", pausedGrowth)
	}
	if samples.Load()-pcmBefore < 6000 {
		t.Fatal("pause blocked input receive")
	}
	if e = s.Resume(ctx); e != nil {
		t.Fatal(e)
	}
	wait(func() bool { return pub.markers.Load() >= before+25 })
	emit(map[string]any{"event": "false_interruption_recovery", "paused_marker_growth": pausedGrowth, "resumed_markers": pub.markers.Load() - before, "same_producer": true})
	// Pause again, hard-clear pending content, and start a new marker producer.
	if e = s.Pause(ctx); e != nil {
		t.Fatal(e)
	}
	time.Sleep(60 * time.Millisecond)
	if e = s.Clear(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-producerDone:
		if !errors.Is(e, context.Canceled) {
			t.Fatal("old producer not interrupted", e)
		}
	case <-ctx.Done():
		t.Fatal("old capture stuck")
	}
	stop()
	stopNew, newDone := startProducer(1000)
	defer stopNew()
	wait(func() bool { return pub.wrong.Load() >= 25 })
	emit(map[string]any{"event": "hard_interruption", "new_marker_frames": pub.wrong.Load()})
	// Independent native signaling outage; caller media stays active/unmuted.
	var original io.ReadCloser
	select {
	case original = <-conns:
	case <-ctx.Done():
		t.Fatal("initial websocket missing")
	}
	baselineSamples := samples.Load()
	baselineMarkers := pub.wrong.Load()
	outageAt := time.Now()
	maxGap.Store(0)
	blocked.Store(true)
	if e = original.Close(); e != nil {
		t.Fatal(e)
	}
	time.Sleep(2 * time.Second)
	blocked.Store(false)
	select {
	case <-conns:
	case <-ctx.Done():
		t.Fatal("replacement websocket missing")
	}
	wait(func() bool { return pub.wrong.Load() >= baselineMarkers+50 && samples.Load() >= baselineSamples+24000 })
	// Require resumed PCM and content after the new WebSocket, not merely buffered output.
	after := samples.Load()
	afterOutput := pub.wrong.Load()
	recoveredAt := time.Now()
	time.Sleep(2 * time.Second)
	growth := float64(samples.Load()-after) / 24000
	elapsed := time.Since(recoveredAt).Seconds()
	if math.Abs(growth-elapsed) > .25 || pub.wrong.Load()-afterOutput < 50 {
		t.Fatal("reconnected media failed sustained recovery", growth, elapsed)
	}
	gap := float64(maxGap.Load()) / 1e6
	emit(map[string]any{"event": "signaling_outage", "outage_seconds": 2, "elapsed_seconds": time.Since(outageAt).Seconds(), "max_pcm_gap_ms": gap, "original_3s_gap_gate_pass": gap <= 3000, "recovered_pcm_seconds": growth, "recovered_wall_seconds": elapsed, "post_reconnect_output_markers": pub.wrong.Load() - afterOutput})
	stopNew()
	select {
	case e := <-newDone:
		if !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("output producer stuck")
	}
	if e = s.Close(ctx); e != nil {
		t.Fatal(e)
	}
	<-consumerDone
	if e = rt.Close(ctx); e != nil {
		t.Fatal(e)
	}
	pub.finish()
	emit(map[string]any{"event": "shutdown", "clean": true})
}
