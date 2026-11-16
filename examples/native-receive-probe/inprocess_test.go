//go:build nativeinprocess

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hraban/opus"
	lkproto "github.com/livekit/protocol/livekit"
	lk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

func processAmplitude(pcm []int16, rate int, hz float64) float64 {
	var re, im float64
	for i, v := range pcm {
		a := 2 * math.Pi * hz * float64(i) / float64(rate)
		re += float64(v) * math.Cos(a)
		im += float64(v) * math.Sin(a)
	}
	return 2 * math.Hypot(re, im) / float64(len(pcm))
}

func TestProcessRequestRouting(t *testing.T) {
	p := newProcessRuntime()
	a, b := &processRoom{name: "a"}, &processRoom{name: "b"}
	x, y := p.id(a, "connect"), p.id(b, "connect")
	if x == y {
		t.Fatal("duplicate request id")
	}
	if r, e := p.resolve(y, "connect"); e != nil || r != b {
		t.Fatal("misrouted B", e)
	}
	if _, e := p.resolve(x, "disconnect"); e == nil {
		t.Fatal("accepted wrong callback kind")
	}
	if r, e := p.resolve(x, "connect"); e != nil || r != a {
		t.Fatal("misrouted A", e)
	}
	if _, e := p.resolve(x, "connect"); e == nil {
		t.Fatal("accepted duplicate callback")
	}
}

type processPublisher struct {
	room                 *lk.Room
	stop                 context.CancelFunc
	sent, markers, wrong atomic.Int64
	done                 chan error
	readers              sync.WaitGroup
	err                  chan error
	finish               func()
}

func startProcessPublisher(t *testing.T, url, name string, hz, other float64) *processPublisher {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	p := &processPublisher{stop: cancel, done: make(chan error, 1), err: make(chan error, 8)}
	cb := lk.NewRoomCallback()
	cb.OnTrackSubscribed = func(track *webrtc.TrackRemote, _ *lk.RemoteTrackPublication, _ *lk.RemoteParticipant) {
		p.readers.Add(1)
		go func() {
			defer p.readers.Done()
			decoder, e := opus.NewDecoder(48000, 1)
			if e != nil {
				p.err <- e
				return
			}
			pcm := make([]int16, 5760)
			for {
				packet, _, e := track.ReadRTP()
				if e != nil {
					return
				}
				n, e := decoder.Decode(packet.Payload, pcm)
				if e != nil {
					p.err <- e
					return
				}
				own, alien := processAmplitude(pcm[:n], 48000, hz), processAmplitude(pcm[:n], 48000, other)
				if own > 1000 && own > alien*3 {
					p.markers.Add(1)
				}
				if alien > 1000 && alien > own*3 {
					p.wrong.Add(1)
				}
			}
		}()
	}
	var e error
	p.room, e = lk.ConnectToRoom(url, lk.ConnectInfo{APIKey: localKey, APISecret: localSecret, RoomName: name, ParticipantIdentity: "caller"}, cb)
	if e != nil {
		cancel()
		t.Fatal(e)
	}
	track, e := lk.NewLocalTrack(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2})
	if e != nil {
		t.Fatal(e)
	}
	_, e = p.room.LocalParticipant.PublishTrack(track, &lk.TrackPublicationOptions{Name: "controlled-tone", Source: lkproto.TrackSource_MICROPHONE})
	if e != nil {
		t.Fatal(e)
	}
	go func() {
		enc, e := opus.NewEncoder(48000, 1, opus.AppVoIP)
		if e != nil {
			p.done <- e
			return
		}
		pcm := make([]int16, 960)
		for i := range pcm {
			pcm[i] = int16(10000 * math.Sin(2*math.Pi*hz*float64(i)/48000))
		}
		buf := make([]byte, 4000)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		var seq uint16
		var ts uint32
		for {
			select {
			case <-ctx.Done():
				p.done <- nil
				return
			case <-ticker.C:
				n, e := enc.Encode(pcm, buf)
				if e == nil {
					e = track.WriteRTP(&rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: seq, Timestamp: ts}, Payload: append([]byte(nil), buf[:n]...)}, nil)
				}
				if e != nil {
					p.done <- e
					return
				}
				seq++
				ts += 960
				p.sent.Add(1)
			}
		}
	}()
	p.finish = sync.OnceFunc(func() {
		p.stop()
		p.room.Disconnect()
		select {
		case e := <-p.done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(3 * time.Second):
			t.Error("publisher did not stop")
		}
		p.readers.Wait()
	})
	t.Cleanup(p.finish)
	return p
}

