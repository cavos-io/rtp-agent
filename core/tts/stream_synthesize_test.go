package tts

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestSynthesizeWithStreamPushesTextAndFlushes(t *testing.T) {
	provider := &fakeStreamingTTS{
		stream: &fakeSynthesizeStream{},
	}

	chunked, err := SynthesizeWithStream(context.Background(), provider, "hello world")
	if err != nil {
		t.Fatalf("SynthesizeWithStream() error = %v", err)
	}
	defer chunked.Close()

	wantCalls := []string{"push:hello world", "flush"}
	if !reflect.DeepEqual(provider.stream.calls, wantCalls) {
		t.Fatalf("stream calls = %#v, want %#v", provider.stream.calls, wantCalls)
	}
}

func TestSynthesizeWithStreamEndsInputWhenSupported(t *testing.T) {
	stream := &endInputSynthesizeStream{
		events: []*SynthesizedAudio{{Frame: &model.AudioFrame{Data: []byte{1}}}},
	}
	provider := &endInputStreamingTTS{stream: stream}

	chunked, err := SynthesizeWithStream(context.Background(), provider, "hello world")
	if err != nil {
		t.Fatalf("SynthesizeWithStream() error = %v", err)
	}
	defer chunked.Close()

	wantCalls := []string{"push:hello world", "end_input"}
	if !reflect.DeepEqual(stream.calls, wantCalls) {
		t.Fatalf("stream calls = %#v, want %#v", stream.calls, wantCalls)
	}
	if _, err := chunked.Next(); err != nil {
		t.Fatalf("Next() error = %v", err)
	}
}

func TestSynthesizeWithStreamPushesEmptyText(t *testing.T) {
	provider := &fakeStreamingTTS{
		stream: &fakeSynthesizeStream{},
	}

	chunked, err := SynthesizeWithStream(context.Background(), provider, "")
	if err != nil {
		t.Fatalf("SynthesizeWithStream() error = %v", err)
	}
	defer chunked.Close()
	if chunked == nil {
		t.Fatal("SynthesizeWithStream() returned nil stream")
	}

	wantCalls := []string{"push:", "flush"}
	if !reflect.DeepEqual(provider.stream.calls, wantCalls) {
		t.Fatalf("stream calls = %#v, want %#v", provider.stream.calls, wantCalls)
	}
}

func TestSynthesizeWithStreamPushesEmptyTextBeforeEndingInput(t *testing.T) {
	stream := &endInputSynthesizeStream{}
	provider := &endInputStreamingTTS{stream: stream}

	chunked, err := SynthesizeWithStream(context.Background(), provider, "")
	if err != nil {
		t.Fatalf("SynthesizeWithStream() error = %v", err)
	}
	defer chunked.Close()
	if chunked == nil {
		t.Fatal("SynthesizeWithStream() returned nil stream")
	}

	wantCalls := []string{"push:", "end_input"}
	if !reflect.DeepEqual(stream.calls, wantCalls) {
		t.Fatalf("stream calls = %#v, want %#v", stream.calls, wantCalls)
	}
}

func TestSynthesizeWithStreamRecordsTTSStreamSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	oldTracer := telemetry.Tracer
	telemetry.Tracer = provider.Tracer("test")
	t.Cleanup(func() {
		telemetry.Tracer = oldTracer
		_ = provider.Shutdown(context.Background())
	})

	ttsProvider := &fakeStreamingTTS{
		model:    "voice-model",
		provider: "voice-provider",
		stream:   &fakeSynthesizeStream{emptyErr: io.EOF},
	}

	chunked, err := SynthesizeWithStream(context.Background(), ttsProvider, "")
	if err != nil {
		t.Fatalf("SynthesizeWithStream() error = %v, want nil", err)
	}
	_, err = chunked.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next() error = %v, want EOF", err)
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(spans))
	}
	if spans[0].Name() != "tts_stream" {
		t.Fatalf("span name = %q, want tts_stream", spans[0].Name())
	}
	attrs := spanAttributes(spans[0].Attributes())
	if attrs[telemetry.AttrGenAIRequestModel] != "voice-model" {
		t.Fatalf("span model attr = %q, want voice-model", attrs[telemetry.AttrGenAIRequestModel])
	}
	if attrs[telemetry.AttrGenAIProviderName] != "voice-provider" {
		t.Fatalf("span provider attr = %q, want voice-provider", attrs[telemetry.AttrGenAIProviderName])
	}
}

