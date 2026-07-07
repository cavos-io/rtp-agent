package ultravox

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"time"

	audiomodel "github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestUltravoxRealtimeConstructorMatchesReference(t *testing.T) {
	t.Run("defaults", TestUltravoxRealtimeDefaultsMatchReference)
	t.Run("env_key", TestNewUltravoxRealtimeModelUsesEnvironmentAPIKey)
	t.Run("missing_key", TestNewUltravoxRealtimeModelRequiresAPIKey)
	t.Run("options", TestUltravoxRealtimeOptionsMatchReference)
}

func TestUltravoxRealtimeDefaultsMatchReference(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	if got := model.Model(); got != "fixie-ai/ultravox" {
		t.Fatalf("model = %q, want reference default", got)
	}
	if got := model.Provider(); got != "Ultravox" {
		t.Fatalf("provider = %q, want Ultravox", got)
	}
	if got := model.Label(); got != "ultravox-fixie-ai/ultravox" {
		t.Fatalf("label = %q, want reference label", got)
	}
	if got := model.Voice(); got != "Mark" {
		t.Fatalf("voice = %q, want reference default voice", got)
	}
	if got := model.BaseURL(); got != "https://api.ultravox.ai/api" {
		t.Fatalf("base URL = %q, want reference API URL", got)
	}
	if got := model.SystemPrompt(); got != "You are a helpful assistant." {
		t.Fatalf("system prompt = %q, want reference default prompt", got)
	}
	if got := model.InputSampleRate(); got != 16000 {
		t.Fatalf("input sample rate = %d, want reference 16000", got)
	}
	if got := model.OutputSampleRate(); got != 24000 {
		t.Fatalf("output sample rate = %d, want reference 24000", got)
	}
	if got := model.OutputMedium(); got != "voice" {
		t.Fatalf("output medium = %q, want reference voice", got)
	}
	if got, ok := model.FirstSpeaker(); !ok || got != "FIRST_SPEAKER_USER" {
		t.Fatalf("first speaker = %q/%v, want reference FIRST_SPEAKER_USER/true", got, ok)
	}

	caps := model.Capabilities()
	if !caps.MessageTruncation || !caps.TurnDetection || !caps.UserTranscription || !caps.AutoToolReplyGeneration || !caps.AudioOutput {
		t.Fatalf("capabilities = %+v, want reference realtime voice capabilities", caps)
	}
	if caps.ManualFunctionCalls || caps.PerResponseToolChoice {
		t.Fatalf("capabilities = %+v, want no manual function calls or per-response tool choice", caps)
	}
	var _ llm.RealtimeModel = model
}

func TestNewUltravoxRealtimeModelUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("ULTRAVOX_API_KEY", "env-key")

	model, err := NewRealtimeModel("")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v, want env fallback", err)
	}
	if got := model.APIKey(); got != "env-key" {
		t.Fatalf("api key = %q, want env key", got)
	}
}

func TestNewUltravoxRealtimeModelRequiresAPIKey(t *testing.T) {
	t.Setenv("ULTRAVOX_API_KEY", "")

	_, err := NewRealtimeModel("")
	if err == nil || !strings.Contains(err.Error(), "ULTRAVOX_API_KEY") {
		t.Fatalf("NewRealtimeModel error = %v, want missing key guidance", err)
	}
}

func TestUltravoxRealtimeOptionsMatchReference(t *testing.T) {
	model, err := NewRealtimeModel("test-key",
		WithRealtimeModel("fixie-ai/ultravox-llama3.3-70b"),
		WithRealtimeVoice("Jessica"),
		WithRealtimeBaseURL("https://ultravox.example/api/"),
		WithRealtimeSystemPrompt("stay concise"),
		WithRealtimeOutputMedium("text"),
		WithRealtimeInputSampleRate(8000),
		WithRealtimeOutputSampleRate(48000),
		WithRealtimeTemperature(0.2),
		WithRealtimeLanguageHint("es"),
		WithRealtimeMaxDuration("30m"),
		WithRealtimeTimeExceededMessage("done"),
		WithRealtimeEnableGreetingPrompt(false),
		WithRealtimeFirstSpeaker("FIRST_SPEAKER_AGENT"),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	if got := model.Model(); got != "fixie-ai/ultravox-llama3.3-70b" {
		t.Fatalf("model = %q, want configured model", got)
	}
	if got := model.Voice(); got != "Jessica" {
		t.Fatalf("voice = %q, want configured voice", got)
	}
	if got := model.BaseURL(); got != "https://ultravox.example/api" {
		t.Fatalf("base URL = %q, want trimmed configured URL", got)
	}
	if got := model.OutputMedium(); got != "text" {
		t.Fatalf("output medium = %q, want text", got)
	}
	if model.Capabilities().AudioOutput {
		t.Fatal("audio output = true, want false for text output medium")
	}
	if got, ok := model.Temperature(); !ok || got != 0.2 {
		t.Fatalf("temperature = %v/%v, want 0.2/true", got, ok)
	}
	if got, ok := model.LanguageHint(); !ok || got != "es" {
		t.Fatalf("language hint = %q/%v, want es/true", got, ok)
	}
	if got, ok := model.MaxDuration(); !ok || got != "30m" {
		t.Fatalf("max duration = %q/%v, want 30m/true", got, ok)
	}
	if got, ok := model.TimeExceededMessage(); !ok || got != "done" {
		t.Fatalf("time exceeded message = %q/%v, want done/true", got, ok)
	}
	if got, ok := model.EnableGreetingPrompt(); !ok || got {
		t.Fatalf("enable greeting prompt = %v/%v, want false/true", got, ok)
	}
	if got, ok := model.FirstSpeaker(); !ok || got != "FIRST_SPEAKER_AGENT" {
		t.Fatalf("first speaker = %q/%v, want FIRST_SPEAKER_AGENT/true", got, ok)
	}
}

func TestUltravoxRealtimeUpdateOptionsMatchReference(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	model.UpdateOptions(WithRealtimeUpdateOutputMedium("text"))
	if got := model.OutputMedium(); got != "text" {
		t.Fatalf("output medium = %q, want text after reference update_options", got)
	}
	if model.Capabilities().AudioOutput {
		t.Fatal("audio output = true, want false after output_medium=text")
	}

	model.UpdateOptions(WithRealtimeUpdateOutputMedium("voice"))
	if got := model.OutputMedium(); got != "voice" {
		t.Fatalf("output medium = %q, want voice after reference update_options", got)
	}
	if !model.Capabilities().AudioOutput {
		t.Fatal("audio output = false, want true after output_medium=voice")
	}
}

func TestUltravoxRealtimeSessionLifecycleMatchesReference(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v, want reference session lifecycle", err)
	}
	if session == nil {
		t.Fatal("Session = nil, want reference realtime session")
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("second Close error = %v", err)
	}
	if _, ok := <-session.EventCh(); ok {
		t.Fatal("EventCh still open after Close")
	}
}

