//go:build nativereceiveintegration

package main

import (
	"context"
	"fmt"
	"math"
	"os"
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
	url := os.Getenv("NATIVE_TEST_URL")
	if url == "" {
		t.Fatal("NATIVE_TEST_URL required; local test server only")
	}
	t.Setenv("RTP_AGENT_EXPERIMENTAL_NATIVE_RECEIVE", "1")
	config, err := nativeReceiveFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"default", "native", "native-unpublish"} {
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
			session := agent.NewAgentSession(agent.NewAgent("offline native receive test"), nil, agent.AgentSessionOptions{AudioInputHook: func(_ context.Context, f *model.AudioFrame) *model.AudioFrame {
				samples.Add(uint64(f.SamplesPerChannel))
				for i := 0; i+1 < len(f.Data); i += 2 {
					v := int64(int16(uint16(f.Data[i]) | uint16(f.Data[i+1])<<8))
					energy.Add(uint64(v * v))
				}
				return nil // Observe real session input without invoking any provider.
			}})
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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
			for i := 0; i < 150; i++ {
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
			}
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
