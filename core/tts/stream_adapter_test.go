package tts

import (
	"context"
	"errors"
	"io"
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
	provider := &fakeStreamAdapterTTS{}
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
	if ending, ok := stream.(interface{ EndInput() error }); ok {
		if err := ending.EndInput(); err != nil {
			t.Fatalf("EndInput returned error: %v", err)
		}
	}

	first := nextStreamAdapterAudio(t, stream)
	second := nextStreamAdapterAudio(t, stream)
	if first.SegmentID == "" || second.SegmentID == "" || second.SegmentID != first.SegmentID {
		t.Fatalf("segment ids = first:%q second:%q, want same non-empty segment", first.SegmentID, second.SegmentID)
	}
	if first.IsFinal {
		t.Fatal("first sentence audio IsFinal = true, want non-final within the same segment")
	}
	if !second.IsFinal {
		t.Fatal("second sentence audio IsFinal = false, want final at segment end")
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

	texts         []string
	model         string
	provider      string
	prewarmCalls  int
	synthesizeErr error
	streamErr     error
	events        []*SynthesizedAudio
	chunked       ChunkedStream
	empty         bool
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
