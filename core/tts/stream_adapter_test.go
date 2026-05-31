package tts

import (
	"context"
	"errors"
	"io"
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

type fakeStreamAdapterTTS struct {
	texts         []string
	synthesizeErr error
	streamErr     error
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
	return &fakeStreamAdapterChunkedStream{
		events: []*SynthesizedAudio{{
			Frame: &model.AudioFrame{Data: []byte{1}, SampleRate: 24000, NumChannels: 1, SamplesPerChannel: 1},
		}},
		err: f.streamErr,
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
