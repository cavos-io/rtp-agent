//go:build nativeinprocess && nativeroomintegration

package nativeroom

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	model "github.com/cavos-io/rtp-agent/core/audio/model"
	wl "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	"github.com/hraban/opus"
	"github.com/livekit/protocol/auth"
	pb "github.com/livekit/protocol/livekit"
	lk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeRemoteRecordingLive(t *testing.T) {
	url, lib, out := os.Getenv("NATIVE_TEST_URL"), os.Getenv("LIVEKIT_LIB_PATH"), os.Getenv("NATIVE_RECORDING_RESULTS")
	if url == "" || lib == "" || out == "" {
		t.Fatal("NATIVE_TEST_URL, LIVEKIT_LIB_PATH and new NATIVE_RECORDING_RESULTS required")
	}
	if err := os.Mkdir(out, 0755); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := CloseInProcessRuntime(ctx); err != nil {
			t.Error(err)
		}
	}()
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			name := fmt.Sprintf("native-record-%d", time.Now().UnixNano())
			join := func(id string) *lk.Room {
				t.Helper()
				r, err := lk.ConnectToRoom(url, lk.ConnectInfo{APIKey: "devkey", APISecret: "local-native-receive-experiment-secret", RoomName: name, ParticipantIdentity: id}, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(r.Disconnect)
				return r
			}
			caller, other := join("caller"), join("other")
			publish := func(room *lk.Room) (*lk.LocalTrack, *lk.LocalTrackPublication) {
				t.Helper()
				tr, err := lk.NewLocalTrack(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2})
				if err != nil {
					t.Fatal(err)
				}
				pub, err := room.LocalParticipant.PublishTrack(tr, &lk.TrackPublicationOptions{Source: pb.TrackSource_MICROPHONE})
				if err != nil {
					t.Fatal(err)
				}
				return tr, pub
			}
			primary, primaryPub := publish(caller)
			aux, _ := publish(other)
			token, err := auth.NewAccessToken("devkey", "local-native-receive-experiment-secret").SetIdentity("native-agent").SetVideoGrant(&auth.VideoGrant{RoomJoin: true, Room: name}).ToJWT()
			if err != nil {
				t.Fatal(err)
			}
			recorder := wl.NewRecorderIO(nil)
			path := filepath.Join(out, fmt.Sprintf("record-%t.ogg", enabled))
			if err := recorder.Start(path, 24000); err != nil {
				t.Fatal(err)
			}
			defer recorder.Stop()
			var primaryFrames, auxFrames, leaks atomic.Int64
			cfg := Config{URL: url, Token: token, Library: lib, Participant: "caller", TrackSID: primaryPub.SID()}
			if enabled {
				cfg.recordAux = func(sid string, f *model.AudioFrame) { auxFrames.Add(1); recorder.RecordAux(sid, f) }
			}
			client, err := launchInProcess(ctx, cfg, func(_ context.Context, f *model.AudioFrame) {
				primaryFrames.Add(1)
				recorder.RecordInput(f)
				if recordingAmplitude(f.Data, 24000, 1760) > 500 {
					leaks.Add(1)
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			defer client.close()
			select {
			case <-client.ready:
			case <-ctx.Done():
				t.Fatal("native startup timeout")
			case <-client.done:
				t.Fatal(client.err)
			}
			send := func(track *lk.LocalTrack, hz float64, duration time.Duration) <-chan error {
				done := make(chan error, 1)
				go func() {
					enc, err := opus.NewEncoder(48000, 1, opus.AppVoIP)
					if err != nil {
						done <- err
						return
					}
					buf := make([]byte, 4000)
					pcm := make([]int16, 960)
					tick := time.NewTicker(20 * time.Millisecond)
					defer tick.Stop()
					for i := 0; i < int(duration/(20*time.Millisecond)); i++ {
						select {
						case <-ctx.Done():
							done <- ctx.Err()
							return
						case <-tick.C:
						}
						for j := range pcm {
							pcm[j] = int16(10000 * math.Sin(2*math.Pi*hz*float64(i*960+j)/48000))
						}
						n, err := enc.Encode(pcm, buf)
						if err == nil {
							err = track.WriteRTP(&rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: uint16(i), Timestamp: uint32(i * 960)}, Payload: append([]byte(nil), buf[:n]...)}, nil)
						}
						if err != nil {
							done <- err
							return
						}
					}
					done <- nil
				}()
				return done
			}
			primaryDone, auxDone := send(primary, 440, 4*time.Second), send(aux, 1760, 2*time.Second)
			if err := <-auxDone; err != nil {
				t.Fatal(err)
			}
			before := primaryFrames.Load()
			other.Disconnect()
			if err := <-primaryDone; err != nil {
				t.Fatal(err)
			}
			if primaryFrames.Load()-before < 50 || leaks.Load() != 0 {
				t.Fatal("aux leaked into input or departure stopped primary", primaryFrames.Load(), before, leaks.Load())
			}
			if enabled && auxFrames.Load() < 50 || !enabled && auxFrames.Load() != 0 {
				t.Fatal("recording enabled/disabled mismatch", auxFrames.Load())
			}
			client.close()
			if !client.cleanup.HandlesReleased {
				t.Fatal("native handles not released", client.err)
			}
			if err := recorder.Stop(); err != nil {
				t.Fatal(err)
			}
			data, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", path, "-f", "s16le", "-ac", "1", "-ar", "24000", "pipe:1").Output()
			if err != nil {
				t.Fatal(err)
			}
			peak := 0.0
			for start := 0; start+960 <= len(data); start += 960 {
				peak = math.Max(peak, recordingAmplitude(data[start:start+960], 24000, 1760))
			}
			if enabled && peak < 500 || !enabled && peak > 100 {
				t.Fatal("final recording marker mismatch", peak)
			}
			result := map[string]any{"record_remote": enabled, "primary_frames": primaryFrames.Load(), "aux_frames": auxFrames.Load(), "input_marker_leaks": leaks.Load(), "recording_aux_peak": peak, "cleanup": client.cleanup}
			b, _ := json.MarshalIndent(result, "", "  ")
			if err := os.WriteFile(filepath.Join(out, fmt.Sprintf("summary-%t.json", enabled)), b, 0600); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func recordingAmplitude(data []byte, rate int, hz float64) float64 {
	var a, b float64
	n := len(data) / 2
	for i := 0; i < n; i++ {
		v := float64(int16(binary.LittleEndian.Uint16(data[2*i:])))
		angle := 2 * math.Pi * hz * float64(i) / float64(rate)
		a += v * math.Cos(angle)
		b += v * math.Sin(angle)
	}
	return 2 * math.Hypot(a, b) / float64(n)
}
