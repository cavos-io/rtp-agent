package aws

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
	stream.emitJSON(`{"event":{"audioOutput":{"contentId":"audio-1","content":"` + base64.StdEncoding.EncodeToString(audioBytes) + `"}}}`)
	audio := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeAudio)
	if string(audio.Data) != string(audioBytes) {
		t.Fatalf("audio data = %v, want %v", audio.Data, audioBytes)
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

	stream.emitJSON(`{"event":{"toolUse":{"toolUseId":"tool-1","toolName":"lookup","content":"{\"query\":\"weather\"}"}}}`)

	event := assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeFunctionCall)
	if event.Function == nil {
		t.Fatal("Function = nil, want tool call")
	}
	if event.Function.CallID != "tool-1" || event.Function.Name != "lookup" {
		t.Fatalf("function call = %#v, want lookup tool-1", event.Function)
	}
	if event.Function.Arguments != `{"query":"weather"}` {
		t.Fatalf("arguments = %q, want reference tool content", event.Function.Arguments)
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

	stream.emitJSON(`{"event":{"toolUse":{"toolUseId":"tool-1","toolName":"lookup","content":"{\"query\":\"weather\"}"}}}`)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeFunctionCall)

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

func TestAWSRealtimeSessionWrapsReferenceToolErrorResult(t *testing.T) {
	stream := newFakeAWSRealtimeStream()
	provider := NewAWSRealtimeModel("", WithAWSRealtimeClient(&fakeAWSRealtimeClient{stream: stream}))
	session, err := provider.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	stream.emitJSON(`{"event":{"toolUse":{"toolUseId":"tool-err","toolName":"lookup","content":"{}"}}}`)
	assertAWSRealtimeEvent(t, session.EventCh(), llm.RealtimeEventTypeFunctionCall)

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
	sent   []string
	closed bool
	events chan awstypes.InvokeModelWithBidirectionalStreamOutput
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
