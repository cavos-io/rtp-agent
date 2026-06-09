package stt

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/telemetry"
)

func TestFallbackAdapterAggregatesProviderCapabilities(t *testing.T) {
	adapter := NewFallbackAdapter([]STT{
		&metadataSTT{label: "primary", capabilities: STTCapabilities{
			Streaming:        true,
			InterimResults:   true,
			Diarization:      true,
			OfflineRecognize: false,
		}},
		&metadataSTT{label: "fallback", capabilities: STTCapabilities{
			Streaming:        true,
			InterimResults:   false,
			Diarization:      false,
			OfflineRecognize: true,
		}},
	})

	caps := adapter.Capabilities()
	if !caps.Streaming {
		t.Fatal("Streaming = false, want true when all providers stream")
	}
	if caps.InterimResults {
		t.Fatal("InterimResults = true, want false unless all providers support interim results")
	}
	if caps.Diarization {
		t.Fatal("Diarization = true, want false unless all providers support diarization")
	}
	if !caps.OfflineRecognize {
		t.Fatal("OfflineRecognize = false, want true when any provider can batch-recognize")
	}
}

func TestFallbackAdapterRequiresAtLeastOneSTT(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("NewFallbackAdapter() did not panic, want empty STT list rejection")
		}
		if got, want := r, "At least one STT instance must be provided."; got != want {
			t.Fatalf("NewFallbackAdapter() panic = %q, want %q", got, want)
		}
	}()

	_ = NewFallbackAdapter(nil)
}

func TestFallbackAdapterDefaultConstructorsUseReferenceRetryInterval(t *testing.T) {
	adapter := NewFallbackAdapter([]STT{
		&metadataSTT{label: "primary", capabilities: STTCapabilities{Streaming: true}},
	})
	if adapter.retryInterval != 5*time.Second {
		t.Fatalf("RetryInterval = %s, want 5s reference default", adapter.retryInterval)
	}

	offline := &metadataSTT{
		label:        "offline",
		capabilities: STTCapabilities{OfflineRecognize: true},
	}
	withVAD := NewFallbackAdapterWithVAD([]STT{offline}, &fakeStreamAdapterVAD{})
	if withVAD.retryInterval != 5*time.Second {
		t.Fatalf("VAD RetryInterval = %s, want 5s reference default", withVAD.retryInterval)
	}
}

func TestDefaultFallbackAdapterOptionsMatchReferenceDefaults(t *testing.T) {
	options := DefaultFallbackAdapterOptions()

	if options.MaxRetryPerSTT != 1 {
		t.Fatalf("MaxRetryPerSTT = %d, want 1", options.MaxRetryPerSTT)
	}
	if options.AttemptTimeout != 10*time.Second {
		t.Fatalf("AttemptTimeout = %s, want 10s", options.AttemptTimeout)
	}
	if options.RetryInterval != 5*time.Second {
		t.Fatalf("RetryInterval = %s, want 5s", options.RetryInterval)
	}
}

func TestFallbackAdapterAlwaysAdvertisesOfflineRecognize(t *testing.T) {
	adapter := NewFallbackAdapter([]STT{
		&metadataSTT{label: "primary", capabilities: STTCapabilities{
			Streaming:        true,
			OfflineRecognize: false,
		}},
		&metadataSTT{label: "fallback", capabilities: STTCapabilities{
			Streaming:        true,
			OfflineRecognize: false,
		}},
	})

	if !adapter.Capabilities().OfflineRecognize {
		t.Fatal("OfflineRecognize = false, want true because FallbackAdapter exposes Recognize")
	}
}

func TestFallbackAdapterForwardsProviderMetrics(t *testing.T) {
	primary := &metadataSTT{label: "primary", capabilities: STTCapabilities{Streaming: true}}
	fallback := &metadataSTT{label: "fallback", capabilities: STTCapabilities{Streaming: true}}
	adapter := NewFallbackAdapter([]STT{primary, fallback})
	metricsCh := make(chan string, 2)

	unsubscribe := adapter.OnMetricsCollected(func(metrics *telemetry.STTMetrics) {
		metricsCh <- metrics.RequestID
	})
	defer unsubscribe()

	primary.EmitMetricsCollected(&telemetry.STTMetrics{RequestID: "primary-req"})
	fallback.EmitMetricsCollected(&telemetry.STTMetrics{RequestID: "fallback-req"})

	got := []string{<-metricsCh, <-metricsCh}
	if strings.Join(got, ",") != "primary-req,fallback-req" {
		t.Fatalf("forwarded metrics = %#v, want primary and fallback request IDs", got)
	}
}

func TestFallbackAdapterForwardsProviderErrors(t *testing.T) {
	primary := &metadataSTT{label: "primary", capabilities: STTCapabilities{Streaming: true}}
	fallback := &metadataSTT{label: "fallback", capabilities: STTCapabilities{Streaming: true}}
	adapter := NewFallbackAdapter([]STT{primary, fallback})
	errCh := make(chan error, 2)

	unsubscribe := adapter.OnError(func(err *STTError) {
		errCh <- err
	})
	defer unsubscribe()

	primaryCause := errors.New("primary failed")
	fallbackCause := errors.New("fallback failed")
	primary.EmitError(NewSTTError("primary", primaryCause, true))
	fallback.EmitError(NewSTTError("fallback", fallbackCause, true))

	got := []error{<-errCh, <-errCh}
	if !errors.Is(got[0], primaryCause) || !errors.Is(got[1], fallbackCause) {
		t.Fatalf("forwarded errors = %#v, want primary then fallback causes", got)
	}
}

func TestFallbackStreamSeedsStartTime(t *testing.T) {
	inner := &metadataRecognizeStream{events: []*SpeechEvent{{Type: SpeechEventFinalTranscript}}}
	adapter := NewFallbackAdapter([]STT{
		&metadataSTT{
			label:        "primary",
			capabilities: STTCapabilities{Streaming: true},
			stream:       inner,
		},
	})

	before := time.Now()
	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	after := time.Now()
	defer stream.Close()

	timing, ok := stream.(StreamTiming)
	if !ok {
		t.Fatal("stream does not implement StreamTiming")
	}
	assertStreamStartTimeSeeded(t, timing, before, after)
}

func TestFallbackAdapterAggregatesAlignedTranscriptGranularity(t *testing.T) {
	adapter := NewFallbackAdapter([]STT{
		&metadataSTT{label: "primary", capabilities: STTCapabilities{
			Streaming:         true,
			AlignedTranscript: "word",
		}},
		&metadataSTT{label: "fallback", capabilities: STTCapabilities{
			Streaming:         true,
			AlignedTranscript: "chunk",
		}},
	})

	if got := adapter.Capabilities().AlignedTranscript; got != "word" {
		t.Fatalf("AlignedTranscript = %q, want primary provider granularity word", got)
	}
}

func TestFallbackAdapterClearsAlignedTranscriptWhenAnyProviderLacksIt(t *testing.T) {
	adapter := NewFallbackAdapter([]STT{
		&metadataSTT{label: "primary", capabilities: STTCapabilities{
			Streaming:         true,
			AlignedTranscript: "word",
		}},
		&metadataSTT{label: "fallback", capabilities: STTCapabilities{
			Streaming: true,
		}},
	})

	if got := adapter.Capabilities().AlignedTranscript; got != "" {
		t.Fatalf("AlignedTranscript = %q, want empty when any provider lacks aligned transcripts", got)
	}
}

func TestFallbackAdapterRejectsNonStreamingProviderWithoutVAD(t *testing.T) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("NewFallbackAdapter did not panic")
		}
		want := "STTs do not support streaming: offline. Provide a VAD to enable stt.StreamAdapter automatically or wrap them with stt.StreamAdapter before using this adapter."
		if got := recovered; got != want {
			t.Fatalf("NewFallbackAdapter panic = %q, want %q", got, want)
		}
	}()

	NewFallbackAdapter([]STT{
		&metadataSTT{
			label:        "offline",
			capabilities: STTCapabilities{OfflineRecognize: true},
		},
	})
}

func TestFallbackAdapterRejectsAllNonStreamingProviderLabelsWithoutVAD(t *testing.T) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("NewFallbackAdapter did not panic")
		}
		message := recovered.(string)
		if !strings.Contains(message, "offline-a") || !strings.Contains(message, "offline-b") {
			t.Fatalf("panic message = %q, want all non-streaming provider labels", message)
		}
	}()

	NewFallbackAdapter([]STT{
		&metadataSTT{
			label:        "offline-a",
			capabilities: STTCapabilities{OfflineRecognize: true},
		},
		&metadataSTT{
			label:        "offline-b",
			capabilities: STTCapabilities{OfflineRecognize: true},
		},
	})
}

