package ten

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

func TestVADDefaultsMatchTenReferenceFrameContract(t *testing.T) {
	detector := NewVAD()
	if detector.Label() != "ten.VAD" {
		t.Fatalf("Label() = %q, want ten.VAD", detector.Label())
	}
	if detector.Model() != "ten-vad" {
		t.Fatalf("Model() = %q, want ten-vad", detector.Model())
	}
	if detector.Provider() != "TEN" {
		t.Fatalf("Provider() = %q, want TEN", detector.Provider())
	}
	if detector.Capabilities().UpdateInterval != 0.016 {
		t.Fatalf("Capabilities().UpdateInterval = %v, want 0.016", detector.Capabilities().UpdateInterval)
	}

	options := detector.options
	if options.SampleRate != 16000 {
		t.Fatalf("SampleRate = %d, want 16000", options.SampleRate)
	}
	if options.HopSize != 256 {
		t.Fatalf("HopSize = %d, want 256", options.HopSize)
	}
	if options.ActivationThreshold != 0.5 {
		t.Fatalf("ActivationThreshold = %v, want 0.5", options.ActivationThreshold)
	}
	if options.DeactivationThreshold != 0.5 {
		t.Fatalf("DeactivationThreshold = %v, want 0.5", options.DeactivationThreshold)
	}
}

func TestVADUsesProbabilityEstimatorPerStream(t *testing.T) {
	originalFactory := newProbabilityEstimatorFactory
	defer func() { newProbabilityEstimatorFactory = originalFactory }()

	var created int
	newProbabilityEstimatorFactory = func(options VADOptions) (vad.ProbabilityEstimatorFactory, error) {
		created++
		if options.LibraryPath != "libten_vad.so" {
			t.Fatalf("LibraryPath = %q, want libten_vad.so", options.LibraryPath)
		}
		if options.ModelPath != "ten-vad.onnx" {
			t.Fatalf("ModelPath = %q, want ten-vad.onnx", options.ModelPath)
		}
		if options.HopSize != 256 {
			t.Fatalf("HopSize = %d, want 256", options.HopSize)
		}
		return func() vad.ProbabilityEstimator {
			used := false
			return func(*model.AudioFrame) (float64, error) {
				if used {
					return 0, nil
				}
				used = true
				return 0.9, nil
			}
		}, nil
	}

	detector, err := NewVADWithOptions(
		WithNativeLibrary("libten_vad.so"),
		WithModelPath("ten-vad.onnx"),
		WithActivationThreshold(0.5),
		WithMinSpeechDuration(0.016),
	)
	if err != nil {
		t.Fatalf("NewVADWithOptions() error = %v", err)
	}
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(testAudioFrame(16000, 256, 0)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}

	assertVADEventType(t, stream, vad.VADEventInferenceDone)
	assertVADEventType(t, stream, vad.VADEventStartOfSpeech)
	if created != 1 {
		t.Fatalf("estimator factories = %d, want 1", created)
	}
}

