package tts

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/cavos-io/conversation-worker/model"
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

func TestSynthesizeWithStreamIgnoresEmptyText(t *testing.T) {
	provider := &fakeStreamingTTS{
		stream: &fakeSynthesizeStream{},
	}

	chunked, err := SynthesizeWithStream(context.Background(), provider, "")
	if err != nil {
		t.Fatalf("SynthesizeWithStream() error = %v", err)
	}
	defer chunked.Close()

	wantCalls := []string{"flush"}
	if !reflect.DeepEqual(provider.stream.calls, wantCalls) {
		t.Fatalf("stream calls = %#v, want %#v", provider.stream.calls, wantCalls)
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
}

func TestSynthesizeWithStreamErrorsWhenWhitespaceTextProducesNoAudio(t *testing.T) {
	provider := &fakeStreamingTTS{
		stream: &fakeSynthesizeStream{emptyErr: io.EOF},
	}

	chunked, err := SynthesizeWithStream(context.Background(), provider, "   ")
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

type fakeStreamingTTS struct {
	stream *fakeSynthesizeStream
}

func (f *fakeStreamingTTS) Label() string                 { return "fake" }
func (f *fakeStreamingTTS) Capabilities() TTSCapabilities { return TTSCapabilities{Streaming: true} }
func (f *fakeStreamingTTS) SampleRate() int               { return 24000 }
func (f *fakeStreamingTTS) NumChannels() int              { return 1 }
func (f *fakeStreamingTTS) Synthesize(context.Context, string) (ChunkedStream, error) {
	return nil, nil
}
func (f *fakeStreamingTTS) Stream(context.Context) (SynthesizeStream, error) {
	return f.stream, nil
}

type fakeSynthesizeStream struct {
	calls    []string
	events   []*SynthesizedAudio
	closed   bool
	emptyErr error
}

func (f *fakeSynthesizeStream) PushText(text string) error {
	f.calls = append(f.calls, "push:"+text)
	return nil
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
