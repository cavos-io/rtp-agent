package aws

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
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

func TestAWSRealtimeSessionUsesReferenceDefaultSystemPrompt(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))

	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	prompt := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, stream.sent[3]), "event", "textInput", "content")
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

	sessionStart := mustAWSRealtimeJSONEvent(t, stream.sent[0])
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

	closeEvents := first.sent[len(first.sent)-3:]
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, closeEvents[0]), "event", "contentEnd", "contentName"); got == "" {
		t.Fatalf("recycle contentEnd contentName empty")
	}
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, closeEvents[1]), "event", "promptEnd", "promptName"); got == "" {
		t.Fatalf("recycle promptEnd promptName empty")
	}
	if _, ok := nestedMap(t, mustAWSRealtimeJSONEvent(t, closeEvents[2]), "event")["sessionEnd"].(map[string]any); !ok {
		t.Fatalf("recycle sessionEnd event = %s", closeEvents[2])
	}
	if !first.closed {
		t.Fatal("first stream closed = false, want true after active tool update recycle")
	}
	if len(second.sent) == 0 {
		t.Fatal("second stream sent no events, want restarted prompt with new tools")
	}
	toolConfig := nestedMap(t, mustAWSRealtimeJSONEvent(t, second.sent[1]), "event", "promptStart", "toolConfiguration")
	tools, ok := toolConfig["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("recycled tools = %#v, want one tool", toolConfig["tools"])
	}
	spec := nestedMap(t, map[string]any{"tool": tools[0]}, "tool", "toolSpec")
	if spec["name"] != "lookup_order" {
		t.Fatalf("recycled tool name = %#v, want lookup_order", spec["name"])
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

	sentBeforeAudio := len(first.sent)
	if err := session.PushAudio(awsRealtimeTestMonoFrame(16000, make([]int16, 256))); err != nil {
		t.Fatalf("PushAudio first tail error = %v", err)
	}
	if got := countAWSRealtimeAudioInputs(t, first.sent[sentBeforeAudio:]); got != 0 {
		t.Fatalf("audioInput events before recycle = %d, want none for buffered tail", got)
	}

	if err := session.UpdateTools([]llm.Tool{awsSecondRequestTestTool{}}); err != nil {
		t.Fatalf("UpdateTools active error = %v", err)
	}
	if got := countAWSRealtimeAudioInputs(t, first.sent[sentBeforeAudio:]); got != 0 {
		t.Fatalf("old stream audioInput events during recycle = %d, want buffered tail kept", got)
	}

	sentSecondBeforeAudio := len(second.sent)
	if err := session.PushAudio(awsRealtimeTestMonoFrame(16000, make([]int16, 256))); err != nil {
		t.Fatalf("PushAudio second tail error = %v", err)
	}
	audioInputs := collectAWSRealtimeAudioInputPayloads(t, second.sent[sentSecondBeforeAudio:])
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
	audioStart := mustAWSRealtimeJSONEvent(t, stream.sent[11])
	if got := awsRealtimeNestedString(audioStart, "event", "contentStart", "type"); got != "AUDIO" {
		t.Fatalf("event[11] type = %q, want AUDIO", got)
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

	historyTexts := awsRealtimeSentTextInputContents(t, stream.sent)
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

	frame := awsRealtimeTestMonoFrame(16000, make([]int16, 512))
	if err := session.PushAudio(frame); err != nil {
		t.Fatalf("PushAudio error = %v", err)
	}
	audioInput := mustAWSRealtimeJSONEvent(t, stream.sent[len(stream.sent)-1])
	if got := awsRealtimeNestedString(audioInput, "event", "audioInput", "content"); got != base64.StdEncoding.EncodeToString(frame.Data) {
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

func TestAWSRealtimeSessionPushAudioSendErrorReturnsAPIConnectionError(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
	stream.sendErr = errors.New("bedrock send failed")

	err = session.PushAudio(awsRealtimeTestMonoFrame(16000, make([]int16, 512)))

	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("PushAudio error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(err.Error(), "AWS Nova Sonic realtime send failed") {
		t.Fatalf("PushAudio error = %q, want realtime send context", err.Error())
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

	samples := make([][2]int16, 1536)
	for i := range samples {
		samples[i] = [2]int16{10, 30}
	}
	frame := awsRealtimeTestStereoFrame(48000, samples)
	if err := session.PushAudio(frame); err != nil {
		t.Fatalf("PushAudio error = %v", err)
	}

	audioInput := mustAWSRealtimeJSONEvent(t, stream.sent[len(stream.sent)-1])
	content := awsRealtimeNestedString(audioInput, "event", "audioInput", "content")
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

	sentCount := len(stream.sent)
	if err := session.PushAudio(awsRealtimeTestMonoFrame(16000, make([]int16, 256))); err != nil {
		t.Fatalf("PushAudio first error = %v", err)
	}
	if got := countAWSRealtimeAudioInputs(t, stream.sent[sentCount:]); got != 0 {
		t.Fatalf("audioInput events after partial frame = %d, want 0", got)
	}

	if err := session.PushAudio(awsRealtimeTestMonoFrame(16000, make([]int16, 256))); err != nil {
		t.Fatalf("PushAudio second error = %v", err)
	}
	audioInputs := collectAWSRealtimeAudioInputPayloads(t, stream.sent[sentCount:])
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

	sentCount := len(stream.sent)
	if err := session.PushAudio(awsRealtimeTestMonoFrame(16000, make([]int16, 256))); err != nil {
		t.Fatalf("PushAudio error = %v", err)
	}
	if got := countAWSRealtimeAudioInputs(t, stream.sent[sentCount:]); got != 0 {
		t.Fatalf("audioInput events before Close = %d, want none until chunk flush", got)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	if got := countAWSRealtimeAudioInputs(t, stream.sent[sentCount:]); got != 0 {
		t.Fatalf("audioInput events after Close = %d, want buffered tail dropped", got)
	}
	audioIndex := -1
	contentEndIndex := -1
	for i, raw := range stream.sent[sentCount:] {
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

	sentCount := len(stream.sent)
	if err := session.PushAudio(awsRealtimeTestMonoFrame(16000, make([]int16, 512))); err != nil {
		t.Fatalf("PushAudio complete chunk error = %v", err)
	}
	if err := session.PushAudio(awsRealtimeTestMonoFrame(16000, make([]int16, 256))); err != nil {
		t.Fatalf("PushAudio tail error = %v", err)
	}
	audioInputs := collectAWSRealtimeAudioInputPayloads(t, stream.sent[sentCount:])
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
	audioInputs = collectAWSRealtimeAudioInputPayloads(t, stream.sent[sentCount:])
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

	sentCount := len(stream.sent)
	if err := session.PushAudio(awsRealtimeTestMonoFrame(16000, make([]int16, 256))); err != nil {
		t.Fatalf("PushAudio error = %v", err)
	}
	if err := session.ClearAudio(); err != nil {
		t.Fatalf("ClearAudio error = %v", err)
	}
	if got := countAWSRealtimeAudioInputs(t, stream.sent[sentCount:]); got != 0 {
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

	sentCount := len(stream.sent)
	if err := session.PushAudio(awsRealtimeTestMonoFrame(16000, make([]int16, 256))); err != nil {
		t.Fatalf("PushAudio error = %v", err)
	}
	if err := session.CommitAudio(); err != nil {
		t.Fatalf("CommitAudio error = %v", err)
	}

	audioInputs := collectAWSRealtimeAudioInputPayloads(t, stream.sent[sentCount:])
	if len(audioInputs) != 0 {
		t.Fatalf("audioInput events after CommitAudio = %d, want no-op", len(audioInputs))
	}
	if err := session.CommitAudio(); err != nil {
		t.Fatalf("second CommitAudio error = %v", err)
	}
	if got := countAWSRealtimeAudioInputs(t, stream.sent[sentCount:]); got != 0 {
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

func TestAWSRealtimeSessionPushAudioPreservesResampleDurationAcrossFrames(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	sentCount := len(stream.sent)
	for range 180 {
		if err := session.PushAudio(awsRealtimeTestMonoFrame(44100, make([]int16, 100))); err != nil {
			t.Fatalf("PushAudio error = %v", err)
		}
	}
	audioInputs := collectAWSRealtimeAudioInputPayloads(t, stream.sent[sentCount:])
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
	var connectionErr *llm.APIConnectionError
	if !errors.As(event.Error, &connectionErr) {
		t.Fatalf("Error = %T %v, want APIConnectionError", event.Error, event.Error)
	}
	if !strings.Contains(event.Error.Error(), "AWS Nova Sonic realtime stream failed") {
		t.Fatalf("Error = %q, want stream failure context", event.Error.Error())
	}
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
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeSpeechStopped)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)
	created := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
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

	sentBeforeUpdate := len(first.sent)
	if err := session.UpdateTools([]llm.Tool{awsSecondRequestTestTool{}}); err != nil {
		t.Fatalf("UpdateTools active error = %v", err)
	}
	if first.closed {
		t.Fatal("first stream closed = true, want deferred recycle while tool result pending")
	}
	if len(second.sent) != 0 {
		t.Fatalf("second stream sent %d events, want no restart before pending tool result", len(second.sent))
	}
	if len(first.sent) != sentBeforeUpdate {
		t.Fatalf("UpdateTools sent %d events before tool result, want none", len(first.sent)-sentBeforeUpdate)
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

	toolResult := mustAWSRealtimeJSONEvent(t, first.sent[sentBeforeUpdate+1])
	if got := awsRealtimeNestedString(toolResult, "event", "toolResult", "content"); got != `{"forecast":"sunny"}` {
		t.Fatalf("tool result content = %q, want output before recycle", got)
	}
	if !first.closed {
		t.Fatal("first stream closed = false, want recycle after pending tool result")
	}
	if len(second.sent) == 0 {
		t.Fatal("second stream sent no events, want restarted prompt after tool result")
	}
	toolConfig := nestedMap(t, mustAWSRealtimeJSONEvent(t, second.sent[1]), "event", "promptStart", "toolConfiguration")
	tools, ok := toolConfig["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("recycled tools = %#v, want one tool", toolConfig["tools"])
	}
	spec := nestedMap(t, map[string]any{"tool": tools[0]}, "tool", "toolSpec")
	if spec["name"] != "lookup_order" {
		t.Fatalf("recycled tool name = %#v, want lookup_order", spec["name"])
	}
}

func TestAWSRealtimeSessionRetriesToolResultAfterSendFailure(t *testing.T) {
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
	stream.sendErr = nil
	sentCount := len(stream.sent)

	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext retry error = %v", err)
	}
	if len(stream.sent) != sentCount+3 {
		t.Fatalf("retry sent %d events, want 3 tool result events", len(stream.sent)-sentCount)
	}
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, stream.sent[sentCount+1]), "event", "toolResult", "content"); got != `{"ok":true}` {
		t.Fatalf("retry tool result content = %q, want output", got)
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
			"textOutput": map[string]any{"role": "USER", "content": "hello sonic"},
		},
	})
	awsSession.handleResponseEvent(map[string]any{
		"event": map[string]any{
			"contentStart": map[string]any{
				"type":                  "TEXT",
				"role":                  "ASSISTANT",
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
	sentCount := len(stream.sent)
	if err := session.UpdateChatContext(ctx); err != nil {
		t.Fatalf("UpdateChatContext error = %v", err)
	}
	if len(stream.sent) != sentCount {
		t.Fatalf("audio transcript user text sent %d events, want none", len(stream.sent)-sentCount)
	}
}

func TestAWSRealtimeSessionDoesNotReplayUserTextAfterSendFailure(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	ctx := llm.NewChatContext()
	ctx.Append(&llm.ChatMessage{
		ID:      "user-retry",
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "try again"}},
	})

	stream.sendErr = errors.New("bedrock send failed")
	if err := session.UpdateChatContext(ctx); err == nil {
		t.Fatal("UpdateChatContext error = nil, want send failure")
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

func TestAWSRealtimeSessionGenerateReplyAudioOnlyEmitsReferenceEmptyGeneration(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModelWithNovaSonic1(WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()
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
	sent    []string
	closed  bool
	sendErr error
	err     error
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

func (s *fakeAWSRealtimeStream) Err() error {
	return s.err
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
