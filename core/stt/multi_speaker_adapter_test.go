package stt

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

func TestPrimarySpeakerDetectorFormatsPrimaryAndBackgroundText(t *testing.T) {
	detector := newPrimarySpeakerDetector(
		true,
		false,
		"primary {speaker_id}: {text}",
		"background {speaker_id}: {text}",
		DefaultPrimarySpeakerDetectionOptions(),
	)
	detector.primarySpeaker = "speaker-a"

	primary := detector.onSttEvent(&SpeechEvent{
		Type: SpeechEventInterimTranscript,
		Alternatives: []SpeechData{{
			Text:      "hello",
			SpeakerID: "speaker-a",
		}},
	})
	if got := primary.Alternatives[0].Text; got != "primary speaker-a: hello" {
		t.Fatalf("primary text = %q, want formatted primary text", got)
	}
	if primary.Alternatives[0].IsPrimarySpeaker == nil || !*primary.Alternatives[0].IsPrimarySpeaker {
		t.Fatal("primary IsPrimarySpeaker was not set to true")
	}

	background := detector.onSttEvent(&SpeechEvent{
		Type: SpeechEventInterimTranscript,
		Alternatives: []SpeechData{{
			Text:      "aside",
			SpeakerID: "speaker-b",
		}},
	})
	if got := background.Alternatives[0].Text; got != "background speaker-b: aside" {
		t.Fatalf("background text = %q, want formatted background text", got)
	}
	if background.Alternatives[0].IsPrimarySpeaker == nil || *background.Alternatives[0].IsPrimarySpeaker {
		t.Fatal("background IsPrimarySpeaker was not set to false")
	}
}

func TestPrimarySpeakerDetectorSuppressesFormattedBackgroundText(t *testing.T) {
	detector := newPrimarySpeakerDetector(
		true,
		true,
		"{text}",
		"background {speaker_id}: {text}",
		DefaultPrimarySpeakerDetectionOptions(),
	)
	detector.primarySpeaker = "speaker-a"

	event := detector.onSttEvent(&SpeechEvent{
		Type: SpeechEventFinalTranscript,
		Alternatives: []SpeechData{{
			Text:      "aside",
			SpeakerID: "speaker-b",
		}},
	})
	if event != nil {
		t.Fatalf("background event = %#v, want suppressed nil event", event)
	}
}

func TestPrimarySpeakerDetectorTreatsSilentRMSAsValidData(t *testing.T) {
	detector := newPrimarySpeakerDetector(
		true,
		false,
		"{text}",
		"{text}",
		DefaultPrimarySpeakerDetectionOptions(),
	)
	detector.rmsBuffer = []float64{0, 0, 0}
	detector.pushedDuration = 0.3

	event := detector.onSttEvent(&SpeechEvent{
		Type: SpeechEventFinalTranscript,
		Alternatives: []SpeechData{{
			Text:      "quiet",
			SpeakerID: "speaker-a",
			StartTime: 0,
			EndTime:   0.3,
		}},
	})
	if event == nil {
		t.Fatal("event = nil, want final transcript")
	}
	if detector.primarySpeaker != "speaker-a" {
		t.Fatalf("primarySpeaker = %q, want speaker-a", detector.primarySpeaker)
	}
	if event.Alternatives[0].IsPrimarySpeaker == nil || !*event.Alternatives[0].IsPrimarySpeaker {
		t.Fatalf("IsPrimarySpeaker = %#v, want true", event.Alternatives[0].IsPrimarySpeaker)
	}
}

func TestPrimarySpeakerDetectorPushAudioInitializesByteStream(t *testing.T) {
	detector := newPrimarySpeakerDetector(
		true,
		false,
		"{text}",
		"{text}",
		DefaultPrimarySpeakerDetectionOptions(),
	)
	frame := &model.AudioFrame{
		Data:              make([]byte, 1600*2),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1600,
	}
	done := make(chan struct{})

	go func() {
		detector.pushAudio(frame)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("pushAudio did not return, want initialized byte stream to emit frame")
	}
	if detector.bstream == nil {
		t.Fatal("bstream = nil, want initialized byte stream")
	}
	if detector.bstream.SamplesPerChannel == 0 {
		t.Fatal("bstream SamplesPerChannel = 0, want frame-sized stream")
	}
	if detector.pushedDuration == 0 {
		t.Fatal("pushedDuration = 0, want emitted audio duration")
	}
}

