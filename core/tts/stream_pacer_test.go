package tts

import (
	"context"
	"errors"
	"io"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

func TestSentenceStreamPacerBatchesQueuedSentencesByMaxTextLength(t *testing.T) {
	underlying := newFakePacerStream()
	pacer := NewSentenceStreamPacerWithOptions(context.Background(), underlying, SentenceStreamPacerOptions{
		MinRemainingAudio: 20 * time.Second,
		MaxTextLength:     45,
	})
	defer pacer.Close()

	if err := pacer.PushText("First complete sentence. Second complete sentence. Third complete sentence."); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := pacer.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	want := []string{
		"First complete sentence.",
		"Second complete sentence. Third complete sentence.",
	}
	if !underlying.waitForPushes(t, want) {
		t.Fatalf("pushed text = %#v, want %#v", underlying.pushes(), want)
	}
}

func TestSentenceStreamPacerHoldsIncompleteSentenceUntilFlush(t *testing.T) {
	underlying := newFakePacerStream()
	pacer := NewSentenceStreamPacerWithOptions(context.Background(), underlying, SentenceStreamPacerOptions{
		MinRemainingAudio: 20 * time.Second,
		MaxTextLength:     80,
	})
	defer pacer.Close()

	if err := pacer.PushText("This sentence is still arriving without punctuation"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := underlying.pushes(); len(got) != 0 {
		t.Fatalf("pushed text before flush = %#v, want none for incomplete sentence", got)
	}

	if err := pacer.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if !underlying.waitForPushes(t, []string{"This sentence is still arriving without punctuation"}) {
		t.Fatalf("pushed text = %#v, want incomplete sentence released by flush", underlying.pushes())
	}
}

func TestSentenceStreamPacerRetainsTailAfterNormalizedNewlineSentence(t *testing.T) {
	underlying := newFakePacerStream()
	pacer := NewSentenceStreamPacerWithOptions(context.Background(), underlying, SentenceStreamPacerOptions{
		MinRemainingAudio: 20 * time.Second,
		MaxTextLength:     80,
	})
	defer pacer.Close()

	if err := pacer.PushText("First line\ncontinues complete sentence. Second sentence still arriving"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if !underlying.waitForPushes(t, []string{"First line continues complete sentence."}) {
		t.Fatalf("pushed text = %#v, want normalized first sentence only", underlying.pushes())
	}

	if err := pacer.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	want := []string{"First line continues complete sentence.", "Second sentence still arriving"}
	if !underlying.waitForPushes(t, want) {
		t.Fatalf("pushed text = %#v, want tail once after flush", underlying.pushes())
	}
}

func TestSentenceStreamPacerOptionsPreserveExplicitZeroValues(t *testing.T) {
	underlying := newFakePacerStream()
	pacer := NewSentenceStreamPacerWithOptions(context.Background(), underlying, SentenceStreamPacerOptions{
		MinRemainingAudio:    0,
		MinRemainingAudioSet: true,
		MaxTextLength:        0,
		MaxTextLengthSet:     true,
	})
	defer pacer.Close()

	if pacer.minRemainingAudio != 0 {
		t.Fatalf("minRemainingAudio = %v, want explicit zero", pacer.minRemainingAudio)
	}
	if pacer.maxTextLength != 0 {
		t.Fatalf("maxTextLength = %d, want explicit zero", pacer.maxTextLength)
	}
}

func TestSentenceStreamPacerWaitsForGenerationProgressBeforeDrainingFlush(t *testing.T) {
	underlying := newFakePacerStream()
	underlying.blockAudio = true
	pacer := NewSentenceStreamPacerWithOptions(context.Background(), underlying, SentenceStreamPacerOptions{
		MinRemainingAudio: 20 * time.Second,
		MaxTextLength:     80,
	})
	defer pacer.Close()

	if err := pacer.PushText("First complete sentence. Second complete sentence. Third complete sentence."); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := pacer.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	if !underlying.waitForPushes(t, []string{"First complete sentence."}) {
		t.Fatalf("pushed text = %#v, want only first sentence before generation progresses", underlying.pushes())
	}
	time.Sleep(120 * time.Millisecond)
	if got := underlying.pushes(); !reflect.DeepEqual(got, []string{"First complete sentence."}) {
		t.Fatalf("pushed text = %#v, want no additional text before generation progresses", got)
	}
}

func TestSentenceStreamPacerAllowsPushAfterFlush(t *testing.T) {
	underlying := newFakePacerStream()
	pacer := NewSentenceStreamPacerWithOptions(context.Background(), underlying, SentenceStreamPacerOptions{
		MinRemainingAudio: 20 * time.Second,
		MaxTextLength:     80,
	})
	defer pacer.Close()

	if err := pacer.PushText("First segment."); err != nil {
		t.Fatalf("first PushText() error = %v", err)
	}
	if err := pacer.Flush(); err != nil {
		t.Fatalf("first Flush() error = %v", err)
	}
	if !underlying.waitForPushes(t, []string{"First segment."}) {
		t.Fatalf("pushed text = %#v, want first segment", underlying.pushes())
	}

	if err := pacer.PushText("Second segment."); err != nil {
		t.Fatalf("second PushText() error = %v", err)
	}
	if err := pacer.Flush(); err != nil {
		t.Fatalf("second Flush() error = %v", err)
	}
	if !underlying.waitForPushes(t, []string{"First segment.", "Second segment."}) {
		t.Fatalf("pushed text = %#v, want both segments", underlying.pushes())
	}
}

func TestSentenceStreamPacerEndsInputWhenSupported(t *testing.T) {
	underlying := newEndInputPacerStream()
	pacer := NewSentenceStreamPacerWithOptions(context.Background(), underlying, SentenceStreamPacerOptions{})
	defer pacer.Close()

	ending, ok := any(pacer).(inputEndingSynthesizeStream)
	if !ok {
		t.Fatal("SentenceStreamPacer does not implement EndInput")
	}

	if err := pacer.PushText("Only segment."); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}

	if _, err := pacer.Next(); err != nil {
		t.Fatalf("first Next() error = %v", err)
	}
	if _, err := pacer.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("second Next() error = %v, want io.EOF", err)
	}

	wantCalls := []string{"push:Only segment.", "end_input"}
	if got := underlying.calls(); !reflect.DeepEqual(got, wantCalls) {
		t.Fatalf("underlying calls = %#v, want %#v", got, wantCalls)
	}
}

func TestSentenceStreamPacerIgnoresInputAfterCloseLikeReference(t *testing.T) {
	underlying := newEndInputPacerStream()
	pacer := NewSentenceStreamPacerWithOptions(context.Background(), underlying, SentenceStreamPacerOptions{})

	if err := pacer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	callsAfterClose := underlying.calls()
	if err := pacer.PushText("late"); err != nil {
		t.Fatalf("PushText after close error = %v", err)
	}
	if err := pacer.Flush(); err != nil {
		t.Fatalf("Flush after close error = %v", err)
	}
	ending, ok := any(pacer).(inputEndingSynthesizeStream)
	if !ok {
		t.Fatal("SentenceStreamPacer does not implement EndInput")
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput after close error = %v", err)
	}
	if got := underlying.calls(); !reflect.DeepEqual(got, callsAfterClose) {
		t.Fatalf("underlying calls after late input = %#v, want unchanged %#v", got, callsAfterClose)
	}
}

func TestSentenceStreamPacerFlushOnlyDoesNotFlushUnderlying(t *testing.T) {
	underlying := newEndInputPacerStream()
	pacer := NewSentenceStreamPacerWithOptions(context.Background(), underlying, SentenceStreamPacerOptions{})
	defer pacer.Close()

	if err := pacer.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	time.Sleep(25 * time.Millisecond)
	if got := underlying.calls(); len(got) != 0 {
		t.Fatalf("underlying calls after empty flush = %#v, want none", got)
	}
}

func TestSentenceStreamPacerReturnsEOFWhenUnderlyingCompletes(t *testing.T) {
	underlying := newEOFAfterOnePacerStream()
	pacer := NewSentenceStreamPacerWithOptions(context.Background(), underlying, SentenceStreamPacerOptions{})
	defer pacer.Close()

	if err := pacer.PushText("Only segment."); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := pacer.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	if _, err := pacer.Next(); err != nil {
		t.Fatalf("first Next() error = %v", err)
	}
	if _, err := pacer.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("second Next() error = %v, want io.EOF", err)
	}
}

