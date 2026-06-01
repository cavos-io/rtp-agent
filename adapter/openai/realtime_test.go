package openai

import (
	"math"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestRealtimeModelCapabilitiesMatchReference(t *testing.T) {
	model := NewRealtimeModel("test-key", "")

	capabilities := model.Capabilities()

	if !capabilities.MessageTruncation {
		t.Fatal("MessageTruncation = false, want true")
	}
	if !capabilities.ManualFunctionCalls {
		t.Fatal("ManualFunctionCalls = false, want true")
	}
	if !capabilities.MutableChatContext || !capabilities.MutableInstructions || !capabilities.MutableTools {
		t.Fatalf("mutable capabilities = %#v, want chat context, instructions, and tools mutable", capabilities)
	}
	if !capabilities.PerResponseToolChoice {
		t.Fatal("PerResponseToolChoice = false, want true")
	}
}

func TestRealtimeUpdateOptionsMessageMapsNamedToolChoice(t *testing.T) {
	msg := openAIRealtimeUpdateOptionsMessage(llm.RealtimeSessionOptions{
		ToolChoice: map[string]any{
			"type":     "function",
			"function": map[string]any{"name": "lookup"},
		},
	})

	if msg["type"] != "session.update" {
		t.Fatalf("message type = %#v, want session.update", msg["type"])
	}
	session := msg["session"].(map[string]any)
	choice := session["tool_choice"].(map[string]any)
	if choice["type"] != "function" || choice["name"] != "lookup" {
		t.Fatalf("tool_choice = %#v, want realtime named function", choice)
	}
}

func TestRealtimeAudioBufferMessages(t *testing.T) {
	commit := openAIRealtimeCommitAudioMessage()
	if commit["type"] != "input_audio_buffer.commit" {
		t.Fatalf("commit message type = %#v, want input_audio_buffer.commit", commit["type"])
	}

	clear := openAIRealtimeClearAudioMessage()
	if clear["type"] != "input_audio_buffer.clear" {
		t.Fatalf("clear message type = %#v, want input_audio_buffer.clear", clear["type"])
	}
}

func TestRealtimeGenerateReplyMessageMapsPerResponseOptions(t *testing.T) {
	msg := openAIRealtimeGenerateReplyMessage(llm.RealtimeGenerateReplyOptions{
		Instructions: "answer briefly",
		ToolChoice: map[string]any{
			"type":     "function",
			"function": map[string]any{"name": "lookup"},
		},
		Tools: []llm.Tool{requestTestTool{}},
	})

	if msg["type"] != "response.create" {
		t.Fatalf("message type = %#v, want response.create", msg["type"])
	}
	response := msg["response"].(map[string]any)
	if response["instructions"] != "answer briefly" {
		t.Fatalf("instructions = %#v, want answer briefly", response["instructions"])
	}
	choice := response["tool_choice"].(map[string]any)
	if choice["type"] != "function" || choice["name"] != "lookup" {
		t.Fatalf("tool_choice = %#v, want realtime named function", choice)
	}
	tools := response["tools"].([]map[string]any)
	if len(tools) != 1 || tools[0]["name"] != "lookup" {
		t.Fatalf("tools = %#v, want lookup tool", tools)
	}
}

func TestRealtimeTruncateMessageMapsAudioModality(t *testing.T) {
	msg := openAIRealtimeTruncateMessage(llm.RealtimeTruncateOptions{
		MessageID:      "msg_123",
		Modalities:     []string{"text", "audio"},
		AudioEndMillis: 1500,
	})

	if msg["type"] != "conversation.item.truncate" {
		t.Fatalf("message type = %#v, want conversation.item.truncate", msg["type"])
	}
	if msg["item_id"] != "msg_123" {
		t.Fatalf("item_id = %#v, want msg_123", msg["item_id"])
	}
	if msg["content_index"] != 0 {
		t.Fatalf("content_index = %#v, want 0", msg["content_index"])
	}
	if msg["audio_end_ms"] != 1500 {
		t.Fatalf("audio_end_ms = %#v, want 1500", msg["audio_end_ms"])
	}
}

func TestRealtimeVideoMessageMapsImageContent(t *testing.T) {
	msg, err := openAIRealtimeVideoMessage(&llm.ImageContent{
		Image:    "data:image/png;base64,aW1hZ2U=",
		MimeType: "image/png",
	})
	if err != nil {
		t.Fatalf("openAIRealtimeVideoMessage error = %v, want nil", err)
	}

	if msg["type"] != "conversation.item.create" {
		t.Fatalf("message type = %#v, want conversation.item.create", msg["type"])
	}
	item := msg["item"].(map[string]any)
	if item["type"] != "message" || item["role"] != "user" {
		t.Fatalf("item = %#v, want user message", item)
	}
	content := item["content"].([]map[string]any)
	if len(content) != 1 || content[0]["type"] != "input_image" {
		t.Fatalf("content = %#v, want one input_image part", content)
	}
	if content[0]["image_url"] != "data:image/png;base64,aW1hZ2U=" {
		t.Fatalf("image_url = %#v, want data URL", content[0]["image_url"])
	}
}

func TestRealtimeEventMapsInputAudioTranscriptionCompleted(t *testing.T) {
	ev, ok := openAIRealtimeEvent(map[string]any{
		"type":       "conversation.item.input_audio_transcription.completed",
		"item_id":    "item_123",
		"transcript": "hello",
		"logprobs": []any{
			map[string]any{"logprob": math.Log(0.81)},
			map[string]any{"logprob": math.Log(0.49)},
		},
	})
	if !ok {
		t.Fatal("openAIRealtimeEvent returned ok=false, want transcription event")
	}
	if ev.Type != llm.RealtimeEventTypeInputAudioTranscriptionCompleted {
		t.Fatalf("event type = %q, want input audio transcription", ev.Type)
	}
	if ev.InputTranscription == nil {
		t.Fatal("InputTranscription = nil, want transcription payload")
	}
	if ev.InputTranscription.ItemID != "item_123" || ev.InputTranscription.Transcript != "hello" || !ev.InputTranscription.IsFinal {
		t.Fatalf("InputTranscription = %#v, want final item transcript", ev.InputTranscription)
	}
	wantConfidence := 0.63
	if ev.InputTranscription.Confidence == nil || math.Abs(*ev.InputTranscription.Confidence-wantConfidence) > 1e-9 {
		t.Fatalf("Confidence = %#v, want %.2f", ev.InputTranscription.Confidence, wantConfidence)
	}

	ev, ok = openAIRealtimeEvent(map[string]any{
		"type":       "conversation.item.input_audio_transcription.completed",
		"item_id":    "item_123",
		"transcript": "hello",
		"logprobs":   []any{},
	})
	if !ok {
		t.Fatal("openAIRealtimeEvent empty logprobs returned ok=false, want transcription event")
	}
	if ev.InputTranscription.Confidence != nil {
		t.Fatalf("Confidence = %#v, want nil for empty logprobs", ev.InputTranscription.Confidence)
	}
}

func TestRealtimeEventMapsInputAudioTranscriptionDelta(t *testing.T) {
	ev, ok := openAIRealtimeEvent(map[string]any{
		"type":          "conversation.item.input_audio_transcription.delta",
		"item_id":       "item_123",
		"content_index": 2,
		"delta":         "hel",
	})
	if !ok {
		t.Fatal("openAIRealtimeEvent returned ok=false, want transcription delta event")
	}
	if ev.Type != llm.RealtimeEventTypeInputAudioTranscriptionCompleted {
		t.Fatalf("event type = %q, want input audio transcription", ev.Type)
	}
	if ev.InputTranscription == nil {
		t.Fatal("InputTranscription = nil, want transcription payload")
	}
	if ev.InputTranscription.ItemID != "item_123" || ev.InputTranscription.Transcript != "hel" || ev.InputTranscription.IsFinal {
		t.Fatalf("InputTranscription = %#v, want non-final delta transcript", ev.InputTranscription)
	}
	if ev.InputTranscription.ContentIndex != 2 {
		t.Fatalf("ContentIndex = %d, want 2", ev.InputTranscription.ContentIndex)
	}
	if ev.InputTranscription.Confidence != nil {
		t.Fatalf("Confidence = %#v, want nil for delta", ev.InputTranscription.Confidence)
	}
}

func TestRealtimeEventMapsOutputAudioTranscriptDelta(t *testing.T) {
	for _, eventType := range []string{
		"response.output_audio_transcript.delta",
		"response.audio_transcript.delta",
	} {
		t.Run(eventType, func(t *testing.T) {
			ev, ok := openAIRealtimeEvent(map[string]any{
				"type":          eventType,
				"item_id":       "msg_123",
				"content_index": 2,
				"delta":         "hello",
			})
			if !ok {
				t.Fatal("openAIRealtimeEvent returned ok=false, want text event")
			}
			if ev.Type != llm.RealtimeEventTypeText || ev.Text != "hello" {
				t.Fatalf("event = %#v, want text delta hello", ev)
			}
			if ev.ItemID != "msg_123" || ev.ContentIndex != 2 {
				t.Fatalf("event metadata = item %q content %d, want msg_123 content 2", ev.ItemID, ev.ContentIndex)
			}
		})
	}
}

func TestRealtimeEventMapsOutputTextAndAudioAliases(t *testing.T) {
	textEvent, ok := openAIRealtimeEvent(map[string]any{
		"type":          "response.output_text.delta",
		"item_id":       "msg_123",
		"content_index": 1,
		"delta":         "hello",
	})
	if !ok {
		t.Fatal("openAIRealtimeEvent text returned ok=false, want text event")
	}
	if textEvent.Type != llm.RealtimeEventTypeText || textEvent.Text != "hello" {
		t.Fatalf("text event = %#v, want text delta hello", textEvent)
	}
	if textEvent.ItemID != "msg_123" || textEvent.ContentIndex != 1 {
		t.Fatalf("text event metadata = item %q content %d, want msg_123 content 1", textEvent.ItemID, textEvent.ContentIndex)
	}

	audioEvent, ok := openAIRealtimeEvent(map[string]any{
		"type":          "response.output_audio.delta",
		"item_id":       "msg_456",
		"content_index": 3,
		"delta":         "aGVsbG8=",
	})
	if !ok {
		t.Fatal("openAIRealtimeEvent audio returned ok=false, want audio event")
	}
	if audioEvent.Type != llm.RealtimeEventTypeAudio || string(audioEvent.Data) != "hello" {
		t.Fatalf("audio event = %#v, want decoded audio bytes", audioEvent)
	}
	if audioEvent.ItemID != "msg_456" || audioEvent.ContentIndex != 3 {
		t.Fatalf("audio event metadata = item %q content %d, want msg_456 content 3", audioEvent.ItemID, audioEvent.ContentIndex)
	}

	if _, ok := openAIRealtimeEvent(map[string]any{
		"type":  "response.output_audio.delta",
		"delta": "not-base64",
	}); ok {
		t.Fatal("openAIRealtimeEvent invalid audio returned ok=true, want false")
	}
}

func TestRealtimeEventMapsInputAudioBufferSpeechStoppedPayload(t *testing.T) {
	ev, ok := openAIRealtimeEvent(map[string]any{
		"type": "input_audio_buffer.speech_stopped",
	})
	if !ok {
		t.Fatal("openAIRealtimeEvent returned ok=false, want speech stopped event")
	}
	if ev.Type != llm.RealtimeEventTypeSpeechStopped {
		t.Fatalf("event type = %q, want speech stopped", ev.Type)
	}
	if ev.SpeechStopped == nil {
		t.Fatal("SpeechStopped = nil, want speech stopped payload")
	}
	if !ev.SpeechStopped.UserTranscriptionEnabled {
		t.Fatal("UserTranscriptionEnabled = false, want OpenAI realtime transcription enabled")
	}
}

func TestRealtimeEventMapsResponseCreated(t *testing.T) {
	ev, ok := openAIRealtimeEvent(map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id":       "resp_123",
			"metadata": map[string]any{"client_event_id": "response_create_123"},
		},
	})
	if !ok {
		t.Fatal("openAIRealtimeEvent returned ok=false, want generation-created event")
	}
	if ev.Type != llm.RealtimeEventTypeGenerationCreated {
		t.Fatalf("event type = %q, want generation created", ev.Type)
	}
	if ev.Generation == nil {
		t.Fatal("Generation = nil, want generation-created payload")
	}
	if ev.Generation.ResponseID != "resp_123" || !ev.Generation.UserInitiated {
		t.Fatalf("Generation = %#v, want user-initiated response", ev.Generation)
	}
}

