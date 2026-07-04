package aws

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	bedrockruntime "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	awstypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/google/uuid"
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
	waitAWSRealtimeAudioContentStart(t, stream, 0)
	sent := stream.snapshotSent()
	if len(sent) != 6 {
		t.Fatalf("sent init event count = %d, want 6", len(sent))
	}
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, sent[0]), "event", "sessionStart", "endpointingSensitivity"); got != "HIGH" {
		t.Fatalf("endpointing sensitivity = %q, want HIGH", got)
	}
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, sent[1]), "event", "promptStart", "audioOutputConfiguration", "voiceId"); got != "matthew" {
		t.Fatalf("voiceId = %q, want matthew", got)
	}
	audioStart := waitAWSRealtimeAudioContentStart(t, stream, 5)
	if got := awsRealtimeNestedString(audioStart, "event", "contentStart", "type"); got != "AUDIO" {
		t.Fatalf("event[5] type = %q, want AUDIO", got)
	}
	assertAWSRealtimeJSONNumber(t, nestedMap(t, audioStart, "event", "contentStart", "audioInputConfiguration")["sampleRateHertz"], 16000)
}

func TestAWSRealtimeSessionStartsReferenceReaderBeforeAudioInput(t *testing.T) {
	stream := &fakeAWSRealtimeStream{}
	client := &fakeAWSRealtimeClient{stream: stream}
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(client))

	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	if stream.audioSentBeforeEvents.Load() {
		t.Fatal("audio contentStart sent before response Events reader started")
	}
}

func TestAWSRealtimeSessionDoesNotBlockOnReferenceAudioContentStart(t *testing.T) {
	stream := &blockingAudioContentStartAWSRealtimeStream{
		fakeAWSRealtimeStream: newFakeAWSRealtimeStream(),
		started:               make(chan struct{}),
		release:               make(chan struct{}),
	}
	t.Cleanup(func() { close(stream.release) })
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))

	sessionCh := make(chan llm.RealtimeSession, 1)
	errCh := make(chan error, 1)
	go func() {
		session, err := provider.Session()
		if err == nil {
			sessionCh <- session
		}
		errCh <- err
	}()

	select {
	case <-stream.started:
	case <-time.After(time.Second):
		t.Fatal("Session did not start provider audio contentStart send")
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Session error = %v, want nil while audio contentStart send continues asynchronously", err)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("Session blocked on provider audio contentStart send")
	}

	session := <-sessionCh
	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
}

func TestAWSRealtimeSessionUsesReferenceDefaultSystemPrompt(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))

	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	sent := stream.snapshotSent()
	prompt := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, sent[3]), "event", "textInput", "content")
	if !strings.Contains(prompt, "CRITICAL LANGUAGE MIRRORING RULES") {
		t.Fatalf("default system prompt missing reference language mirroring rules: %q", prompt)
	}
	if !strings.Contains(prompt, "Do not make up information or make assumptions") {
		t.Fatalf("default system prompt missing reference truthfulness guard: %q", prompt)
	}
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

func TestAWSRealtimeSessionStartErrorClosesReferenceAudioSender(t *testing.T) {
	before := awsRealtimeAudioSenderGoroutines()
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
	deadline := time.After(100 * time.Millisecond)
	for {
		got := awsRealtimeAudioSenderGoroutines()
		if got > before {
			t.Fatalf("audio sender goroutines = %d, want %d after failed start cleanup", got, before)
		}
		select {
		case <-deadline:
			return
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func TestAWSRealtimeSessionStartDeadlineReturnsAPITimeoutError(t *testing.T) {
	client := &fakeAWSRealtimeClient{err: context.DeadlineExceeded}
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(client))

	session, err := provider.Session()

	if session != nil {
		t.Fatalf("Session = %#v, want nil", session)
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Session error = %T %v, want APITimeoutError", err, err)
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

func TestAWSRealtimeSessionStartSendDeadlineReturnsAPITimeoutError(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	stream.sendErr = context.DeadlineExceeded
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))

	session, err := provider.Session()

	if session != nil {
		t.Fatalf("Session = %#v, want nil", session)
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Session error = %T %v, want APITimeoutError", err, err)
	}
	if !stream.closed {
		t.Fatal("stream closed = false, want true after timeout startup send")
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

	sent := stream.snapshotSent()
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, sent[3]), "event", "textInput", "content"); got != "speak like a billing agent" {
		t.Fatalf("system prompt = %q, want updated instructions", got)
	}
}

func TestAWSRealtimeSessionRestartUsesReferenceUpdatedInstructions(t *testing.T) {
	first := newFakeAWSRealtimeStream()
	second := newFakeAWSRealtimeStream()
	first.err = errors.New("ValidationException: System instability detected. Please retry your request.")
	client := &fakeAWSRealtimeClient{streams: []awsRealtimeStream{first, second}}
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(client))
	session := newAWSRealtimeSession(provider, client)
	if err := session.start(context.Background()); err != nil {
		t.Fatalf("start error = %v", err)
	}
	defer session.Close()
	waitAWSRealtimeAudioContentStart(t, first, 0)

	if err := session.UpdateInstructions("speak like an escalation agent"); err != nil {
		t.Fatalf("UpdateInstructions error = %v", err)
	}
	close(first.events)

	event := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeSessionReconnected)
	if event.Reconnect == nil {
		t.Fatal("Reconnect = nil, want reference restart notification")
	}
	waitAWSRealtimeAudioContentStart(t, second, 0)
	texts := awsRealtimeSentTextInputContents(t, second.snapshotSent())
	if len(texts) == 0 || texts[0] != "speak like an escalation agent" {
		t.Fatalf("restart text inputs = %v, want updated system instructions first", texts)
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

	sent := stream.snapshotSent()
	toolConfig := nestedMap(t, mustAWSRealtimeJSONEvent(t, sent[1]), "event", "promptStart", "toolConfiguration")
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
	sessionStart := mustAWSRealtimeJSONEvent(t, sent[0])
	inference := nestedMap(t, sessionStart, "event", "sessionStart", "inferenceConfiguration")
	assertAWSRealtimeJSONNumber(t, inference["topP"], 1.0)
	assertAWSRealtimeJSONNumber(t, inference["temperature"], 1.0)
}

func TestAWSRealtimeSessionStartsWithReferenceToolChoice(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("",
		WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}),
		WithAWSRealtimeToolChoice("required"),
	)
	session := newAWSRealtimeSession(provider, &fakeAWSRealtimeClient{stream: stream})

	if err := session.UpdateTools([]llm.Tool{awsRequestTestTool{}}); err != nil {
		t.Fatalf("UpdateTools error = %v", err)
	}
	if err := session.start(context.Background()); err != nil {
		t.Fatalf("start error = %v", err)
	}
	defer session.Close()

	sent := stream.snapshotSent()
	toolConfig := nestedMap(t, mustAWSRealtimeJSONEvent(t, sent[1]), "event", "promptStart", "toolConfiguration")
	toolChoice := nestedMap(t, map[string]any{"choice": toolConfig["toolChoice"]}, "choice")
	if _, ok := toolChoice["any"].(map[string]any); !ok {
		t.Fatalf("toolChoice = %#v, want reference required/any choice", toolConfig["toolChoice"])
	}
}

func TestAWSRealtimeSessionAppliesReferenceInferenceOptions(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("",
		WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}),
		WithAWSRealtimeMaxTokens(4096),
		WithAWSRealtimeTopP(0.25),
		WithAWSRealtimeTemperature(0.5),
	)
	session := newAWSRealtimeSession(provider, &fakeAWSRealtimeClient{stream: stream})

	if err := session.UpdateTools([]llm.Tool{awsRequestTestTool{}}); err != nil {
		t.Fatalf("UpdateTools error = %v", err)
	}
	if err := session.start(context.Background()); err != nil {
		t.Fatalf("start error = %v", err)
	}
	defer session.Close()

	sent := stream.snapshotSent()
	sessionStart := mustAWSRealtimeJSONEvent(t, sent[0])
	inference := nestedMap(t, sessionStart, "event", "sessionStart", "inferenceConfiguration")
	assertAWSRealtimeJSONNumber(t, inference["maxTokens"], 4096)
	assertAWSRealtimeJSONNumber(t, inference["topP"], 0.25)
	assertAWSRealtimeJSONNumber(t, inference["temperature"], 0.5)
}

func TestAWSRealtimeSessionUpdateToolsRecyclesActiveStream(t *testing.T) {
	first := newFakeAWSRealtimeStream()
	second := newFakeAWSRealtimeStream()
	client := &fakeAWSRealtimeClient{streams: []awsRealtimeStream{first, second}}
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(client))
	session := newAWSRealtimeSession(provider, client)

	if err := session.UpdateTools([]llm.Tool{awsRequestTestTool{}}); err != nil {
		t.Fatalf("initial UpdateTools error = %v", err)
	}
	if err := session.start(context.Background()); err != nil {
		t.Fatalf("start error = %v", err)
	}
	defer session.Close()

	if err := session.UpdateTools([]llm.Tool{awsSecondRequestTestTool{}}); err != nil {
		t.Fatalf("UpdateTools active error = %v", err)
	}
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeSessionReconnected)

	firstSent := first.snapshotSent()
	closeEvents := firstSent[len(firstSent)-3:]
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, closeEvents[0]), "event", "contentEnd", "contentName"); got == "" {
		t.Fatalf("recycle contentEnd contentName empty")
	}
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, closeEvents[1]), "event", "promptEnd", "promptName"); got == "" {
		t.Fatalf("recycle promptEnd promptName empty")
	}
	if _, ok := nestedMap(t, mustAWSRealtimeJSONEvent(t, closeEvents[2]), "event")["sessionEnd"].(map[string]any); !ok {
		t.Fatalf("recycle sessionEnd event = %s", closeEvents[2])
	}
	if !first.isClosed() {
		t.Fatal("first stream closed = false, want true after active tool update recycle")
	}
	secondSent := second.snapshotSent()
	if len(secondSent) == 0 {
		t.Fatal("second stream sent no events, want restarted prompt with new tools")
	}
	toolConfig := nestedMap(t, mustAWSRealtimeJSONEvent(t, secondSent[1]), "event", "promptStart", "toolConfiguration")
	tools, ok := toolConfig["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("recycled tools = %#v, want one tool", toolConfig["tools"])
	}
	spec := nestedMap(t, map[string]any{"tool": tools[0]}, "tool", "toolSpec")
	if spec["name"] != "lookup_order" {
		t.Fatalf("recycled tool name = %#v, want lookup_order", spec["name"])
	}
}

func TestAWSRealtimeSessionUpdateToolsDefersReferenceActiveRecycle(t *testing.T) {
	first := newFakeAWSRealtimeStream()
	second := newFakeAWSRealtimeStream()
	client := &fakeAWSRealtimeClient{streams: []awsRealtimeStream{first, second}}
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(client))
	provider.toolRecycleDelay = 30 * time.Millisecond
	session := newAWSRealtimeSession(provider, client)

	if err := session.UpdateTools([]llm.Tool{awsRequestTestTool{}}); err != nil {
		t.Fatalf("initial UpdateTools error = %v", err)
	}
	if err := session.start(context.Background()); err != nil {
		t.Fatalf("start error = %v", err)
	}
	defer session.Close()

	if err := session.UpdateTools([]llm.Tool{awsSecondRequestTestTool{}}); err != nil {
		t.Fatalf("UpdateTools active error = %v", err)
	}
	firstClosed := first.isClosed()
	if firstClosed {
		t.Fatal("first stream closed synchronously, want reference deferred recycle")
	}
	secondSent := second.snapshotSent()
	if len(secondSent) != 0 {
		t.Fatalf("second stream sent %d events synchronously, want deferred recycle", len(secondSent))
	}

	event := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeSessionReconnected)
	if event.Reconnect == nil {
		t.Fatal("Reconnect = nil, want deferred reference recycle notification")
	}
	firstClosed = first.isClosed()
	if !firstClosed {
		t.Fatal("first stream closed = false, want deferred active recycle")
	}
	secondSent = second.snapshotSent()
	if len(secondSent) == 0 {
		t.Fatal("second stream sent no events, want restarted prompt with new tools")
	}
}

func TestAWSRealtimeSessionUpdateToolsRecycleKeepsBufferedInputTail(t *testing.T) {
	first := newFakeAWSRealtimeStream()
	second := newFakeAWSRealtimeStream()
	client := &fakeAWSRealtimeClient{streams: []awsRealtimeStream{first, second}}
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(client))
	session := newAWSRealtimeSession(provider, client)

	if err := session.UpdateTools([]llm.Tool{awsRequestTestTool{}}); err != nil {
		t.Fatalf("initial UpdateTools error = %v", err)
	}
	if err := session.start(context.Background()); err != nil {
		t.Fatalf("start error = %v", err)
	}

	firstSent := first.snapshotSent()
	sentBeforeAudio := len(firstSent)
	if err := session.PushAudio(awsRealtimeTestMonoFrame(16000, make([]int16, 256))); err != nil {
		t.Fatalf("PushAudio first tail error = %v", err)
	}
	firstSent = first.snapshotSent()
	if got := countAWSRealtimeAudioInputs(t, firstSent[sentBeforeAudio:]); got != 0 {
		t.Fatalf("audioInput events before recycle = %d, want none for buffered tail", got)
	}

	if err := session.UpdateTools([]llm.Tool{awsSecondRequestTestTool{}}); err != nil {
		t.Fatalf("UpdateTools active error = %v", err)
	}
	firstSent = first.snapshotSent()
	if got := countAWSRealtimeAudioInputs(t, firstSent[sentBeforeAudio:]); got != 0 {
		t.Fatalf("old stream audioInput events during recycle = %d, want buffered tail kept", got)
	}
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeSessionReconnected)

	secondSent := second.snapshotSent()
	sentSecondBeforeAudio := len(secondSent)
	if err := session.PushAudio(awsRealtimeTestMonoFrame(16000, make([]int16, 256))); err != nil {
		t.Fatalf("PushAudio second tail error = %v", err)
	}
	audioInputs := waitAWSRealtimeAudioInputPayloads(t, second, sentSecondBeforeAudio, 1)
	if len(audioInputs) != 1 {
		t.Fatalf("new stream audioInput events after tail completion = %d, want one", len(audioInputs))
	}
	decoded, err := base64.StdEncoding.DecodeString(audioInputs[0])
	if err != nil {
		t.Fatalf("audioInput base64 decode error = %v", err)
	}
	if got, want := len(decoded), 512*2; got != want {
		t.Fatalf("audioInput bytes = %d, want carried tail chunk %d", got, want)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
}

