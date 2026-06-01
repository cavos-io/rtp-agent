package vad

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/cavos-io/conversation-worker/library/telemetry"
	"github.com/cavos-io/conversation-worker/model"
)

func TestSimpleVADMetadataAndCapabilities(t *testing.T) {
	detector := NewSimpleVADWithOptions(SimpleVADOptions{UpdateInterval: 0.5})

	if detector.Label() != "vad.SimpleVAD" {
		t.Fatalf("Label() = %q, want vad.SimpleVAD", detector.Label())
	}
	if detector.Model() != "simple" {
		t.Fatalf("Model() = %q, want simple", detector.Model())
	}
	if detector.Provider() != "builtin" {
		t.Fatalf("Provider() = %q, want builtin", detector.Provider())
	}
	if detector.Capabilities().UpdateInterval != 0.5 {
		t.Fatalf("Capabilities().UpdateInterval = %v, want 0.5", detector.Capabilities().UpdateInterval)
	}
}

func TestSimpleVADEmitsMetricsCollected(t *testing.T) {
	detector := NewSimpleVADWithOptions(SimpleVADOptions{UpdateInterval: 1})
	metricsCh := make(chan *telemetry.VADMetrics, 1)
	detector.OnMetricsCollected(func(metrics *telemetry.VADMetrics) {
		metricsCh <- metrics
	})

	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(audioFrame(16000, 160, 6000)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)

	select {
	case metrics := <-metricsCh:
		if metrics.Label != "vad.SimpleVAD" {
			t.Fatalf("metrics Label = %q, want vad.SimpleVAD", metrics.Label)
		}
		if metrics.InferenceCount != 1 {
			t.Fatalf("metrics InferenceCount = %d, want 1", metrics.InferenceCount)
		}
		if metrics.Metadata == nil {
			t.Fatal("metrics Metadata = nil, want model metadata")
		}
		if metrics.Metadata.ModelName != "simple" || metrics.Metadata.ModelProvider != "builtin" {
			t.Fatalf("metrics Metadata = %#v, want simple/builtin", metrics.Metadata)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for VAD metrics")
	}
}

func TestSimpleVADUpdateOptionsAppliesToActiveStream(t *testing.T) {
	detector := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:         0.05,
		MinSpeechDuration: 0.03,
	})
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(audioFrame(16000, 160, 6000)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)

	detector.UpdateOptions(SimpleVADOptions{MinSpeechDuration: 0.01})
	if err := stream.PushFrame(audioFrame(16000, 160, 6000)); err != nil {
		t.Fatalf("PushFrame() after UpdateOptions() error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventStartOfSpeech)
}

func TestSimpleVADUpdateOptionsRelaxesMaxBufferedSpeech(t *testing.T) {
	detector := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:                 0.05,
		MaxBufferedSpeechDuration: 0.01,
	})
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	firstSpeech := audioFrame(16000, 160, 6000)
	secondSpeech := audioFrame(16000, 160, 6000)
	thirdSpeech := audioFrame(16000, 160, 6000)
	if err := stream.PushFrame(firstSpeech); err != nil {
		t.Fatalf("PushFrame() first error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventStartOfSpeech)
	if err := stream.PushFrame(secondSpeech); err != nil {
		t.Fatalf("PushFrame() second error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)

	detector.UpdateOptions(SimpleVADOptions{MaxBufferedSpeechDuration: 0.03})
	if err := stream.PushFrame(thirdSpeech); err != nil {
		t.Fatalf("PushFrame() third error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)
	silence := audioFrame(16000, 160, 0)
	if err := stream.PushFrame(silence); err != nil {
		t.Fatalf("PushFrame() silence error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)
	end := nextVADEvent(t, stream)
	if end.Type != VADEventEndOfSpeech {
		t.Fatalf("event type = %s, want %s", end.Type, VADEventEndOfSpeech)
	}
	assertCombinedFrames(t, end.Frames, firstSpeech, thirdSpeech, silence)
}

func TestSimpleVADUpdateOptionsShrinksBufferedSpeech(t *testing.T) {
	detector := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:                 0.05,
		MaxBufferedSpeechDuration: 0.05,
	})
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	speechFrames := []*model.AudioFrame{
		audioFrame(16000, 160, 6000),
		audioFrame(16000, 160, 6000),
		audioFrame(16000, 160, 6000),
		audioFrame(16000, 160, 6000),
	}
	for _, frame := range speechFrames {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatalf("PushFrame() speech error = %v", err)
		}
	}
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventStartOfSpeech)
	for range len(speechFrames) - 1 {
		assertEventType(t, stream, VADEventInferenceDone)
	}

	detector.UpdateOptions(SimpleVADOptions{MaxBufferedSpeechDuration: 0.02})
	if err := stream.PushFrame(audioFrame(16000, 160, 0)); err != nil {
		t.Fatalf("PushFrame() silence error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)
	end := nextVADEvent(t, stream)
	if end.Type != VADEventEndOfSpeech {
		t.Fatalf("event type = %s, want %s", end.Type, VADEventEndOfSpeech)
	}
	assertCombinedFrames(t, end.Frames, speechFrames[0], speechFrames[1])
}

func TestSimpleVADIgnoresMismatchedSampleRateFrames(t *testing.T) {
	stream, err := NewSimpleVAD(0.05).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(audioFrame(16000, 160, 6000)); err != nil {
		t.Fatalf("PushFrame() first frame error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventStartOfSpeech)

	if err := stream.PushFrame(audioFrame(8000, 80, 0)); err != nil {
		t.Fatalf("PushFrame() mismatched sample rate error = %v", err)
	}

	matchingSilence := audioFrame(16000, 160, 0)
	if err := stream.PushFrame(matchingSilence); err != nil {
		t.Fatalf("PushFrame() matching silence error = %v", err)
	}
	inference := nextVADEvent(t, stream)
	if inference.Type != VADEventInferenceDone {
		t.Fatalf("event type = %s, want %s", inference.Type, VADEventInferenceDone)
	}
	assertCombinedFrames(t, inference.Frames, matchingSilence)
	assertEventType(t, stream, VADEventEndOfSpeech)
}

func TestSimpleVADRejectsNilFrames(t *testing.T) {
	stream, err := NewSimpleVAD(0.05).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("PushFrame(nil) panicked: %v", recovered)
		}
	}()

	if err := stream.PushFrame(nil); err == nil {
		t.Fatal("PushFrame(nil) error = nil, want error")
	}
	assertNoVADEvent(t, stream)
}

func TestSimpleVADRejectsInvalidFrameMetadata(t *testing.T) {
	stream, err := NewSimpleVAD(0.05).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	for _, frame := range []*model.AudioFrame{
		audioFrame(0, 160, 6000),
		{
			Data:              audioFrame(16000, 160, 6000).Data,
			SampleRate:        16000,
			SamplesPerChannel: 160,
		},
	} {
		if err := stream.PushFrame(frame); err == nil {
			t.Fatalf("PushFrame(%#v) error = nil, want error", frame)
		}
	}
	assertNoVADEvent(t, stream)
}

func TestSimpleVADUsesConfiguredInferenceSampleRateForSampleIndex(t *testing.T) {
	stream, err := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:  0.05,
		SampleRate: 8000,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(audioFrame(16000, 160, 6000)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	inference := nextVADEvent(t, stream)
	if inference.Type != VADEventInferenceDone {
		t.Fatalf("event type = %s, want %s", inference.Type, VADEventInferenceDone)
	}
	if inference.SamplesIndex != 80 {
		t.Fatalf("SamplesIndex = %d, want 80 at configured 8 kHz inference rate", inference.SamplesIndex)
	}
	if inference.Timestamp != 0.01 {
		t.Fatalf("Timestamp = %v, want 0.01 from input duration", inference.Timestamp)
	}
	start := nextVADEvent(t, stream)
	if start.Type != VADEventStartOfSpeech {
		t.Fatalf("event type = %s, want %s", start.Type, VADEventStartOfSpeech)
	}
	if start.SamplesIndex != 80 {
		t.Fatalf("start SamplesIndex = %d, want 80", start.SamplesIndex)
	}
}

func TestSimpleVADWindowDurationBuffersUntilWindowComplete(t *testing.T) {
	stream, err := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:      0.05,
		WindowDuration: 0.032,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	firstPartial := audioFrame(16000, 160, 6000)
	secondPartial := audioFrame(16000, 352, 6000)
	if err := stream.PushFrame(firstPartial); err != nil {
		t.Fatalf("PushFrame() first partial error = %v", err)
	}
	if err := stream.PushFrame(secondPartial); err != nil {
		t.Fatalf("PushFrame() second partial error = %v", err)
	}

	inference := nextVADEvent(t, stream)
	if inference.Type != VADEventInferenceDone {
		t.Fatalf("event type = %s, want %s", inference.Type, VADEventInferenceDone)
	}
	if inference.SamplesIndex != 512 {
		t.Fatalf("SamplesIndex = %d, want 512", inference.SamplesIndex)
	}
	if inference.Timestamp != 0.032 {
		t.Fatalf("Timestamp = %v, want 0.032", inference.Timestamp)
	}
	assertCombinedFrames(t, inference.Frames, firstPartial, secondPartial)

	start := nextVADEvent(t, stream)
	if start.Type != VADEventStartOfSpeech {
		t.Fatalf("event type = %s, want %s", start.Type, VADEventStartOfSpeech)
	}
	if start.SamplesIndex != 512 {
		t.Fatalf("start SamplesIndex = %d, want 512", start.SamplesIndex)
	}
	if start.SpeechDuration != 0.032 {
		t.Fatalf("SpeechDuration = %v, want 0.032", start.SpeechDuration)
	}
	assertCombinedFrames(t, start.Frames, firstPartial, secondPartial)
}

func TestSimpleVADWindowDurationPreservesLeftoverSamples(t *testing.T) {
	stream, err := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:         0.05,
		MinSpeechDuration: 0.064,
		WindowDuration:    0.032,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	firstPush := audioFrame(16000, 800, 6000)
	secondPush := audioFrame(16000, 224, 6000)
	if err := stream.PushFrame(firstPush); err != nil {
		t.Fatalf("PushFrame() first push error = %v", err)
	}
	firstInference := nextVADEvent(t, stream)
	if firstInference.Type != VADEventInferenceDone {
		t.Fatalf("event type = %s, want %s", firstInference.Type, VADEventInferenceDone)
	}
	if firstInference.SamplesIndex != 512 {
		t.Fatalf("SamplesIndex = %d, want 512", firstInference.SamplesIndex)
	}
	assertCombinedFrames(t, firstInference.Frames, audioFrame(16000, 512, 6000))

	if err := stream.PushFrame(secondPush); err != nil {
		t.Fatalf("PushFrame() second push error = %v", err)
	}
	secondInference := nextVADEvent(t, stream)
	if secondInference.Type != VADEventInferenceDone {
		t.Fatalf("event type = %s, want %s", secondInference.Type, VADEventInferenceDone)
	}
	if secondInference.SamplesIndex != 1024 {
		t.Fatalf("SamplesIndex = %d, want 1024", secondInference.SamplesIndex)
	}
	assertCombinedFrames(t, secondInference.Frames, audioFrame(16000, 288, 6000), secondPush)

	start := nextVADEvent(t, stream)
	if start.Type != VADEventStartOfSpeech {
		t.Fatalf("event type = %s, want %s", start.Type, VADEventStartOfSpeech)
	}
	if start.SpeechDuration != 0.064 {
		t.Fatalf("SpeechDuration = %v, want 0.064", start.SpeechDuration)
	}
	assertCombinedFrames(t, start.Frames, audioFrame(16000, 512, 6000), audioFrame(16000, 288, 6000), secondPush)
}

func TestSimpleVADWindowDurationUsesInferenceDurationForSampleIndex(t *testing.T) {
	stream, err := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:      0.05,
		SampleRate:     16000,
		WindowDuration: 0.032,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	window := audioFrame(44100, 1411, 0)
	if err := stream.PushFrame(window); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}

	inference := nextVADEvent(t, stream)
	if inference.Type != VADEventInferenceDone {
		t.Fatalf("event type = %s, want %s", inference.Type, VADEventInferenceDone)
	}
	if inference.SamplesIndex != 512 {
		t.Fatalf("SamplesIndex = %d, want 512 at configured 16 kHz inference rate", inference.SamplesIndex)
	}
	if inference.Timestamp != 0.032 {
		t.Fatalf("Timestamp = %v, want 0.032", inference.Timestamp)
	}
	assertCombinedFrames(t, inference.Frames, window)
}

func TestSimpleVADWindowDurationCarriesFractionalInputSamples(t *testing.T) {
	stream, err := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:      0.05,
		SampleRate:     16000,
		WindowDuration: 0.032,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(audioFrame(44100, 7056, 0)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}

	wantSamples := []int{1411, 1411, 1411, 1411, 1412}
	for i, samples := range wantSamples {
		inference := nextVADEvent(t, stream)
		if inference.Type != VADEventInferenceDone {
			t.Fatalf("event %d type = %s, want %s", i, inference.Type, VADEventInferenceDone)
		}
		if inference.SamplesIndex != (i+1)*512 {
			t.Fatalf("event %d SamplesIndex = %d, want %d", i, inference.SamplesIndex, (i+1)*512)
		}
		assertCombinedFrames(t, inference.Frames, audioFrame(44100, samples, 0))
	}
}

func TestSimpleVADWindowDurationLargePushDoesNotBlock(t *testing.T) {
	stream, err := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:      0.05,
		WindowDuration: 0.032,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- stream.PushFrame(audioFrame(16000, 512*12, 0))
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("PushFrame() error = %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("PushFrame() blocked while queuing windowed inference events")
	}
	defer stream.Close()

	for range 12 {
		assertEventType(t, stream, VADEventInferenceDone)
	}
}

func TestSimpleVADUsesDeactivationThresholdWhileSpeaking(t *testing.T) {
	stream, err := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:             0.1,
		DeactivationThreshold: 0.05,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	speech := audioFrame(16000, 160, 6000)
	dipAboveDeactivation := audioFrame(16000, 160, 2200)
	silence := audioFrame(16000, 160, 0)
	for _, frame := range []*model.AudioFrame{speech, dipAboveDeactivation, silence} {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatalf("PushFrame() error = %v", err)
		}
	}

	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventStartOfSpeech)
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventInferenceDone)
	end := nextVADEvent(t, stream)
	if end.Type != VADEventEndOfSpeech {
		t.Fatalf("event type = %s, want %s", end.Type, VADEventEndOfSpeech)
	}
	assertCombinedFrames(t, end.Frames, speech, dipAboveDeactivation, silence)
}

func TestSimpleVADEmitsInferenceBeforeSpeechTransition(t *testing.T) {
	stream, err := NewSimpleVAD(0.05).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	frame := audioFrame(16000, 160, 6000)
	if err := stream.PushFrame(frame); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}

	inference := nextVADEvent(t, stream)
	if inference.Type != VADEventInferenceDone {
		t.Fatalf("first event type = %s, want %s", inference.Type, VADEventInferenceDone)
	}
	if inference.SamplesIndex != int(frame.SamplesPerChannel) {
		t.Fatalf("SamplesIndex = %d, want %d", inference.SamplesIndex, frame.SamplesPerChannel)
	}
	if inference.Timestamp != 0.01 {
		t.Fatalf("Timestamp = %v, want 0.01", inference.Timestamp)
	}
	if inference.Probability <= 0 {
		t.Fatalf("Probability = %v, want positive speech probability", inference.Probability)
	}
	if inference.Speaking {
		t.Fatal("inference Speaking = true before start of speech, want false")
	}
	assertCombinedFrames(t, inference.Frames, frame)

	start := nextVADEvent(t, stream)
	if start.Type != VADEventStartOfSpeech {
		t.Fatalf("second event type = %s, want %s", start.Type, VADEventStartOfSpeech)
	}
	if !start.Speaking {
		t.Fatal("start Speaking = false, want true")
	}
	assertCombinedFrames(t, start.Frames, frame)
}

func TestSimpleVADInferenceEventCopiesInputFrameData(t *testing.T) {
	stream, err := NewSimpleVAD(0.05).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	frame := audioFrame(16000, 160, 6000)
	if err := stream.PushFrame(frame); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	for i := range frame.Data {
		frame.Data[i] = 0
	}

	inference := nextVADEvent(t, stream)
	if inference.Type != VADEventInferenceDone {
		t.Fatalf("event type = %s, want %s", inference.Type, VADEventInferenceDone)
	}
	assertCombinedFrames(t, inference.Frames, audioFrame(16000, 160, 6000))
}

func TestSimpleVADPrefixBufferCopiesInputFrameData(t *testing.T) {
	stream, err := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:             0.05,
		MinSpeechDuration:     0.02,
		PrefixPaddingDuration: 0.02,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	silence := audioFrame(16000, 160, 100)
	firstSpeech := audioFrame(16000, 160, 6000)
	secondSpeech := audioFrame(16000, 160, 6000)
	if err := stream.PushFrame(silence); err != nil {
		t.Fatalf("PushFrame() silence error = %v", err)
	}
	for i := range silence.Data {
		silence.Data[i] = 0
	}
	for _, frame := range []*model.AudioFrame{firstSpeech, secondSpeech} {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatalf("PushFrame() speech error = %v", err)
		}
	}

	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventInferenceDone)
	start := nextVADEvent(t, stream)
	if start.Type != VADEventStartOfSpeech {
		t.Fatalf("event type = %s, want %s", start.Type, VADEventStartOfSpeech)
	}
	assertCombinedFrames(t, start.Frames, audioFrame(16000, 160, 100), firstSpeech, secondSpeech)
}

