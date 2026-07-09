package speechmatics

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

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

func TestSpeechmaticsRealtimeGenerateReplyPreservesPerResponseTools(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	toolChoice := map[string]any{
		"type": "function",
		"name": "lookup_weather",
	}
	err = session.GenerateReply(llm.RealtimeGenerateReplyOptions{
		Tools: []llm.Tool{speechmaticsRealtimeTestTool{
			name:        "lookup_weather",
			description: "look up weather",
			parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{"type": "string"},
				},
			},
		}},
		ToolChoice: toolChoice,
	})
	if err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}
	command := nextSpeechmaticsRealtimeCommand(t, session)
	if command["type"] != "response.create" {
		t.Fatalf("command type = %#v, want response.create", command["type"])
	}
	tools, ok := command["tools"].([]map[string]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one formatted function tool", command["tools"])
	}
	tool := tools[0]
	if tool["type"] != "function" || tool["name"] != "lookup_weather" || tool["description"] != "look up weather" {
		t.Fatalf("tool = %#v, want function lookup_weather", tool)
	}
	parameters, ok := tool["parameters"].(map[string]any)
	if !ok || parameters["type"] != "object" {
		t.Fatalf("parameters = %#v, want object schema", tool["parameters"])
	}
	gotToolChoice, ok := command["tool_choice"].(map[string]any)
	if !ok || gotToolChoice["type"] != "function" || gotToolChoice["name"] != "lookup_weather" {
		t.Fatalf("tool_choice = %#v, want original map", command["tool_choice"])
	}
}