func TestSynthesizeWithStreamEmitsErrorOnPushFailure(t *testing.T) {
	wantErr := errors.New("push failed")
	stream := &fakeSynthesizeStream{pushErr: wantErr}
	provider := &fakeStreamingTTS{stream: stream}
	errCh := make(chan TTSError, 1)
	provider.OnError(func(err TTSError) {
		errCh <- err
	})

	_, err := SynthesizeWithStream(context.Background(), provider, "hello")
	if !errors.Is(err, wantErr) {
		t.Fatalf("SynthesizeWithStream() error = %v, want %v", err, wantErr)
	}
	if !stream.closed {
		t.Fatal("stream closed = false, want closed after push failure")
	}

	select {
	case got := <-errCh:
		if !errors.Is(got.Err, wantErr) {
			t.Fatalf("emitted error = %v, want %v", got.Err, wantErr)
		}
		if got.Recoverable {
			t.Fatal("emitted error is recoverable, want false")
		}
	default:
		t.Fatal("provider did not emit push error")
	}
}

func TestSynthesizeWithStreamReturnsStreamEvents(t *testing.T) {
	want := &SynthesizedAudio{RequestID: "req-a", DeltaText: "hello"}
	provider := &fakeStreamingTTS{
		stream: &fakeSynthesizeStream{
			events:   []*SynthesizedAudio{want},
			emptyErr: io.EOF,
		},
	}

	chunked, err := SynthesizeWithStream(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("SynthesizeWithStream() error = %v", err)
	}
	defer chunked.Close()

	got, err := chunked.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if got == want {
		t.Fatal("Next() returned provider audio pointer, want wrapper-owned event")
	}
	if got.DeltaText != want.DeltaText {
		t.Fatalf("DeltaText = %q, want %q", got.DeltaText, want.DeltaText)
	}
	if got.RequestID == "" || got.RequestID == want.RequestID {
		t.Fatalf("RequestID = %q, want wrapper request id", got.RequestID)
	}
}

func TestSynthesizeWithStreamEmitsMetricsAfterEOF(t *testing.T) {
	provider := &fakeStreamingTTS{
		stream: &fakeSynthesizeStream{
			events: []*SynthesizedAudio{{Frame: &model.AudioFrame{
				Data:              []byte{1, 0, 2, 0},
				SampleRate:        24000,
				NumChannels:       1,
				SamplesPerChannel: 2,
			}}},
			emptyErr: io.EOF,
		},
	}
	metricsCh := make(chan *telemetry.TTSMetrics, 1)
	provider.OnMetricsCollected(func(metrics *telemetry.TTSMetrics) {
		metricsCh <- metrics
	})

	chunked, err := SynthesizeWithStream(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("SynthesizeWithStream() error = %v", err)
	}
	defer chunked.Close()

	audio, err := chunked.Next()
	if err != nil {
		t.Fatalf("first Next() error = %v", err)
	}
	if audio.RequestID == "" {
		t.Fatal("first audio RequestID is empty")
	}
	if _, err := chunked.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("second Next() error = %v, want io.EOF", err)
	}

	select {
	case got := <-metricsCh:
		if got.Label != "fake" {
			t.Fatalf("metrics Label = %q, want fake", got.Label)
		}
		if got.RequestID != audio.RequestID {
			t.Fatalf("metrics RequestID = %q, want %q", got.RequestID, audio.RequestID)
		}
		if got.CharactersCount != len("hello") {
			t.Fatalf("metrics CharactersCount = %d, want %d", got.CharactersCount, len("hello"))
		}
		if got.Streamed {
			t.Fatal("metrics Streamed = true, want false")
		}
		if got.AudioDuration <= 0 {
			t.Fatalf("metrics AudioDuration = %f, want > 0", got.AudioDuration)
		}
		if got.Metadata == nil || got.Metadata.ModelName != "unknown" || got.Metadata.ModelProvider != "unknown" {
			t.Fatalf("metrics Metadata = %#v, want unknown model/provider", got.Metadata)
		}
	default:
		t.Fatal("provider did not emit TTS metrics")
	}
}

