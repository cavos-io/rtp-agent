package llm

import (
	"context"
	"io"
	"testing"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/library/utils/images"
)

func TestLLMMetadataDefaults(t *testing.T) {
	provider := &metadataTestLLM{}

	if got := Label(provider); got != "llm.metadataTestLLM" {
		t.Fatalf("Label() = %q, want llm.metadataTestLLM", got)
	}
	if got := Model(provider); got != "unknown" {
		t.Fatalf("Model() = %q, want unknown", got)
	}
	if got := Provider(provider); got != "unknown" {
		t.Fatalf("Provider() = %q, want unknown", got)
	}
}

func TestLLMMetadataUsesProviderOverrides(t *testing.T) {
	provider := &metadataTestLLM{
		label:    "test.LLM",
		model:    "model-a",
		provider: "provider-a",
	}

	if got := Label(provider); got != "test.LLM" {
		t.Fatalf("Label() = %q, want provider label", got)
	}
	if got := Model(provider); got != "model-a" {
		t.Fatalf("Model() = %q, want model-a", got)
	}
	if got := Provider(provider); got != "provider-a" {
		t.Fatalf("Provider() = %q, want provider-a", got)
	}
}

func TestLLMPrewarmDelegatesWhenSupported(t *testing.T) {
	provider := &metadataTestLLM{}

	Prewarm(provider)

	if !provider.prewarmed {
		t.Fatal("Prewarm() did not call provider Prewarm")
	}
}

func TestRealtimeModelMetadataDefaults(t *testing.T) {
	model := &metadataRealtimeModel{}

	if got := RealtimeLabel(model); got != "llm.metadataRealtimeModel" {
		t.Fatalf("RealtimeLabel() = %q, want llm.metadataRealtimeModel", got)
	}
	if got := RealtimeModelName(model); got != "unknown" {
		t.Fatalf("RealtimeModelName() = %q, want unknown", got)
	}
	if got := RealtimeProvider(model); got != "unknown" {
		t.Fatalf("RealtimeProvider() = %q, want unknown", got)
	}
}

func TestRealtimeModelMetadataUsesProviderOverrides(t *testing.T) {
	model := &metadataRealtimeModel{
		label:    "test.RealtimeModel",
		model:    "realtime-a",
		provider: "provider-a",
	}

	if got := RealtimeLabel(model); got != "test.RealtimeModel" {
		t.Fatalf("RealtimeLabel() = %q, want provider label", got)
	}
	if got := RealtimeModelName(model); got != "realtime-a" {
		t.Fatalf("RealtimeModelName() = %q, want realtime-a", got)
	}
	if got := RealtimeProvider(model); got != "provider-a" {
		t.Fatalf("RealtimeProvider() = %q, want provider-a", got)
	}
}

func TestRealtimeCapabilitiesExposeReferenceFlags(t *testing.T) {
	capabilities := RealtimeCapabilities{
		MessageTruncation:       true,
		TurnDetection:           true,
		UserTranscription:       true,
		AutoToolReplyGeneration: true,
		AudioOutput:             true,
		ManualFunctionCalls:     true,
		MutableChatContext:      true,
		MutableInstructions:     true,
		MutableTools:            true,
		PerResponseToolChoice:   true,
		SupportsSay:             true,
	}

	if !capabilities.ManualFunctionCalls || !capabilities.MutableChatContext || !capabilities.MutableInstructions || !capabilities.MutableTools || !capabilities.PerResponseToolChoice || !capabilities.SupportsSay {
		t.Fatalf("capabilities missing reference flags: %#v", capabilities)
	}
}

func TestRealtimeSessionOptionsExposeToolChoice(t *testing.T) {
	options := RealtimeSessionOptions{
		ToolChoice: map[string]any{
			"type":     "function",
			"function": map[string]any{"name": "lookup"},
		},
	}

	if options.ToolChoice == nil {
		t.Fatal("ToolChoice = nil, want named tool choice")
	}
}

func TestRealtimeGenerateReplyOptionsExposePerResponseOverrides(t *testing.T) {
	options := RealtimeGenerateReplyOptions{
		Instructions: "answer briefly",
		ToolChoice:   "none",
		Tools:        []Tool{},
	}

	if options.Instructions != "answer briefly" {
		t.Fatalf("Instructions = %q, want answer briefly", options.Instructions)
	}
	if options.ToolChoice == nil {
		t.Fatal("ToolChoice = nil, want per-response override")
	}
	if options.Tools == nil {
		t.Fatal("Tools = nil, want explicit per-response tools")
	}
}

