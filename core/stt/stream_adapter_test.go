package stt

import (
	"context"
	"errors"
	"io"
	"testing"

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

type fakeStreamAdapterSTT struct{}

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
	nextErr error
}

func (f *fakeStreamAdapterVADStream) PushFrame(*model.AudioFrame) error {
	return nil
}

func (f *fakeStreamAdapterVADStream) Flush() error {
	return nil
}

func (f *fakeStreamAdapterVADStream) Close() error {
	return nil
}

func (f *fakeStreamAdapterVADStream) Next() (*vad.VADEvent, error) {
	return nil, f.nextErr
}
