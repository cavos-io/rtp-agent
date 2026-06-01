package vad

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/library/telemetry"
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

func TestSimpleVADMetricsHandlerCanCloseStream(t *testing.T) {
	detector := NewSimpleVADWithOptions(SimpleVADOptions{UpdateInterval: 1})
	var stream VADStream
	closed := make(chan struct{}, 1)
	detector.OnMetricsCollected(func(metrics *telemetry.VADMetrics) {
		if stream != nil {
			_ = stream.Close()
			closed <- struct{}{}
		}
	})

	var err error
	stream, err = detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- stream.PushFrame(audioFrame(16000, 160, 6000))
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("PushFrame() error = %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("PushFrame() blocked while metrics handler closed stream")
	}

	select {
	case <-closed:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for metrics handler to close stream")
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next() after metrics handler close error = %v, want io.EOF", err)
	}
}

func TestSimpleVADMetricsHandlerDoesNotBlockPushFrame(t *testing.T) {
	detector := NewSimpleVADWithOptions(SimpleVADOptions{UpdateInterval: 1})
	release := make(chan struct{})
	handlerStarted := make(chan struct{}, 1)
	detector.OnMetricsCollected(func(metrics *telemetry.VADMetrics) {
		handlerStarted <- struct{}{}
		<-release
	})
	defer close(release)

	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	done := make(chan error, 1)
	go func() {
		done <- stream.PushFrame(audioFrame(16000, 160, 6000))
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("PushFrame() error = %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("PushFrame() blocked on metrics handler")
	}

	select {
	case <-handlerStarted:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for metrics handler")
	}
}

func TestSimpleVADMetricsHandlerPanicDoesNotStopOtherHandlers(t *testing.T) {
	detector := NewSimpleVADWithOptions(SimpleVADOptions{UpdateInterval: 1})
	metricsCh := make(chan *telemetry.VADMetrics, 1)
	detector.OnMetricsCollected(func(metrics *telemetry.VADMetrics) {
		panic("metrics handler panic")
	})
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
	case <-metricsCh:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for second metrics handler")
	}
}

func TestSimpleVADMetricsIdleTimeStartsAtStreamCreation(t *testing.T) {
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
	time.Sleep(20 * time.Millisecond)

	if err := stream.PushFrame(audioFrame(16000, 160, 0)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)

	select {
	case metrics := <-metricsCh:
		if metrics.IdleTime < 0.01 {
			t.Fatalf("metrics IdleTime = %v, want at least 0.01s since stream creation", metrics.IdleTime)
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

func TestSimpleVADRejectsInputAfterContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := NewSimpleVAD(0.05).Stream(ctx)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	cancel()

	if err := stream.PushFrame(audioFrame(16000, 160, 6000)); !errors.Is(err, context.Canceled) {
		t.Fatalf("PushFrame() after context cancel error = %v, want context.Canceled", err)
	}
	if err := stream.Flush(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Flush() after context cancel error = %v, want context.Canceled", err)
	}
	if err := stream.EndInput(); !errors.Is(err, context.Canceled) {
		t.Fatalf("EndInput() after context cancel error = %v, want context.Canceled", err)
	}
	if _, err := stream.Next(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Next() after context cancel error = %v, want context.Canceled", err)
	}
}

func TestSimpleVADRejectsCanceledContextAtStreamCreation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	detector := NewSimpleVAD(0.05)

	stream, err := detector.Stream(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Stream() with canceled context error = %v, want context.Canceled", err)
	}
	if stream != nil {
		t.Fatalf("Stream() with canceled context returned stream %T, want nil", stream)
	}
	if len(detector.streams) != 0 {
		t.Fatalf("registered streams after canceled context = %d, want 0", len(detector.streams))
	}
}

func TestSimpleVADContextCancelStopsIterationBeforeQueuedEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := NewSimpleVAD(0.05).Stream(ctx)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(audioFrame(16000, 160, 6000)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	cancel()

	if _, err := stream.Next(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Next() after context cancel with queued events error = %v, want context.Canceled", err)
	}
}

func TestSimpleVADCloseEndsIterationAfterContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := NewSimpleVAD(0.05).Stream(ctx)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	cancel()
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() after context cancel error = %v", err)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next() after Close() and context cancel error = %v, want io.EOF", err)
	}
}

func TestSimpleVADContextCancelUnregistersStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	detector := NewSimpleVAD(0.05)
	stream, err := detector.Stream(ctx)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()
	registeredStreams := func() int {
		detector.mu.RLock()
		defer detector.mu.RUnlock()
		return len(detector.streams)
	}

	if got := registeredStreams(); got != 1 {
		t.Fatalf("registered streams before cancel = %d, want 1", got)
	}
	cancel()

	deadline := time.Now().Add(time.Second)
	for registeredStreams() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := registeredStreams(); got != 0 {
		t.Fatalf("registered streams after context cancel = %d, want 0", got)
	}
}

func TestSimpleVADEndInputDrainsQueuedEvents(t *testing.T) {
	stream, err := NewSimpleVAD(0.05).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	frame := audioFrame(16000, 160, 6000)
	if err := stream.PushFrame(frame); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	if err := stream.EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}

	inference := nextVADEvent(t, stream)
	if inference.Type != VADEventInferenceDone {
		t.Fatalf("event type = %s, want %s", inference.Type, VADEventInferenceDone)
	}
	assertCombinedFrames(t, inference.Frames, frame)
	start := nextVADEvent(t, stream)
	if start.Type != VADEventStartOfSpeech {
		t.Fatalf("event type = %s, want %s", start.Type, VADEventStartOfSpeech)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next() after draining EndInput events error = %v, want io.EOF", err)
	}
}

func TestSimpleVADCloseAfterEndInputDropsQueuedEvents(t *testing.T) {
	stream, err := NewSimpleVAD(0.05).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	if err := stream.PushFrame(audioFrame(16000, 160, 6000)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	if err := stream.EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() after EndInput() error = %v", err)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next() after Close() and EndInput() error = %v, want io.EOF", err)
	}
}

func TestNewSimpleVADWithAllowsExplicitZeroThreshold(t *testing.T) {
	detector := NewSimpleVADWith(
		WithThreshold(0),
		WithMinSpeechDuration(0),
	)
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(audioFrame(16000, 160, 0)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventStartOfSpeech)
}

func TestSimpleVADUpdateOptionsWithAllowsZeroThreshold(t *testing.T) {
	detector := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:         0.05,
		MinSpeechDuration: 0,
	})
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	detector.UpdateOptionsWith(WithThreshold(0))
	if err := stream.PushFrame(audioFrame(16000, 160, 0)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventStartOfSpeech)
}

func TestSimpleVADUpdateOptionsWithAllowsZeroMinSpeechDuration(t *testing.T) {
	detector := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:         0.05,
		MinSpeechDuration: 0.02,
	})
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	detector.UpdateOptionsWith(WithMinSpeechDuration(0))
	if err := stream.PushFrame(audioFrame(16000, 160, 6000)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventStartOfSpeech)
}

