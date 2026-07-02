package aws

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	bedrockruntime "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	awstypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestAWSRealtimeSessionStartsReferenceBedrockStream(t *testing.T) {
	stream := &fakeAWSRealtimeStream{}
	client := &fakeAWSRealtimeClient{stream: stream}
	provider := NewAWSRealtimeModel("amazon.nova-sonic-v1:0",
		WithAWSRealtimeClient(client),
		WithAWSRealtimeVoice("matthew"),
		WithAWSRealtimeTurnDetection("HIGH"),
	)

	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if client.input == nil || client.input.ModelId == nil {
		t.Fatalf("InvokeModelWithBidirectionalStream input = %#v, want model id", client.input)
	}
	if *client.input.ModelId != "amazon.nova-sonic-v1:0" {
		t.Fatalf("model id = %q, want configured Nova Sonic model", *client.input.ModelId)
	}
	if len(stream.sent) != 6 {
		t.Fatalf("sent init event count = %d, want 6", len(stream.sent))
	}
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, stream.sent[0]), "event", "sessionStart", "endpointingSensitivity"); got != "HIGH" {
		t.Fatalf("endpointing sensitivity = %q, want HIGH", got)
	}
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, stream.sent[1]), "event", "promptStart", "audioOutputConfiguration", "voiceId"); got != "matthew" {
		t.Fatalf("voiceId = %q, want matthew", got)
	}
	audioStart := mustAWSRealtimeJSONEvent(t, stream.sent[5])
	if got := awsRealtimeNestedString(audioStart, "event", "contentStart", "type"); got != "AUDIO" {
		t.Fatalf("event[5] type = %q, want AUDIO", got)
	}
	assertAWSRealtimeJSONNumber(t, nestedMap(t, audioStart, "event", "contentStart", "audioInputConfiguration")["sampleRateHertz"], 16000)
}

func TestAWSRealtimeSessionStartErrorReturnsAPIConnectionError(t *testing.T) {
	client := &fakeAWSRealtimeClient{err: errors.New("bedrock invoke failed")}
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(client))

	session, err := provider.Session()

	if session != nil {
		t.Fatalf("Session = %#v, want nil", session)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Session error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(err.Error(), "AWS Nova Sonic realtime stream start failed") {
		t.Fatalf("Session error = %q, want Nova Sonic stream context", err.Error())
	}
}

func TestAWSRealtimeSessionStartSendErrorClosesStream(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	stream.sendErr = errors.New("bedrock send failed")
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))

	session, err := provider.Session()

	if session != nil {
		t.Fatalf("Session = %#v, want nil", session)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Session error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(err.Error(), "AWS Nova Sonic realtime startup send failed") {
		t.Fatalf("Session error = %q, want startup send context", err.Error())
	}
	if !stream.closed {
		t.Fatal("stream closed = false, want true after failed startup send")
	}
}

func TestAWSRealtimeSessionStartsWithReferenceUpdatedInstructions(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session := newAWSRealtimeSession(provider, &fakeAWSRealtimeClient{stream: stream})

	if err := session.UpdateInstructions("speak like a billing agent"); err != nil {
		t.Fatalf("UpdateInstructions error = %v", err)
	}
	if err := session.start(context.Background()); err != nil {
		t.Fatalf("start error = %v", err)
	}
	defer session.Close()

	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, stream.sent[3]), "event", "textInput", "content"); got != "speak like a billing agent" {
		t.Fatalf("system prompt = %q, want updated instructions", got)
	}
}

func TestAWSRealtimeSessionStartsWithReferenceTools(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session := newAWSRealtimeSession(provider, &fakeAWSRealtimeClient{stream: stream})

	if err := session.UpdateTools([]llm.Tool{awsRequestTestTool{}}); err != nil {
		t.Fatalf("UpdateTools error = %v", err)
	}
	if err := session.start(context.Background()); err != nil {
		t.Fatalf("start error = %v", err)
	}
	defer session.Close()

	toolConfig := nestedMap(t, mustAWSRealtimeJSONEvent(t, stream.sent[1]), "event", "promptStart", "toolConfiguration")
	tools, ok := toolConfig["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one tool", toolConfig["tools"])
	}
	spec := nestedMap(t, map[string]any{"tool": tools[0]}, "tool", "toolSpec")
	if spec["name"] != "lookup" || spec["description"] != "look up information" {
		t.Fatalf("toolSpec = %#v, want lookup tool", spec)
	}
	schema := awsRealtimeNestedString(map[string]any{"tool": tools[0]}, "tool", "toolSpec", "inputSchema", "json")
	if !strings.Contains(schema, `"query"`) {
		t.Fatalf("tool schema = %q, want query property", schema)
	}
	sessionStart := mustAWSRealtimeJSONEvent(t, stream.sent[0])
	inference := nestedMap(t, sessionStart, "event", "sessionStart", "inferenceConfiguration")
	assertAWSRealtimeJSONNumber(t, inference["topP"], 1.0)
	assertAWSRealtimeJSONNumber(t, inference["temperature"], 1.0)
}

