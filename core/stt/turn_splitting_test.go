package stt

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

func TestTurnSplittingSTTReplacesBeforeRetiringAndForwardsLateFinal(t *testing.T) {
	provider := &splitTestSTT{}
	logical, err := NewTurnSplittingSTT(provider, 10*time.Millisecond).Stream(context.Background(), "en")
	if err != nil {
		t.Fatal(err)
	}
	defer logical.Close()

	first := provider.stream(0)
	if err := logical.Flush(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for provider.count() != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if provider.count() != 2 {
		t.Fatal("replacement stream was not opened")
	}
	for !first.isEnded() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !first.isEnded() {
		t.Fatal("old stream was not ended after replacement opened")
	}
	if err := logical.PushFrame(&model.AudioFrame{SampleRate: 16000}); err != nil {
		t.Fatal(err)
	}
	if provider.stream(1).pushCount() != 1 || first.pushCount() != 0 {
		t.Fatal("new audio did not route exclusively to replacement stream")
	}

	first.events <- &SpeechEvent{Type: SpeechEventFinalTranscript}
	event, err := logical.Next()
	if err != nil || event.Type != SpeechEventFinalTranscript {
		t.Fatalf("late final not forwarded: event=%v err=%v", event, err)
	}
}

func TestTurnSplittingSTTFinalCancelsPendingSplit(t *testing.T) {
	provider := &splitTestSTT{}
	logical, err := NewTurnSplittingSTT(provider, 20*time.Millisecond).Stream(context.Background(), "en")
	if err != nil {
		t.Fatal(err)
	}
	defer logical.Close()
	if err := logical.Flush(); err != nil {
		t.Fatal(err)
	}
	provider.stream(0).events <- &SpeechEvent{Type: SpeechEventFinalTranscript}
	if _, err := logical.Next(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if provider.count() != 1 {
		t.Fatalf("final should cancel split; streams=%d", provider.count())
	}
}

func TestTurnSplittingSTTLateRetiredFinalDoesNotCancelCurrentSplit(t *testing.T) {
	provider := &splitTestSTT{}
	logical, err := NewTurnSplittingSTT(provider, 10*time.Millisecond).Stream(context.Background(), "en")
	if err != nil {
		t.Fatal(err)
	}
	defer logical.Close()
	if err := logical.Flush(); err != nil {
		t.Fatal(err)
	}
	waitForSplitStreams(t, provider, 2)
	if err := logical.Flush(); err != nil {
		t.Fatal(err)
	}
	provider.stream(0).events <- &SpeechEvent{Type: SpeechEventFinalTranscript}
	if _, err := logical.Next(); err != nil {
		t.Fatal(err)
	}
	waitForSplitStreams(t, provider, 3)
}

func TestTurnSplittingSTTPreservesTimingOnReplacement(t *testing.T) {
	provider := &splitTestSTT{}
	logical, err := NewTurnSplittingSTT(provider, time.Millisecond).Stream(context.Background(), "en")
	if err != nil {
		t.Fatal(err)
	}
	defer logical.Close()
	timing := logical.(StreamTiming)
	timing.SetStartTimeOffset(2.5)
	timing.SetStartTime(10.5)
	if err := logical.Flush(); err != nil {
		t.Fatal(err)
	}
	waitForSplitStreams(t, provider, 2)
	replacement := provider.stream(1)
	deadline := time.Now().Add(time.Second)
	for (replacement.StartTimeOffset() != 2.5 || replacement.StartTime() != 10.5) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if replacement.StartTimeOffset() != 2.5 || replacement.StartTime() != 10.5 {
		t.Fatalf("replacement timing = (%v, %v), want (2.5, 10.5)", replacement.StartTimeOffset(), replacement.StartTime())
	}
}

func TestTurnSplittingSTTCloseCancelsReplacementOpen(t *testing.T) {
	provider := &blockingSplitSTT{splitTestSTT: splitTestSTT{}, started: make(chan struct{})}
	logical, err := NewTurnSplittingSTT(provider, time.Millisecond).Stream(context.Background(), "en")
	if err != nil {
		t.Fatal(err)
	}
	provider.block = true
	if err := logical.Flush(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("replacement open did not start")
	}
	done := make(chan struct{})
	go func() { _ = logical.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("close blocked behind replacement open")
	}
}

func TestTurnSplittingSTTForwardsActiveError(t *testing.T) {
	provider := &splitTestSTT{}
	logical, err := NewTurnSplittingSTT(provider, time.Second).Stream(context.Background(), "en")
	if err != nil {
		t.Fatal(err)
	}
	defer logical.Close()
	cause := errors.New("provider failed")
	provider.stream(0).fail <- cause
	result := make(chan error, 1)
	go func() { _, err := logical.Next(); result <- err }()
	select {
	case err := <-result:
		if !errors.Is(err, cause) {
			t.Fatalf("Next error = %v, want %v", err, cause)
		}
	case <-time.After(time.Second):
		t.Fatal("active provider error was hidden")
	}
	select {
	case <-provider.stream(0).closed:
	case <-time.After(time.Second):
		t.Fatal("failed provider stream was not closed")
	}
}

func TestTurnSplittingSTTDiscardsErrorQueuedBeforeRetirement(t *testing.T) {
	provider := &splitTestSTT{}
	logical, err := NewTurnSplittingSTT(provider, time.Millisecond).Stream(context.Background(), "en")
	if err != nil {
		t.Fatal(err)
	}
	defer logical.Close()
	old := provider.stream(0)
	old.failRead = make(chan struct{})
	old.fail <- errors.New("old stream failed")
	<-old.failRead
	if err := logical.Flush(); err != nil {
		t.Fatal(err)
	}
	waitForSplitStreams(t, provider, 2)
	provider.stream(1).events <- &SpeechEvent{Type: SpeechEventStartOfSpeech}
	event, err := logical.Next()
	if err != nil || event.Type != SpeechEventStartOfSpeech {
		t.Fatalf("Next = %v, %v", event, err)
	}
}

func TestTurnSplittingSTTIgnoresRetiredError(t *testing.T) {
	provider := &splitTestSTT{}
	logical, err := NewTurnSplittingSTT(provider, time.Millisecond).Stream(context.Background(), "en")
	if err != nil {
		t.Fatal(err)
	}
	defer logical.Close()
	if err := logical.Flush(); err != nil {
		t.Fatal(err)
	}
	waitForSplitStreams(t, provider, 2)
	provider.stream(0).fail <- errors.New("retired stream ended")
	provider.stream(1).events <- &SpeechEvent{Type: SpeechEventStartOfSpeech}
	event, err := logical.Next()
	if err != nil || event.Type != SpeechEventStartOfSpeech {
		t.Fatalf("Next = %v, %v", event, err)
	}
}

func TestTurnSplittingSTTCancellationReturnsEOF(t *testing.T) {
	provider := &splitTestSTT{}
	ctx, cancel := context.WithCancel(context.Background())
	logical, err := NewTurnSplittingSTT(provider, time.Second).Stream(ctx, "en")
	if err != nil {
		t.Fatal(err)
	}
	defer logical.Close()
	cancel()
	_, err = logical.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want EOF", err)
	}
}

type splitTestSTT struct {
	mu      sync.Mutex
	streams []*splitTestStream
}

func waitForSplitStreams(t *testing.T, provider *splitTestSTT, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for provider.count() != want && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if provider.count() != want {
		t.Fatalf("streams=%d, want %d", provider.count(), want)
	}
}

type blockingSplitSTT struct {
	splitTestSTT
	block   bool
	started chan struct{}
}

func (s *blockingSplitSTT) Stream(ctx context.Context, language string) (RecognizeStream, error) {
	if s.block {
		close(s.started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return s.splitTestSTT.Stream(ctx, language)
}

func (s *splitTestSTT) Label() string                 { return "test" }
func (s *splitTestSTT) Capabilities() STTCapabilities { return STTCapabilities{Streaming: true} }
func (s *splitTestSTT) Recognize(context.Context, []*model.AudioFrame, string) (*SpeechEvent, error) {
	return nil, nil
}
func (s *splitTestSTT) Stream(context.Context, string) (RecognizeStream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stream := &splitTestStream{events: make(chan *SpeechEvent, 1), fail: make(chan error, 1), closed: make(chan struct{})}
	s.streams = append(s.streams, stream)
	return stream, nil
}
func (s *splitTestSTT) count() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.streams) }
func (s *splitTestSTT) stream(i int) *splitTestStream {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streams[i]
}

type splitTestStream struct {
	mu              sync.Mutex
	events          chan *SpeechEvent
	fail            chan error
	failRead        chan struct{}
	closed          chan struct{}
	ended           bool
	pushes          int
	startTimeOffset float64
	startTime       float64
	closeOnce       sync.Once
}

func (s *splitTestStream) PushFrame(*model.AudioFrame) error {
	s.mu.Lock()
	s.pushes++
	s.mu.Unlock()
	return nil
}
func (s *splitTestStream) Flush() error    { return nil }
func (s *splitTestStream) EndInput() error { s.mu.Lock(); s.ended = true; s.mu.Unlock(); return nil }
func (s *splitTestStream) Close() error    { s.closeOnce.Do(func() { close(s.closed) }); return nil }
func (s *splitTestStream) Next() (*SpeechEvent, error) {
	select {
	case event := <-s.events:
		return event, nil
	case err := <-s.fail:
		if s.failRead != nil {
			close(s.failRead)
		}
		return nil, err
	case <-s.closed:
		return nil, io.EOF
	}
}
func (s *splitTestStream) isEnded() bool  { s.mu.Lock(); defer s.mu.Unlock(); return s.ended }
func (s *splitTestStream) pushCount() int { s.mu.Lock(); defer s.mu.Unlock(); return s.pushes }
func (s *splitTestStream) StartTimeOffset() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startTimeOffset
}
func (s *splitTestStream) SetStartTimeOffset(offset float64) {
	s.mu.Lock()
	s.startTimeOffset = offset
	s.mu.Unlock()
}
func (s *splitTestStream) StartTime() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startTime
}
func (s *splitTestStream) SetStartTime(startTime float64) {
	s.mu.Lock()
	s.startTime = startTime
	s.mu.Unlock()
}
