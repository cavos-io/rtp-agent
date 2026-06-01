package silero

import (
	"bytes"
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/telemetry"
)

func TestSileroFallbackHonorsMinimumDurations(t *testing.T) {
	detector := NewSileroVAD(
		WithMinSpeechDuration(0.096),
		WithMinSilenceDuration(0.096),
		WithActivationThreshold(0.5),
	)
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	for _, frame := range []*model.AudioFrame{
		testAudioFrame(16000, 512, 6000),
		testAudioFrame(16000, 512, 6000),
		testAudioFrame(16000, 512, 6000),
		testAudioFrame(16000, 512, 0),
		testAudioFrame(16000, 512, 0),
		testAudioFrame(16000, 512, 0),
		testAudioFrame(16000, 512, 0),
	} {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatalf("PushFrame() error = %v", err)
		}
	}

	for range 3 {
		assertSileroVADEventType(t, stream, vad.VADEventInferenceDone)
	}
	assertSileroVADEventType(t, stream, vad.VADEventStartOfSpeech)
	for range 4 {
		assertSileroVADEventType(t, stream, vad.VADEventInferenceDone)
	}
	assertSileroVADEventType(t, stream, vad.VADEventEndOfSpeech)
}

func TestSileroVADMetadataAndMetrics(t *testing.T) {
	detector := NewSileroVAD()
	if detector.Label() != "silero.VAD" {
		t.Fatalf("Label() = %q, want silero.VAD", detector.Label())
	}
	if detector.Model() != "silero" {
		t.Fatalf("Model() = %q, want silero", detector.Model())
	}
	if detector.Provider() != "ONNX" {
		t.Fatalf("Provider() = %q, want ONNX", detector.Provider())
	}
	if detector.Capabilities().UpdateInterval != 0.032 {
		t.Fatalf("Capabilities().UpdateInterval = %v, want 0.032", detector.Capabilities().UpdateInterval)
	}

	metricsCh := make(chan string, 1)
	detector.OnMetricsCollected(func(metrics *telemetry.VADMetrics) {
		if metrics.Metadata == nil {
			metricsCh <- "missing metadata"
			return
		}
		metricsCh <- metrics.Label + ":" + metrics.Metadata.ModelName + ":" + metrics.Metadata.ModelProvider
	})
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	for range 32 {
		if err := stream.PushFrame(testAudioFrame(16000, 512, 6000)); err != nil {
			t.Fatalf("PushFrame() error = %v", err)
		}
		nextSileroVADEvent(t, stream)
	}
	nextSileroVADEvent(t, stream)

	select {
	case got := <-metricsCh:
		if got != "silero.VAD:silero:ONNX" {
			t.Fatalf("metrics identity = %q, want silero.VAD:silero:ONNX", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for VAD metrics")
	}
}

func TestSileroVADDerivesInitialDeactivationThreshold(t *testing.T) {
	options := NewSileroVAD(WithActivationThreshold(0.8)).options
	if options.DeactivationThreshold != 0.65 {
		t.Fatalf("DeactivationThreshold = %v, want 0.65", options.DeactivationThreshold)
	}

	options = NewSileroVAD(
		WithActivationThreshold(0.8),
		WithDeactivationThreshold(0.2),
	).options
	if options.DeactivationThreshold != 0.2 {
		t.Fatalf("DeactivationThreshold with explicit option = %v, want 0.2", options.DeactivationThreshold)
	}
}

func TestSileroVADPaddingDurationAliasesPrefixPaddingDuration(t *testing.T) {
	options := NewSileroVAD(WithPaddingDuration(0.123)).options
	if options.PrefixPaddingDuration != 0.123 {
		t.Fatalf("PrefixPaddingDuration = %v, want 0.123", options.PrefixPaddingDuration)
	}
}

func TestSileroVADHonorsExplicitZeroActivationThreshold(t *testing.T) {
	detector := NewSileroVAD(
		WithActivationThreshold(0),
		WithMinSpeechDuration(0.032),
	)
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(testAudioFrame(16000, 512, 0)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	assertSileroVADEventType(t, stream, vad.VADEventInferenceDone)
	assertSileroVADEventType(t, stream, vad.VADEventStartOfSpeech)
}

func TestSileroVADStreamRejectsUnsupportedSampleRate(t *testing.T) {
	detector := NewSileroVAD(WithSampleRate(44100))

	if _, err := detector.Stream(context.Background()); err == nil {
		t.Fatal("Stream() error = nil, want unsupported sample rate error")
	} else if !strings.Contains(err.Error(), "8KHz and 16KHz") {
		t.Fatalf("Stream() error = %q, want supported sample rate message", err.Error())
	}
}

func TestSileroVADStreamRejectsInvalidDeactivationThreshold(t *testing.T) {
	detector := NewSileroVAD(WithDeactivationThreshold(-0.1))

	if _, err := detector.Stream(context.Background()); err == nil {
		t.Fatal("Stream() error = nil, want invalid deactivation threshold error")
	} else if !strings.Contains(err.Error(), "deactivation_threshold must be greater than 0") {
		t.Fatalf("Stream() error = %q, want deactivation threshold message", err.Error())
	}
}

func TestNewSileroVADWithOptionsRejectsInvalidOptions(t *testing.T) {
	if _, err := NewSileroVADWithOptions(WithSampleRate(44100)); err == nil {
		t.Fatal("NewSileroVADWithOptions() error = nil, want unsupported sample rate error")
	} else if !strings.Contains(err.Error(), "8KHz and 16KHz") {
		t.Fatalf("NewSileroVADWithOptions() error = %q, want supported sample rate message", err.Error())
	}

	if _, err := NewSileroVADWithOptions(WithDeactivationThreshold(-0.1)); err == nil {
		t.Fatal("NewSileroVADWithOptions() error = nil, want invalid deactivation threshold error")
	} else if !strings.Contains(err.Error(), "deactivation_threshold must be greater than 0") {
		t.Fatalf("NewSileroVADWithOptions() error = %q, want deactivation threshold message", err.Error())
	}

	if _, err := NewSileroVADWithOptions(WithDeactivationThreshold(math.NaN())); err == nil {
		t.Fatal("NewSileroVADWithOptions() error = nil, want invalid deactivation threshold error")
	} else if !strings.Contains(err.Error(), "deactivation_threshold must be greater than 0") {
		t.Fatalf("NewSileroVADWithOptions() error = %q, want deactivation threshold message", err.Error())
	}
}

func TestSileroVADSampleRateControlsInferenceSampleIndex(t *testing.T) {
	detector := NewSileroVAD(
		WithSampleRate(8000),
		WithMinSpeechDuration(0.01),
		WithActivationThreshold(0.5),
	)
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(testAudioFrame(16000, 512, 6000)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	inference := nextSileroVADEvent(t, stream)
	if inference.Type != vad.VADEventInferenceDone {
		t.Fatalf("event type = %s, want %s", inference.Type, vad.VADEventInferenceDone)
	}
	if inference.SamplesIndex != 256 {
		t.Fatalf("SamplesIndex = %d, want 256 at configured 8 kHz sample rate", inference.SamplesIndex)
	}
	start := nextSileroVADEvent(t, stream)
	if start.Type != vad.VADEventStartOfSpeech {
		t.Fatalf("event type = %s, want %s", start.Type, vad.VADEventStartOfSpeech)
	}
	if start.SamplesIndex != 256 {
		t.Fatalf("start SamplesIndex = %d, want 256", start.SamplesIndex)
	}
}

func TestSileroVADUpdateOptionsDoesNotChangeSampleRate(t *testing.T) {
	detector := NewSileroVAD(
		WithSampleRate(16000),
		WithMinSpeechDuration(0.032),
		WithActivationThreshold(0.5),
	)
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	detector.UpdateOptions(VADOptions{SampleRate: 8000})
	if detector.options.SampleRate != 16000 {
		t.Fatalf("detector SampleRate = %d, want 16000", detector.options.SampleRate)
	}
	if err := stream.PushFrame(testAudioFrame(16000, 512, 6000)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	inference := nextSileroVADEvent(t, stream)
	if inference.Type != vad.VADEventInferenceDone {
		t.Fatalf("event type = %s, want %s", inference.Type, vad.VADEventInferenceDone)
	}
	if inference.SamplesIndex != 512 {
		t.Fatalf("SamplesIndex after sample-rate update = %d, want 512", inference.SamplesIndex)
	}
	assertSileroVADEventType(t, stream, vad.VADEventStartOfSpeech)
}

func TestSileroVADUpdateOptionsWithDoesNotChangeSampleRate(t *testing.T) {
	detector := NewSileroVAD(
		WithSampleRate(16000),
		WithMinSpeechDuration(0.032),
		WithActivationThreshold(0.5),
	)
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	detector.UpdateOptionsWith(WithSampleRate(8000))
	if detector.options.SampleRate != 16000 {
		t.Fatalf("detector SampleRate = %d, want 16000", detector.options.SampleRate)
	}
	if err := stream.PushFrame(testAudioFrame(16000, 512, 6000)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	inference := nextSileroVADEvent(t, stream)
	if inference.Type != vad.VADEventInferenceDone {
		t.Fatalf("event type = %s, want %s", inference.Type, vad.VADEventInferenceDone)
	}
	if inference.SamplesIndex != 512 {
		t.Fatalf("SamplesIndex after sample-rate update = %d, want 512", inference.SamplesIndex)
	}
	assertSileroVADEventType(t, stream, vad.VADEventStartOfSpeech)
}

func TestSileroVADUpdateOptionsDoesNotChangeUpdateInterval(t *testing.T) {
	detector := NewSileroVAD(
		WithMinSpeechDuration(0.032),
		WithActivationThreshold(0.5),
	)
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	detector.UpdateOptions(VADOptions{UpdateInterval: 0.016})
	if detector.Capabilities().UpdateInterval != 0.032 {
		t.Fatalf("Capabilities().UpdateInterval = %v, want 0.032", detector.Capabilities().UpdateInterval)
	}
	if err := stream.PushFrame(testAudioFrame(16000, 512, 6000)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	inference := nextSileroVADEvent(t, stream)
	if inference.Type != vad.VADEventInferenceDone {
		t.Fatalf("event type = %s, want %s", inference.Type, vad.VADEventInferenceDone)
	}
	if inference.SamplesIndex != 512 {
		t.Fatalf("SamplesIndex after update interval update = %d, want 512", inference.SamplesIndex)
	}
	assertSileroVADEventType(t, stream, vad.VADEventStartOfSpeech)
}

func TestSileroVADUpdateOptionsWithDoesNotChangeUpdateInterval(t *testing.T) {
	detector := NewSileroVAD(
		WithMinSpeechDuration(0.032),
		WithActivationThreshold(0.5),
	)
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	detector.UpdateOptionsWith(func(options *VADOptions) {
		options.UpdateInterval = 0.016
	})
	if detector.Capabilities().UpdateInterval != 0.032 {
		t.Fatalf("Capabilities().UpdateInterval = %v, want 0.032", detector.Capabilities().UpdateInterval)
	}
	if err := stream.PushFrame(testAudioFrame(16000, 512, 6000)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	inference := nextSileroVADEvent(t, stream)
	if inference.Type != vad.VADEventInferenceDone {
		t.Fatalf("event type = %s, want %s", inference.Type, vad.VADEventInferenceDone)
	}
	if inference.SamplesIndex != 512 {
		t.Fatalf("SamplesIndex after update interval update = %d, want 512", inference.SamplesIndex)
	}
	assertSileroVADEventType(t, stream, vad.VADEventStartOfSpeech)
}

func TestSileroVADUpdateOptionsIgnoresInvalidDeactivationThreshold(t *testing.T) {
	detector := NewSileroVAD(
		WithDeactivationThreshold(0.2),
		WithMinSpeechDuration(0.032),
		WithActivationThreshold(0.5),
	)

	detector.UpdateOptions(VADOptions{DeactivationThreshold: -0.1})
	if detector.options.DeactivationThreshold != 0.2 {
		t.Fatalf("detector DeactivationThreshold = %v, want 0.2", detector.options.DeactivationThreshold)
	}

	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() after invalid update error = %v", err)
	}
	defer stream.Close()
}

func TestSileroVADUpdateOptionsWithIgnoresInvalidDeactivationThreshold(t *testing.T) {
	detector := NewSileroVAD(
		WithDeactivationThreshold(0.2),
		WithMinSpeechDuration(0.032),
		WithActivationThreshold(0.5),
	)

	detector.UpdateOptionsWith(WithDeactivationThreshold(-0.1))
	if detector.options.DeactivationThreshold != 0.2 {
		t.Fatalf("detector DeactivationThreshold = %v, want 0.2", detector.options.DeactivationThreshold)
	}

	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() after invalid update error = %v", err)
	}
	defer stream.Close()
}

func TestSileroVADUpdateOptionsIgnoresNonFiniteDeactivationThreshold(t *testing.T) {
	detector := NewSileroVAD(
		WithDeactivationThreshold(0.2),
		WithMinSpeechDuration(0.032),
		WithActivationThreshold(0.5),
	)

	detector.UpdateOptions(VADOptions{DeactivationThreshold: math.Inf(1)})
	if detector.options.DeactivationThreshold != 0.2 {
		t.Fatalf("detector DeactivationThreshold = %v, want 0.2", detector.options.DeactivationThreshold)
	}

	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() after invalid update error = %v", err)
	}
	defer stream.Close()
}

func TestSileroVADBuffersDefaultInferenceWindow(t *testing.T) {
	detector := NewSileroVAD(
		WithMinSpeechDuration(0.032),
		WithActivationThreshold(0.5),
	)
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	firstPartial := testAudioFrame(16000, 160, 6000)
	secondPartial := testAudioFrame(16000, 352, 6000)
	if err := stream.PushFrame(firstPartial); err != nil {
		t.Fatalf("PushFrame() first partial error = %v", err)
	}
	if err := stream.PushFrame(secondPartial); err != nil {
		t.Fatalf("PushFrame() second partial error = %v", err)
	}

	inference := nextSileroVADEvent(t, stream)
	if inference.Type != vad.VADEventInferenceDone {
		t.Fatalf("event type = %s, want %s", inference.Type, vad.VADEventInferenceDone)
	}
	if inference.SamplesIndex != 512 {
		t.Fatalf("SamplesIndex = %d, want 512", inference.SamplesIndex)
	}
	if inference.Timestamp != 0.032 {
		t.Fatalf("Timestamp = %v, want 0.032", inference.Timestamp)
	}
	assertCombinedSileroFrames(t, inference.Frames, firstPartial, secondPartial)
	assertSileroVADEventType(t, stream, vad.VADEventStartOfSpeech)
}

func TestSileroVADUpdateOptionsAppliesToActiveStream(t *testing.T) {
	detector := NewSileroVAD(
		WithMinSpeechDuration(0.064),
		WithActivationThreshold(0.5),
	)
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(testAudioFrame(16000, 512, 6000)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	assertSileroVADEventType(t, stream, vad.VADEventInferenceDone)

	detector.UpdateOptions(VADOptions{MinSpeechDuration: 0.032})
	if err := stream.PushFrame(testAudioFrame(16000, 512, 6000)); err != nil {
		t.Fatalf("PushFrame() after UpdateOptions() error = %v", err)
	}
	assertSileroVADEventType(t, stream, vad.VADEventInferenceDone)
	assertSileroVADEventType(t, stream, vad.VADEventStartOfSpeech)
}

func TestSileroVADUpdateOptionsWithAllowsZeroMinSpeechDuration(t *testing.T) {
	detector := NewSileroVAD(
		WithMinSpeechDuration(0.064),
		WithActivationThreshold(0.5),
	)
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	detector.UpdateOptionsWith(WithMinSpeechDuration(0))
	if err := stream.PushFrame(testAudioFrame(16000, 512, 6000)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	assertSileroVADEventType(t, stream, vad.VADEventInferenceDone)
	assertSileroVADEventType(t, stream, vad.VADEventStartOfSpeech)
}

func TestSileroVADHonorsExplicitZeroMaxBufferedSpeech(t *testing.T) {
	detector := NewSileroVAD(
		WithPrefixPaddingDuration(0.032),
		WithMaxBufferedSpeech(0),
		WithMinSpeechDuration(0.032),
		WithActivationThreshold(0.5),
	)
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	prefix := testAudioFrame(16000, 512, 0)
	if err := stream.PushFrame(prefix); err != nil {
		t.Fatalf("PushFrame() prefix error = %v", err)
	}
	assertSileroVADEventType(t, stream, vad.VADEventInferenceDone)

	if err := stream.PushFrame(testAudioFrame(16000, 512, 6000)); err != nil {
		t.Fatalf("PushFrame() speech error = %v", err)
	}
	assertSileroVADEventType(t, stream, vad.VADEventInferenceDone)
	start := nextSileroVADEvent(t, stream)
	if start.Type != vad.VADEventStartOfSpeech {
		t.Fatalf("event type = %s, want %s", start.Type, vad.VADEventStartOfSpeech)
	}
	assertCombinedSileroFrames(t, start.Frames, prefix)
}

func TestSileroVADActivationUpdatePreservesDeactivationThreshold(t *testing.T) {
	detector := NewSileroVAD(
		WithMinSpeechDuration(0.01),
		WithMinSilenceDuration(0.01),
		WithActivationThreshold(0.5),
	)
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	speech := testAudioFrame(16000, 512, 6000)
	if err := stream.PushFrame(speech); err != nil {
		t.Fatalf("PushFrame() speech error = %v", err)
	}
	assertSileroVADEventType(t, stream, vad.VADEventInferenceDone)
	assertSileroVADEventType(t, stream, vad.VADEventStartOfSpeech)

	detector.UpdateOptions(VADOptions{ActivationThreshold: 0.8})
	dipAboveOriginalDeactivation := testAudioFrame(16000, 512, 1800)
	if err := stream.PushFrame(dipAboveOriginalDeactivation); err != nil {
		t.Fatalf("PushFrame() dip error = %v", err)
	}
	assertSileroVADEventType(t, stream, vad.VADEventInferenceDone)

	silence := testAudioFrame(16000, 512, 0)
	if err := stream.PushFrame(silence); err != nil {
		t.Fatalf("PushFrame() silence error = %v", err)
	}
	assertSileroVADEventType(t, stream, vad.VADEventInferenceDone)
	end := nextSileroVADEvent(t, stream)
	if end.Type != vad.VADEventEndOfSpeech {
		t.Fatalf("event type = %s, want %s", end.Type, vad.VADEventEndOfSpeech)
	}
	assertCombinedSileroFrames(t, end.Frames, speech, dipAboveOriginalDeactivation, silence)
}

func TestSileroFallbackHonorsBufferingOptions(t *testing.T) {
	detector := NewSileroVAD(
		WithPrefixPaddingDuration(0.064),
		WithMaxBufferedSpeech(0.064),
		WithMinSpeechDuration(0.064),
		WithActivationThreshold(0.5),
	)
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	frames := []*model.AudioFrame{
		testAudioFrame(16000, 512, 0),
		testAudioFrame(16000, 512, 0),
		testAudioFrame(16000, 512, 6000),
		testAudioFrame(16000, 512, 6000),
		testAudioFrame(16000, 512, 6000),
	}
	for _, frame := range frames {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatalf("PushFrame() error = %v", err)
		}
	}

	for range 4 {
		assertSileroVADEventType(t, stream, vad.VADEventInferenceDone)
	}
	start := nextSileroVADEvent(t, stream)
	if start.Type != vad.VADEventStartOfSpeech {
		t.Fatalf("event type = %s, want %s", start.Type, vad.VADEventStartOfSpeech)
	}
	assertCombinedSileroFrames(t, start.Frames, frames[:4]...)
}

func assertSileroVADEventType(t *testing.T, stream vad.VADStream, want vad.VADEventType) {
	t.Helper()
	event := nextSileroVADEvent(t, stream)
	if event.Type != want {
		t.Fatalf("event type = %s, want %s", event.Type, want)
	}
}

func nextSileroVADEvent(t *testing.T, stream vad.VADStream) *vad.VADEvent {
	t.Helper()

	done := make(chan struct {
		event *vad.VADEvent
		err   error
	}, 1)
	go func() {
		event, err := stream.Next()
		done <- struct {
			event *vad.VADEvent
			err   error
		}{event: event, err: err}
	}()

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("Next() error = %v", result.err)
		}
		return result.event
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for VAD event")
		return nil
	}
}

func assertCombinedSileroFrames(t *testing.T, got []*model.AudioFrame, want ...*model.AudioFrame) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("frames len = %d, want 1 combined frame", len(got))
	}
	combined := got[0]
	if combined.SampleRate != want[0].SampleRate {
		t.Fatalf("combined SampleRate = %d, want %d", combined.SampleRate, want[0].SampleRate)
	}
	var samples uint32
	var data []byte
	for _, frame := range want {
		samples += frame.SamplesPerChannel
		data = append(data, frame.Data...)
	}
	if combined.SamplesPerChannel != samples {
		t.Fatalf("combined SamplesPerChannel = %d, want %d", combined.SamplesPerChannel, samples)
	}
	if !bytes.Equal(combined.Data, data) {
		t.Fatalf("combined Data = %v, want %v", combined.Data, data)
	}
}

func testAudioFrame(sampleRate uint32, samples int, value int16) *model.AudioFrame {
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