func TestAWSRealtimeSessionStartsWithReferenceChatContext(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session := newAWSRealtimeSession(provider, &fakeAWSRealtimeClient{stream: stream})

	ctx := llm.NewChatContext()
	ctx.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleUser, Text: "hello"})
	ctx.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleAssistant, Text: "hi"})
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext before start error = %v", err)
	}
	if len(stream.sent) != 0 {
		t.Fatalf("UpdateChatContext before start sent %d events, want none", len(stream.sent))
	}

	if err := session.start(context.Background()); err != nil {
		t.Fatalf("start error = %v", err)
	}
	defer session.Close()

	if len(stream.sent) != 12 {
		t.Fatalf("sent event count = %d, want 12 with two history messages", len(stream.sent))
	}
	firstHistoryStart := mustAWSRealtimeJSONEvent(t, stream.sent[5])
	if got := awsRealtimeNestedString(firstHistoryStart, "event", "contentStart", "role"); got != "USER" {
		t.Fatalf("first history role = %q, want USER", got)
	}
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, stream.sent[6]), "event", "textInput", "content"); got != "hello" {
		t.Fatalf("first history text = %q, want hello", got)
	}
	secondHistoryStart := mustAWSRealtimeJSONEvent(t, stream.sent[8])
	if got := awsRealtimeNestedString(secondHistoryStart, "event", "contentStart", "role"); got != "ASSISTANT" {
		t.Fatalf("second history role = %q, want ASSISTANT", got)
	}
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, stream.sent[9]), "event", "textInput", "content"); got != "hi" {
		t.Fatalf("second history text = %q, want hi", got)
	}
	audioStart := mustAWSRealtimeJSONEvent(t, stream.sent[11])
	if got := awsRealtimeNestedString(audioStart, "event", "contentStart", "type"); got != "AUDIO" {
		t.Fatalf("event[11] type = %q, want AUDIO", got)
	}
}

func TestAWSRealtimeSessionDoesNotReplaySeededUserHistory(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session := newAWSRealtimeSession(provider, &fakeAWSRealtimeClient{stream: stream})

	ctx := llm.NewChatContext()
	ctx.AddMessage(llm.ChatMessageArgs{ID: "user-seeded", Role: llm.ChatRoleUser, Text: "already seeded"})
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext before start error = %v", err)
	}
	if err := session.start(context.Background()); err != nil {
		t.Fatalf("start error = %v", err)
	}
	defer session.Close()

	sentCount := len(stream.sent)
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext after start error = %v", err)
	}
	if len(stream.sent) != sentCount {
		t.Fatalf("seeded history replay sent %d new events, want none", len(stream.sent)-sentCount)
	}
}

func TestAWSRealtimeSessionPushAudioAndCloseSendReferenceEvents(t *testing.T) {
	stream := &fakeAWSRealtimeStream{}
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}

	if err := session.PushAudio(&model.AudioFrame{Data: []byte{1, 2, 3}, SampleRate: 16000, NumChannels: 1}); err != nil {
		t.Fatalf("PushAudio error = %v", err)
	}
	audioInput := mustAWSRealtimeJSONEvent(t, stream.sent[len(stream.sent)-1])
	if got := awsRealtimeNestedString(audioInput, "event", "audioInput", "content"); got != base64.StdEncoding.EncodeToString([]byte{1, 2, 3}) {
		t.Fatalf("audioInput content = %q, want base64 PCM", got)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	closeEvents := stream.sent[len(stream.sent)-3:]
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, closeEvents[0]), "event", "contentEnd", "contentName"); got == "" {
		t.Fatalf("contentEnd contentName empty")
	}
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, closeEvents[1]), "event", "promptEnd", "promptName"); got == "" {
		t.Fatalf("promptEnd promptName empty")
	}
	if _, ok := nestedMap(t, mustAWSRealtimeJSONEvent(t, closeEvents[2]), "event")["sessionEnd"].(map[string]any); !ok {
		t.Fatalf("sessionEnd event = %s", closeEvents[2])
	}
	if !stream.closed {
		t.Fatal("stream closed = false, want true")
	}
}