func TestRealtimeSessionRoutesOutputMessageTextDeltasToGenerationStream(t *testing.T) {
	session := &realtimeSession{}
	created := session.trackRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeGenerationCreated,
		Generation: &llm.GenerationCreatedEvent{
			ResponseID: "resp_123",
		},
	})
	if created.Generation == nil {
		t.Fatal("Generation = nil, want generation payload")
	}
	if created.Generation.MessageCh == nil || created.Generation.FunctionCh == nil {
		t.Fatalf("generation channels = %#v/%#v, want message and function streams", created.Generation.MessageCh, created.Generation.FunctionCh)
	}

	if ev, ok := session.trackOpenAIRealtimeEvent(map[string]any{
		"type": "response.output_item.added",
		"item": map[string]any{
			"id":   "msg_123",
			"type": "message",
		},
	}); ok {
		t.Fatalf("trackOpenAIRealtimeEvent = %#v, true; want side effect only", ev)
	}

	var msg llm.MessageGeneration
	select {
	case msg = <-created.Generation.MessageCh:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for message generation")
	}
	if msg.MessageID != "msg_123" {
		t.Fatalf("MessageID = %q, want msg_123", msg.MessageID)
	}
	if msg.TextCh == nil || msg.AudioCh == nil || msg.ModalitiesCh == nil {
		t.Fatalf("message channels = %#v/%#v/%#v, want text, audio, and modalities streams", msg.TextCh, msg.AudioCh, msg.ModalitiesCh)
	}

	session.trackRealtimeEvent(llm.RealtimeEvent{
		Type:   llm.RealtimeEventTypeText,
		ItemID: "msg_123",
		Text:   "hello",
	})

	select {
	case text := <-msg.TextCh:
		if text != "hello" {
			t.Fatalf("text delta = %q, want hello", text)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for text delta")
	}
}