func TestAWSRealtimeSessionStartsWithReferenceChatContext(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session := newAWSRealtimeSession(provider, &fakeAWSRealtimeClient{stream: stream})

	ctx := llm.NewChatContext()
	ctx.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleSystem, Text: "system instructions"})
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

	waitAWSRealtimeAudioContentStart(t, stream, 11)
	if len(stream.sent) != 12 {
		t.Fatalf("sent event count = %d, want 12 with system filtered and two history messages", len(stream.sent))
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
	audioStart := waitAWSRealtimeAudioContentStart(t, stream, 11)
	if got := awsRealtimeNestedString(audioStart, "event", "contentStart", "type"); got != "AUDIO" {
		t.Fatalf("event[11] type = %q, want AUDIO", got)
	}
}

func TestAWSRealtimeSessionSpacesReferenceHistoryEvents(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session := newAWSRealtimeSession(provider, &fakeAWSRealtimeClient{stream: stream})

	ctx := llm.NewChatContext()
	ctx.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleUser, Text: "hello"})
	ctx.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleAssistant, Text: "hi"})
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext before start error = %v", err)
	}

	if err := session.start(context.Background()); err != nil {
		t.Fatalf("start error = %v", err)
	}
	defer session.Close()

	waitAWSRealtimeAudioContentStart(t, stream, 11)
	if len(stream.sentAt) < 12 {
		t.Fatalf("sent timestamps = %d, want startup and audio timestamps", len(stream.sentAt))
	}
	for i := 5; i < 11; i++ {
		gap := stream.sentAt[i+1].Sub(stream.sentAt[i])
		if gap < 8*time.Millisecond {
			t.Fatalf("history event gap %d->%d = %v, want reference 10ms pacing", i, i+1, gap)
		}
	}
}

func TestAWSRealtimeSessionTruncatesReferenceChatContextOnStart(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session := newAWSRealtimeSession(provider, &fakeAWSRealtimeClient{stream: stream})

	ctx := llm.NewChatContext()
	for i := range defaultAWSRealtimeMaxMessages + 6 {
		role := llm.ChatRoleUser
		if i%2 == 1 {
			role = llm.ChatRoleAssistant
		}
		ctx.AddMessage(llm.ChatMessageArgs{Role: role, Text: fmt.Sprintf("msg-%02d", i)})
	}
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext before start error = %v", err)
	}

	if err := session.start(context.Background()); err != nil {
		t.Fatalf("start error = %v", err)
	}
	defer session.Close()

	sent := stream.snapshotSent()
	historyTexts := awsRealtimeSentTextInputContents(t, sent)
	if len(historyTexts) > 0 && strings.Contains(historyTexts[0], "Your name is Sonic") {
		historyTexts = historyTexts[1:]
	}
	if len(historyTexts) != defaultAWSRealtimeMaxMessages {
		t.Fatalf("history text count = %d, want reference max %d", len(historyTexts), defaultAWSRealtimeMaxMessages)
	}
	if historyTexts[0] != "msg-06" {
		t.Fatalf("first history text = %q, want msg-06 after reference truncation", historyTexts[0])
	}
	if historyTexts[len(historyTexts)-1] != "msg-45" {
		t.Fatalf("last history text = %q, want msg-45", historyTexts[len(historyTexts)-1])
	}
}

func TestAWSRealtimeSessionExcludesReferenceEmptyMessagesBeforeTruncation(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session := newAWSRealtimeSession(provider, &fakeAWSRealtimeClient{stream: stream})

	ctx := llm.NewChatContext()
	ctx.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleUser, Text: "keep this turn"})
	for i := 0; i < defaultAWSRealtimeMaxMessages+6; i++ {
		ctx.Append(&llm.ChatMessage{
			ID:      fmt.Sprintf("empty-%02d", i),
			Role:    llm.ChatRoleUser,
			Content: []llm.ChatContent{},
		})
	}
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext before start error = %v", err)
	}

	if err := session.start(context.Background()); err != nil {
		t.Fatalf("start error = %v", err)
	}
	defer session.Close()

	sent := stream.snapshotSent()
	texts := awsRealtimeSentTextInputContents(t, sent)
	for _, text := range texts {
		if text == "keep this turn" {
			return
		}
	}
	t.Fatalf("text inputs = %v, want non-empty reference turn preserved before truncation", texts)
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

	sent := stream.snapshotSent()
	sentCount := len(sent)
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext after start error = %v", err)
	}
	sent = stream.snapshotSent()
	if len(sent) != sentCount {
		t.Fatalf("seeded history replay sent %d new events, want none", len(sent)-sentCount)
	}
}

func TestAWSRealtimeSessionPushAudioAndCloseSendReferenceEvents(t *testing.T) {
	stream := &fakeAWSRealtimeStream{}
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}

	frame := awsRealtimeTestMonoFrame(16000, make([]int16, 512))
	sent := stream.snapshotSent()
	sentCount := len(sent)
	if err := session.PushAudio(frame); err != nil {
		t.Fatalf("PushAudio error = %v", err)
	}
	audioInputs := waitAWSRealtimeAudioInputPayloads(t, stream, sentCount, 1)
	if got := audioInputs[0]; got != base64.StdEncoding.EncodeToString(frame.Data) {
		t.Fatalf("audioInput content = %q, want base64 PCM", got)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	sent = stream.snapshotSent()
	closeEvents := sent[len(sent)-3:]
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, closeEvents[0]), "event", "contentEnd", "contentName"); got == "" {
		t.Fatalf("contentEnd contentName empty")
	}
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, closeEvents[1]), "event", "promptEnd", "promptName"); got == "" {
		t.Fatalf("promptEnd promptName empty")
	}
	if _, ok := nestedMap(t, mustAWSRealtimeJSONEvent(t, closeEvents[2]), "event")["sessionEnd"].(map[string]any); !ok {
		t.Fatalf("sessionEnd event = %s", closeEvents[2])
	}
	if !stream.isClosed() {
		t.Fatal("stream closed = false, want true")
	}
}

func TestAWSRealtimeSessionPushAudioSendErrorDoesNotBlockReferenceInput(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
	stream.setSendErr(errors.New("bedrock send failed"))

	err = session.PushAudio(awsRealtimeTestMonoFrame(16000, make([]int16, 512)))

	if err != nil {
		t.Fatalf("PushAudio error = %v, want nil because reference queues mic audio before provider send", err)
	}
}

func TestAWSRealtimeSessionPushAudioDoesNotBlockOnReferenceProviderSend(t *testing.T) {
	stream := &blockingAudioInputAWSRealtimeStream{
		fakeAWSRealtimeStream: newFakeAWSRealtimeStream(),
		started:               make(chan struct{}),
		release:               make(chan struct{}),
	}
	t.Cleanup(func() { close(stream.release) })
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- session.PushAudio(awsRealtimeTestMonoFrame(16000, make([]int16, 512)))
	}()

	select {
	case <-stream.started:
	case <-time.After(time.Second):
		t.Fatal("PushAudio did not start provider audio send")
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("PushAudio error = %v, want nil while provider send continues asynchronously", err)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("PushAudio blocked on provider audio send")
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

func TestAWSRealtimeSessionUpdateChatContextIgnoresReferenceBlankUserText(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
	waitAWSRealtimeAudioContentStart(t, stream, 0)
	sentCount := len(stream.sent)

	ctx := llm.NewChatContext()
	ctx.Append(&llm.ChatMessage{
		ID:      "blank-user",
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: " \n\t "}},
	})
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext blank user error = %v", err)
	}
	if len(stream.sent) != sentCount {
		t.Fatalf("UpdateChatContext blank user sent %d events, want none", len(stream.sent)-sentCount)
	}
}

func TestAWSRealtimeSessionStripsReferenceLeadingAssistantOnInitialChatContext(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	client := &fakeAWSRealtimeClient{stream: stream}
	session := newAWSRealtimeSession(NewAWSRealtimeModel(""), client)
	ctx := llm.NewChatContext()
	ctx.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleAssistant, Text: "orphan greeting"})
	ctx.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleUser, Text: "continue"})

	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext before start error = %v", err)
	}

	if session.chatCtx == nil || len(session.chatCtx.Items) != 1 {
		t.Fatalf("stored chatCtx = %#v, want leading assistant stripped", session.chatCtx)
	}
	msg, ok := session.chatCtx.Items[0].(*llm.ChatMessage)
	if !ok || msg.Role != llm.ChatRoleUser || msg.TextContent() != "continue" {
		t.Fatalf("stored first item = %#v, want user continue", session.chatCtx.Items[0])
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

	samples := make([][2]int16, 1536)
	for i := range samples {
		samples[i] = [2]int16{10, 30}
	}
	frame := awsRealtimeTestStereoFrame(48000, samples)
	sent := stream.snapshotSent()
	sentCount := len(sent)
	if err := session.PushAudio(frame); err != nil {
		t.Fatalf("PushAudio error = %v", err)
	}

	audioInputs := waitAWSRealtimeAudioInputPayloads(t, stream, sentCount, 1)
	content := audioInputs[0]
	decoded, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		t.Fatalf("audioInput base64 decode error = %v", err)
	}
	if len(decoded) != 512*2 {
		t.Fatalf("normalized audio bytes = %d, want one 512-sample 16-bit mono chunk", len(decoded))
	}
	if got := int16(binary.LittleEndian.Uint16(decoded)); got != 20 {
		t.Fatalf("normalized sample = %d, want first downmixed 16k mono sample 20", got)
	}
}

func TestAWSRealtimeSessionPushAudioChunksReferenceInput(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	sent := stream.snapshotSent()
	sentCount := len(sent)
	if err := session.PushAudio(awsRealtimeTestMonoFrame(16000, make([]int16, 256))); err != nil {
		t.Fatalf("PushAudio first error = %v", err)
	}
	sent = stream.snapshotSent()
	if got := countAWSRealtimeAudioInputs(t, sent[sentCount:]); got != 0 {
		t.Fatalf("audioInput events after partial frame = %d, want 0", got)
	}

	if err := session.PushAudio(awsRealtimeTestMonoFrame(16000, make([]int16, 256))); err != nil {
		t.Fatalf("PushAudio second error = %v", err)
	}
	audioInputs := waitAWSRealtimeAudioInputPayloads(t, stream, sentCount, 1)
	if len(audioInputs) != 1 {
		t.Fatalf("audioInput events = %d, want one 512-sample chunk", len(audioInputs))
	}
	decoded, err := base64.StdEncoding.DecodeString(audioInputs[0])
	if err != nil {
		t.Fatalf("audioInput base64 decode error = %v", err)
	}
	if got, want := len(decoded), 512*2; got != want {
		t.Fatalf("audioInput bytes = %d, want %d", got, want)
	}
}

func TestAWSRealtimeSessionCloseDropsReferenceInputAudioTail(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}

	sent := stream.snapshotSent()
	sentCount := len(sent)
	if err := session.PushAudio(awsRealtimeTestMonoFrame(16000, make([]int16, 256))); err != nil {
		t.Fatalf("PushAudio error = %v", err)
	}
	sent = stream.snapshotSent()
	if got := countAWSRealtimeAudioInputs(t, sent[sentCount:]); got != 0 {
		t.Fatalf("audioInput events before Close = %d, want none until chunk flush", got)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	sent = stream.snapshotSent()
	if got := countAWSRealtimeAudioInputs(t, sent[sentCount:]); got != 0 {
		t.Fatalf("audioInput events after Close = %d, want buffered tail dropped", got)
	}
	audioIndex := -1
	contentEndIndex := -1
	for i, raw := range sent[sentCount:] {
		event := mustAWSRealtimeJSONEvent(t, raw)
		if awsRealtimeNestedString(event, "event", "audioInput", "content") != "" {
			audioIndex = i
		}
		if awsRealtimeNestedString(event, "event", "contentEnd", "contentName") != "" {
			contentEndIndex = i
			break
		}
	}
	if audioIndex >= 0 || contentEndIndex < 0 {
		t.Fatalf("audioInput/contentEnd order = %d/%d, want no tail before contentEnd", audioIndex, contentEndIndex)
	}
}

func TestAWSRealtimeSessionCloseKeepsCompletedInputChunks(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}

	sent := stream.snapshotSent()
	sentCount := len(sent)
	if err := session.PushAudio(awsRealtimeTestMonoFrame(16000, make([]int16, 512))); err != nil {
		t.Fatalf("PushAudio complete chunk error = %v", err)
	}
	if err := session.PushAudio(awsRealtimeTestMonoFrame(16000, make([]int16, 256))); err != nil {
		t.Fatalf("PushAudio tail error = %v", err)
	}
	audioInputs := waitAWSRealtimeAudioInputPayloads(t, stream, sentCount, 1)
	if len(audioInputs) != 1 {
		t.Fatalf("audioInput events before Close = %d, want one completed chunk", len(audioInputs))
	}
	decoded, err := base64.StdEncoding.DecodeString(audioInputs[0])
	if err != nil {
		t.Fatalf("audioInput base64 decode error = %v", err)
	}
	if got, want := len(decoded), 512*2; got != want {
		t.Fatalf("audioInput bytes = %d, want completed chunk %d", got, want)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	sent = stream.snapshotSent()
	audioInputs = collectAWSRealtimeAudioInputPayloads(t, sent[sentCount:])
	if len(audioInputs) != 1 {
		t.Fatalf("audioInput events after Close = %d, want no extra tail chunk", len(audioInputs))
	}
}

func TestAWSRealtimeSessionClearAudioIsReferenceNoop(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}

	sent := stream.snapshotSent()
	sentCount := len(sent)
	if err := session.PushAudio(awsRealtimeTestMonoFrame(16000, make([]int16, 256))); err != nil {
		t.Fatalf("PushAudio error = %v", err)
	}
	if err := session.ClearAudio(); err != nil {
		t.Fatalf("ClearAudio error = %v", err)
	}
	sent = stream.snapshotSent()
	if got := countAWSRealtimeAudioInputs(t, sent[sentCount:]); got != 0 {
		t.Fatalf("audioInput events after ClearAudio = %d, want no-op", got)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	audioInputs := collectAWSRealtimeAudioInputPayloads(t, stream.sent[sentCount:])
	if len(audioInputs) != 0 {
		t.Fatalf("audioInput events after Close = %d, want buffered tail dropped", len(audioInputs))
	}
}

func TestAWSRealtimeSessionCommitAudioIsReferenceNoop(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}

	sent := stream.snapshotSent()
	sentCount := len(sent)
	if err := session.PushAudio(awsRealtimeTestMonoFrame(16000, make([]int16, 256))); err != nil {
		t.Fatalf("PushAudio error = %v", err)
	}
	if err := session.CommitAudio(); err != nil {
		t.Fatalf("CommitAudio error = %v", err)
	}

	sent = stream.snapshotSent()
	audioInputs := collectAWSRealtimeAudioInputPayloads(t, sent[sentCount:])
	if len(audioInputs) != 0 {
		t.Fatalf("audioInput events after CommitAudio = %d, want no-op", len(audioInputs))
	}
	if err := session.CommitAudio(); err != nil {
		t.Fatalf("second CommitAudio error = %v", err)
	}
	sent = stream.snapshotSent()
	if got := countAWSRealtimeAudioInputs(t, sent[sentCount:]); got != 0 {
		t.Fatalf("audioInput events after second CommitAudio = %d, want no-op", got)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	audioInputs = collectAWSRealtimeAudioInputPayloads(t, stream.sent[sentCount:])
	if len(audioInputs) != 0 {
		t.Fatalf("audioInput events after Close = %d, want buffered tail dropped", len(audioInputs))
	}
}