func TestAWSRealtimeSessionPushAudioAfterCloseIsIgnored(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	sentCount := len(stream.sent)

	err = session.PushAudio(&model.AudioFrame{
		Data:              []byte{1, 2},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	})
	if err != nil {
		t.Fatalf("PushAudio after Close error = %v, want nil like reference closed channel", err)
	}
	if len(stream.sent) != sentCount {
		t.Fatalf("PushAudio after Close sent %d events, want none", len(stream.sent)-sentCount)
	}
}

func TestAWSRealtimeSessionUpdateChatContextAfterCloseIsIgnored(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	sentCount := len(stream.sent)

	ctx := llm.NewChatContext()
	ctx.Append(&llm.ChatMessage{
		ID:      "user-after-close",
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "still there?"}},
	})
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext after Close error = %v, want nil", err)
	}
	if len(stream.sent) != sentCount {
		t.Fatalf("UpdateChatContext after Close sent %d events, want none", len(stream.sent)-sentCount)
	}
}

func TestAWSRealtimeSessionGenerateReplyAfterCloseIsIgnored(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModelWithNovaSonic2(WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	sentCount := len(stream.sent)

	err = session.GenerateReply(llm.RealtimeGenerateReplyOptions{Instructions: "ask again"})
	if err != nil {
		t.Fatalf("GenerateReply after Close error = %v, want nil", err)
	}
	if len(stream.sent) != sentCount {
		t.Fatalf("GenerateReply after Close sent %d events, want none", len(stream.sent)-sentCount)
	}
}

func TestAWSRealtimeSessionPushAudioNormalizesReferenceInputFormat(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	frame := awsRealtimeTestStereoFrame(48000, [][2]int16{
		{10, 30},
		{20, 40},
		{30, 50},
	})
	if err := session.PushAudio(frame); err != nil {
		t.Fatalf("PushAudio error = %v", err)
	}

	audioInput := mustAWSRealtimeJSONEvent(t, stream.sent[len(stream.sent)-1])
	content := awsRealtimeNestedString(audioInput, "event", "audioInput", "content")
	decoded, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		t.Fatalf("audioInput base64 decode error = %v", err)
	}
	if len(decoded) != 2 {
		t.Fatalf("normalized audio bytes = %d, want one 16-bit mono sample", len(decoded))
	}
	if got := int16(binary.LittleEndian.Uint16(decoded)); got != 20 {
		t.Fatalf("normalized sample = %d, want first downmixed 16k mono sample 20", got)
	}
}

func TestAWSRealtimeSessionMapsReferenceResponseEvents(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	stream.emitJSON(`{"event":{"textOutput":{"role":"USER","content":"hello","contentId":"user-1"}}}`)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeSpeechStarted)
	transcript := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)
	if transcript.InputTranscription == nil || transcript.InputTranscription.Transcript != "hello" || transcript.InputTranscription.IsFinal {
		t.Fatalf("transcript event = %#v, want interim hello", transcript)
	}

	audioBytes := []byte{1, 2, 3, 4}
	stream.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	stream.emitJSON(`{"event":{"contentStart":{"type":"AUDIO","role":"ASSISTANT","contentId":"audio-1"}}}`)
	stream.emitJSON(`{"event":{"audioOutput":{"contentId":"audio-1","content":"` + base64.StdEncoding.EncodeToString(audioBytes) + `"}}}`)
	audio := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeAudio)
	if string(audio.Data) != string(audioBytes) {
		t.Fatalf("audio data = %v, want %v", audio.Data, audioBytes)
	}
}

