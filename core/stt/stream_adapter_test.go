package stt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestStreamAdapterPropagatesVADStartError(t *testing.T) {
	startErr := errors.New("vad start failed")
	stream, err := NewStreamAdapter(&fakeStreamAdapterSTT{}, &fakeStreamAdapterVAD{err: startErr}).Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	_, err = stream.Next()
	if !errors.Is(err, startErr) {
		t.Fatalf("Next error = %v, want VAD start error", err)
	}
}

func TestStreamAdapterRejectsNilVADStream(t *testing.T) {
	stream, err := NewStreamAdapter(&fakeStreamAdapterSTT{}, &fakeStreamAdapterVAD{}).Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	_, err = stream.Next()
	if err == nil || !strings.Contains(err.Error(), "nil VAD stream") {
		t.Fatalf("Next error = %v, want nil VAD stream error", err)
	}
}

func TestStreamAdapterRejectsTypedNilVADStream(t *testing.T) {
	var vadStream *fakeStreamAdapterVADStream
	stream, err := NewStreamAdapter(&fakeStreamAdapterSTT{}, &fakeStreamAdapterVAD{stream: vadStream}).Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	_, err = stream.Next()
	if err == nil || !strings.Contains(err.Error(), "nil VAD stream") {
		t.Fatalf("Next error = %v, want nil VAD stream error", err)
	}
}

func TestStreamAdapterCapabilitiesMatchReference(t *testing.T) {
	caps := NewStreamAdapter(&fakeStreamAdapterSTT{}, &fakeStreamAdapterVAD{}).Capabilities()

	if !caps.Streaming {
		t.Fatal("Streaming = false, want true")
	}
	if caps.InterimResults {
		t.Fatal("InterimResults = true, want false")
	}
	if caps.Diarization {
		t.Fatal("Diarization = true, want false")
	}
	if !caps.OfflineRecognize {
		t.Fatal("OfflineRecognize = false, want true because Recognize delegates to wrapped STT")
	}
}

func TestStreamAdapterExposesWrappedSTT(t *testing.T) {
	wrapped := &fakeStreamAdapterSTT{}
	adapter := NewStreamAdapter(wrapped, &fakeStreamAdapterVAD{})

	if adapter.WrappedSTT() != wrapped {
		t.Fatal("WrappedSTT did not return the wrapped STT")
	}
}

func TestStreamAdapterForwardsWrappedSTTMetrics(t *testing.T) {
	wrapped := &fakeStreamAdapterSTT{}
	adapter := NewStreamAdapter(wrapped, &fakeStreamAdapterVAD{})
	metricsCh := make(chan string, 1)

	unsubscribe := adapter.OnMetricsCollected(func(metrics *telemetry.STTMetrics) {
		metricsCh <- metrics.RequestID
	})
	defer unsubscribe()

	wrapped.EmitMetricsCollected(&telemetry.STTMetrics{RequestID: "req-1"})

	select {
	case requestID := <-metricsCh:
		if requestID != "req-1" {
			t.Fatalf("metrics RequestID = %q, want req-1", requestID)
		}
	default:
		t.Fatal("metrics handler was not called")
	}
}

func TestStreamAdapterCloseUnsubscribesWrappedSTTMetrics(t *testing.T) {
	wrapped := &fakeStreamAdapterSTT{}
	adapter := NewStreamAdapter(wrapped, &fakeStreamAdapterVAD{})
	metricsCh := make(chan string, 2)

	unsubscribe := adapter.OnMetricsCollected(func(metrics *telemetry.STTMetrics) {
		metricsCh <- metrics.RequestID
	})
	defer unsubscribe()

	wrapped.EmitMetricsCollected(&telemetry.STTMetrics{RequestID: "before"})
	select {
	case requestID := <-metricsCh:
		if requestID != "before" {
			t.Fatalf("metrics RequestID before Close = %q, want before", requestID)
		}
	default:
		t.Fatal("wrapped metrics before Close were not forwarded")
	}

	if err := adapter.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	wrapped.EmitMetricsCollected(&telemetry.STTMetrics{RequestID: "after"})
	adapter.EmitMetricsCollected(&telemetry.STTMetrics{RequestID: "local"})

	select {
	case requestID := <-metricsCh:
		if requestID != "local" {
			t.Fatalf("metrics RequestID after Close = %q, want adapter-local metric only", requestID)
		}
	default:
		t.Fatal("adapter-local metrics after Close were not forwarded")
	}
	select {
	case requestID := <-metricsCh:
		t.Fatalf("received wrapped metric after Close(): %q", requestID)
	default:
	}
}