func TestAWSRealtimeSessionUpdateOptionsIsReferenceNoop(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
	sent := stream.snapshotSent()
	before := len(sent)

	err = session.UpdateOptions(llm.RealtimeSessionOptions{
		ToolChoice:       "required",
		ToolChoiceSet:    true,
		Voice:            "matthew",
		VoiceSet:         true,
		TurnDetection:    map[string]any{"type": "server_vad"},
		TurnDetectionSet: true,
	})

	if err != nil {
		t.Fatalf("UpdateOptions error = %v", err)
	}
	sent = stream.snapshotSent()
	if after := len(sent); after != before {
		t.Fatalf("sent events after UpdateOptions = %d, want unchanged %d", after, before)
	}
	if stream.isClosed() {
		t.Fatal("stream closed after UpdateOptions, want reference no-op")
	}
}

func TestAWSRealtimeSessionPushAudioPreservesResampleDurationAcrossFrames(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	sent := stream.snapshotSent()
	sentCount := len(sent)
	for range 180 {
		if err := session.PushAudio(awsRealtimeTestMonoFrame(44100, make([]int16, 100))); err != nil {
			t.Fatalf("PushAudio error = %v", err)
		}
	}
	audioInputs := waitAWSRealtimeAudioInputPayloads(t, stream, sentCount, 12)
	if len(audioInputs) != 12 {
		t.Fatalf("audioInput chunks = %d, want 12 with cumulative 44.1kHz->16kHz resampling", len(audioInputs))
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

func TestAWSRealtimeSessionIgnoresReferenceMalformedResponseJSON(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	stream.emitJSON(`{"event":`)
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

func TestAWSRealtimeSessionInvalidReferenceAudioOutputIsModelError(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
	waitAWSRealtimeAudioContentStart(t, stream, 0)

	stream.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	stream.emitJSON(`{"event":{"contentStart":{"type":"AUDIO","role":"ASSISTANT","contentId":"audio-1"}}}`)
	stream.emitJSON(`{"event":{"audioOutput":{"contentId":"audio-1","content":"abc"}}}`)

	event := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeError)
	modelErr, ok := event.Error.(*llm.RealtimeModelError)
	if !ok {
		t.Fatalf("Error = %T %v, want RealtimeModelError", event.Error, event.Error)
	}
	if modelErr.Recoverable {
		t.Fatal("Recoverable = true, want reference nonrecoverable audio decode error")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(modelErr.Err, &statusErr) {
		t.Fatalf("RealtimeModelError.Err = %T %v, want APIStatusError", modelErr.Err, modelErr.Err)
	}
	if statusErr.StatusCode != 500 {
		t.Fatalf("StatusCode = %d, want 500", statusErr.StatusCode)
	}
}

func TestAWSRealtimeSessionEmitsErrorOnReferenceReadFailure(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	stream.err = errors.New("bedrock output stream failed")
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	close(stream.events)

	event := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeError)
	if event.Error == nil {
		t.Fatal("Error = nil, want stream failure")
	}
	modelErr, ok := event.Error.(*llm.RealtimeModelError)
	if !ok {
		t.Fatalf("Error = %T %v, want RealtimeModelError", event.Error, event.Error)
	}
	if modelErr.Recoverable {
		t.Fatal("Recoverable = true, want reference nonrecoverable stream failure")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(modelErr.Err, &statusErr) {
		t.Fatalf("RealtimeModelError.Err = %T %v, want APIStatusError", modelErr.Err, modelErr.Err)
	}
	if statusErr.StatusCode != 500 {
		t.Fatalf("StatusCode = %d, want 500", statusErr.StatusCode)
	}
	if !strings.Contains(statusErr.Error(), "bedrock output stream failed") {
		t.Fatalf("Error = %q, want provider stream failure", statusErr.Error())
	}
}

func TestAWSRealtimeSessionClosedFileReadErrorIsReferenceGraceful(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	stream.err = errors.New("I/O operation on closed file.")
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	close(stream.events)

	assertNoAWSRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeError)
}

func TestAWSRealtimeSessionReadDeadlineEmitsAPITimeoutError(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	stream.err = context.DeadlineExceeded
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	close(stream.events)

	event := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeError)
	if event.Error == nil {
		t.Fatal("Error = nil, want stream timeout")
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(event.Error, &timeoutErr) {
		t.Fatalf("Error = %T %v, want APITimeoutError", event.Error, event.Error)
	}
}

func TestAWSRealtimeRecoverableValidationErrorMatchesReferenceMessages(t *testing.T) {
	tests := []string{
		"ValidationException: InternalErrorCode=531::RST_STREAM closed stream. HTTP/2 error code: NO_ERROR",
		"ValidationException: System instability detected. Please retry your request.",
	}

	for _, message := range tests {
		if !isAWSRealtimeRecoverableReadError(errors.New(message)) {
			t.Fatalf("isAWSRealtimeRecoverableReadError(%q) = false, want reference recoverable validation error", message)
		}
	}
}

func TestAWSRealtimeUnknownValidationErrorIsReferenceNonRecoverable(t *testing.T) {
	err := errors.New("ValidationException: The provided request is invalid.")

	if isAWSRealtimeRecoverableReadError(err) {
		t.Fatal("isAWSRealtimeRecoverableReadError = true, want reference nonrecoverable validation error")
	}
}

func TestAWSRealtimeSessionRestartsAfterReferenceRecoverableReadFailure(t *testing.T) {
	first := newFakeAWSRealtimeStream()
	second := newFakeAWSRealtimeStream()
	first.err = errors.New("ValidationException: System instability detected. Please retry your request.")
	client := &fakeAWSRealtimeClient{streams: []awsRealtimeStream{first, second}}
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(client))
	awsSession := newAWSRealtimeSession(provider, client)
	ctx := llm.NewChatContext()
	ctx.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleAssistant, Text: "assistant opener"})
	ctx.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleUser, Text: "please continue"})
	if err := awsSession.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext before start error = %v", err)
	}
	if err := awsSession.start(context.Background()); err != nil {
		t.Fatalf("start error = %v", err)
	}
	defer awsSession.Close()

	close(first.events)

	event := assertAWSRealtimeEvent(t, awsSession.EventCh(), llm.RealtimeEventTypeSessionReconnected)
	if event.Reconnect == nil {
		t.Fatal("Reconnect = nil, want reference restart notification")
	}
	if !first.closed {
		t.Fatal("first stream closed = false, want stale recoverable stream closed")
	}
	if len(second.sent) == 0 {
		t.Fatal("second stream sent no startup events, want restarted Nova Sonic session")
	}
	texts := awsRealtimeSentTextInputContents(t, second.sent)
	if len(texts) < 2 {
		t.Fatalf("restart text inputs = %v, want system and interactive user", texts)
	}
	for _, text := range texts {
		if text == "[Resuming conversation]" || text == "assistant opener" {
			t.Fatalf("restart text inputs = %v, want orphan assistant stripped like reference", texts)
		}
	}
	if got := texts[len(texts)-1]; got != "please continue" {
		t.Fatalf("restart interactive text = %q, want last user turn", got)
	}
	lastStart := mustAWSRealtimeJSONEvent(t, second.sent[len(second.sent)-3])
	if got := nestedMap(t, lastStart, "event", "contentStart")["interactive"]; got != true {
		t.Fatalf("restart last user interactive = %v, want true", got)
	}
	assertNoAWSRealtimeEventType(t, awsSession.EventCh(), llm.RealtimeEventTypeError)
}

func TestAWSRealtimeSessionDelaysRestartInteractiveTextAfterAudioStart(t *testing.T) {
	first := newFakeAWSRealtimeStream()
	second := newFakeAWSRealtimeStream()
	first.err = errors.New("ValidationException: System instability detected. Please retry your request.")
	client := &fakeAWSRealtimeClient{streams: []awsRealtimeStream{first, second}}
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(client))
	awsSession := newAWSRealtimeSession(provider, client)
	ctx := llm.NewChatContext()
	ctx.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleUser, Text: "continue now"})
	if err := awsSession.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext before start error = %v", err)
	}
	if err := awsSession.start(context.Background()); err != nil {
		t.Fatalf("start error = %v", err)
	}
	defer awsSession.Close()

	close(first.events)
	assertAWSRealtimeEvent(t, awsSession.EventCh(), llm.RealtimeEventTypeSessionReconnected)

	audioAt, textAt := awsRealtimeAudioAndInteractiveTextStartTimes(t, second)
	if gap := textAt.Sub(audioAt); gap < 40*time.Millisecond {
		t.Fatalf("restart interactive text gap = %v, want reference delay after audio start", gap)
	}
}

func TestAWSRealtimeSessionRestartsAfterReferenceModelTimeoutReadFailure(t *testing.T) {
	first := newFakeAWSRealtimeStream()
	second := newFakeAWSRealtimeStream()
	first.err = errors.New("ModelTimeoutException: model stream timed out")
	client := &fakeAWSRealtimeClient{streams: []awsRealtimeStream{first, second}}
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(client))
	awsSession := newAWSRealtimeSession(provider, client)
	if err := awsSession.start(context.Background()); err != nil {
		t.Fatalf("start error = %v", err)
	}
	defer awsSession.Close()

	close(first.events)

	event := assertAWSRealtimeEvent(t, awsSession.EventCh(), llm.RealtimeEventTypeSessionReconnected)
	if event.Reconnect == nil {
		t.Fatal("Reconnect = nil, want reference restart for model timeout")
	}
	if !first.isClosed() {
		t.Fatal("first stream closed = false, want stale timeout stream closed")
	}
	secondSent := second.snapshotSent()
	if len(secondSent) == 0 {
		t.Fatal("second stream sent no startup events, want restarted Nova Sonic session")
	}
	assertNoAWSRealtimeEventType(t, awsSession.EventCh(), llm.RealtimeEventTypeError)
}

func TestAWSRealtimeSessionRecyclesIdleStreamAfterReferenceDuration(t *testing.T) {
	first := newFakeAWSRealtimeStream()
	second := newFakeAWSRealtimeStream()
	client := &fakeAWSRealtimeClient{streams: []awsRealtimeStream{first, second}}
	provider := NewAWSRealtimeModel("",
		WithAWSRealtimeClient(client),
		WithAWSRealtimeMaxSessionDuration(10*time.Millisecond),
	)
	awsSession := newAWSRealtimeSession(provider, client)
	if err := awsSession.start(context.Background()); err != nil {
		t.Fatalf("start error = %v", err)
	}
	defer awsSession.Close()

	event := assertAWSRealtimeEvent(t, awsSession.EventCh(), llm.RealtimeEventTypeSessionReconnected)
	if event.Reconnect == nil {
		t.Fatal("Reconnect = nil, want reference session recycle notification")
	}
	if !first.isClosed() {
		t.Fatal("first stream closed = false, want stale duration-limited stream closed")
	}
	secondSent := second.snapshotSent()
	if len(secondSent) == 0 {
		t.Fatal("second stream sent no startup events, want recycled Nova Sonic session")
	}
}

func TestAWSRealtimeSessionRecycleWaitsForReferenceTurnBoundary(t *testing.T) {
	first := newFakeAWSRealtimeStream()
	second := newFakeAWSRealtimeStream()
	client := &fakeAWSRealtimeClient{streams: []awsRealtimeStream{first, second}}
	provider := NewAWSRealtimeModel("",
		WithAWSRealtimeClient(client),
		WithAWSRealtimeMaxSessionDuration(10*time.Millisecond),
	)
	awsSession := newAWSRealtimeSession(provider, client)
	if err := awsSession.start(context.Background()); err != nil {
		t.Fatalf("start error = %v", err)
	}
	defer awsSession.Close()

	first.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	first.emitJSON(`{"event":{"contentStart":{"contentId":"audio-1","type":"AUDIO"}}}`)
	assertAWSRealtimeEvent(t, awsSession.EventCh(), llm.RealtimeEventTypeGenerationCreated)

	select {
	case event := <-awsSession.EventCh():
		if event.Type == llm.RealtimeEventTypeSessionReconnected {
			t.Fatalf("got reconnect before AUDIO END_TURN: %#v", event)
		}
	case <-time.After(50 * time.Millisecond):
	}
	if first.isClosed() {
		t.Fatal("first stream closed before AUDIO END_TURN")
	}

	first.emitJSON(`{"event":{"contentEnd":{"contentId":"audio-1","type":"AUDIO","stopReason":"END_TURN"}}}`)
	event := assertAWSRealtimeEvent(t, awsSession.EventCh(), llm.RealtimeEventTypeSessionReconnected)
	if event.Reconnect == nil {
		t.Fatal("Reconnect = nil, want reference session recycle after turn boundary")
	}
	if !first.isClosed() {
		t.Fatal("first stream closed = false, want recycle after AUDIO END_TURN")
	}
	secondSent := second.snapshotSent()
	if len(secondSent) == 0 {
		t.Fatal("second stream sent no startup events, want recycled Nova Sonic session")
	}
}