func TestSimpleVADEndOfSpeechIncludesAccumulatedSpeechFrames(t *testing.T) {
	stream, err := NewSimpleVAD(0.05).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	firstSpeech := audioFrame(16000, 160, 6000)
	secondSpeech := audioFrame(16000, 160, 7000)
	silence := audioFrame(16000, 160, 0)

	for _, frame := range []*model.AudioFrame{firstSpeech, secondSpeech, silence} {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatalf("PushFrame() error = %v", err)
		}
	}

	wantTypes := []VADEventType{
		VADEventInferenceDone,
		VADEventStartOfSpeech,
		VADEventInferenceDone,
		VADEventInferenceDone,
		VADEventEndOfSpeech,
	}
	var end *VADEvent
	for _, wantType := range wantTypes {
		ev := nextVADEvent(t, stream)
		if ev.Type != wantType {
			t.Fatalf("event type = %s, want %s", ev.Type, wantType)
		}
		if ev.Type == VADEventEndOfSpeech {
			end = ev
		}
	}

	if end == nil {
		t.Fatal("missing end of speech event")
	}
	if end.Speaking {
		t.Fatal("end Speaking = true, want false")
	}
	assertCombinedFrames(t, end.Frames, firstSpeech, secondSpeech, silence)
	if end.SpeechDuration != 0.02 {
		t.Fatalf("SpeechDuration = %v, want 0.02", end.SpeechDuration)
	}
	if end.SilenceDuration != 0.01 {
		t.Fatalf("SilenceDuration = %v, want 0.01", end.SilenceDuration)
	}
}