func TestSynthesizeWithStreamEmitsMetricsWhenReturningFinalTail(t *testing.T) {
	provider := &fakeStreamingTTS{
		stream: &fakeSynthesizeStream{
			events: []*SynthesizedAudio{{Frame: &model.AudioFrame{
				Data:              make([]byte, 24000*2),
				SampleRate:        24000,
				NumChannels:       1,
				SamplesPerChannel: 24000,
			}}},
			emptyErr: io.EOF,
		},
	}
	metricsCh := make(chan *telemetry.TTSMetrics, 1)
	provider.OnMetricsCollected(func(metrics *telemetry.TTSMetrics) {
		metricsCh <- metrics
	})

	chunked, err := SynthesizeWithStream(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("SynthesizeWithStream() error = %v", err)
	}
	defer chunked.Close()

	head, err := chunked.Next()
	if err != nil {
		t.Fatalf("first Next() error = %v", err)
	}
	if head.IsFinal {
		t.Fatal("first audio IsFinal = true, want non-final head")
	}
	select {
	case got := <-metricsCh:
		t.Fatalf("metrics emitted before final tail: %#v", got)
	default:
	}

	tail, err := chunked.Next()
	if err != nil {
		t.Fatalf("second Next() error = %v", err)
	}
	if !tail.IsFinal {
		t.Fatal("second audio IsFinal = false, want final tail")
	}

	select {
	case got := <-metricsCh:
		if got.RequestID != tail.RequestID {
			t.Fatalf("metrics RequestID = %q, want %q", got.RequestID, tail.RequestID)
		}
		if got.AudioDuration <= 0 {
			t.Fatalf("metrics AudioDuration = %f, want > 0", got.AudioDuration)
		}
	default:
		t.Fatal("provider did not emit TTS metrics when final tail was returned")
	}
}

func TestSynthesizeWithStreamSetsStableRequestID(t *testing.T) {
	provider := &fakeStreamingTTS{
		stream: &fakeSynthesizeStream{
			events: []*SynthesizedAudio{
				{RequestID: "provider-a"},
				{RequestID: "provider-b"},
			},
			emptyErr: io.EOF,
		},
	}

	chunked, err := SynthesizeWithStream(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("SynthesizeWithStream() error = %v", err)
	}
	defer chunked.Close()

	first, err := chunked.Next()
	if err != nil {
		t.Fatalf("first Next() error = %v", err)
	}
	second, err := chunked.Next()
	if err != nil {
		t.Fatalf("second Next() error = %v", err)
	}
	if first.RequestID == "" {
		t.Fatal("first RequestID is empty")
	}
	if second.RequestID != first.RequestID {
		t.Fatalf("second RequestID = %q, want stable request id %q", second.RequestID, first.RequestID)
	}
	if first.RequestID == "provider-a" || second.RequestID == "provider-b" {
		t.Fatalf("RequestID forwarded provider ids: first=%q second=%q", first.RequestID, second.RequestID)
	}
}

func TestSynthesizeWithStreamClearsProviderSegmentID(t *testing.T) {
	provider := &fakeStreamingTTS{
		stream: &fakeSynthesizeStream{
			events: []*SynthesizedAudio{
				{SegmentID: "provider-a"},
				{SegmentID: "provider-b"},
			},
			emptyErr: io.EOF,
		},
	}

	chunked, err := SynthesizeWithStream(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("SynthesizeWithStream() error = %v", err)
	}
	defer chunked.Close()

	first, err := chunked.Next()
	if err != nil {
		t.Fatalf("first Next() error = %v", err)
	}
	second, err := chunked.Next()
	if err != nil {
		t.Fatalf("second Next() error = %v", err)
	}
	if first.SegmentID != "" || second.SegmentID != "" {
		t.Fatalf("SegmentID forwarded provider ids: first=%q second=%q", first.SegmentID, second.SegmentID)
	}
}