func TestSentenceStreamPacerPropagatesUnderlyingError(t *testing.T) {
	streamErr := errors.New("provider stream failed")
	underlying := newEOFAfterOnePacerStream()
	underlying.err = streamErr
	pacer := NewSentenceStreamPacerWithOptions(context.Background(), underlying, SentenceStreamPacerOptions{})
	defer pacer.Close()

	if err := pacer.PushText("Only segment."); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := pacer.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	if _, err := pacer.Next(); err != nil {
		t.Fatalf("first Next() error = %v", err)
	}
	if _, err := pacer.Next(); !errors.Is(err, streamErr) {
		t.Fatalf("second Next() error = %v, want %v", err, streamErr)
	}
}

func TestSentenceStreamPacerPropagatesUnderlyingPushTextError(t *testing.T) {
	pushErr := errors.New("provider write failed")
	underlying := &pushErrorPacerStream{err: pushErr, pushed: make(chan struct{})}
	pacer := NewSentenceStreamPacerWithOptions(context.Background(), underlying, SentenceStreamPacerOptions{})
	defer pacer.Close()

	if err := pacer.PushText("Only segment."); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := pacer.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	if _, err := pacer.Next(); !errors.Is(err, pushErr) {
		t.Fatalf("Next() error = %v, want push error %v", err, pushErr)
	}
}

