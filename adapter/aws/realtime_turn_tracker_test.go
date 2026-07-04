package aws

import (
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestAWSRealtimeTurnTrackerEmitsReferenceTurnEvents(t *testing.T) {
	var events []llm.RealtimeEvent
	tracker := newAWSRealtimeTurnTracker(
		func(event llm.RealtimeEvent) { events = append(events, event) },
		func() {
			events = append(events, llm.RealtimeEvent{
				Type:       llm.RealtimeEventTypeGenerationCreated,
				Generation: &llm.GenerationCreatedEvent{},
			})
		},
	)

	tracker.feed(map[string]any{
		"event": map[string]any{
			"textOutput": map[string]any{"role": "USER", "content": "hello"},
		},
	})
	tracker.feed(map[string]any{
		"event": map[string]any{
			"textOutput": map[string]any{"role": "USER", "content": "there"},
		},
	})
	tracker.feed(map[string]any{
		"event": map[string]any{
			"contentStart": map[string]any{
				"role":                  "ASSISTANT",
				"additionalModelFields": "SPECULATIVE",
			},
		},
	})

	if len(events) != 6 {
		t.Fatalf("event count = %d, want 6: %#v", len(events), events)
	}
	if events[0].Type != llm.RealtimeEventTypeSpeechStarted {
		t.Fatalf("event[0] = %s, want speech_started", events[0].Type)
	}
	assertAWSRealtimeTurnTranscript(t, events[1], "hello", false)
	firstItemID := events[1].InputTranscription.ItemID
	assertAWSRealtimeTurnTranscript(t, events[2], "hello there", false)
	if events[2].InputTranscription.ItemID != firstItemID {
		t.Fatalf("interim item id = %q, want same %q", events[2].InputTranscription.ItemID, firstItemID)
	}
	if events[3].Type != llm.RealtimeEventTypeSpeechStopped {
		t.Fatalf("event[3] = %s, want speech_stopped", events[3].Type)
	}
	if events[3].SpeechStopped == nil || !events[3].SpeechStopped.UserTranscriptionEnabled {
		t.Fatalf("speech stopped payload = %#v, want user transcription enabled", events[3].SpeechStopped)
	}
	assertAWSRealtimeTurnTranscript(t, events[4], "hello there", true)
	if events[4].InputTranscription.ItemID != firstItemID {
		t.Fatalf("final item id = %q, want same %q", events[4].InputTranscription.ItemID, firstItemID)
	}
	if events[5].Type != llm.RealtimeEventTypeGenerationCreated {
		t.Fatalf("event[5] = %s, want generation_created", events[5].Type)
	}

	tracker.feed(map[string]any{
		"event": map[string]any{
			"textOutput": map[string]any{"role": "USER", "content": "next"},
		},
	})
	if len(events) != 8 {
		t.Fatalf("event count after next turn = %d, want 8: %#v", len(events), events)
	}
	if events[6].Type != llm.RealtimeEventTypeSpeechStarted {
		t.Fatalf("event[6] = %s, want fresh speech_started", events[6].Type)
	}
	assertAWSRealtimeTurnTranscript(t, events[7], "next", false)
	if events[7].InputTranscription.ItemID == firstItemID {
		t.Fatalf("next turn item id reused %q", firstItemID)
	}
}

func TestAWSRealtimeTurnTrackerReplacesCumulativeUserPartials(t *testing.T) {
	var events []llm.RealtimeEvent
	tracker := newAWSRealtimeTurnTracker(
		func(event llm.RealtimeEvent) { events = append(events, event) },
		func() {
			events = append(events, llm.RealtimeEvent{
				Type:       llm.RealtimeEventTypeGenerationCreated,
				Generation: &llm.GenerationCreatedEvent{},
			})
		},
	)

	tracker.feed(map[string]any{
		"event": map[string]any{
			"textOutput": map[string]any{"role": "USER", "content": "hello"},
		},
	})
	tracker.feed(map[string]any{
		"event": map[string]any{
			"textOutput": map[string]any{"role": "USER", "content": "hello there"},
		},
	})
	tracker.feed(map[string]any{
		"event": map[string]any{
			"contentStart": map[string]any{
				"role":                  "ASSISTANT",
				"additionalModelFields": "SPECULATIVE",
			},
		},
	})

	if len(events) != 6 {
		t.Fatalf("event count = %d, want 6: %#v", len(events), events)
	}
	assertAWSRealtimeTurnTranscript(t, events[1], "hello", false)
	assertAWSRealtimeTurnTranscript(t, events[2], "hello there", false)
	assertAWSRealtimeTurnTranscript(t, events[4], "hello there", true)
}

func TestAWSRealtimeTurnTrackerToolOutputStartsReferenceGeneration(t *testing.T) {
	var events []llm.RealtimeEvent
	tracker := newAWSRealtimeTurnTracker(
		func(event llm.RealtimeEvent) { events = append(events, event) },
		func() {
			events = append(events, llm.RealtimeEvent{
				Type:       llm.RealtimeEventTypeGenerationCreated,
				Generation: &llm.GenerationCreatedEvent{},
			})
		},
	)

	tracker.feed(map[string]any{
		"event": map[string]any{
			"textOutput": map[string]any{"role": "USER", "content": "book a flight"},
		},
	})
	tracker.feed(map[string]any{
		"event": map[string]any{
			"contentStart": map[string]any{"type": "TOOL"},
		},
	})

	if len(events) != 5 {
		t.Fatalf("event count = %d, want 5: %#v", len(events), events)
	}
	if events[0].Type != llm.RealtimeEventTypeSpeechStarted {
		t.Fatalf("event[0] = %s, want speech_started", events[0].Type)
	}
	assertAWSRealtimeTurnTranscript(t, events[1], "book a flight", false)
	if events[2].Type != llm.RealtimeEventTypeSpeechStopped {
		t.Fatalf("event[2] = %s, want speech_stopped", events[2].Type)
	}
	assertAWSRealtimeTurnTranscript(t, events[3], "book a flight", true)
	if events[4].Type != llm.RealtimeEventTypeGenerationCreated {
		t.Fatalf("event[4] = %s, want generation_created", events[4].Type)
	}

	tracker.feed(map[string]any{
		"event": map[string]any{
			"textOutput": map[string]any{"role": "USER", "content": "next turn"},
		},
	})
	if len(events) != 7 {
		t.Fatalf("event count after next turn = %d, want 7: %#v", len(events), events)
	}
	if events[6].InputTranscription.ItemID == events[1].InputTranscription.ItemID {
		t.Fatalf("next turn item id reused %q", events[1].InputTranscription.ItemID)
	}
}

func TestAWSRealtimeTurnTrackerBargeInStartsFreshSpeech(t *testing.T) {
	var events []llm.RealtimeEvent
	tracker := newAWSRealtimeTurnTracker(
		func(event llm.RealtimeEvent) { events = append(events, event) },
		func() {
			events = append(events, llm.RealtimeEvent{
				Type:       llm.RealtimeEventTypeGenerationCreated,
				Generation: &llm.GenerationCreatedEvent{},
			})
		},
	)

	tracker.feed(map[string]any{
		"event": map[string]any{
			"textOutput": map[string]any{"role": "ASSISTANT", "content": awsRealtimeBargeInContent},
		},
	})

	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1: %#v", len(events), events)
	}
	if events[0].Type != llm.RealtimeEventTypeSpeechStarted {
		t.Fatalf("event[0] = %s, want speech_started for barge-in", events[0].Type)
	}
}

func assertAWSRealtimeTurnTranscript(t *testing.T, event llm.RealtimeEvent, wantText string, wantFinal bool) {
	t.Helper()
	if event.Type != llm.RealtimeEventTypeInputAudioTranscriptionCompleted {
		t.Fatalf("event = %s, want input_audio_transcription_completed", event.Type)
	}
	if event.InputTranscription == nil {
		t.Fatal("InputTranscription = nil")
	}
	if event.InputTranscription.ItemID == "" {
		t.Fatal("InputTranscription.ItemID is empty")
	}
	if event.InputTranscription.Transcript != wantText {
		t.Fatalf("transcript = %q, want %q", event.InputTranscription.Transcript, wantText)
	}
	if event.InputTranscription.IsFinal != wantFinal {
		t.Fatalf("is_final = %t, want %t", event.InputTranscription.IsFinal, wantFinal)
	}
}