func TestSynthesizeWithStreamMarksLastFrameFinal(t *testing.T) {
	provider := &fakeStreamingTTS{
		stream: &fakeSynthesizeStream{
			events: []*SynthesizedAudio{
				{Frame: &model.AudioFrame{Data: []byte{1}}},
				{Frame: &model.AudioFrame{Data: []byte{2}}},
			},
			emptyErr: io.EOF,
		},
	}

	chunked, err := SynthesizeWithStream(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("SynthesizeWithStream() error = %v", err)
	}
	defer chunked.Close()

	first, err := chunked.Next()
	if err != nil {
		t.Fatalf("first Next() error = %v", err)
	}
	if first.IsFinal {
		t.Fatal("first audio IsFinal = true, want false")
	}
	second, err := chunked.Next()
	if err != nil {
		t.Fatalf("second Next() error = %v", err)
	}
	if !second.IsFinal {
		t.Fatal("second audio IsFinal = false, want true")
	}
}

func TestSynthesizeWithStreamEmitsLongFrameHeadBeforeProviderEOF(t *testing.T) {
	frame := &model.AudioFrame{
		Data:              make([]byte, 24000*2),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 24000,
	}
	stream := newOneFrameThenBlockingSynthesizeStream(frame)
	provider := &blockingStreamingTTS{stream: stream}

	chunked, err := SynthesizeWithStream(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("SynthesizeWithStream() error = %v", err)
	}
	defer chunked.Close()

	audio, err := nextChunkedAudioWithTimeout(chunked)
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if audio.IsFinal {
		t.Fatal("first audio IsFinal = true, want non-final head before provider EOF")
	}
	if got, want := audio.Frame.SamplesPerChannel, uint32(23760); got != want {
		t.Fatalf("head SamplesPerChannel = %d, want %d", got, want)
	}
}

func TestSynthesizeWithStreamClearsProviderFinalBeforeLastFrame(t *testing.T) {
	provider := &fakeStreamingTTS{
		stream: &fakeSynthesizeStream{
			events: []*SynthesizedAudio{
				{IsFinal: true, Frame: &model.AudioFrame{Data: []byte{1}}},
				{Frame: &model.AudioFrame{Data: []byte{2}}},
			},
			emptyErr: io.EOF,
		},
	}

	chunked, err := SynthesizeWithStream(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("SynthesizeWithStream() error = %v", err)
	}
	defer chunked.Close()

	first, err := chunked.Next()
	if err != nil {
		t.Fatalf("first Next() error = %v", err)
	}
	if first.IsFinal {
		t.Fatal("first audio IsFinal = true, want wrapper to clear provider final before last frame")
	}
	second, err := chunked.Next()
	if err != nil {
		t.Fatalf("second Next() error = %v", err)
	}
	if !second.IsFinal {
		t.Fatal("second audio IsFinal = false, want wrapper-owned final marker")
	}
}

func TestSynthesizeWithStreamDoesNotMutateProviderAudioMetadata(t *testing.T) {
	providerAudio := &SynthesizedAudio{RequestID: "provider-request"}
	provider := &fakeStreamingTTS{
		stream: &fakeSynthesizeStream{
			events:   []*SynthesizedAudio{providerAudio},
			emptyErr: io.EOF,
		},
	}

	chunked, err := SynthesizeWithStream(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("SynthesizeWithStream() error = %v", err)
	}
	defer chunked.Close()

	got, err := chunked.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if got == providerAudio {
		t.Fatal("returned provider audio pointer, want wrapper-owned event")
	}
	if got.RequestID == "" || got.RequestID == providerAudio.RequestID {
		t.Fatalf("RequestID = %q, want wrapper request id", got.RequestID)
	}
	if providerAudio.RequestID != "provider-request" {
		t.Fatalf("provider RequestID = %q, want unchanged", providerAudio.RequestID)
	}
}

