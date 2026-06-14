package tts

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/library/telemetry"
)

const streamAdapterTestTimeout = 5 * time.Second

func TestStreamAdapterFlushSynthesizesBufferedText(t *testing.T) {
	provider := &fakeStreamAdapterTTS{}
	stream, err := NewStreamAdapter(provider).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello without punctuation"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	done := make(chan *SynthesizedAudio, 1)
	errCh := make(chan error, 1)
	go func() {
		audio, err := stream.Next()
		if err != nil {
			errCh <- err
			return
		}
		done <- audio
	}()

	select {
	case audio := <-done:
		if audio.Frame == nil {
			t.Fatal("audio frame is nil")
		}
	case err := <-errCh:
		t.Fatalf("Next returned error: %v", err)
	case <-time.After(streamAdapterTestTimeout):
		t.Fatal("Next timed out waiting for flushed text")
	}

	if got := provider.texts; len(got) != 1 || got[0] != "hello without punctuation" {
		t.Fatalf("synthesized texts = %#v, want flushed text", got)
	}
}

func TestStreamAdapterForwardsModelProvider(t *testing.T) {
	provider := &fakeStreamAdapterTTS{
		model:    "voice-model",
		provider: "voice-provider",
	}
	adapter := NewStreamAdapter(provider)

	if got := adapter.Model(); got != "voice-model" {
		t.Fatalf("Model = %q, want wrapped TTS model", got)
	}
	if got := adapter.Provider(); got != "voice-provider" {
		t.Fatalf("Provider = %q, want wrapped TTS provider", got)
	}
}

func TestStreamAdapterForwardsPrewarm(t *testing.T) {
	provider := &fakeStreamAdapterTTS{}
	adapter := NewStreamAdapter(provider)

	adapter.Prewarm()

	if provider.prewarmCalls != 1 {
		t.Fatalf("provider prewarm calls = %d, want 1", provider.prewarmCalls)
	}
}

func TestStreamAdapterCloseDoesNotCloseWrappedProvider(t *testing.T) {
	provider := &fakeStreamAdapterTTS{}
	adapter := NewStreamAdapter(provider)

	if err := adapter.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if provider.closed {
		t.Fatal("Close closed wrapped provider; reference StreamAdapter only detaches adapter-local listeners")
	}
	if err := adapter.Close(); err != nil {
		t.Fatalf("second Close error = %v", err)
	}
	if provider.closed {
		t.Fatal("second Close closed wrapped provider")
	}
}

func TestStreamAdapterForwardsMetricsCollected(t *testing.T) {
	provider := &fakeStreamAdapterTTS{}
	adapter := NewStreamAdapter(provider)
	metricsCh := make(chan string, 1)

	unsubscribe := adapter.OnMetricsCollected(func(metrics *telemetry.TTSMetrics) {
		metricsCh <- metrics.RequestID
	})
	defer unsubscribe()

	provider.EmitMetricsCollected(&telemetry.TTSMetrics{RequestID: "req-1"})

	select {
	case requestID := <-metricsCh:
		if requestID != "req-1" {
			t.Fatalf("metrics RequestID = %q, want req-1", requestID)
		}
	default:
		t.Fatal("metrics handler was not called")
	}
}

func TestStreamAdapterCloseUnsubscribesProviderMetrics(t *testing.T) {
	provider := &fakeStreamAdapterTTS{}
	adapter := NewStreamAdapter(provider)
	metricsCh := make(chan string, 3)

	unsubscribe := adapter.OnMetricsCollected(func(metrics *telemetry.TTSMetrics) {
		metricsCh <- metrics.RequestID
	})
	defer unsubscribe()

	provider.EmitMetricsCollected(&telemetry.TTSMetrics{RequestID: "before"})
	select {
	case requestID := <-metricsCh:
		if requestID != "before" {
			t.Fatalf("metrics RequestID before Close = %q, want before", requestID)
		}
	default:
		t.Fatal("provider metrics before Close were not forwarded")
	}

	if err := adapter.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	provider.EmitMetricsCollected(&telemetry.TTSMetrics{RequestID: "after"})
	adapter.EmitMetricsCollected(&telemetry.TTSMetrics{RequestID: "local"})

	var got []string
	for {
		select {
		case requestID := <-metricsCh:
			got = append(got, requestID)
		default:
			if !reflect.DeepEqual(got, []string{"local"}) {
				t.Fatalf("metrics request IDs = %#v, want provider forwarding before close plus local adapter event", got)
			}
			return
		}
	}
}

