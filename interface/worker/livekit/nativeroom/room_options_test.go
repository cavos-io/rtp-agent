//go:build nativeinprocess

package nativeroom

import (
	"context"
	"github.com/cavos-io/rtp-agent/core/agent"
	audio "github.com/cavos-io/rtp-agent/core/audio/model"
	wl "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNativeRoomOptionsValidationAndSelection(t *testing.T) {
	r := NewInProcess(nil, nil, Config{Participant: "caller"}, "", "")
	if e := r.ConfigureRoomOptions(wl.RoomOptions{}); e != nil {
		t.Fatal("enabled pre-connect audio rejected", e)
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

func TestNativeRejectsUnsupportedRoomOptionsBeforeMutation(t *testing.T) {
	cases := map[string]wl.RoomOptions{
		"AudioInputProcessor":      {AudioInputProcessor: &unsupportedTestProcessor{}},
		"ParticipantKinds":         {ParticipantKinds: []lksdk.ParticipantKind{0}},
		"AudioTrackName":           {AudioTrackName: "custom"},
		"AudioInputSampleRate":     {AudioInputSampleRate: 48000},
		"AudioInputFrameSizeMS":    {AudioInputFrameSizeMS: 50},
		"AudioOutputSampleRate":    {AudioOutputSampleRate: 48000},
		"AudioSubscriptionTimeout": {AudioSubscriptionTimeout: time.Second},
		"PreConnectAudioTimeout":   {PreConnectAudioTimeout: -time.Second},
		"DisableAudioInput":        {DisableAudioInput: true},
		"DisableAudioOutput":       {DisableAudioOutput: true},
		"TranscriptionJSONFormat":  {TranscriptionJSONFormat: true},
		"DisableCloseOnDisconnect": {DisableCloseOnDisconnect: true},
		"DeleteRoomOnClose":        {DeleteRoomOnClose: true},
		"DeleteRoom":               {DeleteRoom: func(context.Context, string) error { return nil }},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			r := NewInProcess(nil, nil, Config{}, "", "")
			opts.DisablePreConnectAudio = true
			opts.ParticipantIdentity = "must-not-apply"
			err := r.ConfigureRoomOptions(opts)
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("expected %s rejection, got %v", name, err)
			}
			if r.Config.Participant != "" {
				t.Fatal("rejected options mutated room")
			}
		})
	}
}

// Make additions to RoomOptions an explicit native compatibility decision.
func TestNativeRoomOptionsContractCoversEveryField(t *testing.T) {
	fields := strings.Fields("AudioTrackName AudioInputSampleRate AudioInputFrameSizeMS AudioInputProcessor AudioOutputSampleRate AudioSubscriptionTimeout PreConnectAudioTimeout DisablePreConnectAudio DisableAudioInput DisableTextInput DisableAudioOutput DisableTranscriptionOutput TranscriptionJSONFormat TranscriptionNextOutput DisableCloseOnDisconnect DeleteRoomOnClose DeleteRoom TextInputCallback ParticipantIdentity ParticipantKinds RecordRemoteParticipants")
	typ := reflect.TypeOf(wl.RoomOptions{})
	if typ.NumField() != len(fields) {
		t.Fatal("update native option contract for changed RoomOptions")
	}
	for _, name := range fields {
		if _, ok := typ.FieldByName(name); !ok {
			t.Fatalf("missing field %s", name)
		}
	}
}

type unsupportedTestProcessor struct{}

func (*unsupportedTestProcessor) Process(context.Context, *audio.AudioFrame) ([]*audio.AudioFrame, error) {
	panic("must reject before processing")
}
func (*unsupportedTestProcessor) Reset()       {}
func (*unsupportedTestProcessor) Close() error { return nil }

func TestNativePreConnectOptionsPreserved(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		r := NewInProcess(nil, nil, Config{}, "", "")
		if err := r.ConfigureRoomOptions(wl.RoomOptions{DisablePreConnectAudio: disabled, PreConnectAudioTimeout: time.Second}); err != nil {
			t.Fatal(err)
		}
		if r.Config.captionOptions.DisablePreConnectAudio != disabled || r.Config.captionOptions.PreConnectAudioTimeout != time.Second {
			t.Fatal("pre-connect options lost")
		}
	}
}