func TestSynthesizeWithStreamErrorsWhenNonEmptyTextProducesNoAudio(t *testing.T) {
	provider := &fakeStreamingTTS{
		stream: &fakeSynthesizeStream{emptyErr: io.EOF},
	}
	errCh := make(chan TTSError, 1)
	provider.OnError(func(err TTSError) {
		errCh <- err
	})

	chunked, err := SynthesizeWithStream(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("SynthesizeWithStream() error = %v", err)
	}
	defer chunked.Close()

	_, err = chunked.Next()
	if err == nil {
		t.Fatal("Next() error = nil, want no-audio error")
	}
	if !strings.Contains(err.Error(), "no audio frames") {
		t.Fatalf("Next() error = %v, want no-audio error", err)
	}
	select {
	case got := <-errCh:
		if !strings.Contains(got.Err.Error(), "no audio frames") {
			t.Fatalf("emitted error = %v, want no-audio error", got.Err)
		}
		if got.Recoverable {
			t.Fatal("emitted error is recoverable, want false")
		}
	default:
		t.Fatal("provider did not emit no-audio error")
	}
}

func TestSynthesizeWithStreamReturnsEOFWhenWhitespaceTextProducesNoAudio(t *testing.T) {
	provider := &fakeStreamingTTS{
		stream: &fakeSynthesizeStream{emptyErr: io.EOF},
	}

	chunked, err := SynthesizeWithStream(context.Background(), provider, "   ")
	if err != nil {
		t.Fatalf("SynthesizeWithStream() error = %v", err)
	}
	defer chunked.Close()

	_, err = chunked.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next() error = %v, want io.EOF", err)
	}
}

func TestSynthesizeWithStreamCloseDelegatesToStream(t *testing.T) {
	stream := &fakeSynthesizeStream{}
	provider := &fakeStreamingTTS{stream: stream}

	chunked, err := SynthesizeWithStream(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("SynthesizeWithStream() error = %v", err)
	}

	if err := chunked.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !stream.closed {
		t.Fatal("underlying stream closed = false, want true")
	}
}

func TestSynthesizeWithStreamClosesUnderlyingStreamAfterEOF(t *testing.T) {
	stream := &fakeSynthesizeStream{
		events:   []*SynthesizedAudio{{DeltaText: "hello"}},
		emptyErr: io.EOF,
	}
	provider := &fakeStreamingTTS{stream: stream}

	chunked, err := SynthesizeWithStream(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("SynthesizeWithStream() error = %v", err)
	}

	if _, err := chunked.Next(); err != nil {
		t.Fatalf("first Next() error = %v", err)
	}
	_, err = chunked.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("second Next() error = %v, want io.EOF", err)
	}
	if !stream.closed {
		t.Fatal("underlying stream closed = false, want true after EOF")
	}
}

func TestSynthesizeWithStreamEmitsErrorOnStreamFailure(t *testing.T) {
	wantErr := errors.New("provider failed")
	provider := &fakeStreamingTTS{
		stream: &fakeSynthesizeStream{emptyErr: wantErr},
	}
	errCh := make(chan TTSError, 1)
	provider.OnError(func(err TTSError) {
		errCh <- err
	})

	chunked, err := SynthesizeWithStream(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("SynthesizeWithStream() error = %v", err)
	}
	defer chunked.Close()

	_, err = chunked.Next()
	if !errors.Is(err, wantErr) {
		t.Fatalf("Next() error = %v, want %v", err, wantErr)
	}

	select {
	case got := <-errCh:
		if got.Type != TTSErrorType {
			t.Fatalf("error type = %q, want %q", got.Type, TTSErrorType)
		}
		if got.Label != "fake" {
			t.Fatalf("error label = %q, want fake", got.Label)
		}
		if !errors.Is(got.Err, wantErr) {
			t.Fatalf("emitted error = %v, want %v", got.Err, wantErr)
		}
		if got.Recoverable {
			t.Fatal("emitted error is recoverable, want false")
		}
	default:
		t.Fatal("provider did not emit TTS error")
	}
}

