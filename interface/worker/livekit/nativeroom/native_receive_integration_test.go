//go:build nativereceiveintegration

package nativeroom_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	model "github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/interface/worker"
	"github.com/hraban/opus"
	lkproto "github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

func TestStarterReceiveLocal(t *testing.T) {
	runStarterReceiveLocal(t, []string{"default", "native", "native-unpublish"})
}

func runStarterReceiveLocal(t *testing.T, modes []string) {
	url := os.Getenv("NATIVE_TEST_URL")
	if url == "" {
		t.Fatal("NATIVE_TEST_URL required; local test server only")
	}
	t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE", "1")
	t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_ROOM", "0")
	config, err := nativeReceiveFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	duration := 3 * time.Second
	if value := os.Getenv("NATIVE_TEST_DURATION"); value != "" {
		duration, err = time.ParseDuration(value)
		if err != nil || duration < 3*time.Second || duration > 2*time.Minute {
			t.Fatal("NATIVE_TEST_DURATION must be 3s..2m")
		}
	}
	for _, mode := range modes {
		enabled := mode != "default"
		t.Run(mode, func(t *testing.T) {
			roomName := fmt.Sprintf("starter-native-%d", time.Now().UnixNano())
			publisher, err := lksdk.ConnectToRoom(url, lksdk.ConnectInfo{APIKey: "devkey", APISecret: "local-native-receive-experiment-secret", RoomName: roomName, ParticipantIdentity: "caller-test"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer publisher.Disconnect()
			track, err := lksdk.NewLocalTrack(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2})
			if err != nil {
				t.Fatal(err)
			}
			published, err := publisher.LocalParticipant.PublishTrack(track, &lksdk.TrackPublicationOptions{Name: "caller", Source: lkproto.TrackSource_MICROPHONE})
			if err != nil {
				t.Fatal(err)
			}
			job := worker.NewJobContext(&lkproto.Job{Id: "native-test", Room: &lkproto.Room{Name: roomName}}, url, "devkey", "local-native-receive-experiment-secret")
			defer os.RemoveAll(job.SessionDirectory())
			server := worker.NewAgentServer(worker.WorkerOptions{WSURL: url, APIKey: "devkey", APISecret: "local-native-receive-experiment-secret"})
			var samples, energy atomic.Uint64
			var lastFrame, maxGap atomic.Int64
			session := agent.NewAgentSession(agent.NewAgent("offline native receive test"), nil, agent.AgentSessionOptions{AudioInputHook: func(_ context.Context, f *model.AudioFrame) *model.AudioFrame {
				now := time.Now().UnixNano()
				if prev := lastFrame.Swap(now); prev != 0 {
					gap := now - prev
					for old := maxGap.Load(); gap > old; old = maxGap.Load() {
						if maxGap.CompareAndSwap(old, gap) {
							break
						}
					}
				}
				samples.Add(uint64(f.SamplesPerChannel))
				for i := 0; i+1 < len(f.Data); i += 2 {
					v := int64(int16(uint16(f.Data[i]) | uint16(f.Data[i+1])<<8))
					energy.Add(uint64(v * v))
				}
				return nil // Observe real session input without invoking any provider.
			}})
			ctx, cancel := context.WithTimeout(context.Background(), duration+20*time.Second)
			defer cancel()
			config.enabled = enabled
			if err := startStarterSession(ctx, job, session, server, config); err != nil {
				job.Shutdown("test failed")
				t.Fatal(err)
			}
			defer func() {
				session.Shutdown(false)
				job.Shutdown("test complete")
				select {
				case <-job.ShutdownDone():
				case <-time.After(5 * time.Second):
					t.Error("job shutdown timeout")
				}
			}()
			encoder, err := opus.NewEncoder(48000, 1, opus.AppVoIP)
			if err != nil {
				t.Fatal(err)
			}
			pcm := make([]int16, 960)
			buf := make([]byte, 4000)
			began := time.Now()
			initialSamples := samples.Load()
			checkpointSamples := initialSamples
			checkpointTime := began
			for i := 0; i < int(duration/(20*time.Millisecond)); i++ {
				select {
				case <-job.ShutdownDone():
					t.Fatal("job ended during stream")
				default:
				}
				for j := range pcm {
					pcm[j] = int16(10000 * math.Sin(2*math.Pi*1000*float64(i*960+j)/48000))
				}
				n, err := encoder.Encode(pcm, buf)
				if err != nil {
					t.Fatal(err)
				}
				if err := track.WriteRTP(&rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: uint16(i), Timestamp: uint32(i * 960)}, Payload: append([]byte(nil), buf[:n]...)}, nil); err != nil {
					t.Fatal(err)
				}
				time.Sleep(time.Until(began.Add(time.Duration(i+1) * 20 * time.Millisecond)))
				if enabled && (i+1)%50 == 0 {
					currentSamples, currentTime := samples.Load(), time.Now()
					pcmWindow := float64(currentSamples-checkpointSamples) / 24000
					wallWindow := currentTime.Sub(checkpointTime).Seconds()
					t.Logf("continuity second=%d pcm=%.3f wall=%.3f", (i+1)/50, pcmWindow, wallWindow)
					if math.Abs(pcmWindow-wallWindow) > .25 {
						t.Errorf("one-second PCM window differs by >250ms")
					}
					checkpointSamples, checkpointTime = currentSamples, currentTime
				}
			}
			windowSamples := samples.Load() - initialSamples
			elapsed := time.Since(began)
			if enabled && math.Abs(float64(windowSamples)/24000-elapsed.Seconds()) > .5 {
				t.Fatalf("PCM/wall duration differs by >500ms: pcm=%.3f wall=%.3f", float64(windowSamples)/24000, elapsed.Seconds())
			}
			if enabled {
				t.Logf("max PCM callback gap=%.3fms", float64(maxGap.Load())/1e6)
				if maxGap.Load() > int64(250*time.Millisecond) {
					t.Error("PCM callback gap exceeded 250ms")
				}
			}
			t.Logf("measured window: pcm=%.3fs wall=%.3fs", float64(windowSamples)/24000, elapsed.Seconds())
			if samples.Load() < 24000 || energy.Load() < 1000000 {
				t.Fatalf("no usable PCM at agent input: samples=%d energy=%d", samples.Load(), energy.Load())
			}
			if enabled {
				for _, p := range job.Room.GetRemoteParticipants() {
					for _, pub := range p.TrackPublications() {
						if pub.IsSubscribed() {
							t.Fatal("Pion input subscribed in native mode")
						}
					}
				}
			}
			if mode == "native-unpublish" {
				if err := publisher.LocalParticipant.UnpublishTrack(published.SID()); err != nil {
					t.Fatal(err)
				}
				select {
				case <-job.ShutdownDone():
				case <-time.After(5 * time.Second):
					t.Fatal("native track loss did not shut down job")
				}
			}
			t.Logf("real PCM reached session: samples=%d seconds=%.3f energy=%d", samples.Load(), float64(samples.Load())/24000, energy.Load())
		})
	}
}