func TestSimpleVADEndOfSpeechIncludesSilenceThresholdFrames(t *testing.T) {
	stream, err := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:          0.05,
		MinSilenceDuration: 0.02,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	speech := audioFrame(16000, 160, 6000)
	firstSilence := audioFrame(16000, 160, 0)
	secondSilence := audioFrame(16000, 160, 0)
	for _, frame := range []*model.AudioFrame{speech, firstSilence, secondSilence} {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatalf("PushFrame() error = %v", err)
		}
	}

	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventStartOfSpeech)
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventInferenceDone)
	end := nextVADEvent(t, stream)
	if end.Type != VADEventEndOfSpeech {
		t.Fatalf("event type = %s, want %s", end.Type, VADEventEndOfSpeech)
	}
	assertCombinedFrames(t, end.Frames, speech, firstSilence, secondSilence)
	if end.SpeechDuration != 0.01 {
		t.Fatalf("SpeechDuration = %v, want 0.01", end.SpeechDuration)
	}
	if end.SilenceDuration != 0.02 {
		t.Fatalf("SilenceDuration = %v, want 0.02", end.SilenceDuration)
	}
}

func TestSimpleVADRequiresMinimumSpeechDurationBeforeStart(t *testing.T) {
	stream, err := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:         0.05,
		MinSpeechDuration: 0.03,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	firstSpeech := audioFrame(16000, 160, 6000)
	secondSpeech := audioFrame(16000, 160, 6000)
	thirdSpeech := audioFrame(16000, 160, 6000)
	for _, frame := range []*model.AudioFrame{firstSpeech, secondSpeech, thirdSpeech} {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatalf("PushFrame() error = %v", err)
		}
	}

	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventInferenceDone)
	start := nextVADEvent(t, stream)
	if start.Type != VADEventStartOfSpeech {
		t.Fatalf("event type = %s, want %s", start.Type, VADEventStartOfSpeech)
	}
	if start.SpeechDuration != 0.03 {
		t.Fatalf("SpeechDuration = %v, want 0.03", start.SpeechDuration)
	}
	assertCombinedFrames(t, start.Frames, firstSpeech, secondSpeech, thirdSpeech)
}