func TestFallbackAdapterWithVADWrapsNonStreamingProvider(t *testing.T) {
	offline := &metadataSTT{
		label:        "offline",
		capabilities: STTCapabilities{OfflineRecognize: true},
		recognizeResult: &SpeechEvent{
			Type: SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{
				Text: "hello",
			}},
		},
	}
	adapter := NewFallbackAdapterWithVAD([]STT{offline}, &fakeStreamAdapterVAD{
		stream: &fakeStreamAdapterVADStream{
			events: []*vad.VADEvent{{
				Type:   vad.VADEventEndOfSpeech,
				Frames: []*model.AudioFrame{{Data: []byte{1, 0}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}},
			}},
			done: make(chan struct{}),
		},
	})

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if event.Type != SpeechEventEndOfSpeech {
		t.Fatalf("first event type = %s, want end_of_speech", event.Type)
	}

	event, err = stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if event.Type != SpeechEventFinalTranscript || len(event.Alternatives) != 1 || event.Alternatives[0].Text != "hello" {
		t.Fatalf("second event = %#v, want final transcript", event)
	}
	if offline.recognizeCalls != 1 {
		t.Fatalf("recognize calls = %d, want 1", offline.recognizeCalls)
	}
}

func TestFallbackStreamReturnsEOFWhenProviderCompletes(t *testing.T) {
	second := &metadataSTT{
		label:        "fallback",
		capabilities: STTCapabilities{Streaming: true},
		stream: &metadataRecognizeStream{
			events: []*SpeechEvent{{Type: SpeechEventFinalTranscript}},
		},
	}
	adapter := NewFallbackAdapter([]STT{
		&metadataSTT{
			label:        "primary",
			capabilities: STTCapabilities{Streaming: true},
			stream: &metadataRecognizeStream{
				events: []*SpeechEvent{{Type: SpeechEventFinalTranscript}},
			},
		},
		second,
	})

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if _, err := stream.Next(); err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("second Next error = %v, want io.EOF", err)
	}
	if second.streamCalls != 0 {
		t.Fatalf("fallback stream calls = %d, want 0", second.streamCalls)
	}
}

func TestFallbackStreamKeepsReturningEOFAfterProviderCompletes(t *testing.T) {
	adapter := NewFallbackAdapter([]STT{
		&metadataSTT{
			label:        "primary",
			capabilities: STTCapabilities{Streaming: true},
			stream: &metadataRecognizeStream{
				events: []*SpeechEvent{{Type: SpeechEventFinalTranscript}},
			},
		},
	})

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if _, err := stream.Next(); err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("second Next error = %v, want io.EOF", err)
	}

	err = nextFallbackStreamError(stream)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("third Next error = %v, want io.EOF", err)
	}
}

func TestFallbackStreamRejectsInputAfterProviderCompletes(t *testing.T) {
	inner := &metadataRecognizeStream{
		events: []*SpeechEvent{{Type: SpeechEventFinalTranscript}},
	}
	adapter := NewFallbackAdapter([]STT{
		&metadataSTT{
			label:        "primary",
			capabilities: STTCapabilities{Streaming: true},
			stream:       inner,
		},
	})

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if _, err := stream.Next(); err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("second Next error = %v, want io.EOF", err)
	}

	err = stream.PushFrame(&model.AudioFrame{Data: []byte("late"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1})
	if err == nil {
		t.Fatal("PushFrame after provider completion returned nil, want error")
	}
}

func TestFallbackStreamRetriesNextProviderBeforeEvents(t *testing.T) {
	firstErr := errors.New("primary stream failed")
	second := &metadataSTT{
		label:        "fallback",
		capabilities: STTCapabilities{Streaming: true},
		stream: &metadataRecognizeStream{
			events: []*SpeechEvent{{Type: SpeechEventFinalTranscript}},
		},
	}
	adapter := NewFallbackAdapter([]STT{
		&metadataSTT{
			label:        "primary",
			capabilities: STTCapabilities{Streaming: true},
			stream:       &metadataRecognizeStream{err: firstErr},
		},
		second,
	})

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if _, err := stream.Next(); err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if second.streamCalls != 1 {
		t.Fatalf("fallback stream calls = %d, want 1", second.streamCalls)
	}
}

func TestFallbackStreamReturnsAllFailedErrorWhenProvidersExhausted(t *testing.T) {
	primaryErr := errors.New("primary stream failed")
	fallbackErr := errors.New("fallback stream failed")
	adapter := NewFallbackAdapterWithOptions([]STT{
		&metadataSTT{
			label:        "primary",
			capabilities: STTCapabilities{Streaming: true},
			streams: []RecognizeStream{
				&metadataRecognizeStream{err: primaryErr},
				&metadataRecognizeStream{events: []*SpeechEvent{{
					Type:         SpeechEventFinalTranscript,
					Alternatives: []SpeechData{{Text: "primary recovered"}},
				}}},
			},
		},
		&metadataSTT{
			label:        "fallback",
			capabilities: STTCapabilities{Streaming: true},
			streams: []RecognizeStream{
				&metadataRecognizeStream{err: fallbackErr},
				&metadataRecognizeStream{events: []*SpeechEvent{{
					Type:         SpeechEventFinalTranscript,
					Alternatives: []SpeechData{{Text: "fallback recovered"}},
				}}},
			},
		},
	}, FallbackAdapterOptions{MaxRetryPerSTT: 0})

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want all STTs failed error")
	}
	if !errors.Is(err, fallbackErr) {
		t.Fatalf("Next error = %v, want to wrap final provider stream error", err)
	}
	var allFailed *FallbackAllFailedError
	if !errors.As(err, &allFailed) {
		t.Fatalf("Next error = %T, want *FallbackAllFailedError", err)
	}
	if allFailed.Count != 2 {
		t.Fatalf("all failed Count = %d, want 2", allFailed.Count)
	}
	if strings.Join(allFailed.Labels, ",") != "primary,fallback" {
		t.Fatalf("all failed Labels = %#v, want primary/fallback", allFailed.Labels)
	}
	if allFailed.Duration <= 0 {
		t.Fatalf("all failed Duration = %s, want positive duration", allFailed.Duration)
	}
}

func TestFallbackStreamClosesRecoveriesWhenRuntimeProvidersExhausted(t *testing.T) {
	primaryErr := errors.New("primary stream failed")
	fallbackErr := errors.New("fallback stream failed")
	primaryRecovery := &liveRecoveryStream{
		release: make(chan struct{}),
		event: &SpeechEvent{
			Type:         SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{Text: "primary recovered"}},
		},
	}
	fallbackRecovery := &liveRecoveryStream{
		release: make(chan struct{}),
		event: &SpeechEvent{
			Type:         SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{Text: "fallback recovered"}},
		},
	}
	adapter := NewFallbackAdapterWithOptions([]STT{
		&metadataSTT{
			label:        "primary",
			capabilities: STTCapabilities{Streaming: true},
			streams: []RecognizeStream{
				&metadataRecognizeStream{err: primaryErr},
				primaryRecovery,
			},
		},
		&metadataSTT{
			label:        "fallback",
			capabilities: STTCapabilities{Streaming: true},
			streams: []RecognizeStream{
				&metadataRecognizeStream{err: fallbackErr},
				fallbackRecovery,
			},
		},
	}, FallbackAdapterOptions{MaxRetryPerSTT: 0})

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want all STTs failed error")
	}
	var allFailed *FallbackAllFailedError
	if !errors.As(err, &allFailed) {
		t.Fatalf("Next error = %T, want *FallbackAllFailedError", err)
	}
	waitForRecoveryClosed(t, primaryRecovery)
	waitForRecoveryClosed(t, fallbackRecovery)
	close(primaryRecovery.release)
	close(fallbackRecovery.release)
}

func TestFallbackStreamStartReturnsAllFailedErrorWhenProvidersExhausted(t *testing.T) {
	primaryErr := errors.New("primary stream start failed")
	fallbackErr := errors.New("fallback stream start failed")
	adapter := NewFallbackAdapterWithOptions([]STT{
		&metadataSTT{
			label:        "primary",
			capabilities: STTCapabilities{Streaming: true},
			streamErrs:   []error{primaryErr},
		},
		&metadataSTT{
			label:        "fallback",
			capabilities: STTCapabilities{Streaming: true},
			streamErrs:   []error{fallbackErr},
		},
	}, FallbackAdapterOptions{MaxRetryPerSTT: 0})

	_, err := adapter.Stream(context.Background(), "en")
	if err == nil {
		t.Fatal("Stream error = nil, want all STTs failed error")
	}
	if !errors.Is(err, fallbackErr) {
		t.Fatalf("Stream error = %v, want to wrap final provider start error", err)
	}
	var allFailed *FallbackAllFailedError
	if !errors.As(err, &allFailed) {
		t.Fatalf("Stream error = %T, want *FallbackAllFailedError", err)
	}
	if allFailed.Count != 2 {
		t.Fatalf("all failed Count = %d, want 2", allFailed.Count)
	}
	if strings.Join(allFailed.Labels, ",") != "primary,fallback" {
		t.Fatalf("all failed Labels = %#v, want primary/fallback", allFailed.Labels)
	}
	if allFailed.Duration <= 0 {
		t.Fatalf("all failed Duration = %s, want positive duration", allFailed.Duration)
	}
}