func TestSimpleVADUpdateOptionsWithAllowsZeroMinSilenceDuration(t *testing.T) {
	detector := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:          0.05,
		MinSilenceDuration: 0.03,
	})
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	speech := audioFrame(16000, 160, 6000)
	if err := stream.PushFrame(speech); err != nil {
		t.Fatalf("PushFrame() speech error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventStartOfSpeech)

	detector.UpdateOptionsWith(WithMinSilenceDuration(0))
	silence := audioFrame(16000, 160, 0)
	if err := stream.PushFrame(silence); err != nil {
		t.Fatalf("PushFrame() silence error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)
	end := nextVADEvent(t, stream)
	if end.Type != VADEventEndOfSpeech {
		t.Fatalf("event type = %s, want %s", end.Type, VADEventEndOfSpeech)
	}
	assertCombinedFrames(t, end.Frames, speech, silence)
}

func TestSimpleVADUpdateOptionsWithAllowsZeroPrefixPadding(t *testing.T) {
	detector := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:             0.05,
		MinSpeechDuration:     0.02,
		PrefixPaddingDuration: 0.02,
	})
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	prefix := audioFrame(16000, 160, 0)
	if err := stream.PushFrame(prefix); err != nil {
		t.Fatalf("PushFrame() prefix error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)

	detector.UpdateOptionsWith(WithPrefixPaddingDuration(0))
	firstSpeech := audioFrame(16000, 160, 6000)
	secondSpeech := audioFrame(16000, 160, 7000)
	for _, frame := range []*model.AudioFrame{firstSpeech, secondSpeech} {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatalf("PushFrame() speech error = %v", err)
		}
		assertEventType(t, stream, VADEventInferenceDone)
	}
	start := nextVADEvent(t, stream)
	if start.Type != VADEventStartOfSpeech {
		t.Fatalf("event type = %s, want %s", start.Type, VADEventStartOfSpeech)
	}
	assertCombinedFrames(t, start.Frames, firstSpeech, secondSpeech)
}

func TestSimpleVADUpdateOptionsWithAllowsZeroMaxBufferedSpeech(t *testing.T) {
	detector := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:                 0.05,
		PrefixPaddingDuration:     0.01,
		MaxBufferedSpeechDuration: 0.03,
	})
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	prefix := audioFrame(16000, 160, 0)
	if err := stream.PushFrame(prefix); err != nil {
		t.Fatalf("PushFrame() prefix error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)

	detector.UpdateOptionsWith(WithMaxBufferedSpeechDuration(0))
	if err := stream.PushFrame(audioFrame(16000, 160, 6000)); err != nil {
		t.Fatalf("PushFrame() speech error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)
	start := nextVADEvent(t, stream)
	if start.Type != VADEventStartOfSpeech {
		t.Fatalf("event type = %s, want %s", start.Type, VADEventStartOfSpeech)
	}
	assertCombinedFrames(t, start.Frames, prefix)
}

func TestSimpleVADUpdateOptionsIgnoresInvalidProbabilitySmoothingAlpha(t *testing.T) {
	detector := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:                 0.05,
		ProbabilitySmoothingAlpha: 0.35,
	})
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	detector.UpdateOptions(SimpleVADOptions{ProbabilitySmoothingAlpha: -0.1})
	if detector.options.ProbabilitySmoothingAlpha != 0.35 {
		t.Fatalf("detector smoothing alpha = %v, want 0.35", detector.options.ProbabilitySmoothingAlpha)
	}
	simpleStream := stream.(*simpleVADStream)
	if simpleStream.options.ProbabilitySmoothingAlpha != 0.35 {
		t.Fatalf("stream smoothing alpha = %v, want 0.35", simpleStream.options.ProbabilitySmoothingAlpha)
	}
}

func TestSimpleVADUpdateOptionsWithIgnoresInvalidProbabilitySmoothingAlpha(t *testing.T) {
	detector := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:                 0.05,
		ProbabilitySmoothingAlpha: 0.35,
	})
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	detector.UpdateOptionsWith(WithProbabilitySmoothingAlpha(1.1))
	if detector.options.ProbabilitySmoothingAlpha != 0.35 {
		t.Fatalf("detector smoothing alpha = %v, want 0.35", detector.options.ProbabilitySmoothingAlpha)
	}
	simpleStream := stream.(*simpleVADStream)
	if simpleStream.options.ProbabilitySmoothingAlpha != 0.35 {
		t.Fatalf("stream smoothing alpha = %v, want 0.35", simpleStream.options.ProbabilitySmoothingAlpha)
	}
}

func TestSimpleVADRejectsInvalidUpdateIntervalAtStream(t *testing.T) {
	for _, interval := range []float64{-1, math.NaN(), math.Inf(1)} {
		detector := NewSimpleVADWithOptions(SimpleVADOptions{UpdateInterval: interval})

		stream, err := detector.Stream(context.Background())
		if err == nil {
			if stream != nil {
				_ = stream.Close()
			}
			t.Fatalf("Stream() with update interval %v error = nil, want invalid update interval error", interval)
		}
		if !strings.Contains(err.Error(), "update interval must be greater than 0") {
			t.Fatalf("Stream() with update interval %v error = %q, want update interval message", interval, err.Error())
		}
		if len(detector.streams) != 0 {
			t.Fatalf("registered streams after invalid update interval = %d, want 0", len(detector.streams))
		}
	}
}

func TestSimpleVADUpdateOptionsWithIgnoresInvalidUpdateInterval(t *testing.T) {
	detector := NewSimpleVADWithOptions(SimpleVADOptions{UpdateInterval: 0.5})
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	detector.UpdateOptionsWith(WithUpdateInterval(0))
	if detector.Capabilities().UpdateInterval != 0.5 {
		t.Fatalf("Capabilities().UpdateInterval = %v, want 0.5", detector.Capabilities().UpdateInterval)
	}
	simpleStream := stream.(*simpleVADStream)
	if simpleStream.options.UpdateInterval != 0.5 {
		t.Fatalf("stream update interval = %v, want 0.5", simpleStream.options.UpdateInterval)
	}
}

