package openai

import (
	"math"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func (s *realtimeSession) alignSpeechTiming(event *llm.RealtimeEvent, raw map[string]any) {
	if event.Type != llm.RealtimeEventTypeSpeechStarted && event.Type != llm.RealtimeEventTypeSpeechStopped {
		return
	}
	s.mu.Lock()
	origin := s.inputAudioStartedAt
	s.mu.Unlock()
	if origin.IsZero() {
		return
	}
	key := "audio_start_ms"
	if event.Type == llm.RealtimeEventTypeSpeechStopped {
		key = "audio_end_ms"
	}
	millis, ok := openAIRealtimeOptionalFloat(raw[key])
	if !ok || millis < 0 || math.IsNaN(millis) || math.IsInf(millis, 0) {
		return
	}
	at := origin.Add(time.Duration(millis * float64(time.Millisecond)))
	if at.After(time.Now()) {
		at = time.Now()
	}
	value := float64(at.UnixNano()) / 1e9
	if event.Type == llm.RealtimeEventTypeSpeechStarted {
		event.SpeechStartTime = &value
	} else if event.SpeechStopped != nil {
		event.SpeechStopped.SpeechEndTime = &value
	}
}