func TestRealtimeSessionRoutesOutputMessageAudioDeltasToGenerationStream(t *testing.T) {
	session := &realtimeSession{}
	created := session.trackRealtimeEvent(llm.RealtimeEvent{
		Type:       llm.RealtimeEventTypeGenerationCreated,
		Generation: &llm.GenerationCreatedEvent{},
	})
	session.trackOpenAIRealtimeEvent(map[string]any{
		"type": "response.output_item.added",
		"item": map[string]any{
			"id":   "msg_123",
			"type": "message",
		},
	})

	var msg llm.MessageGeneration
	select {
	case msg = <-created.Generation.MessageCh:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for message generation")
	}

	session.trackRealtimeEvent(llm.RealtimeEvent{
		Type:   llm.RealtimeEventTypeAudio,
		ItemID: "msg_123",
		Data:   []byte{1, 0, 2, 0},
	})

	select {
	case frame := <-msg.AudioCh:
		if frame == nil {
			t.Fatal("audio frame = nil, want frame")
		}
		if string(frame.Data) != string([]byte{1, 0, 2, 0}) {
			t.Fatalf("audio data = %v, want [1 0 2 0]", frame.Data)
		}
		if frame.SampleRate != 24000 || frame.NumChannels != 1 || frame.SamplesPerChannel != 2 {
			t.Fatalf("audio frame metadata = %#v, want 24kHz mono with 2 samples", frame)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for audio delta")
	}
}

func TestRealtimeSessionRoutesContentPartModalitiesToGenerationStream(t *testing.T) {
	session := &realtimeSession{}
	created := session.trackRealtimeEvent(llm.RealtimeEvent{
		Type:       llm.RealtimeEventTypeGenerationCreated,
		Generation: &llm.GenerationCreatedEvent{},
	})
	session.trackOpenAIRealtimeEvent(map[string]any{
		"type": "response.output_item.added",
		"item": map[string]any{
			"id":   "msg_123",
			"type": "message",
		},
	})

	var msg llm.MessageGeneration
	select {
	case msg = <-created.Generation.MessageCh:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for message generation")
	}

	if ev, ok := session.trackOpenAIRealtimeEvent(map[string]any{
		"type":    "response.content_part.added",
		"item_id": "msg_123",
		"part":    map[string]any{"type": "audio"},
	}); ok {
		t.Fatalf("trackOpenAIRealtimeEvent = %#v, true; want side effect only", ev)
	}

	select {
	case modalities := <-msg.ModalitiesCh:
		if len(modalities) != 2 || modalities[0] != "audio" || modalities[1] != "text" {
			t.Fatalf("modalities = %#v, want audio and text", modalities)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for modalities")
	}

	session.trackOpenAIRealtimeEvent(map[string]any{
		"type":    "response.content_part.added",
		"item_id": "msg_123",
		"part":    map[string]any{"type": "text"},
	})
	select {
	case modalities := <-msg.ModalitiesCh:
		t.Fatalf("unexpected duplicate modalities = %#v", modalities)
	default:
	}
}

func TestRealtimeSessionClosesOutputMessageStreamsWhenItemDone(t *testing.T) {
	session := &realtimeSession{}
	created := session.trackRealtimeEvent(llm.RealtimeEvent{
		Type:       llm.RealtimeEventTypeGenerationCreated,
		Generation: &llm.GenerationCreatedEvent{},
	})
	session.trackOpenAIRealtimeEvent(map[string]any{
		"type": "response.output_item.added",
		"item": map[string]any{
			"id":   "msg_123",
			"type": "message",
		},
	})

	var msg llm.MessageGeneration
	select {
	case msg = <-created.Generation.MessageCh:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for message generation")
	}

	if ev, ok := session.trackOpenAIRealtimeEvent(map[string]any{
		"type": "response.output_item.done",
		"item": map[string]any{
			"id":   "msg_123",
			"type": "message",
		},
	}); ok {
		t.Fatalf("trackOpenAIRealtimeEvent = %#v, true; want side effect only", ev)
	}

	select {
	case _, ok := <-msg.TextCh:
		if ok {
			t.Fatal("TextCh still open, want closed")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for text stream close")
	}
	select {
	case _, ok := <-msg.AudioCh:
		if ok {
			t.Fatal("AudioCh still open, want closed")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for audio stream close")
	}
}

func TestRealtimeSessionClosesGenerationStreamsWhenResponseDone(t *testing.T) {
	session := &realtimeSession{}
	created := session.trackRealtimeEvent(llm.RealtimeEvent{
		Type:       llm.RealtimeEventTypeGenerationCreated,
		Generation: &llm.GenerationCreatedEvent{},
	})
	session.trackOpenAIRealtimeEvent(map[string]any{
		"type": "response.output_item.added",
		"item": map[string]any{
			"id":   "msg_123",
			"type": "message",
		},
	})

	var msg llm.MessageGeneration
	select {
	case msg = <-created.Generation.MessageCh:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for message generation")
	}

	if ev, ok := session.trackOpenAIRealtimeEvent(map[string]any{
		"type":     "response.done",
		"response": map[string]any{"id": "resp_123"},
	}); ok {
		t.Fatalf("trackOpenAIRealtimeEvent = %#v, true; want side effect only", ev)
	}

	select {
	case _, ok := <-msg.TextCh:
		if ok {
			t.Fatal("TextCh still open, want closed")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for text stream close")
	}
	select {
	case _, ok := <-msg.AudioCh:
		if ok {
			t.Fatal("AudioCh still open, want closed")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for audio stream close")
	}
	select {
	case _, ok := <-created.Generation.MessageCh:
		if ok {
			t.Fatal("MessageCh still open, want closed")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for generation message stream close")
	}
	select {
	case _, ok := <-created.Generation.FunctionCh:
		if ok {
			t.Fatal("FunctionCh still open, want closed")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for generation function stream close")
	}
}

func TestRealtimeSessionResolvesDefaultModalitiesWhenResponseDone(t *testing.T) {
	session := &realtimeSession{}
	created := session.trackRealtimeEvent(llm.RealtimeEvent{
		Type:       llm.RealtimeEventTypeGenerationCreated,
		Generation: &llm.GenerationCreatedEvent{},
	})
	session.trackOpenAIRealtimeEvent(map[string]any{
		"type": "response.output_item.added",
		"item": map[string]any{
			"id":   "msg_123",
			"type": "message",
		},
	})

	var msg llm.MessageGeneration
	select {
	case msg = <-created.Generation.MessageCh:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for message generation")
	}

	session.trackOpenAIRealtimeEvent(map[string]any{
		"type":     "response.done",
		"response": map[string]any{"id": "resp_123"},
	})

	select {
	case modalities, ok := <-msg.ModalitiesCh:
		if !ok {
			t.Fatal("ModalitiesCh closed before default modalities, want default modalities")
		}
		if len(modalities) != 2 || modalities[0] != "audio" || modalities[1] != "text" {
			t.Fatalf("modalities = %#v, want default audio and text", modalities)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for default modalities")
	}
}

func TestRealtimeSessionRoutesOutputFunctionCallsToGenerationStream(t *testing.T) {
	session := &realtimeSession{}
	created := session.trackRealtimeEvent(llm.RealtimeEvent{
		Type:       llm.RealtimeEventTypeGenerationCreated,
		Generation: &llm.GenerationCreatedEvent{},
	})

	if ev, ok := session.trackOpenAIRealtimeEvent(map[string]any{
		"type": "response.output_item.done",
		"item": map[string]any{
			"id":        "fc_123",
			"type":      "function_call",
			"call_id":   "call_123",
			"name":      "lookup",
			"arguments": `{"query":"hello"}`,
		},
	}); ok {
		t.Fatalf("trackOpenAIRealtimeEvent = %#v, true; want side effect only", ev)
	}

	select {
	case call := <-created.Generation.FunctionCh:
		if call == nil {
			t.Fatal("function call = nil, want completed call")
		}
		if call.ID != "fc_123" || call.CallID != "call_123" || call.Name != "lookup" || call.Arguments != `{"query":"hello"}` {
			t.Fatalf("function call = %#v, want OpenAI function call item", call)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for function call")
	}
}

func TestRealtimeEventMapsConversationItemAddedMessage(t *testing.T) {
	ev, ok := openAIRealtimeEvent(map[string]any{
		"type":             "conversation.item.added",
		"previous_item_id": "prev_123",
		"item": map[string]any{
			"id":   "msg_123",
			"type": "message",
			"role": "user",
			"content": []any{
				map[string]any{"type": "input_text", "text": "hello"},
			},
		},
	})
	if !ok {
		t.Fatal("openAIRealtimeEvent returned ok=false, want remote item event")
	}
	if ev.Type != llm.RealtimeEventTypeRemoteItemAdded {
		t.Fatalf("event type = %q, want remote item added", ev.Type)
	}
	if ev.RemoteItem == nil {
		t.Fatal("RemoteItem = nil, want remote item payload")
	}
	if ev.RemoteItem.PreviousItemID != "prev_123" {
		t.Fatalf("PreviousItemID = %q, want prev_123", ev.RemoteItem.PreviousItemID)
	}
	msg, ok := ev.RemoteItem.Item.(*llm.ChatMessage)
	if !ok {
		t.Fatalf("RemoteItem.Item = %T, want *llm.ChatMessage", ev.RemoteItem.Item)
	}
	if msg.ID != "msg_123" || msg.Role != llm.ChatRoleUser || msg.TextContent() != "hello" {
		t.Fatalf("message = %#v, want user text message", msg)
	}
}

func TestRealtimeSessionTracksRemoteItemAddedEvents(t *testing.T) {
	session := &realtimeSession{remote: llm.NewRemoteChatContext()}
	root := &llm.ChatMessage{ID: "root", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "root"}}}
	session.remote.Insert(nil, root)

	ev := llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeRemoteItemAdded,
		RemoteItem: &llm.RemoteItemAddedEvent{
			PreviousItemID: "root",
			Item: &llm.ChatMessage{
				ID:      "remote_123",
				Role:    llm.ChatRoleAssistant,
				Content: []llm.ChatContent{{Text: "hello"}},
			},
		},
	}

	session.trackRealtimeEvent(ev)

	item := session.remote.Get("remote_123")
	msg, ok := item.(*llm.ChatMessage)
	if !ok {
		t.Fatalf("tracked item = %T, want *llm.ChatMessage", item)
	}
	if msg.TextContent() != "hello" {
		t.Fatalf("tracked message text = %q, want hello", msg.TextContent())
	}
}

func TestRealtimeSessionTracksRemoteItemDeletedEvents(t *testing.T) {
	session := &realtimeSession{remote: llm.NewRemoteChatContext()}
	root := &llm.ChatMessage{ID: "root", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "root"}}}
	removed := &llm.ChatMessage{ID: "removed", Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "removed"}}}
	session.remote.Insert(nil, root)
	previousID := root.ID
	session.remote.Insert(&previousID, removed)

	session.trackOpenAIRealtimeEvent(map[string]any{
		"type":    "conversation.item.deleted",
		"item_id": "removed",
	})

	if item := session.remote.Get("removed"); item != nil {
		t.Fatalf("deleted item = %#v, want nil", item)
	}
	if item := session.remote.Get("root"); item == nil {
		t.Fatal("root item missing after deleting another item")
	}
}

func TestRealtimeSessionAccumulatesInputAudioTranscriptionDeltas(t *testing.T) {
	session := &realtimeSession{}

	first := session.trackRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
		InputTranscription: &llm.InputTranscriptionCompleted{
			ItemID:     "item_123",
			Transcript: "hel",
			IsFinal:    false,
		},
	})
	second := session.trackRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
		InputTranscription: &llm.InputTranscriptionCompleted{
			ItemID:     "item_123",
			Transcript: "lo",
			IsFinal:    false,
		},
	})

	if first.InputTranscription.Transcript != "hel" {
		t.Fatalf("first transcript = %q, want hel", first.InputTranscription.Transcript)
	}
	if second.InputTranscription.Transcript != "hello" {
		t.Fatalf("second transcript = %q, want accumulated hello", second.InputTranscription.Transcript)
	}

	session.trackRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
		InputTranscription: &llm.InputTranscriptionCompleted{
			ItemID:     "item_123",
			Transcript: "hello",
			IsFinal:    true,
		},
	})
	next := session.trackRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
		InputTranscription: &llm.InputTranscriptionCompleted{
			ItemID:     "item_123",
			Transcript: "new",
			IsFinal:    false,
		},
	})
	if next.InputTranscription.Transcript != "new" {
		t.Fatalf("next transcript = %q, want new after final clears accumulator", next.InputTranscription.Transcript)
	}
}