func TestSynthesizeWithStreamReportsDoneAndExceptionAfterFailure(t *testing.T) {
	wantErr := errors.New("provider failed")
	provider := &fakeStreamingTTS{
		stream: &fakeSynthesizeStream{emptyErr: wantErr},
	}

	chunked, err := SynthesizeWithStream(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("SynthesizeWithStream() error = %v", err)
	}
	defer chunked.Close()

	doneStream, ok := chunked.(DoneStream)
	if !ok {
		t.Fatal("chunked stream does not implement DoneStream")
	}
	exceptionStream, ok := chunked.(ExceptionStream)
	if !ok {
		t.Fatal("chunked stream does not implement ExceptionStream")
	}
	if doneStream.Done() {
		t.Fatal("Done() = true before stream failure")
	}
	if err := exceptionStream.Exception(); err != nil {
		t.Fatalf("Exception() before failure = %v, want nil", err)
	}

	_, err = chunked.Next()
	if !errors.Is(err, wantErr) {
		t.Fatalf("Next() error = %v, want %v", err, wantErr)
	}
	if !doneStream.Done() {
		t.Fatal("Done() = false after stream failure")
	}
	if err := exceptionStream.Exception(); !errors.Is(err, wantErr) {
		t.Fatalf("Exception() after failure = %v, want %v", err, wantErr)
	}
}

func TestSynthesizeWithStreamReportsDoneAfterFinalTail(t *testing.T) {
	provider := &fakeStreamingTTS{
		stream: &fakeSynthesizeStream{
			events: []*SynthesizedAudio{{Frame: &model.AudioFrame{
				Data:              make([]byte, 24000*2),
				SampleRate:        24000,
				NumChannels:       1,
				SamplesPerChannel: 24000,
			}}},
			emptyErr: io.EOF,
		},
	}

	chunked, err := SynthesizeWithStream(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("SynthesizeWithStream() error = %v", err)
	}
	defer chunked.Close()

	doneStream, ok := chunked.(DoneStream)
	if !ok {
		t.Fatal("chunked stream does not implement DoneStream")
	}
	exceptionStream, ok := chunked.(ExceptionStream)
	if !ok {
		t.Fatal("chunked stream does not implement ExceptionStream")
	}

	if _, err := chunked.Next(); err != nil {
		t.Fatalf("first Next() error = %v", err)
	}
	if doneStream.Done() {
		t.Fatal("Done() = true before final tail")
	}
	tail, err := chunked.Next()
	if err != nil {
		t.Fatalf("second Next() error = %v", err)
	}
	if !tail.IsFinal {
		t.Fatal("second audio IsFinal = false, want final tail")
	}
	if !doneStream.Done() {
		t.Fatal("Done() = false after final tail")
	}
	if err := exceptionStream.Exception(); err != nil {
		t.Fatalf("Exception() after final tail = %v, want nil", err)
	}
}

func spanAttributes(attrs []attribute.KeyValue) map[string]string {
	values := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		values[string(attr.Key)] = attr.Value.AsString()
	}
	return values
}

type fakeStreamingTTS struct {
	ErrorEmitter
	MetricsEmitter
	stream   *fakeSynthesizeStream
	model    string
	provider string
}

func (f *fakeStreamingTTS) Label() string                 { return "fake" }
func (f *fakeStreamingTTS) Capabilities() TTSCapabilities { return TTSCapabilities{Streaming: true} }
func (f *fakeStreamingTTS) SampleRate() int               { return 24000 }
func (f *fakeStreamingTTS) NumChannels() int              { return 1 }
func (f *fakeStreamingTTS) Model() string                 { return f.model }
func (f *fakeStreamingTTS) Provider() string              { return f.provider }
func (f *fakeStreamingTTS) Synthesize(context.Context, string) (ChunkedStream, error) {
	return nil, nil
}
func (f *fakeStreamingTTS) Stream(context.Context) (SynthesizeStream, error) {
	return f.stream, nil
}

type blockingStreamingTTS struct {
	stream SynthesizeStream
}

func (f *blockingStreamingTTS) Label() string { return "blocking" }
func (f *blockingStreamingTTS) Capabilities() TTSCapabilities {
	return TTSCapabilities{Streaming: true}
}
func (f *blockingStreamingTTS) SampleRate() int  { return 24000 }
func (f *blockingStreamingTTS) NumChannels() int { return 1 }
func (f *blockingStreamingTTS) Synthesize(context.Context, string) (ChunkedStream, error) {
	return nil, nil
}
func (f *blockingStreamingTTS) Stream(context.Context) (SynthesizeStream, error) {
	return f.stream, nil
}