func TestMultiSpeakerAdapterWrapperReturnsEOFWhenInnerCompletes(t *testing.T) {
	wrapper := &multiSpeakerAdapterWrapper{
		inner:   &fakeMultiSpeakerStream{nextErr: io.EOF},
		ctx:     context.Background(),
		eventCh: make(chan *SpeechEvent, 1),
		errCh:   make(chan error, 1),
		inputCh: make(chan multiSpeakerInput, 1),
	}
	go wrapper.run()

	_, err := wrapper.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want io.EOF", err)
	}
}

func TestMultiSpeakerAdapterWrapperKeepsReturningEOFAfterInnerCompletes(t *testing.T) {
	wrapper := &multiSpeakerAdapterWrapper{
		inner:   &fakeMultiSpeakerStream{nextErr: io.EOF},
		ctx:     context.Background(),
		eventCh: make(chan *SpeechEvent, 1),
		errCh:   make(chan error, 1),
		inputCh: make(chan multiSpeakerInput, 1),
	}
	go wrapper.run()

	_, err := wrapper.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("first Next error = %v, want io.EOF", err)
	}
	err = nextMultiSpeakerAdapterError(wrapper)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("second Next error = %v, want io.EOF", err)
	}
}

func TestMultiSpeakerAdapterWrapperRejectsInputAfterInnerCompletes(t *testing.T) {
	wrapper := &multiSpeakerAdapterWrapper{
		inner:   &fakeMultiSpeakerStream{nextErr: io.EOF},
		ctx:     context.Background(),
		eventCh: make(chan *SpeechEvent, 1),
		errCh:   make(chan error, 1),
		inputCh: make(chan multiSpeakerInput, 1),
	}
	go wrapper.run()

	_, err := wrapper.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want io.EOF", err)
	}

	err = wrapper.PushFrame(&model.AudioFrame{Data: []byte("late"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1})
	if err == nil {
		t.Fatal("PushFrame after inner completion returned nil, want error")
	}
}

func TestMultiSpeakerAdapterWrapperPropagatesInnerError(t *testing.T) {
	innerErr := errors.New("inner stream failed")
	wrapper := &multiSpeakerAdapterWrapper{
		inner:   &fakeMultiSpeakerStream{nextErr: innerErr},
		ctx:     context.Background(),
		eventCh: make(chan *SpeechEvent, 1),
		errCh:   make(chan error, 1),
		inputCh: make(chan multiSpeakerInput, 1),
	}
	go wrapper.run()

	_, err := wrapper.Next()
	if !errors.Is(err, innerErr) {
		t.Fatalf("Next error = %v, want inner error", err)
	}
}

func TestMultiSpeakerAdapterWrapperPreservesFrameFlushOrder(t *testing.T) {
	inner := &fakeMultiSpeakerStream{nextErr: io.EOF, waitCalls: 2, callCh: make(chan struct{}, 2)}
	wrapper := &multiSpeakerAdapterWrapper{
		inner:    inner,
		ctx:      context.Background(),
		detector: newPrimarySpeakerDetector(false, false, "{text}", "{text}", DefaultPrimarySpeakerDetectionOptions()),
		eventCh:  make(chan *SpeechEvent, 1),
		errCh:    make(chan error, 1),
		inputCh:  make(chan multiSpeakerInput, 2),
	}

	if err := wrapper.PushFrame(&model.AudioFrame{Data: []byte("a")}); err != nil {
		t.Fatalf("PushFrame returned error: %v", err)
	}
	if err := wrapper.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	go wrapper.run()

	_, _ = wrapper.Next()

	want := []string{"push:a", "flush"}
	if !reflect.DeepEqual(inner.calls, want) {
		t.Fatalf("inner calls = %#v, want %#v", inner.calls, want)
	}
}