func TestSimpleVADDropsSpeechShorterThanMinimumDuration(t *testing.T) {
	stream, err := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:         0.05,
		MinSpeechDuration: 0.03,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(audioFrame(16000, 160, 6000)); err != nil {
		t.Fatalf("PushFrame() speech error = %v", err)
	}
	if err := stream.PushFrame(audioFrame(16000, 160, 0)); err != nil {
		t.Fatalf("PushFrame() silence error = %v", err)
	}

	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventInferenceDone)
	assertNoVADEvent(t, stream)
}

func TestSimpleVADRetainsShortSpeechAsPrefixPadding(t *testing.T) {
	stream, err := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:             0.05,
		MinSpeechDuration:     0.02,
		PrefixPaddingDuration: 0.03,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	shortSpeech := audioFrame(16000, 160, 6000)
	silence := audioFrame(16000, 160, 0)
	firstSpeech := audioFrame(16000, 160, 6000)
	secondSpeech := audioFrame(16000, 160, 6000)
	for _, frame := range []*model.AudioFrame{shortSpeech, silence, firstSpeech, secondSpeech} {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatalf("PushFrame() error = %v", err)
		}
	}

	for range 4 {
		assertEventType(t, stream, VADEventInferenceDone)
	}
	start := nextVADEvent(t, stream)
	if start.Type != VADEventStartOfSpeech {
		t.Fatalf("event type = %s, want %s", start.Type, VADEventStartOfSpeech)
	}
	assertCombinedFrames(t, start.Frames, shortSpeech, silence, firstSpeech, secondSpeech)
}