func TestAWSRealtimeSessionCreatesReferenceGenerationStreams(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	stream.emitJSON(`{"event":{"textOutput":{"role":"USER","content":"hello","contentId":"user-1"}}}`)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeSpeechStarted)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)

	stream.emitJSON(`{"event":{"contentStart":{"type":"TEXT","role":"ASSISTANT","contentId":"text-1","additionalModelFields":"{\"generationStage\":\"SPECULATIVE\"}"}}}`)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeSpeechStopped)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	if created.Generation == nil {
		t.Fatal("Generation = nil, want reference generation")
	}
	if created.Generation.MessageCh == nil || created.Generation.FunctionCh == nil {
		t.Fatalf("generation streams = %#v/%#v, want message and function streams", created.Generation.MessageCh, created.Generation.FunctionCh)
	}

	var msg llm.MessageGeneration
	select {
	case msg = <-created.Generation.MessageCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message generation")
	}
	if msg.MessageID == "" || msg.TextCh == nil || msg.AudioCh == nil || msg.ModalitiesCh == nil {
		t.Fatalf("message generation = %#v, want id plus text/audio/modalities streams", msg)
	}
	select {
	case modalities := <-msg.ModalitiesCh:
		if len(modalities) != 2 || modalities[0] != "audio" || modalities[1] != "text" {
			t.Fatalf("modalities = %v, want [audio text]", modalities)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for modalities")
	}

	audioBytes := []byte{1, 2, 3, 4}
	stream.emitJSON(`{"event":{"contentStart":{"type":"AUDIO","role":"ASSISTANT","contentId":"audio-1"}}}`)
	stream.emitJSON(`{"event":{"audioOutput":{"contentId":"audio-1","content":"` + base64.StdEncoding.EncodeToString(audioBytes) + `"}}}`)
	select {
	case frame := <-msg.AudioCh:
		if frame.SampleRate != defaultAWSRealtimeOutputSampleRate || frame.NumChannels != defaultAWSRealtimeChannels || string(frame.Data) != string(audioBytes) {
			t.Fatalf("audio frame = %#v, want reference output PCM frame", frame)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for generated audio")
	}

	stream.emitJSON(`{"event":{"textOutput":{"role":"ASSISTANT","content":"hi there","contentId":"text-1"}}}`)
	select {
	case text := <-msg.TextCh:
		if text != "hi there" {
			t.Fatalf("text delta = %q, want hi there", text)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for generated text")
	}
}

func TestAWSRealtimeSessionTracksReferenceAudioContentStartWithoutRole(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	stream.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	var msg llm.MessageGeneration
	select {
	case msg = <-created.Generation.MessageCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message generation")
	}
	select {
	case <-msg.ModalitiesCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for modalities")
	}

	audioBytes := []byte{9, 8, 7, 6}
	stream.emitJSON(`{"event":{"contentStart":{"type":"AUDIO","contentId":"audio-roleless"}}}`)
	stream.emitJSON(`{"event":{"audioOutput":{"contentId":"audio-roleless","content":"` + base64.StdEncoding.EncodeToString(audioBytes) + `"}}}`)
	audio := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeAudio)
	if string(audio.Data) != string(audioBytes) {
		t.Fatalf("audio data = %v, want %v", audio.Data, audioBytes)
	}
	select {
	case frame := <-msg.AudioCh:
		if string(frame.Data) != string(audioBytes) {
			t.Fatalf("generation audio = %v, want %v", frame.Data, audioBytes)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for generated audio")
	}
}

func TestAWSRealtimeSessionCreatesReferenceGenerationOnCompletionStart(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	stream.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	if created.Generation == nil || created.Generation.ResponseID == "" {
		t.Fatalf("generation = %#v, want response id", created.Generation)
	}
	if created.Generation.MessageCh == nil || created.Generation.FunctionCh == nil {
		t.Fatalf("generation streams = %#v/%#v, want reference streams", created.Generation.MessageCh, created.Generation.FunctionCh)
	}
	select {
	case msg := <-created.Generation.MessageCh:
		if msg.MessageID != created.Generation.ResponseID || msg.TextCh == nil || msg.AudioCh == nil || msg.ModalitiesCh == nil {
			t.Fatalf("message generation = %#v, want response id and streams", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for completionStart message generation")
	}
}

func TestAWSRealtimeSessionClosesReferenceGenerationOnCompletionEnd(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	stream.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	var msg llm.MessageGeneration
	select {
	case msg = <-created.Generation.MessageCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message generation")
	}

	stream.emitJSON(`{"event":{"completionEnd":{"completionId":"completion-1"}}}`)
	select {
	case _, ok := <-msg.TextCh:
		if ok {
			t.Fatal("TextCh still open, want closed on completionEnd")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for TextCh close")
	}
	select {
	case _, ok := <-msg.AudioCh:
		if ok {
			t.Fatal("AudioCh still open, want closed on completionEnd")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for AudioCh close")
	}
	select {
	case _, ok := <-created.Generation.MessageCh:
		if ok {
			t.Fatal("MessageCh still open, want closed on completionEnd")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for MessageCh close")
	}
	select {
	case _, ok := <-created.Generation.FunctionCh:
		if ok {
			t.Fatal("FunctionCh still open, want closed on completionEnd")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for FunctionCh close")
	}
}

func TestAWSRealtimeSessionClosesReferenceGenerationOnBargeIn(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	stream.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	var msg llm.MessageGeneration
	select {
	case msg = <-created.Generation.MessageCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message generation")
	}

	stream.emitJSON(`{"event":{"textOutput":{"role":"ASSISTANT","content":` + strconv.Quote(awsRealtimeBargeInContent) + `,"contentId":"barge-1"}}}`)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeSpeechStarted)
	select {
	case _, ok := <-msg.TextCh:
		if ok {
			t.Fatal("TextCh still open, want closed on barge-in")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for TextCh close")
	}
	select {
	case _, ok := <-msg.AudioCh:
		if ok {
			t.Fatal("AudioCh still open, want closed on barge-in")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for AudioCh close")
	}
	select {
	case _, ok := <-created.Generation.FunctionCh:
		if ok {
			t.Fatal("FunctionCh still open, want closed on barge-in")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for FunctionCh close")
	}
}

func TestAWSRealtimeSessionInterruptClosesReferenceGeneration(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	stream.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	var msg llm.MessageGeneration
	select {
	case msg = <-created.Generation.MessageCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message generation")
	}

	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt error = %v", err)
	}
	select {
	case _, ok := <-msg.TextCh:
		if ok {
			t.Fatal("TextCh still open, want closed on interrupt")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for TextCh close")
	}
	select {
	case _, ok := <-msg.AudioCh:
		if ok {
			t.Fatal("AudioCh still open, want closed on interrupt")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for AudioCh close")
	}
	select {
	case _, ok := <-created.Generation.FunctionCh:
		if ok {
			t.Fatal("FunctionCh still open, want closed on interrupt")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for FunctionCh close")
	}
}

func TestAWSRealtimeSessionFiltersReferenceGenerationContent(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	stream.emitJSON(`{"event":{"textOutput":{"role":"USER","content":"hello","contentId":"user-1"}}}`)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeSpeechStarted)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)
	stream.emitJSON(`{"event":{"contentStart":{"type":"TEXT","role":"ASSISTANT","contentId":"text-1","additionalModelFields":"{\"generationStage\":\"SPECULATIVE\"}"}}}`)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeSpeechStopped)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)

	var msg llm.MessageGeneration
	select {
	case msg = <-created.Generation.MessageCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message generation")
	}
	select {
	case <-msg.ModalitiesCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for modalities")
	}

	stream.emitJSON(`{"event":{"contentStart":{"type":"TEXT","role":"ASSISTANT","contentId":"final-1","additionalModelFields":"{\"generationStage\":\"FINAL\"}"}}}`)
	stream.emitJSON(`{"event":{"textOutput":{"role":"ASSISTANT","content":"final transcript","contentId":"final-1"}}}`)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeText)
	select {
	case text := <-msg.TextCh:
		t.Fatalf("generation text delta = %q, want final assistant text filtered from stream", text)
	default:
	}

	audioBytes := []byte{5, 6, 7, 8}
	stream.emitJSON(`{"event":{"audioOutput":{"contentId":"untracked-audio","content":"` + base64.StdEncoding.EncodeToString(audioBytes) + `"}}}`)
	assertNoAWSRealtimeEvent(t, session.EventCh())
	select {
	case frame := <-msg.AudioCh:
		t.Fatalf("generation audio frame = %#v, want untracked audio filtered from stream", frame)
	default:
	}
}

func TestAWSRealtimeSessionMapsReferenceToolUseEvent(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	stream.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	<-created.Generation.MessageCh
	stream.emitJSON(`{"event":{"contentStart":{"type":"TOOL","role":"TOOL","contentId":"tool-content-1"}}}`)
	stream.emitJSON(`{"event":{"toolUse":{"toolUseId":"tool-1","toolName":"lookup","content":"{\"query\":\"weather\"}"}}}`)

	var call *llm.FunctionCall
	select {
	case call = <-created.Generation.FunctionCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for function stream call")
	}
	if call.CallID != "tool-1" || call.Name != "lookup" {
		t.Fatalf("function call = %#v, want lookup tool-1", call)
	}
	if call.Arguments != `{"query":"weather"}` {
		t.Fatalf("arguments = %q, want reference tool content", call.Arguments)
	}
	assertNoAWSRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeFunctionCall)
}

func TestAWSRealtimeSessionIgnoresReferenceToolUseWithoutGeneration(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	stream.emitJSON(`{"event":{"toolUse":{"toolUseId":"tool-stray","toolName":"lookup","content":"{}"}}}`)
	assertNoAWSRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeFunctionCall)

	ctx := llm.NewChatContext()
	ctx.Append(&llm.FunctionCallOutput{
		CallID: "tool-stray",
		Name:   "lookup",
		Output: `{"forecast":"sunny"}`,
	})
	sentCount := len(stream.sent)
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext error = %v", err)
	}
	if len(stream.sent) != sentCount {
		t.Fatalf("stray tool result sent %d events, want none", len(stream.sent)-sentCount)
	}
}

func TestAWSRealtimeSessionUpdateChatContextSendsReferenceToolResult(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	stream.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	<-created.Generation.MessageCh
	stream.emitJSON(`{"event":{"contentStart":{"type":"TOOL","role":"TOOL","contentId":"tool-content-1"}}}`)
	stream.emitJSON(`{"event":{"toolUse":{"toolUseId":"tool-1","toolName":"lookup","content":"{\"query\":\"weather\"}"}}}`)
	select {
	case <-created.Generation.FunctionCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for function stream call")
	}

	ctx := llm.NewChatContext()
	ctx.Append(&llm.FunctionCallOutput{
		CallID: "tool-1",
		Name:   "lookup",
		Output: `{"forecast":"sunny"}`,
	})
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext error = %v", err)
	}

	toolEvents := stream.sent[len(stream.sent)-3:]
	start := mustAWSRealtimeJSONEvent(t, toolEvents[0])
	if got := awsRealtimeNestedString(start, "event", "contentStart", "type"); got != "TOOL" {
		t.Fatalf("tool contentStart type = %q, want TOOL", got)
	}
	if got := awsRealtimeNestedString(start, "event", "contentStart", "toolResultInputConfiguration", "toolUseId"); got != "tool-1" {
		t.Fatalf("toolUseId = %q, want tool-1", got)
	}
	result := mustAWSRealtimeJSONEvent(t, toolEvents[1])
	if got := awsRealtimeNestedString(result, "event", "toolResult", "content"); got != `{"forecast":"sunny"}` {
		t.Fatalf("tool result content = %q, want output", got)
	}
	end := mustAWSRealtimeJSONEvent(t, toolEvents[2])
	if got := awsRealtimeNestedString(end, "event", "contentEnd", "contentName"); got == "" {
		t.Fatal("tool contentEnd contentName empty")
	}
}

func TestAWSRealtimeSessionWrapsReferencePlainToolResult(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	stream.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	<-created.Generation.MessageCh
	stream.emitJSON(`{"event":{"contentStart":{"type":"TOOL","role":"TOOL","contentId":"tool-content-1"}}}`)
	stream.emitJSON(`{"event":{"toolUse":{"toolUseId":"tool-plain","toolName":"lookup","content":"{}"}}}`)
	select {
	case <-created.Generation.FunctionCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for function stream call")
	}

	ctx := llm.NewChatContext()
	ctx.Append(&llm.FunctionCallOutput{
		CallID: "tool-plain",
		Name:   "lookup",
		Output: "sunny",
	})
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext error = %v", err)
	}

	result := mustAWSRealtimeJSONEvent(t, stream.sent[len(stream.sent)-2])
	if got := awsRealtimeNestedString(result, "event", "toolResult", "content"); got != `{"tool_result":"sunny"}` {
		t.Fatalf("plain tool result content = %q, want JSON wrapper", got)
	}
}

func TestAWSRealtimeSessionWrapsReferenceToolErrorResult(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	stream.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	<-created.Generation.MessageCh
	stream.emitJSON(`{"event":{"contentStart":{"type":"TOOL","role":"TOOL","contentId":"tool-content-1"}}}`)
	stream.emitJSON(`{"event":{"toolUse":{"toolUseId":"tool-err","toolName":"lookup","content":"{}"}}}`)
	select {
	case <-created.Generation.FunctionCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for function stream call")
	}

	ctx := llm.NewChatContext()
	ctx.Append(&llm.FunctionCallOutput{
		CallID:  "tool-err",
		Name:    "lookup",
		Output:  "boom",
		IsError: true,
	})
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext error = %v", err)
	}

	result := mustAWSRealtimeJSONEvent(t, stream.sent[len(stream.sent)-2])
	if got := awsRealtimeNestedString(result, "event", "toolResult", "content"); got != `{"error":"boom"}` {
		t.Fatalf("tool error content = %q, want JSON error", got)
	}
}

func TestAWSRealtimeSessionUpdateChatContextSendsInteractiveUserText(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	ctx := llm.NewChatContext()
	ctx.Append(&llm.ChatMessage{
		ID:      "user-1",
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "hello sonic"}},
	})
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext error = %v", err)
	}
	textEvents := stream.sent[len(stream.sent)-3:]
	start := mustAWSRealtimeJSONEvent(t, textEvents[0])
	if got := awsRealtimeNestedString(start, "event", "contentStart", "type"); got != "TEXT" {
		t.Fatalf("text contentStart type = %q, want TEXT", got)
	}
	if got := awsRealtimeNestedString(start, "event", "contentStart", "role"); got != "USER" {
		t.Fatalf("text role = %q, want USER", got)
	}
	if got := nestedMap(t, start, "event", "contentStart")["interactive"]; got != true {
		t.Fatalf("interactive = %v, want true", got)
	}
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, textEvents[1]), "event", "textInput", "content"); got != "hello sonic" {
		t.Fatalf("text input = %q, want hello sonic", got)
	}

	sentCount := len(stream.sent)
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext repeat error = %v", err)
	}
	if len(stream.sent) != sentCount {
		t.Fatalf("repeat UpdateChatContext sent %d new events, want none", len(stream.sent)-sentCount)
	}
}