func TestAWSRealtimeSessionRecycleWaitsForReferenceAudioEndTurnAfterCompletionEnd(t *testing.T) {
	first := newFakeAWSRealtimeStream()
	second := newFakeAWSRealtimeStream()
	client := &fakeAWSRealtimeClient{streams: []awsRealtimeStream{first, second}}
	provider := NewAWSRealtimeModel("",
		WithAWSRealtimeClient(client),
		WithAWSRealtimeMaxSessionDuration(10*time.Millisecond),
	)
	awsSession := newAWSRealtimeSession(provider, client)
	if err := awsSession.start(context.Background()); err != nil {
		t.Fatalf("start error = %v", err)
	}
	defer awsSession.Close()

	first.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	first.emitJSON(`{"event":{"contentStart":{"contentId":"audio-1","type":"AUDIO"}}}`)
	assertAWSRealtimeEvent(t, awsSession.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	first.emitJSON(`{"event":{"completionEnd":{"completionId":"completion-1"}}}`)

	select {
	case event := <-awsSession.EventCh():
		if event.Type == llm.RealtimeEventTypeSessionReconnected {
			t.Fatalf("got reconnect before AUDIO END_TURN after completionEnd: %#v", event)
		}
	case <-time.After(50 * time.Millisecond):
	}
	firstClosed := first.isClosed()
	if firstClosed {
		t.Fatal("first stream closed before AUDIO END_TURN after completionEnd")
	}

	first.emitJSON(`{"event":{"contentEnd":{"contentId":"audio-1","type":"AUDIO","stopReason":"END_TURN"}}}`)
	event := assertAWSRealtimeEvent(t, awsSession.EventCh(), llm.RealtimeEventTypeSessionReconnected)
	if event.Reconnect == nil {
		t.Fatal("Reconnect = nil, want reference session recycle after AUDIO END_TURN")
	}
	firstClosed = first.isClosed()
	if !firstClosed {
		t.Fatal("first stream closed = false, want recycle after AUDIO END_TURN")
	}
	secondSent := second.snapshotSent()
	if len(secondSent) == 0 {
		t.Fatal("second stream sent no startup events, want recycled Nova Sonic session")
	}
}

func TestAWSRealtimeSessionRecycleWaitsForReferenceAudioQuiet(t *testing.T) {
	first := newFakeAWSRealtimeStream()
	second := newFakeAWSRealtimeStream()
	client := &fakeAWSRealtimeClient{streams: []awsRealtimeStream{first, second}}
	provider := NewAWSRealtimeModel("",
		WithAWSRealtimeClient(client),
		WithAWSRealtimeMaxSessionDuration(10*time.Millisecond),
	)
	provider.recycleQuietPeriod = 30 * time.Millisecond
	awsSession := newAWSRealtimeSession(provider, client)
	if err := awsSession.start(context.Background()); err != nil {
		t.Fatalf("start error = %v", err)
	}
	defer awsSession.Close()

	audioBytes := []byte{1, 0, 2, 0}
	first.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	first.emitJSON(`{"event":{"contentStart":{"contentId":"audio-1","type":"AUDIO"}}}`)
	assertAWSRealtimeEvent(t, awsSession.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	first.emitJSON(`{"event":{"audioOutput":{"contentId":"audio-1","content":"` + base64.StdEncoding.EncodeToString(audioBytes) + `"}}}`)
	first.emitJSON(`{"event":{"contentEnd":{"contentId":"audio-1","type":"AUDIO","stopReason":"END_TURN"}}}`)

	deadline := time.After(20 * time.Millisecond)
	for {
		select {
		case event := <-awsSession.EventCh():
			if event.Type == llm.RealtimeEventTypeSessionReconnected {
				t.Fatalf("got reconnect before reference audio quiet period: %#v", event)
			}
		case <-deadline:
			goto waited
		}
	}

waited:
	firstClosed := first.isClosed()
	if firstClosed {
		t.Fatal("first stream closed before reference audio quiet period")
	}
	event := assertAWSRealtimeEvent(t, awsSession.EventCh(), llm.RealtimeEventTypeSessionReconnected)
	if event.Reconnect == nil {
		t.Fatal("Reconnect = nil, want reference session recycle after audio quiet")
	}
	firstClosed = first.isClosed()
	if !firstClosed {
		t.Fatal("first stream closed = false, want recycle after audio quiet")
	}
	secondSent := second.snapshotSent()
	if len(secondSent) == 0 {
		t.Fatal("second stream sent no startup events, want recycled Nova Sonic session")
	}
}

func TestAWSRealtimeSessionCapsReferenceRecoverableRestartsPerGeneration(t *testing.T) {
	streams := []*fakeAWSRealtimeStream{
		newFakeAWSRealtimeStream(),
		newFakeAWSRealtimeStream(),
		newFakeAWSRealtimeStream(),
		newFakeAWSRealtimeStream(),
		newFakeAWSRealtimeStream(),
	}
	for _, stream := range streams[:4] {
		stream.err = errors.New("ValidationException: System instability detected. Please retry your request.")
	}
	clientStreams := make([]awsRealtimeStream, 0, len(streams))
	for _, stream := range streams {
		clientStreams = append(clientStreams, stream)
	}
	client := &fakeAWSRealtimeClient{streams: clientStreams}
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(client))
	awsSession := newAWSRealtimeSession(provider, client)
	if err := awsSession.start(context.Background()); err != nil {
		t.Fatalf("start error = %v", err)
	}
	defer awsSession.Close()

	streams[0].emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	assertAWSRealtimeEvent(t, awsSession.EventCh(), llm.RealtimeEventTypeGenerationCreated)

	for i := 0; i < 3; i++ {
		close(streams[i].events)
		assertAWSRealtimeEvent(t, awsSession.EventCh(), llm.RealtimeEventTypeSessionReconnected)
	}

	close(streams[3].events)
	event := assertAWSRealtimeEvent(t, awsSession.EventCh(), llm.RealtimeEventTypeError)
	if event.Error == nil {
		t.Fatal("Error = nil, want max restart attempts error")
	}
	if !strings.Contains(event.Error.Error(), "Max restart attempts exceeded") {
		t.Fatalf("Error = %q, want max restart attempts exceeded", event.Error.Error())
	}
	if len(streams[4].sent) != 0 {
		t.Fatalf("fifth stream sent %d events, want no restart after max attempts", len(streams[4].sent))
	}
}

func TestAWSRealtimeSessionReadFailureClosesReferenceGeneration(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	stream.err = errors.New("bedrock output stream failed")
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

	close(stream.events)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeError)

	select {
	case _, ok := <-msg.TextCh:
		if ok {
			t.Fatal("TextCh still open, want closed on read failure")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for TextCh close")
	}
	select {
	case _, ok := <-msg.AudioCh:
		if ok {
			t.Fatal("AudioCh still open, want closed on read failure")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for AudioCh close")
	}
	select {
	case _, ok := <-created.Generation.FunctionCh:
		if ok {
			t.Fatal("FunctionCh still open, want closed on read failure")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for FunctionCh close")
	}
}

func TestAWSRealtimeSessionToolResponseParsingErrorIsReferenceRecoverable(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	stream.err = errors.New("ValidationException: Tool Response parsing error: malformed tool result")
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
	awsSession := session.(*awsRealtimeSession)

	stream.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	<-created.Generation.MessageCh
	stream.emitJSON(`{"event":{"contentStart":{"type":"TOOL","role":"TOOL","contentId":"tool-content-1"}}}`)
	stream.emitJSON(`{"event":{"toolUse":{"toolUseId":"tool-parse","toolName":"lookup","content":"{}"}}}`)
	select {
	case <-created.Generation.FunctionCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for function stream call")
	}

	close(stream.events)
	var event llm.RealtimeEvent
	for {
		select {
		case event = <-session.EventCh():
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for tool parsing error")
		}
		if event.Type == llm.RealtimeEventTypeError {
			break
		}
	}
	modelErr, ok := event.Error.(*llm.RealtimeModelError)
	if !ok {
		t.Fatalf("Error = %T %v, want RealtimeModelError", event.Error, event.Error)
	}
	if !modelErr.Recoverable {
		t.Fatal("Recoverable = false, want reference recoverable tool parsing error")
	}

	awsSession.mu.Lock()
	pendingCount := len(awsSession.pending)
	awsSession.mu.Unlock()
	if pendingCount != 0 {
		t.Fatalf("pending tools = %d, want cleared after tool parsing error", pendingCount)
	}
	assertNoAWSRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeSessionReconnected)
}

func TestAWSRealtimeSessionValidationExceptionIsReferenceNonRecoverable(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	stream.err = errors.New("ValidationException: The toolResult field must be a valid JSON object")
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	close(stream.events)

	event := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeError)
	modelErr, ok := event.Error.(*llm.RealtimeModelError)
	if !ok {
		t.Fatalf("Error = %T %v, want RealtimeModelError", event.Error, event.Error)
	}
	if modelErr.Recoverable {
		t.Fatal("Recoverable = true, want reference nonrecoverable validation error")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(modelErr.Err, &statusErr) {
		t.Fatalf("RealtimeModelError.Err = %T %v, want APIStatusError", modelErr.Err, modelErr.Err)
	}
	if statusErr.StatusCode != 400 {
		t.Fatalf("StatusCode = %d, want 400", statusErr.StatusCode)
	}
	if statusErr.APIError == nil || statusErr.APIError.Retryable {
		t.Fatalf("Retryable = %v, want false", statusErr.APIError != nil && statusErr.APIError.Retryable)
	}
	assertNoAWSRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeSessionReconnected)
}

func TestAWSRealtimeSessionReadEOFClosesReferenceGeneration(t *testing.T) {
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

	close(stream.events)

	select {
	case _, ok := <-msg.TextCh:
		if ok {
			t.Fatal("TextCh still open, want closed on provider EOF")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for TextCh close")
	}
	select {
	case _, ok := <-msg.AudioCh:
		if ok {
			t.Fatal("AudioCh still open, want closed on provider EOF")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for AudioCh close")
	}
	select {
	case _, ok := <-created.Generation.FunctionCh:
		if ok {
			t.Fatal("FunctionCh still open, want closed on provider EOF")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for FunctionCh close")
	}
	assertNoAWSRealtimeEvent(t, session.EventCh())
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
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeSpeechStopped)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)
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
	assertNoAWSRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeText)
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

func TestAWSRealtimeSessionStreamsReferenceEmptyAudioOutput(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	stream.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	created := assertAWSRealtimeEventEventually(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	var msg llm.MessageGeneration
	select {
	case msg = <-created.Generation.MessageCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message generation")
	}

	stream.emitJSON(`{"event":{"contentStart":{"type":"AUDIO","contentId":"audio-empty"}}}`)
	stream.emitJSON(`{"event":{"audioOutput":{"contentId":"audio-empty","content":""}}}`)

	audio := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeAudio)
	if len(audio.Data) != 0 {
		t.Fatalf("audio data = %v, want empty reference audio delta", audio.Data)
	}
	select {
	case frame := <-msg.AudioCh:
		if frame == nil {
			t.Fatal("audio frame = nil, want empty reference frame")
		}
		if len(frame.Data) != 0 || frame.SamplesPerChannel != 0 || frame.SampleRate != defaultAWSRealtimeOutputSampleRate || frame.NumChannels != defaultAWSRealtimeChannels {
			t.Fatalf("audio frame = %#v, want empty reference output frame", frame)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for empty generated audio")
	}
}

func TestAWSRealtimeSessionStreamsReferencePunctuationAudioOutput(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	stream.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	created := assertAWSRealtimeEventEventually(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	var msg llm.MessageGeneration
	select {
	case msg = <-created.Generation.MessageCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message generation")
	}

	stream.emitJSON(`{"event":{"contentStart":{"type":"AUDIO","contentId":"audio-punctuation"}}}`)
	stream.emitJSON(`{"event":{"audioOutput":{"contentId":"audio-punctuation","content":"!!!"}}}`)

	audio := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeAudio)
	if len(audio.Data) != 0 {
		t.Fatalf("audio data = %v, want empty reference audio delta", audio.Data)
	}
	select {
	case frame := <-msg.AudioCh:
		if frame == nil {
			t.Fatal("audio frame = nil, want empty reference frame")
		}
		if len(frame.Data) != 0 || frame.SamplesPerChannel != 0 || frame.SampleRate != defaultAWSRealtimeOutputSampleRate || frame.NumChannels != defaultAWSRealtimeChannels {
			t.Fatalf("audio frame = %#v, want empty reference output frame", frame)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for punctuation generated audio")
	}
}

func TestAWSRealtimeSessionStreamsReferencePaddingAudioOutput(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	stream.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	created := assertAWSRealtimeEventEventually(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	var msg llm.MessageGeneration
	select {
	case msg = <-created.Generation.MessageCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message generation")
	}

	stream.emitJSON(`{"event":{"contentStart":{"type":"AUDIO","contentId":"audio-padding"}}}`)
	stream.emitJSON(`{"event":{"audioOutput":{"contentId":"audio-padding","content":"==="}}}`)

	audio := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeAudio)
	if len(audio.Data) != 0 {
		t.Fatalf("audio data = %v, want empty reference audio delta", audio.Data)
	}
	select {
	case frame := <-msg.AudioCh:
		if frame == nil {
			t.Fatal("audio frame = nil, want empty reference frame")
		}
		if len(frame.Data) != 0 || frame.SamplesPerChannel != 0 || frame.SampleRate != defaultAWSRealtimeOutputSampleRate || frame.NumChannels != defaultAWSRealtimeChannels {
			t.Fatalf("audio frame = %#v, want empty reference output frame", frame)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for padding generated audio")
	}
}

func TestAWSRealtimeSessionPreservesReferenceQueuedGenerationAudio(t *testing.T) {
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
	stream.emitJSON(`{"event":{"contentStart":{"type":"AUDIO","contentId":"audio-queued"}}}`)

	const total = 20
	for i := range total {
		audioBytes := []byte{byte(i), byte(i + 1)}
		stream.emitJSON(`{"event":{"audioOutput":{"contentId":"audio-queued","content":"` + base64.StdEncoding.EncodeToString(audioBytes) + `"}}}`)
	}

	for i := range total {
		select {
		case frame := <-msg.AudioCh:
			want := []byte{byte(i), byte(i + 1)}
			if !bytes.Equal(frame.Data, want) {
				t.Fatalf("queued audio frame %d = %v, want %v", i, frame.Data, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for queued audio frame %d of %d", i+1, total)
		}
	}
}

func TestAWSRealtimeSessionPreservesReferenceQueuedEvents(t *testing.T) {
	provider := NewAWSRealtimeModel("")
	session := newAWSRealtimeSession(provider, nil)

	const total = 20
	for i := range total {
		session.emit(llm.RealtimeEvent{
			Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
			InputTranscription: &llm.InputTranscriptionCompleted{
				ItemID:     fmt.Sprintf("user-%d", i),
				Transcript: fmt.Sprintf("transcript-%d", i),
				IsFinal:    true,
			},
		})
	}

	for i := range total {
		select {
		case event := <-session.EventCh():
			if event.InputTranscription == nil || event.InputTranscription.Transcript != fmt.Sprintf("transcript-%d", i) {
				t.Fatalf("queued event %d = %#v, want transcript-%d", i, event, i)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for queued event %d of %d", i+1, total)
		}
	}
}

func TestAWSRealtimeSessionIgnoresReferenceAudioOutputMissingContent(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	stream.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	created := assertAWSRealtimeEventEventually(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	var msg llm.MessageGeneration
	select {
	case msg = <-created.Generation.MessageCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message generation")
	}

	stream.emitJSON(`{"event":{"contentStart":{"type":"AUDIO","contentId":"audio-missing"}}}`)
	stream.emitJSON(`{"event":{"audioOutput":{"contentId":"audio-missing"}}}`)

	assertNoAWSRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeAudio)
	select {
	case frame := <-msg.AudioCh:
		t.Fatalf("generated audio frame = %#v, want none for missing reference content", frame)
	case <-time.After(50 * time.Millisecond):
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
	if created.Generation.ResponseID == "completion-1" {
		t.Fatalf("response id = %q, want generated LiveKit id distinct from provider completion id", created.Generation.ResponseID)
	}
	if _, err := uuid.Parse(created.Generation.ResponseID); err != nil {
		t.Fatalf("response id = %q, want generated UUID: %v", created.Generation.ResponseID, err)
	}
	if created.Generation.MessageCh == nil || created.Generation.FunctionCh == nil {
		t.Fatalf("generation streams = %#v/%#v, want reference streams", created.Generation.MessageCh, created.Generation.FunctionCh)
	}
	select {
	case msg := <-created.Generation.MessageCh:
		if msg.MessageID != created.Generation.ResponseID || msg.TextCh == nil || msg.AudioCh == nil || msg.ModalitiesCh == nil {
			t.Fatalf("message generation = %#v, want generated response id and streams", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for completionStart message generation")
	}
}

func TestAWSRealtimeSessionReusesReferenceGenerationOnRepeatedCompletionStart(t *testing.T) {
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
		t.Fatalf("first generation = %#v, want response id", created.Generation)
	}

	stream.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	assertNoAWSRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
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

func TestAWSRealtimeSessionKeepsReferenceGenerationOpenOnEndTurnWithoutType(t *testing.T) {
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

	stream.emitJSON(`{"event":{"contentEnd":{"contentId":"audio-1","stopReason":"END_TURN"}}}`)

	assertNoAWSRealtimeEvent(t, session.EventCh())
	select {
	case _, ok := <-msg.TextCh:
		if !ok {
			t.Fatal("TextCh closed, want reference missing-type END_TURN to leave generation open")
		}
	default:
	}
	select {
	case _, ok := <-msg.AudioCh:
		if !ok {
			t.Fatal("AudioCh closed, want reference missing-type END_TURN to leave generation open")
		}
	default:
	}

	stream.emitJSON(`{"event":{"contentEnd":{"contentId":"audio-1","type":"AUDIO","stopReason":"END_TURN"}}}`)

	select {
	case _, ok := <-msg.TextCh:
		if ok {
			t.Fatal("TextCh still open after AUDIO END_TURN")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for TextCh close after AUDIO END_TURN")
	}
	select {
	case _, ok := <-msg.AudioCh:
		if ok {
			t.Fatal("AudioCh still open after AUDIO END_TURN")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for AudioCh close after AUDIO END_TURN")
	}
}

func TestAWSRealtimeSessionKeepsReferenceGenerationOpenOnTextEndTurn(t *testing.T) {
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

	stream.emitJSON(`{"event":{"contentEnd":{"contentId":"text-1","type":"TEXT","stopReason":"END_TURN"}}}`)

	assertNoAWSRealtimeEvent(t, session.EventCh())
	select {
	case _, ok := <-msg.TextCh:
		if !ok {
			t.Fatal("TextCh closed, want reference text END_TURN to leave generation open")
		}
	default:
	}
	select {
	case _, ok := <-msg.AudioCh:
		if !ok {
			t.Fatal("AudioCh closed, want reference text END_TURN to leave generation open")
		}
	default:
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

func TestAWSRealtimeSessionIgnoresReferenceBargeInWithoutGeneration(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
	awsSession, ok := session.(*awsRealtimeSession)
	if !ok {
		t.Fatalf("session type = %T, want *awsRealtimeSession", session)
	}

	awsSession.updateProviderTextHistory(llm.ChatRoleAssistant, "still speaking", "")

	stream.emitJSON(`{"event":{"textOutput":{"role":"ASSISTANT","content":` + strconv.Quote(awsRealtimeBargeInContent) + `,"contentId":"barge-late"}}}`)

	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeSpeechStarted)
	if awsSession.chatCtx == nil || len(awsSession.chatCtx.Items) != 1 {
		t.Fatalf("chatCtx items = %#v, want one assistant message", awsSession.chatCtx)
	}
	msg, ok := awsSession.chatCtx.Items[0].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("chat item = %#v, want assistant message", awsSession.chatCtx.Items[0])
	}
	if msg.Interrupted {
		t.Fatal("assistant message interrupted = true, want unchanged without active generation")
	}
}

func TestAWSRealtimeSessionClosesReferenceGenerationBeforeBargeInSpeechStart(t *testing.T) {
	session := newAWSRealtimeSession(NewAWSRealtimeModel(""), nil)
	generation, _ := session.ensureGenerationWithCreated("response-1")
	checked := false
	session.turns = newAWSRealtimeTurnTracker(func(event llm.RealtimeEvent) {
		if event.Type != llm.RealtimeEventTypeSpeechStarted {
			return
		}
		checked = true
		select {
		case _, ok := <-generation.textCh:
			if ok {
				t.Fatal("generation TextCh still open when barge-in speech_started emitted")
			}
		default:
			t.Fatal("generation TextCh still open when barge-in speech_started emitted")
		}
	}, session.emitGenerationCreated)

	session.handleResponseEvent(map[string]any{
		"event": map[string]any{
			"textOutput": map[string]any{
				"role":      "ASSISTANT",
				"content":   awsRealtimeBargeInContent,
				"contentId": "barge-in-1",
			},
		},
	})
	if !checked {
		t.Fatal("barge-in did not emit speech_started")
	}
}

func TestAWSRealtimeSessionMissingRoleBargeInClosesGenerationWithoutSpeechStart(t *testing.T) {
	session := newAWSRealtimeSession(NewAWSRealtimeModel(""), nil)
	defer session.Close()
	generation, _ := session.ensureGenerationWithCreated("response-1")

	session.handleResponseEvent(map[string]any{
		"event": map[string]any{
			"textOutput": map[string]any{
				"content":   awsRealtimeBargeInContent,
				"contentId": "barge-in-1",
			},
		},
	})

	select {
	case _, ok := <-generation.textCh:
		if ok {
			t.Fatal("generation TextCh still open, want handler to close on barge-in sentinel")
		}
	default:
		t.Fatal("generation TextCh still open, want handler to close on barge-in sentinel")
	}
	assertNoAWSRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeSpeechStarted)
}

func TestAWSRealtimeSessionMarksReferenceAssistantMessageInterruptedOnBargeIn(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
	awsSession := session.(*awsRealtimeSession)

	awsSession.handleResponseEvent(map[string]any{
		"event": map[string]any{
			"contentStart": map[string]any{
				"type":                  "TEXT",
				"role":                  "ASSISTANT",
				"contentId":             "text-1",
				"additionalModelFields": "{\"generationStage\":\"SPECULATIVE\"}",
			},
		},
	})
	awsSession.handleResponseEvent(map[string]any{
		"event": map[string]any{
			"textOutput": map[string]any{
				"role":      "ASSISTANT",
				"content":   "hello",
				"contentId": "text-1",
			},
		},
	})
	awsSession.handleResponseEvent(map[string]any{
		"event": map[string]any{
			"textOutput": map[string]any{
				"role":      "ASSISTANT",
				"content":   awsRealtimeBargeInContent,
				"contentId": "barge-1",
			},
		},
	})

	awsSession.mu.Lock()
	defer awsSession.mu.Unlock()
	if len(awsSession.chatCtx.Items) == 0 {
		t.Fatal("chat context empty, want assistant message")
	}
	msg, ok := awsSession.chatCtx.Items[len(awsSession.chatCtx.Items)-1].(*llm.ChatMessage)
	if !ok || msg.Role != llm.ChatRoleAssistant {
		t.Fatalf("last chat item = %#v, want assistant message", awsSession.chatCtx.Items[len(awsSession.chatCtx.Items)-1])
	}
	if !msg.Interrupted {
		t.Fatal("assistant message Interrupted = false, want reference barge-in marker")
	}
}

func TestAWSRealtimeSessionInterruptIsReferenceNoop(t *testing.T) {
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
	stream.emitJSON(`{"event":{"contentStart":{"type":"AUDIO","role":"ASSISTANT","contentId":"audio-1"}}}`)

	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt error = %v", err)
	}

	audioBytes := []byte{1, 2, 3, 4}
	stream.emitJSON(`{"event":{"audioOutput":{"contentId":"audio-1","content":"` + base64.StdEncoding.EncodeToString(audioBytes) + `"}}}`)
	audioEvent := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeAudio)
	if string(audioEvent.Data) != string(audioBytes) {
		t.Fatalf("audio event data = %v, want %v", audioEvent.Data, audioBytes)
	}
	select {
	case audio, ok := <-msg.AudioCh:
		if !ok {
			t.Fatal("AudioCh closed on interrupt, want provider-managed barge-in")
		}
		if string(audio.Data) != string(audioBytes) {
			t.Fatalf("message audio data = %v, want %v", audio.Data, audioBytes)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for AudioCh data")
	}

	stream.emitJSON(`{"event":{"completionEnd":{"completionId":"completion-1"}}}`)
	select {
	case _, ok := <-msg.AudioCh:
		if ok {
			t.Fatal("AudioCh still open, want closed after provider completionEnd")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for AudioCh close after completionEnd")
	}
	select {
	case _, ok := <-created.Generation.FunctionCh:
		if ok {
			t.Fatal("FunctionCh still open, want closed after provider completionEnd")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for FunctionCh close after completionEnd")
	}
}

func TestAWSRealtimeSessionSayReportsReferenceUnsupported(t *testing.T) {
	session := newAWSRealtimeSession(NewAWSRealtimeModel(""), nil)

	err := session.Say("hello")

	if err == nil {
		t.Fatal("Say error = nil, want reference unsupported error")
	}
	if !strings.Contains(err.Error(), "does not implement say(). use a TTS model instead") {
		t.Fatalf("Say error = %q, want reference unsupported say message", err.Error())
	}
}

func TestAWSRealtimeSessionTruncateIsReferenceNoop(t *testing.T) {
	session := newAWSRealtimeSession(NewAWSRealtimeModel(""), nil)
	transcript := "played words"

	if err := session.Truncate(llm.RealtimeTruncateOptions{
		MessageID:       "assistant-1",
		AudioEndMillis:  250,
		AudioTranscript: &transcript,
	}); err != nil {
		t.Fatalf("Truncate error = %v, want nil reference warning-only no-op", err)
	}
}

func TestAWSRealtimeSessionPushVideoIsReferenceNoop(t *testing.T) {
	session := newAWSRealtimeSession(NewAWSRealtimeModel(""), nil)

	if err := session.PushVideo(nil); err != nil {
		t.Fatalf("PushVideo error = %v, want nil reference warning-only no-op", err)
	}
}

func TestAWSRealtimeSessionEmitsReferenceSpeculativeGenerationBeforeTurnFinality(t *testing.T) {
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
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	if created.Generation == nil {
		t.Fatal("Generation = nil, want speculative contentStart generation")
	}
	stopped := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeSpeechStopped)
	if stopped.SpeechStopped == nil || !stopped.SpeechStopped.UserTranscriptionEnabled {
		t.Fatalf("SpeechStopped = %#v, want user transcription enabled", stopped.SpeechStopped)
	}
	final := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)
	if final.InputTranscription == nil || !final.InputTranscription.IsFinal || final.InputTranscription.Transcript != "hello" {
		t.Fatalf("final transcription = %#v, want final hello after generation", final.InputTranscription)
	}
	reemit := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	if reemit.Generation == nil || reemit.Generation.ResponseID != created.Generation.ResponseID {
		t.Fatalf("re-emitted generation = %#v, want same response id %q", reemit.Generation, created.Generation.ResponseID)
	}
}

func TestAWSRealtimeSessionMalformedSpeculativeStartSkipsReferenceTurnFinality(t *testing.T) {
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

	stream.emitJSON(`{"event":{"contentStart":{"type":"TEXT","role":"ASSISTANT","additionalModelFields":"{\"generationStage\":\"SPECULATIVE\"}"}}}`)
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	if created.Generation == nil {
		t.Fatal("Generation = nil, want reference generation before malformed contentId abort")
	}
	assertNoAWSRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeSpeechStopped)
	assertNoAWSRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)
}

func TestAWSRealtimeSessionEmitsReferenceToolGenerationBeforeTurnFinality(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	stream.emitJSON(`{"event":{"textOutput":{"role":"USER","content":"book a flight","contentId":"user-1"}}}`)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeSpeechStarted)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)

	stream.emitJSON(`{"event":{"contentStart":{"type":"TOOL","role":"TOOL","contentId":"tool-content-1"}}}`)
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	if created.Generation == nil {
		t.Fatal("Generation = nil, want tool contentStart generation")
	}
	stopped := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeSpeechStopped)
	if stopped.SpeechStopped == nil || !stopped.SpeechStopped.UserTranscriptionEnabled {
		t.Fatalf("SpeechStopped = %#v, want user transcription enabled", stopped.SpeechStopped)
	}
	final := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)
	if final.InputTranscription == nil || !final.InputTranscription.IsFinal || final.InputTranscription.Transcript != "book a flight" {
		t.Fatalf("final transcription = %#v, want final book a flight after tool generation", final.InputTranscription)
	}
	reemit := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	if reemit.Generation == nil || reemit.Generation.ResponseID != created.Generation.ResponseID {
		t.Fatalf("re-emitted generation = %#v, want same response id %q", reemit.Generation, created.Generation.ResponseID)
	}

	stream.emitJSON(`{"event":{"toolUse":{"toolUseId":"tool-1","toolName":"lookup","content":"{\"destination\":\"SFO\"}"}}}`)
	var call *llm.FunctionCall
	select {
	case call = <-created.Generation.FunctionCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for function stream call")
	}
	if call.CallID != "tool-1" || call.Name != "lookup" || call.Arguments != `{"destination":"SFO"}` {
		t.Fatalf("function call = %#v, want lookup tool-1 with destination", call)
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
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeSpeechStopped)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)

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
	assertNoAWSRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeText)
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

func TestAWSRealtimeSessionIgnoresReferenceInvalidUntrackedAudioOutput(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
	awsSession := session.(*awsRealtimeSession)

	stream.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	var msg llm.MessageGeneration
	select {
	case msg = <-created.Generation.MessageCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message generation")
	}

	stream.emitJSON(`{"event":{"audioOutput":{"contentId":"stray-audio","content":"not-base64"}}}`)

	assertNoAWSRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeError)
	select {
	case frame := <-msg.AudioCh:
		t.Fatalf("generation audio frame = %#v, want no frame for invalid untracked audio", frame)
	case <-time.After(50 * time.Millisecond):
	}
	awsSession.mu.Lock()
	activeGeneration := awsSession.generation != nil
	awsSession.mu.Unlock()
	if !activeGeneration {
		t.Fatal("generation closed, want invalid untracked audio ignored before decode")
	}
}

func TestAWSRealtimeSessionStreamsReferenceAssistantTextWithoutOutputRole(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
	awsSession := session.(*awsRealtimeSession)

	stream.emitJSON(`{"event":{"contentStart":{"type":"TEXT","role":"ASSISTANT","contentId":"text-1","additionalModelFields":"{\"generationStage\":\"SPECULATIVE\"}"}}}`)
	created := assertAWSRealtimeEventEventually(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	var msg llm.MessageGeneration
	select {
	case msg = <-created.Generation.MessageCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message generation")
	}

	stream.emitJSON(`{"event":{"textOutput":{"content":"roleless delta","contentId":"text-1"}}}`)

	select {
	case text := <-msg.TextCh:
		if text != "roleless delta" {
			t.Fatalf("text delta = %q, want roleless delta", text)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for roleless assistant text delta")
	}
	chatCtx := awsSession.chatCtx
	if chatCtx == nil || len(chatCtx.Items) != 1 {
		t.Fatalf("chatCtx items = %#v, want assistant provider text history", chatCtx)
	}
	assistantMsg, ok := chatCtx.Items[0].(*llm.ChatMessage)
	if !ok || assistantMsg.Role != llm.ChatRoleAssistant || assistantMsg.TextContent() != "roleless delta" {
		t.Fatalf("assistant history = %#v, want roleless delta", chatCtx.Items[0])
	}
}

func TestAWSRealtimeSessionStreamsReferenceEmptyAssistantTextDelta(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
	awsSession := session.(*awsRealtimeSession)

	stream.emitJSON(`{"event":{"contentStart":{"type":"TEXT","role":"ASSISTANT","contentId":"text-empty","additionalModelFields":"{\"generationStage\":\"SPECULATIVE\"}"}}}`)
	created := assertAWSRealtimeEventEventually(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	var msg llm.MessageGeneration
	select {
	case msg = <-created.Generation.MessageCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message generation")
	}

	stream.emitJSON(`{"event":{"textOutput":{"content":"","contentId":"text-empty"}}}`)

	select {
	case text := <-msg.TextCh:
		if text != "" {
			t.Fatalf("text delta = %q, want empty reference delta", text)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for empty assistant text delta")
	}
	awsSession.mu.Lock()
	chatCtx := awsSession.chatCtx
	awsSession.mu.Unlock()
	if chatCtx == nil || len(chatCtx.Items) != 1 {
		t.Fatalf("chatCtx items = %#v, want empty assistant provider text history", chatCtx)
	}
	assistantMsg, ok := chatCtx.Items[0].(*llm.ChatMessage)
	if !ok || assistantMsg.Role != llm.ChatRoleAssistant || assistantMsg.TextContent() != "" {
		t.Fatalf("assistant history = %#v, want empty delta", chatCtx.Items[0])
	}
}

func TestAWSRealtimeSessionPreservesReferenceProviderTextHistory(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
	awsSession := session.(*awsRealtimeSession)

	stream.emitJSON(`{"event":{"contentStart":{"type":"TEXT","role":"USER","contentId":"user-1"}}}`)
	stream.emitJSON(`{"event":{"textOutput":{"role":"USER","content":"hello","contentId":"user-1"}}}`)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeSpeechStarted)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)
	stream.emitJSON(`{"event":{"textOutput":{"role":"USER","content":"again","contentId":"user-1"}}}`)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)

	stream.emitJSON(`{"event":{"contentStart":{"type":"TEXT","role":"ASSISTANT","contentId":"text-1","additionalModelFields":"{\"generationStage\":\"SPECULATIVE\"}"}}}`)
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeSpeechStopped)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)
	var msg llm.MessageGeneration
	select {
	case msg = <-created.Generation.MessageCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message generation")
	}
	stream.emitJSON(`{"event":{"textOutput":{"role":"ASSISTANT","content":"hi","contentId":"text-1"}}}`)
	select {
	case text := <-msg.TextCh:
		if text != "hi" {
			t.Fatalf("assistant text delta = %q, want hi", text)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for assistant text delta")
	}

	awsSession.mu.Lock()
	chatCtx := awsSession.chatCtx
	awsSession.mu.Unlock()
	if chatCtx == nil || len(chatCtx.Items) != 2 {
		t.Fatalf("chatCtx items = %#v, want user and assistant provider text history", chatCtx)
	}
	userMsg, ok := chatCtx.Items[0].(*llm.ChatMessage)
	if !ok || userMsg.Role != llm.ChatRoleUser || userMsg.TextContent() != "hello\nagain" {
		t.Fatalf("user history = %#v, want merged provider ASR text", chatCtx.Items[0])
	}
	assistantMsg, ok := chatCtx.Items[1].(*llm.ChatMessage)
	if !ok || assistantMsg.Role != llm.ChatRoleAssistant || assistantMsg.TextContent() != "hi" {
		t.Fatalf("assistant history = %#v, want provider assistant text", chatCtx.Items[1])
	}
	if !awsSession.isAudioTranscriptMessage(userMsg.ID) {
		t.Fatalf("user message id %q not marked as provider audio transcript", userMsg.ID)
	}
}

func TestAWSRealtimeSessionPreservesReferenceRolelessUserTextHistory(t *testing.T) {
	session := newAWSRealtimeSession(NewAWSRealtimeModel(""), nil)

	session.handleResponseEvent(map[string]any{
		"event": map[string]any{
			"contentStart": map[string]any{
				"type":      "TEXT",
				"role":      "USER",
				"contentId": "user-1",
			},
		},
	})
	session.handleResponseEvent(map[string]any{
		"event": map[string]any{
			"textOutput": map[string]any{
				"content":   "roleless user transcript",
				"contentId": "user-1",
			},
		},
	})

	chatCtx := session.chatCtx
	if chatCtx == nil || len(chatCtx.Items) != 1 {
		t.Fatalf("chatCtx items = %#v, want roleless user transcript history", chatCtx)
	}
	userMsg, ok := chatCtx.Items[0].(*llm.ChatMessage)
	if !ok || userMsg.Role != llm.ChatRoleUser || userMsg.TextContent() != "roleless user transcript" {
		t.Fatalf("user history = %#v, want roleless user transcript", chatCtx.Items[0])
	}
	if !session.isAudioTranscriptMessage(userMsg.ID) {
		t.Fatalf("user message id %q not marked as provider audio transcript", userMsg.ID)
	}
}

func TestAWSRealtimeSessionPreservesReferenceBareUserTextHistory(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
	awsSession := session.(*awsRealtimeSession)

	stream.emitJSON(`{"event":{"textOutput":{"role":"USER","content":"bare hello","contentId":"user-bare"}}}`)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeSpeechStarted)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)

	awsSession.mu.Lock()
	chatCtx := awsSession.chatCtx
	awsSession.mu.Unlock()
	if chatCtx == nil || len(chatCtx.Items) != 1 {
		t.Fatalf("chatCtx items = %#v, want bare provider USER text history", chatCtx)
	}
	userMsg, ok := chatCtx.Items[0].(*llm.ChatMessage)
	if !ok || userMsg.Role != llm.ChatRoleUser || userMsg.TextContent() != "bare hello" {
		t.Fatalf("user history = %#v, want bare provider ASR text", chatCtx.Items[0])
	}
	if !awsSession.isAudioTranscriptMessage(userMsg.ID) {
		t.Fatalf("bare user message id %q not marked as provider audio transcript", userMsg.ID)
	}
}

func TestAWSRealtimeSessionIgnoresReferenceTextOutputMissingContentID(t *testing.T) {
	session := newAWSRealtimeSession(NewAWSRealtimeModel(""), nil)
	defer session.Close()

	if !session.handleResponseEvent(map[string]any{
		"event": map[string]any{
			"textOutput": map[string]any{
				"role":    "USER",
				"content": "missing id",
			},
		},
	}) {
		t.Fatal("handleResponseEvent = false, want malformed textOutput ignored without ending reader")
	}

	assertNoAWSRealtimeEvent(t, session.EventCh())
	session.mu.Lock()
	chatCtx := session.chatCtx
	session.mu.Unlock()
	if chatCtx != nil && len(chatCtx.Items) != 0 {
		t.Fatalf("chatCtx items = %#v, want no history for missing contentId", chatCtx.Items)
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

func TestAWSRealtimeSessionIgnoresReferenceToolUseMissingName(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
	awsSession := session.(*awsRealtimeSession)

	stream.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	<-created.Generation.MessageCh
	stream.emitJSON(`{"event":{"contentStart":{"type":"TOOL","role":"TOOL","contentId":"tool-content-1"}}}`)
	stream.emitJSON(`{"event":{"toolUse":{"toolUseId":"tool-1","content":"{\"query\":\"weather\"}"}}}`)

	select {
	case call := <-created.Generation.FunctionCh:
		t.Fatalf("function call = %#v, want none for missing reference toolName", call)
	case <-time.After(50 * time.Millisecond):
	}
	awsSession.mu.Lock()
	_, pending := awsSession.pending["tool-1"]
	activeGeneration := awsSession.generation != nil
	awsSession.mu.Unlock()
	if pending {
		t.Fatal("tool-1 pending = true, want malformed toolUse ignored before pending registration")
	}
	if !activeGeneration {
		t.Fatal("generation closed, want malformed toolUse to leave reference generation open")
	}
}

func TestAWSRealtimeSessionMarksReferenceToolPendingDuringEmission(t *testing.T) {
	provider := NewAWSRealtimeModel("")
	session := newAWSRealtimeSession(provider, nil)
	generation, _ := session.ensureGenerationWithCreated("response-1")

	ok := session.sendGenerationFunction(&llm.FunctionCall{
		CallID:    "tool-1",
		Name:      "lookup",
		Arguments: `{"query":"weather"}`,
	})
	if !ok {
		t.Fatal("sendGenerationFunction = false, want emitted tool call")
	}
	session.mu.Lock()
	_, pending := session.pending["tool-1"]
	activeGeneration := session.generation != nil
	session.mu.Unlock()
	if !pending {
		t.Fatal("tool-1 pending = false, want registered before generation close")
	}
	if activeGeneration {
		t.Fatal("generation still active, want closed after tool call emission")
	}
	var call *llm.FunctionCall
	select {
	case call = <-generation.functionCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for emitted tool call")
	}
	if call.CallID != "tool-1" || call.Name != "lookup" {
		t.Fatalf("function call = %#v, want lookup tool-1", call)
	}
}

func TestAWSRealtimeSessionUnwrapsReferenceDoubleEncodedToolArguments(t *testing.T) {
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
	stream.emitJSON(`{"event":{"toolUse":{"toolUseId":"tool-double","toolName":"lookup","content":"\"{\\\"input\\\":{\\\"date\\\":\\\"2026-04-10\\\"}}\""}}}`)

	var call *llm.FunctionCall
	select {
	case call = <-created.Generation.FunctionCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for function stream call")
	}
	if call.Arguments != `{"input":{"date":"2026-04-10"}}` {
		t.Fatalf("arguments = %q, want reference unwrapped JSON object string", call.Arguments)
	}
}

func TestAWSRealtimeSessionKeepsReferenceStringPrimitiveToolArguments(t *testing.T) {
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
	stream.emitJSON(`{"event":{"toolUse":{"toolUseId":"tool-string","toolName":"lookup","content":"\"hello\""}}}`)

	var call *llm.FunctionCall
	select {
	case call = <-created.Generation.FunctionCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for function stream call")
	}
	if call.Arguments != `"hello"` {
		t.Fatalf("arguments = %q, want reference JSON string literal preserved", call.Arguments)
	}
}

func TestAWSRealtimeSessionKeepsReferenceInvalidJSONToolArguments(t *testing.T) {
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
	stream.emitJSON(`{"event":{"toolUse":{"toolUseId":"tool-invalid","toolName":"lookup","content":"not-valid-json"}}}`)

	var call *llm.FunctionCall
	select {
	case call = <-created.Generation.FunctionCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for function stream call")
	}
	if call.Arguments != "not-valid-json" {
		t.Fatalf("arguments = %q, want reference invalid JSON string preserved", call.Arguments)
	}
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
	sent := stream.snapshotSent()
	sentCount := len(sent)
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext error = %v", err)
	}
	sent = stream.snapshotSent()
	if len(sent) != sentCount {
		t.Fatalf("stray tool result sent %d events, want none", len(sent)-sentCount)
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
	contentStart := nestedMap(t, start, "event", "contentStart")
	if got := contentStart["type"]; got != "TOOL" {
		t.Fatalf("tool contentStart type = %#v, want TOOL", got)
	}
	if got := contentStart["role"]; got != "TOOL" {
		t.Fatalf("tool contentStart role = %#v, want TOOL", got)
	}
	if got := contentStart["interactive"]; got != false {
		t.Fatalf("tool contentStart interactive = %#v, want false", got)
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

func TestAWSRealtimeSessionDefersToolUpdateRecycleUntilPendingToolResult(t *testing.T) {
	first := newFakeAWSRealtimeStream()
	second := newFakeAWSRealtimeStream()
	client := &fakeAWSRealtimeClient{streams: []awsRealtimeStream{first, second}}
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(client))
	session := newAWSRealtimeSession(provider, client)

	if err := session.UpdateTools([]llm.Tool{awsRequestTestTool{}}); err != nil {
		t.Fatalf("initial UpdateTools error = %v", err)
	}
	if err := session.start(context.Background()); err != nil {
		t.Fatalf("start error = %v", err)
	}
	defer session.Close()
	waitAWSRealtimeAudioContentStart(t, first, 0)

	first.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	<-created.Generation.MessageCh
	first.emitJSON(`{"event":{"contentStart":{"type":"TOOL","role":"TOOL","contentId":"tool-content-1"}}}`)
	first.emitJSON(`{"event":{"toolUse":{"toolUseId":"tool-1","toolName":"lookup","content":"{\"query\":\"weather\"}"}}}`)
	select {
	case <-created.Generation.FunctionCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for function stream call")
	}

	firstSent := first.snapshotSent()
	sentBeforeUpdate := len(firstSent)
	if err := session.UpdateTools([]llm.Tool{awsSecondRequestTestTool{}}); err != nil {
		t.Fatalf("UpdateTools active error = %v", err)
	}
	if first.isClosed() {
		t.Fatal("first stream closed = true, want deferred recycle while tool result pending")
	}
	secondSent := second.snapshotSent()
	if len(secondSent) != 0 {
		t.Fatalf("second stream sent %d events, want no restart before pending tool result", len(secondSent))
	}
	firstSent = first.snapshotSent()
	if len(firstSent) != sentBeforeUpdate {
		t.Fatalf("UpdateTools sent %d events before tool result, want none", len(firstSent)-sentBeforeUpdate)
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

	firstSent = first.snapshotSent()
	toolResult := mustAWSRealtimeJSONEvent(t, firstSent[sentBeforeUpdate+1])
	if got := awsRealtimeNestedString(toolResult, "event", "toolResult", "content"); got != `{"forecast":"sunny"}` {
		t.Fatalf("tool result content = %q, want output before recycle", got)
	}
	if !first.isClosed() {
		t.Fatal("first stream closed = false, want recycle after pending tool result")
	}
	secondSent = second.snapshotSent()
	if len(secondSent) == 0 {
		t.Fatal("second stream sent no events, want restarted prompt after tool result")
	}
	toolConfig := nestedMap(t, mustAWSRealtimeJSONEvent(t, secondSent[1]), "event", "promptStart", "toolConfiguration")
	tools, ok := toolConfig["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("recycled tools = %#v, want one tool", toolConfig["tools"])
	}
	spec := nestedMap(t, map[string]any{"tool": tools[0]}, "tool", "toolSpec")
	if spec["name"] != "lookup_order" {
		t.Fatalf("recycled tool name = %#v, want lookup_order", spec["name"])
	}
}

func TestAWSRealtimeSessionToolUpdateRecycleClearsStalePendingTool(t *testing.T) {
	first := newFakeAWSRealtimeStream()
	second := newFakeAWSRealtimeStream()
	client := &fakeAWSRealtimeClient{streams: []awsRealtimeStream{first, second}}
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(client))
	provider.toolRecycleDelay = 10 * time.Millisecond
	session := newAWSRealtimeSession(provider, client)
	if err := session.UpdateTools([]llm.Tool{awsRequestTestTool{}}); err != nil {
		t.Fatalf("initial UpdateTools error = %v", err)
	}
	if err := session.start(context.Background()); err != nil {
		t.Fatalf("start error = %v", err)
	}
	defer session.Close()

	first.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	<-created.Generation.MessageCh
	first.emitJSON(`{"event":{"contentStart":{"type":"TOOL","role":"TOOL","contentId":"tool-content-1"}}}`)
	first.emitJSON(`{"event":{"toolUse":{"toolUseId":"tool-stale","toolName":"lookup","content":"{\"query\":\"weather\"}"}}}`)
	select {
	case <-created.Generation.FunctionCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for function call")
	}

	if err := session.UpdateTools([]llm.Tool{awsSecondRequestTestTool{}}); err != nil {
		t.Fatalf("UpdateTools with pending tool error = %v", err)
	}
	event := assertAWSRealtimeEventEventually(t, session.EventCh(), llm.RealtimeEventTypeSessionReconnected)
	if event.Reconnect == nil {
		t.Fatal("Reconnect = nil, want reference tool recycle")
	}
	if !first.isClosed() {
		t.Fatal("first stream closed = false, want stale pending tool recycle")
	}
	secondSent := second.snapshotSent()
	if len(secondSent) == 0 {
		t.Fatal("second stream sent no events, want restarted prompt with new tools")
	}
	toolConfig := nestedMap(t, mustAWSRealtimeJSONEvent(t, secondSent[1]), "event", "promptStart", "toolConfiguration")
	tools, ok := toolConfig["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("recycled tools = %#v, want one tool", toolConfig["tools"])
	}
	spec := nestedMap(t, map[string]any{"tool": tools[0]}, "tool", "toolSpec")
	if spec["name"] != "lookup_order" {
		t.Fatalf("recycled tool name = %#v, want lookup_order", spec["name"])
	}
}

func TestAWSRealtimeSessionDropsReferenceToolResultAfterSendFailure(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
	waitAWSRealtimeAudioContentStart(t, stream, 0)

	stream.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	<-created.Generation.MessageCh
	stream.emitJSON(`{"event":{"contentStart":{"type":"TOOL","role":"TOOL","contentId":"tool-content-1"}}}`)
	stream.emitJSON(`{"event":{"toolUse":{"toolUseId":"tool-retry","toolName":"lookup","content":"{}"}}}`)
	select {
	case <-created.Generation.FunctionCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for function stream call")
	}

	ctx := llm.NewChatContext()
	ctx.Append(&llm.FunctionCallOutput{
		CallID: "tool-retry",
		Name:   "lookup",
		Output: `{"ok":true}`,
	})
	stream.sendErr = errors.New("bedrock send failed")
	sentBeforeFailure := len(stream.sent)
	if err := session.UpdateChatContext(ctx); err == nil {
		t.Fatal("UpdateChatContext error = nil, want send failure")
	}
	if len(stream.sent) != sentBeforeFailure {
		t.Fatalf("failed UpdateChatContext sent %d events, want none accepted before send error", len(stream.sent)-sentBeforeFailure)
	}
	awsSession := session.(*awsRealtimeSession)
	awsSession.mu.Lock()
	_, stillPending := awsSession.pending["tool-retry"]
	awsSession.mu.Unlock()
	if stillPending {
		t.Fatal("tool-retry still pending after failed send, want reference pending state cleared before delivery")
	}
	stream.sendErr = nil
	sentCount := len(stream.sent)

	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext after failed send error = %v", err)
	}
	if len(stream.sent) != sentCount {
		t.Fatalf("UpdateChatContext after failed send emitted %d events, want no stale reference retry", len(stream.sent)-sentCount)
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

func TestAWSRealtimeSessionUnwrapsReferenceJSONStringToolResult(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
	waitAWSRealtimeAudioContentStart(t, stream, 0)

	stream.emitJSON(`{"event":{"contentStart":{"type":"TOOL","role":"TOOL","contentId":"tool-content-1"}}}`)
	stream.emitJSON(`{"event":{"toolUse":{"toolUseId":"tool-json-string","toolName":"lookup","content":"{}"}}}`)
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	select {
	case <-created.Generation.FunctionCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for function stream call")
	}
	ctx := llm.NewChatContext()
	ctx.Append(&llm.FunctionCallOutput{
		CallID: "tool-json-string",
		Name:   "lookup",
		Output: `"sunny"`,
	})

	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext error = %v", err)
	}

	result := mustAWSRealtimeJSONEvent(t, stream.sent[len(stream.sent)-2])
	if got := awsRealtimeNestedString(result, "event", "toolResult", "content"); got != "sunny" {
		t.Fatalf("JSON string tool result content = %q, want reference unwrapped string", got)
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
	waitAWSRealtimeAudioContentStart(t, stream, 0)

	ctx := llm.NewChatContext()
	ctx.Append(&llm.ChatMessage{
		ID:      "user-1",
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "hello sonic"}},
	})
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext error = %v", err)
	}
	waitAWSRealtimeTextInput(t, stream, "hello sonic")
	textInputIndex := -1
	for i, raw := range stream.sent {
		if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, raw), "event", "textInput", "content"); got == "hello sonic" {
			textInputIndex = i
			break
		}
	}
	if textInputIndex < 1 || textInputIndex+1 >= len(stream.sent) {
		t.Fatalf("text input index = %d in %d events, want surrounding content events", textInputIndex, len(stream.sent))
	}
	textEvents := stream.sent[textInputIndex-1 : textInputIndex+2]
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

func TestAWSRealtimeSessionSkipsReferenceAudioTranscriptUserText(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	awsSession := session.(*awsRealtimeSession)
	awsSession.handleResponseEvent(map[string]any{
		"event": map[string]any{
			"textOutput": map[string]any{"role": "USER", "content": "hello sonic", "contentId": "user-audio-1"},
		},
	})
	awsSession.handleResponseEvent(map[string]any{
		"event": map[string]any{
			"contentStart": map[string]any{
				"type":                  "TEXT",
				"role":                  "ASSISTANT",
				"contentId":             "assistant-spec-1",
				"additionalModelFields": "SPECULATIVE",
			},
		},
	})

	audioMessageID := ""
	for range 5 {
		event := <-awsSession.eventCh
		if event.Type == llm.RealtimeEventTypeInputAudioTranscriptionCompleted && event.InputTranscription != nil && event.InputTranscription.IsFinal {
			audioMessageID = event.InputTranscription.ItemID
			break
		}
	}
	if audioMessageID == "" {
		t.Fatal("final audio transcript item id is empty")
	}

	ctx := llm.NewChatContext()
	ctx.Append(&llm.ChatMessage{
		ID:      audioMessageID,
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "hello sonic"}},
	})
	sent := stream.snapshotSent()
	sentCount := len(sent)
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext error = %v", err)
	}
	sent = stream.snapshotSent()
	if len(sent) != sentCount {
		t.Fatalf("audio transcript user text sent %d events, want none", len(sent)-sentCount)
	}
}

