//go:build nativeroomintegration

package main

import (
	"context"
	"encoding/json"
	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/hraban/opus"
	pb "github.com/livekit/protocol/livekit"
	lk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func prepareLifecycleInput(t *testing.T, scenario string, caller *lk.Room, pub *lk.LocalTrackPublication, dir string) func() {
	if !strings.HasPrefix(scenario, "lifecycle-") {
		return func() {}
	}
	screen, e := lk.NewLocalTrack(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = caller.LocalParticipant.PublishTrack(screen, &lk.TrackPublicationOptions{Name: "screen", Source: pb.TrackSource_SCREEN_SHARE_AUDIO}); e != nil {
		t.Fatal(e)
	}
	if scenario == "lifecycle-muted" {
		pub.SetMuted(true)
	}
	enc, e := opus.NewEncoder(48000, 1, opus.AppVoIP)
	if e != nil {
		t.Fatal(e)
	}
	stop, done := make(chan struct{}), make(chan struct{})
	var sent int
	var publishErr error
	go func() {
		defer close(done)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		pcm := make([]int16, 960)
		buf := make([]byte, 4000)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
			for j := range pcm {
				pcm[j] = int16(10000 * math.Sin(2*math.Pi*1760*float64(i*960+j)/48000))
			}
			n, e := enc.Encode(pcm, buf)
			if e != nil {
				publishErr = e
				return
			}
			if e = screen.WriteRTP(&rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: uint16(i), Timestamp: uint32(i * 960)}, Payload: append([]byte(nil), buf[:n]...)}, nil); e != nil {
				publishErr = e
				return
			}
			sent++
		}
	}()
	return func() {
		close(stop)
		<-done
		if publishErr != nil || sent < 25 {
			t.Errorf("screen publisher sent=%d err=%v", sent, publishErr)
		}
		b, _ := json.Marshal(map[string]any{"packets_sent": sent, "tone_hz": 1760})
		if e := os.WriteFile(filepath.Join(dir, "screen-publisher.json"), b, 0600); e != nil {
			t.Error(e)
		}
	}
}
func exerciseLifecycle(t *testing.T, ctx context.Context, scenario string, caller *lk.Room, session *agent.AgentSession, track *lk.LocalTrack, pub *lk.LocalTrackPublication, dir string) (*lk.LocalTrack, *lk.LocalTrackPublication) {
	if !strings.HasPrefix(scenario, "lifecycle-") {
		return track, pub
	}
	evidence := map[string]any{"scenario": scenario, "original_track": pub.SID(), "screen_audio_published": true}
	switch scenario {
	case "lifecycle-delayed":
		sent := make(chan error, 1)
		start := time.Now()
		go func() {
			sent <- session.EnsureAssistant().(*agent.PipelineAgent).PublishAudio(ctx, primaryTone(480, 990))
		}()
		select {
		case e := <-sent:
			t.Fatal("capture completed before subscription", e)
		case <-time.After(300 * time.Millisecond):
		}
		found := false
		for _, p := range caller.GetRemoteParticipants() {
			if p.Identity() == "primary-agent" {
				for _, pub := range p.TrackPublications() {
					if e := pub.(*lk.RemoteTrackPublication).SetSubscribed(true); e != nil {
						t.Fatal(e)
					}
					found = true
				}
			}
		}
		if !found {
			t.Fatal("no output publication")
		}
		select {
		case e := <-sent:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("capture did not resume")
		}
		session.AudioPlaybackController().ClearBuffer()
		evidence["capture_wait_ms"] = float64(time.Since(start)) / float64(time.Millisecond)
	case "lifecycle-muted":
		// Reaching this point proves startup did not require an unmuted publication.
		pub.SetMuted(false)
		time.Sleep(100 * time.Millisecond)
		pub.SetMuted(true)
		time.Sleep(100 * time.Millisecond)
		pub.SetMuted(false)
		evidence["muted_startup_and_unmute"] = true
	case "lifecycle-device":
		old := pub.SID()
		if e := caller.LocalParticipant.UnpublishTrack(old); e != nil {
			t.Fatal(e)
		}
		time.Sleep(200 * time.Millisecond)
		var e error
		track, e = lk.NewLocalTrack(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2})
		if e != nil {
			t.Fatal(e)
		}
		pub, e = caller.LocalParticipant.PublishTrack(track, &lk.TrackPublicationOptions{Name: "replacement", Source: pb.TrackSource_MICROPHONE})
		if e != nil {
			t.Fatal(e)
		}
		if old == pub.SID() {
			t.Fatal("replacement SID unchanged")
		}
		evidence["replacement_track"] = pub.SID()
	}
	b, e := json.MarshalIndent(evidence, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "lifecycle.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
	return track, pub
}
