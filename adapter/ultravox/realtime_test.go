package ultravox

import (
	"bytes"
	"context"
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

func TestUltravoxRealtimeModelUpdateOptionsPropagatesReferenceSessions(t *testing.T) {
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

	model.UpdateOptions(WithRealtimeUpdateOutputMedium("text"))
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":   "set_output_medium",
		"medium": "text",
	})

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "thinking"})
	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)
	requireUltravoxRealtimeModalities(t, message.ModalitiesCh, []string{"text"})
}

func TestUltravoxRealtimeSessionUpdateOptionsQueuesReferenceOutputMedium(t *testing.T) {
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

	if err := session.UpdateOptions(llm.RealtimeSessionOptions{
		OutputMedium:    "text",
		OutputMediumSet: true,
	}); err != nil {
		t.Fatalf("UpdateOptions output medium error = %v, want reference set_output_medium event", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":   "set_output_medium",
		"medium": "text",
	})

	if err := session.UpdateOptions(llm.RealtimeSessionOptions{}); err != nil {
		t.Fatalf("UpdateOptions empty error = %v, want reference no-op for unset output medium", err)
	}
	select {
	case got := <-session.clientEventCh:
		t.Fatalf("unexpected client event for empty UpdateOptions = %#v", got)
	default:
	}
}

func TestUltravoxRealtimeSessionQueuesReferenceInitialTextOutputMedium(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithRealtimeOutputMedium("text"))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":   "set_output_medium",
		"medium": "text",
	})
}

func TestUltravoxRealtimeSessionUpdateInstructionsMarksReferenceRestart(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithRealtimeSystemPrompt("stay concise"))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	if err := session.UpdateInstructions("stay concise"); err != nil {
		t.Fatalf("UpdateInstructions same prompt error = %v, want reference no-op", err)
	}
	if got := session.restartCount; got != 0 {
		t.Fatalf("restart count after unchanged instructions = %d, want 0", got)
	}

	if err := session.UpdateInstructions("answer briefly"); err != nil {
		t.Fatalf("UpdateInstructions changed prompt error = %v, want reference restart", err)
	}
	if got := session.restartCount; got != 1 {
		t.Fatalf("restart count after changed instructions = %d, want 1", got)
	}
	if err := session.UpdateInstructions("answer briefly"); err != nil {
		t.Fatalf("UpdateInstructions repeated prompt error = %v, want reference no-op", err)
	}
	if got := session.restartCount; got != 1 {
		t.Fatalf("restart count after repeated instructions = %d, want 1", got)
	}
}

func TestUltravoxRealtimeSessionUpdateInstructionsClosesActiveGenerationForReferenceRestart(t *testing.T) {
	model, err := NewRealtimeModel("test-key", WithRealtimeSystemPrompt("stay concise"))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)
	defer session.Close()

	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
		Role:    "agent",
		Delta:   "hello",
		Final:   false,
		Ordinal: 1,
	})
	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)
	requireUltravoxRealtimeText(t, message.TextCh, "hello")

	if err := session.UpdateInstructions("answer briefly"); err != nil {
		t.Fatalf("UpdateInstructions changed prompt error = %v, want reference restart cleanup", err)
	}
	requireUltravoxRealtimeClosedText(t, message.TextCh)
	requireUltravoxRealtimeClosedAudio(t, message.AudioCh)
}

func TestUltravoxRealtimeSessionUpdateToolsMarksReferenceRestartOnNameSetChange(t *testing.T) {
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

	lookup := ultravoxRealtimeTestTool{name: "lookup"}
	if err := session.UpdateTools([]llm.Tool{lookup}); err != nil {
		t.Fatalf("UpdateTools lookup error = %v, want reference restart", err)
	}
	if got := session.restartCount; got != 1 {
		t.Fatalf("restart count after adding lookup = %d, want 1", got)
	}
	if err := session.UpdateTools([]llm.Tool{ultravoxRealtimeTestTool{name: "lookup"}}); err != nil {
		t.Fatalf("UpdateTools same name error = %v, want reference no-op", err)
	}
	if got := session.restartCount; got != 1 {
		t.Fatalf("restart count after same tool-name set = %d, want 1", got)
	}
	if err := session.UpdateTools([]llm.Tool{lookup, ultravoxRealtimeTestTool{name: "calendar"}}); err != nil {
		t.Fatalf("UpdateTools changed name set error = %v, want reference restart", err)
	}
	if got := session.restartCount; got != 2 {
		t.Fatalf("restart count after changed tool-name set = %d, want 2", got)
	}
}