func TestAWSRealtimeSessionDoesNotReplayUserTextAfterSendFailure(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeGenerateReplyTimeout(50*time.Millisecond), WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
	waitAWSRealtimeAudioContentStart(t, stream, 0)

	ctx := llm.NewChatContext()
	ctx.Append(&llm.ChatMessage{
		ID:      "user-retry",
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "try again"}},
	})

	stream.sendErr = errors.New("bedrock send failed")
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext error = %v, want nil because reference sends user text asynchronously", err)
	}
	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{}); err == nil {
		t.Fatal("GenerateReply error = nil, want pending async send failure")
	}
	stream.sendErr = nil
	sentCount := len(stream.sent)

	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext repeat error = %v", err)
	}
	if len(stream.sent) != sentCount {
		t.Fatalf("repeat UpdateChatContext sent %d events, want none after failed send marked delivered", len(stream.sent)-sentCount)
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
	waitAWSRealtimeAudioContentStart(t, stream, 0)

	done := make(chan error, 1)
	go func() {
		done <- session.GenerateReply(llm.RealtimeGenerateReplyOptions{Instructions: "ask for the card number"})
	}()
	waitAWSRealtimeTextInput(t, stream, "ask for the card number")

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
	stream.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	if err := <-done; err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}
}

func TestAWSRealtimeSessionGenerateReplyIgnoresReferencePerResponseTools(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModelWithNovaSonic2(WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
	waitAWSRealtimeAudioContentStart(t, stream, 0)
	sentCount := len(stream.sent)

	done := make(chan error, 1)
	go func() {
		done <- session.GenerateReply(llm.RealtimeGenerateReplyOptions{
			Instructions: "ask for the card number",
			Tools:        []llm.Tool{awsRequestTestTool{}},
			ToolChoice:   "required",
		})
	}()
	waitAWSRealtimeTextInput(t, stream, "ask for the card number")

	if got := len(stream.sent) - sentCount; got != 3 {
		t.Fatalf("GenerateReply sent %d provider events, want only reference interactive text triplet", got)
	}
	for _, raw := range stream.sent[sentCount:] {
		event := mustAWSRealtimeJSONEvent(t, raw)
		if nestedMap(t, event, "event")["promptStart"] != nil {
			t.Fatalf("GenerateReply sent promptStart with per-response tools: %s", raw)
		}
	}
	stream.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	if err := <-done; err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}
}