func TestStreamAdapterMetricsUnsubscribeRemovesLocalAndProviderHandlers(t *testing.T) {
	wrapped := &fakeStreamAdapterSTT{}
	adapter := NewStreamAdapter(wrapped, &fakeStreamAdapterVAD{})
	metricsCh := make(chan string, 1)

	unsubscribe := adapter.OnMetricsCollected(func(metrics *telemetry.STTMetrics) {
		metricsCh <- metrics.RequestID
	})

	wrapped.EmitMetricsCollected(&telemetry.STTMetrics{RequestID: "before"})
	select {
	case requestID := <-metricsCh:
		if requestID != "before" {
			t.Fatalf("metrics RequestID before unsubscribe = %q, want before", requestID)
		}
	default:
		t.Fatal("wrapped metrics before unsubscribe were not forwarded")
	}

	unsubscribe()
	unsubscribe()
	wrapped.EmitMetricsCollected(&telemetry.STTMetrics{RequestID: "provider-after"})
	adapter.EmitMetricsCollected(&telemetry.STTMetrics{RequestID: "local-after"})

	select {
	case requestID := <-metricsCh:
		t.Fatalf("received metrics after unsubscribe: %q", requestID)
	default:
	}
}

func TestStreamAdapterDoesNotForwardWrappedSTTErrors(t *testing.T) {
	wrapped := &fakeStreamAdapterSTT{}
	adapter := NewStreamAdapter(wrapped, &fakeStreamAdapterVAD{})
	labelsCh := make(chan string, 2)

	unsubscribe := adapter.OnError(func(err *STTError) {
		labelsCh <- err.Label
	})
	defer unsubscribe()

	wrapped.EmitError(NewSTTError("wrapped", errors.New("wrapped stt failed"), true))
	adapter.EmitError(NewSTTError("adapter", errors.New("adapter failed"), true))

	select {
	case label := <-labelsCh:
		if label != "adapter" {
			t.Fatalf("error label = %q, want adapter-local error only", label)
		}
	default:
		t.Fatal("error handler was not called")
	}
	select {
	case label := <-labelsCh:
		t.Fatalf("unexpected forwarded wrapped STT error label %q", label)
	default:
	}
}