func TestUltravoxRealtimeSessionUpdateToolsClosesActiveGenerationForReferenceRestart(t *testing.T) {
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

	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
		Role:    "agent",
		Delta:   "hello",
		Final:   false,
		Ordinal: 1,
	})
	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)
	requireUltravoxRealtimeText(t, message.TextCh, "hello")

	if err := session.UpdateTools([]llm.Tool{ultravoxRealtimeTestTool{name: "lookup"}}); err != nil {
		t.Fatalf("UpdateTools changed tool set error = %v, want reference restart cleanup", err)
	}
	requireUltravoxRealtimeClosedText(t, message.TextCh)
	requireUltravoxRealtimeClosedAudio(t, message.AudioCh)
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

func TestUltravoxRealtimeSessionCloseFinishesReferenceActiveGeneration(t *testing.T) {
	model, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := model.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*realtimeSession)

	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
		Role:    "agent",
		Delta:   "hello",
		Final:   false,
		Ordinal: 1,
	})
	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)
	requireUltravoxRealtimeText(t, message.TextCh, "hello")

	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v, want reference active generation cleanup", err)
	}
	requireUltravoxRealtimeClosedText(t, message.TextCh)
	requireUltravoxRealtimeClosedAudio(t, message.AudioCh)
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

func TestUltravoxRealtimeSessionInterruptSendsReferenceBargeIn(t *testing.T) {
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

	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt without active generation error = %v, want reference no-op", err)
	}
	select {
	case event := <-session.clientEventCh:
		t.Fatalf("barge-in event without active generation = %#v", event)
	default:
	}

	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
		Role:    "agent",
		Delta:   "hello",
		Final:   false,
		Ordinal: 1,
	})
	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)
	requireUltravoxRealtimeText(t, message.TextCh, "hello")

	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt active generation error = %v, want reference barge-in", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "user_text_message",
		"text":          "",
		"urgency":       "immediate",
		"deferResponse": true,
	})
	requireUltravoxRealtimeClosedText(t, message.TextCh)
	requireUltravoxRealtimeClosedAudio(t, message.AudioCh)
}