func TestSimpleVADRejectsInvalidWindowDurationAtStream(t *testing.T) {
	for _, duration := range []float64{-1, math.NaN(), math.Inf(1)} {
		detector := NewSimpleVADWithOptions(SimpleVADOptions{WindowDuration: duration})

		stream, err := detector.Stream(context.Background())
		if err == nil {
			if stream != nil {
				_ = stream.Close()
			}
			t.Fatalf("Stream() with window duration %v error = nil, want invalid window duration error", duration)
		}
		if !strings.Contains(err.Error(), "window duration must be greater than or equal to 0") {
			t.Fatalf("Stream() with window duration %v error = %q, want window duration message", duration, err.Error())
		}
		if len(detector.streams) != 0 {
			t.Fatalf("registered streams after invalid window duration = %d, want 0", len(detector.streams))
		}
	}
}

func TestSimpleVADUpdateOptionsWithIgnoresInvalidWindowDuration(t *testing.T) {
	detector := NewSimpleVADWithOptions(SimpleVADOptions{WindowDuration: 0.032})
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	detector.UpdateOptionsWith(WithWindowDuration(math.Inf(1)))
	if detector.options.WindowDuration != 0.032 {
		t.Fatalf("detector window duration = %v, want 0.032", detector.options.WindowDuration)
	}
	simpleStream := stream.(*simpleVADStream)
	if simpleStream.options.WindowDuration != 0.032 {
		t.Fatalf("stream window duration = %v, want 0.032", simpleStream.options.WindowDuration)
	}
}

func TestSimpleVADRejectsInvalidThresholdAtStream(t *testing.T) {
	for _, threshold := range []float64{-1, math.NaN(), math.Inf(1)} {
		detector := NewSimpleVADWithOptions(SimpleVADOptions{
			Threshold:             threshold,
			DeactivationThreshold: 0.05,
		})

		stream, err := detector.Stream(context.Background())
		if err == nil {
			if stream != nil {
				_ = stream.Close()
			}
			t.Fatalf("Stream() with threshold %v error = nil, want invalid threshold error", threshold)
		}
		if !strings.Contains(err.Error(), "threshold must be greater than or equal to 0") {
			t.Fatalf("Stream() with threshold %v error = %q, want threshold message", threshold, err.Error())
		}
		if len(detector.streams) != 0 {
			t.Fatalf("registered streams after invalid threshold = %d, want 0", len(detector.streams))
		}
	}
}

func TestSimpleVADUpdateOptionsWithIgnoresInvalidThreshold(t *testing.T) {
	detector := NewSimpleVADWithOptions(SimpleVADOptions{Threshold: 0.05})
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	detector.UpdateOptionsWith(WithThreshold(math.NaN()))
	if detector.options.Threshold != 0.05 {
		t.Fatalf("detector threshold = %v, want 0.05", detector.options.Threshold)
	}
	simpleStream := stream.(*simpleVADStream)
	if simpleStream.options.Threshold != 0.05 {
		t.Fatalf("stream threshold = %v, want 0.05", simpleStream.options.Threshold)
	}
}

func TestSimpleVADRejectsInvalidTimingDurationsAtStream(t *testing.T) {
	tests := []struct {
		name    string
		options SimpleVADOptions
		want    string
	}{
		{
			name:    "min speech duration",
			options: SimpleVADOptions{MinSpeechDuration: math.NaN()},
			want:    "min speech duration must be greater than or equal to 0",
		},
		{
			name:    "min silence duration",
			options: SimpleVADOptions{MinSilenceDuration: math.Inf(1)},
			want:    "min silence duration must be greater than or equal to 0",
		},
		{
			name:    "prefix padding duration",
			options: SimpleVADOptions{PrefixPaddingDuration: -0.1},
			want:    "prefix padding duration must be greater than or equal to 0",
		},
		{
			name:    "max buffered speech duration",
			options: SimpleVADOptions{MaxBufferedSpeechDuration: math.Inf(1)},
			want:    "max buffered speech duration must be greater than or equal to 0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector := NewSimpleVADWithOptions(tt.options)

			stream, err := detector.Stream(context.Background())
			if err == nil {
				if stream != nil {
					_ = stream.Close()
				}
				t.Fatal("Stream() error = nil, want invalid timing duration error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Stream() error = %q, want %q", err.Error(), tt.want)
			}
			if len(detector.streams) != 0 {
				t.Fatalf("registered streams after invalid timing duration = %d, want 0", len(detector.streams))
			}
		})
	}
}

