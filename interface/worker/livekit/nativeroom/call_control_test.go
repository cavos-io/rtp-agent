//go:build nativeinprocess

package nativeroom

import (
	"context"
	"errors"
	"github.com/cavos-io/rtp-agent/core/agent"
	"testing"
)

type callControlFixture struct {
	err     error
	topic   string
	payload []byte
}

func (f *callControlFixture) PublishData(_ context.Context, topic string, payload []byte) error {
	f.topic = topic
	f.payload = payload
	return f.err
}
func (f *callControlFixture) ParticipantIdentities() []string              { return []string{"caller"} }
func (f *callControlFixture) BeginHold(context.Context) error              { return f.err }
func (f *callControlFixture) EndHold(context.Context) error                { return f.err }
func (f *callControlFixture) CaptureHold(context.Context, []int16) error   { return f.err }
func (f *callControlFixture) SetOutputEnabled(context.Context, bool) error { return f.err }
func TestCallControlRequiresNativeConnectionAndPreservesFailures(t *testing.T) {
	r := NewInProcess(nil, agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{}), Config{}, "", "")
	if err := r.BeginHold(t.Context()); err == nil {
		t.Fatal("unconnected hold accepted")
	}
	f := &callControlFixture{err: errors.New("denied")}
	r.child = &client{callControl: f}
	if err := r.PublishData(t.Context(), "agent.events", []byte("data")); !errors.Is(err, f.err) {
		t.Fatal(err)
	}
	if f.topic != "agent.events" || string(f.payload) != "data" {
		t.Fatal(f)
	}
	if got := r.RemoteParticipantIdentities(); len(got) != 1 || got[0] != "caller" {
		t.Fatal(got)
	}
	if err := r.SetInputAudioEnabled(t.Context(), false); err != nil || !r.Session.InputAudioMuted() {
		t.Fatal(err)
	}
	if err := r.SetInputAudioEnabled(t.Context(), true); err != nil || r.Session.InputAudioMuted() {
		t.Fatal(err)
	}
	r.BeginShutdown()
	if err := r.PublishData(t.Context(), "agent.events", []byte("data")); err == nil {
		t.Fatal("closed publication accepted")
	}
}

func TestHoldDeliveryCountsOnlyAcknowledgedPCM(t *testing.T) {
	f := &callControlFixture{}
	r := &Room{child: &client{callControl: f}}
	if err := r.BeginHold(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := r.CaptureHold(t.Context(), make([]int16, 480)); err != nil {
		t.Fatal(err)
	}
	f.err = errors.New("capture rejected")
	if err := r.CaptureHold(t.Context(), make([]int16, 480)); !errors.Is(err, f.err) {
		t.Fatal(err)
	}
	if r.holdFrames.Load() != 1 || r.holdSamples.Load() != 480 {
		t.Fatal("failed capture counted as delivered")
	}
	f.err = nil
	if err := r.EndHold(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := r.BeginHold(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r.holdFrames.Load() != 0 || r.holdSamples.Load() != 0 {
		t.Fatal("new hold retained old counters")
	}
}