func TestAWSRealtimeSessionGenerateReplyUsesReferenceTimeout(t *testing.T) {
	stream := &blockingAWSRealtimeStream{fakeAWSRealtimeStream: newFakeAWSRealtimeStream()}
	provider := NewAWSRealtimeModelWithNovaSonic2(
		WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}),
		WithAWSRealtimeGenerateReplyTimeout(time.Millisecond),
	)
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer func() {
		stream.setBlocked(false)
		_ = session.Close()
	}()
	stream.setBlocked(true)

	err = session.GenerateReply(llm.RealtimeGenerateReplyOptions{Instructions: "ask for the card number"})

	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("GenerateReply error = %T %v, want APITimeoutError", err, err)
	}
}

func TestAWSRealtimeSessionGenerateReplyWaitsForReferenceGenerationStart(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModelWithNovaSonic2(
		WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}),
		WithAWSRealtimeGenerateReplyTimeout(time.Second),
	)
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
	waitAWSRealtimeAudioContentStart(t, stream, 0)

	done := make(chan error, 1)
	go func() {
		done <- session.GenerateReply(llm.RealtimeGenerateReplyOptions{Instructions: "ask for the card number"})
	}()
	waitAWSRealtimeTextInput(t, stream, "ask for the card number")

	select {
	case err := <-done:
		t.Fatalf("GenerateReply returned before generation start: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	stream.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	if err := <-done; err != nil {
		t.Fatalf("GenerateReply error after generation start = %v", err)
	}
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	if created.Generation == nil {
		t.Fatal("Generation = nil, want reference generation event")
	}
}