func TestRealtimeSessionAccumulatesInputAudioTranscriptionByContentIndex(t *testing.T) {
	session := &realtimeSession{}

	session.trackRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
		InputTranscription: &llm.InputTranscriptionCompleted{
			ItemID:       "item_123",
			ContentIndex: 0,
			Transcript:   "hel",
			IsFinal:      false,
		},
	})
	otherContent := session.trackRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
		InputTranscription: &llm.InputTranscriptionCompleted{
			ItemID:       "item_123",
			ContentIndex: 1,
			Transcript:   "oth",
			IsFinal:      false,
		},
	})
	firstContent := session.trackRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
		InputTranscription: &llm.InputTranscriptionCompleted{
			ItemID:       "item_123",
			ContentIndex: 0,
			Transcript:   "lo",
			IsFinal:      false,
		},
	})

	if otherContent.InputTranscription.Transcript != "oth" {
		t.Fatalf("other content transcript = %q, want oth", otherContent.InputTranscription.Transcript)
	}
	if firstContent.InputTranscription.Transcript != "hello" {
		t.Fatalf("first content transcript = %q, want hello", firstContent.InputTranscription.Transcript)
	}
}

func TestRealtimeSessionUpdatesRemoteItemOnFinalInputAudioTranscription(t *testing.T) {
	session := &realtimeSession{remote: llm.NewRemoteChatContext()}
	msg := &llm.ChatMessage{ID: "item_123", Role: llm.ChatRoleUser}
	session.remote.Insert(nil, msg)
	confidence := 0.82

	session.trackRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
		InputTranscription: &llm.InputTranscriptionCompleted{
			ItemID:     "item_123",
			Transcript: "hello",
			IsFinal:    true,
			Confidence: &confidence,
		},
	})

	item := session.remote.Get("item_123")
	tracked, ok := item.(*llm.ChatMessage)
	if !ok {
		t.Fatalf("tracked item = %T, want *llm.ChatMessage", item)
	}
	if tracked.TextContent() != "hello" {
		t.Fatalf("tracked message text = %q, want hello", tracked.TextContent())
	}
	if tracked.TranscriptConfidence == nil || *tracked.TranscriptConfidence != confidence {
		t.Fatalf("tracked confidence = %#v, want %.2f", tracked.TranscriptConfidence, confidence)
	}
}