func TestUltravoxRealtimeSessionOutputAudioStartsReferenceGeneration(t *testing.T) {
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

	audio := make([]byte, 960)
	for i := range audio {
		audio[i] = byte(i % 251)
	}
	session.handleOutputAudio(audio)

	var generation *llm.GenerationCreatedEvent
	select {
	case event := <-session.EventCh():
		if event.Type != llm.RealtimeEventTypeGenerationCreated {
			t.Fatalf("event type = %s, want generation_created", event.Type)
		}
		generation = event.Generation
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for generation_created")
	}
	if generation == nil {
		t.Fatal("generation = nil")
	}

	var message llm.MessageGeneration
	select {
	case message = <-generation.MessageCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message generation")
	}

	select {
	case got := <-message.AudioCh:
		if got.SampleRate != 24000 || got.NumChannels != 1 || got.SamplesPerChannel != 480 {
			t.Fatalf("audio frame shape = rate %d channels %d samples %d, want 24000/1/480", got.SampleRate, got.NumChannels, got.SamplesPerChannel)
		}
		if !bytes.Equal(got.Data, audio) {
			t.Fatalf("audio data = %v, want original output bytes", got.Data[:min(len(got.Data), 8)])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for output audio frame")
	}
}

func TestUltravoxRealtimeSessionGenerationMessageExposesReferenceModalities(t *testing.T) {
	for _, tc := range []struct {
		name         string
		outputMedium string
		want         []string
	}{
		{name: "voice", outputMedium: "voice", want: []string{"audio", "text"}},
		{name: "text", outputMedium: "text", want: []string{"text"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model, err := NewRealtimeModel("test-key", WithRealtimeOutputMedium(tc.outputMedium))
			if err != nil {
				t.Fatalf("NewRealtimeModel error = %v", err)
			}
			sessionInterface, err := model.Session()
			if err != nil {
				t.Fatalf("Session error = %v", err)
			}
			session := sessionInterface.(*realtimeSession)
			defer session.Close()
			if tc.outputMedium == "text" {
				requireUltravoxRealtimeClientEvent(t, session, map[string]any{
					"type":   "set_output_medium",
					"medium": "text",
				})
			}

			session.handleStateEvent(ultravoxRealtimeStateEvent{State: "thinking"})
			generation := requireUltravoxRealtimeGeneration(t, session)
			message := requireUltravoxRealtimeMessage(t, generation)
			requireUltravoxRealtimeModalities(t, message.ModalitiesCh, tc.want)
		})
	}
}

func TestUltravoxRealtimeSessionOutputMediumUpdateChangesReferenceModalities(t *testing.T) {
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

	if err := session.UpdateOptions(llm.RealtimeSessionOptions{
		OutputMedium:    "text",
		OutputMediumSet: true,
	}); err != nil {
		t.Fatalf("UpdateOptions text error = %v", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":   "set_output_medium",
		"medium": "text",
	})

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "thinking"})
	textGeneration := requireUltravoxRealtimeGeneration(t, session)
	textMessage := requireUltravoxRealtimeMessage(t, textGeneration)
	requireUltravoxRealtimeModalities(t, textMessage.ModalitiesCh, []string{"text"})
	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "listening"})
	requireUltravoxRealtimeClosedText(t, textMessage.TextCh)

	if err := session.UpdateOptions(llm.RealtimeSessionOptions{
		OutputMedium:    "voice",
		OutputMediumSet: true,
	}); err != nil {
		t.Fatalf("UpdateOptions voice error = %v", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":   "set_output_medium",
		"medium": "voice",
	})

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "thinking"})
	voiceGeneration := requireUltravoxRealtimeGeneration(t, session)
	voiceMessage := requireUltravoxRealtimeMessage(t, voiceGeneration)
	requireUltravoxRealtimeModalities(t, voiceMessage.ModalitiesCh, []string{"audio", "text"})
}

func TestUltravoxRealtimeSessionUserTranscriptEmitsReferenceFinality(t *testing.T) {
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

	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
		Role:    "user",
		Text:    "hello",
		Final:   false,
		Ordinal: 7,
	})
	requireUltravoxRealtimeTranscriptEvent(t, session, "msg_user_7", "hello", false)

	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
		Role:    "user",
		Text:    "hello world",
		Final:   true,
		Ordinal: 7,
	})
	requireUltravoxRealtimeTranscriptEvent(t, session, "msg_user_7", "hello world", true)
}

func TestUltravoxRealtimeSessionAgentTranscriptStreamsReferenceDeltas(t *testing.T) {
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

	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
		Role:    "agent",
		Delta:   "hel",
		Final:   false,
		Ordinal: 2,
	})

	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)
	requireUltravoxRealtimeText(t, message.TextCh, "hel")

	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
		Role:    "agent",
		Text:    "hello",
		Final:   true,
		Ordinal: 2,
	})
	requireUltravoxRealtimeClosedText(t, message.TextCh)
	requireUltravoxRealtimeClosedAudio(t, message.AudioCh)
}

func TestUltravoxRealtimeSessionGenerationCreatedDoesNotBlockProviderReceive(t *testing.T) {
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

	for i := 0; i < cap(session.eventCh); i++ {
		session.eventCh <- llm.RealtimeEvent{Type: llm.RealtimeEventTypeText}
	}

	done := make(chan struct{})
	go func() {
		session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{
			Role:    "agent",
			Delta:   "hello",
			Final:   false,
			Ordinal: 1,
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		<-session.eventCh
		<-done
		t.Fatal("agent transcript handler blocked on full generation_created event buffer")
	}
}

func TestUltravoxRealtimeSessionGenerationsUseReferenceUniqueMessageIDs(t *testing.T) {
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

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "thinking"})
	firstGeneration := requireUltravoxRealtimeGeneration(t, session)
	firstMessage := requireUltravoxRealtimeMessage(t, firstGeneration)
	session.handleTranscriptEvent(ultravoxRealtimeTranscriptEvent{Role: "agent", Text: "done", Final: true, Ordinal: 1})
	requireUltravoxRealtimeClosedText(t, firstMessage.TextCh)

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "thinking"})
	secondGeneration := requireUltravoxRealtimeGeneration(t, session)
	secondMessage := requireUltravoxRealtimeMessage(t, secondGeneration)

	if !strings.HasPrefix(firstMessage.MessageID, "ultravox-turn-") ||
		!strings.HasPrefix(secondMessage.MessageID, "ultravox-turn-") {
		t.Fatalf("message IDs = %q/%q, want reference ultravox-turn-* prefix", firstMessage.MessageID, secondMessage.MessageID)
	}
	if firstMessage.MessageID == secondMessage.MessageID {
		t.Fatalf("message IDs both %q, want unique reference turn IDs", firstMessage.MessageID)
	}
}