func TestAWSRealtimeSessionGenerateReplySendsReferenceInstructions(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModelWithNovaSonic2(WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{Instructions: "ask for the card number"}); err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}

	textEvents := stream.sent[len(stream.sent)-3:]
	start := mustAWSRealtimeJSONEvent(t, textEvents[0])
	if got := awsRealtimeNestedString(start, "event", "contentStart", "type"); got != "TEXT" {
		t.Fatalf("text contentStart type = %q, want TEXT", got)
	}
	if got := awsRealtimeNestedString(start, "event", "contentStart", "role"); got != "USER" {
		t.Fatalf("text role = %q, want USER", got)
	}
	if got := nestedMap(t, start, "event", "contentStart")["interactive"]; got != true {
		t.Fatalf("interactive = %v, want true", got)
	}
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, textEvents[1]), "event", "textInput", "content"); got != "ask for the card number" {
		t.Fatalf("text input = %q, want instructions", got)
	}
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, textEvents[2]), "event", "contentEnd", "contentName"); got == "" {
		t.Fatal("contentEnd contentName empty")
	}
}

func TestAWSRealtimeSessionMapsReferenceUsageMetrics(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("amazon.nova-sonic-v1:0", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	stream.emitJSON(`{"event":{"usageEvent":{"completionId":"completion-1","details":{"delta":{"input":{"speechTokens":3,"textTokens":4},"output":{"speechTokens":5,"textTokens":6}}}}}}`)

	event := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeMetricsCollected)
	if event.Metrics == nil {
		t.Fatal("Metrics = nil")
	}
	if event.Metrics.RequestID != "completion-1" {
		t.Fatalf("RequestID = %q, want completion-1", event.Metrics.RequestID)
	}
	if event.Metrics.InputTokens != 7 || event.Metrics.OutputTokens != 11 || event.Metrics.TotalTokens != 18 {
		t.Fatalf("token counts = input %d output %d total %d, want 7/11/18", event.Metrics.InputTokens, event.Metrics.OutputTokens, event.Metrics.TotalTokens)
	}
	if event.Metrics.InputTokenDetails.AudioTokens != 3 || event.Metrics.InputTokenDetails.TextTokens != 4 {
		t.Fatalf("input details = %+v, want audio=3 text=4", event.Metrics.InputTokenDetails)
	}
	if event.Metrics.OutputTokenDetails.AudioTokens != 5 || event.Metrics.OutputTokenDetails.TextTokens != 6 {
		t.Fatalf("output details = %+v, want audio=5 text=6", event.Metrics.OutputTokenDetails)
	}
	if event.Metrics.Metadata == nil || event.Metrics.Metadata.ModelName != "amazon.nova-sonic-v1:0" || event.Metrics.Metadata.ModelProvider != "Amazon" {
		t.Fatalf("metadata = %+v, want AWS Nova Sonic", event.Metrics.Metadata)
	}
}