func TestRealtimeSessionEmitsFinalPartialTranscriptOnInputAudioTranscriptionFailed(t *testing.T) {
	session := &realtimeSession{}
	session.trackRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
		InputTranscription: &llm.InputTranscriptionCompleted{
			ItemID:     "item_123",
			Transcript: "hel",
			IsFinal:    false,
		},
	})
	session.trackRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
		InputTranscription: &llm.InputTranscriptionCompleted{
			ItemID:     "item_123",
			Transcript: "lo",
			IsFinal:    false,
		},
	})

	ev, ok := session.trackOpenAIRealtimeEvent(map[string]any{
		"type":    "conversation.item.input_audio_transcription.failed",
		"item_id": "item_123",
	})
	if !ok {
		t.Fatal("trackOpenAIRealtimeEvent returned ok=false, want final partial event")
	}
	if ev.Type != llm.RealtimeEventTypeInputAudioTranscriptionCompleted || ev.InputTranscription == nil {
		t.Fatalf("event = %#v, want input transcription event", ev)
	}
	if ev.InputTranscription.Transcript != "hello" || !ev.InputTranscription.IsFinal {
		t.Fatalf("InputTranscription = %#v, want final accumulated hello", ev.InputTranscription)
	}

	next := session.trackRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
		InputTranscription: &llm.InputTranscriptionCompleted{
			ItemID:     "item_123",
			Transcript: "new",
			IsFinal:    false,
		},
	})
	if next.InputTranscription.Transcript != "new" {
		t.Fatalf("next transcript = %q, want new after failure clears accumulator", next.InputTranscription.Transcript)
	}
}