func TestMultiSpeakerAdapterWrapperPropagatesForwardInputError(t *testing.T) {
	pushErr := errors.New("inner push failed")
	inner := &fakeMultiSpeakerStream{
		pushErr:   pushErr,
		waitCalls: 2,
		callCh:    make(chan struct{}, 2),
	}
	wrapper := &multiSpeakerAdapterWrapper{
		inner:    inner,
		ctx:      context.Background(),
		detector: newPrimarySpeakerDetector(false, false, "{text}", "{text}", DefaultPrimarySpeakerDetectionOptions()),
		eventCh:  make(chan *SpeechEvent, 1),
		errCh:    make(chan error, 1),
		inputCh:  make(chan multiSpeakerInput, 1),
	}
	go wrapper.run()

	if err := wrapper.PushFrame(&model.AudioFrame{Data: []byte("a"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err != nil {
		t.Fatalf("PushFrame returned error: %v", err)
	}

	_, err := wrapper.Next()
	if !errors.Is(err, pushErr) {
		t.Fatalf("Next error = %v, want push error", err)
	}
}

func TestMultiSpeakerAdapterWrapperRejectsMismatchedSampleRates(t *testing.T) {
	wrapper := &multiSpeakerAdapterWrapper{
		inner:    &fakeMultiSpeakerStream{nextErr: io.EOF},
		ctx:      context.Background(),
		detector: newPrimarySpeakerDetector(false, false, "{text}", "{text}", DefaultPrimarySpeakerDetectionOptions()),
		eventCh:  make(chan *SpeechEvent, 1),
		errCh:    make(chan error, 1),
		inputCh:  make(chan multiSpeakerInput, 2),
	}

	if err := wrapper.PushFrame(&model.AudioFrame{Data: []byte("first"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err != nil {
		t.Fatalf("PushFrame(first) returned error: %v", err)
	}
	if err := wrapper.PushFrame(&model.AudioFrame{Data: []byte("second"), SampleRate: 8000, NumChannels: 1, SamplesPerChannel: 1}); err == nil {
		t.Fatal("PushFrame(second) returned nil, want sample-rate mismatch error")
	}
}

func TestMultiSpeakerAdapterWrapperPropagatesTimingAnchors(t *testing.T) {
	inner := &fakeMultiSpeakerStream{nextErr: io.EOF}
	wrapper := &multiSpeakerAdapterWrapper{
		inner:    inner,
		ctx:      context.Background(),
		detector: newPrimarySpeakerDetector(false, false, "{text}", "{text}", DefaultPrimarySpeakerDetectionOptions()),
		eventCh:  make(chan *SpeechEvent, 1),
		errCh:    make(chan error, 1),
		inputCh:  make(chan multiSpeakerInput, 1),
	}

	timing, ok := any(wrapper).(StreamTiming)
	if !ok {
		t.Fatal("wrapper does not implement StreamTiming")
	}
	timing.SetStartTimeOffset(4.5)
	timing.SetStartTime(88.0)

	if timing.StartTimeOffset() != 4.5 {
		t.Fatalf("StartTimeOffset = %v, want 4.5", timing.StartTimeOffset())
	}
	if timing.StartTime() != 88.0 {
		t.Fatalf("StartTime = %v, want 88.0", timing.StartTime())
	}
	if inner.startTimeOffset != 4.5 {
		t.Fatalf("inner StartTimeOffset = %v, want 4.5", inner.startTimeOffset)
	}
	if inner.startTime != 88.0 {
		t.Fatalf("inner StartTime = %v, want 88.0", inner.startTime)
	}

	timing.SetStartTimeOffset(-1)
	timing.SetStartTime(-2)
	if timing.StartTimeOffset() < 0 {
		t.Fatalf("negative StartTimeOffset was stored: %v", timing.StartTimeOffset())
	}
	if timing.StartTime() < 0 {
		t.Fatalf("negative StartTime was stored: %v", timing.StartTime())
	}
	if inner.startTimeOffset < 0 {
		t.Fatalf("negative inner StartTimeOffset was propagated: %v", inner.startTimeOffset)
	}
	if inner.startTime < 0 {
		t.Fatalf("negative inner StartTime was propagated: %v", inner.startTime)
	}
}

func TestMultiSpeakerAdapterStreamSeedsStartTime(t *testing.T) {
	inner := &fakeMultiSpeakerStream{nextErr: io.EOF}
	adapter, err := NewMultiSpeakerAdapter(&metadataSTT{
		label:        "diarized",
		capabilities: STTCapabilities{Streaming: true, Diarization: true},
		stream:       inner,
	}, true, false, "{text}", "{text}", nil)
	if err != nil {
		t.Fatalf("NewMultiSpeakerAdapter returned error: %v", err)
	}

	before := time.Now()
	stream, err := adapter.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	after := time.Now()
	defer stream.Close()

	timing, ok := stream.(StreamTiming)
	if !ok {
		t.Fatal("stream does not implement StreamTiming")
	}
	assertStreamStartTimeSeeded(t, timing, before, after)
}

func TestMultiSpeakerAdapterWrapperEndInputFlushesAndRejectsMoreInput(t *testing.T) {
	inner := &fakeMultiSpeakerStream{nextErr: io.EOF}
	wrapper := &multiSpeakerAdapterWrapper{
		inner:    inner,
		ctx:      context.Background(),
		detector: newPrimarySpeakerDetector(false, false, "{text}", "{text}", DefaultPrimarySpeakerDetectionOptions()),
		eventCh:  make(chan *SpeechEvent, 1),
		errCh:    make(chan error, 1),
		inputCh:  make(chan multiSpeakerInput, 2),
	}

	if err := wrapper.PushFrame(&model.AudioFrame{Data: []byte("first"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err != nil {
		t.Fatalf("PushFrame returned error: %v", err)
	}
	ending, ok := any(wrapper).(InputEnding)
	if !ok {
		t.Fatal("wrapper does not implement InputEnding")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput returned error: %v", err)
	}
	if err := wrapper.PushFrame(&model.AudioFrame{Data: []byte("late"), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err == nil {
		t.Fatal("PushFrame after EndInput returned nil, want error")
	}
	if err := wrapper.Flush(); err == nil {
		t.Fatal("Flush after EndInput returned nil, want error")
	}
}

func TestMultiSpeakerAdapterWrapperForwardsEndInput(t *testing.T) {
	inner := &fakeMultiSpeakerStream{nextErr: io.EOF, callCh: make(chan struct{}, 1)}
	wrapper := &multiSpeakerAdapterWrapper{
		inner:    inner,
		ctx:      context.Background(),
		detector: newPrimarySpeakerDetector(false, false, "{text}", "{text}", DefaultPrimarySpeakerDetectionOptions()),
		eventCh:  make(chan *SpeechEvent, 1),
		errCh:    make(chan error, 1),
		inputCh:  make(chan multiSpeakerInput, 1),
	}
	go wrapper.run()

	if err := wrapper.EndInput(); err != nil {
		t.Fatalf("EndInput returned error: %v", err)
	}

	select {
	case <-inner.callCh:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for inner EndInput")
	}
	want := []string{"flush", "end_input"}
	if len(inner.calls) != len(want) {
		t.Fatalf("inner call count = %d, want %d: %#v", len(inner.calls), len(want), inner.calls)
	}
	if !reflect.DeepEqual(inner.calls, want) {
		t.Fatalf("inner calls = %#v, want %#v", inner.calls, want)
	}
}

type fakeMultiSpeakerStream struct {
	nextErr         error
	pushErr         error
	flushErr        error
	endInputErr     error
	calls           []string
	waitCalls       int
	callCh          chan struct{}
	startTimeOffset float64
	startTime       float64
}

func (f *fakeMultiSpeakerStream) PushFrame(frame *model.AudioFrame) error {
	f.calls = append(f.calls, "push:"+string(frame.Data))
	if f.callCh != nil {
		f.callCh <- struct{}{}
	}
	if f.pushErr != nil {
		return f.pushErr
	}
	return nil
}

func (f *fakeMultiSpeakerStream) Flush() error {
	f.calls = append(f.calls, "flush")
	if f.callCh != nil {
		f.callCh <- struct{}{}
	}
	if f.flushErr != nil {
		return f.flushErr
	}
	return nil
}

func (f *fakeMultiSpeakerStream) EndInput() error {
	f.calls = append(f.calls, "end_input")
	if f.callCh != nil {
		f.callCh <- struct{}{}
	}
	if f.endInputErr != nil {
		return f.endInputErr
	}
	return nil
}

func (f *fakeMultiSpeakerStream) Close() error {
	return nil
}

func (f *fakeMultiSpeakerStream) Next() (*SpeechEvent, error) {
	for range f.waitCalls {
		<-f.callCh
	}
	return nil, f.nextErr
}

func (f *fakeMultiSpeakerStream) StartTimeOffset() float64 {
	return f.startTimeOffset
}

func (f *fakeMultiSpeakerStream) SetStartTimeOffset(offset float64) {
	f.startTimeOffset = offset
}

func (f *fakeMultiSpeakerStream) StartTime() float64 {
	return f.startTime
}

func (f *fakeMultiSpeakerStream) SetStartTime(startTime float64) {
	f.startTime = startTime
}

func nextMultiSpeakerAdapterError(stream RecognizeStream) error {
	errCh := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		errCh <- err
	}()
	select {
	case err := <-errCh:
		return err
	case <-time.After(100 * time.Millisecond):
		return errors.New("timed out waiting for multi-speaker stream Next")
	}
}
