package tts

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/conversation-worker/model"
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

type fakePacerStream struct {
	mu        sync.Mutex
	cond      *sync.Cond
	closed    bool
	nextIndex int
	texts     []string
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