func TestRealtimeSessionIgnoresInputAudioTranscriptionFailedWithoutPartial(t *testing.T) {
	session := &realtimeSession{}

	if ev, ok := session.trackOpenAIRealtimeEvent(map[string]any{
		"type":    "conversation.item.input_audio_transcription.failed",
		"item_id": "item_123",
	}); ok {
		t.Fatalf("trackOpenAIRealtimeEvent = %#v, true; want no event", ev)
	}
}

func TestRealtimeEventMapsConversationItemAddedFunctionCall(t *testing.T) {
	ev, ok := openAIRealtimeEvent(map[string]any{
		"type": "conversation.item.added",
		"item": map[string]any{
			"id":        "fc_123",
			"type":      "function_call",
			"call_id":   "call_123",
			"name":      "lookup",
			"arguments": `{"query":"hello"}`,
		},
	})
	if !ok {
		t.Fatal("openAIRealtimeEvent returned ok=false, want remote function call event")
	}
	if ev.Type != llm.RealtimeEventTypeRemoteItemAdded {
		t.Fatalf("event type = %q, want remote item added", ev.Type)
	}
	call, ok := ev.RemoteItem.Item.(*llm.FunctionCall)
	if !ok {
		t.Fatalf("RemoteItem.Item = %T, want *llm.FunctionCall", ev.RemoteItem.Item)
	}
	if call.ID != "fc_123" || call.CallID != "call_123" || call.Name != "lookup" || call.Arguments != `{"query":"hello"}` {
		t.Fatalf("function call = %#v, want OpenAI function call item", call)
	}
}