func TestFallbackStreamStartClosesRecoveryStreamsWhenProvidersExhausted(t *testing.T) {
	primaryErr := errors.New("primary stream start failed")
	fallbackErr := errors.New("fallback stream start failed")
	recovery := &liveRecoveryStream{
		release: make(chan struct{}),
		event: &SpeechEvent{
			Type:         SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{Text: "primary recovered"}},
		},
	}
	adapter := NewFallbackAdapterWithOptions([]STT{
		&metadataSTT{
			label:        "primary",
			capabilities: STTCapabilities{Streaming: true},
			streamErrs:   []error{primaryErr},
			streams:      []RecognizeStream{recovery},
		},
		&metadataSTT{
			label:        "fallback",
			capabilities: STTCapabilities{Streaming: true},
			streamErrs:   []error{fallbackErr},
		},
	}, FallbackAdapterOptions{MaxRetryPerSTT: 0})

	_, err := adapter.Stream(context.Background(), "en")
	if err == nil {
		t.Fatal("Stream error = nil, want all STTs failed error")
	}
	var allFailed *FallbackAllFailedError
	if !errors.As(err, &allFailed) {
		t.Fatalf("Stream error = %T, want *FallbackAllFailedError", err)
	}
	waitForRecoveryClosed(t, recovery)
	close(recovery.release)
}

func TestFallbackAdapterRetriesSameSTTBeforeFallback(t *testing.T) {
	firstErr := errors.New("primary recognize failed")
	primary := &metadataSTT{
		label:        "primary",
		capabilities: STTCapabilities{Streaming: true},
		recognizeResults: []*SpeechEvent{
			nil,
			{Type: SpeechEventFinalTranscript, Alternatives: []SpeechData{{Text: "primary recovered"}}},
		},
		recognizeErrs: []error{firstErr, nil},
	}
	fallback := &metadataSTT{
		label:        "fallback",
		capabilities: STTCapabilities{Streaming: true},
		recognizeResult: &SpeechEvent{
			Type:         SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{Text: "fallback"}},
		},
	}
	adapter := NewFallbackAdapterWithOptions([]STT{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerSTT: 1,
	})

	event, err := adapter.Recognize(context.Background(), nil, "en")
	if err != nil {
		t.Fatalf("Recognize returned error: %v", err)
	}
	if got := event.Alternatives[0].Text; got != "primary recovered" {
		t.Fatalf("recognized text = %q, want primary recovered", got)
	}
	if primary.recognizeCalls != 2 {
		t.Fatalf("primary recognize calls = %d, want 2", primary.recognizeCalls)
	}
	if fallback.recognizeCalls != 0 {
		t.Fatalf("fallback recognize calls = %d, want 0", fallback.recognizeCalls)
	}
}

func TestFallbackAdapterWaitsRetryIntervalBeforeSameSTTRetry(t *testing.T) {
	firstErr := errors.New("primary recognize failed")
	primary := &metadataSTT{
		label:        "primary",
		capabilities: STTCapabilities{Streaming: true},
		recognizeResults: []*SpeechEvent{
			nil,
			{Type: SpeechEventFinalTranscript, Alternatives: []SpeechData{{Text: "primary recovered"}}},
		},
		recognizeErrs: []error{firstErr, nil},
	}
	adapter := NewFallbackAdapterWithOptions([]STT{primary}, FallbackAdapterOptions{
		MaxRetryPerSTT: 1,
		RetryInterval:  30 * time.Millisecond,
	})

	startedAt := time.Now()
	event, err := adapter.Recognize(context.Background(), nil, "en")
	elapsed := time.Since(startedAt)
	if err != nil {
		t.Fatalf("Recognize returned error: %v", err)
	}
	if got := event.Alternatives[0].Text; got != "primary recovered" {
		t.Fatalf("recognized text = %q, want primary recovered", got)
	}
	if elapsed < 25*time.Millisecond {
		t.Fatalf("Recognize elapsed = %s, want retry interval delay", elapsed)
	}
}

func TestFallbackAdapterTimesOutBlockedRecognizeAttempt(t *testing.T) {
	primary := &blockingContextSTT{
		label:        "primary",
		capabilities: STTCapabilities{Streaming: true},
		started:      make(chan struct{}, 1),
	}
	fallback := &metadataSTT{
		label:        "fallback",
		capabilities: STTCapabilities{Streaming: true},
		recognizeResult: &SpeechEvent{
			Type:         SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{Text: "fallback"}},
		},
	}
	adapter := NewFallbackAdapterWithOptions([]STT{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerSTT: 0,
		AttemptTimeout: 20 * time.Millisecond,
	})

	event, err := adapter.Recognize(context.Background(), nil, "en")
	if err != nil {
		t.Fatalf("Recognize returned error: %v", err)
	}
	if got := event.Alternatives[0].Text; got != "fallback" {
		t.Fatalf("recognized text = %q, want fallback", got)
	}
	select {
	case <-primary.started:
	default:
		t.Fatal("primary recognize was not attempted")
	}
	if !primary.wasCanceled() {
		t.Fatal("primary recognize context was not canceled after attempt timeout")
	}
	if adapter.isAvailable(0) {
		t.Fatal("primary provider is still available after timed-out attempt")
	}
}

func TestFallbackAdapterReturnsAllFailedErrorWhenProvidersExhausted(t *testing.T) {
	primaryErr := errors.New("primary recognize failed")
	fallbackErr := errors.New("fallback recognize failed")
	adapter := NewFallbackAdapterWithOptions([]STT{
		&metadataSTT{
			label:         "primary",
			capabilities:  STTCapabilities{Streaming: true},
			recognizeErrs: []error{primaryErr},
		},
		&metadataSTT{
			label:         "fallback",
			capabilities:  STTCapabilities{Streaming: true},
			recognizeErrs: []error{fallbackErr},
		},
	}, FallbackAdapterOptions{MaxRetryPerSTT: 0})

	_, err := adapter.Recognize(context.Background(), nil, "en")
	if err == nil {
		t.Fatal("Recognize error = nil, want all STTs failed error")
	}
	if !errors.Is(err, fallbackErr) {
		t.Fatalf("Recognize error = %v, want to wrap final provider error", err)
	}
	var allFailed *FallbackAllFailedError
	if !errors.As(err, &allFailed) {
		t.Fatalf("Recognize error = %T, want *FallbackAllFailedError", err)
	}
	if allFailed.Count != 2 {
		t.Fatalf("all failed Count = %d, want 2", allFailed.Count)
	}
	if strings.Join(allFailed.Labels, ",") != "primary,fallback" {
		t.Fatalf("all failed Labels = %#v, want primary/fallback", allFailed.Labels)
	}
	if allFailed.Duration <= 0 {
		t.Fatalf("all failed Duration = %s, want positive duration", allFailed.Duration)
	}
	if !strings.Contains(err.Error(), "all STTs failed") {
		t.Fatalf("Recognize error = %q, want all STTs failed message", err)
	}
}

func TestFallbackAdapterSkipsUnavailableSTTOnNextRecognize(t *testing.T) {
	primaryErr := errors.New("primary recognize failed")
	primary := &metadataSTT{
		label:           "primary",
		capabilities:    STTCapabilities{Streaming: true},
		recognizeErrs:   []error{primaryErr, primaryErr},
		recognizeResult: &SpeechEvent{Type: SpeechEventFinalTranscript, Alternatives: []SpeechData{{Text: "primary"}}},
	}
	fallback := &metadataSTT{
		label:        "fallback",
		capabilities: STTCapabilities{Streaming: true},
		recognizeResult: &SpeechEvent{
			Type:         SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{Text: "fallback"}},
		},
	}
	adapter := NewFallbackAdapterWithOptions([]STT{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerSTT: 0,
	})

	event, err := adapter.Recognize(context.Background(), nil, "en")
	if err != nil {
		t.Fatalf("first Recognize returned error: %v", err)
	}
	if got := event.Alternatives[0].Text; got != "fallback" {
		t.Fatalf("first recognized text = %q, want fallback", got)
	}

	event, err = adapter.Recognize(context.Background(), nil, "en")
	if err != nil {
		t.Fatalf("second Recognize returned error: %v", err)
	}
	if got := event.Alternatives[0].Text; got != "fallback" {
		t.Fatalf("second recognized text = %q, want fallback", got)
	}
	if primary.recognizeCalls != 1 {
		t.Fatalf("primary recognize calls = %d, want 1 because unavailable providers are skipped", primary.recognizeCalls)
	}
	if fallback.recognizeCalls != 2 {
		t.Fatalf("fallback recognize calls = %d, want 2", fallback.recognizeCalls)
	}
}

