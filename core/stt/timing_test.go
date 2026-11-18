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

// Missing audio must move wall anchors without stretching words across the gap.
func TestAlignSpeechEventTimingAcrossSilenceGap(t *testing.T) {
	s := &mappedTimingStream{timingTestStream: timingTestStream{startTime: 100}}
	event := &SpeechEvent{ReceivedAt: time.Unix(140, 0), Alternatives: []SpeechData{{StartTime: 0, EndTime: 2, Words: []TimedString{{Text: "ok", StartTime: 0, EndTime: 1}, {Text: "stop", StartTime: 1, EndTime: 2}}}}}
	got := AlignSpeechEventTiming(event, s)
	if *got.SpeechEndTime != 132 {
		t.Fatalf("end=%v, want 132 after 30s gap", *got.SpeechEndTime)
	}
	if *got.SpeechStartTime != 100 {
		t.Fatal("start changed")
	}
}

type mappedTimingStream struct{ timingTestStream }

func (s *mappedTimingStream) WallTimeAtStart(offset float64) (time.Time, bool) {
	if offset == 1 {
		return time.Unix(131, 0), true
	}
	return s.WallTimeAt(offset)
}
func (*mappedTimingStream) WallTimeAt(offset float64) (time.Time, bool) {
	if offset > 1 {
		return time.Unix(130, 0).Add(time.Duration(offset * float64(time.Second))), true
	}
	return time.Unix(100, 0).Add(time.Duration(offset * float64(time.Second))), true
}

func TestAlignedWallTimingPreservesProviderOffsetsAndSurvivesReplay(t *testing.T) {
	s := &mappedTimingStream{timingTestStream: timingTestStream{startTime: 100}}
	end := 102.0
	event := &SpeechEvent{ReceivedAt: time.Unix(140, 0), SpeechEndTime: &end, Alternatives: []SpeechData{{StartTime: 1, EndTime: 2, Words: []TimedString{{Text: "stop", StartTime: 1.2, EndTime: 1.8}}}}}
	got := AlignSpeechEventTiming(event, s)
	if *got.SpeechEndTime != 132 || *got.Alternatives[0].Words[0].SpeechMidTime != 131.5 {
		t.Fatalf("wall anchors not mapped: %+v", got)
	}
	if event.Alternatives[0].Words[0].SpeechMidTime != nil || got.Alternatives[0].Words[0].StartTime != 1.2 || *event.SpeechEndTime != 102 {
		t.Fatal("provider data mutated")
	}
	replay := AlignSpeechEventTiming(got, s)
	if *replay.SpeechEndTime != 132 || *replay.Alternatives[0].Words[0].SpeechMidTime != 131.5 {
		t.Fatal("replay remapped wall anchors twice")
	}
	zero := AlignSpeechEventTiming(&SpeechEvent{ReceivedAt: time.Unix(140, 0), Alternatives: []SpeechData{{StartTime: 0.2, EndTime: 0.8}}}, s)
	if math.Abs(*zero.SpeechStartTime-100.2) > 1e-8 || math.Abs(*zero.SpeechEndTime-100.8) > 1e-8 {
		t.Fatal("zero-drift behavior changed")
	}
}