func TestRealtimeEventMapsOutputItemDoneFunctionCall(t *testing.T) {
	ev, ok := openAIRealtimeEvent(map[string]any{
		"type": "response.output_item.done",
		"item": map[string]any{
			"id":        "fc_123",
			"type":      "function_call",
			"call_id":   "call_123",
			"name":      "lookup",
			"arguments": `{"query":"hello"}`,
		},
	})
	if !ok {
		t.Fatal("openAIRealtimeEvent returned ok=false, want completed function call event")
	}
	if ev.Type != llm.RealtimeEventTypeFunctionCall {
		t.Fatalf("event type = %q, want function call", ev.Type)
	}
	if ev.Function == nil {
		t.Fatal("Function = nil, want completed function call")
	}
	if ev.Function.CallID != "call_123" || ev.Function.Name != "lookup" || ev.Function.Arguments != `{"query":"hello"}` {
		t.Fatalf("Function = %#v, want completed OpenAI function call", ev.Function)
	}
}

func TestRealtimeEventMapsConversationItemAddedFunctionCallOutput(t *testing.T) {
	ev, ok := openAIRealtimeEvent(map[string]any{
		"type": "conversation.item.added",
		"item": map[string]any{
			"id":      "out_123",
			"type":    "function_call_output",
			"call_id": "call_123",
			"output":  "Paris",
		},
	})
	if !ok {
		t.Fatal("openAIRealtimeEvent returned ok=false, want remote function output event")
	}
	if ev.Type != llm.RealtimeEventTypeRemoteItemAdded {
		t.Fatalf("event type = %q, want remote item added", ev.Type)
	}
	output, ok := ev.RemoteItem.Item.(*llm.FunctionCallOutput)
	if !ok {
		t.Fatalf("RemoteItem.Item = %T, want *llm.FunctionCallOutput", ev.RemoteItem.Item)
	}
	if output.ID != "out_123" || output.CallID != "call_123" || output.Output != "Paris" || output.IsError {
		t.Fatalf("function output = %#v, want successful OpenAI function output item", output)
	}
}