func TestFallbackAdapterRecoversUnavailableSTTInBackground(t *testing.T) {
	primaryErr := errors.New("primary recognize failed")
	primary := &metadataSTT{
		label:           "primary",
		capabilities:    STTCapabilities{Streaming: true},
		recognizeErrs:   []error{primaryErr, nil, nil},
		recognizeResult: &SpeechEvent{Type: SpeechEventFinalTranscript, Alternatives: []SpeechData{{Text: "primary"}}},
	}
	fallback := &metadataSTT{
		label:        "fallback",
		capabilities: STTCapabilities{Streaming: true},
		recognizeResult: &SpeechEvent{
			Type:         SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{Text: "fallback"}},
		},
	}
	adapter := NewFallbackAdapterWithOptions([]STT{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerSTT: 0,
	})

	event, err := adapter.Recognize(context.Background(), nil, "en")
	if err != nil {
		t.Fatalf("first Recognize returned error: %v", err)
	}
	if got := event.Alternatives[0].Text; got != "fallback" {
		t.Fatalf("first recognized text = %q, want fallback", got)
	}

	waitForRecognizeCalls(t, primary, 2)

	event, err = adapter.Recognize(context.Background(), nil, "en")
	if err != nil {
		t.Fatalf("second Recognize returned error: %v", err)
	}
	if got := event.Alternatives[0].Text; got != "primary" {
		t.Fatalf("second recognized text = %q, want recovered primary", got)
	}
	if primary.recognizeCalls != 3 {
		t.Fatalf("primary recognize calls = %d, want failure + recovery + active call", primary.recognizeCalls)
	}
	if fallback.recognizeCalls != 1 {
		t.Fatalf("fallback recognize calls = %d, want only initial fallback", fallback.recognizeCalls)
	}
}

func TestFallbackAdapterEmitsAvailabilityChanges(t *testing.T) {
	primaryErr := errors.New("primary recognize failed")
	primary := &metadataSTT{
		label:           "primary",
		capabilities:    STTCapabilities{Streaming: true},
		recognizeErrs:   []error{primaryErr, nil, nil},
		recognizeResult: &SpeechEvent{Type: SpeechEventFinalTranscript, Alternatives: []SpeechData{{Text: "primary"}}},
	}
	fallback := &metadataSTT{
		label:        "fallback",
		capabilities: STTCapabilities{Streaming: true},
		recognizeResult: &SpeechEvent{
			Type:         SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{Text: "fallback"}},
		},
	}
	adapter := NewFallbackAdapterWithOptions([]STT{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerSTT: 0,
	})
	changes := make(chan AvailabilityChangedEvent, 2)
	adapter.OnAvailabilityChanged(func(event AvailabilityChangedEvent) {
		changes <- event
	})

	if _, err := adapter.Recognize(context.Background(), nil, "en"); err != nil {
		t.Fatalf("Recognize returned error: %v", err)
	}

	unavailable := receiveAvailabilityChange(t, changes)
	if unavailable.STT != primary {
		t.Fatalf("unavailable STT = %v, want primary", unavailable.STT.Label())
	}
	if unavailable.Available {
		t.Fatal("unavailable event Available = true, want false")
	}

	available := receiveAvailabilityChange(t, changes)
	if available.STT != primary {
		t.Fatalf("available STT = %v, want primary", available.STT.Label())
	}
	if !available.Available {
		t.Fatal("available event Available = false, want true")
	}
}

func TestFallbackAdapterCanUnsubscribeAvailabilityChanges(t *testing.T) {
	primaryErr := errors.New("primary recognize failed")
	primary := &metadataSTT{
		label:         "primary",
		capabilities:  STTCapabilities{Streaming: true},
		recognizeErrs: []error{primaryErr},
	}
	fallback := &metadataSTT{
		label:        "fallback",
		capabilities: STTCapabilities{Streaming: true},
		recognizeResult: &SpeechEvent{
			Type:         SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{Text: "fallback"}},
		},
	}
	adapter := NewFallbackAdapterWithOptions([]STT{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerSTT: 0,
	})
	changes := make(chan AvailabilityChangedEvent, 2)
	unsubscribe := adapter.OnAvailabilityChanged(func(event AvailabilityChangedEvent) {
		changes <- event
	})
	unsubscribe()

	if _, err := adapter.Recognize(context.Background(), nil, "en"); err != nil {
		t.Fatalf("Recognize returned error: %v", err)
	}

	select {
	case event := <-changes:
		t.Fatalf("received availability change after unsubscribe: %#v", event)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestFallbackAdapterRecoverySurvivesCallerContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	releaseRecovery := make(chan struct{})
	primary := &contextRecoverySTT{
		releaseRecovery: releaseRecovery,
		recoveryStarted: make(chan struct{}, 1),
	}
	fallback := &metadataSTT{
		label:        "fallback",
		capabilities: STTCapabilities{Streaming: true},
		recognizeResult: &SpeechEvent{
			Type:         SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{Text: "fallback"}},
		},
	}
	adapter := NewFallbackAdapterWithOptions([]STT{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerSTT: 0,
	})

	event, err := adapter.Recognize(ctx, nil, "en")
	if err != nil {
		t.Fatalf("first Recognize returned error: %v", err)
	}
	if got := event.Alternatives[0].Text; got != "fallback" {
		t.Fatalf("first recognized text = %q, want fallback", got)
	}

	select {
	case <-primary.recoveryStarted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for recovery attempt")
	}
	cancel()
	close(releaseRecovery)
	waitForContextRecoveryCalls(t, primary, 2)
	waitForProviderAvailable(t, adapter, 0)

	event, err = adapter.Recognize(context.Background(), nil, "en")
	if err != nil {
		t.Fatalf("second Recognize returned error: %v", err)
	}
	if got := event.Alternatives[0].Text; got != "primary" {
		t.Fatalf("second recognized text = %q, want recovered primary", got)
	}
}

func TestFallbackAdapterCloseCancelsRecognizeRecovery(t *testing.T) {
	primary := &closeCancelRecoverySTT{
		recoveryStarted:  make(chan struct{}, 1),
		recoveryCanceled: make(chan struct{}, 1),
	}
	fallback := &metadataSTT{
		label:        "fallback",
		capabilities: STTCapabilities{Streaming: true},
		recognizeResult: &SpeechEvent{
			Type:         SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{Text: "fallback"}},
		},
	}
	adapter := NewFallbackAdapterWithOptions([]STT{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerSTT: 0,
	})

	event, err := adapter.Recognize(context.Background(), nil, "en")
	if err != nil {
		t.Fatalf("Recognize returned error: %v", err)
	}
	if got := event.Alternatives[0].Text; got != "fallback" {
		t.Fatalf("recognized text = %q, want fallback", got)
	}

	select {
	case <-primary.recoveryStarted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for recovery attempt")
	}
	if err := adapter.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	select {
	case <-primary.recoveryCanceled:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for recovery cancellation")
	}
}

func TestFallbackStreamSkipsUnavailableSTTFromRecognizeFailure(t *testing.T) {
	primaryErr := errors.New("primary recognize failed")
	primary := &metadataSTT{
		label:         "primary",
		capabilities:  STTCapabilities{Streaming: true},
		recognizeErrs: []error{primaryErr, primaryErr},
		stream: &metadataRecognizeStream{events: []*SpeechEvent{{
			Type:         SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{Text: "primary stream"}},
		}}},
	}
	fallback := &metadataSTT{
		label:        "fallback",
		capabilities: STTCapabilities{Streaming: true},
		recognizeResult: &SpeechEvent{
			Type:         SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{Text: "fallback recognize"}},
		},
		stream: &metadataRecognizeStream{events: []*SpeechEvent{{
			Type:         SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{Text: "fallback stream"}},
		}}},
	}
	adapter := NewFallbackAdapterWithOptions([]STT{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerSTT: 0,
	})

	if _, err := adapter.Recognize(context.Background(), nil, "en"); err != nil {
		t.Fatalf("Recognize returned error: %v", err)
	}
	waitForRecognizeCalls(t, primary, 2)

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := event.Alternatives[0].Text; got != "fallback stream" {
		t.Fatalf("stream text = %q, want fallback stream", got)
	}
	if primary.streamCalls != 1 {
		t.Fatalf("primary stream calls = %d, want one recovery stream while active stream skips unavailable provider", primary.streamCalls)
	}
	if fallback.streamCalls != 1 {
		t.Fatalf("fallback stream calls = %d, want 1", fallback.streamCalls)
	}
}

