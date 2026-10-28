package stt

import (
	"context"
	"math"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

func TestAlignSpeechEventTimingKeepsOriginAndReceipt(t *testing.T) {
	stream := &splitTestStream{startTime: 100}
	event := &SpeechEvent{ReceivedAt: time.Unix(101, 400000000), Alternatives: []SpeechData{{StartTime: 0, EndTime: 1}}}
	aligned := AlignSpeechEventTiming(event, stream)
	if aligned.SpeechStartTime == nil || *aligned.SpeechStartTime != 100 || aligned.SpeechEndTime == nil || *aligned.SpeechEndTime != 101 {
		t.Fatalf("aligned anchors=%+v", aligned)
	}
	if event.SpeechStartTime != nil || event.SpeechEndTime != nil {
		t.Fatal("provider event was mutated")
	}
	other := AlignSpeechEventTiming(aligned, &splitTestStream{startTime: 200})
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

func TestTurnSplittingTimingAdvancesWithInputAudio(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		provider := &splitTestSTT{}
		logical, err := NewTurnSplittingSTT(provider, time.Millisecond).Stream(context.Background(), "en")
		if err != nil {
			t.Fatal(err)
		}
		defer logical.Close()
		timing := logical.(StreamTiming)
		timing.SetStartTime(1000)
		if err := logical.PushFrame(&model.AudioFrame{SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 16000}); err != nil {
			t.Fatal(err)
		}
		speak(t, logical)
		if err := logical.Flush(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		if provider.count() != 2 {
			t.Fatalf("streams=%d, want replacement", provider.count())
		}
		if got := provider.stream(1).StartTime(); got != 1001 {
			t.Fatalf("replacement origin=%v, want 1001", got)
		}
		if provider.stream(0).StartTime() != 1000 {
			t.Fatal("retired stream origin was changed")
		}
	})
}