func TestAWSRealtimeSessionGenerateReplyWithoutInstructionsJoinsReferencePendingGeneration(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModelWithNovaSonic2(
		WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}),
		WithAWSRealtimeGenerateReplyTimeout(time.Second),
	)
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
	waitAWSRealtimeAudioContentStart(t, stream, 0)

	instructionsDone := make(chan error, 1)
	go func() {
		instructionsDone <- session.GenerateReply(llm.RealtimeGenerateReplyOptions{Instructions: "ask for the card number"})
	}()
	waitAWSRealtimeTextInput(t, stream, "ask for the card number")
	sentCount := len(stream.sent)

	joinDone := make(chan error, 1)
	go func() {
		joinDone <- session.GenerateReply(llm.RealtimeGenerateReplyOptions{})
	}()

	select {
	case err := <-joinDone:
		t.Fatalf("GenerateReply without instructions returned before pending generation start: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if len(stream.sent) != sentCount {
		t.Fatalf("GenerateReply without instructions sent %d provider events, want join existing pending generation", len(stream.sent)-sentCount)
	}

	stream.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	if err := <-instructionsDone; err != nil {
		t.Fatalf("GenerateReply with instructions error after generation start = %v", err)
	}
	if err := <-joinDone; err != nil {
		t.Fatalf("GenerateReply without instructions error after generation start = %v", err)
	}
}