func TestSimpleVADRequiresMinimumSilenceDurationBeforeEnd(t *testing.T) {
	stream, err := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:          0.05,
		MinSilenceDuration: 0.03,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	speech := audioFrame(16000, 160, 6000)
	firstSilence := audioFrame(16000, 160, 0)
	secondSilence := audioFrame(16000, 160, 0)
	thirdSilence := audioFrame(16000, 160, 0)
	for _, frame := range []*model.AudioFrame{speech, firstSilence, secondSilence, thirdSilence} {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatalf("PushFrame() error = %v", err)
		}
	}

	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventStartOfSpeech)
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventInferenceDone)
	end := nextVADEvent(t, stream)
	if end.Type != VADEventEndOfSpeech {
		t.Fatalf("event type = %s, want %s", end.Type, VADEventEndOfSpeech)
	}
	if end.SpeechDuration != 0.01 {
		t.Fatalf("SpeechDuration = %v, want 0.01", end.SpeechDuration)
	}
	if end.SilenceDuration != 0.03 {
		t.Fatalf("SilenceDuration = %v, want 0.03", end.SilenceDuration)
	}
	assertCombinedFrames(t, end.Frames, speech, firstSilence, secondSilence, thirdSilence)
}

func TestSimpleVADRetainsTrailingPrefixAfterEndOfSpeech(t *testing.T) {
	stream, err := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:             0.05,
		MinSpeechDuration:     0.02,
		MinSilenceDuration:    0.02,
		PrefixPaddingDuration: 0.02,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	firstSpeech := audioFrame(16000, 160, 6000)
	secondSpeech := audioFrame(16000, 160, 6000)
	firstSilence := audioFrame(16000, 160, 0)
	secondSilence := audioFrame(16000, 160, 0)
	nextFirstSpeech := audioFrame(16000, 160, 6000)
	nextSecondSpeech := audioFrame(16000, 160, 6000)
	for _, frame := range []*model.AudioFrame{
		firstSpeech,
		secondSpeech,
		firstSilence,
		secondSilence,
		nextFirstSpeech,
		nextSecondSpeech,
	} {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatalf("PushFrame() error = %v", err)
		}
	}

	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventStartOfSpeech)
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventEndOfSpeech)
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventInferenceDone)
	start := nextVADEvent(t, stream)
	if start.Type != VADEventStartOfSpeech {
		t.Fatalf("event type = %s, want %s", start.Type, VADEventStartOfSpeech)
	}
	assertCombinedFrames(t, start.Frames, firstSilence, secondSilence, nextFirstSpeech, nextSecondSpeech)
}