func TestFallbackStreamRecoversSkippedUnavailableProvider(t *testing.T) {
	primaryErr := errors.New("primary recognize failed")
	primary := &metadataSTT{
		label:         "primary",
		capabilities:  STTCapabilities{Streaming: true},
		recognizeErrs: []error{primaryErr, primaryErr},
		streams: []RecognizeStream{
			&metadataRecognizeStream{events: []*SpeechEvent{{
				Type:         SpeechEventFinalTranscript,
				Alternatives: []SpeechData{{Text: "primary recovered"}},
			}}},
			&metadataRecognizeStream{events: []*SpeechEvent{{
				Type:         SpeechEventFinalTranscript,
				Alternatives: []SpeechData{{Text: "primary active"}},
			}}},
		},
	}
	fallback := &metadataSTT{
		label:        "fallback",
		capabilities: STTCapabilities{Streaming: true},
		recognizeResult: &SpeechEvent{
			Type:         SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{Text: "fallback recognize"}},
		},
		stream: &metadataRecognizeStream{events: []*SpeechEvent{{
			Type:         SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{Text: "fallback stream"}},
		}}},
	}
	adapter := NewFallbackAdapterWithOptions([]STT{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerSTT: 0,
	})

	if _, err := adapter.Recognize(context.Background(), nil, "en"); err != nil {
		t.Fatalf("Recognize returned error: %v", err)
	}
	waitForRecognizeCalls(t, primary, 2)

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := event.Alternatives[0].Text; got != "fallback stream" {
		t.Fatalf("stream text = %q, want fallback stream", got)
	}

	waitForStreamCalls(t, primary, 1)
	waitForProviderAvailable(t, adapter, 0)

	nextStream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("second Stream returned error: %v", err)
	}
	defer nextStream.Close()

	event, err = nextStream.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if got := event.Alternatives[0].Text; got != "primary active" {
		t.Fatalf("second stream text = %q, want primary active", got)
	}
}

func TestFallbackStreamRecoversFailedProviderInBackground(t *testing.T) {
	firstFrame := &model.AudioFrame{Data: []byte("1"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}
	primaryFailure := &blockingFailRecognizeStream{
		err:     errors.New("primary stream failed"),
		release: make(chan struct{}),
	}
	recovery := &metadataRecognizeStream{events: []*SpeechEvent{{
		Type:         SpeechEventFinalTranscript,
		Alternatives: []SpeechData{{Text: "primary recovered"}},
	}}}
	active := &metadataRecognizeStream{events: []*SpeechEvent{{
		Type:         SpeechEventFinalTranscript,
		Alternatives: []SpeechData{{Text: "primary active"}},
	}}}
	primary := &metadataSTT{
		label:        "primary",
		capabilities: STTCapabilities{Streaming: true},
		streams: []RecognizeStream{
			primaryFailure,
			recovery,
			active,
		},
	}
	fallback := &metadataSTT{
		label:        "fallback",
		capabilities: STTCapabilities{Streaming: true},
		stream: &metadataRecognizeStream{events: []*SpeechEvent{{
			Type:         SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{Text: "fallback stream"}},
		}}},
	}
	adapter := NewFallbackAdapterWithOptions([]STT{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerSTT: 0,
	})

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if err := stream.PushFrame(firstFrame); err != nil {
		t.Fatalf("PushFrame returned error: %v", err)
	}

	close(primaryFailure.release)

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := event.Alternatives[0].Text; got != "fallback stream" {
		t.Fatalf("first stream text = %q, want fallback stream", got)
	}

	waitForStreamCalls(t, primary, 2)
	waitForProviderAvailable(t, adapter, 0)

	nextStream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("second Stream returned error: %v", err)
	}
	defer nextStream.Close()

	event, err = nextStream.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if got := event.Alternatives[0].Text; got != "primary active" {
		t.Fatalf("second stream text = %q, want recovered primary active", got)
	}
	if primary.streamCalls != 3 {
		t.Fatalf("primary stream calls = %d, want failure + recovery + active", primary.streamCalls)
	}
}

func TestFallbackStreamPropagatesTimingAnchorsToRecoveryStream(t *testing.T) {
	primaryFailure := &blockingFailRecognizeStream{
		err:     errors.New("primary stream failed"),
		release: make(chan struct{}),
	}
	recovery := &metadataRecognizeStream{events: []*SpeechEvent{{
		Type:         SpeechEventFinalTranscript,
		Alternatives: []SpeechData{{Text: "primary recovered"}},
	}}}
	primary := &metadataSTT{
		label:        "primary",
		capabilities: STTCapabilities{Streaming: true},
		streams: []RecognizeStream{
			primaryFailure,
			recovery,
		},
	}
	fallback := &metadataSTT{
		label:        "fallback",
		capabilities: STTCapabilities{Streaming: true},
		stream: &metadataRecognizeStream{events: []*SpeechEvent{{
			Type:         SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{Text: "fallback stream"}},
		}}},
	}
	adapter := NewFallbackAdapterWithOptions([]STT{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerSTT: 0,
	})

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	timing, ok := stream.(StreamTiming)
	if !ok {
		t.Fatal("fallback stream does not implement StreamTiming")
	}
	timing.SetStartTimeOffset(7.5)
	timing.SetStartTime(123.25)

	close(primaryFailure.release)

	if _, err := stream.Next(); err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	waitForStreamCalls(t, primary, 2)
	waitForProviderAvailable(t, adapter, 0)

	if recovery.startTimeOffset != 7.5 {
		t.Fatalf("recovery StartTimeOffset = %v, want 7.5", recovery.startTimeOffset)
	}
	if recovery.startTime != 123.25 {
		t.Fatalf("recovery StartTime = %v, want 123.25", recovery.startTime)
	}
}

func TestFallbackStreamRecoversProviderAfterStreamStartFailure(t *testing.T) {
	startErr := errors.New("primary stream start failed")
	recovery := &metadataRecognizeStream{events: []*SpeechEvent{{
		Type:         SpeechEventFinalTranscript,
		Alternatives: []SpeechData{{Text: "primary recovered"}},
	}}}
	active := &metadataRecognizeStream{events: []*SpeechEvent{{
		Type:         SpeechEventFinalTranscript,
		Alternatives: []SpeechData{{Text: "primary active"}},
	}}}
	primary := &metadataSTT{
		label:        "primary",
		capabilities: STTCapabilities{Streaming: true},
		streamErrs:   []error{startErr},
		streams: []RecognizeStream{
			recovery,
			active,
		},
	}
	fallback := &metadataSTT{
		label:        "fallback",
		capabilities: STTCapabilities{Streaming: true},
		stream: &metadataRecognizeStream{events: []*SpeechEvent{{
			Type:         SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{Text: "fallback stream"}},
		}}},
	}
	adapter := NewFallbackAdapterWithOptions([]STT{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerSTT: 0,
	})

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := event.Alternatives[0].Text; got != "fallback stream" {
		t.Fatalf("first stream text = %q, want fallback stream", got)
	}

	waitForStreamCalls(t, primary, 2)
	waitForProviderAvailable(t, adapter, 0)

	nextStream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("second Stream returned error: %v", err)
	}
	defer nextStream.Close()

	event, err = nextStream.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if got := event.Alternatives[0].Text; got != "primary active" {
		t.Fatalf("second stream text = %q, want recovered primary active", got)
	}
}

