//go:build nativeinprocess && nativeroomintegration

package nativeroom_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/interface/worker"
	wl "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	"github.com/cavos-io/rtp-agent/interface/worker/livekit/nativeroom"
	lk "github.com/livekit/server-sdk-go/v2"
)

func TestStarterInProcessNative(t *testing.T) {
	t.Setenv("NATIVE_TEST_INPROCESS", "1")
	t.Setenv("NATIVE_ROOM_DURATION", "10s")
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := nativeroom.CloseInProcessRuntime(ctx); err != nil {
			t.Error(err)
		}
	}()
	runNativePrimary(t, []string{"session-close", "false-recovery", "publisher-disconnect"})
}

func TestStarterInProcessNativeCaptions(t *testing.T) {
	t.Setenv("NATIVE_TEST_INPROCESS", "1")
	t.Setenv("NATIVE_TEST_CAPTIONS", "1")
	t.Setenv("NATIVE_ROOM_DURATION", "5s")
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := nativeroom.CloseInProcessRuntime(ctx); e != nil {
			t.Error(e)
		}
	}()
	runNativePrimary(t, []string{"session-close"})
}

func TestStarterInProcessNativeClientState(t *testing.T) {
	t.Setenv("NATIVE_TEST_INPROCESS", "1")
	t.Setenv("NATIVE_TEST_STATE", "1")
	t.Setenv("NATIVE_ROOM_DURATION", "5s")
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := nativeroom.CloseInProcessRuntime(ctx); e != nil {
			t.Error(e)
		}
	}()
	runNativePrimary(t, []string{"session-close"})
}

func TestStarterInProcessNativeDTMF(t *testing.T) {
	t.Setenv("NATIVE_TEST_INPROCESS", "1")
	t.Setenv("NATIVE_TEST_DTMF", "1")
	t.Setenv("NATIVE_ROOM_DURATION", "5s")
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := nativeroom.CloseInProcessRuntime(ctx); e != nil {
			t.Error(e)
		}
	}()
	runNativePrimary(t, []string{"end-call"})
}

func TestStarterInProcessNativeAudioLifecycle(t *testing.T) {
	t.Setenv("NATIVE_TEST_INPROCESS", "1")
	t.Setenv("NATIVE_ROOM_DURATION", "5s")
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := nativeroom.CloseInProcessRuntime(ctx); e != nil {
			t.Error(e)
		}
	}()
	runNativePrimary(t, []string{"lifecycle-delayed", "lifecycle-muted", "lifecycle-device"})
}

func TestStarterInProcessNativeChat(t *testing.T) {
	t.Setenv("NATIVE_TEST_INPROCESS", "1")
	t.Setenv("NATIVE_TEST_CHAT", "1")
	t.Setenv("NATIVE_ROOM_DURATION", "5s")
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := nativeroom.CloseInProcessRuntime(ctx); err != nil {
			t.Error(err)
		}
	}()
	runNativePrimary(t, []string{"session-close"})
}

