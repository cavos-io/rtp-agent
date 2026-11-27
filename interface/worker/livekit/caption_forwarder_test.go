package livekit

import (
	"context"
	"github.com/cavos-io/rtp-agent/core/agent"
	lk "github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"testing"
	"time"
)

func TestCaptionForwarderSessionEventsAndClose(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	packets := make(chan *lk.Transcription, 16)
	texts := make(chan string, 16)
	f := NewCaptionForwarder(session, CaptionTransport{AgentIdentity: "agent", AgentTrack: "out", CallerIdentity: "caller", CallerTrack: "in", PublishPacket: func(p *lk.Transcription) error { packets <- p; return nil }, PublishText: func(s string, o lksdk.StreamTextOptions) {
		if o.Topic != RoomIOTranscriptionTopic {
			t.Error("topic", o.Topic)
		}
		texts <- s
	}, Usable: func() bool { return true }})
	read := func() *lk.Transcription {
		select {
		case p := <-packets:
			return p
		case <-time.After(time.Second):
			t.Fatal("missing caption")
			return nil
		}
	}
	session.EmitUserInputTranscribed(agent.UserInputTranscribedEvent{Transcript: "hel", IsFinal: false})
	a := read()
	session.EmitUserInputTranscribed(agent.UserInputTranscribedEvent{Transcript: "hello", IsFinal: true})
	b := read()
	if a.TrackId != "in" || b.TranscribedParticipantIdentity != "caller" || a.Segments[0].Id != b.Segments[0].Id || !b.Segments[0].Final {
		t.Fatal(a, b)
	}
	session.EmitAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{Transcript: "good ", IsFinal: false})
	a = read()
	session.EmitAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{Transcript: "day", IsFinal: false})
	b = read()
	if b.Segments[0].Text != "good day" || a.Segments[0].Id != b.Segments[0].Id || b.TrackId != "out" || b.TranscribedParticipantIdentity != "agent" {
		t.Fatal(a, b)
	}
	session.EmitAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{Transcript: "good day", IsFinal: true})
	if p := read(); !p.Segments[0].Final {
		t.Fatal(p)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e := f.Close(ctx); e != nil {
		t.Fatal(e)
	}
	if e := f.Close(ctx); e != nil {
		t.Fatal(e)
	}
	// Closed forwarding cannot publish new events, even if the session continues.
	f.rio.handleUserInputTranscribed(agent.UserInputTranscribedEvent{Transcript: "late", IsFinal: true})
	select {
	case p := <-packets:
		t.Fatal("publication after close", p)
	default:
	}
}

// Caller captions leave the agent's segment intact. The native forwarder shares
// the default RoomIO semantics without the retired caption-weaving option.
func TestCaptionForwarderPreservesAgentSegmentAcrossCallerCaptions(t *testing.T) {
	packets := make(chan *lk.Transcription, 16)
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	f := NewCaptionForwarder(session, CaptionTransport{AgentIdentity: "agent", AgentTrack: "out", CallerIdentity: "caller", CallerTrack: "in", PublishPacket: func(p *lk.Transcription) error { packets <- p; return nil }})
	defer f.Close(context.Background())
	f.rio.handleAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{Transcript: "hello "})
	first := <-packets
	f.rio.handleUserInputTranscribed(agent.UserInputTranscribedEvent{Transcript: "yes", IsFinal: true})
	caller := <-packets
	if first.Segments[0].Final || caller.TrackId != "in" || !caller.Segments[0].Final || caller.Segments[0].Id == first.Segments[0].Id {
		t.Fatal("caption ordering")
	}
	f.rio.handleAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{Transcript: "again"})
	resumed := <-packets
	f.rio.handleAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{Transcript: "hello again", IsFinal: true})
	final := <-packets
	if resumed.Segments[0].Id != first.Segments[0].Id || resumed.Segments[0].Text != "hello again" || resumed.Segments[0].Id != final.Segments[0].Id || final.Segments[0].Text != "hello again" || !final.Segments[0].Final {
		t.Fatal("agent caption changed across caller input", resumed, final)
	}
	select {
	case p := <-packets:
		t.Fatal("unexpected caption cut or repeated final", p)
	default:
	}
}

func TestCaptionForwarderOptionsMatchDefault(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
		next := &recordingTranscriptionTextOutput{}
		packets := make(chan *lk.Transcription, 8)
		f := NewCaptionForwarder(session, CaptionTransport{AgentIdentity: "agent", AgentTrack: "out", CallerIdentity: "caller", CallerTrack: "in", DisableTranscriptionOutput: disabled, TranscriptionNextOutput: next, PublishPacket: func(p *lk.Transcription) error { packets <- p; return nil }})
		session.EmitUserInputTranscribed(agent.UserInputTranscribedEvent{Transcript: "caller", IsFinal: true})
		session.EmitAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{Transcript: "ans", IsFinal: false})
		session.EmitAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{Transcript: "answer", IsFinal: true})
		if !disabled {
			for i := 0; i < 3; i++ {
				select {
				case <-packets:
				case <-time.After(time.Second):
					t.Fatal("missing captions")
				}
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if e := f.Close(ctx); e != nil {
			t.Fatal(e)
		}
		cancel()
		next.mu.Lock()
		calls := append([]string(nil), next.calls...)
		next.mu.Unlock()
		if disabled {
			if len(packets) != 0 || len(calls) != 0 {
				t.Fatal("disabled output forwarded", calls)
			}
		} else {
			if len(calls) != 3 || calls[0] != "capture:ans" || calls[1] != "capture:answer" || calls[2] != "flush" {
				t.Fatal("next output", calls)
			}
		}
	}
}
