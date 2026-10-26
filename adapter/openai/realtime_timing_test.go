package openai

import (
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestRealtimeSpeechTimingUsesAudioOrigin(t *testing.T) {
	s := &realtimeSession{inputAudioStartedAt: time.Unix(100, 0)}
	event := llm.RealtimeEvent{Type: llm.RealtimeEventTypeSpeechStopped, SpeechStopped: &llm.InputSpeechStoppedEvent{}}
	s.alignSpeechTiming(&event, map[string]any{"audio_end_ms": 1500.0})
	if event.SpeechStopped.SpeechEndTime == nil || *event.SpeechStopped.SpeechEndTime != 101.5 {
		t.Fatalf("end=%v, want 101.5", event.SpeechStopped.SpeechEndTime)
	}
	start := llm.RealtimeEvent{Type: llm.RealtimeEventTypeSpeechStarted}
	s.alignSpeechTiming(&start, map[string]any{"audio_start_ms": 1000.0})
	if start.SpeechStartTime == nil || *start.SpeechStartTime != 101 {
		t.Fatal("speech start did not use input origin")
	}
	s.inputAudioStartedAt = time.Time{}
	event.SpeechStopped.SpeechEndTime = nil
	s.alignSpeechTiming(&event, map[string]any{"audio_end_ms": 1500.0})
	if event.SpeechStopped.SpeechEndTime != nil {
		t.Fatal("unavailable clock origin fabricated a speech end")
	}
}