func TestVADMetadataAndMetrics(t *testing.T) {
	detector := NewVAD(
		WithMinSpeechDuration(0.016),
		WithActivationThreshold(0.5),
	)
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

	for range 63 {
		if err := stream.PushFrame(testAudioFrame(16000, 256, 6000)); err != nil {
			t.Fatalf("PushFrame() error = %v", err)
		}
		nextVADEvent(t, stream)
	}
	nextVADEvent(t, stream)

	select {
	case got := <-metricsCh:
		if got != "ten.VAD:ten-vad:TEN" {
			t.Fatalf("metrics identity = %q, want ten.VAD:ten-vad:TEN", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for VAD metrics")
	}
}

func TestVADRejectsUnsupportedSampleRate(t *testing.T) {
	detector := NewVAD(WithSampleRate(8000))

	if _, err := detector.Stream(context.Background()); err == nil {
		t.Fatal("Stream() error = nil, want unsupported sample rate error")
	} else if !strings.Contains(err.Error(), "TEN VAD only supports 16KHz") {
		t.Fatalf("Stream() error = %q, want supported sample rate message", err.Error())
	}
}

func TestVADRejectsInvalidOptions(t *testing.T) {
	if _, err := NewVADWithOptions(WithSampleRate(8000)); err == nil {
		t.Fatal("NewVADWithOptions() error = nil, want unsupported sample rate error")
	} else if !strings.Contains(err.Error(), "TEN VAD only supports 16KHz") {
		t.Fatalf("NewVADWithOptions() error = %q, want supported sample rate message", err.Error())
	}

	if _, err := NewVADWithOptions(WithHopSize(0)); err == nil {
		t.Fatal("NewVADWithOptions() error = nil, want invalid hop size error")
	} else if !strings.Contains(err.Error(), "hop_size must be greater than 0") {
		t.Fatalf("NewVADWithOptions() error = %q, want hop size message", err.Error())
	}

	if _, err := NewVADWithOptions(WithActivationThreshold(math.NaN())); err == nil {
		t.Fatal("NewVADWithOptions() error = nil, want invalid activation threshold error")
	} else if !strings.Contains(err.Error(), "activation_threshold must be between 0 and 1") {
		t.Fatalf("NewVADWithOptions() error = %q, want activation threshold message", err.Error())
	}

	if _, err := NewVADWithOptions(WithDeactivationThreshold(-0.1)); err == nil {
		t.Fatal("NewVADWithOptions() error = nil, want invalid deactivation threshold error")
	} else if !strings.Contains(err.Error(), "deactivation_threshold must be between 0 and 1") {
		t.Fatalf("NewVADWithOptions() error = %q, want deactivation threshold message", err.Error())
	}
}

func TestVADHonorsExplicitZeroActivationThreshold(t *testing.T) {
	detector := NewVAD(
		WithActivationThreshold(0),
		WithMinSpeechDuration(0.016),
	)
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(testAudioFrame(16000, 256, 0)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	assertVADEventType(t, stream, vad.VADEventInferenceDone)
	assertVADEventType(t, stream, vad.VADEventStartOfSpeech)
}

func TestVADBuffersTenDefaultInferenceWindow(t *testing.T) {
	detector := NewVAD(
		WithMinSpeechDuration(0.016),
		WithActivationThreshold(0.5),
	)
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	firstPartial := testAudioFrame(16000, 100, 6000)
	secondPartial := testAudioFrame(16000, 156, 6000)
	if err := stream.PushFrame(firstPartial); err != nil {
		t.Fatalf("PushFrame() first partial error = %v", err)
	}
	if err := stream.PushFrame(secondPartial); err != nil {
		t.Fatalf("PushFrame() second partial error = %v", err)
	}

	inference := nextVADEvent(t, stream)
	if inference.Type != vad.VADEventInferenceDone {
		t.Fatalf("event type = %s, want %s", inference.Type, vad.VADEventInferenceDone)
	}
	if inference.SamplesIndex != 256 {
		t.Fatalf("SamplesIndex = %d, want 256", inference.SamplesIndex)
	}
	if inference.Timestamp != 0.016 {
		t.Fatalf("Timestamp = %v, want 0.016", inference.Timestamp)
	}
	assertCombinedFrames(t, inference.Frames, firstPartial, secondPartial)
	assertVADEventType(t, stream, vad.VADEventStartOfSpeech)
}

func TestVADHonorsBufferingOptions(t *testing.T) {
	detector := NewVAD(
		WithPrefixPaddingDuration(0.032),
		WithMaxBufferedSpeech(0.016),
		WithMinSpeechDuration(0.032),
		WithActivationThreshold(0.5),
	)
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	frames := []*model.AudioFrame{
		testAudioFrame(16000, 256, 0),
		testAudioFrame(16000, 256, 0),
		testAudioFrame(16000, 256, 6000),
		testAudioFrame(16000, 256, 6000),
		testAudioFrame(16000, 256, 6000),
	}
	for _, frame := range frames {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatalf("PushFrame() error = %v", err)
		}
	}

	for range 4 {
		assertVADEventType(t, stream, vad.VADEventInferenceDone)
	}
	start := nextVADEvent(t, stream)
	if start.Type != vad.VADEventStartOfSpeech {
		t.Fatalf("event type = %s, want %s", start.Type, vad.VADEventStartOfSpeech)
	}
	assertCombinedFrames(t, start.Frames, frames[0], frames[1], frames[2])
}

func TestVADPaddingDurationAliasesPrefixPaddingDuration(t *testing.T) {
	options := NewVAD(WithPaddingDuration(0.123)).options
	if options.PrefixPaddingDuration != 0.123 {
		t.Fatalf("PrefixPaddingDuration = %v, want 0.123", options.PrefixPaddingDuration)
	}
}

func TestVADCustomUpdateIntervalReportsCapability(t *testing.T) {
	detector := NewVAD(WithUpdateInterval(0.032))
	if detector.Capabilities().UpdateInterval != 0.032 {
		t.Fatalf("Capabilities().UpdateInterval = %v, want 0.032", detector.Capabilities().UpdateInterval)
	}
}

func TestVADUpdateOptionsAppliesToActiveStream(t *testing.T) {
	detector := NewVAD(
		WithMinSpeechDuration(0.032),
		WithActivationThreshold(0.5),
	)
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(testAudioFrame(16000, 256, 6000)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	assertVADEventType(t, stream, vad.VADEventInferenceDone)

	detector.UpdateOptions(VADOptions{MinSpeechDuration: 0.016})
	if err := stream.PushFrame(testAudioFrame(16000, 256, 6000)); err != nil {
		t.Fatalf("PushFrame() after UpdateOptions() error = %v", err)
	}
	assertVADEventType(t, stream, vad.VADEventInferenceDone)
	assertVADEventType(t, stream, vad.VADEventStartOfSpeech)
}

func TestVADUpdateOptionsWithAppliesToActiveStream(t *testing.T) {
	detector := NewVAD(
		WithMinSpeechDuration(0.032),
		WithMinSilenceDuration(0.032),
		WithActivationThreshold(0.5),
	)
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(testAudioFrame(16000, 256, 6000)); err != nil {
		t.Fatalf("PushFrame() speech error = %v", err)
	}
	assertVADEventType(t, stream, vad.VADEventInferenceDone)

	detector.UpdateOptionsWith(WithMinSpeechDuration(0.016), WithMinSilenceDuration(0))
	if err := stream.PushFrame(testAudioFrame(16000, 256, 6000)); err != nil {
		t.Fatalf("PushFrame() second speech error = %v", err)
	}
	assertVADEventType(t, stream, vad.VADEventInferenceDone)
	assertVADEventType(t, stream, vad.VADEventStartOfSpeech)

	if err := stream.PushFrame(testAudioFrame(16000, 256, 0)); err != nil {
		t.Fatalf("PushFrame() silence error = %v", err)
	}
	assertVADEventType(t, stream, vad.VADEventInferenceDone)
	assertVADEventType(t, stream, vad.VADEventEndOfSpeech)
}

func TestVADUpdateOptionsDoesNotChangeSampleRateOrHopSize(t *testing.T) {
	detector := NewVAD(
		WithSampleRate(16000),
		WithHopSize(256),
		WithMinSpeechDuration(0.016),
	)
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	detector.UpdateOptions(VADOptions{SampleRate: 8000, HopSize: 160})
	if detector.options.SampleRate != 16000 {
		t.Fatalf("detector SampleRate = %d, want 16000", detector.options.SampleRate)
	}
	if detector.options.HopSize != 256 {
		t.Fatalf("detector HopSize = %d, want 256", detector.options.HopSize)
	}
}

func assertVADEventType(t *testing.T, stream vad.VADStream, want vad.VADEventType) {
	t.Helper()
	event := nextVADEvent(t, stream)
	if event.Type != want {
		t.Fatalf("event type = %s, want %s", event.Type, want)
	}
}

func nextVADEvent(t *testing.T, stream vad.VADStream) *vad.VADEvent {
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

func assertCombinedFrames(t *testing.T, got []*model.AudioFrame, want ...*model.AudioFrame) {
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