func TestStreamAdapterEmitsStreamedMetricsForFinalSegment(t *testing.T) {
	provider := &fakeStreamAdapterTTS{
		events: []*SynthesizedAudio{{
			Frame: &model.AudioFrame{
				Data:              []byte{1, 0, 2, 0},
				SampleRate:        24000,
				NumChannels:       1,
				SamplesPerChannel: 2,
			},
		}},
	}
	adapter := NewStreamAdapter(provider)
	metricsCh := make(chan *telemetry.TTSMetrics, 1)
	adapter.OnMetricsCollected(func(metrics *telemetry.TTSMetrics) {
		if metrics.Streamed {
			metricsCh <- metrics
		}
	})

	stream, err := adapter.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello world."); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := EndSynthesizeStreamInput(stream); err != nil {
		t.Fatalf("EndSynthesizeStreamInput returned error: %v", err)
	}

	audio := nextStreamAdapterAudio(t, stream)
	if !audio.IsFinal {
		t.Fatal("audio IsFinal = false, want final segment")
	}

	select {
	case got := <-metricsCh:
		if got.Label != adapter.Label() {
			t.Fatalf("metrics Label = %q, want %q", got.Label, adapter.Label())
		}
		if got.RequestID != audio.RequestID {
			t.Fatalf("metrics RequestID = %q, want %q", got.RequestID, audio.RequestID)
		}
		if got.SegmentID != audio.SegmentID {
			t.Fatalf("metrics SegmentID = %q, want %q", got.SegmentID, audio.SegmentID)
		}
		if got.CharactersCount != len("hello world.") {
			t.Fatalf("metrics CharactersCount = %d, want %d", got.CharactersCount, len("hello world."))
		}
		if got.AudioDuration <= 0 {
			t.Fatalf("metrics AudioDuration = %f, want > 0", got.AudioDuration)
		}
		if got.Metadata == nil || got.Metadata.ModelName != "unknown" || got.Metadata.ModelProvider != "unknown" {
			t.Fatalf("metrics Metadata = %#v, want unknown model/provider", got.Metadata)
		}
	default:
		t.Fatal("stream adapter did not emit streamed metrics")
	}
}

func TestStreamAdapterMetricsUnsubscribeRemovesLocalAndProviderHandlers(t *testing.T) {
	provider := &fakeStreamAdapterTTS{}
	adapter := NewStreamAdapter(provider)
	metricsCh := make(chan *telemetry.TTSMetrics, 2)

	unsubscribe := adapter.OnMetricsCollected(func(metrics *telemetry.TTSMetrics) {
		metricsCh <- metrics
	})
	unsubscribe()
	unsubscribe()

	provider.EmitMetricsCollected(&telemetry.TTSMetrics{RequestID: "provider"})
	adapter.EmitMetricsCollected(&telemetry.TTSMetrics{RequestID: "adapter"})

	select {
	case got := <-metricsCh:
		t.Fatalf("metrics handler called after unsubscribe: %#v", got)
	default:
	}
}

func TestStreamAdapterDoesNotForwardProviderErrorEvents(t *testing.T) {
	provider := &fakeStreamAdapterTTS{}
	adapter := NewStreamAdapter(provider)
	errCh := make(chan string, 2)

	unsubscribe := adapter.OnError(func(err TTSError) {
		errCh <- err.Label
	})
	defer unsubscribe()

	provider.EmitError(TTSError{Label: "provider", Err: errors.New("provider failed"), Recoverable: true})
	adapter.EmitError(TTSError{Label: "adapter", Err: errors.New("adapter failed"), Recoverable: true})

	select {
	case label := <-errCh:
		if label != "adapter" {
			t.Fatalf("error label = %q, want adapter-local error only", label)
		}
	default:
		t.Fatal("error handler was not called")
	}
	select {
	case label := <-errCh:
		t.Fatalf("unexpected forwarded provider error label %q", label)
	default:
	}
}