func assertAWSRealtimeEvent(t *testing.T, ch <-chan llm.RealtimeEvent, want llm.RealtimeEventType) llm.RealtimeEvent {
	t.Helper()
	select {
	case event := <-ch:
		if event.Type != want {
			t.Fatalf("event type = %s, want %s: %#v", event.Type, want, event)
		}
		return event
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", want)
		return llm.RealtimeEvent{}
	}
}

func assertNoAWSRealtimeEvent(t *testing.T, ch <-chan llm.RealtimeEvent) {
	t.Helper()
	select {
	case event := <-ch:
		t.Fatalf("unexpected event: %#v", event)
	case <-time.After(50 * time.Millisecond):
	}
}

func assertNoAWSRealtimeEventType(t *testing.T, ch <-chan llm.RealtimeEvent, unwanted llm.RealtimeEventType) {
	t.Helper()
	deadline := time.After(50 * time.Millisecond)
	for {
		select {
		case event := <-ch:
			if event.Type == unwanted {
				t.Fatalf("unexpected %s event: %#v", unwanted, event)
			}
		case <-deadline:
			return
		}
	}
}

type fakeAWSRealtimeClient struct {
	input  *bedrockruntime.InvokeModelWithBidirectionalStreamInput
	stream awsRealtimeStream
	err    error
}