func TestUltravoxRealtimeSessionStateEventsMatchReferenceTurnLifecycle(t *testing.T) {
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

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "thinking"})
	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "speaking"})
	select {
	case event := <-session.EventCh():
		if event.Type != llm.RealtimeEventTypeSpeechStopped {
			t.Fatalf("event type = %s, want speech_stopped", event.Type)
		}
		if event.SpeechStopped == nil || event.SpeechStopped.UserTranscriptionEnabled {
			t.Fatalf("SpeechStopped = %+v, want user transcription disabled", event.SpeechStopped)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for speech_stopped")
	}

	session.handleStateEvent(ultravoxRealtimeStateEvent{State: "listening"})
	requireUltravoxRealtimeClosedText(t, message.TextCh)
	requireUltravoxRealtimeClosedAudio(t, message.AudioCh)
}

func TestUltravoxRealtimeSessionToolInvocationEmitsReferenceFunctionCall(t *testing.T) {
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

	session.handleToolInvocationEvent(ultravoxRealtimeToolInvocationEvent{
		ToolName:     "lookup",
		InvocationID: "call-7",
		Parameters:   map[string]any{"city": "Paris"},
	})

	generation := requireUltravoxRealtimeGeneration(t, session)
	message := requireUltravoxRealtimeMessage(t, generation)
	select {
	case call := <-generation.FunctionCh:
		if call == nil {
			t.Fatal("function call = nil")
		}
		if call.CallID != "call-7" || call.Name != "lookup" || call.Arguments != `{"city":"Paris"}` {
			t.Fatalf("function call = %+v, want call-7 lookup JSON args", call)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for function call")
	}
	requireUltravoxRealtimeClosedText(t, message.TextCh)
	requireUltravoxRealtimeClosedAudio(t, message.AudioCh)
}

func TestUltravoxRealtimeSessionToolResultQueuesReferenceClientEvent(t *testing.T) {
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

	ctx := llm.NewChatContext()
	ctx.Append(&llm.FunctionCallOutput{
		ID:     "result-1",
		CallID: "call-7",
		Name:   "lookup",
		Output: "Paris",
	})
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext error = %v, want reference tool result event", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "client_tool_result",
		"invocationId":  "call-7",
		"result":        "Paris",
		"agentReaction": "speaks",
		"responseType":  "tool-response",
	})

	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("second UpdateChatContext error = %v", err)
	}
	select {
	case got := <-session.clientEventCh:
		t.Fatalf("duplicate tool result event = %#v", got)
	default:
	}
}