func TestStreamAdapterEmitsErrorOnStreamFailure(t *testing.T) {
	wantErr := errors.New("provider failed")
	provider := &fakeStreamAdapterTTS{streamErr: wantErr}
	adapter := NewStreamAdapter(provider)
	errCh := make(chan TTSError, 1)
	adapter.OnError(func(err TTSError) {
		errCh <- err
	})

	stream, err := adapter.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello."); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := EndSynthesizeStreamInput(stream); err != nil {
		t.Fatalf("EndSynthesizeStreamInput returned error: %v", err)
	}
	_, err = stream.Next()
	if !errors.Is(err, wantErr) {
		t.Fatalf("Next error = %v, want %v", err, wantErr)
	}

	select {
	case got := <-errCh:
		if got.Type != TTSErrorType {
			t.Fatalf("error Type = %q, want %q", got.Type, TTSErrorType)
		}
		if got.Label != adapter.Label() {
			t.Fatalf("error Label = %q, want %q", got.Label, adapter.Label())
		}
		if !errors.Is(got.Err, wantErr) {
			t.Fatalf("emitted error = %v, want %v", got.Err, wantErr)
		}
		if got.Recoverable {
			t.Fatal("Recoverable = true, want false")
		}
	default:
		t.Fatal("stream adapter did not emit TTS error")
	}
}

func TestStreamAdapterErrorUnsubscribeRemovesLocalHandler(t *testing.T) {
	provider := &fakeStreamAdapterTTS{}
	adapter := NewStreamAdapter(provider)
	errCh := make(chan TTSError, 2)

	unsubscribe := adapter.OnError(func(err TTSError) {
		errCh <- err
	})
	unsubscribe()
	unsubscribe()

	provider.EmitError(TTSError{Label: "provider", Err: errors.New("provider")})
	adapter.EmitError(TTSError{Label: "adapter", Err: errors.New("adapter")})

	select {
	case got := <-errCh:
		t.Fatalf("error handler called after unsubscribe: %#v", got)
	default:
	}
}

func TestStreamAdapterStreamReportsDoneAndExceptionAfterFailure(t *testing.T) {
	wantErr := errors.New("provider failed")
	provider := &fakeStreamAdapterTTS{
		streamErr: wantErr,
	}
	adapter := NewStreamAdapter(provider)

	stream, err := adapter.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	doneStream, ok := stream.(DoneStream)
	if !ok {
		t.Fatal("stream does not implement DoneStream")
	}
	exceptionStream, ok := stream.(ExceptionStream)
	if !ok {
		t.Fatal("stream does not implement ExceptionStream")
	}
	if doneStream.Done() {
		t.Fatal("Done() = true before input")
	}
	if err := exceptionStream.Exception(); err != nil {
		t.Fatalf("Exception() before failure = %v, want nil", err)
	}

	if err := stream.PushText("hello."); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := EndSynthesizeStreamInput(stream); err != nil {
		t.Fatalf("EndSynthesizeStreamInput() error = %v", err)
	}

	_, err = stream.Next()
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

func TestStreamAdapterStreamReportsDoneAfterEOF(t *testing.T) {
	provider := &fakeStreamAdapterTTS{}
	adapter := NewStreamAdapter(provider)

	stream, err := adapter.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	doneStream, ok := stream.(DoneStream)
	if !ok {
		t.Fatal("stream does not implement DoneStream")
	}
	exceptionStream, ok := stream.(ExceptionStream)
	if !ok {
		t.Fatal("stream does not implement ExceptionStream")
	}

	if err := EndSynthesizeStreamInput(stream); err != nil {
		t.Fatalf("EndSynthesizeStreamInput() error = %v", err)
	}
	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next() error = %v, want io.EOF", err)
	}
	if !doneStream.Done() {
		t.Fatal("Done() = false after EOF")
	}
	if err := exceptionStream.Exception(); err != nil {
		t.Fatalf("Exception() after EOF = %v, want nil", err)
	}
}