func TestSimpleVADUpdateOptionsWithIgnoresInvalidTimingDuration(t *testing.T) {
	detector := NewSimpleVADWithOptions(SimpleVADOptions{MinSpeechDuration: 0.02})
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	detector.UpdateOptionsWith(WithMinSpeechDuration(math.NaN()))
	if detector.options.MinSpeechDuration != 0.02 {
		t.Fatalf("detector min speech duration = %v, want 0.02", detector.options.MinSpeechDuration)
	}
	simpleStream := stream.(*simpleVADStream)
	if simpleStream.options.MinSpeechDuration != 0.02 {
		t.Fatalf("stream min speech duration = %v, want 0.02", simpleStream.options.MinSpeechDuration)
	}
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

func TestSimpleVADUpdateOptionsDoesNotRecoverDroppedPendingSpeech(t *testing.T) {
	detector := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:                 0.05,
		MinSpeechDuration:         0.04,
		MaxBufferedSpeechDuration: 0.02,
	})
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	firstSpeech := audioFrame(16000, 160, 6000)
	secondSpeech := audioFrame(16000, 160, 7000)
	droppedSpeech := audioFrame(16000, 160, 8000)
	fourthSpeech := audioFrame(16000, 160, 9000)
	for _, frame := range []*model.AudioFrame{firstSpeech, secondSpeech, droppedSpeech} {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatalf("PushFrame() speech error = %v", err)
		}
		assertEventType(t, stream, VADEventInferenceDone)
	}

	detector.UpdateOptions(SimpleVADOptions{MaxBufferedSpeechDuration: 0.04})
	if err := stream.PushFrame(fourthSpeech); err != nil {
		t.Fatalf("PushFrame() fourth speech error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)
	start := nextVADEvent(t, stream)
	if start.Type != VADEventStartOfSpeech {
		t.Fatalf("event type = %s, want %s", start.Type, VADEventStartOfSpeech)
	}
	assertCombinedFrames(t, start.Frames, firstSpeech, secondSpeech, fourthSpeech)
}

func TestSimpleVADLimitsPendingSpeechAtSampleBoundary(t *testing.T) {
	stream, err := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:                 0.05,
		MinSpeechDuration:         0.03,
		MaxBufferedSpeechDuration: 0.025,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	firstSpeech := audioFrame(16000, 160, 6000)
	secondSpeech := audioFrame(16000, 160, 7000)
	thirdSpeech := audioFrame(16000, 160, 8000)
	for _, frame := range []*model.AudioFrame{firstSpeech, secondSpeech, thirdSpeech} {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatalf("PushFrame() speech error = %v", err)
		}
		assertEventType(t, stream, VADEventInferenceDone)
	}

	start := nextVADEvent(t, stream)
	if start.Type != VADEventStartOfSpeech {
		t.Fatalf("event type = %s, want %s", start.Type, VADEventStartOfSpeech)
	}
	assertCombinedFrames(t, start.Frames, firstSpeech, secondSpeech, audioFrame(16000, 80, 8000))
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

func TestSimpleVADIgnoresMismatchedChannelCountFrames(t *testing.T) {
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

	if err := stream.PushFrame(audioFrameWithChannels(16000, 2, 160, 0)); err != nil {
		t.Fatalf("PushFrame() mismatched channel count error = %v", err)
	}
	assertNoQueuedVADEvent(t, stream)

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
		{
			SampleRate:  16000,
			NumChannels: 1,
		},
		{
			Data:              []byte{0, 0},
			SampleRate:        16000,
			NumChannels:       1,
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

func TestSimpleVADThresholdUpdatePreservesDeactivationThreshold(t *testing.T) {
	detector := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:             0.05,
		DeactivationThreshold: 0.05,
	})
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	speech := audioFrame(16000, 160, 6000)
	if err := stream.PushFrame(speech); err != nil {
		t.Fatalf("PushFrame() speech error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventStartOfSpeech)

	detector.UpdateOptions(SimpleVADOptions{Threshold: 0.1})
	if detector.options.DeactivationThreshold != 0.05 {
		t.Fatalf("detector deactivation threshold = %v, want 0.05", detector.options.DeactivationThreshold)
	}
	simpleStream := stream.(*simpleVADStream)
	if simpleStream.options.DeactivationThreshold != 0.05 {
		t.Fatalf("stream deactivation threshold = %v, want 0.05", simpleStream.options.DeactivationThreshold)
	}

	dipAboveOriginalDeactivation := audioFrame(16000, 160, 2200)
	if err := stream.PushFrame(dipAboveOriginalDeactivation); err != nil {
		t.Fatalf("PushFrame() dip error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)
	assertNoQueuedVADEvent(t, stream)

	silence := audioFrame(16000, 160, 0)
	if err := stream.PushFrame(silence); err != nil {
		t.Fatalf("PushFrame() silence error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)
	end := nextVADEvent(t, stream)
	if end.Type != VADEventEndOfSpeech {
		t.Fatalf("event type = %s, want %s", end.Type, VADEventEndOfSpeech)
	}
	assertCombinedFrames(t, end.Frames, speech, dipAboveOriginalDeactivation, silence)
}

func TestSimpleVADRejectsInvalidDeactivationThresholdAtStream(t *testing.T) {
	for _, threshold := range []float64{-0.1, math.NaN(), math.Inf(1)} {
		detector := NewSimpleVADWithOptions(SimpleVADOptions{
			Threshold:             0.05,
			DeactivationThreshold: threshold,
		})

		stream, err := detector.Stream(context.Background())
		if err == nil {
			if stream != nil {
				_ = stream.Close()
			}
			t.Fatalf("Stream() with deactivation threshold %v error = nil, want invalid threshold error", threshold)
		}
		if !strings.Contains(err.Error(), "deactivation threshold must be greater than or equal to 0") {
			t.Fatalf("Stream() with deactivation threshold %v error = %q, want deactivation threshold message", threshold, err.Error())
		}
		if len(detector.streams) != 0 {
			t.Fatalf("registered streams after invalid deactivation threshold = %d, want 0", len(detector.streams))
		}
	}
}

func TestSimpleVADUpdateOptionsWithIgnoresInvalidDeactivationThreshold(t *testing.T) {
	detector := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:             0.1,
		DeactivationThreshold: 0.05,
	})
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	detector.UpdateOptionsWith(WithDeactivationThreshold(-0.1))
	if detector.options.DeactivationThreshold != 0.05 {
		t.Fatalf("detector deactivation threshold = %v, want 0.05", detector.options.DeactivationThreshold)
	}
	simpleStream := stream.(*simpleVADStream)
	if simpleStream.options.DeactivationThreshold != 0.05 {
		t.Fatalf("stream deactivation threshold = %v, want 0.05", simpleStream.options.DeactivationThreshold)
	}
}

func TestSimpleVADUpdateOptionsWithAllowsZeroDeactivationThreshold(t *testing.T) {
	detector := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:             0.1,
		DeactivationThreshold: 0.05,
	})
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	speech := audioFrame(16000, 160, 6000)
	if err := stream.PushFrame(speech); err != nil {
		t.Fatalf("PushFrame() speech error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventStartOfSpeech)

	detector.UpdateOptionsWith(WithDeactivationThreshold(0))
	dipAboveZero := audioFrame(16000, 160, 1200)
	if err := stream.PushFrame(dipAboveZero); err != nil {
		t.Fatalf("PushFrame() dip error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)
	assertNoQueuedVADEvent(t, stream)

	silence := audioFrame(16000, 160, 0)
	if err := stream.PushFrame(silence); err != nil {
		t.Fatalf("PushFrame() silence error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)
	end := nextVADEvent(t, stream)
	if end.Type != VADEventEndOfSpeech {
		t.Fatalf("event type = %s, want %s", end.Type, VADEventEndOfSpeech)
	}
	assertCombinedFrames(t, end.Frames, speech, dipAboveZero, silence)
}

func TestSimpleVADProbabilitySmoothingDelaysSpeechEnd(t *testing.T) {
	stream, err := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:                 0.05,
		MinSilenceDuration:        0.01,
		ProbabilitySmoothingAlpha: 0.35,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	speech := audioFrame(16000, 160, 6000)
	firstSilence := audioFrame(16000, 160, 0)
	secondSilence := audioFrame(16000, 160, 0)
	for _, frame := range []*model.AudioFrame{speech, firstSilence} {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatalf("PushFrame() error = %v", err)
		}
	}

	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventStartOfSpeech)
	assertEventType(t, stream, VADEventInferenceDone)
	assertNoQueuedVADEvent(t, stream)

	if err := stream.PushFrame(secondSilence); err != nil {
		t.Fatalf("PushFrame() second silence error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventEndOfSpeech)
}

func TestSimpleVADRejectsInvalidProbabilitySmoothingAlpha(t *testing.T) {
	for _, alpha := range []float64{-0.1, 1.1, 2, math.NaN(), math.Inf(1)} {
		detector := NewSimpleVADWithOptions(SimpleVADOptions{
			Threshold:                 0.05,
			ProbabilitySmoothingAlpha: alpha,
		})
		stream, err := detector.Stream(context.Background())
		if err == nil {
			if stream != nil {
				_ = stream.Close()
			}
			t.Fatalf("Stream() with alpha %v error = nil, want invalid smoothing alpha error", alpha)
		}
		if !strings.Contains(err.Error(), "alpha must be in [0, 1]") {
			t.Fatalf("Stream() with alpha %v error = %q, want alpha bounds message", alpha, err.Error())
		}
		if len(detector.streams) != 0 {
			t.Fatalf("registered streams after invalid alpha = %d, want 0", len(detector.streams))
		}
	}
}

func TestSimpleVADAllowsZeroProbabilitySmoothingAlpha(t *testing.T) {
	detector := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:                 0.05,
		ProbabilitySmoothingAlpha: 0,
	})
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() with zero smoothing alpha error = %v, want nil", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(audioFrame(16000, 160, 6000)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventStartOfSpeech)
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

func TestSimpleVADEndOfSpeechCopiesInputFrameData(t *testing.T) {
	stream, err := NewSimpleVAD(0.05).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	speech := audioFrame(16000, 160, 6000)
	silence := audioFrame(16000, 160, 0)
	if err := stream.PushFrame(speech); err != nil {
		t.Fatalf("PushFrame() speech error = %v", err)
	}
	if err := stream.PushFrame(silence); err != nil {
		t.Fatalf("PushFrame() silence error = %v", err)
	}
	for i := range speech.Data {
		speech.Data[i] = 0
	}
	for i := range silence.Data {
		silence.Data[i] = 1
	}

	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventStartOfSpeech)
	assertEventType(t, stream, VADEventInferenceDone)
	end := nextVADEvent(t, stream)
	if end.Type != VADEventEndOfSpeech {
		t.Fatalf("event type = %s, want %s", end.Type, VADEventEndOfSpeech)
	}
	assertCombinedFrames(t, end.Frames, audioFrame(16000, 160, 6000), audioFrame(16000, 160, 0))
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

func TestSimpleVADPreStartSpeechIncrementsSilenceDuration(t *testing.T) {
	stream, err := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:         0.05,
		MinSpeechDuration: 0.03,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	for i := range 3 {
		if err := stream.PushFrame(audioFrame(16000, 160, 6000)); err != nil {
			t.Fatalf("PushFrame() error = %v", err)
		}
		inference := nextVADEvent(t, stream)
		if inference.Type != VADEventInferenceDone {
			t.Fatalf("event %d type = %s, want %s", i, inference.Type, VADEventInferenceDone)
		}
		wantSilenceDuration := float64(i+1) * 0.01
		if inference.SilenceDuration != wantSilenceDuration {
			t.Fatalf("event %d SilenceDuration = %v, want %v", i, inference.SilenceDuration, wantSilenceDuration)
		}
	}
	assertEventType(t, stream, VADEventStartOfSpeech)
}

func TestSimpleVADShortSpeechSilenceInferenceUsesPreTransitionThresholds(t *testing.T) {
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
	inference := nextVADEvent(t, stream)
	if inference.Type != VADEventInferenceDone {
		t.Fatalf("event type = %s, want %s", inference.Type, VADEventInferenceDone)
	}
	if inference.SilenceDuration != 0.02 {
		t.Fatalf("SilenceDuration = %v, want 0.02", inference.SilenceDuration)
	}
	if inference.RawAccumulatedSpeech != 0.01 {
		t.Fatalf("RawAccumulatedSpeech = %v, want 0.01", inference.RawAccumulatedSpeech)
	}
	if inference.RawAccumulatedSilence != 0 {
		t.Fatalf("RawAccumulatedSilence = %v, want 0", inference.RawAccumulatedSilence)
	}
	assertNoVADEvent(t, stream)
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

func TestSimpleVADCountsShortInternalSilenceAsSpeechDuration(t *testing.T) {
	stream, err := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:          0.05,
		MinSilenceDuration: 0.03,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	firstSpeech := audioFrame(16000, 160, 6000)
	internalSilence := audioFrame(16000, 160, 0)
	secondSpeech := audioFrame(16000, 160, 6000)
	firstTrailingSilence := audioFrame(16000, 160, 0)
	secondTrailingSilence := audioFrame(16000, 160, 0)
	thirdTrailingSilence := audioFrame(16000, 160, 0)
	for _, frame := range []*model.AudioFrame{
		firstSpeech,
		internalSilence,
		secondSpeech,
		firstTrailingSilence,
		secondTrailingSilence,
		thirdTrailingSilence,
	} {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatalf("PushFrame() error = %v", err)
		}
	}

	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventStartOfSpeech)
	assertEventType(t, stream, VADEventInferenceDone)
	assertEventType(t, stream, VADEventInferenceDone)
	for range 3 {
		assertEventType(t, stream, VADEventInferenceDone)
	}
	end := nextVADEvent(t, stream)
	if end.Type != VADEventEndOfSpeech {
		t.Fatalf("event type = %s, want %s", end.Type, VADEventEndOfSpeech)
	}
	if end.SpeechDuration != 0.03 {
		t.Fatalf("SpeechDuration = %v, want 0.03 including short internal silence", end.SpeechDuration)
	}
	if end.SilenceDuration != 0.03 {
		t.Fatalf("SilenceDuration = %v, want 0.03", end.SilenceDuration)
	}
	assertCombinedFrames(t, end.Frames, firstSpeech, internalSilence, secondSpeech, firstTrailingSilence, secondTrailingSilence, thirdTrailingSilence)
}

func TestSimpleVADContinuesSilenceDurationAfterEndOfSpeech(t *testing.T) {
	stream, err := NewSimpleVADWithOptions(SimpleVADOptions{
		Threshold:          0.05,
		MinSilenceDuration: 0.01,
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
	assertEventType(t, stream, VADEventEndOfSpeech)
	inference := nextVADEvent(t, stream)
	if inference.Type != VADEventInferenceDone {
		t.Fatalf("event type = %s, want %s", inference.Type, VADEventInferenceDone)
	}
	if inference.SilenceDuration != 0.02 {
		t.Fatalf("SilenceDuration after end = %v, want 0.02", inference.SilenceDuration)
	}
	if inference.RawAccumulatedSilence != 0.01 {
		t.Fatalf("RawAccumulatedSilence after end = %v, want 0.01", inference.RawAccumulatedSilence)
	}
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
	} else if !strings.Contains(err.Error(), "input ended") {
		t.Fatalf("PushFrame() after EndInput() error = %q, want input ended", err.Error())
	}
	if err := stream.Flush(); err == nil {
		t.Fatal("Flush() after EndInput() error = nil, want error")
	} else if !strings.Contains(err.Error(), "input ended") {
		t.Fatalf("Flush() after EndInput() error = %q, want input ended", err.Error())
	}
	if err := stream.EndInput(); err == nil {
		t.Fatal("second EndInput() error = nil, want error")
	} else if !strings.Contains(err.Error(), "input ended") {
		t.Fatalf("second EndInput() error = %q, want input ended", err.Error())
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
	} else if !strings.Contains(err.Error(), "input ended") {
		t.Fatalf("PushFrame() after Close() error = %q, want input ended", err.Error())
	}
	if err := stream.Flush(); err == nil {
		t.Fatal("Flush() after Close() error = nil, want error")
	} else if !strings.Contains(err.Error(), "input ended") {
		t.Fatalf("Flush() after Close() error = %q, want input ended", err.Error())
	}
	if err := stream.EndInput(); err == nil {
		t.Fatal("EndInput() after Close() error = nil, want error")
	} else if !strings.Contains(err.Error(), "input ended") {
		t.Fatalf("EndInput() after Close() error = %q, want input ended", err.Error())
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

func assertNoQueuedVADEvent(t *testing.T, stream VADStream) {
	t.Helper()
	simpleStream, ok := stream.(*simpleVADStream)
	if !ok {
		t.Fatalf("stream type = %T, want *simpleVADStream", stream)
	}
	simpleStream.eventMu.Lock()
	defer simpleStream.eventMu.Unlock()
	if len(simpleStream.eventQueue) != 0 {
		t.Fatalf("unexpected queued VAD event: %#v", simpleStream.eventQueue[0])
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
	return audioFrameWithChannels(sampleRate, 1, samples, value)
}

func audioFrameWithChannels(sampleRate uint32, channels uint32, samples int, value int16) *model.AudioFrame {
	data := make([]byte, samples*int(channels)*2)
	for i := 0; i < samples*int(channels); i++ {
		data[i*2] = byte(value)
		data[i*2+1] = byte(uint16(value) >> 8)
	}
	return &model.AudioFrame{
		Data:              data,
		SampleRate:        sampleRate,
		NumChannels:       channels,
		SamplesPerChannel: uint32(samples),
	}
}