func TestRealtimeTruncateOptionsExposeAudioTruncationFields(t *testing.T) {
	transcript := "spoken text"
	options := RealtimeTruncateOptions{
		MessageID:       "msg_123",
		Modalities:      []string{"audio"},
		AudioEndMillis:  1500,
		AudioTranscript: &transcript,
	}

	if options.MessageID != "msg_123" {
		t.Fatalf("MessageID = %q, want msg_123", options.MessageID)
	}
	if len(options.Modalities) != 1 || options.Modalities[0] != "audio" {
		t.Fatalf("Modalities = %#v, want audio", options.Modalities)
	}
	if options.AudioEndMillis != 1500 {
		t.Fatalf("AudioEndMillis = %d, want 1500", options.AudioEndMillis)
	}
	if options.AudioTranscript == nil || *options.AudioTranscript != "spoken text" {
		t.Fatalf("AudioTranscript = %#v, want spoken text", options.AudioTranscript)
	}
}

func TestRealtimeSessionCanAcceptVideoFrames(t *testing.T) {
	var _ interface {
		PushVideo(*images.VideoFrame) error
	} = (RealtimeSession)(nil)

	var frame *images.VideoFrame
	_ = frame
}

func TestRealtimeEventCanCarryInputAudioTranscription(t *testing.T) {
	confidence := 0.91
	ev := RealtimeEvent{
		Type: RealtimeEventTypeInputAudioTranscriptionCompleted,
		InputTranscription: &InputTranscriptionCompleted{
			ItemID:       "item_123",
			ContentIndex: 2,
			Transcript:   "hello",
			IsFinal:      true,
			Confidence:   &confidence,
		},
	}

	if ev.InputTranscription == nil {
		t.Fatal("InputTranscription = nil, want transcription payload")
	}
	if ev.InputTranscription.ItemID != "item_123" || ev.InputTranscription.Transcript != "hello" || !ev.InputTranscription.IsFinal {
		t.Fatalf("InputTranscription = %#v, want final item transcript", ev.InputTranscription)
	}
	if ev.InputTranscription.ContentIndex != 2 {
		t.Fatalf("ContentIndex = %d, want 2", ev.InputTranscription.ContentIndex)
	}
	if ev.InputTranscription.Confidence == nil || *ev.InputTranscription.Confidence != confidence {
		t.Fatalf("Confidence = %#v, want %.2f", ev.InputTranscription.Confidence, confidence)
	}
}

func TestRealtimeEventCanCarryInputSpeechStoppedPayload(t *testing.T) {
	ev := RealtimeEvent{
		Type: RealtimeEventTypeSpeechStopped,
		SpeechStopped: &InputSpeechStoppedEvent{
			UserTranscriptionEnabled: true,
		},
	}

	if ev.SpeechStopped == nil {
		t.Fatal("SpeechStopped = nil, want speech stopped payload")
	}
	if !ev.SpeechStopped.UserTranscriptionEnabled {
		t.Fatal("UserTranscriptionEnabled = false, want true")
	}
}

func TestRealtimeEventCanCarryOutputItemMetadata(t *testing.T) {
	ev := RealtimeEvent{
		Type:         RealtimeEventTypeText,
		ItemID:       "msg_123",
		ContentIndex: 2,
		Text:         "hello",
	}

	if ev.ItemID != "msg_123" {
		t.Fatalf("ItemID = %q, want msg_123", ev.ItemID)
	}
	if ev.ContentIndex != 2 {
		t.Fatalf("ContentIndex = %d, want 2", ev.ContentIndex)
	}
}