func TestFallbackStreamForwardsOnlyNewInputToRecoveringProvider(t *testing.T) {
	firstFrame := &model.AudioFrame{Data: []byte("1"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}
	secondFrame := &model.AudioFrame{Data: []byte("2"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}
	primaryFailure := &blockingFailRecognizeStream{
		err:     errors.New("primary stream failed"),
		release: make(chan struct{}),
	}
	recovery := &liveRecoveryStream{
		release: make(chan struct{}),
		event: &SpeechEvent{
			Type:         SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{Text: "primary recovered"}},
		},
	}
	primary := &metadataSTT{
		label:        "primary",
		capabilities: STTCapabilities{Streaming: true},
		streams: []RecognizeStream{
			primaryFailure,
			recovery,
		},
	}
	fallback := &metadataSTT{
		label:        "fallback",
		capabilities: STTCapabilities{Streaming: true},
		stream:       newBlockingRecognizeStream(),
	}
	adapter := NewFallbackAdapterWithOptions([]STT{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerSTT: 0,
	})

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(firstFrame); err != nil {
		t.Fatalf("PushFrame(first) returned error: %v", err)
	}
	close(primaryFailure.release)
	waitForStreamCalls(t, primary, 2)
	assertNoRecoveryCall(t, recovery, "push:1")

	if err := stream.PushFrame(secondFrame); err != nil {
		t.Fatalf("PushFrame(second) returned error: %v", err)
	}
	waitForRecoveryCall(t, recovery, "push:2")
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	waitForRecoveryCall(t, recovery, "flush")
	ending, ok := stream.(InputEnding)
	if !ok {
		t.Fatal("stream does not implement InputEnding")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput returned error: %v", err)
	}
	waitForRecoveryCall(t, recovery, "end_input")
	close(recovery.release)
}

func TestFallbackStreamCloseClosesRecoveringProvider(t *testing.T) {
	firstFrame := &model.AudioFrame{Data: []byte("1"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}
	primaryFailure := &blockingFailRecognizeStream{
		err:     errors.New("primary stream failed"),
		release: make(chan struct{}),
	}
	recovery := &liveRecoveryStream{
		release: make(chan struct{}),
		event: &SpeechEvent{
			Type:         SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{Text: "primary recovered"}},
		},
	}
	primary := &metadataSTT{
		label:        "primary",
		capabilities: STTCapabilities{Streaming: true},
		streams: []RecognizeStream{
			primaryFailure,
			recovery,
		},
	}
	fallback := &metadataSTT{
		label:        "fallback",
		capabilities: STTCapabilities{Streaming: true},
		stream:       newBlockingRecognizeStream(),
	}
	adapter := NewFallbackAdapterWithOptions([]STT{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerSTT: 0,
	})

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if err := stream.PushFrame(firstFrame); err != nil {
		t.Fatalf("PushFrame returned error: %v", err)
	}
	close(primaryFailure.release)
	waitForStreamCalls(t, primary, 2)

	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	waitForRecoveryClosed(t, recovery)
	close(recovery.release)
}

func TestFallbackAdapterCloseClosesStreamRecovery(t *testing.T) {
	firstFrame := &model.AudioFrame{Data: []byte("1"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}
	primaryFailure := &blockingFailRecognizeStream{
		err:     errors.New("primary stream failed"),
		release: make(chan struct{}),
	}
	recovery := &liveRecoveryStream{
		release: make(chan struct{}),
		event: &SpeechEvent{
			Type:         SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{Text: "primary recovered"}},
		},
	}
	primary := &metadataSTT{
		label:        "primary",
		capabilities: STTCapabilities{Streaming: true},
		streams: []RecognizeStream{
			primaryFailure,
			recovery,
		},
	}
	fallback := &metadataSTT{
		label:        "fallback",
		capabilities: STTCapabilities{Streaming: true},
		stream:       newBlockingRecognizeStream(),
	}
	adapter := NewFallbackAdapterWithOptions([]STT{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerSTT: 0,
	})

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if err := stream.PushFrame(firstFrame); err != nil {
		t.Fatalf("PushFrame returned error: %v", err)
	}
	close(primaryFailure.release)
	waitForStreamCalls(t, primary, 2)

	if err := adapter.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	waitForRecoveryClosed(t, recovery)
	close(recovery.release)
}

func TestFallbackStreamRetriesSameSTTBeforeFallback(t *testing.T) {
	firstErr := errors.New("primary stream failed")
	primary := &metadataSTT{
		label:        "primary",
		capabilities: STTCapabilities{Streaming: true},
		streams: []RecognizeStream{
			&metadataRecognizeStream{err: firstErr},
			&metadataRecognizeStream{events: []*SpeechEvent{{
				Type:         SpeechEventFinalTranscript,
				Alternatives: []SpeechData{{Text: "primary recovered"}},
			}}},
		},
	}
	fallback := &metadataSTT{
		label:        "fallback",
		capabilities: STTCapabilities{Streaming: true},
		stream: &metadataRecognizeStream{events: []*SpeechEvent{{
			Type:         SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{Text: "fallback"}},
		}}},
	}
	adapter := NewFallbackAdapterWithOptions([]STT{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerSTT: 1,
	})

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := event.Alternatives[0].Text; got != "primary recovered" {
		t.Fatalf("stream text = %q, want primary recovered", got)
	}
	if primary.streamCalls != 2 {
		t.Fatalf("primary stream calls = %d, want 2", primary.streamCalls)
	}
	if fallback.streamCalls != 0 {
		t.Fatalf("fallback stream calls = %d, want 0", fallback.streamCalls)
	}
}

func TestFallbackStreamWaitsRetryIntervalBeforeSameSTTRetry(t *testing.T) {
	firstErr := errors.New("primary stream failed")
	primary := &metadataSTT{
		label:        "primary",
		capabilities: STTCapabilities{Streaming: true},
		streams: []RecognizeStream{
			&metadataRecognizeStream{err: firstErr},
			&metadataRecognizeStream{events: []*SpeechEvent{{
				Type:         SpeechEventFinalTranscript,
				Alternatives: []SpeechData{{Text: "primary recovered"}},
			}}},
		},
	}
	adapter := NewFallbackAdapterWithOptions([]STT{primary}, FallbackAdapterOptions{
		MaxRetryPerSTT: 1,
		RetryInterval:  30 * time.Millisecond,
	})

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	startedAt := time.Now()
	event, err := stream.Next()
	elapsed := time.Since(startedAt)
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := event.Alternatives[0].Text; got != "primary recovered" {
		t.Fatalf("stream text = %q, want primary recovered", got)
	}
	if elapsed < 25*time.Millisecond {
		t.Fatalf("Next elapsed = %s, want retry interval delay", elapsed)
	}
}

func TestFallbackStreamTimesOutBlockedProvider(t *testing.T) {
	primaryStream := newBlockingRecognizeStream()
	fallbackStream := &metadataRecognizeStream{events: []*SpeechEvent{{
		Type:         SpeechEventFinalTranscript,
		Alternatives: []SpeechData{{Text: "fallback stream"}},
	}}}
	primary := &metadataSTT{
		label:        "primary",
		capabilities: STTCapabilities{Streaming: true},
		streams:      []RecognizeStream{primaryStream},
	}
	fallback := &metadataSTT{
		label:        "fallback",
		capabilities: STTCapabilities{Streaming: true},
		stream:       fallbackStream,
	}
	adapter := NewFallbackAdapterWithOptions([]STT{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerSTT: 0,
		AttemptTimeout: 20 * time.Millisecond,
	})

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	eventCh := make(chan *SpeechEvent, 1)
	errCh := make(chan error, 1)
	go func() {
		event, err := stream.Next()
		if err != nil {
			errCh <- err
			return
		}
		eventCh <- event
	}()

	select {
	case event := <-eventCh:
		if got := event.Alternatives[0].Text; got != "fallback stream" {
			t.Fatalf("stream text = %q, want fallback stream", got)
		}
	case err := <-errCh:
		t.Fatalf("Next returned error: %v", err)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for fallback stream after blocked provider")
	}
	if primary.streamCalls != 2 {
		t.Fatalf("primary stream calls = %d, want active attempt plus recovery attempt", primary.streamCalls)
	}
	if fallback.streamCalls != 1 {
		t.Fatalf("fallback stream calls = %d, want 1", fallback.streamCalls)
	}
}

func TestFallbackStreamTimesOutBlockedRecoveryProvider(t *testing.T) {
	primaryFailure := &blockingFailRecognizeStream{
		err:     errors.New("primary stream failed"),
		release: make(chan struct{}),
	}
	recovery := newBlockingRecognizeStream()
	primary := &metadataSTT{
		label:        "primary",
		capabilities: STTCapabilities{Streaming: true},
		streams: []RecognizeStream{
			primaryFailure,
			recovery,
		},
	}
	fallback := &metadataSTT{
		label:        "fallback",
		capabilities: STTCapabilities{Streaming: true},
		stream:       newHeartbeatRecognizeStream(5 * time.Millisecond),
	}
	adapter := NewFallbackAdapterWithOptions([]STT{primary, fallback}, FallbackAdapterOptions{
		MaxRetryPerSTT: 0,
		AttemptTimeout: 20 * time.Millisecond,
	})

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	nextErr := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		nextErr <- err
	}()

	close(primaryFailure.release)
	waitForStreamCalls(t, primary, 2)
	waitForBlockingStreamClosed(t, recovery)
	adapter.mu.Lock()
	recovering := adapter.recoveringStream[0]
	adapter.mu.Unlock()
	if recovering {
		t.Fatal("primary provider is still marked as recovering after recovery timeout")
	}
}

