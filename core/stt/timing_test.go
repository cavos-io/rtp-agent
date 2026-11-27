package stt

import (
	"math"
	"testing"
	"time"
)

func TestAlignSpeechEventTimingKeepsOriginAndReceipt(t *testing.T) {
	stream := &timingTestStream{startTime: 100}
	event := &SpeechEvent{ReceivedAt: time.Unix(101, 400000000), Alternatives: []SpeechData{{StartTime: 0, EndTime: 1}}}
	aligned := AlignSpeechEventTiming(event, stream)
	if aligned.SpeechStartTime == nil || *aligned.SpeechStartTime != 100 || aligned.SpeechEndTime == nil || *aligned.SpeechEndTime != 101 {
		t.Fatalf("aligned anchors=%+v", aligned)
	}
	if event.SpeechStartTime != nil || event.SpeechEndTime != nil {
		t.Fatal("provider event was mutated")
	}
	other := AlignSpeechEventTiming(aligned, &timingTestStream{startTime: 200})
	if *other.SpeechEndTime != 101 || !other.ReceivedAt.Equal(event.ReceivedAt) {
		t.Fatal("buffering or stream replacement realigned an absolute timestamp")
	}
}
func TestAlignSpeechEventTimingRejectsInvalidAndClampsFuture(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), -1} {
		got := AlignSpeechEventTiming(&SpeechEvent{ReceivedAt: time.Unix(100, 0), SpeechEndTime: &value}, nil)
		if got.SpeechEndTime != nil {
			t.Fatal("invalid timestamp survived")
		}
	}
	future := 101.
	got := AlignSpeechEventTiming(&SpeechEvent{ReceivedAt: time.Unix(100, 0), SpeechEndTime: &future}, nil)
	if got.SpeechEndTime == nil || *got.SpeechEndTime != 100 {
		t.Fatal("future timestamp was not clamped")
	}
	got = AlignSpeechEventTiming(&SpeechEvent{Alternatives: []SpeechData{{EndTime: 1}}}, nil)
	if got.SpeechEndTime != nil {
		t.Fatal("relative timestamp used without an input origin")
	}
	if AlignSpeechEventTiming(nil, nil) != nil {
		t.Fatal("nil event should stay nil")
	}
}

// timingTestStream only supplies the clock used by AlignSpeechEventTiming.
type timingTestStream struct {
	RecognizeStream
	StreamTiming
	startTime float64
}

func (s *timingTestStream) StartTime() float64 { return s.startTime }