func TestRealtimeEventCanCarryGenerationCreated(t *testing.T) {
	messageCh := make(chan MessageGeneration, 1)
	functionCh := make(chan *FunctionCall, 1)
	textCh := make(chan string, 1)
	audioCh := make(chan *model.AudioFrame, 1)
	modalitiesCh := make(chan []string, 1)
	messageCh <- MessageGeneration{
		MessageID:    "msg_123",
		TextCh:       textCh,
		AudioCh:      audioCh,
		ModalitiesCh: modalitiesCh,
	}
	ev := RealtimeEvent{
		Type: RealtimeEventTypeGenerationCreated,
		Generation: &GenerationCreatedEvent{
			MessageCh:     messageCh,
			FunctionCh:    functionCh,
			ResponseID:    "resp_123",
			UserInitiated: true,
		},
	}

	if ev.Generation == nil {
		t.Fatal("Generation = nil, want generation-created payload")
	}
	if ev.Generation.ResponseID != "resp_123" || !ev.Generation.UserInitiated {
		t.Fatalf("Generation = %#v, want user-initiated response", ev.Generation)
	}
	msg := <-ev.Generation.MessageCh
	if msg.MessageID != "msg_123" || msg.TextCh != (<-chan string)(textCh) || msg.AudioCh != (<-chan *model.AudioFrame)(audioCh) || msg.ModalitiesCh != (<-chan []string)(modalitiesCh) {
		t.Fatalf("MessageGeneration = %#v, want stream channels", msg)
	}
	if ev.Generation.FunctionCh != (<-chan *FunctionCall)(functionCh) {
		t.Fatalf("FunctionCh = %#v, want provided function stream", ev.Generation.FunctionCh)
	}
}

func TestRealtimeEventCanCarryRemoteItemAdded(t *testing.T) {
	item := &ChatMessage{
		ID:      "msg_123",
		Role:    ChatRoleUser,
		Content: []ChatContent{{Text: "hello"}},
	}
	ev := RealtimeEvent{
		Type: RealtimeEventTypeRemoteItemAdded,
		RemoteItem: &RemoteItemAddedEvent{
			PreviousItemID: "prev_123",
			Item:           item,
		},
	}

	if ev.RemoteItem == nil {
		t.Fatal("RemoteItem = nil, want remote item payload")
	}
	if ev.RemoteItem.PreviousItemID != "prev_123" || ev.RemoteItem.Item.GetID() != "msg_123" {
		t.Fatalf("RemoteItem = %#v, want previous id and chat item", ev.RemoteItem)
	}
}

func TestRealtimeEventCanCarrySessionReconnected(t *testing.T) {
	ev := RealtimeEvent{
		Type:      RealtimeEventTypeSessionReconnected,
		Reconnect: &RealtimeSessionReconnectedEvent{},
	}

	if ev.Type != "session_reconnected" {
		t.Fatalf("Type = %q, want session_reconnected", ev.Type)
	}
	if ev.Reconnect == nil {
		t.Fatal("Reconnect = nil, want session reconnected payload")
	}
}

func TestRealtimeEventCanCarryMetricsCollected(t *testing.T) {
	ev := RealtimeEvent{
		Type: RealtimeEventTypeMetricsCollected,
		Metrics: &telemetry.RealtimeModelMetrics{
			RequestID:    "resp_123",
			InputTokens:  11,
			OutputTokens: 7,
			TotalTokens:  18,
		},
	}

	if ev.Metrics == nil {
		t.Fatal("Metrics = nil, want realtime metrics payload")
	}
	if ev.Metrics.RequestID != "resp_123" || ev.Metrics.InputTokens != 11 || ev.Metrics.OutputTokens != 7 || ev.Metrics.TotalTokens != 18 {
		t.Fatalf("Metrics = %#v, want realtime token usage", ev.Metrics)
	}
}

type metadataTestLLM struct {
	label     string
	model     string
	provider  string
	prewarmed bool
}

func (m *metadataTestLLM) Chat(context.Context, *ChatContext, ...ChatOption) (LLMStream, error) {
	return &metadataTestStream{}, nil
}

func (m *metadataTestLLM) Label() string {
	return m.label
}

func (m *metadataTestLLM) Model() string {
	return m.model
}

func (m *metadataTestLLM) Provider() string {
	return m.provider
}

func (m *metadataTestLLM) Prewarm() {
	m.prewarmed = true
}

type metadataTestStream struct{}

func (m *metadataTestStream) Next() (*ChatChunk, error) {
	return nil, io.EOF
}

func (m *metadataTestStream) Close() error {
	return nil
}

type metadataRealtimeModel struct {
	label    string
	model    string
	provider string
}

func (m *metadataRealtimeModel) Capabilities() RealtimeCapabilities {
	return RealtimeCapabilities{}
}

func (m *metadataRealtimeModel) Session() (RealtimeSession, error) {
	return nil, nil
}

func (m *metadataRealtimeModel) Close() error {
	return nil
}

func (m *metadataRealtimeModel) Label() string {
	return m.label
}

func (m *metadataRealtimeModel) Model() string {
	return m.model
}

func (m *metadataRealtimeModel) Provider() string {
	return m.provider
}
