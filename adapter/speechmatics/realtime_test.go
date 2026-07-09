package speechmatics

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/utils/images"
)

func TestSpeechmaticsRealtimeModelRequiresAPIKey(t *testing.T) {
	t.Setenv(speechmaticsAPIKeyEnv, "")

	_, err := NewRealtimeModel("")

	if err == nil || !strings.Contains(err.Error(), speechmaticsAPIKeyEnv) {
		t.Fatalf("NewRealtimeModel error = %v, want missing API key error", err)
	}
}

func TestSpeechmaticsRealtimeModelMetadataAndCapabilities(t *testing.T) {
	t.Setenv(speechmaticsAPIKeyEnv, "env-key")

	rtModel, err := NewRealtimeModel("")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	if rtModel.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", rtModel.apiKey)
	}
	if got := rtModel.Label(); got != "speechmatics.RealtimeModel" {
		t.Fatalf("Label() = %q, want speechmatics.RealtimeModel", got)
	}
	if got := rtModel.Model(); got != "flow" {
		t.Fatalf("Model() = %q, want flow", got)
	}
	if got := rtModel.Provider(); got != "Speechmatics" {
		t.Fatalf("Provider() = %q, want Speechmatics", got)
	}

	caps := rtModel.Capabilities()
	if !caps.TurnDetection || !caps.UserTranscription || !caps.AudioOutput || !caps.AutoToolReplyGeneration {
		t.Fatalf("capabilities = %#v, want full duplex voice model defaults", caps)
	}
	if caps.MessageTruncation || caps.ManualFunctionCalls || caps.PerResponseToolChoice {
		t.Fatalf("capabilities = %#v, want unsupported optional controls disabled", caps)
	}
	if !caps.MutableInstructions || !caps.MutableChatContext || !caps.MutableTools || !caps.SupportsSay {
		t.Fatalf("capabilities = %#v, want mutable instructions/context/tools and say support", caps)
	}
}

func TestSpeechmaticsRealtimeModelOptionsAndSessionSnapshot(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key",
		WithRealtimeBaseURL("wss://flow.example/v1"),
		WithRealtimeModel("flow-pro"),
		WithRealtimeVoice("theo"),
		WithRealtimeSystemPrompt("base"),
		WithRealtimeInputSampleRate(24000),
		WithRealtimeOutputSampleRate(48000),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	session, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	rtSession := session.(*speechmaticsRealtimeSession)

	if rtSession.baseURL != "wss://flow.example/v1" {
		t.Fatalf("session baseURL = %q, want snapshot", rtSession.baseURL)
	}
	if rtSession.model != "flow-pro" || rtSession.voice != "theo" || rtSession.instructions != "base" {
		t.Fatalf("session options = %q/%q/%q, want snapshot", rtSession.model, rtSession.voice, rtSession.instructions)
	}
	if rtSession.inputSampleRate != 24000 || rtSession.outputSampleRate != 48000 {
		t.Fatalf("session rates = %d/%d, want 24000/48000", rtSession.inputSampleRate, rtSession.outputSampleRate)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow-pro")
}

func TestSpeechmaticsRealtimeSessionControlMethods(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	if err := session.UpdateInstructions("new instructions"); err != nil {
		t.Fatalf("UpdateInstructions error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "session.update", "instructions", "new instructions")
	if err := session.UpdateOptions(llm.RealtimeSessionOptions{Voice: "megan", VoiceSet: true}); err != nil {
		t.Fatalf("UpdateOptions voice error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "session.update", "voice", "megan")
	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{Instructions: "answer now", InstructionsSet: true}); err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "response.create", "instructions", "answer now")
	if err := session.Say("hello"); err != nil {
		t.Fatalf("Say error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "response.create", "text", "hello")
	if err := session.PushAudio(&model.AudioFrame{Data: []byte{0x01, 0x02}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err != nil {
		t.Fatalf("PushAudio error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "input_audio_buffer.append", "audio", []byte{0x01, 0x02})
	if err := session.CommitAudio(); err != nil {
		t.Fatalf("CommitAudio error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "input_audio_buffer.commit", "", nil)
	if err := session.ClearAudio(); err != nil {
		t.Fatalf("ClearAudio error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "input_audio_buffer.clear", "", nil)
	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "response.cancel", "", nil)
	if err := session.Truncate(llm.RealtimeTruncateOptions{}); err != nil {
		t.Fatalf("Truncate error = %v", err)
	}
	if err := session.PushVideo(&images.VideoFrame{}); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("PushVideo error = %v, want unsupported", err)
	}
}

func TestSpeechmaticsRealtimeSessionCloseIsIdempotent(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("first Close error = %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("second Close error = %v", err)
	}
	if _, ok := <-session.EventCh(); ok {
		t.Fatal("EventCh still open after Close")
	}
	if err := session.PushAudio(&model.AudioFrame{Data: []byte{0x01, 0x02}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushAudio after Close error = %v, want io.ErrClosedPipe", err)
	}
}

func assertSpeechmaticsRealtimeCommand(t *testing.T, session llm.RealtimeSession, wantType, key string, want any) {
	t.Helper()
	rtSession := session.(*speechmaticsRealtimeSession)
	select {
	case command := <-rtSession.commandCh:
		if command["type"] != wantType {
			t.Fatalf("command type = %#v, want %q in %#v", command["type"], wantType, command)
		}
		if key == "" {
			return
		}
		got := command[key]
		if key == "audio" {
			gotBytes, _ := got.([]byte)
			wantBytes, _ := want.([]byte)
			if string(gotBytes) != string(wantBytes) {
				t.Fatalf("command[%q] = %v, want %v", key, gotBytes, wantBytes)
			}
			return
		}
		if got != want {
			t.Fatalf("command[%q] = %#v, want %#v", key, got, want)
		}
	default:
		t.Fatalf("missing realtime command %q", wantType)
	}
}
