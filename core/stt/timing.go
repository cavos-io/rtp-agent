package stt

import (
	"math"
	"time"
)

// AlignSpeechEventTiming preserves provider-relative timestamps and adds absolute
// anchors using the originating stream's clock. Call before buffering an event.
func AlignSpeechEventTiming(event *SpeechEvent, stream RecognizeStream) *SpeechEvent {
	if event == nil {
		return nil
	}
	copy := *event
	if copy.ReceivedAt.IsZero() {
		copy.ReceivedAt = time.Now()
	}
	now := float64(copy.ReceivedAt.UnixNano()) / 1e9
	if timing, ok := stream.(StreamTiming); ok && timing.StartTime() > 0 && len(copy.Alternatives) > 0 {
		data := copy.Alternatives[0]
		if data.EndTime > 0 && data.EndTime >= data.StartTime {
			if copy.SpeechStartTime == nil {
				value := timing.StartTime() + data.StartTime
				copy.SpeechStartTime = &value
			}
			if copy.SpeechEndTime == nil {
				value := timing.StartTime() + data.EndTime
				copy.SpeechEndTime = &value
			}
		}
	}
	for _, anchor := range []**float64{&copy.SpeechStartTime, &copy.SpeechEndTime} {
		if *anchor == nil {
			continue
		}
		value := **anchor
		if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			*anchor = nil
			continue
		}
		value = min(value, now)
		*anchor = &value
	}
	return &copy
}