func TestStreamAdapterErrorUnsubscribeRemovesLocalHandler(t *testing.T) {
	wrapped := &fakeStreamAdapterSTT{}
	adapter := NewStreamAdapter(wrapped, &fakeStreamAdapterVAD{})
	labelsCh := make(chan string, 1)
	unsubscribe := adapter.OnError(func(err *STTError) {
		labelsCh <- err.Label
	})
	unsubscribe()
	unsubscribe()

	wrapped.EmitError(NewSTTError("wrapped", errors.New("wrapped stt failed"), true))
	adapter.EmitError(NewSTTError("adapter", errors.New("adapter failed"), true))

	select {
	case label := <-labelsCh:
		t.Fatalf("received error after unsubscribe: %q", label)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestStreamAdapterWrapperIsPublicReferenceType(t *testing.T) {
	var _ RecognizeStream = (*StreamAdapterWrapper)(nil)
	var _ StreamTiming = (*StreamAdapterWrapper)(nil)
	var _ InputEnding = (*StreamAdapterWrapper)(nil)
}

func TestStreamAdapterExposesTimingAnchors(t *testing.T) {
	before := time.Now()
	stream, err := NewStreamAdapter(&fakeStreamAdapterSTT{}, &fakeStreamAdapterVAD{
		stream: &fakeStreamAdapterVADStream{done: make(chan struct{})},
	}).Stream(context.Background(), "")
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
	timing.SetStartTimeOffset(1.5)
	timing.SetStartTime(24.0)

	if timing.StartTimeOffset() != 1.5 {
		t.Fatalf("StartTimeOffset = %v, want 1.5", timing.StartTimeOffset())
	}
	if timing.StartTime() != 24.0 {
		t.Fatalf("StartTime = %v, want 24.0", timing.StartTime())
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

func TestStreamAdapterReturnsEOFWhenVADCompletes(t *testing.T) {
	stream, err := NewStreamAdapter(&fakeStreamAdapterSTT{}, &fakeStreamAdapterVAD{
		stream: &fakeStreamAdapterVADStream{nextErr: io.EOF},
	}).Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want io.EOF", err)
	}
}

func TestStreamAdapterRecordsSTTStreamSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	oldTracer := telemetry.Tracer
	telemetry.Tracer = provider.Tracer("test")
	t.Cleanup(func() {
		telemetry.Tracer = oldTracer
		_ = provider.Shutdown(context.Background())
	})

	stream, err := NewStreamAdapter(
		&fakeStreamAdapterSTT{model: "speech-model", provider: "speech-provider"},
		&fakeStreamAdapterVAD{stream: &fakeStreamAdapterVADStream{nextErr: io.EOF}},
	).Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want EOF", err)
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(spans))
	}
	if spans[0].Name() != "stt_stream" {
		t.Fatalf("span name = %q, want stt_stream", spans[0].Name())
	}
	attrs := streamAdapterSpanAttributes(spans[0].Attributes())
	if attrs[telemetry.AttrGenAIRequestModel] != "speech-model" {
		t.Fatalf("span model attr = %q, want speech-model", attrs[telemetry.AttrGenAIRequestModel])
	}
	if attrs[telemetry.AttrGenAIProviderName] != "speech-provider" {
		t.Fatalf("span provider attr = %q, want speech-provider", attrs[telemetry.AttrGenAIProviderName])
	}
}

func TestStreamAdapterKeepsReturningEOFAfterVADCompletes(t *testing.T) {
	stream, err := NewStreamAdapter(&fakeStreamAdapterSTT{}, &fakeStreamAdapterVAD{
		stream: &fakeStreamAdapterVADStream{nextErr: io.EOF},
	}).Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("first Next error = %v, want io.EOF", err)
	}
	err = nextStreamAdapterSTTError(stream)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("second Next error = %v, want io.EOF", err)
	}
}

func TestStreamAdapterClosesVADStreamWhenRunCompletes(t *testing.T) {
	closedCh := make(chan struct{}, 1)
	stream, err := NewStreamAdapter(&fakeStreamAdapterSTT{}, &fakeStreamAdapterVAD{
		stream: &fakeStreamAdapterVADStream{nextErr: io.EOF, closedCh: closedCh},
	}).Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want io.EOF", err)
	}
	select {
	case <-closedCh:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for VAD Close")
	}
}

func TestStreamAdapterRejectsInputAfterRunCompletes(t *testing.T) {
	closedCh := make(chan struct{}, 1)
	stream, err := NewStreamAdapter(&fakeStreamAdapterSTT{}, &fakeStreamAdapterVAD{
		stream: &fakeStreamAdapterVADStream{nextErr: io.EOF, closedCh: closedCh},
	}).Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want io.EOF", err)
	}
	select {
	case <-closedCh:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for VAD Close")
	}

	err = stream.PushFrame(&model.AudioFrame{Data: []byte("late"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1})
	if err == nil {
		t.Fatal("PushFrame after stream completion returned nil, want error")
	}
}