func TestFallbackStreamReplaysFlushBoundariesOnRetry(t *testing.T) {
	firstFrame := &model.AudioFrame{Data: []byte("1"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}
	secondFrame := &model.AudioFrame{Data: []byte("2"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}
	primaryFailure := &blockingFailRecognizeStream{
		err:     errors.New("primary stream failed"),
		release: make(chan struct{}),
	}
	recovered := &metadataRecognizeStream{events: []*SpeechEvent{{
		Type:         SpeechEventFinalTranscript,
		Alternatives: []SpeechData{{Text: "primary recovered"}},
	}}}
	primary := &metadataSTT{
		label:        "primary",
		capabilities: STTCapabilities{Streaming: true},
		streams: []RecognizeStream{
			primaryFailure,
			recovered,
		},
	}
	adapter := NewFallbackAdapterWithOptions([]STT{primary}, FallbackAdapterOptions{
		MaxRetryPerSTT: 1,
	})

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(firstFrame); err != nil {
		t.Fatalf("PushFrame(first) returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	if err := stream.PushFrame(secondFrame); err != nil {
		t.Fatalf("PushFrame(second) returned error: %v", err)
	}

	close(primaryFailure.release)

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := event.Alternatives[0].Text; got != "primary recovered" {
		t.Fatalf("stream text = %q, want primary recovered", got)
	}

	wantCalls := []string{"push:1", "flush", "push:2"}
	if strings.Join(recovered.calls, ",") != strings.Join(wantCalls, ",") {
		t.Fatalf("replayed stream calls = %#v, want %#v", recovered.calls, wantCalls)
	}
}

func TestFallbackStreamPropagatesTimingAnchorsOnRetry(t *testing.T) {
	primaryFailure := &blockingFailRecognizeStream{
		err:     errors.New("primary stream failed"),
		release: make(chan struct{}),
	}
	recovered := &metadataRecognizeStream{events: []*SpeechEvent{{
		Type:         SpeechEventFinalTranscript,
		Alternatives: []SpeechData{{Text: "primary recovered"}},
	}}}
	primary := &metadataSTT{
		label:        "primary",
		capabilities: STTCapabilities{Streaming: true},
		streams: []RecognizeStream{
			primaryFailure,
			recovered,
		},
	}
	adapter := NewFallbackAdapterWithOptions([]STT{primary}, FallbackAdapterOptions{
		MaxRetryPerSTT: 1,
	})

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	timing, ok := stream.(StreamTiming)
	if !ok {
		t.Fatal("fallback stream does not implement StreamTiming")
	}
	timing.SetStartTimeOffset(3.25)
	timing.SetStartTime(42.5)

	close(primaryFailure.release)

	if _, err := stream.Next(); err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if recovered.startTimeOffset != 3.25 {
		t.Fatalf("recovered StartTimeOffset = %v, want 3.25", recovered.startTimeOffset)
	}
	if recovered.startTime != 42.5 {
		t.Fatalf("recovered StartTime = %v, want 42.5", recovered.startTime)
	}

	timing.SetStartTimeOffset(-1)
	timing.SetStartTime(-2)
	if timing.StartTimeOffset() < 0 {
		t.Fatalf("negative StartTimeOffset was stored: %v", timing.StartTimeOffset())
	}
	if timing.StartTime() < 0 {
		t.Fatalf("negative StartTime was stored: %v", timing.StartTime())
	}
}

func TestFallbackStreamRejectsMismatchedSampleRates(t *testing.T) {
	inner := &metadataRecognizeStream{events: []*SpeechEvent{{Type: SpeechEventFinalTranscript}}}
	adapter := NewFallbackAdapter([]STT{
		&metadataSTT{
			label:        "primary",
			capabilities: STTCapabilities{Streaming: true},
			stream:       inner,
		},
	})

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("first"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err != nil {
		t.Fatalf("PushFrame(first) returned error: %v", err)
	}
	err = stream.PushFrame(&model.AudioFrame{Data: []byte("second"), SampleRate: 8000, NumChannels: 1, SamplesPerChannel: 1})
	if err == nil {
		t.Fatal("PushFrame(second) returned nil, want sample-rate mismatch error")
	}
	if strings.Join(inner.calls, ",") != "push:first" {
		t.Fatalf("inner calls = %#v, want only first frame forwarded", inner.calls)
	}
}

func TestFallbackStreamEndInputFlushesAndRejectsMoreInput(t *testing.T) {
	inner := &metadataRecognizeStream{events: []*SpeechEvent{{Type: SpeechEventFinalTranscript}}}
	adapter := NewFallbackAdapter([]STT{
		&metadataSTT{
			label:        "primary",
			capabilities: STTCapabilities{Streaming: true},
			stream:       inner,
		},
	})

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	ending, ok := stream.(InputEnding)
	if !ok {
		t.Fatal("stream does not implement InputEnding")
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("first"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err != nil {
		t.Fatalf("PushFrame returned error: %v", err)
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput returned error: %v", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("late"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err == nil {
		t.Fatal("PushFrame after EndInput returned nil, want error")
	}
	if err := stream.Flush(); err == nil {
		t.Fatal("Flush after EndInput returned nil, want error")
	}

	want := []string{"push:first", "flush", "end_input"}
	if len(inner.calls) != len(want) {
		t.Fatalf("inner call count = %d, want %d: %#v", len(inner.calls), len(want), inner.calls)
	}
	if strings.Join(inner.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("inner calls = %#v, want %#v", inner.calls, want)
	}
}

func TestFallbackStreamForwardsEndInput(t *testing.T) {
	inner := &metadataRecognizeStream{events: []*SpeechEvent{{Type: SpeechEventFinalTranscript}}}
	adapter := NewFallbackAdapter([]STT{
		&metadataSTT{
			label:        "primary",
			capabilities: STTCapabilities{Streaming: true},
			stream:       inner,
		},
	})

	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	ending, ok := stream.(InputEnding)
	if !ok {
		t.Fatal("stream does not implement InputEnding")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput returned error: %v", err)
	}

	want := "flush,end_input"
	if len(inner.calls) != 2 {
		t.Fatalf("inner call count = %d, want 2: %#v", len(inner.calls), inner.calls)
	}
	if strings.Join(inner.calls, ",") != want {
		t.Fatalf("inner calls = %#v, want flush,end_input", inner.calls)
	}
}

type metadataSTT struct {
	MetricsEmitter
	ErrorEmitter

	mu               sync.Mutex
	label            string
	capabilities     STTCapabilities
	stream           RecognizeStream
	streams          []RecognizeStream
	streamErrs       []error
	streamCalls      int
	recognizeResult  *SpeechEvent
	recognizeResults []*SpeechEvent
	recognizeErrs    []error
	recognizeCalls   int
}

func (m *metadataSTT) Label() string {
	return m.label
}

func (m *metadataSTT) Capabilities() STTCapabilities {
	return m.capabilities
}

func (m *metadataSTT) Stream(context.Context, string) (RecognizeStream, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streamCalls++
	if len(m.streamErrs) > 0 {
		err := m.streamErrs[0]
		m.streamErrs = m.streamErrs[1:]
		if err != nil {
			return nil, err
		}
	}
	if len(m.streams) > 0 {
		stream := m.streams[0]
		m.streams = m.streams[1:]
		return stream, nil
	}
	return m.stream, nil
}

func (m *metadataSTT) Recognize(context.Context, []*model.AudioFrame, string) (*SpeechEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recognizeCalls++
	if len(m.recognizeErrs) > 0 || len(m.recognizeResults) > 0 {
		var err error
		if len(m.recognizeErrs) > 0 {
			err = m.recognizeErrs[0]
			m.recognizeErrs = m.recognizeErrs[1:]
		}
		var event *SpeechEvent
		if len(m.recognizeResults) > 0 {
			event = m.recognizeResults[0]
			m.recognizeResults = m.recognizeResults[1:]
		} else {
			event = m.recognizeResult
		}
		return event, err
	}
	return m.recognizeResult, nil
}

type blockingContextSTT struct {
	mu           sync.Mutex
	label        string
	capabilities STTCapabilities
	started      chan struct{}
	canceled     bool
}

func (s *blockingContextSTT) Label() string {
	return s.label
}

func (s *blockingContextSTT) Capabilities() STTCapabilities {
	return s.capabilities
}

func (s *blockingContextSTT) Stream(context.Context, string) (RecognizeStream, error) {
	return nil, errors.New("stream unsupported")
}

func (s *blockingContextSTT) Recognize(ctx context.Context, _ []*model.AudioFrame, _ string) (*SpeechEvent, error) {
	select {
	case s.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	s.mu.Lock()
	s.canceled = true
	s.mu.Unlock()
	return nil, ctx.Err()
}

func (s *blockingContextSTT) wasCanceled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.canceled
}

type closeCancelRecoverySTT struct {
	mu               sync.Mutex
	calls            int
	recoveryStarted  chan struct{}
	recoveryCanceled chan struct{}
}

func (s *closeCancelRecoverySTT) Label() string {
	return "close-cancel-primary"
}

func (s *closeCancelRecoverySTT) Capabilities() STTCapabilities {
	return STTCapabilities{Streaming: true}
}

func (s *closeCancelRecoverySTT) Stream(context.Context, string) (RecognizeStream, error) {
	return nil, errors.New("stream unsupported")
}

func (s *closeCancelRecoverySTT) Recognize(ctx context.Context, _ []*model.AudioFrame, _ string) (*SpeechEvent, error) {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.mu.Unlock()
	if call == 1 {
		return nil, errors.New("primary failed")
	}
	select {
	case s.recoveryStarted <- struct{}{}:
	default:
	}
	<-ctx.Done()
	select {
	case s.recoveryCanceled <- struct{}{}:
	default:
	}
	return nil, ctx.Err()
}

func waitForRecognizeCalls(t *testing.T, stt *metadataSTT, want int) {
	t.Helper()
	deadline := time.After(200 * time.Millisecond)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		stt.mu.Lock()
		got := stt.recognizeCalls
		stt.mu.Unlock()
		if got >= want {
			return
		}

		select {
		case <-deadline:
			t.Fatalf("recognize calls did not reach %d", want)
		case <-ticker.C:
		}
	}
}

func receiveAvailabilityChange(t *testing.T, changes <-chan AvailabilityChangedEvent) AvailabilityChangedEvent {
	t.Helper()
	select {
	case event := <-changes:
		return event
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for availability change")
		return AvailabilityChangedEvent{}
	}
}

func waitForStreamCalls(t *testing.T, stt *metadataSTT, want int) {
	t.Helper()
	deadline := time.After(200 * time.Millisecond)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		stt.mu.Lock()
		got := stt.streamCalls
		stt.mu.Unlock()
		if got >= want {
			return
		}

		select {
		case <-deadline:
			t.Fatalf("stream calls did not reach %d", want)
		case <-ticker.C:
		}
	}
}

type contextRecoverySTT struct {
	mu              sync.Mutex
	calls           int
	releaseRecovery chan struct{}
	recoveryStarted chan struct{}
}

func (s *contextRecoverySTT) Label() string {
	return "context-primary"
}

func (s *contextRecoverySTT) Capabilities() STTCapabilities {
	return STTCapabilities{Streaming: true}
}

func (s *contextRecoverySTT) Stream(context.Context, string) (RecognizeStream, error) {
	return nil, errors.New("stream unsupported")
}

func (s *contextRecoverySTT) Recognize(ctx context.Context, _ []*model.AudioFrame, _ string) (*SpeechEvent, error) {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.mu.Unlock()

	if call == 1 {
		return nil, errors.New("primary failed")
	}
	if call == 2 {
		s.recoveryStarted <- struct{}{}
		<-s.releaseRecovery
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}
	return &SpeechEvent{
		Type:         SpeechEventFinalTranscript,
		Alternatives: []SpeechData{{Text: "primary"}},
	}, nil
}

func waitForContextRecoveryCalls(t *testing.T, stt *contextRecoverySTT, want int) {
	t.Helper()
	deadline := time.After(200 * time.Millisecond)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		stt.mu.Lock()
		got := stt.calls
		stt.mu.Unlock()
		if got >= want {
			return
		}

		select {
		case <-deadline:
			t.Fatalf("recognize calls did not reach %d", want)
		case <-ticker.C:
		}
	}
}

func waitForProviderAvailable(t *testing.T, adapter *FallbackAdapter, index int) {
	t.Helper()
	deadline := time.After(200 * time.Millisecond)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		if adapter.isAvailable(index) {
			return
		}

		select {
		case <-deadline:
			t.Fatalf("provider %d did not become available", index)
		case <-ticker.C:
		}
	}
}

func nextFallbackStreamError(stream RecognizeStream) error {
	errCh := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		errCh <- err
	}()
	select {
	case err := <-errCh:
		return err
	case <-time.After(100 * time.Millisecond):
		return errors.New("timed out waiting for fallback stream Next")
	}
}

type metadataRecognizeStream struct {
	events          []*SpeechEvent
	index           int
	err             error
	calls           []string
	startTimeOffset float64
	startTime       float64
}

func (m *metadataRecognizeStream) PushFrame(frame *model.AudioFrame) error {
	m.calls = append(m.calls, "push:"+string(frame.Data))
	return nil
}

func (m *metadataRecognizeStream) Flush() error {
	m.calls = append(m.calls, "flush")
	return nil
}

func (m *metadataRecognizeStream) EndInput() error {
	m.calls = append(m.calls, "end_input")
	return nil
}

func (m *metadataRecognizeStream) Close() error {
	return nil
}

func (m *metadataRecognizeStream) Next() (*SpeechEvent, error) {
	if m.index < len(m.events) {
		event := m.events[m.index]
		m.index++
		return event, nil
	}
	if m.err != nil {
		return nil, m.err
	}
	return nil, io.EOF
}

func (m *metadataRecognizeStream) StartTimeOffset() float64 {
	return m.startTimeOffset
}

func (m *metadataRecognizeStream) SetStartTimeOffset(offset float64) {
	m.startTimeOffset = offset
}

func (m *metadataRecognizeStream) StartTime() float64 {
	return m.startTime
}

func (m *metadataRecognizeStream) SetStartTime(startTime float64) {
	m.startTime = startTime
}

type blockingFailRecognizeStream struct {
	err     error
	release chan struct{}
}

func (s *blockingFailRecognizeStream) PushFrame(*model.AudioFrame) error {
	return nil
}

func (s *blockingFailRecognizeStream) Flush() error {
	return nil
}

func (s *blockingFailRecognizeStream) Close() error {
	return nil
}

func (s *blockingFailRecognizeStream) Next() (*SpeechEvent, error) {
	<-s.release
	return nil, s.err
}

type blockingRecognizeStream struct {
	closeOnce sync.Once
	closeCh   chan struct{}
}

func newBlockingRecognizeStream() *blockingRecognizeStream {
	return &blockingRecognizeStream{closeCh: make(chan struct{})}
}

func (s *blockingRecognizeStream) PushFrame(*model.AudioFrame) error {
	return nil
}

func (s *blockingRecognizeStream) Flush() error {
	return nil
}

func (s *blockingRecognizeStream) Close() error {
	s.closeOnce.Do(func() {
		close(s.closeCh)
	})
	return nil
}

func (s *blockingRecognizeStream) Next() (*SpeechEvent, error) {
	<-s.closeCh
	return nil, io.EOF
}

type heartbeatRecognizeStream struct {
	interval  time.Duration
	closeOnce sync.Once
	closeCh   chan struct{}
}

func newHeartbeatRecognizeStream(interval time.Duration) *heartbeatRecognizeStream {
	return &heartbeatRecognizeStream{interval: interval, closeCh: make(chan struct{})}
}

func (s *heartbeatRecognizeStream) PushFrame(*model.AudioFrame) error {
	return nil
}

func (s *heartbeatRecognizeStream) Flush() error {
	return nil
}

func (s *heartbeatRecognizeStream) Close() error {
	s.closeOnce.Do(func() {
		close(s.closeCh)
	})
	return nil
}

func (s *heartbeatRecognizeStream) Next() (*SpeechEvent, error) {
	timer := time.NewTimer(s.interval)
	defer timer.Stop()
	select {
	case <-timer.C:
		return &SpeechEvent{
			Type:         SpeechEventFinalTranscript,
			Alternatives: []SpeechData{{Text: "heartbeat"}},
		}, nil
	case <-s.closeCh:
		return nil, io.EOF
	}
}

type liveRecoveryStream struct {
	mu      sync.Mutex
	calls   []string
	release chan struct{}
	event   *SpeechEvent
	closed  bool
}

func (s *liveRecoveryStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, "push:"+string(frame.Data))
	return nil
}

