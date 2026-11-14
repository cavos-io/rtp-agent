//go:build nativecycles

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"testing"
	"time"
)

type cycleSnapshot struct {
	NativeThreads   []nativeThread    `json:"native_threads,omitempty"`
	CallbackThreads map[string]uint64 `json:"callback_threads,omitempty"`
	Name            string            `json:"name"`
	PID             int               `json:"pid"`
	NumGoroutine    int               `json:"num_goroutine"`
	ForeignIDs      []string          `json:"foreign_ids"`
	GoIDs           []string          `json:"go_ids"`
	QueuedCallbacks int               `json:"queued_callbacks"`
	HeapAlloc       uint64            `json:"go_heap_alloc"`
}

// Explicit local integration test, compiled only with -tags nativecycles.
func TestNativeCycles(t *testing.T) {
	url := os.Getenv("PROBE_CYCLE_URL")
	if url == "" {
		t.Fatal("set PROBE_CYCLE_URL for local native lifecycle experiment")
	}
	dir := os.Getenv("PROBE_CYCLE_OUT")
	if dir == "" {
		t.Fatal("PROBE_CYCLE_OUT required")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	cycles := 100
	if v := os.Getenv("PROBE_CYCLES"); v != "" {
		var err error
		cycles, err = strconv.Atoi(v)
		if err != nil || cycles < 1 {
			t.Fatal("invalid PROBE_CYCLES")
		}
	}
	lifetime := os.Getenv("PROBE_RUNTIME_LIFETIME")
	if lifetime == "" {
		lifetime = "process"
	}
	if lifetime != "process" && lifetime != "cycle" {
		t.Fatal("invalid runtime lifetime")
	}
	requireAudio := os.Getenv("PROBE_REQUIRE_AUDIO") != "0"
	log, err := os.Create(filepath.Join(dir, "snapshots.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	snapshot := func(name string) cycleSnapshot {
		t.Helper()
		var stacks bytes.Buffer
		if err := pprof.Lookup("goroutine").WriteTo(&stacks, 2); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".stacks"), stacks.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		foreign, regular := classifyStacks(stacks.String())
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		s := cycleSnapshot{Name: name, PID: os.Getpid(), NumGoroutine: runtime.NumGoroutine(), ForeignIDs: foreign, GoIDs: regular, QueuedCallbacks: len(callbacks.events), HeapAlloc: mem.HeapAlloc}
		if os.Getenv("PROBE_NATIVE_THREADS") == "1" {
			s.NativeThreads, err = nativeThreads()
			if err != nil {
				t.Fatal(err)
			}
			s.CallbackThreads = callbackThreadSnapshot()
		}
		if err := json.NewEncoder(log).Encode(s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	inspectCallbacks.Store(true)
	defer inspectCallbacks.Store(false)
	baseline := snapshot("baseline")
	disposed := false
	if lifetime == "process" {
		if err := startNative(); err != nil {
			t.Fatal(err)
		}
		// Register fallback disposal before any fatal assertion; normal completion also disposes.
		defer func() {
			if !disposed {
				disposeNative()
			}
		}()
		snapshot("initialized")
	}
	maxWarmForeign := 0
	for i := 0; i < cycles; i++ {
		name := fmt.Sprintf("cycle-%03d", i)
		sink, err := newRecorder(dir, name)
		if err != nil {
			t.Fatal(err)
		}
		jwt, err := token("native-cycle-test", "cycle-receiver")
		if err != nil {
			t.Fatal(err)
		}
		before := runtime.NumGoroutine()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if lifetime == "cycle" {
			err = nativeReceive(ctx, url, jwt, sink)
		} else {
			err = nativeRoom(ctx, url, jwt, sink, false, before)
		}
		cancel()
		samples := sink.offset
		closeErr := sink.close()
		snapshot(name + "-immediate")
		if err != nil || closeErr != nil {
			t.Fatalf("cycle %d: %v / %v", i, err, closeErr)
		}
		if requireAudio && samples == 0 {
			t.Fatalf("cycle %d received no PCM", i)
		}
		// This wait samples teardown; it does not alter receive/playout policy.
		time.Sleep(100 * time.Millisecond)
		s := snapshot(name + "-settled")
		if s.QueuedCallbacks != 0 {
			t.Fatalf("cycle %d retained %d callbacks", i, s.QueuedCallbacks)
		}
		if len(s.GoIDs) != len(baseline.GoIDs) {
			t.Fatalf("cycle %d: Go-owned stacks %v vs baseline %v; inspect stacks", i, s.GoIDs, baseline.GoIDs)
		}
		if i < 20 && len(s.ForeignIDs) > maxWarmForeign {
			maxWarmForeign = len(s.ForeignIDs)
		}
		if i >= 20 && len(s.ForeignIDs) > maxWarmForeign {
			t.Errorf("foreign-context count grew after warmup: %d > %d", len(s.ForeignIDs), maxWarmForeign)
		}
		if (i+1)%10 == 0 || i+1 == cycles {
			t.Logf("%s %d/%d: Go-owned=%d foreign=%d queued=%d samples=%d", lifetime, i+1, cycles, len(s.GoIDs), len(s.ForeignIDs), s.QueuedCallbacks, samples)
		}
	}
	if lifetime == "process" {
		disposeNative()
		disposed = true
	}
	snapshot("final-immediate")
	time.Sleep(time.Second)
	snapshot("final-1s")
	time.Sleep(4 * time.Second)
	last := snapshot("final-5s")
	if len(last.GoIDs) != len(baseline.GoIDs) || last.QueuedCallbacks != 0 {
		t.Fatal("final Go-owned stacks or callbacks did not return to baseline")
	}
}