func TestUltravoxRealtimeSessionToolErrorResultQueuesReferenceClientEvent(t *testing.T) {
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

	ctx := llm.NewChatContext()
	ctx.Append(&llm.FunctionCallOutput{
		ID:      "result-err",
		CallID:  "call-err",
		Name:    "lookup",
		Output:  "database unavailable",
		IsError: true,
	})
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext error = %v, want reference tool error result event", err)
	}
	select {
	case got := <-session.clientEventCh:
		if got["type"] != "client_tool_result" ||
			got["invocationId"] != "call-err" ||
			got["agentReaction"] != "speaks" ||
			got["responseType"] != "tool-response" ||
			got["errorType"] != "implementation-error" ||
			got["errorMessage"] != "database unavailable" {
			t.Fatalf("tool error event = %#v, want reference error fields", got)
		}
		if _, ok := got["result"]; ok {
			t.Fatalf("tool error event result = %#v, want no result field", got["result"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tool error event")
	}
}

func TestUltravoxRealtimeSessionUpdateChatContextQueuesReferenceDeferredMessages(t *testing.T) {
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

	ctx := llm.NewChatContext()
	ctx.AddMessage(llm.ChatMessageArgs{ID: "sys", Role: llm.ChatRoleSystem, Text: "be concise"})
	ctx.AddMessage(llm.ChatMessageArgs{ID: "user", Role: llm.ChatRoleUser, Text: "remember Paris"})
	ctx.AddMessage(llm.ChatMessageArgs{ID: "assistant", Role: llm.ChatRoleAssistant, Text: "managed by provider"})

	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext error = %v, want reference deferred messages", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "user_text_message",
		"text":          "<instruction>be concise</instruction>",
		"deferResponse": true,
	})
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "user_text_message",
		"text":          "remember Paris",
		"deferResponse": true,
	})
	select {
	case got := <-session.clientEventCh:
		t.Fatalf("unexpected assistant/duplicate context event = %#v", got)
	default:
	}

	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("second UpdateChatContext error = %v", err)
	}
	select {
	case got := <-session.clientEventCh:
		t.Fatalf("duplicate context event = %#v", got)
	default:
	}
}

func TestUltravoxRealtimeSessionUpdateChatContextResendsReferenceReaddedItems(t *testing.T) {
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

	ctx := llm.NewChatContext()
	ctx.AddMessage(llm.ChatMessageArgs{ID: "memo", Role: llm.ChatRoleUser, Text: "remember Paris"})
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext initial error = %v", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "user_text_message",
		"text":          "remember Paris",
		"deferResponse": true,
	})

	if err := session.UpdateChatContext(llm.NewChatContext()); err != nil {
		t.Fatalf("UpdateChatContext empty error = %v", err)
	}
	select {
	case got := <-session.clientEventCh:
		t.Fatalf("unexpected event for deletion-only context update = %#v", got)
	default:
	}

	readded := llm.NewChatContext()
	readded.AddMessage(llm.ChatMessageArgs{ID: "memo", Role: llm.ChatRoleUser, Text: "remember Paris"})
	if err := session.UpdateChatContext(readded); err != nil {
		t.Fatalf("UpdateChatContext readd error = %v", err)
	}
	requireUltravoxRealtimeClientEvent(t, session, map[string]any{
		"type":          "user_text_message",
		"text":          "remember Paris",
		"deferResponse": true,
	})
}

