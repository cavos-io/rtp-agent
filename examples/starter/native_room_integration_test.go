//go:build nativeroomintegration

package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	model "github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/examples/internal/nativeroom"
	"github.com/cavos-io/rtp-agent/interface/worker"
	logutil "github.com/cavos-io/rtp-agent/library/logger"
	"github.com/hraban/opus"
	lkproto "github.com/livekit/protocol/livekit"
	protologger "github.com/livekit/protocol/logger"
	lk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

func primaryTone(n int, hz float64) *model.AudioFrame {
	f := &model.AudioFrame{SampleRate: 24000, NumChannels: 1, SamplesPerChannel: uint32(n), Data: make([]byte, n*2)}
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint16(f.Data[i*2:], uint16(int16(10000*math.Sin(2*math.Pi*hz*float64(i)/24000))))
	}
	return f
}
func primaryAmplitude(pcm []int16, hz float64) float64 {
	var a, b float64
	for i, v := range pcm {
		angle := 2 * math.Pi * hz * float64(i) / 48000
		a += float64(v) * math.Cos(angle)
		b += float64(v) * math.Sin(angle)
	}
	return 2 * math.Hypot(a, b) / float64(len(pcm))
}
func TestStarterNativePrimaryLocal(t *testing.T) {
	runNativePrimary(t, []string{"session-close", "publisher-disconnect", "track-unpublish"})
}
func runNativePrimary(t *testing.T, endings []string) {
	url, out := os.Getenv("NATIVE_TEST_URL"), os.Getenv("NATIVE_ROOM_RESULT_DIR")
	if url == "" || out == "" {
		t.Fatal("NATIVE_TEST_URL and new NATIVE_ROOM_RESULT_DIR required")
	}
	if err := os.Mkdir(out, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE", "0")
	t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_ROOM", "1")
	config, err := nativeReceiveFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	duration := 5 * time.Second
	if value := os.Getenv("NATIVE_ROOM_DURATION"); value != "" {
		duration, err = time.ParseDuration(value)
		if err != nil || duration < 5*time.Second || duration > 2*time.Minute {
			t.Fatal("NATIVE_ROOM_DURATION must be 5s..2m")
		}
	}
	for _, ending := range endings {
		t.Run(ending, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), duration+35*time.Second)
			defer cancel()
			dir := filepath.Join(out, ending)
			if err := os.Mkdir(dir, 0755); err != nil {
				t.Fatal(err)
			}
			roomName := fmt.Sprintf("starter-primary-%d", time.Now().UnixNano())
			var tone990, tone1320 atomic.Int64
			subscribed := make(chan struct{}, 1)
			cb := lk.NewRoomCallback()
			cb.OnTrackSubscribed = func(track *webrtc.TrackRemote, _ *lk.RemoteTrackPublication, p *lk.RemoteParticipant) {
				if p.Identity() != "primary-agent" {
					return
				}
				subscribed <- struct{}{}
				go func() {
					decoder, e := opus.NewDecoder(48000, 1)
					if e != nil {
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
							return
						}
						if primaryAmplitude(pcm[:n], 990) > 1000 {
							tone990.Add(1)
						}
						if primaryAmplitude(pcm[:n], 1320) > 1000 {
							tone1320.Add(1)
						}
					}
				}()
			}
			caller, err := lk.ConnectToRoom(url, lk.ConnectInfo{APIKey: "devkey", APISecret: "local-native-receive-experiment-secret", RoomName: roomName, ParticipantIdentity: "caller-test"}, cb)
			if err != nil {
				t.Fatal(err)
			}
			defer caller.Disconnect()
			track, err := lk.NewLocalTrack(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2})
			if err != nil {
				t.Fatal(err)
			}
			pub, err := caller.LocalParticipant.PublishTrack(track, &lk.TrackPublicationOptions{Name: "caller", Source: lkproto.TrackSource_MICROPHONE})
			if err != nil {
				t.Fatal(err)
			}
			server := worker.NewAgentServer(worker.WorkerOptions{WSURL: url, APIKey: "devkey", APISecret: "local-native-receive-experiment-secret", JobExecutorType: worker.JobExecutorTypeThread})
			var samples atomic.Uint64
			var last, gap atomic.Int64
			var inputEnergy atomic.Uint64
			type running struct {
				job     *worker.JobContext
				session *agent.AgentSession
				err     error
			}
			ready := make(chan running, 1)
			if err := server.RTCSession(func(job *worker.JobContext) error {
				session := agent.NewAgentSession(agent.NewAgent("offline primary native room"), job.Room, agent.AgentSessionOptions{AudioInputHook: func(_ context.Context, f *model.AudioFrame) *model.AudioFrame {
					now := time.Now().UnixNano()
					if prev := last.Swap(now); prev != 0 {
						for old := gap.Load(); now-prev > old; old = gap.Load() {
							if gap.CompareAndSwap(old, now-prev) {
								break
							}
						}
					}
					samples.Add(uint64(f.SamplesPerChannel))
					for i := 0; i < len(f.Data); i += 2 {
						v := int64(int16(binary.LittleEndian.Uint16(f.Data[i:])))
						inputEnergy.Add(uint64(v * v))
					}
					return nil
				}})
				err := startStarterSession(ctx, job, session, server, config)
				ready <- running{job, session, err}
				return err
			}, nil, nil); err != nil {
				t.Fatal(err)
			}
			execution := make(chan error, 1)
			go func() {
				execution <- server.ExecuteLocalJobWithOptions(ctx, roomName, "primary-agent", worker.LocalJobOptions{FakeJob: true, SessionDirectory: dir, SessionReportPath: filepath.Join(dir, "session_report.json"), RecordingOptions: agent.RecordingOptions{Audio: true}})
			}()
			var run running
			select {
			case run = <-ready:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if run.err != nil {
				t.Fatal(run.err)
			}
			defer run.job.Shutdown("test cleanup")
			if run.job.RoomConnected() || run.session.Room != nil {
				t.Fatal("Pion room connected")
			}
			select {
			case <-subscribed:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			api := lk.NewRoomServiceClient(url, "devkey", "local-native-receive-experiment-secret")
			participants, err := api.ListParticipants(ctx, &lkproto.ListParticipantsRequest{Room: roomName})
			if err != nil {
				t.Fatal(err)
			}
			if len(participants.Participants) != 2 {
				t.Fatalf("participants=%d", len(participants.Participants))
			}
			var foundAgent bool
			for _, p := range participants.Participants {
				if p.Identity == "primary-agent" && p.Kind == lkproto.ParticipantInfo_AGENT {
					foundAgent = true
				}
			}
			if !foundAgent {
				t.Fatal("assigned agent identity absent")
			}
			started := time.Now()
			initial := samples.Load()
			var windows []map[string]float64
			var pubWG sync.WaitGroup
			pubWG.Add(1)
			publisherErr := make(chan error, 1)
			go func() {
				defer pubWG.Done()
				encoder, e := opus.NewEncoder(48000, 1, opus.AppVoIP)
				if e != nil {
					publisherErr <- e
					return
				}
				buf := make([]byte, 4000)
				pcm := make([]int16, 960)
				checkpointSamples, checkpointTime := samples.Load(), started
				for i := 0; i < int(duration/(20*time.Millisecond)); i++ {
					for j := range pcm {
						pcm[j] = int16(10000 * math.Sin(2*math.Pi*660*float64(i*960+j)/48000))
						if i >= 100 && i < 150 {
							pcm[j] = 0
						}
					}
					n, e := encoder.Encode(pcm, buf)
					if e != nil {
						publisherErr <- e
						return
					}
					if e = track.WriteRTP(&rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: uint16(i), Timestamp: uint32(i * 960)}, Payload: append([]byte(nil), buf[:n]...)}, nil); e != nil {
						publisherErr <- e
						return
					}
					time.Sleep(time.Until(started.Add(time.Duration(i+1) * 20 * time.Millisecond)))
					if (i+1)%50 == 0 {
						current, currentTime := samples.Load(), time.Now()
						pcmWindow, wallWindow := float64(current-checkpointSamples)/24000, currentTime.Sub(checkpointTime).Seconds()
						windows = append(windows, map[string]float64{"second": float64((i + 1) / 50), "pcm_seconds": pcmWindow, "wall_seconds": wallWindow})
						if math.Abs(pcmWindow-wallWindow) > .25 {
							t.Errorf("one-second PCM window differs by >250ms: pcm=%.3f wall=%.3f", pcmWindow, wallWindow)
						}
						checkpointSamples, checkpointTime = current, currentTime
					}
				}
			}()
			pipeline := run.session.EnsureAssistant().(*agent.PipelineAgent)
			playback := run.session.AudioPlaybackController()
			for i := 0; i < 20; i++ {
				if err := pipeline.PublishAudio(ctx, primaryTone(1200, 990)); err != nil {
					t.Fatal(err)
				}
			}
			playback.(interface{ Flush() }).Flush()
			result, err := playback.WaitForPlayout(ctx)
			if err != nil || result.Interrupted || result.PlaybackPosition != time.Second {
				t.Fatalf("drain %+v %v", result, err)
			}
			for i := 0; i < 4; i++ {
				if err := pipeline.PublishAudio(ctx, primaryTone(1200, 990)); err != nil {
					t.Fatal(err)
				}
			}
			playback.ClearBuffer()
			result, err = playback.WaitForPlayout(ctx)
			if err != nil || !result.Interrupted || result.PlaybackPosition >= 200*time.Millisecond {
				t.Fatalf("clear %+v %v", result, err)
			}
			for i := 0; i < 20; i++ {
				if err := pipeline.PublishAudio(ctx, primaryTone(1200, 1320)); err != nil {
					t.Fatal(err)
				}
			}
			playback.(interface{ Flush() }).Flush()
			if _, err = playback.WaitForPlayout(ctx); err != nil {
				t.Fatal(err)
			}
			pubWG.Wait()
			select {
			case err := <-publisherErr:
				t.Fatal(err)
			default:
			}
			wall := time.Since(started)
			pcm := float64(samples.Load()-initial) / 24000
			if math.Abs(pcm-wall.Seconds()) > .25 || gap.Load() > int64(250*time.Millisecond) {
				t.Fatalf("continuity pcm=%.3f wall=%.3f gap=%.3fms", pcm, wall.Seconds(), float64(gap.Load())/1e6)
			}
			if inputEnergy.Load() < 1000000 || tone990.Load() < 20 || tone1320.Load() < 20 {
				t.Fatalf("missing content input=%d output=%d/%d", inputEnergy.Load(), tone990.Load(), tone1320.Load())
			}
			stop := time.Now()
			switch ending {
			case "publisher-disconnect":
				caller.Disconnect()
			case "track-unpublish":
				if err := caller.LocalParticipant.UnpublishTrack(pub.SID()); err != nil {
					t.Fatal(err)
				}
			default:
				run.session.Shutdown(false)
				run.job.Shutdown("test session close")
			}
			select {
			case <-run.job.ShutdownDone():
			case <-time.After(5 * time.Second):
				t.Fatal("job shutdown timeout")
			}
			select {
			case err := <-execution:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("execution did not settle")
			}
			after, err := api.ListParticipants(ctx, &lkproto.ListParticipantsRequest{Room: roomName})
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if ending == "publisher-disconnect" {
				want = 0
			}
			if len(after.Participants) != want {
				t.Fatalf("remaining participants=%d want=%d", len(after.Participants), want)
			}
			cleanup, settled := run.job.PrimaryRoomIO().(*nativeroom.Room).CleanupDiagnostics()
			if !settled || !cleanup.Reaped || cleanup.ExitCode != 0 || cleanup.KillAttempted || !cleanup.ShutdownSeen || !cleanup.DisconnectCallback || !cleanup.HandlesReleased || !cleanup.RuntimeDisposed || cleanup.CallbackOverflow {
				t.Errorf("helper cleanup failed: settled=%v %+v", settled, cleanup)
			}
			metrics := map[string]any{"helper_cleanup": cleanup, "continuity_windows": windows, "input_pcm_seconds": pcm, "wall_seconds": wall.Seconds(), "max_input_gap_ms": float64(gap.Load()) / 1e6, "output_990_frames": tone990.Load(), "output_1320_frames": tone1320.Load(), "participants_during": 2, "participants_after": len(after.Participants), "cleanup_ms": float64(time.Since(stop)) / 1e6}
			data, _ := json.MarshalIndent(metrics, "", "  ")
			if err := os.WriteFile(filepath.Join(dir, "summary.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			t.Log(string(data))
		})
	}
}