func TestSentenceStreamPacerSkipsNilSynthesizedAudioEvents(t *testing.T) {
	underlying := newNilThenAudioPacerStream()
	pacer := NewSentenceStreamPacerWithOptions(context.Background(), underlying, SentenceStreamPacerOptions{})
	defer pacer.Close()

	if err := pacer.PushText("Only segment."); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := pacer.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	audio, err := pacer.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("Next() audio = %#v, want nil event skipped and real audio returned", audio)
	}
}

func TestSentenceStreamPacerCloseWaitsForAudioLoop(t *testing.T) {
	underlying := newBlockingClosePacerStream()
	pacer := NewSentenceStreamPacerWithOptions(context.Background(), underlying, SentenceStreamPacerOptions{})

	if !underlying.waitForNext(t) {
		t.Fatal("underlying Next() was not called")
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- pacer.Close()
	}()

	if !underlying.waitForClose(t) {
		t.Fatal("underlying Close() was not called")
	}

	select {
	case err := <-closeDone:
		t.Fatalf("Close() returned before audio loop exited, err = %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	underlying.releaseNext()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Close() did not return after audio loop exited")
	}
}

type fakePacerStream struct {
	mu         sync.Mutex
	cond       *sync.Cond
	closed     bool
	blockAudio bool
	nextIndex  int
	texts      []string
}

func newFakePacerStream() *fakePacerStream {
	f := &fakePacerStream{}
	f.cond = sync.NewCond(&f.mu)
	return f
}

func (f *fakePacerStream) PushText(text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.texts = append(f.texts, text)
	f.cond.Broadcast()
	return nil
}

func (f *fakePacerStream) Flush() error {
	return nil
}

func (f *fakePacerStream) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	f.cond.Broadcast()
	return nil
}

func (f *fakePacerStream) Next() (*SynthesizedAudio, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for !f.closed && f.nextIndex >= len(f.texts) {
		f.cond.Wait()
	}
	if f.closed {
		return nil, context.Canceled
	}
	for !f.closed && f.blockAudio {
		f.cond.Wait()
	}
	if f.closed {
		return nil, context.Canceled
	}
	f.nextIndex++
	return &SynthesizedAudio{
		Frame: &model.AudioFrame{
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: 24000,
		},
	}, nil
}

func (f *fakePacerStream) pushes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.texts...)
}

func (f *fakePacerStream) waitForPushes(t *testing.T, want []string) bool {
	t.Helper()

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if reflect.DeepEqual(f.pushes(), want) {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return reflect.DeepEqual(f.pushes(), want)
}

type endInputPacerStream struct {
	mu      sync.Mutex
	cond    *sync.Cond
	closed  bool
	ended   bool
	emitted bool
	events  []string
}

func newEndInputPacerStream() *endInputPacerStream {
	s := &endInputPacerStream{}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *endInputPacerStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, "push:"+text)
	s.cond.Broadcast()
	return nil
}

func (s *endInputPacerStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, "flush")
	return nil
}

func (s *endInputPacerStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, "end_input")
	s.ended = true
	s.cond.Broadcast()
	return nil
}

func (s *endInputPacerStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.cond.Broadcast()
	return nil
}

func (s *endInputPacerStream) Next() (*SynthesizedAudio, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for !s.closed && !s.ended {
		s.cond.Wait()
	}
	if s.closed {
		return nil, context.Canceled
	}
	if s.emitted {
		return nil, io.EOF
	}
	s.emitted = true
	return &SynthesizedAudio{
		Frame: &model.AudioFrame{
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: 24000,
		},
	}, nil
}