func TestAWSRealtimeSessionGenerateReplyKeepsReferenceLatestPendingGeneration(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModelWithNovaSonic2(
		WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}),
		WithAWSRealtimeGenerateReplyTimeout(60*time.Millisecond),
	)
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
	waitAWSRealtimeAudioContentStart(t, stream, 0)

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- session.GenerateReply(llm.RealtimeGenerateReplyOptions{Instructions: "first prompt"})
	}()
	waitAWSRealtimeTextInput(t, stream, "first prompt")

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- session.GenerateReply(llm.RealtimeGenerateReplyOptions{Instructions: "second prompt"})
	}()
	waitAWSRealtimeTextInput(t, stream, "second prompt")

	stream.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	if err := <-secondDone; err != nil {
		t.Fatalf("second GenerateReply error after generation start = %v", err)
	}
	select {
	case err := <-firstDone:
		var realtimeErr llm.RealtimeError
		if !errors.As(err, &realtimeErr) {
			t.Fatalf("first GenerateReply error = %T %v, want RealtimeError", err, err)
		}
		if !strings.Contains(err.Error(), "generate_reply timed out waiting for generation") {
			t.Fatalf("first GenerateReply error = %v, want generation timeout", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first GenerateReply did not time out after newer pending generation resolved")
	}
}

func TestAWSRealtimeSessionGenerateReplyWaitsForReferenceChatContextGeneration(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModelWithNovaSonic2(
		WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}),
		WithAWSRealtimeGenerateReplyTimeout(time.Second),
	)
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
	waitAWSRealtimeAudioContentStart(t, stream, 0)

	ctx := llm.NewChatContext()
	ctx.AddMessage(llm.ChatMessageArgs{ID: "user-1", Role: llm.ChatRoleUser, Text: "hello from user"})
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext error = %v", err)
	}
	waitAWSRealtimeTextInput(t, stream, "hello from user")

	done := make(chan error, 1)
	go func() {
		done <- session.GenerateReply(llm.RealtimeGenerateReplyOptions{})
	}()
	select {
	case err := <-done:
		t.Fatalf("GenerateReply returned before chat-context generation start: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	stream.emitJSON(`{"event":{"completionStart":{"completionId":"completion-1"}}}`)
	if err := <-done; err != nil {
		t.Fatalf("GenerateReply error after generation start = %v", err)
	}
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	if created.Generation == nil {
		t.Fatal("Generation = nil, want reference generation event")
	}
}

func TestAWSRealtimeSessionGenerateReplyWithoutPendingTimesOutLikeReferenceFuture(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModelWithNovaSonic2(
		WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}),
		WithAWSRealtimeGenerateReplyTimeout(time.Millisecond),
	)
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
	waitAWSRealtimeAudioContentStart(t, stream, 0)
	sentCount := len(stream.sent)

	err = session.GenerateReply(llm.RealtimeGenerateReplyOptions{})

	var realtimeErr llm.RealtimeError
	if !errors.As(err, &realtimeErr) {
		t.Fatalf("GenerateReply error = %T %v, want RealtimeError", err, err)
	}
	if !strings.Contains(err.Error(), "generate_reply timed out waiting for generation") {
		t.Fatalf("GenerateReply error = %v, want generation timeout", err)
	}
	if len(stream.sent) != sentCount {
		t.Fatalf("GenerateReply sent %d provider events, want none without pending generation", len(stream.sent)-sentCount)
	}
}

func TestAWSRealtimeSessionCloseRejectsPendingReferenceGenerateReply(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModelWithNovaSonic2(
		WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}),
		WithAWSRealtimeGenerateReplyTimeout(time.Second),
	)
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	waitAWSRealtimeAudioContentStart(t, stream, 0)

	done := make(chan error, 1)
	go func() {
		done <- session.GenerateReply(llm.RealtimeGenerateReplyOptions{Instructions: "ask for the card number"})
	}()
	waitAWSRealtimeTextInput(t, stream, "ask for the card number")

	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	select {
	case err := <-done:
		var realtimeErr llm.RealtimeError
		if !errors.As(err, &realtimeErr) {
			t.Fatalf("GenerateReply error = %T %v, want RealtimeError", err, err)
		}
		if !strings.Contains(err.Error(), "Session closed while waiting for generation") {
			t.Fatalf("GenerateReply error = %v, want session-closed context", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("GenerateReply did not unblock after Close")
	}
}

func TestAWSRealtimeSessionCloseCleansReferenceStreamAfterSendFailure(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModelWithNovaSonic2(WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	waitAWSRealtimeAudioContentStart(t, stream, 0)

	stream.sendErr = errors.New("prompt end send failed")
	err = session.Close()
	if err == nil {
		t.Fatal("Close error = nil, want prompt-end send failure")
	}
	if !stream.closed {
		t.Fatal("provider stream closed = false, want Close to release stream after prompt-end send failure")
	}
	select {
	case _, ok := <-session.EventCh():
		if ok {
			t.Fatal("event channel still open after failed Close cleanup")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("event channel did not close after failed Close cleanup")
	}
}

func TestAWSRealtimeSessionRecycleClosesReferenceStreamAfterSendFailure(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModelWithNovaSonic2(WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	waitAWSRealtimeAudioContentStart(t, stream, 0)
	awsSession := session.(*awsRealtimeSession)

	stream.sendErr = errors.New("prompt end send failed")
	err = awsSession.recycleForUpdatedTools(context.Background())
	if err == nil {
		t.Fatal("recycleForUpdatedTools error = nil, want prompt-end send failure")
	}
	if !stream.closed {
		t.Fatal("provider stream closed = false, want recycle to release old stream after prompt-end send failure")
	}
}

func TestAWSRealtimeSessionGenerateReplyAudioOnlyEmitsReferenceEmptyGeneration(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModelWithNovaSonic1(WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
	waitAWSRealtimeAudioContentStart(t, stream, 0)
	sentCount := len(stream.sent)

	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{Instructions: "not supported"}); err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}

	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	if created.Generation == nil {
		t.Fatal("Generation = nil, want empty generation")
	}
	if !created.Generation.UserInitiated {
		t.Fatal("UserInitiated = false, want true for GenerateReply")
	}
	select {
	case _, ok := <-created.Generation.MessageCh:
		if ok {
			t.Fatal("MessageCh yielded message, want closed empty stream")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for empty MessageCh close")
	}
	select {
	case _, ok := <-created.Generation.FunctionCh:
		if ok {
			t.Fatal("FunctionCh yielded call, want closed empty stream")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for empty FunctionCh close")
	}
	if len(stream.sent) != sentCount {
		t.Fatalf("GenerateReply sent %d provider events, want no-op provider send for audio-only model", len(stream.sent)-sentCount)
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

func assertAWSRealtimeEventEventually(t *testing.T, ch <-chan llm.RealtimeEvent, want llm.RealtimeEventType) llm.RealtimeEvent {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case event := <-ch:
			if event.Type == want {
				return event
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", want)
			return llm.RealtimeEvent{}
		}
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
	input   *bedrockruntime.InvokeModelWithBidirectionalStreamInput
	stream  awsRealtimeStream
	streams []awsRealtimeStream
	err     error
}

func (c *fakeAWSRealtimeClient) InvokeModelWithBidirectionalStream(ctx context.Context, input *bedrockruntime.InvokeModelWithBidirectionalStreamInput) (awsRealtimeStream, error) {
	c.input = input
	if c.err != nil {
		return nil, c.err
	}
	if len(c.streams) > 0 {
		stream := c.streams[0]
		c.streams = c.streams[1:]
		return stream, nil
	}
	return c.stream, nil
}

type fakeAWSRealtimeStream struct {
	mu                    sync.Mutex
	sent                  []string
	sentAt                []time.Time
	closed                bool
	sendErr               error
	err                   error
	events                chan awstypes.InvokeModelWithBidirectionalStreamOutput
	eventsStarted         atomic.Bool
	audioSentBeforeEvents atomic.Bool
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

func awsRealtimeTestMonoFrame(sampleRate uint32, samples []int16) *model.AudioFrame {
	data := make([]byte, len(samples)*2)
	for i, sample := range samples {
		binary.LittleEndian.PutUint16(data[i*2:i*2+2], uint16(sample))
	}
	return &model.AudioFrame{
		Data:              data,
		SampleRate:        sampleRate,
		NumChannels:       1,
		SamplesPerChannel: uint32(len(samples)),
	}
}

func countAWSRealtimeAudioInputs(t *testing.T, events []string) int {
	t.Helper()
	return len(collectAWSRealtimeAudioInputPayloads(t, events))
}

func collectAWSRealtimeAudioInputPayloads(t *testing.T, events []string) []string {
	t.Helper()
	var payloads []string
	for _, raw := range events {
		event := mustAWSRealtimeJSONEvent(t, raw)
		content := awsRealtimeNestedString(event, "event", "audioInput", "content")
		if content != "" {
			payloads = append(payloads, content)
		}
	}
	return payloads
}

func waitAWSRealtimeAudioInputPayloads(t *testing.T, stream *fakeAWSRealtimeStream, start int, want int) []string {
	t.Helper()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		sent := stream.snapshotSent()
		payloads := collectAWSRealtimeAudioInputPayloads(t, sent[start:])
		if len(payloads) >= want {
			return payloads
		}
		select {
		case <-deadline:
			t.Fatalf("audioInput events = %d, want at least %d", len(payloads), want)
		case <-ticker.C:
		}
	}
}

func waitAWSRealtimeAudioContentStart(t *testing.T, stream *fakeAWSRealtimeStream, start int) map[string]any {
	t.Helper()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		for _, raw := range stream.snapshotSent()[start:] {
			event := mustAWSRealtimeJSONEvent(t, raw)
			if awsRealtimeNestedString(event, "event", "contentStart", "type") == "AUDIO" {
				return event
			}
		}
		select {
		case <-deadline:
			t.Fatal("audio contentStart not sent")
		case <-ticker.C:
		}
	}
}

func waitAWSRealtimeTextInput(t *testing.T, stream *fakeAWSRealtimeStream, content string) {
	t.Helper()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		sent := stream.snapshotSent()
		for _, raw := range sent {
			event := mustAWSRealtimeJSONEvent(t, raw)
			if awsRealtimeNestedString(event, "event", "textInput", "content") == content {
				return
			}
		}
		select {
		case <-deadline:
			t.Fatalf("text input %q not sent", content)
		case <-ticker.C:
		}
	}
}

func awsRealtimeSentTextInputContents(t *testing.T, events []string) []string {
	t.Helper()
	var contents []string
	for _, raw := range events {
		event := mustAWSRealtimeJSONEvent(t, raw)
		content := awsRealtimeNestedString(event, "event", "textInput", "content")
		if content != "" {
			contents = append(contents, content)
		}
	}
	return contents
}

func awsRealtimeAudioSenderGoroutines() int {
	size := 64 * 1024
	for {
		buf := make([]byte, size)
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return strings.Count(string(buf[:n]), "github.com/cavos-io/rtp-agent/adapter/aws.(*awsRealtimeSession).runAudioInputSender")
		}
		size *= 2
	}
}

func awsRealtimeAudioAndInteractiveTextStartTimes(t *testing.T, stream *fakeAWSRealtimeStream) (time.Time, time.Time) {
	t.Helper()
	var audioAt, textAt time.Time
	for i, raw := range stream.sent {
		if i >= len(stream.sentAt) {
			t.Fatalf("sent timestamp missing for event %d", i)
		}
		event := mustAWSRealtimeJSONEvent(t, raw)
		if awsRealtimeNestedString(event, "event", "contentStart", "type") == "AUDIO" {
			audioAt = stream.sentAt[i]
			continue
		}
		body, _ := event["event"].(map[string]any)
		start, _ := body["contentStart"].(map[string]any)
		if start["type"] == "TEXT" && start["interactive"] == true {
			textAt = stream.sentAt[i]
			break
		}
	}
	if audioAt.IsZero() || textAt.IsZero() {
		t.Fatalf("audio/text start times missing, sent = %v", stream.sent)
	}
	return audioAt, textAt
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
	s.mu.Lock()
	sendErr := s.sendErr
	s.mu.Unlock()
	if sendErr != nil {
		return sendErr
	}
	chunk, ok := event.(*awstypes.InvokeModelWithBidirectionalStreamInputMemberChunk)
	if !ok {
		return nil
	}
	var decoded map[string]any
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := json.Unmarshal(chunk.Value.Bytes, &decoded); err == nil {
		encoded, _ := json.Marshal(decoded)
		s.sent = append(s.sent, string(encoded))
		s.sentAt = append(s.sentAt, time.Now())
		if awsRealtimeNestedString(decoded, "event", "contentStart", "type") == "AUDIO" && !s.eventsStarted.Load() {
			s.audioSentBeforeEvents.Store(true)
		}
		return nil
	}
	s.sent = append(s.sent, string(chunk.Value.Bytes))
	s.sentAt = append(s.sentAt, time.Now())
	return nil
}

func (s *fakeAWSRealtimeStream) setSendErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendErr = err
}

func (s *fakeAWSRealtimeStream) snapshotSent() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sent...)
}

func (s *fakeAWSRealtimeStream) Events() <-chan awstypes.InvokeModelWithBidirectionalStreamOutput {
	s.eventsStarted.Store(true)
	if s.events == nil {
		s.events = make(chan awstypes.InvokeModelWithBidirectionalStreamOutput)
		close(s.events)
	}
	return s.events
}

func (s *fakeAWSRealtimeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *fakeAWSRealtimeStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *fakeAWSRealtimeStream) Err() error {
	return s.err
}

type blockingAWSRealtimeStream struct {
	*fakeAWSRealtimeStream
	blockSend atomic.Bool
}

func (s *blockingAWSRealtimeStream) Send(ctx context.Context, event awstypes.InvokeModelWithBidirectionalStreamInput) error {
	if !s.isBlocked() {
		return s.fakeAWSRealtimeStream.Send(ctx, event)
	}
	<-ctx.Done()
	return ctx.Err()
}

func (s *blockingAWSRealtimeStream) setBlocked(block bool) {
	s.blockSend.Store(block)
}

func (s *blockingAWSRealtimeStream) isBlocked() bool {
	return s.blockSend.Load()
}

type blockingAudioInputAWSRealtimeStream struct {
	*fakeAWSRealtimeStream
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingAudioInputAWSRealtimeStream) Send(ctx context.Context, event awstypes.InvokeModelWithBidirectionalStreamInput) error {
	chunk, ok := event.(*awstypes.InvokeModelWithBidirectionalStreamInputMemberChunk)
	if ok {
		var decoded map[string]any
		if err := json.Unmarshal(chunk.Value.Bytes, &decoded); err == nil && awsRealtimeNestedString(decoded, "event", "audioInput", "content") != "" {
			s.once.Do(func() { close(s.started) })
			select {
			case <-s.release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return s.fakeAWSRealtimeStream.Send(ctx, event)
}

type blockingAudioContentStartAWSRealtimeStream struct {
	*fakeAWSRealtimeStream
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingAudioContentStartAWSRealtimeStream) Send(ctx context.Context, event awstypes.InvokeModelWithBidirectionalStreamInput) error {
	chunk, ok := event.(*awstypes.InvokeModelWithBidirectionalStreamInputMemberChunk)
	if ok {
		var decoded map[string]any
		if err := json.Unmarshal(chunk.Value.Bytes, &decoded); err == nil && awsRealtimeNestedString(decoded, "event", "contentStart", "type") == "AUDIO" {
			s.once.Do(func() { close(s.started) })
			select {
			case <-s.release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return s.fakeAWSRealtimeStream.Send(ctx, event)
}

type awsSecondRequestTestTool struct{}

func (awsSecondRequestTestTool) ID() string          { return "lookup_order" }
func (awsSecondRequestTestTool) Name() string        { return "lookup_order" }
func (awsSecondRequestTestTool) Description() string { return "look up orders" }
func (awsSecondRequestTestTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"order_id": map[string]any{"type": "string"}},
	}
}
func (awsSecondRequestTestTool) Execute(context.Context, string) (string, error) {
	return `{"ok":true}`, nil
}