func TestSpeechmaticsRealtimeSessionBuffersBurstAudioCommandsInOrder(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	for i := 0; i < 300; i++ {
		frame := &model.AudioFrame{
			Data:              []byte{byte(i % 251)},
			SampleRate:        16000,
			NumChannels:       1,
			SamplesPerChannel: 1,
		}
		if err := session.PushAudio(frame); err != nil {
			t.Fatalf("PushAudio #%d error = %v, want buffered command", i, err)
		}
	}
	for i := 0; i < 300; i++ {
		command := nextSpeechmaticsRealtimeCommand(t, session)
		if command["type"] != "input_audio_buffer.append" {
			t.Fatalf("command #%d type = %#v, want input_audio_buffer.append", i, command["type"])
		}
		data, ok := command["audio"].([]byte)
		if !ok || len(data) != 1 || data[0] != byte(i%251) {
			t.Fatalf("command #%d audio = %#v, want ordered byte %d", i, command["audio"], byte(i%251))
		}
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
	if err := session.PushAudio(&model.AudioFrame{Data: []byte{0x01, 0x02}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err != nil {
		t.Fatalf("PushAudio after Close error = %v, want nil", err)
	}
}

func TestSpeechmaticsRealtimeSessionIgnoresLateClientEventsAfterClose(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	rtSession := session.(*speechmaticsRealtimeSession)

	lateCalls := []struct {
		name string
		call func() error
	}{
		{name: "UpdateInstructions", call: func() error { return session.UpdateInstructions("late") }},
		{name: "UpdateOptions", call: func() error { return session.UpdateOptions(llm.RealtimeSessionOptions{Voice: "late", VoiceSet: true}) }},
		{name: "GenerateReply", call: func() error {
			return session.GenerateReply(llm.RealtimeGenerateReplyOptions{Instructions: "late", InstructionsSet: true})
		}},
		{name: "Say", call: func() error { return session.Say("late") }},
		{name: "PushAudio", call: func() error {
			return session.PushAudio(&model.AudioFrame{Data: []byte{0x01}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1})
		}},
		{name: "CommitAudio", call: session.CommitAudio},
		{name: "ClearAudio", call: session.ClearAudio},
		{name: "Interrupt", call: session.Interrupt},
		{name: "Truncate", call: func() error { return session.Truncate(llm.RealtimeTruncateOptions{}) }},
		{name: "PushVideo", call: func() error { return session.PushVideo(&images.VideoFrame{}) }},
	}
	for _, tc := range lateCalls {
		if err := tc.call(); err != nil {
			t.Fatalf("%s after Close error = %v, want nil", tc.name, err)
		}
	}
	if rtSession.instructions != defaultSpeechmaticsRealtimeSystemPrompt {
		t.Fatalf("instructions after late update = %q, want original", rtSession.instructions)
	}
	if rtSession.voice != defaultSpeechmaticsRealtimeVoice {
		t.Fatalf("voice after late update = %q, want original", rtSession.voice)
	}
}

func TestSpeechmaticsRealtimeSessionServerEventsEmitReferenceGenerationStreams(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key", WithRealtimeOutputSampleRate(24000))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*speechmaticsRealtimeSession)
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	if ok := session.handleServerEvent(map[string]any{"type": "input_audio_buffer.speech_started"}); !ok {
		t.Fatal("speech_started event ignored")
	}
	assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeSpeechStarted)
	if ok := session.handleServerEvent(map[string]any{
		"type":       "conversation.item.input_audio_transcription.completed",
		"item_id":    "msg_user_1",
		"transcript": "hello",
	}); !ok {
		t.Fatal("input transcription event ignored")
	}
	transcript := assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)
	if transcript.InputTranscription == nil || !transcript.InputTranscription.IsFinal || transcript.InputTranscription.Transcript != "hello" {
		t.Fatalf("input transcription = %#v, want final hello", transcript.InputTranscription)
	}

	if ok := session.handleServerEvent(map[string]any{"type": "response.created", "response_id": "resp_1"}); !ok {
		t.Fatal("response.created event ignored")
	}
	created := assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	if created.Generation == nil || created.Generation.ResponseID != "resp_1" {
		t.Fatalf("generation = %#v, want resp_1", created.Generation)
	}
	if ok := session.handleServerEvent(map[string]any{"type": "response.output_item.added", "item_id": "msg_agent_1"}); !ok {
		t.Fatal("output item event ignored")
	}
	message := assertSpeechmaticsRealtimeMessage(t, created.Generation.MessageCh)
	if message.MessageID != "msg_agent_1" {
		t.Fatalf("message id = %q, want msg_agent_1", message.MessageID)
	}

	audio := []byte{1, 2, 3, 4}
	for _, event := range []map[string]any{
		{"type": "response.output_audio_transcript.delta", "item_id": "msg_agent_1", "delta": "Hi "},
		{"type": "response.output_text.delta", "item_id": "msg_agent_1", "delta": "there"},
		{"type": "response.output_audio.delta", "item_id": "msg_agent_1", "delta": base64.StdEncoding.EncodeToString(audio)},
		{"type": "response.output_item.done", "item_id": "msg_agent_1"},
	} {
		if ok := session.handleServerEvent(event); !ok {
			t.Fatalf("server event ignored: %#v", event)
		}
	}
	if got := assertSpeechmaticsRealtimeText(t, message.TextCh); got != "Hi " {
		t.Fatalf("first text delta = %q, want Hi ", got)
	}
	if got := assertSpeechmaticsRealtimeText(t, message.TextCh); got != "there" {
		t.Fatalf("second text delta = %q, want there", got)
	}
	frame := assertSpeechmaticsRealtimeAudio(t, message.AudioCh)
	if frame.SampleRate != 24000 || frame.NumChannels != 1 || int(frame.SamplesPerChannel) != len(audio)/2 {
		t.Fatalf("audio frame = rate %d channels %d samples %d, want 24000/1/%d", frame.SampleRate, frame.NumChannels, frame.SamplesPerChannel, len(audio)/2)
	}
	if !bytes.Equal(frame.Data, audio) {
		t.Fatalf("audio data = %#v, want %#v", frame.Data, audio)
	}
	assertSpeechmaticsRealtimeClosedText(t, message.TextCh)
	assertSpeechmaticsRealtimeClosedAudio(t, message.AudioCh)
}

func assertSpeechmaticsRealtimeCommand(t *testing.T, session llm.RealtimeSession, wantType, key string, want any) {
	t.Helper()
	command := nextSpeechmaticsRealtimeCommand(t, session)
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
}

func nextSpeechmaticsRealtimeCommand(t *testing.T, session llm.RealtimeSession) map[string]any {
	t.Helper()
	rtSession := session.(*speechmaticsRealtimeSession)
	select {
	case command := <-rtSession.commandCh:
		return command
	case <-time.After(time.Second):
		t.Fatal("missing realtime command")
	}
	return nil
}

func assertSpeechmaticsRealtimeEventType(t *testing.T, ch <-chan llm.RealtimeEvent, want llm.RealtimeEventType) llm.RealtimeEvent {
	t.Helper()
	select {
	case event, ok := <-ch:
		if !ok {
			t.Fatalf("event channel closed, want %s", want)
		}
		if event.Type != want {
			t.Fatalf("event type = %s, want %s", event.Type, want)
		}
		return event
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", want)
	}
	return llm.RealtimeEvent{}
}

func assertSpeechmaticsRealtimeMessage(t *testing.T, ch <-chan llm.MessageGeneration) llm.MessageGeneration {
	t.Helper()
	select {
	case message, ok := <-ch:
		if !ok {
			t.Fatal("message channel closed")
		}
		return message
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message generation")
	}
	return llm.MessageGeneration{}
}

func assertSpeechmaticsRealtimeText(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case text, ok := <-ch:
		if !ok {
			t.Fatal("text channel closed")
		}
		return text
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for text delta")
	}
	return ""
}

func assertSpeechmaticsRealtimeAudio(t *testing.T, ch <-chan *model.AudioFrame) *model.AudioFrame {
	t.Helper()
	select {
	case frame, ok := <-ch:
		if !ok {
			t.Fatal("audio channel closed")
		}
		return frame
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for audio delta")
	}
	return nil
}

func assertSpeechmaticsRealtimeClosedText(t *testing.T, ch <-chan string) {
	t.Helper()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("text channel still open")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for closed text channel")
	}
}

func assertSpeechmaticsRealtimeClosedAudio(t *testing.T, ch <-chan *model.AudioFrame) {
	t.Helper()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("audio channel still open")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for closed audio channel")
	}
}

type speechmaticsRealtimeTestTool struct {
	name        string
	description string
	parameters  map[string]any
}

func (t speechmaticsRealtimeTestTool) ID() string          { return t.name }
func (t speechmaticsRealtimeTestTool) Name() string        { return t.name }
func (t speechmaticsRealtimeTestTool) Description() string { return t.description }
func (t speechmaticsRealtimeTestTool) Parameters() map[string]any {
	return t.parameters
}
func (t speechmaticsRealtimeTestTool) Execute(context.Context, string) (string, error) {
	return "", nil
}