func (s *endInputPacerStream) calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.events...)
}

type eofAfterOnePacerStream struct {
	ready chan struct{}
	once  sync.Once
	index int
	err   error
}

func newEOFAfterOnePacerStream() *eofAfterOnePacerStream {
	return &eofAfterOnePacerStream{
		ready: make(chan struct{}),
	}
}

func (s *eofAfterOnePacerStream) PushText(string) error {
	s.once.Do(func() {
		close(s.ready)
	})
	return nil
}

func (s *eofAfterOnePacerStream) Flush() error {
	return nil
}

func (s *eofAfterOnePacerStream) Close() error {
	s.once.Do(func() {
		close(s.ready)
	})
	return nil
}

func (s *eofAfterOnePacerStream) Next() (*SynthesizedAudio, error) {
	<-s.ready
	if s.index > 0 {
		if s.err != nil {
			return nil, s.err
		}
		return nil, io.EOF
	}
	s.index++
	return &SynthesizedAudio{
		Frame: &model.AudioFrame{
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: 24000,
		},
	}, nil
}

type nilThenAudioPacerStream struct {
	ready chan struct{}
	once  sync.Once
	index int
}

func newNilThenAudioPacerStream() *nilThenAudioPacerStream {
	return &nilThenAudioPacerStream{
		ready: make(chan struct{}),
	}
}

func (s *nilThenAudioPacerStream) PushText(string) error {
	s.once.Do(func() {
		close(s.ready)
	})
	return nil
}

func (s *nilThenAudioPacerStream) Flush() error {
	return nil
}

func (s *nilThenAudioPacerStream) Close() error {
	s.once.Do(func() {
		close(s.ready)
	})
	return nil
}

func (s *nilThenAudioPacerStream) Next() (*SynthesizedAudio, error) {
	<-s.ready
	switch s.index {
	case 0:
		s.index++
		return nil, nil
	case 1:
		s.index++
		return &SynthesizedAudio{
			Frame: &model.AudioFrame{
				SampleRate:        24000,
				NumChannels:       1,
				SamplesPerChannel: 24000,
			},
		}, nil
	default:
		return nil, io.EOF
	}
}

type pushErrorPacerStream struct {
	err    error
	once   sync.Once
	pushed chan struct{}
}

func (s *pushErrorPacerStream) PushText(string) error {
	s.once.Do(func() {
		close(s.pushed)
	})
	return s.err
}

func (s *pushErrorPacerStream) Flush() error {
	return nil
}

func (s *pushErrorPacerStream) Close() error {
	s.once.Do(func() {
		close(s.pushed)
	})
	return nil
}

func (s *pushErrorPacerStream) Next() (*SynthesizedAudio, error) {
	<-s.pushed
	return nil, io.EOF
}

type blockingClosePacerStream struct {
	nextCalled chan struct{}
	closeSeen  chan struct{}
	release    chan struct{}
	nextOnce   sync.Once
	closeOnce  sync.Once
}

func newBlockingClosePacerStream() *blockingClosePacerStream {
	return &blockingClosePacerStream{
		nextCalled: make(chan struct{}),
		closeSeen:  make(chan struct{}),
		release:    make(chan struct{}),
	}
}

func (s *blockingClosePacerStream) PushText(string) error {
	return nil
}

func (s *blockingClosePacerStream) Flush() error {
	return nil
}

func (s *blockingClosePacerStream) Close() error {
	s.closeOnce.Do(func() {
		close(s.closeSeen)
	})
	return nil
}

func (s *blockingClosePacerStream) Next() (*SynthesizedAudio, error) {
	s.nextOnce.Do(func() {
		close(s.nextCalled)
	})
	<-s.closeSeen
	<-s.release
	return nil, context.Canceled
}

func (s *blockingClosePacerStream) waitForNext(t *testing.T) bool {
	t.Helper()

	select {
	case <-s.nextCalled:
		return true
	case <-time.After(200 * time.Millisecond):
		return false
	}
}

func (s *blockingClosePacerStream) waitForClose(t *testing.T) bool {
	t.Helper()

	select {
	case <-s.closeSeen:
		return true
	case <-time.After(200 * time.Millisecond):
		return false
	}
}

func (s *blockingClosePacerStream) releaseNext() {
	close(s.release)
}