func TestStreamAdapterPropagatesVADRuntimeError(t *testing.T) {
	runtimeErr := errors.New("vad failed")
	stream, err := NewStreamAdapter(&fakeStreamAdapterSTT{}, &fakeStreamAdapterVAD{
		stream: &fakeStreamAdapterVADStream{nextErr: runtimeErr},
	}).Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	_, err = stream.Next()
	if !errors.Is(err, runtimeErr) {
		t.Fatalf("Next error = %v, want VAD runtime error", err)
	}
}

func TestStreamAdapterCloseClosesVADStream(t *testing.T) {
	closedCh := make(chan struct{}, 1)
	startedCh := make(chan struct{}, 1)
	stream, err := NewStreamAdapter(&fakeStreamAdapterSTT{}, &fakeStreamAdapterVAD{
		startedCh: startedCh,
		stream:    &fakeStreamAdapterVADStream{closedCh: closedCh, done: make(chan struct{})},
	}).Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	select {
	case <-startedCh:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for VAD stream start")
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	select {
	case <-closedCh:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for VAD Close")
	}
}

func TestStreamAdapterCloseDoesNotPanicBlockedPushFrame(t *testing.T) {
	pushStartedCh := make(chan struct{}, 1)
	releasePushCh := make(chan struct{})
	stream, err := NewStreamAdapter(&fakeStreamAdapterSTT{}, &fakeStreamAdapterVAD{
		stream: &fakeStreamAdapterVADStream{
			pushStartedCh: pushStartedCh,
			releasePushCh: releasePushCh,
			done:          make(chan struct{}),
		},
	}).Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	frame := &model.AudioFrame{SampleRate: 16000}
	if err := stream.PushFrame(frame); err != nil {
		t.Fatalf("first PushFrame returned error: %v", err)
	}
	select {
	case <-pushStartedCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for VAD PushFrame to block")
	}

	wrapper := stream.(*streamAdapterWrapper)
	for range cap(wrapper.inputCh) {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatalf("buffering PushFrame returned error: %v", err)
		}
	}

	pushDone := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				pushDone <- fmt.Errorf("PushFrame panicked: %v", r)
			}
		}()
		pushDone <- stream.PushFrame(frame)
	}()

	select {
	case err := <-pushDone:
		t.Fatalf("blocked PushFrame returned before Close: %v", err)
	case <-time.After(10 * time.Millisecond):
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	close(releasePushCh)

	select {
	case err := <-pushDone:
		if err == nil || !strings.Contains(err.Error(), "stream closed") {
			t.Fatalf("blocked PushFrame error = %v, want stream closed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for blocked PushFrame to unblock")
	}
}

func TestStreamAdapterForwardsFlushToVAD(t *testing.T) {
	flushCh := make(chan struct{}, 1)
	stream, err := NewStreamAdapter(&fakeStreamAdapterSTT{}, &fakeStreamAdapterVAD{
		stream: &fakeStreamAdapterVADStream{flushCh: flushCh, done: make(chan struct{})},
	}).Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	select {
	case <-flushCh:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for VAD Flush")
	}
}

func TestStreamAdapterEndInputForwardsFlushAndEndThenRejectsMoreInput(t *testing.T) {
	flushCh := make(chan struct{}, 1)
	endInputCh := make(chan struct{}, 1)
	stream, err := NewStreamAdapter(&fakeStreamAdapterSTT{}, &fakeStreamAdapterVAD{
		stream: &fakeStreamAdapterVADStream{
			flushCh:              flushCh,
			endInputCh:           endInputCh,
			done:                 make(chan struct{}),
			disableEndInputFlush: true,
		},
	}).Stream(context.Background(), "")
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

	select {
	case <-endInputCh:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for VAD EndInput")
	}
	select {
	case <-flushCh:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for VAD Flush")
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("late"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err == nil {
		t.Fatal("PushFrame after EndInput returned nil, want error")
	}
	if err := stream.Flush(); err == nil {
		t.Fatal("Flush after EndInput returned nil, want error")
	}
	if err := ending.EndInput(); err == nil {
		t.Fatal("second EndInput returned nil, want error")
	}
	select {
	case <-endInputCh:
		t.Fatal("second EndInput forwarded another VAD EndInput")
	default:
	}
}

func TestStreamAdapterPropagatesVADPushFrameError(t *testing.T) {
	pushErr := errors.New("vad push failed")
	stream, err := NewStreamAdapter(&fakeStreamAdapterSTT{}, &fakeStreamAdapterVAD{
		stream: &fakeStreamAdapterVADStream{pushErr: pushErr, done: make(chan struct{})},
	}).Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(&model.AudioFrame{Data: []byte{1, 0}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err != nil {
		t.Fatalf("PushFrame returned error: %v", err)
	}

	err = nextStreamAdapterSTTError(stream)
	if !errors.Is(err, pushErr) {
		t.Fatalf("Next error = %v, want VAD push error", err)
	}
}

func TestStreamAdapterRejectsMismatchedSampleRates(t *testing.T) {
	stream, err := NewStreamAdapter(&fakeStreamAdapterSTT{}, &fakeStreamAdapterVAD{
		stream: &fakeStreamAdapterVADStream{done: make(chan struct{})},
	}).Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("first"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err != nil {
		t.Fatalf("PushFrame(first) returned error: %v", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("second"), SampleRate: 8000, NumChannels: 1, SamplesPerChannel: 1}); err == nil {
		t.Fatal("PushFrame(second) returned nil, want sample-rate mismatch error")
	}
}

func TestStreamAdapterPropagatesRecognizeError(t *testing.T) {
	recognizeErr := errors.New("recognize failed")
	stream, err := NewStreamAdapter(
		&fakeStreamAdapterSTT{recognizeErr: recognizeErr},
		&fakeStreamAdapterVAD{stream: &fakeStreamAdapterVADStream{
			events: []*vad.VADEvent{{
				Type:   vad.VADEventEndOfSpeech,
				Frames: []*model.AudioFrame{{Data: []byte{1, 0}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}},
			}},
			done: make(chan struct{}),
		}},
	).Stream(context.Background(), "")
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

	err = nextStreamAdapterSTTError(stream)
	if !errors.Is(err, recognizeErr) {
		t.Fatalf("Next error = %v, want recognize error", err)
	}
}

func TestStreamAdapterFinalTranscriptUsesFirstRecognizedAlternative(t *testing.T) {
	stream, err := NewStreamAdapter(
		&fakeStreamAdapterSTT{recognizeResult: &SpeechEvent{
			Type: SpeechEventInterimTranscript,
			Alternatives: []SpeechData{
				{Text: "first"},
				{Text: "second"},
			},
		}},
		&fakeStreamAdapterVAD{stream: &fakeStreamAdapterVADStream{
			events: []*vad.VADEvent{{
				Type:   vad.VADEventEndOfSpeech,
				Frames: []*model.AudioFrame{{Data: []byte{1, 0}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}},
			}},
			done: make(chan struct{}),
		}},
	).Stream(context.Background(), "")
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
	if event.Type != SpeechEventFinalTranscript {
		t.Fatalf("second event type = %s, want final_transcript", event.Type)
	}
	if len(event.Alternatives) != 1 {
		t.Fatalf("final alternatives = %d, want 1", len(event.Alternatives))
	}
	if event.Alternatives[0].Text != "first" {
		t.Fatalf("final text = %q, want first", event.Alternatives[0].Text)
	}
}

func TestStreamAdapterRecognizesBufferedFramesWhenVADEndOmitsFrames(t *testing.T) {
	firstFrame := &model.AudioFrame{Data: []byte("first"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}
	secondFrame := &model.AudioFrame{Data: []byte("second"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}
	wrapped := &fakeStreamAdapterSTT{recognizeResult: &SpeechEvent{
		Type:         SpeechEventFinalTranscript,
		Alternatives: []SpeechData{{Text: "buffered speech"}},
	}}
	stream, err := NewStreamAdapter(
		wrapped,
		&fakeStreamAdapterVAD{stream: &fakeStreamAdapterVADStream{
			events: []*vad.VADEvent{
				{Type: vad.VADEventStartOfSpeech},
				{Type: vad.VADEventEndOfSpeech},
			},
			pushCh:                make(chan struct{}, 2),
			waitPushesBeforeEvent: []int{2, 0},
			done:                  make(chan struct{}),
		}},
	).Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(firstFrame); err != nil {
		t.Fatalf("PushFrame(first) returned error: %v", err)
	}
	if err := stream.PushFrame(secondFrame); err != nil {
		t.Fatalf("PushFrame(second) returned error: %v", err)
	}

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if event.Type != SpeechEventStartOfSpeech {
		t.Fatalf("first event type = %s, want start_of_speech", event.Type)
	}

	event, err = stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if event.Type != SpeechEventEndOfSpeech {
		t.Fatalf("second event type = %s, want end_of_speech", event.Type)
	}

	event, err = nextStreamAdapterEvent(stream)
	if err != nil {
		t.Fatalf("third Next returned error: %v", err)
	}
	if event.Type != SpeechEventFinalTranscript {
		t.Fatalf("third event type = %s, want final_transcript", event.Type)
	}
	if got := len(wrapped.recognizeFrames); got != 2 {
		t.Fatalf("Recognize frame count = %d, want 2 buffered frames", got)
	}
}

func TestStreamAdapterRecognizesReferenceEmptyVADEndOfSpeech(t *testing.T) {
	wrapped := &fakeStreamAdapterSTT{recognizeResult: &SpeechEvent{
		Type:         SpeechEventFinalTranscript,
		Alternatives: []SpeechData{{Text: "empty vad speech"}},
	}}
	stream, err := NewStreamAdapter(
		wrapped,
		&fakeStreamAdapterVAD{stream: &fakeStreamAdapterVADStream{
			events: []*vad.VADEvent{{Type: vad.VADEventEndOfSpeech}},
			done:   make(chan struct{}),
		}},
	).Stream(context.Background(), "")
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

	event, err = nextStreamAdapterEvent(stream)
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if event.Type != SpeechEventFinalTranscript {
		t.Fatalf("second event type = %s, want final_transcript", event.Type)
	}
	if len(event.Alternatives) != 1 || event.Alternatives[0].Text != "empty vad speech" {
		t.Fatalf("final alternatives = %#v, want wrapped STT text", event.Alternatives)
	}
	if wrapped.recognizeCalls != 1 {
		t.Fatalf("Recognize calls = %d, want 1 for empty VAD end-of-speech", wrapped.recognizeCalls)
	}
	if len(wrapped.recognizeFrames) != 0 {
		t.Fatalf("Recognize frame count = %d, want 0", len(wrapped.recognizeFrames))
	}
}

func TestStreamAdapterBuffersFrameBeforeVADPushReturns(t *testing.T) {
	frame := &model.AudioFrame{Data: []byte("raced"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}
	pushStarted := make(chan struct{}, 1)
	releasePush := make(chan struct{})
	wrapped := &fakeStreamAdapterSTT{recognizeResult: &SpeechEvent{
		Type:         SpeechEventFinalTranscript,
		Alternatives: []SpeechData{{Text: "raced speech"}},
	}}
	stream, err := NewStreamAdapter(
		wrapped,
		&fakeStreamAdapterVAD{stream: &fakeStreamAdapterVADStream{
			events: []*vad.VADEvent{
				{Type: vad.VADEventEndOfSpeech},
			},
			waitPushesBeforeEvent: []int{1},
			pushCh:                make(chan struct{}, 1),
			pushStartedCh:         pushStarted,
			releasePushCh:         releasePush,
			done:                  make(chan struct{}),
		}},
	).Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	pushErrCh := make(chan error, 1)
	go func() {
		pushErrCh <- stream.PushFrame(frame)
	}()

	select {
	case <-pushStarted:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for VAD PushFrame")
	}

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
	if event.Type != SpeechEventFinalTranscript {
		t.Fatalf("second event type = %s, want final_transcript", event.Type)
	}
	if got := len(wrapped.recognizeFrames); got != 1 {
		t.Fatalf("Recognize frame count = %d, want raced frame buffered before VAD push returns", got)
	}
	if wrapped.recognizeFrames[0] != frame {
		t.Fatalf("Recognize frame = %#v, want original raced frame", wrapped.recognizeFrames[0])
	}

	close(releasePush)
	if err := <-pushErrCh; err != nil {
		t.Fatalf("PushFrame returned error: %v", err)
	}
}

func TestStreamAdapterDoesNotReadNextVADEventBeforeFinalTranscript(t *testing.T) {
	recognizeStarted := make(chan struct{}, 1)
	releaseRecognize := make(chan struct{})
	stream, err := NewStreamAdapter(
		&fakeStreamAdapterSTT{
			recognizeStarted: recognizeStarted,
			releaseRecognize: releaseRecognize,
			recognizeResult: &SpeechEvent{
				Type:         SpeechEventFinalTranscript,
				Alternatives: []SpeechData{{Text: "first turn"}},
			},
		},
		&fakeStreamAdapterVAD{stream: &fakeStreamAdapterVADStream{
			events: []*vad.VADEvent{
				{
					Type:   vad.VADEventEndOfSpeech,
					Frames: []*model.AudioFrame{{Data: []byte{1, 0}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}},
				},
				{Type: vad.VADEventStartOfSpeech},
			},
			done: make(chan struct{}),
		}},
	).Stream(context.Background(), "")
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

	select {
	case <-recognizeStarted:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for Recognize")
	}

	nextCh := make(chan *SpeechEvent, 1)
	errCh := make(chan error, 1)
	go func() {
		event, err := stream.Next()
		if err != nil {
			errCh <- err
			return
		}
		nextCh <- event
	}()

	select {
	case event := <-nextCh:
		t.Fatalf("Next returned %s before prior final transcript", event.Type)
	case err := <-errCh:
		t.Fatalf("Next returned error before prior final transcript: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	close(releaseRecognize)

	select {
	case event := <-nextCh:
		if event.Type != SpeechEventFinalTranscript {
			t.Fatalf("second event type = %s, want final_transcript", event.Type)
		}
		if len(event.Alternatives) != 1 || event.Alternatives[0].Text != "first turn" {
			t.Fatalf("final alternatives = %#v, want first turn", event.Alternatives)
		}
	case err := <-errCh:
		t.Fatalf("Next returned error: %v", err)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for final transcript")
	}
}

func streamAdapterSpanAttributes(attrs []attribute.KeyValue) map[string]string {
	values := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		values[string(attr.Key)] = attr.Value.AsString()
	}
	return values
}

type fakeStreamAdapterSTT struct {
	MetricsEmitter
	ErrorEmitter

	recognizeErr     error
	recognizeResult  *SpeechEvent
	recognizeStarted chan struct{}
	releaseRecognize chan struct{}
	recognizeFrames  []*model.AudioFrame
	recognizeCalls   int
	model            string
	provider         string
}

func (f *fakeStreamAdapterSTT) Label() string {
	return "fake-stt"
}

func (f *fakeStreamAdapterSTT) Model() string {
	return f.model
}

func (f *fakeStreamAdapterSTT) Provider() string {
	return f.provider
}

func (f *fakeStreamAdapterSTT) Capabilities() STTCapabilities {
	return STTCapabilities{OfflineRecognize: true}
}

func (f *fakeStreamAdapterSTT) Stream(context.Context, string) (RecognizeStream, error) {
	return nil, nil
}

func (f *fakeStreamAdapterSTT) Recognize(_ context.Context, frames []*model.AudioFrame, _ string) (*SpeechEvent, error) {
	f.recognizeCalls++
	f.recognizeFrames = append([]*model.AudioFrame(nil), frames...)
	if f.recognizeStarted != nil {
		f.recognizeStarted <- struct{}{}
	}
	if f.releaseRecognize != nil {
		<-f.releaseRecognize
	}
	if f.recognizeErr != nil {
		return nil, f.recognizeErr
	}
	if f.recognizeResult != nil {
		return f.recognizeResult, nil
	}
	return &SpeechEvent{Type: SpeechEventFinalTranscript}, nil
}

type fakeStreamAdapterVAD struct {
	stream    vad.VADStream
	err       error
	startedCh chan struct{}
}

func (f *fakeStreamAdapterVAD) Label() string {
	return "fake.VAD"
}

func (f *fakeStreamAdapterVAD) Model() string {
	return "fake"
}

func (f *fakeStreamAdapterVAD) Provider() string {
	return "fake"
}

func (f *fakeStreamAdapterVAD) Capabilities() vad.VADCapabilities {
	return vad.VADCapabilities{UpdateInterval: 1}
}

func (f *fakeStreamAdapterVAD) OnMetricsCollected(vad.VADMetricsHandler) func() {
	return func() {}
}

func (f *fakeStreamAdapterVAD) Stream(context.Context) (vad.VADStream, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.startedCh != nil {
		f.startedCh <- struct{}{}
	}
	return f.stream, nil
}

type fakeStreamAdapterVADStream struct {
	events                []*vad.VADEvent
	index                 int
	nextErr               error
	pushErr               error
	pushStartedCh         chan struct{}
	releasePushCh         chan struct{}
	flushCh               chan struct{}
	endInputCh            chan struct{}
	closedCh              chan struct{}
	done                  chan struct{}
	pushCh                chan struct{}
	waitPushesBeforeEvent []int
	disableEndInputFlush  bool
}

func (f *fakeStreamAdapterVADStream) PushFrame(*model.AudioFrame) error {
	if f.pushStartedCh != nil {
		f.pushStartedCh <- struct{}{}
	}
	if f.pushCh != nil {
		f.pushCh <- struct{}{}
	}
	if f.releasePushCh != nil {
		<-f.releasePushCh
	}
	if f.pushErr != nil {
		return f.pushErr
	}
	return nil
}

func (f *fakeStreamAdapterVADStream) Flush() error {
	if f.flushCh != nil {
		f.flushCh <- struct{}{}
	}
	return nil
}

func (f *fakeStreamAdapterVADStream) EndInput() error {
	if f.endInputCh != nil {
		f.endInputCh <- struct{}{}
	}
	if f.disableEndInputFlush {
		return nil
	}
	return f.Flush()
}

func (f *fakeStreamAdapterVADStream) Close() error {
	if f.closedCh != nil {
		f.closedCh <- struct{}{}
	}
	if f.done != nil {
		close(f.done)
	}
	return nil
}

func (f *fakeStreamAdapterVADStream) Next() (*vad.VADEvent, error) {
	if f.index < len(f.events) {
		if f.index < len(f.waitPushesBeforeEvent) {
			for range f.waitPushesBeforeEvent[f.index] {
				<-f.pushCh
			}
		}
		event := f.events[f.index]
		f.index++
		return event, nil
	}
	if f.done != nil {
		<-f.done
		return nil, context.Canceled
	}
	return nil, f.nextErr
}

func nextStreamAdapterEvent(stream RecognizeStream) (*SpeechEvent, error) {
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
		return event, nil
	case err := <-errCh:
		return nil, err
	case <-time.After(100 * time.Millisecond):
		return nil, context.DeadlineExceeded
	}
}

func nextStreamAdapterSTTError(stream RecognizeStream) error {
	errCh := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		errCh <- err
	}()

	select {
	case err := <-errCh:
		return err
	case <-time.After(100 * time.Millisecond):
		return context.DeadlineExceeded
	}
}
