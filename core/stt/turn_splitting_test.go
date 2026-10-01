package stt

import (
	"context"
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
	stream := &splitTestStream{events: make(chan *SpeechEvent, 1), closed: make(chan struct{})}
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
	mu        sync.Mutex
	events    chan *SpeechEvent
	closed    chan struct{}
	ended     bool
	pushes    int
	closeOnce sync.Once
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
	case <-s.closed:
		return nil, io.EOF
	}
}
func (s *splitTestStream) isEnded() bool  { s.mu.Lock(); defer s.mu.Unlock(); return s.ended }
func (s *splitTestStream) pushCount() int { s.mu.Lock(); defer s.mu.Unlock(); return s.pushes }