func TestSimpleVADRetainsTrailingPrefixAfterEndOfSpeechAtSampleBoundary(t *testing.T) {
	stream, err := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:             0.05,
		MinSpeechDuration:     0.02,
		MinSilenceDuration:    0.02,
		PrefixPaddingDuration: 0.015,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	firstSpeech := audioFrame(16000, 160, 6000)
	secondSpeech := audioFrame(16000, 160, 6000)
	firstSilence := audioFrame(16000, 160, 100)
	secondSilence := audioFrame(16000, 160, 200)
	nextFirstSpeech := audioFrame(16000, 160, 6000)
	nextSecondSpeech := audioFrame(16000, 160, 6000)
	for _, frame := range []*model.AudioFrame{
		firstSpeech,
		secondSpeech,
		firstSilence,
		secondSilence,
		nextFirstSpeech,
		nextSecondSpeech,
	} {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatalf("PushFrame() error = %v", err)
		}
	}

	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventStartOfSpeech)
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventEndOfSpeech)
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventInferenceDone)
	start := nextVADEvent(t, stream)
	if start.Type != VADEventStartOfSpeech {
		t.Fatalf("event type = %s, want %s", start.Type, VADEventStartOfSpeech)
	}
	assertCombinedFrames(t, start.Frames, audioFrame(16000, 80, 100), secondSilence, nextFirstSpeech, nextSecondSpeech)
}

func TestSimpleVADStartOfSpeechIncludesPrefixPaddingFrames(t *testing.T) {
	stream, err := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:             0.05,
		MinSpeechDuration:     0.02,
		PrefixPaddingDuration: 0.02,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	firstSilence := audioFrame(16000, 160, 0)
	secondSilence := audioFrame(16000, 160, 0)
	firstSpeech := audioFrame(16000, 160, 6000)
	secondSpeech := audioFrame(16000, 160, 6000)
	for _, frame := range []*model.AudioFrame{firstSilence, secondSilence, firstSpeech, secondSpeech} {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatalf("PushFrame() error = %v", err)
		}
	}

	for range 4 {
		assertEventType(t, stream, VADEventInferenceDone)
	}
	start := nextVADEvent(t, stream)
	if start.Type != VADEventStartOfSpeech {
		t.Fatalf("event type = %s, want %s", start.Type, VADEventStartOfSpeech)
	}
	assertCombinedFrames(t, start.Frames, firstSilence, secondSilence, firstSpeech, secondSpeech)
}