func TestUltravoxRealtimeSessionPlaybackClearBufferEmitsReferenceSpeechStarted(t *testing.T) {
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

	session.handlePlaybackClearBufferEvent()

	select {
	case event := <-session.EventCh():
		if event.Type != llm.RealtimeEventTypeSpeechStarted {
			t.Fatalf("event type = %s, want speech_started", event.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for speech_started")
	}
}

func TestUltravoxRealtimeSessionServerJSONDispatchesReferenceEvents(t *testing.T) {
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

	if err := session.handleServerTextMessage([]byte(`{"type":"transcript","role":"user","medium":"voice","text":"hello","final":true,"ordinal":4}`)); err != nil {
		t.Fatalf("handle transcript JSON error = %v", err)
	}
	requireUltravoxRealtimeTranscriptEvent(t, session, "msg_user_4", "hello", true)

	if err := session.handleServerTextMessage([]byte(`{"type":"state","state":"thinking"}`)); err != nil {
		t.Fatalf("handle state JSON error = %v", err)
	}
	generation := requireUltravoxRealtimeGeneration(t, session)

	if err := session.handleServerTextMessage([]byte(`{"type":"client_tool_invocation","toolName":"lookup","invocationId":"call-9","parameters":{"city":"Paris"}}`)); err != nil {
		t.Fatalf("handle tool JSON error = %v", err)
	}
	select {
	case call := <-generation.FunctionCh:
		if call == nil {
			t.Fatal("function call = nil")
		}
		if call.CallID != "call-9" || call.Name != "lookup" || call.Arguments != `{"city":"Paris"}` {
			t.Fatalf("function call = %+v, want call-9 lookup JSON args", call)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for dispatched function call")
	}

	if err := session.handleServerTextMessage([]byte(`{"type":"playback_clear_buffer"}`)); err != nil {
		t.Fatalf("handle playback clear JSON error = %v", err)
	}
	select {
	case event := <-session.EventCh():
		if event.Type != llm.RealtimeEventTypeSpeechStarted {
			t.Fatalf("event type = %s, want speech_started", event.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for speech_started")
	}
}

func TestUltravoxRealtimeSessionPongQueuesReferencePing(t *testing.T) {
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

	if err := session.handleServerTextMessage([]byte(`{"type":"pong","timestamp":123.4}`)); err != nil {
		t.Fatalf("handle pong JSON error = %v", err)
	}
	select {
	case got := <-session.clientEventCh:
		if got["type"] != "ping" {
			t.Fatalf("pong response event type = %#v, want ping in %#v", got["type"], got)
		}
		timestamp, ok := got["timestamp"].(float64)
		if !ok || timestamp <= 0 {
			t.Fatalf("pong response timestamp = %#v, want positive float64", got["timestamp"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reference ping after pong")
	}
}

func requireUltravoxRealtimeGeneration(t *testing.T, session *realtimeSession) *llm.GenerationCreatedEvent {
	t.Helper()
	select {
	case event := <-session.EventCh():
		if event.Type != llm.RealtimeEventTypeGenerationCreated {
			t.Fatalf("event type = %s, want generation_created", event.Type)
		}
		if event.Generation == nil {
			t.Fatal("generation = nil")
		}
		return event.Generation
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for generation_created")
	}
	return nil
}

func requireUltravoxRealtimeMessage(t *testing.T, generation *llm.GenerationCreatedEvent) llm.MessageGeneration {
	t.Helper()
	select {
	case message := <-generation.MessageCh:
		return message
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message generation")
	}
	return llm.MessageGeneration{}
}

func requireUltravoxRealtimeText(t *testing.T, textCh <-chan string, want string) {
	t.Helper()
	select {
	case got := <-textCh:
		if got != want {
			t.Fatalf("text delta = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for text delta %q", want)
	}
}

func requireUltravoxRealtimeModalities(t *testing.T, modalitiesCh <-chan []string, want []string) {
	t.Helper()
	select {
	case got := <-modalitiesCh:
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("modalities = %#v, want %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for modalities %#v", want)
	}
}

func requireUltravoxRealtimeClosedText(t *testing.T, textCh <-chan string) {
	t.Helper()
	select {
	case _, ok := <-textCh:
		if ok {
			t.Fatal("text channel still open after final agent transcript")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for closed text channel")
	}
}

func requireUltravoxRealtimeClosedAudio(t *testing.T, audioCh <-chan *audiomodel.AudioFrame) {
	t.Helper()
	select {
	case _, ok := <-audioCh:
		if ok {
			t.Fatal("audio channel still open after final agent transcript")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for closed audio channel")
	}
}

func requireUltravoxRealtimeTranscriptEvent(t *testing.T, session *realtimeSession, itemID string, transcript string, final bool) {
	t.Helper()
	select {
	case event := <-session.EventCh():
		if event.Type != llm.RealtimeEventTypeInputAudioTranscriptionCompleted {
			t.Fatalf("event type = %s, want input_audio_transcription_completed", event.Type)
		}
		if event.InputTranscription == nil {
			t.Fatal("InputTranscription = nil")
		}
		if event.InputTranscription.ItemID != itemID ||
			event.InputTranscription.Transcript != transcript ||
			event.InputTranscription.IsFinal != final {
			t.Fatalf("InputTranscription = %+v, want item=%q transcript=%q final=%v", event.InputTranscription, itemID, transcript, final)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for transcript event")
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

type ultravoxRealtimeTestTool struct {
	name string
}

func (t ultravoxRealtimeTestTool) ID() string { return t.name }
func (t ultravoxRealtimeTestTool) Name() string {
	return t.name
}
func (t ultravoxRealtimeTestTool) Description() string { return "" }
func (t ultravoxRealtimeTestTool) Parameters() map[string]any {
	return nil
}
func (t ultravoxRealtimeTestTool) Execute(context.Context, string) (string, error) {
	return "", nil
}
