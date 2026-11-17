//go:build nativeinprocess

package nativeroom

import (
	"context"
	"github.com/cavos-io/rtp-agent/core/agent"
	wl "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	"testing"
)

func TestNativeRoomOptionsValidationAndSelection(t *testing.T) {
	r := NewInProcess(nil, nil, Config{Participant: "caller"}, "", "")
	if e := r.ConfigureRoomOptions(wl.RoomOptions{}); e == nil {
		t.Fatal("enabled pre-connect audio accepted")
	}
	if e := r.ConfigureRoomOptions(wl.RoomOptions{DisablePreConnectAudio: true, ParticipantIdentity: "other"}); e == nil {
		t.Fatal("conflicting selector accepted")
	}
	if r.Config.Participant != "caller" {
		t.Fatal("invalid options changed selection")
	}
	next := &optionsTextOutput{}
	if e := r.ConfigureRoomOptions(wl.RoomOptions{DisablePreConnectAudio: true, ParticipantIdentity: "caller", DisableTranscriptionOutput: true, TranscriptionNextOutput: next}); e != nil {
		t.Fatal(e)
	}
	if !r.Config.captionOptions.DisableTranscriptionOutput || r.Config.captionOptions.TranscriptionNextOutput != next {
		t.Fatal("caption option lost")
	}
	r2 := NewInProcess(nil, nil, Config{}, "", "")
	if e := r2.ConfigureRoomOptions(wl.RoomOptions{DisablePreConnectAudio: true, ParticipantIdentity: "selected"}); e != nil {
		t.Fatal(e)
	}
	if r2.Config.Participant != "selected" {
		t.Fatal("identity not applied")
	}
	r2.closed = true
	if e := r2.ConfigureRoomOptions(wl.RoomOptions{DisablePreConnectAudio: true}); e == nil {
		t.Fatal("configured closed room")
	}
	helper := New(nil, nil, Config{Participant: "helper-caller"}, "", "")
	if e := helper.ConfigureRoomOptions(wl.RoomOptions{ParticipantIdentity: "ignored"}); e != nil || helper.Config.Participant != "helper-caller" {
		t.Fatal("helper behavior changed", e)
	}
}

type optionsTextOutput struct{}

func (*optionsTextOutput) CaptureText(context.Context, string) error { return nil }
func (*optionsTextOutput) Flush()                                    {}

func TestNativeChatRoomOptions(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		r := NewInProcess(nil, nil, Config{}, "", "")
		cb := func(context.Context, *agent.AgentSession, wl.TextInputEvent) error { return nil }
		if err := r.ConfigureRoomOptions(wl.RoomOptions{DisablePreConnectAudio: true, DisableTextInput: disabled, TextInputCallback: cb}); err != nil {
			t.Fatal(err)
		}
		if r.Config.captionOptions.DisableTextInput != disabled || r.Config.captionOptions.TextInputCallback == nil {
			t.Fatal("chat options lost")
		}
	}
}