// No helpers are spawned. Both native room connections, Pion test publishers,
// and the PCM consumer execute in this Go test process.
func TestNativeInProcessRooms(t *testing.T) {
	url, out := os.Getenv("PROBE_INPROCESS_URL"), os.Getenv("PROBE_INPROCESS_OUT")
	if url == "" || out == "" {
		t.Fatal("local PROBE_INPROCESS_URL and new PROBE_INPROCESS_OUT required")
	}
	if e := os.Mkdir(out, 0755); e != nil {
		t.Fatal(e)
	}
	log, e := os.Create(filepath.Join(out, "events.jsonl"))
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
	snapshot := func(name string) int {
		t.Helper()
		f, e := os.Create(filepath.Join(out, name+".stacks"))
		if e != nil {
			t.Fatal(e)
		}
		var stacks bytes.Buffer
		if e = pprof.Lookup("goroutine").WriteTo(&stacks, 2); e != nil {
			t.Fatal(e)
		}
		if _, e = f.Write(stacks.Bytes()); e != nil {
			t.Fatal(e)
		}
		f.Close()
		foreign, regular := classifyStacks(stacks.String())
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		emit(map[string]any{"event": "snapshot", "name": name, "pid": os.Getpid(), "goroutines": runtime.NumGoroutine(), "go_owned": len(regular), "foreign_contexts": len(foreign), "heap_alloc": m.HeapAlloc, "callbacks": len(callbacks.events)})
		return len(regular)
	}
	baseline := snapshot("baseline")
	if e = startNative(); e != nil {
		t.Fatal(e)
	}
	disposed := false
	defer func() {
		if !disposed {
			disposeNative()
		}
	}()
	p := newProcessRuntime()
	pump := func(done func() bool, timeout time.Duration) {
		t.Helper()
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		for !done() {
			select {
			case raw := <-callbacks.events:
				if e := p.dispatch(raw); e != nil {
					t.Fatal(e)
				}
				if callbacks.overflow.Load() {
					t.Fatal("global callback overflow")
				}
			case <-timer.C:
				t.Fatal("lifecycle/measurement deadline exceeded")
			}
		}
	}
	window := func(d time.Duration) {
		t.Helper()
		until := time.Now().Add(d)
		pump(func() bool { return time.Now().After(until) }, d+time.Second)
	}
	newRoom := func(label string, hz, other float64) (*processRoom, *processPublisher) {
		t.Helper()
		name := fmt.Sprintf("inprocess-%s-%d", label, time.Now().UnixNano())
		pub := startProcessPublisher(t, url, name, hz, other)
		r := &processRoom{name: label}
		pcmFile, e := os.Create(filepath.Join(out, label+".pcm"))
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { pcmFile.Close() })
		r.onPCM = func(at int64, pcm []int16) error {
			own, alien := processAmplitude(pcm, 24000, hz), processAmplitude(pcm, 24000, other)
			if alien > 1000 && alien > own*3 {
				return fmt.Errorf("room %s received other room marker", label)
			}
			if own > 1000 && own > alien*3 {
				r.markers++
			}
			data := make([]byte, len(pcm)*2)
			for i, v := range pcm {
				binary.LittleEndian.PutUint16(data[i*2:], uint16(v))
			}
			_, e := pcmFile.Write(data)
			emit(map[string]any{"event": "pcm", "room": label, "at": at, "samples": len(pcm), "own_amplitude": own, "other_amplitude": alien})
			return e
		}
		jwt, e := token(name, "native-agent")
		if e != nil {
			t.Fatal(e)
		}
		if e = p.connect(url, jwt, r); e != nil {
			t.Fatal(e)
		}
		return r, pub
	}
	check := func(r *processRoom, pub *processPublisher) {
		t.Helper()
		duration := float64(r.samples) / 24000
		wall := float64(r.last-r.first)/1e9 + .02
		emit(map[string]any{"event": "room_summary", "room": r.name, "pcm_seconds": duration, "wall_seconds": wall, "max_gap_ms": float64(r.maxGap) / 1e6, "input_markers": r.markers, "output_markers": pub.markers.Load(), "wrong_output_markers": pub.wrong.Load(), "rtp_sent": pub.sent.Load(), "capture_callbacks": r.captures})
		if math.Abs(duration-wall) > .25 || r.maxGap > 250_000_000 {
			t.Errorf("room %s continuity: pcm=%f wall=%f maxgap=%f", r.name, duration, wall, float64(r.maxGap)/1e6)
		}
		if r.markers < 50 || pub.markers.Load() < 50 || pub.wrong.Load() != 0 {
			t.Errorf("room %s content failed", r.name)
		}
		select {
		case e := <-pub.err:
			t.Error(e)
		default:
		}
	}
	closeRoom := func(r *processRoom) {
		t.Helper()
		at := time.Now()
		if e = p.closeRoom(r); e != nil {
			t.Fatal(e)
		}
		pump(func() bool { return r.closed }, 3*time.Second)
		emit(map[string]any{"event": "room_closed", "room": r.name, "duration_ms": float64(time.Since(at)) / float64(time.Millisecond), "remaining_rooms": len(p.rooms)})
	}
	a, ap := newRoom("A", 600, 1000)
	b, bp := newRoom("B", 1000, 600)
	pump(func() bool {
		return a.markers >= 25 && b.markers >= 25 && ap.markers.Load() >= 25 && bp.markers.Load() >= 25
	}, 15*time.Second)
	window(3 * time.Second)
	check(a, ap)
	before, outputBefore := b.samples, bp.markers.Load()
	start := time.Now()
	closeRoom(a)
	window(3 * time.Second)
	growth := float64(b.samples-before) / 24000
	elapsed := time.Since(start).Seconds()
	emit(map[string]any{"event": "survivor", "pcm_seconds": growth, "wall_seconds": elapsed, "output_markers": bp.markers.Load() - outputBefore})
	if math.Abs(growth-elapsed) > .25 || bp.markers.Load()-outputBefore < 50 {
		t.Fatal("surviving room stalled after other room closed")
	}
	check(b, bp)
	closeRoom(b)
	ap.finish()
	bp.finish()
	for i := 0; i < 5; i++ {
		r, pub := newRoom(fmt.Sprintf("repeat-%d", i), 600, 1000)
		pump(func() bool { return r.markers >= 25 && pub.markers.Load() >= 25 }, 15*time.Second)
		window(2 * time.Second)
		check(r, pub)
		closeRoom(r)
		pub.finish()
		runtime.GC()
		time.Sleep(100 * time.Millisecond)
		snapshot(fmt.Sprintf("cycle-%d", i))
	}
	// Worker shutdown begins with two live rooms; drain rooms before global disposal.
	c, cp := newRoom("shutdown-C", 600, 1000)
	d, dp := newRoom("shutdown-D", 1000, 600)
	pump(func() bool {
		return c.markers >= 50 && d.markers >= 50 && cp.markers.Load() >= 50 && dp.markers.Load() >= 50
	}, 15*time.Second)
	check(c, cp)
	check(d, dp)
	shutdownAt := time.Now()
	if e = p.closeRoom(c); e != nil {
		t.Fatal(e)
	}
	if e = p.closeRoom(d); e != nil {
		t.Fatal(e)
	}
	pump(func() bool { return c.closed && d.closed && len(p.pending) == 0 }, 3*time.Second)
	if len(p.rooms) != 0 || len(p.streams) != 0 {
		t.Fatal("room/stream handles retained")
	}
	disposeNative()
	disposed = true
	// Dispose must quiesce native callbacks; release any already queued event handles.
	for len(callbacks.events) > 0 {
		if e = p.dispatch(<-callbacks.events); e != nil {
			t.Fatal(e)
		}
	}
	emit(map[string]any{"event": "worker_shutdown", "duration_ms": float64(time.Since(shutdownAt)) / float64(time.Millisecond), "rooms": len(p.rooms), "streams": len(p.streams), "pending": len(p.pending), "overflow": callbacks.overflow.Load()})
	cp.finish()
	dp.finish()
	runtime.GC()
	time.Sleep(time.Second)
	if snapshot("disposed") != baseline {
		t.Error("Go-owned goroutines did not return to baseline")
	}
}