// One test process owns all five sessions; providers are bypassed at input.
func TestNativeSequentialSessions(t *testing.T) {
	t.Setenv("NATIVE_TEST_DURATION", "2m")
	var warmHeap uint64
	var warmGoroutines int
	for cycle := 1; cycle <= 5; cycle++ {
		t.Run(fmt.Sprintf("cycle-%d", cycle), func(t *testing.T) { runStarterReceiveLocal(t, []string{"native-unpublish"}) })
		time.Sleep(3 * time.Second)
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		goroutines := runtime.NumGoroutine()
		rss, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
		if err != nil {
			t.Fatal(err)
		}
		children, err := exec.Command("pgrep", "-P", strconv.Itoa(os.Getpid()), "-f", "probe-helper").CombinedOutput()
		if len(children) > 0 {
			t.Errorf("helper remains after cycle %d: %s", cycle, children)
		}
		if e, ok := err.(*exec.ExitError); err != nil && (!ok || e.ExitCode() != 1) {
			t.Fatalf("helper inspection: %v", err)
		}
		t.Logf("resources cycle=%d pid=%d goroutines=%d heap_alloc=%d heap_sys=%d rss_kib=%s helper_children=%q", cycle, os.Getpid(), goroutines, m.HeapAlloc, m.HeapSys, strings.TrimSpace(string(rss)), children)
		if cycle == 1 {
			warmHeap = m.HeapAlloc
			warmGoroutines = goroutines
		}
		if cycle > 1 && (m.HeapAlloc > warmHeap+16*1024*1024 || goroutines > warmGoroutines+5) {
			t.Error("settled resources exceed warm baseline tolerance: +16MiB heap or +5 goroutines")
		}
	}
}