func TestSimpleVADTrimsPrefixPaddingAtSampleBoundary(t *testing.T) {
	stream, err := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:             0.05,
		MinSpeechDuration:     0.02,
		PrefixPaddingDuration: 0.015,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	firstSilence := audioFrame(16000, 160, 100)
	secondSilence := audioFrame(16000, 160, 200)
	firstSpeech := audioFrame(16000, 160, 6000)
	secondSpeech := audioFrame(16000, 160, 6000)
	for _, frame := range []*model.AudioFrame{firstSilence, secondSilence, firstSpeech, secondSpeech} {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatalf("PushFrame() error = %v", err)
		}
	}

	for range 4 {
		assertEventType(t, stream, VADEventInferenceDone)
	}
	start := nextVADEvent(t, stream)
	if start.Type != VADEventStartOfSpeech {
		t.Fatalf("event type = %s, want %s", start.Type, VADEventStartOfSpeech)
	}
	assertCombinedFrames(t, start.Frames, audioFrame(16000, 80, 100), secondSilence, firstSpeech, secondSpeech)
}

func TestSimpleVADPrefixPaddingDoesNotConsumeMaxBufferedSpeech(t *testing.T) {
	stream, err := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:                 0.05,
		MinSpeechDuration:         0.02,
		PrefixPaddingDuration:     0.02,
		MaxBufferedSpeechDuration: 0.02,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	firstSilence := audioFrame(16000, 160, 0)
	secondSilence := audioFrame(16000, 160, 0)
	firstSpeech := audioFrame(16000, 160, 6000)
	secondSpeech := audioFrame(16000, 160, 6000)
	for _, frame := range []*model.AudioFrame{firstSilence, secondSilence, firstSpeech, secondSpeech} {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatalf("PushFrame() error = %v", err)
		}
	}

	for range 4 {
		assertEventType(t, stream, VADEventInferenceDone)
	}
	start := nextVADEvent(t, stream)
	if start.Type != VADEventStartOfSpeech {
		t.Fatalf("event type = %s, want %s", start.Type, VADEventStartOfSpeech)
	}
	assertCombinedFrames(t, start.Frames, firstSilence, secondSilence, firstSpeech, secondSpeech)
}

func TestSimpleVADLimitsBufferedSpeechFrames(t *testing.T) {
	stream, err := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:                 0.05,
		MaxBufferedSpeechDuration: 0.03,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	speechFrames := []*model.AudioFrame{
		audioFrame(16000, 160, 6000),
		audioFrame(16000, 160, 6000),
		audioFrame(16000, 160, 6000),
		audioFrame(16000, 160, 6000),
		audioFrame(16000, 160, 6000),
	}
	for _, frame := range speechFrames {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatalf("PushFrame() speech error = %v", err)
		}
	}
	if err := stream.PushFrame(audioFrame(16000, 160, 0)); err != nil {
		t.Fatalf("PushFrame() silence error = %v", err)
	}

	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventStartOfSpeech)
	for range 5 {
		assertEventType(t, stream, VADEventInferenceDone)
	}
	end := nextVADEvent(t, stream)
	if end.Type != VADEventEndOfSpeech {
		t.Fatalf("event type = %s, want %s", end.Type, VADEventEndOfSpeech)
	}
	assertCombinedFrames(t, end.Frames, speechFrames[0], speechFrames[1], speechFrames[2])
}

func TestSimpleVADLimitsBufferedSpeechAtSampleBoundary(t *testing.T) {
	stream, err := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:                 0.05,
		MaxBufferedSpeechDuration: 0.025,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	firstSpeech := audioFrame(16000, 160, 6000)
	secondSpeech := audioFrame(16000, 160, 7000)
	thirdSpeech := audioFrame(16000, 160, 8000)
	silence := audioFrame(16000, 160, 0)
	for _, frame := range []*model.AudioFrame{firstSpeech, secondSpeech, thirdSpeech, silence} {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatalf("PushFrame() error = %v", err)
		}
	}

	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventStartOfSpeech)
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventInferenceDone)
	end := nextVADEvent(t, stream)
	if end.Type != VADEventEndOfSpeech {
		t.Fatalf("event type = %s, want %s", end.Type, VADEventEndOfSpeech)
	}
	assertCombinedFrames(t, end.Frames, firstSpeech, secondSpeech, audioFrame(16000, 80, 8000))
}

