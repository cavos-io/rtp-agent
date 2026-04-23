package stt

import (
	"context"
	"fmt"
	"testing"

	"github.com/cavos-io/rtp-agent/model"
)

type mockSTT struct {
	label string
	recognizeFunc func(ctx context.Context, frames []*model.AudioFrame, language string) (*SpeechEvent, error)
}

func (m *mockSTT) Label() string { return m.label }
func (m *mockSTT) Capabilities() STTCapabilities { return STTCapabilities{} }
func (m *mockSTT) Stream(ctx context.Context, language string) (RecognizeStream, error) {
	return nil, fmt.Errorf("not implemented")
}
func (m *mockSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*SpeechEvent, error) {
	return m.recognizeFunc(ctx, frames, language)
}

func TestFallbackAdapter_Recognize(t *testing.T) {
	s1 := &mockSTT{
		label: "s1",
		recognizeFunc: func(ctx context.Context, frames []*model.AudioFrame, language string) (*SpeechEvent, error) {
			return nil, fmt.Errorf("fail")
		},
	}
	s2 := &mockSTT{
		label: "s2",
		recognizeFunc: func(ctx context.Context, frames []*model.AudioFrame, language string) (*SpeechEvent, error) {
			return &SpeechEvent{Type: SpeechEventFinalTranscript}, nil
		},
	}

	fallback := NewFallbackAdapter([]STT{s1, s2})
	res, err := fallback.Recognize(context.Background(), nil, "en")
	if err != nil {
		t.Fatalf("Expected success, got %v", err)
	}
	if res.Type != SpeechEventFinalTranscript {
		t.Errorf("Expected final transcript, got %v", res.Type)
	}
}

func TestFallbackAdapter_PanicOnEmpty(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Expected panic on empty STT list")
		}
	}()
	NewFallbackAdapter([]STT{})
}

func TestPrimarySpeakerDetector(t *testing.T) {
	opt := DefaultPrimarySpeakerDetectionOptions()
	opt.MinRMSSamples = 1
	detector := newPrimarySpeakerDetector(true, true, "{text}", "{text}", opt)

	data := make([]byte, 3200)
	for i := range data {
		data[i] = 0x7F
	}
	frame := &model.AudioFrame{
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1600,
		Data:              data,
	}
	// Push 1 second of audio (10 frames)
	for i := 0; i < 10; i++ {
		detector.pushAudio(frame)
	}

	// Simulate event at 0.5-0.6s
	ev := &SpeechEvent{
		Type: SpeechEventFinalTranscript,
		Alternatives: []SpeechData{
			{SpeakerID: "s1", StartTime: 0.5, EndTime: 0.6, Text: "Hello"},
		},
	}

	res := detector.onSttEvent(ev)
	if res == nil {
		t.Fatalf("Expected non-nil event")
	}

	if *res.Alternatives[0].IsPrimarySpeaker != true {
		t.Errorf("Expected s1 to be primary")
	}
}
