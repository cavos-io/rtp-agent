package tts

import (
	"context"
	"fmt"
	"testing"
	"time"
)

type mockTTS struct {
	label string
	synthesizeFunc func(ctx context.Context, text string) (ChunkedStream, error)
}

func (m *mockTTS) Label() string { return m.label }
func (m *mockTTS) Capabilities() TTSCapabilities { return TTSCapabilities{} }
func (m *mockTTS) SampleRate() int { return 16000 }
func (m *mockTTS) NumChannels() int { return 1 }
func (m *mockTTS) Synthesize(ctx context.Context, text string) (ChunkedStream, error) {
	return m.synthesizeFunc(ctx, text)
}
func (m *mockTTS) Stream(ctx context.Context) (SynthesizeStream, error) {
	return nil, fmt.Errorf("not implemented")
}

func TestFallbackAdapter_Synthesize(t *testing.T) {
	t1 := &mockTTS{
		label: "t1",
		synthesizeFunc: func(ctx context.Context, text string) (ChunkedStream, error) {
			return nil, fmt.Errorf("fail")
		},
	}
	t2 := &mockTTS{
		label: "t2",
		synthesizeFunc: func(ctx context.Context, text string) (ChunkedStream, error) {
			return &mockChunkedStream{}, nil
		},
	}

	fallback := NewFallbackAdapter([]TTS{t1, t2})
	_, err := fallback.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Expected success, got %v", err)
	}
}

type mockChunkedStream struct{}
func (m *mockChunkedStream) Next() (*SynthesizedAudio, error) { return nil, fmt.Errorf("EOF") }
func (m *mockChunkedStream) Close() error { return nil }

func TestFallbackAdapter_PanicOnEmpty(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Expected panic on empty TTS list")
		}
	}()
	NewFallbackAdapter([]TTS{})
}

func TestSentenceStreamPacer(t *testing.T) {
	mock := &mockSynthesizeStream{
		nextFunc: func() (*SynthesizedAudio, error) {
			return &SynthesizedAudio{IsFinal: true}, nil
		},
	}
	pacer := NewSentenceStreamPacer(context.Background(), mock, 100*time.Millisecond)
	
	err := pacer.PushText("This is a sentence. And another.")
	if err != nil {
		t.Fatalf("PushText failed: %v", err)
	}

	pacer.Flush()
	
	res, err := pacer.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}
	if !res.IsFinal {
		t.Errorf("Expected final audio")
	}
}

type mockSynthesizeStream struct {
	nextFunc func() (*SynthesizedAudio, error)
}
func (m *mockSynthesizeStream) PushText(text string) error { return nil }
func (m *mockSynthesizeStream) Flush() error { return nil }
func (m *mockSynthesizeStream) Close() error { return nil }
func (m *mockSynthesizeStream) Next() (*SynthesizedAudio, error) { return m.nextFunc() }