func TestSimpleVADFlushResetsSegmentState(t *testing.T) {
	stream, err := NewSimpleVAD(0.05).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(audioFrame(16000, 160, 6000)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventStartOfSpeech)

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	afterFlushSilence := audioFrame(16000, 160, 0)
	if err := stream.PushFrame(afterFlushSilence); err != nil {
		t.Fatalf("PushFrame() after Flush() error = %v", err)
	}
	inference := nextVADEvent(t, stream)
	if inference.Type != VADEventInferenceDone {
		t.Fatalf("event type = %s, want %s", inference.Type, VADEventInferenceDone)
	}
	if inference.SamplesIndex != int(afterFlushSilence.SamplesPerChannel) {
		t.Fatalf("SamplesIndex after Flush() = %d, want %d", inference.SamplesIndex, afterFlushSilence.SamplesPerChannel)
	}
	if inference.Timestamp != 0.01 {
		t.Fatalf("Timestamp after Flush() = %v, want 0.01", inference.Timestamp)
	}

	if err := stream.PushFrame(audioFrame(16000, 160, 6000)); err != nil {
		t.Fatalf("PushFrame() second segment error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventStartOfSpeech)
}

func TestSimpleVADEndInputFlushesAndRejectsMoreInput(t *testing.T) {
	stream, err := NewSimpleVAD(0.05).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(audioFrame(16000, 160, 6000)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventStartOfSpeech)

	if err := stream.EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}
	if err := stream.PushFrame(audioFrame(16000, 160, 6000)); err == nil {
		t.Fatal("PushFrame() after EndInput() error = nil, want error")
	}
	if err := stream.Flush(); err == nil {
		t.Fatal("Flush() after EndInput() error = nil, want error")
	}
}

func TestSimpleVADCloseIsIdempotentAndEndsIteration(t *testing.T) {
	stream, err := NewSimpleVAD(0.05).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if err := stream.PushFrame(audioFrame(16000, 160, 6000)); err == nil {
		t.Fatal("PushFrame() after Close() error = nil, want error")
	}
	if err := stream.Flush(); err == nil {
		t.Fatal("Flush() after Close() error = nil, want error")
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next() after Close() error = %v, want io.EOF", err)
	}
}

func TestSimpleVADCloseDropsQueuedEvents(t *testing.T) {
	stream, err := NewSimpleVAD(0.05).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.PushFrame(audioFrame(16000, 160, 6000)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next() after Close() with queued events error = %v, want io.EOF", err)
	}
}

func assertEventType(t *testing.T, stream VADStream, want VADEventType) {
	t.Helper()
	ev := nextVADEvent(t, stream)
	if ev.Type != want {
		t.Fatalf("event type = %s, want %s", ev.Type, want)
	}
}

func nextVADEvent(t *testing.T, stream VADStream) *VADEvent {
	t.Helper()

	type result struct {
		ev  *VADEvent
		err error
	}
	done := make(chan result, 1)
	go func() {
		ev, err := stream.Next()
		done <- result{ev: ev, err: err}
	}()

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("Next() error = %v", result.err)
		}
		return result.ev
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for VAD event")
		return nil
	}
}

func assertNoVADEvent(t *testing.T, stream VADStream) {
	t.Helper()

	done := make(chan *VADEvent, 1)
	go func() {
		ev, _ := stream.Next()
		done <- ev
	}()

	select {
	case ev := <-done:
		t.Fatalf("unexpected VAD event: %#v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func assertCombinedFrames(t *testing.T, got []*model.AudioFrame, want ...*model.AudioFrame) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("frames len = %d, want 1 combined frame", len(got))
	}
	combined := got[0]
	if len(want) == 0 {
		if len(combined.Data) != 0 || combined.SamplesPerChannel != 0 {
			t.Fatalf("combined frame = %#v, want empty frame", combined)
		}
		return
	}
	if combined.SampleRate != want[0].SampleRate {
		t.Fatalf("combined SampleRate = %d, want %d", combined.SampleRate, want[0].SampleRate)
	}
	if combined.NumChannels != want[0].NumChannels {
		t.Fatalf("combined NumChannels = %d, want %d", combined.NumChannels, want[0].NumChannels)
	}
	var wantSamples uint32
	var wantData []byte
	for _, frame := range want {
		wantSamples += frame.SamplesPerChannel
		wantData = append(wantData, frame.Data...)
	}
	if combined.SamplesPerChannel != wantSamples {
		t.Fatalf("combined SamplesPerChannel = %d, want %d", combined.SamplesPerChannel, wantSamples)
	}
	if !bytes.Equal(combined.Data, wantData) {
		t.Fatalf("combined Data = %v, want %v", combined.Data, wantData)
	}
}

func audioFrame(sampleRate uint32, samples int, value int16) *model.AudioFrame {
	data := make([]byte, samples*2)
	for i := 0; i < samples; i++ {
		data[i*2] = byte(value)
		data[i*2+1] = byte(uint16(value) >> 8)
	}
	return &model.AudioFrame{
		Data:              data,
		SampleRate:        sampleRate,
		NumChannels:       1,
		SamplesPerChannel: uint32(samples),
	}
}