func TestUltravoxRealtimeSessionPushAudioQueuesReferenceInputChunk(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	pcm := make([]byte, 3200)
	for i := range pcm {
		pcm[i] = byte(i % 251)
	}
	frame := &audiomodel.AudioFrame{
		Data:              pcm,
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1600,
	}

	if err := session.PushAudio(frame); err != nil {
		t.Fatalf("PushAudio error = %v, want reference audio input accepted", err)
	}

	select {
	case got := <-session.audioCh:
		if !bytes.Equal(got, pcm) {
			t.Fatalf("queued audio bytes = %v, want original 100ms PCM chunk", got[:min(len(got), 8)])
		}
	case <-time.After(time.Second):
		t.Fatal("PushAudio did not queue reference 100ms PCM chunk")
	}
	if err := session.CommitAudio(); err != nil {
		t.Fatalf("CommitAudio error = %v, want reference no-op", err)
	}
	if err := session.ClearAudio(); err != nil {
		t.Fatalf("ClearAudio error = %v, want reference no-op", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if err := session.PushAudio(frame); err != nil {
		t.Fatalf("PushAudio after Close error = %v, want reference no-op", err)
	}

	resamplingModel, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	resamplingSessionInterface, err := resamplingModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	resamplingSession := resamplingSessionInterface.(*realtimeSession)
	defer resamplingSession.Close()

	stereo8K := make([]byte, 800*2*2)
	left, right := int16(1000), int16(-1000)
	for sample := 0; sample < 800; sample++ {
		offset := sample * 4
		binary.LittleEndian.PutUint16(stereo8K[offset:], uint16(left))
		binary.LittleEndian.PutUint16(stereo8K[offset+2:], uint16(right))
	}
	if err := resamplingSession.PushAudio(&audiomodel.AudioFrame{
		Data:              stereo8K,
		SampleRate:        8000,
		NumChannels:       2,
		SamplesPerChannel: 800,
	}); err != nil {
		t.Fatalf("PushAudio stereo 8k error = %v, want reference resample/downmix", err)
	}
	select {
	case got := <-resamplingSession.audioCh:
		want := make([]byte, 3200)
		if !bytes.Equal(got, want) {
			t.Fatalf("resampled/downmixed audio bytes = %v, want 16k mono mixed silence", got[:min(len(got), 8)])
		}
	case <-time.After(time.Second):
		t.Fatal("PushAudio did not queue resampled/downmixed chunk")
	}
}

func TestUltravoxRealtimeSessionGenerateReplyQueuesReferenceUserTextMessage(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{}); err != nil {
		t.Fatalf("GenerateReply error = %v, want reference user text event", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "user_text_message",
		"text":          "",
		"deferResponse": false,
	})

	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{
		Instructions:    "answer briefly",
		InstructionsSet: true,
	}); err != nil {
		t.Fatalf("GenerateReply with instructions error = %v, want reference instruction event", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "user_text_message",
		"text":          "<instruction>answer briefly</instruction>",
		"deferResponse": false,
	})

	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{}); err != nil {
		t.Fatalf("GenerateReply after Close error = %v, want reference no-op", err)
	}
}

func TestUltravoxRealtimeSessionTruncateIsReferenceNoop(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if err := session.Truncate(llm.RealtimeTruncateOptions{
		MessageID:      "msg-1",
		Modalities:     []string{"audio"},
		AudioEndMillis: 120,
	}); err != nil {
		t.Fatalf("Truncate error = %v, want reference no-op", err)
	}
}

func requireUltravoxRealtimeClientEvent(t *testing.T, session *realtimeSession, want map[string]any) {
	t.Helper()
	select {
	case got := <-session.clientEventCh:
		for key, wantValue := range want {
			if gotValue := got[key]; gotValue != wantValue {
				t.Fatalf("client event %s = %#v, want %#v in %#v", key, gotValue, wantValue, got)
			}
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for client event %#v", want)
	}
}