func TestStreamAdapterStreamReportsDoneAfterCloseWithActiveStream(t *testing.T) {
	chunked := newBlockingStreamAdapterChunkedStream()
	provider := &fakeStreamAdapterTTS{chunked: chunked}
	adapter := NewStreamAdapter(provider)

	stream, err := adapter.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	doneStream, ok := stream.(DoneStream)
	if !ok {
		t.Fatal("stream does not implement DoneStream")
	}
	exceptionStream, ok := stream.(ExceptionStream)
	if !ok {
		t.Fatal("stream does not implement ExceptionStream")
	}

	if err := stream.PushText("hello."); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if !chunked.waitForNext(t) {
		t.Fatal("active chunked stream was not entered")
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !doneStream.Done() {
		t.Fatal("Done() = false after Close")
	}
	if err := exceptionStream.Exception(); err != nil {
		t.Fatalf("Exception() after Close = %v, want nil", err)
	}
}

func TestStreamAdapterPreservesInternalNewlinesForSynthesis(t *testing.T) {
	provider := &fakeStreamAdapterTTS{}
	stream, err := NewStreamAdapter(provider).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("first line\nsecond line"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	_ = nextStreamAdapterAudio(t, stream)

	if got := provider.texts; len(got) != 1 || got[0] != "first line\nsecond line" {
		t.Fatalf("synthesized texts = %#v, want internal newline preserved", got)
	}
}

func TestStreamAdapterPropagatesTokenizerSegmentIDWithinSegment(t *testing.T) {
	provider := &fakeStreamAdapterTTS{
		events: []*SynthesizedAudio{
			{Frame: &model.AudioFrame{Data: []byte{1}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1}},
			{Frame: &model.AudioFrame{Data: []byte{2}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1}},
		},
	}
	stream, err := NewStreamAdapter(provider).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("first segment"); err != nil {
		t.Fatalf("PushText(first) returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush(first) returned error: %v", err)
	}

	first := nextStreamAdapterAudio(t, stream)
	second := nextStreamAdapterAudio(t, stream)
	if first.SegmentID == "" {
		t.Fatal("first SegmentID is empty")
	}
	firstSegmentID := first.SegmentID
	if second.SegmentID != firstSegmentID {
		t.Fatalf("second SegmentID = %q, want first segment id %q", second.SegmentID, firstSegmentID)
	}
}

func TestStreamAdapterForwardsPushAfterFlush(t *testing.T) {
	provider := &fakeStreamAdapterTTS{}
	stream, err := NewStreamAdapter(provider).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("first segment"); err != nil {
		t.Fatalf("PushText(first) returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush(first) returned error: %v", err)
	}
	_ = nextStreamAdapterAudio(t, stream)

	if err := stream.PushText("second segment"); err != nil {
		t.Fatalf("PushText(second) returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush(second) returned error: %v", err)
	}
	time.Sleep(25 * time.Millisecond)

	want := []string{"first segment", "second segment"}
	if got := provider.texts; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("synthesized texts = %#v, want %#v", got, want)
	}
}

func TestStreamAdapterMarksLastFrameInSegmentFinal(t *testing.T) {
	provider := &fakeStreamAdapterTTS{
		events: []*SynthesizedAudio{
			{Frame: &model.AudioFrame{Data: []byte{1}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1}},
			{Frame: &model.AudioFrame{Data: []byte{2}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1}},
		},
	}
	stream, err := NewStreamAdapter(provider).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("final segment"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	first := nextStreamAdapterAudio(t, stream)
	if first.IsFinal {
		t.Fatal("first audio IsFinal = true, want false")
	}
	second := nextStreamAdapterAudio(t, stream)
	if !second.IsFinal {
		t.Fatal("second audio IsFinal = false, want true")
	}
}

func TestStreamAdapterDoesNotMarkIntermediateSentenceFinal(t *testing.T) {
	provider := &fakeStreamAdapterTTS{
		events: []*SynthesizedAudio{{
			Frame: &model.AudioFrame{
				Data:              make([]byte, 24000*2/50),
				SampleRate:        24000,
				NumChannels:       1,
				SamplesPerChannel: 24000 / 50,
			},
		}},
	}
	stream, err := NewStreamAdapter(provider).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("First sentence has enough words. Second sentence"); err != nil {
		t.Fatalf("PushText(first) returned error: %v", err)
	}
	if err := stream.PushText(" has enough words."); err != nil {
		t.Fatalf("PushText(second) returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	first := nextStreamAdapterAudio(t, stream)
	segmentID := first.SegmentID
	if segmentID == "" {
		t.Fatal("first SegmentID is empty")
	}
	if first.IsFinal {
		t.Fatal("first sentence audio IsFinal = true, want non-final within the same segment")
	}
	finalSeen := false
	for i := 0; i < 5; i++ {
		audio := nextStreamAdapterAudio(t, stream)
		if audio.SegmentID != segmentID {
			t.Fatalf("SegmentID = %q, want %q", audio.SegmentID, segmentID)
		}
		if audio.IsFinal {
			finalSeen = true
			break
		}
	}
	if !finalSeen {
		t.Fatal("did not receive final audio for segment")
	}
	if got, want := provider.texts, []string{"First sentence has enough words.", "Second sentence has enough words."}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("synthesized texts = %#v, want %#v", got, want)
	}
}

func TestStreamAdapterEmitsLongFrameHeadBeforeProviderEOF(t *testing.T) {
	frame := &model.AudioFrame{
		Data:              make([]byte, 24000*2),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 24000,
	}
	chunked := newOneFrameThenBlockingChunkedStream(frame)
	provider := &fakeStreamAdapterTTS{chunked: chunked}
	stream, err := NewStreamAdapter(provider).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("long frame"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	audio := nextStreamAdapterAudio(t, stream)
	if audio.IsFinal {
		t.Fatal("first audio IsFinal = true, want non-final head before provider EOF")
	}
	if got, want := audio.Frame.SamplesPerChannel, uint32(23760); got != want {
		t.Fatalf("head SamplesPerChannel = %d, want %d", got, want)
	}
}

func TestStreamAdapterClearsProviderFinalBeforeLastFrame(t *testing.T) {
	provider := &fakeStreamAdapterTTS{
		events: []*SynthesizedAudio{
			{IsFinal: true, Frame: &model.AudioFrame{Data: []byte{1}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1}},
			{Frame: &model.AudioFrame{Data: []byte{2}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1}},
		},
	}
	stream, err := NewStreamAdapter(provider).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("final owned by adapter"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	first := nextStreamAdapterAudio(t, stream)
	if first.IsFinal {
		t.Fatal("first audio IsFinal = true, want adapter to clear provider final before last frame")
	}
	second := nextStreamAdapterAudio(t, stream)
	if !second.IsFinal {
		t.Fatal("second audio IsFinal = false, want adapter-owned final marker")
	}
}

func TestStreamAdapterStampsTranscriptText(t *testing.T) {
	provider := &fakeStreamAdapterTTS{
		events: []*SynthesizedAudio{
			{Frame: &model.AudioFrame{Data: []byte{1}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1}},
			{Frame: &model.AudioFrame{Data: []byte{2}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1}},
		},
	}
	stream, err := NewStreamAdapter(provider).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("transcript segment"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	first := nextStreamAdapterAudio(t, stream)
	if first.DeltaText != "transcript segment" {
		t.Fatalf("first DeltaText = %q, want tokenizer text", first.DeltaText)
	}
	second := nextStreamAdapterAudio(t, stream)
	if second.DeltaText != "" {
		t.Fatalf("second DeltaText = %q, want no repeated transcript", second.DeltaText)
	}
}

func TestStreamAdapterStampsTimedTranscriptAtAudioCursor(t *testing.T) {
	frame := &model.AudioFrame{
		Data:              make([]byte, 480),
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 240,
	}
	provider := &fakeStreamAdapterTTS{
		events: []*SynthesizedAudio{{Frame: frame}},
	}
	stream, err := NewStreamAdapter(provider).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("First sentence has enough words."); err != nil {
		t.Fatalf("first PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("first Flush returned error: %v", err)
	}

	first := nextStreamAdapterAudio(t, stream)
	if len(first.TimedTranscript) != 1 {
		t.Fatalf("first TimedTranscript = %#v, want one transcript entry", first.TimedTranscript)
	}
	if first.TimedTranscript[0].Text != "First sentence has enough words." || first.TimedTranscript[0].StartTime != 0 {
		t.Fatalf("first TimedTranscript = %#v, want first sentence at start 0", first.TimedTranscript)
	}

	if err := stream.PushText("Second sentence has enough words."); err != nil {
		t.Fatalf("second PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("second Flush returned error: %v", err)
	}

	second := nextStreamAdapterAudio(t, stream)
	if len(second.TimedTranscript) != 1 {
		t.Fatalf("second TimedTranscript = %#v, want one transcript entry", second.TimedTranscript)
	}
	if second.TimedTranscript[0].Text != "Second sentence has enough words." || second.TimedTranscript[0].StartTime != 0.01 {
		t.Fatalf("second TimedTranscript = %#v, want second sentence at start 0.01", second.TimedTranscript)
	}
}

func TestStreamAdapterSetsStableRequestID(t *testing.T) {
	provider := &fakeStreamAdapterTTS{
		events: []*SynthesizedAudio{
			{RequestID: "provider-a", Frame: &model.AudioFrame{Data: []byte{1}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1}},
			{RequestID: "provider-b", Frame: &model.AudioFrame{Data: []byte{2}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1}},
		},
	}
	stream, err := NewStreamAdapter(provider).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("request id segment"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	first := nextStreamAdapterAudio(t, stream)
	second := nextStreamAdapterAudio(t, stream)
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

func TestStreamAdapterDoesNotMutateProviderAudioMetadata(t *testing.T) {
	providerAudio := &SynthesizedAudio{
		RequestID: "provider-request",
		SegmentID: "provider-segment",
		IsFinal:   false,
		Frame:     &model.AudioFrame{Data: []byte{1}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1},
	}
	provider := &fakeStreamAdapterTTS{events: []*SynthesizedAudio{providerAudio}}
	stream, err := NewStreamAdapter(provider).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("wrapped segment"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	got := nextStreamAdapterAudio(t, stream)
	if got == providerAudio {
		t.Fatal("returned provider audio pointer, want wrapper-owned event")
	}
	if got.RequestID == providerAudio.RequestID || got.SegmentID == providerAudio.SegmentID || !got.IsFinal {
		t.Fatalf("wrapped metadata = request:%q segment:%q final:%t, want wrapper metadata", got.RequestID, got.SegmentID, got.IsFinal)
	}
	if providerAudio.RequestID != "provider-request" || providerAudio.SegmentID != "provider-segment" || providerAudio.IsFinal {
		t.Fatalf("provider audio mutated: %#v", providerAudio)
	}
}

func TestStreamAdapterPropagatesSynthesizeError(t *testing.T) {
	synthErr := errors.New("synthesize failed")
	stream, err := NewStreamAdapter(&fakeStreamAdapterTTS{synthesizeErr: synthErr}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	err = nextStreamAdapterError(stream)
	if !errors.Is(err, synthErr) {
		t.Fatalf("Next error = %v, want synthesize error", err)
	}
}

func TestStreamAdapterReportsNilChunkedStream(t *testing.T) {
	stream, err := NewStreamAdapter(&fakeStreamAdapterTTS{nilChunked: true}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	err = nextStreamAdapterError(stream)
	if err == nil {
		t.Fatal("Next error = nil, want nil stream error")
	}
	if !strings.Contains(err.Error(), "nil chunked stream") {
		t.Fatalf("Next error = %v, want nil chunked stream error", err)
	}
}

func TestStreamAdapterReportsTypedNilChunkedStream(t *testing.T) {
	var typedNil *fakeStreamAdapterChunkedStream
	stream, err := NewStreamAdapter(&fakeStreamAdapterTTS{chunked: typedNil}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	err = nextStreamAdapterError(stream)
	if err == nil {
		t.Fatal("Next error = nil, want typed nil stream error")
	}
	if !strings.Contains(err.Error(), "nil chunked stream") {
		t.Fatalf("Next error = %v, want nil chunked stream error", err)
	}
}

func TestStreamAdapterPropagatesChunkedStreamError(t *testing.T) {
	streamErr := errors.New("audio stream failed")
	stream, err := NewStreamAdapter(&fakeStreamAdapterTTS{streamErr: streamErr}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	err = nextStreamAdapterError(stream)
	if !errors.Is(err, streamErr) {
		t.Fatalf("Next error = %v, want chunked stream error", err)
	}
}

func TestStreamAdapterErrorsWhenNonEmptyTextProducesNoAudio(t *testing.T) {
	stream, err := NewStreamAdapter(&fakeStreamAdapterTTS{
		empty: true,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	err = nextStreamAdapterError(stream)
	if err == nil {
		t.Fatal("Next error = nil, want no-audio error")
	}
	if !strings.Contains(err.Error(), "no audio frames") {
		t.Fatalf("Next error = %v, want no-audio error", err)
	}
}

func TestStreamAdapterCloseIsIdempotent(t *testing.T) {
	stream, err := NewStreamAdapter(&fakeStreamAdapterTTS{}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("first Close returned error: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close returned error: %v", err)
	}
}

func TestStreamAdapterCloseClosesActiveChunkedStream(t *testing.T) {
	chunked := newBlockingStreamAdapterChunkedStream()
	stream, err := NewStreamAdapter(&fakeStreamAdapterTTS{
		chunked: chunked,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	if err := stream.PushText("blocked synthesis"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	if !chunked.waitForNext(t) {
		t.Fatal("chunked stream Next was not entered")
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if !chunked.waitForClose(t) {
		t.Fatal("chunked stream Close was not called")
	}
}

func TestStreamAdapterCloseWaitsForRunLoop(t *testing.T) {
	chunked := newReleasableStreamAdapterChunkedStream()
	stream, err := NewStreamAdapter(&fakeStreamAdapterTTS{
		chunked: chunked,
	}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	if err := stream.PushText("blocked synthesis"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	chunked.waitForNext(t)

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- stream.Close()
	}()

	chunked.waitForClose(t)
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before run loop exited: %v", err)
	default:
	}

	chunked.release()
	if err := receiveStreamAdapterCloseResult(t, closeDone); err != nil {
		t.Fatalf("Close returned error = %v", err)
	}
}

func TestStreamAdapterCloseDoesNotSynthesizeBufferedText(t *testing.T) {
	provider := &fakeStreamAdapterTTS{}
	stream, err := NewStreamAdapter(provider).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	if err := stream.PushText("buffered but not flushed"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	err = nextStreamAdapterError(stream)
	if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
		t.Fatalf("Next error = %v, want closed stream", err)
	}
	if got := provider.texts; len(got) != 0 {
		t.Fatalf("synthesized texts after Close = %#v, want none", got)
	}
}

func TestStreamAdapterEndsInputWhenSupported(t *testing.T) {
	provider := &fakeStreamAdapterTTS{}
	stream, err := NewStreamAdapter(provider).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	ending, ok := stream.(inputEndingSynthesizeStream)
	if !ok {
		t.Fatal("StreamAdapter stream does not implement EndInput")
	}
	if err := stream.PushText("hello without punctuation"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput returned error: %v", err)
	}

	audio := nextStreamAdapterAudio(t, stream)
	if audio.Frame == nil {
		t.Fatal("audio frame is nil")
	}
	err = nextStreamAdapterError(stream)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want io.EOF after EndInput drains", err)
	}
	if got := provider.texts; len(got) != 1 || got[0] != "hello without punctuation" {
		t.Fatalf("synthesized texts = %#v, want ended input text", got)
	}
}

func TestStreamAdapterRejectsInputAfterClose(t *testing.T) {
	stream, err := NewStreamAdapter(&fakeStreamAdapterTTS{}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	if err := stream.PushText("late"); err == nil {
		t.Fatal("PushText returned nil error after close")
	}
	if err := stream.Flush(); err == nil {
		t.Fatal("Flush returned nil error after close")
	}
}

func TestStreamAdapterNextReturnsEOFWhenClosed(t *testing.T) {
	stream, err := NewStreamAdapter(&fakeStreamAdapterTTS{}).Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	err = nextStreamAdapterError(stream)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want io.EOF", err)
	}
}

func nextStreamAdapterError(stream SynthesizeStream) error {
	errCh := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		errCh <- err
	}()

	select {
	case err := <-errCh:
		return err
	case <-time.After(streamAdapterTestTimeout):
		return context.DeadlineExceeded
	}
}

func nextStreamAdapterAudio(t *testing.T, stream SynthesizeStream) *SynthesizedAudio {
	t.Helper()
	done := make(chan *SynthesizedAudio, 1)
	errCh := make(chan error, 1)
	go func() {
		audio, err := stream.Next()
		if err != nil {
			errCh <- err
			return
		}
		done <- audio
	}()

	select {
	case audio := <-done:
		return audio
	case err := <-errCh:
		t.Fatalf("Next returned error: %v", err)
	case <-time.After(streamAdapterTestTimeout):
		t.Fatal("Next timed out")
	}
	return nil
}

type fakeStreamAdapterTTS struct {
	MetricsEmitter
	ErrorEmitter

	texts         []string
	model         string
	provider      string
	prewarmCalls  int
	synthesizeErr error
	streamErr     error
	events        []*SynthesizedAudio
	chunked       ChunkedStream
	nilChunked    bool
	empty         bool
	closed        bool
}

func (f *fakeStreamAdapterTTS) Label() string {
	return "fake-tts"
}

func (f *fakeStreamAdapterTTS) Model() string {
	return f.model
}

func (f *fakeStreamAdapterTTS) Provider() string {
	return f.provider
}

func (f *fakeStreamAdapterTTS) Prewarm() {
	f.prewarmCalls++
}

func (f *fakeStreamAdapterTTS) Close() error {
	f.closed = true
	return nil
}

func (f *fakeStreamAdapterTTS) Capabilities() TTSCapabilities {
	return TTSCapabilities{}
}

func (f *fakeStreamAdapterTTS) SampleRate() int {
	return 24000
}

func (f *fakeStreamAdapterTTS) NumChannels() int {
	return 1
}

func (f *fakeStreamAdapterTTS) Synthesize(_ context.Context, text string) (ChunkedStream, error) {
	f.texts = append(f.texts, text)
	if f.synthesizeErr != nil {
		return nil, f.synthesizeErr
	}
	if f.nilChunked {
		return nil, nil
	}
	if f.chunked != nil {
		return f.chunked, nil
	}
	events := f.events
	if len(events) == 0 && !f.empty {
		events = []*SynthesizedAudio{{
			Frame: &model.AudioFrame{Data: []byte{1}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1},
		}}
	}
	return &fakeStreamAdapterChunkedStream{
		events: events,
		err:    f.streamErr,
	}, nil
}

func (f *fakeStreamAdapterTTS) Stream(context.Context) (SynthesizeStream, error) {
	return nil, nil
}

type fakeStreamAdapterChunkedStream struct {
	events []*SynthesizedAudio
	index  int
	err    error
}

func (f *fakeStreamAdapterChunkedStream) Next() (*SynthesizedAudio, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.index >= len(f.events) {
		return nil, io.EOF
	}
	event := f.events[f.index]
	f.index++
	return event, nil
}

func (f *fakeStreamAdapterChunkedStream) Close() error {
	return nil
}

type blockingStreamAdapterChunkedStream struct {
	nextCh  chan struct{}
	closeCh chan struct{}
}

func newBlockingStreamAdapterChunkedStream() *blockingStreamAdapterChunkedStream {
	return &blockingStreamAdapterChunkedStream{
		nextCh:  make(chan struct{}),
		closeCh: make(chan struct{}),
	}
}

func (b *blockingStreamAdapterChunkedStream) Next() (*SynthesizedAudio, error) {
	select {
	case <-b.nextCh:
	default:
		close(b.nextCh)
	}
	<-b.closeCh
	return nil, io.EOF
}

func (b *blockingStreamAdapterChunkedStream) Close() error {
	select {
	case <-b.closeCh:
	default:
		close(b.closeCh)
	}
	return nil
}

func (b *blockingStreamAdapterChunkedStream) waitForNext(t *testing.T) bool {
	t.Helper()
	select {
	case <-b.nextCh:
		return true
	case <-time.After(streamAdapterTestTimeout):
		return false
	}
}

func (b *blockingStreamAdapterChunkedStream) waitForClose(t *testing.T) bool {
	t.Helper()
	select {
	case <-b.closeCh:
		return true
	case <-time.After(streamAdapterTestTimeout):
		return false
	}
}

type releasableStreamAdapterChunkedStream struct {
	nextCh    chan struct{}
	closeCh   chan struct{}
	releaseCh chan struct{}
}

func newReleasableStreamAdapterChunkedStream() *releasableStreamAdapterChunkedStream {
	return &releasableStreamAdapterChunkedStream{
		nextCh:    make(chan struct{}),
		closeCh:   make(chan struct{}),
		releaseCh: make(chan struct{}),
	}
}

func (s *releasableStreamAdapterChunkedStream) Next() (*SynthesizedAudio, error) {
	select {
	case <-s.nextCh:
	default:
		close(s.nextCh)
	}
	<-s.closeCh
	<-s.releaseCh
	return nil, io.EOF
}

func (s *releasableStreamAdapterChunkedStream) Close() error {
	select {
	case <-s.closeCh:
	default:
		close(s.closeCh)
	}
	return nil
}

func (s *releasableStreamAdapterChunkedStream) waitForNext(t *testing.T) {
	t.Helper()
	select {
	case <-s.nextCh:
	case <-time.After(streamAdapterTestTimeout):
		t.Fatal("chunked stream Next was not entered")
	}
}

func (s *releasableStreamAdapterChunkedStream) waitForClose(t *testing.T) {
	t.Helper()
	select {
	case <-s.closeCh:
	case <-time.After(streamAdapterTestTimeout):
		t.Fatal("chunked stream Close was not called")
	}
}

func (s *releasableStreamAdapterChunkedStream) release() {
	close(s.releaseCh)
}

func receiveStreamAdapterCloseResult(t *testing.T, results <-chan error) error {
	t.Helper()
	select {
	case err := <-results:
		return err
	case <-time.After(streamAdapterTestTimeout):
		t.Fatal("timed out waiting for Close to return")
		return nil
	}
}

type oneFrameThenBlockingChunkedStream struct {
	frame   *model.AudioFrame
	closeCh chan struct{}
	once    sync.Once
	index   int
}

func newOneFrameThenBlockingChunkedStream(frame *model.AudioFrame) *oneFrameThenBlockingChunkedStream {
	return &oneFrameThenBlockingChunkedStream{
		frame:   frame,
		closeCh: make(chan struct{}),
	}
}

func (s *oneFrameThenBlockingChunkedStream) Next() (*SynthesizedAudio, error) {
	if s.index == 0 {
		s.index++
		return &SynthesizedAudio{Frame: s.frame}, nil
	}
	<-s.closeCh
	return nil, io.EOF
}

func (s *oneFrameThenBlockingChunkedStream) Close() error {
	s.once.Do(func() {
		close(s.closeCh)
	})
	return nil
}