type endInputStreamingTTS struct {
	stream *endInputSynthesizeStream
}

func (f *endInputStreamingTTS) Label() string { return "end-input" }
func (f *endInputStreamingTTS) Capabilities() TTSCapabilities {
	return TTSCapabilities{Streaming: true}
}
func (f *endInputStreamingTTS) SampleRate() int  { return 24000 }
func (f *endInputStreamingTTS) NumChannels() int { return 1 }
func (f *endInputStreamingTTS) Synthesize(context.Context, string) (ChunkedStream, error) {
	return nil, nil
}
func (f *endInputStreamingTTS) Stream(context.Context) (SynthesizeStream, error) {
	return f.stream, nil
}

type fakeSynthesizeStream struct {
	calls    []string
	events   []*SynthesizedAudio
	closed   bool
	pushErr  error
	emptyErr error
}

func (f *fakeSynthesizeStream) PushText(text string) error {
	f.calls = append(f.calls, "push:"+text)
	return f.pushErr
}

func (f *fakeSynthesizeStream) Flush() error {
	f.calls = append(f.calls, "flush")
	return nil
}

func (f *fakeSynthesizeStream) Close() error {
	f.closed = true
	return nil
}

func (f *fakeSynthesizeStream) Next() (*SynthesizedAudio, error) {
	if len(f.events) == 0 {
		if f.emptyErr != nil {
			return nil, f.emptyErr
		}
		return nil, context.Canceled
	}
	ev := f.events[0]
	f.events = f.events[1:]
	return ev, nil
}

type oneFrameThenBlockingSynthesizeStream struct {
	frame   *model.AudioFrame
	closeCh chan struct{}
	closed  bool
	index   int
}

func newOneFrameThenBlockingSynthesizeStream(frame *model.AudioFrame) *oneFrameThenBlockingSynthesizeStream {
	return &oneFrameThenBlockingSynthesizeStream{
		frame:   frame,
		closeCh: make(chan struct{}),
	}
}

func (s *oneFrameThenBlockingSynthesizeStream) PushText(string) error {
	return nil
}

func (s *oneFrameThenBlockingSynthesizeStream) Flush() error {
	return nil
}

func (s *oneFrameThenBlockingSynthesizeStream) Close() error {
	if !s.closed {
		s.closed = true
		close(s.closeCh)
	}
	return nil
}

func (s *oneFrameThenBlockingSynthesizeStream) Next() (*SynthesizedAudio, error) {
	if s.index == 0 {
		s.index++
		return &SynthesizedAudio{Frame: s.frame}, nil
	}
	<-s.closeCh
	return nil, io.EOF
}

func nextChunkedAudioWithTimeout(stream ChunkedStream) (*SynthesizedAudio, error) {
	audioCh := make(chan *SynthesizedAudio, 1)
	errCh := make(chan error, 1)
	go func() {
		audio, err := stream.Next()
		if err != nil {
			errCh <- err
			return
		}
		audioCh <- audio
	}()

	select {
	case audio := <-audioCh:
		return audio, nil
	case err := <-errCh:
		return nil, err
	case <-time.After(100 * time.Millisecond):
		return nil, context.DeadlineExceeded
	}
}

type endInputSynthesizeStream struct {
	calls  []string
	events []*SynthesizedAudio
	ended  bool
	closed bool
}

func (s *endInputSynthesizeStream) PushText(text string) error {
	s.calls = append(s.calls, "push:"+text)
	return nil
}

func (s *endInputSynthesizeStream) Flush() error {
	s.calls = append(s.calls, "flush")
	return nil
}

func (s *endInputSynthesizeStream) EndInput() error {
	s.calls = append(s.calls, "end_input")
	s.ended = true
	return nil
}

func (s *endInputSynthesizeStream) Close() error {
	s.closed = true
	return nil
}

func (s *endInputSynthesizeStream) Next() (*SynthesizedAudio, error) {
	if !s.ended {
		return nil, errors.New("input not ended")
	}
	if len(s.events) == 0 {
		return nil, io.EOF
	}
	ev := s.events[0]
	s.events = s.events[1:]
	return ev, nil
}
