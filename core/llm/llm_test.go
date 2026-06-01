package llm

import (
	"testing"

	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/library/utils/images"
)

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
	ev := RealtimeEvent{
		Type: RealtimeEventTypeGenerationCreated,
		Generation: &GenerationCreatedEvent{
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