// All five sessions execute sequentially inside this single Go test process.
func TestNativePrimarySequentialSessions(t *testing.T) {
	protologger.InitFromConfig(&protologger.Config{Level: "info", JSON: true}, "native-sequential")
	logutil.SetLogger(protologger.GetLogger())
	root := os.Getenv("NATIVE_ROOM_RESULT_DIR")
	if root == "" {
		t.Fatal("NATIVE_ROOM_RESULT_DIR required")
	}
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NATIVE_ROOM_DURATION", "2m")
	var warmHeap uint64
	var warmGoroutines int
	var resources []map[string]any
	for cycle := 1; cycle <= 5; cycle++ {
		t.Setenv("NATIVE_ROOM_RESULT_DIR", filepath.Join(root, fmt.Sprintf("cycle-%d", cycle)))
		ending := []string{"session-close", "publisher-disconnect", "track-unpublish", "session-close", "track-unpublish"}[cycle-1]
		ok := t.Run(fmt.Sprintf("cycle-%d", cycle), func(t *testing.T) { runNativePrimary(t, []string{ending}) })
		time.Sleep(3 * time.Second)
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		goroutines := runtime.NumGoroutine()
		rss, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
		if err != nil {
			t.Fatal(err)
		}
		children, err := exec.Command("pgrep", "-P", strconv.Itoa(os.Getpid()), "-f", filepath.Base(os.Getenv("RTP_AGENT_NATIVE_HELPER"))).CombinedOutput()
		if len(children) > 0 {
			t.Errorf("helper remains: %s", children)
		}
		if e, yes := err.(*exec.ExitError); err != nil && (!yes || e.ExitCode() != 1) {
			t.Fatal(err)
		}
		if cycle == 1 {
			warmHeap = m.HeapAlloc
			warmGoroutines = goroutines
		}
		if m.HeapAlloc > warmHeap+16*1024*1024 || goroutines > warmGoroutines+5 {
			t.Error("settled resources exceed warm baseline tolerance: +16MiB heap or +5 goroutines")
		}
		r := map[string]any{"cycle": cycle, "pid": os.Getpid(), "goroutines": goroutines, "heap_alloc": m.HeapAlloc, "heap_sys": m.HeapSys, "rss_kib": strings.TrimSpace(string(rss)), "helper_children": strings.TrimSpace(string(children))}
		resources = append(resources, r)
		t.Logf("resources %+v", r)
		data, _ := json.MarshalIndent(resources, "", "  ")
		if err := os.WriteFile(filepath.Join(root, "resources.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatal("session failed; results preserved")
		}
	}
}
