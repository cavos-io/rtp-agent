package tts

import (
	"context"
	"reflect"
	"testing"
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

func TestSynthesizeWithStreamReturnsStreamEvents(t *testing.T) {
	want := &SynthesizedAudio{RequestID: "req-a"}
	provider := &fakeStreamingTTS{
		stream: &fakeSynthesizeStream{
			events: []*SynthesizedAudio{want},
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
	if got != want {
		t.Fatalf("Next() = %#v, want %#v", got, want)
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
	calls  []string
	events []*SynthesizedAudio
	closed bool
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
		return nil, context.Canceled
	}
	ev := f.events[0]
	f.events = f.events[1:]
	return ev, nil
}