func (s *liveRecoveryStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, "flush")
	return nil
}

func (s *liveRecoveryStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, "end_input")
	return nil
}

func (s *liveRecoveryStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *liveRecoveryStream) Next() (*SpeechEvent, error) {
	<-s.release
	if s.event != nil {
		event := s.event
		s.event = nil
		return event, nil
	}
	return nil, io.EOF
}

func waitForRecoveryCall(t *testing.T, stream *liveRecoveryStream, want string) {
	t.Helper()
	deadline := time.After(200 * time.Millisecond)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		stream.mu.Lock()
		for _, call := range stream.calls {
			if call == want {
				stream.mu.Unlock()
				return
			}
		}
		calls := append([]string(nil), stream.calls...)
		stream.mu.Unlock()

		select {
		case <-deadline:
			t.Fatalf("recovery calls = %#v, want %s", calls, want)
		case <-ticker.C:
		}
	}
}

func assertNoRecoveryCall(t *testing.T, stream *liveRecoveryStream, unwanted string) {
	t.Helper()
	deadline := time.After(20 * time.Millisecond)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		stream.mu.Lock()
		calls := append([]string(nil), stream.calls...)
		stream.mu.Unlock()
		for _, call := range calls {
			if call == unwanted {
				t.Fatalf("recovery calls = %#v, did not want %s", calls, unwanted)
			}
		}

		select {
		case <-deadline:
			return
		case <-ticker.C:
		}
	}
}

func waitForRecoveryClosed(t *testing.T, stream *liveRecoveryStream) {
	t.Helper()
	deadline := time.After(200 * time.Millisecond)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		stream.mu.Lock()
		closed := stream.closed
		stream.mu.Unlock()
		if closed {
			return
		}

		select {
		case <-deadline:
			t.Fatal("recovery stream was not closed")
		case <-ticker.C:
		}
	}
}

func waitForBlockingStreamClosed(t *testing.T, stream *blockingRecognizeStream) {
	t.Helper()
	select {
	case <-stream.closeCh:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("blocking stream was not closed")
	}
}
