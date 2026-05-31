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
	stream    vad.VADStream
	err       error
	startedCh chan struct{}
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
	events   []*vad.VADEvent
	index    int
	nextErr  error
	pushErr  error
	flushCh  chan struct{}
	closedCh chan struct{}
	done     chan struct{}
}

func (f *fakeStreamAdapterVADStream) PushFrame(*model.AudioFrame) error {
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
