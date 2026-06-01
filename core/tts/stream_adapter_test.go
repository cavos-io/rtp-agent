package tts

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/conversation-worker/model"
)

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
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Next timed out waiting for flushed text")
	}

	if got := provider.texts; len(got) != 1 || got[0] != "hello without punctuation" {
		t.Fatalf("synthesized texts = %#v, want flushed text", got)
	}
}

func TestStreamAdapterPropagatesTokenizerSegmentID(t *testing.T) {
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

	if err := stream.PushText("second segment"); err != nil {
		t.Fatalf("PushText(second) returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush(second) returned error: %v", err)
	}

	third := nextStreamAdapterAudio(t, stream)
	if third.SegmentID == "" {
		t.Fatal("third SegmentID is empty")
	}
	if third.SegmentID == firstSegmentID {
		t.Fatalf("third SegmentID = %q, want new segment id after flush", third.SegmentID)
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
	case <-time.After(100 * time.Millisecond):
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
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Next timed out")
	}
	return nil
}

type fakeStreamAdapterTTS struct {
	texts         []string
	synthesizeErr error
	streamErr     error
	events        []*SynthesizedAudio
	empty         bool
}

func (f *fakeStreamAdapterTTS) Label() string {
	return "fake-tts"
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
