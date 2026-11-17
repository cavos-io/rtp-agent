//go:build nativeroomintegration

package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/livekit/protocol/auth"
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
	"github.com/cavos-io/rtp-agent/core/vad"
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

// Inject VAD boundaries without a transcript, using the real core recovery
// state machine and actual native output. This is not an acoustic VAD test.
func TestStarterNativePrimaryFalseRecovery(t *testing.T) {
	t.Setenv("NATIVE_ROOM_DURATION", "10s")
	runNativePrimary(t, []string{"false-recovery"})
}

func TestStarterNativePrimaryJobTermination(t *testing.T) {
	runNativePrimary(t, []string{"job-termination"})
}

func TestStarterNativePrimaryPause(t *testing.T) {
	runNativePrimary(t, []string{"pause-resume"})
}

func TestStarterNativePrimaryLocal(t *testing.T) {
	runNativePrimary(t, []string{"session-close", "publisher-disconnect", "track-unpublish"})
}
func runNativePrimary(t *testing.T, endings []string) {
	runNativePrimaryWithSignalFault(t, endings, "", nil, false)
}

// Fault injection is test-only. The native media engine and playback policies
// are unchanged, and existing PCM/content/cleanup gates still apply.
func runNativePrimaryWithSignalFault(t *testing.T, endings []string, agentURL string, fault func(context.Context) error, recovery bool) {
	url, out := os.Getenv("NATIVE_TEST_URL"), os.Getenv("NATIVE_ROOM_RESULT_DIR")
	if url == "" || out == "" {
		t.Fatal("NATIVE_TEST_URL and new NATIVE_ROOM_RESULT_DIR required")
	}
	if err := os.Mkdir(out, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE", "0")
	if os.Getenv("NATIVE_TEST_INPROCESS") == "1" {
		t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_ROOM", "0")
		t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS", "1")
	} else {
		t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_ROOM", "1")
		t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_INPROCESS", "0")
	}
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
	runNativePrimaryConfigured(t, endings, agentURL, fault, recovery, url, out, config, duration)
}

// Explicit settings let concurrent soak rooms avoid process-global environment mutation.
func runNativePrimaryConfigured(t *testing.T, endings []string, agentURL string, fault func(context.Context) error, recovery bool, url, out string, config nativeReceiveConfig, duration time.Duration) {
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
			reconnected := make(chan struct{}, 1)
			cb := lk.NewRoomCallback()
			captionCheck := newNativeCaptionCheck(t, cb)
			stateCheck := newNativeStateCheck(t, cb)
			dtmfCheck := newNativeDTMFCheck(t, cb)
			cb.OnReconnected = func() {
				select {
				case reconnected <- struct{}{}:
				default:
				}
			}
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
			caller, err := lk.ConnectToRoom(url, lk.ConnectInfo{APIKey: "devkey", APISecret: "local-native-receive-experiment-secret", RoomName: roomName, ParticipantIdentity: "caller-test"}, cb, lk.WithAutoSubscribe(ending != "lifecycle-delayed"))
			if err != nil {
				t.Fatal(err)
			}
			defer caller.Disconnect()
			captionCheck.attach(caller)
			track, err := lk.NewLocalTrack(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2})
			if err != nil {
				t.Fatal(err)
			}
			pub, err := caller.LocalParticipant.PublishTrack(track, &lk.TrackPublicationOptions{Name: "caller", Source: lkproto.TrackSource_MICROPHONE})
			if err != nil {
				t.Fatal(err)
			}
			stopScreen := prepareLifecycleInput(t, ending, caller, pub, dir)
			defer stopScreen()
			connectionURL := url
			if agentURL != "" {
				connectionURL = agentURL
			}
			server := worker.NewAgentServer(worker.WorkerOptions{WSURL: connectionURL, APIKey: "devkey", APISecret: "local-native-receive-experiment-secret", JobExecutorType: worker.JobExecutorTypeThread})
			var samples atomic.Uint64
			var last, gap atomic.Int64
			var inputEnergy atomic.Uint64
			var screenLeak atomic.Uint64
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
					if strings.HasPrefix(ending, "lifecycle-") {
						pcm := make([]int16, len(f.Data)/2)
						for i := range pcm {
							pcm[i] = int16(binary.LittleEndian.Uint16(f.Data[i*2:]))
						}
						if primaryAmplitude(pcm, 3520) > 1000 {
							screenLeak.Add(1)
						}
					}
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
			stateToken := ""
			if stateCheck.enabled {
				allowMetadata := true
				stateToken, err = auth.NewAccessToken("devkey", "local-native-receive-experiment-secret").SetIdentity("primary-agent").SetKind(lkproto.ParticipantInfo_AGENT).SetVideoGrant(&auth.VideoGrant{RoomJoin: true, Room: roomName, Agent: true, CanUpdateOwnMetadata: &allowMetadata}).SetValidFor(time.Hour).ToJWT()
				if err != nil {
					t.Fatal(err)
				}
			}
			execution := make(chan error, 1)
			go func() {
				execution <- server.ExecuteLocalJobWithOptions(ctx, roomName, "primary-agent", worker.LocalJobOptions{Token: stateToken, RoomInfo: &lkproto.Room{Name: roomName}, FakeJob: ending != "end-call", SessionDirectory: dir, SessionReportPath: filepath.Join(dir, "session_report.json"), RecordingOptions: agent.RecordingOptions{Audio: true}})
			}()
			var run running
			select {
			case run = <-ready:
			case err := <-execution:
				t.Fatalf("job exited before ready: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if run.err != nil {
				t.Fatal(run.err)
			}
			defer run.job.Shutdown("test cleanup")
			track, pub = exerciseLifecycle(t, ctx, ending, caller, run.session, track, pub, dir)
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
			captionCheck.verify(ctx, run.session, pub.SID(), dir)
			stateCheck.verify(ctx, run.session, dir)
			dtmfCheck.verify(ctx, run.session, run.job.PrimaryRoomIO(), caller, dir)
			if strings.HasPrefix(ending, "lifecycle-") {
				// Separate intentional mute/replacement gaps from the steady PCM contract.
				before := samples.Load()
				deadline := time.After(2 * time.Second)
				tick := time.NewTicker(20 * time.Millisecond)
				for samples.Load() == before {
					select {
					case <-deadline:
						tick.Stop()
						t.Fatal("no PCM after lifecycle change")
					case <-tick.C:
					}
				}
				tick.Stop()
				b, _ := json.Marshal(map[string]float64{"lifecycle_max_gap_ms": float64(gap.Load()) / 1e6})
				if e := os.WriteFile(filepath.Join(dir, "lifecycle-gap.json"), b, 0600); e != nil {
					t.Fatal(e)
				}
				gap.Store(0)
				last.Store(0)
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
				checkpointEnergy := inputEnergy.Load()
				for i := 0; i < int(duration/(20*time.Millisecond)); i++ {
					select {
					case <-ctx.Done():
						return
					default:
					}
					if ending == "lifecycle-muted" {
						if i == 100 {
							pub.SetMuted(true)
						}
						if i == 150 {
							pub.SetMuted(false)
						}
					}
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
						windows = append(windows, map[string]float64{"second": float64((i + 1) / 50), "pcm_seconds": pcmWindow, "wall_seconds": wallWindow, "energy": float64(inputEnergy.Load() - checkpointEnergy)})
						if !recovery && math.Abs(pcmWindow-wallWindow) > .25 {
							t.Errorf("one-second PCM window differs by >250ms: pcm=%.3f wall=%.3f", pcmWindow, wallWindow)
						}
						checkpointSamples, checkpointTime = current, currentTime
						checkpointEnergy = inputEnergy.Load()
					}
				}
			}()
			// Failure injection can end a test before publishing finishes.
			// Join before returning so callbacks cannot report into a closed test.
			defer func() { cancel(); pubWG.Wait() }()
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
			var reconnectMS float64
			if fault != nil || ending == "publisher-signal-reconnect" {
				faultCtx, stopFault := context.WithTimeout(ctx, 5*time.Second)
				faultStart := time.Now()
				if fault != nil {
					err = fault(faultCtx)
				} else {
					caller.Simulate(lk.SimulateSignalReconnect)
					select {
					case <-reconnected:
						err = nil
					case <-faultCtx.Done():
						err = faultCtx.Err()
					}
				}
				stopFault()
				reconnectMS = float64(time.Since(faultStart)) / float64(time.Millisecond)
				if err != nil {
					t.Fatalf("signaling recovery: %v", err)
				}
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
			var recoveryMS float64
			if ending == "pause-resume" || ending == "false-recovery" {
				control := run.session.AudioOutputController()
				if control == nil || !control.CanPauseAudioOutput() {
					t.Fatal("native output controller not wired")
				}
				// Queue real native output before pausing, without weakening the
				// preceding hard-clear gate or the resumed content gate.
				for i := 0; i < 4; i++ {
					if err := pipeline.PublishAudio(ctx, primaryTone(1200, 990)); err != nil {
						t.Fatal(err)
					}
				}
				var activity *agent.AgentActivity
				var speech *agent.SpeechHandle
				var events <-chan agent.AgentFalseInterruptionEvent
				if ending == "false-recovery" {
					// Provider-free controller session: the PCM still travels
					// through the running starter's native output and caller.
					a := agent.NewAgent("controlled false interruption")
					recovery := agent.NewAgentSession(a, nil, agent.AgentSessionOptions{
						ResumeFalseInterruption: true, ResumeFalseInterruptionSet: true,
						FalseInterruptionTimeout: 2, FalseInterruptionTimeoutSet: true,
					})
					recovery.SetAudioOutputController(control)
					recovery.SetAudioPlaybackController(playback)
					activity = agent.NewAgentActivity(a, recovery)
					activity.Start()
					defer activity.Stop()
					speech = agent.NewSpeechHandle(true, agent.DefaultInputDetails())
					defer speech.MarkDone()
					if err := activity.ScheduleSpeech(speech, 0, false); err != nil {
						t.Fatal(err)
					}
					for activity.CurrentSpeech() != speech {
						select {
						case <-ctx.Done():
							t.Fatal("speech not scheduled")
						default:
							time.Sleep(time.Millisecond)
						}
					}
					recovery.UpdateAgentState(agent.AgentStateSpeaking)
					events = recovery.AgentFalseInterruptionEvents()
					activity.OnStartOfSpeech(&vad.VADEvent{Type: vad.VADEventStartOfSpeech})
				} else {
					control.PauseAudioOutput()
				}
				pending := make(chan error, 1)
				go func() { pending <- pipeline.PublishAudio(ctx, primaryTone(1200, 1320)) }()
				// Allow 300ms for already encoded/in-flight packets, then require
				// no old or resumed marker during a 200ms paused interval.
				time.Sleep(300 * time.Millisecond)
				old, next := tone990.Load(), tone1320.Load()
				select {
				case err := <-pending:
					t.Fatalf("publication escaped pause: %v", err)
				case <-time.After(200 * time.Millisecond):
				}
				if tone990.Load() != old || tone1320.Load() != next {
					t.Fatal("marker audio continued while paused")
				}
				if activity != nil {
					endedAt := time.Now()
					activity.OnEndOfSpeech(&vad.VADEvent{Type: vad.VADEventEndOfSpeech})
					select {
					case ev := <-events:
						if !ev.Resumed {
							t.Fatal("false interruption did not resume")
						}
					case <-ctx.Done():
						t.Fatal("no recovery event")
					}
					recoveryMS = float64(time.Since(endedAt)) / float64(time.Millisecond)
					if recoveryMS < 1900 || recoveryMS > 3000 {
						t.Fatalf("two-second recovery timer outside 1.9..3s: %.1fms", recoveryMS)
					}
					if speech.IsInterrupted() || activity.CurrentSpeech() != speech {
						t.Fatal("recovery replaced speech")
					}
				} else {
					control.ResumeAudioOutput()
				}
				select {
				case err := <-pending:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("resume blocked")
				}
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
			if recovery {
				// Separate full-restart recovery contract. The original no-gap
				// test remains unchanged and must not be reported as passing.
				if math.Abs(pcm-wall.Seconds()) > 3 || gap.Load() > int64(3*time.Second) {
					t.Errorf("restart gap exceeds3s: pcm=%.3f wall=%.3f gap=%.3fms", pcm, wall.Seconds(), float64(gap.Load())/1e6)
				}
				if len(windows) < 3 {
					t.Fatal("missing post-recovery windows")
				}
				for _, w := range windows[len(windows)-3:] {
					if math.Abs(w["pcm_seconds"]-w["wall_seconds"]) > .25 || w["energy"] < 1000000 {
						t.Errorf("post-recovery PCM/content failed: %+v", w)
					}
				}
			} else if math.Abs(pcm-wall.Seconds()) > .25 || gap.Load() > int64(250*time.Millisecond) {
				t.Errorf("continuity pcm=%.3f wall=%.3f gap=%.3fms", pcm, wall.Seconds(), float64(gap.Load())/1e6)
			}
			if inputEnergy.Load() < 1000000 || tone990.Load() < 20 || tone1320.Load() < 20 {
				t.Fatalf("missing content input=%d output=%d/%d", inputEnergy.Load(), tone990.Load(), tone1320.Load())
			}
			if screenLeak.Load() != 0 {
				t.Errorf("screen-share marker reached input: %d frames", screenLeak.Load())
			}
			stop := time.Now()
			switch ending {
			case "end-call":
				dtmfCheck.endCall(ctx, run.job.PrimaryRoomIO())
			case "job-termination":
				run.session.AudioOutputController().PauseAudioOutput()
				pending := make(chan error, 1)
				go func() { pending <- pipeline.PublishAudio(ctx, primaryTone(1200, 1320)) }()
				select {
				case err := <-pending:
					t.Fatalf("pause escaped before job shutdown: %v", err)
				case <-time.After(20 * time.Millisecond):
				}
				run.job.Shutdown("controlled job termination during pause")
				select {
				case err := <-pending:
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("shutdown publish: %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("paused producer not released by shutdown")
				}
			case "publisher-disconnect":
				caller.Disconnect()
			case "track-unpublish", "track-republish":
				if err := caller.LocalParticipant.UnpublishTrack(pub.SID()); err != nil {
					t.Fatal(err)
				}
				if ending == "track-republish" {
					replacement, e := lk.NewLocalTrack(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2})
					if e != nil {
						t.Fatal(e)
					}
					replacementPub, e := caller.LocalParticipant.PublishTrack(replacement, &lk.TrackPublicationOptions{Name: "replacement", Source: lkproto.TrackSource_MICROPHONE})
					if e != nil {
						t.Fatal(e)
					}
					if replacementPub.SID() == pub.SID() {
						t.Fatal("replacement reused track SID")
					}
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
			if err != nil && ending != "end-call" {
				t.Fatal(err)
			}
			if ending == "end-call" {
				rooms, e := api.ListRooms(ctx, &lkproto.ListRoomsRequest{Names: []string{roomName}})
				if e != nil || len(rooms.Rooms) != 0 {
					t.Fatal("room not deleted", e)
				}
				after = &lkproto.ListParticipantsResponse{}
			}
			want := 1
			if ending == "publisher-disconnect" || ending == "end-call" {
				want = 0
			}
			if len(after.Participants) != want {
				t.Fatalf("remaining participants=%d want=%d", len(after.Participants), want)
			}
			cleanup, settled := run.job.PrimaryRoomIO().(*nativeroom.Room).CleanupDiagnostics()
			if config.inProcess {
				if !settled || cleanup.Mode != "inprocess" || cleanup.Reaped || cleanup.KillAttempted || !cleanup.ShutdownSeen || !cleanup.DisconnectCallback || !cleanup.HandlesReleased || cleanup.RuntimeDisposed {
					t.Errorf("in-process cleanup failed: settled=%v %+v", settled, cleanup)
				}
			} else if !settled || !cleanup.Reaped || cleanup.ExitCode != 0 || cleanup.KillAttempted || !cleanup.ShutdownSeen || !cleanup.DisconnectCallback || !cleanup.HandlesReleased || !cleanup.RuntimeDisposed || cleanup.CallbackOverflow {
				t.Errorf("helper cleanup failed: settled=%v %+v", settled, cleanup)
			}
			metrics := map[string]any{"full_restart_recovery_contract": recovery, "signaling_recovery_ms": reconnectMS, "false_recovery_ms": recoveryMS, "helper_cleanup": cleanup, "continuity_windows": windows, "input_pcm_seconds": pcm, "wall_seconds": wall.Seconds(), "max_input_gap_ms": float64(gap.Load()) / 1e6, "output_990_frames": tone990.Load(), "output_1320_frames": tone1320.Load(), "participants_during": 2, "participants_after": len(after.Participants), "cleanup_ms": float64(time.Since(stop)) / 1e6}
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
