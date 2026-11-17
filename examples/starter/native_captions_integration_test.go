//go:build nativeroomintegration

package main

import (
	"context"
	"encoding/json"
	"github.com/cavos-io/rtp-agent/core/agent"
	wl "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lk "github.com/livekit/server-sdk-go/v2"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type nativeCaptionObservation struct {
	Text, Identity, Track, Segment string
	Final                          bool
}
type nativeCaptionCheck struct {
	t       *testing.T
	enabled bool
	packets chan nativeCaptionObservation
	streams chan nativeCaptionObservation
}

func newNativeCaptionCheck(t *testing.T, cb *lk.RoomCallback) *nativeCaptionCheck {
	c := &nativeCaptionCheck{t: t, enabled: os.Getenv("NATIVE_TEST_CAPTIONS") == "1", packets: make(chan nativeCaptionObservation, 32), streams: make(chan nativeCaptionObservation, 32)}
	if c.enabled {
		cb.OnTranscriptionReceived = func(segments []*lk.TranscriptionSegment, p lk.Participant, track lk.TrackPublication) {
			for _, s := range segments {
				v := nativeCaptionObservation{Text: s.Text, Segment: s.ID, Final: s.Final}
				if p != nil {
					v.Identity = p.Identity()
				}
				if track != nil {
					v.Track = track.SID()
				}
				select {
				case c.packets <- v:
				default:
				}
			}
		}
	}
	return c
}
func (c *nativeCaptionCheck) attach(room *lk.Room) {
	if !c.enabled {
		return
	}
	if e := room.RegisterTextStreamHandler(wl.RoomIOTranscriptionTopic, func(reader *lk.TextStreamReader, identity string) {
		go func() {
			v := nativeCaptionObservation{Text: reader.ReadAll(), Final: reader.Info.Attributes[wl.RoomIOTranscriptionFinalAttribute] == "true", Identity: identity, Track: reader.Info.Attributes[wl.RoomIOTranscriptionTrackIDAttribute], Segment: reader.Info.Attributes[wl.RoomIOTranscriptionSegmentIDAttribute]}
			select {
			case c.streams <- v:
			default:
			}
		}()
	}); e != nil {
		c.t.Fatal(e)
	}
}
func (c *nativeCaptionCheck) verify(ctx context.Context, s *agent.AgentSession, callerTrack, dir string) {
	if !c.enabled {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var observations []nativeCaptionObservation
	packet := func(text, who string, final bool) nativeCaptionObservation {
		select {
		case p := <-c.packets:
			if p.Text != text || p.Identity != who || p.Final != final || p.Track == "" || p.Segment == "" {
				c.t.Fatalf("caption packet: %+v", p)
			}
			observations = append(observations, p)
			return p
		case <-ctx.Done():
			c.t.Fatal("caption packet timeout", ctx.Err())
			return nativeCaptionObservation{}
		}
	}
	s.EmitUserInputTranscribed(agent.UserInputTranscribedEvent{Transcript: "caller interim", Language: "en"})
	a := packet("caller interim", "caller-test", false)
	s.EmitUserInputTranscribed(agent.UserInputTranscribedEvent{Transcript: "caller final", Language: "en", IsFinal: true})
	b := packet("caller final", "caller-test", true)
	if a.Segment != b.Segment || b.Track != callerTrack {
		c.t.Fatal("caller segment/track changed")
	}
	s.EmitAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{Transcript: "agent ", Language: "en"})
	a = packet("agent ", "primary-agent", false)
	s.EmitAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{Transcript: "caption", Language: "en"})
	b = packet("agent caption", "primary-agent", false)
	if a.Segment != b.Segment {
		c.t.Fatal("agent segment changed")
	}
	s.EmitAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{Transcript: "agent caption", Language: "en", IsFinal: true})
	b = packet("agent caption", "primary-agent", true)
	expected := map[string]bool{"caller interim": false, "caller final": false, "agent caption": false}
	for i := 0; i < 3; i++ {
		select {
		case v := <-c.streams:
			if _, ok := expected[v.Text]; !ok || expected[v.Text] || v.Identity != "primary-agent" || v.Track == "" || v.Segment == "" {
				c.t.Fatalf("caption stream: %+v", v)
			}
			expected[v.Text] = true
			observations = append(observations, v)
		case <-ctx.Done():
			c.t.Fatal("caption stream timeout", ctx.Err())
		}
	}
	data, e := json.MarshalIndent(observations, "", "  ")
	if e != nil {
		c.t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "captions.json"), data, 0600); e != nil {
		c.t.Fatal(e)
	}
}