func TestRealtimeChatContextCreateMessagesMapUserTextMessage(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.AddMessage(llm.ChatMessageArgs{
		ID:   "msg_123",
		Role: llm.ChatRoleUser,
		Text: "hello",
	})

	msgs, err := openAIRealtimeChatContextCreateMessages(chatCtx)
	if err != nil {
		t.Fatalf("openAIRealtimeChatContextCreateMessages error = %v, want nil", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages len = %d, want 1", len(msgs))
	}
	msg := msgs[0]
	if msg["type"] != "conversation.item.create" {
		t.Fatalf("message type = %#v, want conversation.item.create", msg["type"])
	}
	if msg["previous_item_id"] != "root" {
		t.Fatalf("previous_item_id = %#v, want root", msg["previous_item_id"])
	}
	item := msg["item"].(map[string]any)
	if item["id"] != "msg_123" || item["type"] != "message" || item["role"] != "user" {
		t.Fatalf("item = %#v, want user message item", item)
	}
	content := item["content"].([]map[string]any)
	if len(content) != 1 || content[0]["type"] != "input_text" || content[0]["text"] != "hello" {
		t.Fatalf("content = %#v, want input text hello", content)
	}
}

func TestRealtimeChatContextCreateMessagesSkipAgentMetadataItems(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.AgentHandoff{ID: "handoff", NewAgentID: "next"})
	chatCtx.Append(&llm.AgentConfigUpdate{ID: "config"})
	chatCtx.AddMessage(llm.ChatMessageArgs{
		ID:   "msg_123",
		Role: llm.ChatRoleUser,
		Text: "hello",
	})

	msgs, err := openAIRealtimeChatContextCreateMessages(chatCtx)
	if err != nil {
		t.Fatalf("openAIRealtimeChatContextCreateMessages error = %v, want nil", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages len = %d, want only user message", len(msgs))
	}
	item := msgs[0]["item"].(map[string]any)
	if item["id"] != "msg_123" {
		t.Fatalf("item = %#v, want user message only", item)
	}
}

func TestRealtimeChatContextUpdateMessagesDeleteRemovedAndRecreateChangedItems(t *testing.T) {
	oldCtx := llm.NewChatContext()
	oldCtx.AddMessage(llm.ChatMessageArgs{ID: "keep", Role: llm.ChatRoleUser, Text: "keep"})
	oldCtx.AddMessage(llm.ChatMessageArgs{ID: "changed", Role: llm.ChatRoleAssistant, Text: "old"})
	oldCtx.AddMessage(llm.ChatMessageArgs{ID: "removed", Role: llm.ChatRoleUser, Text: "remove"})

	newCtx := llm.NewChatContext()
	newCtx.AddMessage(llm.ChatMessageArgs{ID: "keep", Role: llm.ChatRoleUser, Text: "keep"})
	newCtx.AddMessage(llm.ChatMessageArgs{ID: "changed", Role: llm.ChatRoleAssistant, Text: "new"})
	newCtx.AddMessage(llm.ChatMessageArgs{ID: "created", Role: llm.ChatRoleUser, Text: "create"})

	msgs, err := openAIRealtimeChatContextUpdateMessages(oldCtx, newCtx)
	if err != nil {
		t.Fatalf("openAIRealtimeChatContextUpdateMessages error = %v, want nil", err)
	}
	if len(msgs) != 4 {
		t.Fatalf("messages len = %d, want delete removed, create created, delete changed, create changed", len(msgs))
	}
	if msgs[0]["type"] != "conversation.item.delete" || msgs[0]["item_id"] != "removed" {
		t.Fatalf("first message = %#v, want delete removed", msgs[0])
	}
	if msgs[1]["type"] != "conversation.item.create" || msgs[1]["previous_item_id"] != "changed" {
		t.Fatalf("second message = %#v, want create after changed", msgs[1])
	}
	created := msgs[1]["item"].(map[string]any)
	if created["id"] != "created" {
		t.Fatalf("created item = %#v, want created", created)
	}
	if msgs[2]["type"] != "conversation.item.delete" || msgs[2]["item_id"] != "changed" {
		t.Fatalf("third message = %#v, want delete changed", msgs[2])
	}
	if msgs[3]["type"] != "conversation.item.create" || msgs[3]["previous_item_id"] != "keep" {
		t.Fatalf("fourth message = %#v, want recreate changed after keep", msgs[3])
	}
	changed := msgs[3]["item"].(map[string]any)
	content := changed["content"].([]map[string]any)
	if changed["id"] != "changed" || content[0]["text"] != "new" {
		t.Fatalf("changed item = %#v, want changed text new", changed)
	}
}

func TestRealtimeTruncateTranscriptUpdateMessagesReplacesTextOnlyMessage(t *testing.T) {
	oldCtx := llm.NewChatContext()
	oldCtx.AddMessage(llm.ChatMessageArgs{ID: "msg_123", Role: llm.ChatRoleAssistant, Text: "old transcript"})
	audioTranscript := "forwarded transcript"

	msgs, err := openAIRealtimeTruncateTranscriptUpdateMessages(oldCtx, llm.RealtimeTruncateOptions{
		MessageID:       "msg_123",
		Modalities:      []string{"text"},
		AudioTranscript: &audioTranscript,
	})
	if err != nil {
		t.Fatalf("openAIRealtimeTruncateTranscriptUpdateMessages error = %v, want nil", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("messages len = %d, want delete and recreate changed message", len(msgs))
	}
	if msgs[0]["type"] != "conversation.item.delete" || msgs[0]["item_id"] != "msg_123" {
		t.Fatalf("first message = %#v, want delete msg_123", msgs[0])
	}
	if msgs[1]["type"] != "conversation.item.create" || msgs[1]["previous_item_id"] != "root" {
		t.Fatalf("second message = %#v, want recreate at root", msgs[1])
	}
	item := msgs[1]["item"].(map[string]any)
	content := item["content"].([]map[string]any)
	if item["id"] != "msg_123" || content[0]["text"] != "forwarded transcript" {
		t.Fatalf("recreated item = %#v, want forwarded transcript", item)
	}
}

func TestRealtimeEventMapsResponseDoneMetrics(t *testing.T) {
	ev, ok := openAIRealtimeEvent(map[string]any{
		"type": "response.done",
		"response": map[string]any{
			"id":     "resp_123",
			"status": "cancelled",
			"usage": map[string]any{
				"input_tokens":  11.0,
				"output_tokens": 7.0,
				"total_tokens":  18.0,
				"input_token_details": map[string]any{
					"audio_tokens":  3.0,
					"text_tokens":   4.0,
					"image_tokens":  1.0,
					"cached_tokens": 2.0,
					"cached_tokens_details": map[string]any{
						"text_tokens":  1.0,
						"audio_tokens": 1.0,
						"image_tokens": 0.0,
					},
				},
				"output_token_details": map[string]any{
					"text_tokens":  5.0,
					"audio_tokens": 2.0,
					"image_tokens": 0.0,
				},
			},
		},
	})
	if !ok {
		t.Fatal("openAIRealtimeEvent returned ok=false, want metrics event")
	}
	if ev.Type != llm.RealtimeEventTypeMetricsCollected {
		t.Fatalf("event type = %q, want metrics collected", ev.Type)
	}
	if ev.Metrics == nil {
		t.Fatal("Metrics = nil, want realtime metrics payload")
	}
	if ev.Metrics.RequestID != "resp_123" || !ev.Metrics.Cancelled {
		t.Fatalf("metrics identity = %#v, want cancelled response metrics", ev.Metrics)
	}
	if ev.Metrics.InputTokens != 11 || ev.Metrics.OutputTokens != 7 || ev.Metrics.TotalTokens != 18 {
		t.Fatalf("token totals = %#v, want 11/7/18", ev.Metrics)
	}
	if ev.Metrics.InputTokenDetails.AudioTokens != 3 || ev.Metrics.InputTokenDetails.TextTokens != 4 || ev.Metrics.InputTokenDetails.ImageTokens != 1 || ev.Metrics.InputTokenDetails.CachedTokens != 2 {
		t.Fatalf("input details = %#v, want audio/text/image/cached usage", ev.Metrics.InputTokenDetails)
	}
	if ev.Metrics.InputTokenDetails.CachedTokensDetails == nil {
		t.Fatal("CachedTokensDetails = nil, want cached token breakdown")
	}
	if ev.Metrics.InputTokenDetails.CachedTokensDetails.TextTokens != 1 || ev.Metrics.InputTokenDetails.CachedTokensDetails.AudioTokens != 1 {
		t.Fatalf("cached details = %#v, want text/audio cached usage", ev.Metrics.InputTokenDetails.CachedTokensDetails)
	}
	if ev.Metrics.OutputTokenDetails.TextTokens != 5 || ev.Metrics.OutputTokenDetails.AudioTokens != 2 {
		t.Fatalf("output details = %#v, want text/audio output usage", ev.Metrics.OutputTokenDetails)
	}
}