func (c *fakeAWSRealtimeClient) InvokeModelWithBidirectionalStream(ctx context.Context, input *bedrockruntime.InvokeModelWithBidirectionalStreamInput) (awsRealtimeStream, error) {
	c.input = input
	if c.err != nil {
		return nil, c.err
	}
	return c.stream, nil
}

type fakeAWSRealtimeStream struct {
	sent    []string
	closed  bool
	sendErr error
	events  chan awstypes.InvokeModelWithBidirectionalStreamOutput
}

func awsRealtimeTestStereoFrame(sampleRate uint32, samples [][2]int16) *model.AudioFrame {
	data := make([]byte, len(samples)*4)
	for i, sample := range samples {
		binary.LittleEndian.PutUint16(data[i*4:i*4+2], uint16(sample[0]))
		binary.LittleEndian.PutUint16(data[i*4+2:i*4+4], uint16(sample[1]))
	}
	return &model.AudioFrame{
		Data:              data,
		SampleRate:        sampleRate,
		NumChannels:       2,
		SamplesPerChannel: uint32(len(samples)),
	}
}

func newFakeAWSRealtimeStream() *fakeAWSRealtimeStream {
	return &fakeAWSRealtimeStream{events: make(chan awstypes.InvokeModelWithBidirectionalStreamOutput, 8)}
}

func (s *fakeAWSRealtimeStream) emitJSON(raw string) {
	s.events <- &awstypes.InvokeModelWithBidirectionalStreamOutputMemberChunk{
		Value: awstypes.BidirectionalOutputPayloadPart{Bytes: []byte(raw)},
	}
}

func (s *fakeAWSRealtimeStream) Send(_ context.Context, event awstypes.InvokeModelWithBidirectionalStreamInput) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	chunk, ok := event.(*awstypes.InvokeModelWithBidirectionalStreamInputMemberChunk)
	if !ok {
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(chunk.Value.Bytes, &decoded); err == nil {
		encoded, _ := json.Marshal(decoded)
		s.sent = append(s.sent, string(encoded))
		return nil
	}
	s.sent = append(s.sent, string(chunk.Value.Bytes))
	return nil
}

func (s *fakeAWSRealtimeStream) Events() <-chan awstypes.InvokeModelWithBidirectionalStreamOutput {
	if s.events == nil {
		s.events = make(chan awstypes.InvokeModelWithBidirectionalStreamOutput)
		close(s.events)
	}
	return s.events
}

func (s *fakeAWSRealtimeStream) Close() error {
	s.closed = true
	return nil
}
