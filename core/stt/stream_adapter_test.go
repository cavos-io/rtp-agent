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

func TestStreamAdapterCapabilitiesMatchReference(t *testing.T) {
	caps := NewStreamAdapter(&fakeStreamAdapterSTT{}, &fakeStreamAdapterVAD{}).Capabilities()

	if !caps.Streaming {
		t.Fatal("Streaming = false, want true")
	}
	if caps.InterimResults {
		t.Fatal("InterimResults = true, want false")
	}
	if caps.Diarization {
		t.Fatal("Diarization = true, want false")
	}
	if !caps.OfflineRecognize {
		t.Fatal("OfflineRecognize = false, want true because Recognize delegates to wrapped STT")
	}
}

func TestStreamAdapterExposesTimingAnchors(t *testing.T) {
	stream, err := NewStreamAdapter(&fakeStreamAdapterSTT{}, &fakeStreamAdapterVAD{
		stream: &fakeStreamAdapterVADStream{done: make(chan struct{})},
	}).Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	timing, ok := stream.(StreamTiming)
	if !ok {
		t.Fatal("stream does not implement StreamTiming")
	}
	timing.SetStartTimeOffset(1.5)
	timing.SetStartTime(24.0)

	if timing.StartTimeOffset() != 1.5 {
		t.Fatalf("StartTimeOffset = %v, want 1.5", timing.StartTimeOffset())
	}
	if timing.StartTime() != 24.0 {
		t.Fatalf("StartTime = %v, want 24.0", timing.StartTime())
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

func TestStreamAdapterEndInputFlushesAndRejectsMoreInput(t *testing.T) {
	flushCh := make(chan struct{}, 1)
	endInputCh := make(chan struct{}, 1)
	stream, err := NewStreamAdapter(&fakeStreamAdapterSTT{}, &fakeStreamAdapterVAD{
		stream: &fakeStreamAdapterVADStream{flushCh: flushCh, endInputCh: endInputCh, done: make(chan struct{})},
	}).Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	ending, ok := stream.(InputEnding)
	if !ok {
		t.Fatal("stream does not implement InputEnding")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput returned error: %v", err)
	}

	select {
	case <-flushCh:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for EndInput flush")
	}
	select {
	case <-endInputCh:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for VAD EndInput")
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("late"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err == nil {
		t.Fatal("PushFrame after EndInput returned nil, want error")
	}
	if err := stream.Flush(); err == nil {
		t.Fatal("Flush after EndInput returned nil, want error")
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

func TestStreamAdapterRejectsMismatchedSampleRates(t *testing.T) {
	stream, err := NewStreamAdapter(&fakeStreamAdapterSTT{}, &fakeStreamAdapterVAD{
		stream: &fakeStreamAdapterVADStream{done: make(chan struct{})},
	}).Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("first"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err != nil {
		t.Fatalf("PushFrame(first) returned error: %v", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("second"), SampleRate: 8000, NumChannels: 1, SamplesPerChannel: 1}); err == nil {
		t.Fatal("PushFrame(second) returned nil, want sample-rate mismatch error")
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

func TestStreamAdapterFinalTranscriptUsesFirstRecognizedAlternative(t *testing.T) {
	stream, err := NewStreamAdapter(
		&fakeStreamAdapterSTT{recognizeResult: &SpeechEvent{
			Type: SpeechEventInterimTranscript,
			Alternatives: []SpeechData{
				{Text: "first"},
				{Text: "second"},
			},
		}},
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

	event, err = stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if event.Type != SpeechEventFinalTranscript {
		t.Fatalf("second event type = %s, want final_transcript", event.Type)
	}
	if len(event.Alternatives) != 1 {
		t.Fatalf("final alternatives = %d, want 1", len(event.Alternatives))
	}
	if event.Alternatives[0].Text != "first" {
		t.Fatalf("final text = %q, want first", event.Alternatives[0].Text)
	}
}

type fakeStreamAdapterSTT struct {
	recognizeErr    error
	recognizeResult *SpeechEvent
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
	if f.recognizeResult != nil {
		return f.recognizeResult, nil
	}
	return &SpeechEvent{Type: SpeechEventFinalTranscript}, nil
}

type fakeStreamAdapterVAD struct {
	stream    vad.VADStream
	err       error
	startedCh chan struct{}
}

func (f *fakeStreamAdapterVAD) Label() string {
	return "fake.VAD"
}

func (f *fakeStreamAdapterVAD) Model() string {
	return "fake"
}

func (f *fakeStreamAdapterVAD) Provider() string {
	return "fake"
}

func (f *fakeStreamAdapterVAD) Capabilities() vad.VADCapabilities {
	return vad.VADCapabilities{UpdateInterval: 1}
}

func (f *fakeStreamAdapterVAD) OnMetricsCollected(vad.VADMetricsHandler) {}

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
	events     []*vad.VADEvent
	index      int
	nextErr    error
	pushErr    error
	flushCh    chan struct{}
	endInputCh chan struct{}
	closedCh   chan struct{}
	done       chan struct{}
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

func (f *fakeStreamAdapterVADStream) EndInput() error {
	if f.endInputCh != nil {
		f.endInputCh <- struct{}{}
	}
	return f.Flush()
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