func startChatTestSession(ctx context.Context, job *worker.JobContext, session *agent.AgentSession, server *worker.AgentServer, cfg nativeReceiveConfig, received chan<- wl.TextInputEvent) error {
	transport := nativeroom.NewInProcess(job, session, nativeroom.Config{Library: cfg.library}, server.Options.APIKey, server.Options.APISecret)
	return job.StartSession(ctx, session, worker.StartSessionOptions{Transport: transport, RoomOptions: wl.RoomOptions{DisablePreConnectAudio: true, TextInputCallback: func(ctx context.Context, _ *agent.AgentSession, ev wl.TextInputEvent) error {
		select {
		case received <- ev:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}})
}
func verifyNativeChat(t *testing.T, ctx context.Context, caller *lk.Room, received <-chan wl.TextInputEvent, dir string) {
	t.Helper()
	sent := caller.LocalParticipant.SendText("hello 世界", lk.StreamTextOptions{Topic: "lk.chat", Attributes: map[string]string{"test": "native-chat"}})
	select {
	case ev := <-received:
		if ev.Text != "hello 世界" || ev.ParticipantIdentity != "caller-test" || ev.NativeInfo.GetStreamId() != sent.Id || ev.NativeInfo.GetTopic() != "lk.chat" || ev.NativeInfo.GetAttributes()["test"] != "native-chat" {
			t.Fatalf("chat mismatch: %+v", ev)
		}
		data, err := json.MarshalIndent(ev, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "chat.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("chat delivery timeout", ctx.Err())
	}
}

// Real starter rooms share one FFI runtime. Test settings are immutable once lanes start.
func TestStarterInProcessConcurrentSoak(t *testing.T) {
	root, url := os.Getenv("NATIVE_ROOM_RESULT_DIR"), os.Getenv("NATIVE_TEST_URL")
	if root == "" || url == "" {
		t.Fatal("new result directory and local URL required")
	}
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE", "0")
	t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_ROOM", "0")
	t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS", "1")
	cfg, err := nativeReceiveFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	duration := 15 * time.Minute
	if v := os.Getenv("NATIVE_SOAK_DURATION"); v != "" {
		duration, err = time.ParseDuration(v)
		if err != nil || duration < 50*time.Second {
			t.Fatal("duration must be >=50s")
		}
	}
	started := time.Now()
	type sample struct {
		Seconds    float64
		Goroutines int
		Heap       uint64
		RSSKiB     int
		Threads    int
		FDs        int
	}
	var samples []sample
	measure := func() {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		pid := strconv.Itoa(os.Getpid())
		raw, e := exec.Command("ps", "-o", "rss=", "-p", pid).Output()
		if e != nil {
			t.Error(e)
			return
		}
		rss, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
		raw, e = exec.Command("ps", "-M", "-p", pid).Output()
		if e != nil {
			t.Error(e)
			return
		}
		threads := len(strings.Split(strings.TrimSpace(string(raw)), "\n")) - 1
		raw, e = exec.Command("lsof", "-a", "-p", pid, "-Ff").Output()
		if e != nil {
			t.Error(e)
			return
		}
		fds := 0
		for _, line := range strings.Split(string(raw), "\n") {
			if len(line) > 1 && line[0] == 'f' {
				if _, e := strconv.Atoi(line[1:]); e == nil {
					fds++
				}
			}
		}
		samples = append(samples, sample{time.Since(started).Seconds(), runtime.NumGoroutine(), m.HeapAlloc, rss, threads, fds})
		data, _ := json.MarshalIndent(samples, "", "  ")
		if e := os.WriteFile(filepath.Join(root, "resources.json"), data, 0600); e != nil {
			t.Error(e)
		}
	}
	measure()
	done := make(chan struct{})
	monitored := make(chan struct{})
	go func() {
		defer close(monitored)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				measure()
			}
		}
	}()
	var wg sync.WaitGroup
	for lane := 0; lane < 3; lane++ {
		wg.Add(1)
		go func(lane int) {
			defer wg.Done()
			t.Run(fmt.Sprintf("lane-%d", lane), func(t *testing.T) {
				cycles := 5
				span := duration / 5
				if lane == 0 {
					cycles = 1
					span = duration
				}
				endings := []string{"false-recovery", "publisher-disconnect", "track-unpublish", "pause-resume", "session-close"}
				for cycle := 0; cycle < cycles; cycle++ {
					out := filepath.Join(root, fmt.Sprintf("lane-%d-cycle-%d", lane, cycle))
					if e := os.Mkdir(out, 0755); e != nil {
						t.Fatal(e)
					}
					runNativePrimaryConfigured(t, []string{endings[(cycle+lane)%len(endings)]}, "", nil, false, url, out, cfg, span)
					if t.Failed() {
						return
					}
				}
			})
		}(lane)
	}
	wg.Wait()
	close(done)
	<-monitored
	time.Sleep(5 * time.Second)
	measure()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e := nativeroom.CloseInProcessRuntime(ctx); e != nil {
		t.Error(e)
	}
	time.Sleep(5 * time.Second)
	measure()
	// Compare warmed, equivalent active-session samples; allow native/race allocator caching.
	// These are soak gates, not a claim that RSS must return to the cold baseline.
	if len(samples) >= 5 {
		warm := samples[2]
		last := samples[len(samples)-3]
		if last.Heap > warm.Heap+32*1024*1024 || last.Goroutines > warm.Goroutines+30 || last.RSSKiB > warm.RSSKiB+256*1024 || last.FDs > warm.FDs+30 || last.Threads > warm.Threads+20 {
			t.Errorf("resource growth exceeds soak gates: warm=%+v late=%+v", warm, last)
		}
		settled := samples[len(samples)-1]
		if settled.Goroutines > warm.Goroutines || settled.FDs > warm.FDs {
			t.Errorf("resources did not settle: warm=%+v final=%+v", warm, settled)
		}
	}
}
