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
	if copy.timingAligned {
		return &copy
	}
	copy.timingAligned = true
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
	if timing, ok := stream.(StreamTiming); ok {
		if clock, ok := stream.(StreamWallTiming); ok {
			remap := func(anchor *float64, start bool) *float64 {
				if anchor != nil {
					lookup := clock.WallTimeAt
					if start {
						lookup = clock.WallTimeAtStart
					}
					if wall, found := lookup(*anchor - timing.StartTime()); found {
						value := float64(wall.UnixNano()) / 1e9
						return &value
					}
				}
				return anchor
			}
			copy.SpeechStartTime = remap(copy.SpeechStartTime, true)
			copy.SpeechEndTime = remap(copy.SpeechEndTime, false)
			copy.Alternatives = append([]SpeechData(nil), copy.Alternatives...)
			for i := range copy.Alternatives {
				data := &copy.Alternatives[i]
				data.Words = append([]TimedString(nil), data.Words...)
				for j := range data.Words {
					word := &data.Words[j]
					start, end := timing.StartTime()+word.StartTime, timing.StartTime()+word.EndTime
					word.SpeechStartTime, word.SpeechEndTime = remap(&start, true), remap(&end, false)
					mid := timing.StartTime() + (word.StartTime+word.EndTime)/2
					word.SpeechMidTime = remap(&mid, false)
				}
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
