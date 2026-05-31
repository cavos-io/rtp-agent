package stt

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/cavos-io/conversation-worker/core/vad"
	"github.com/cavos-io/conversation-worker/model"
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

type fakeStreamAdapterSTT struct {
	recognizeErr error
}

func (f *fakeStreamAdapterSTT) Label() string {
	return "fake-stt"
}

func (f *fakeStreamAdapterSTT) Capabilities() STTCapabilities {
	return STTCapabilities{OfflineRecognize: true}
}

func (f *fakeStreamAdapterSTT) Stream(context.Context, string) (RecognizeStream, error) {
	return nil, nil
}

func (f *fakeStreamAdapterSTT) Recognize(context.Context, []*model.AudioFrame, string) (*SpeechEvent, error) {
	if f.recognizeErr != nil {
		return nil, f.recognizeErr
	}
	return &SpeechEvent{Type: SpeechEventFinalTranscript}, nil
}

type fakeStreamAdapterVAD struct {
	stream vad.VADStream
	err    error
}

func (f *fakeStreamAdapterVAD) Stream(context.Context) (vad.VADStream, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.stream, nil
}

type fakeStreamAdapterVADStream struct {
	events  []*vad.VADEvent
	index   int
	nextErr error
	done    chan struct{}
}

func (f *fakeStreamAdapterVADStream) PushFrame(*model.AudioFrame) error {
	return nil
}

func (f *fakeStreamAdapterVADStream) Flush() error {
	return nil
}

func (f *fakeStreamAdapterVADStream) Close() error {
	if f.done != nil {
		close(f.done)
	}
	return nil
}

func (f *fakeStreamAdapterVADStream) Next() (*vad.VADEvent, error) {
	if f.index < len(f.events) {
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
