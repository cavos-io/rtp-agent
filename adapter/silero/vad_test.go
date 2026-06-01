package silero

import (
	"context"
	"testing"
	"time"

	"github.com/cavos-io/conversation-worker/core/vad"
	"github.com/cavos-io/conversation-worker/model"
)

func TestSileroFallbackHonorsMinimumDurations(t *testing.T) {
	detector := NewSileroVAD(
		WithMinSpeechDuration(0.03),
		WithMinSilenceDuration(0.03),
		WithActivationThreshold(0.5),
	)
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	for _, frame := range []*model.AudioFrame{
		testAudioFrame(16000, 160, 6000),
		testAudioFrame(16000, 160, 6000),
		testAudioFrame(16000, 160, 6000),
		testAudioFrame(16000, 160, 0),
		testAudioFrame(16000, 160, 0),
		testAudioFrame(16000, 160, 0),
	} {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatalf("PushFrame() error = %v", err)
		}
	}

	for range 3 {
		assertSileroVADEventType(t, stream, vad.VADEventInferenceDone)
	}
	assertSileroVADEventType(t, stream, vad.VADEventStartOfSpeech)
	for range 3 {
		assertSileroVADEventType(t, stream, vad.VADEventInferenceDone)
	}
	assertSileroVADEventType(t, stream, vad.VADEventEndOfSpeech)
}

func TestSileroFallbackHonorsBufferingOptions(t *testing.T) {
	detector := NewSileroVAD(
		WithPrefixPaddingDuration(0.02),
		WithMaxBufferedSpeech(0.04),
		WithMinSpeechDuration(0.02),
		WithActivationThreshold(0.5),
	)
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	frames := []*model.AudioFrame{
		testAudioFrame(16000, 160, 0),
		testAudioFrame(16000, 160, 0),
		testAudioFrame(16000, 160, 6000),
		testAudioFrame(16000, 160, 6000),
		testAudioFrame(16000, 160, 6000),
	}
	for _, frame := range frames {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatalf("PushFrame() error = %v", err)
		}
	}

	for range 4 {
		assertSileroVADEventType(t, stream, vad.VADEventInferenceDone)
	}
	start := nextSileroVADEvent(t, stream)
	if start.Type != vad.VADEventStartOfSpeech {
		t.Fatalf("event type = %s, want %s", start.Type, vad.VADEventStartOfSpeech)
	}
	if len(start.Frames) != 4 {
		t.Fatalf("start frames len = %d, want 4", len(start.Frames))
	}
	for i, want := range frames[:4] {
		if start.Frames[i] != want {
			t.Fatalf("start frame %d = %#v, want %#v", i, start.Frames[i], want)
		}
	}
}

func assertSileroVADEventType(t *testing.T, stream vad.VADStream, want vad.VADEventType) {
	t.Helper()
	event := nextSileroVADEvent(t, stream)
	if event.Type != want {
		t.Fatalf("event type = %s, want %s", event.Type, want)
	}
}

func nextSileroVADEvent(t *testing.T, stream vad.VADStream) *vad.VADEvent {
	t.Helper()

	done := make(chan struct {
		event *vad.VADEvent
		err   error
	}, 1)
	go func() {
		event, err := stream.Next()
		done <- struct {
			event *vad.VADEvent
			err   error
		}{event: event, err: err}
	}()

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("Next() error = %v", result.err)
		}
		return result.event
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for VAD event")
		return nil
	}
}

func testAudioFrame(sampleRate uint32, samples int, value int16) *model.AudioFrame {
	data := make([]byte, samples*2)
	for i := 0; i < samples; i++ {
		data[i*2] = byte(value)
		data[i*2+1] = byte(uint16(value) >> 8)
	}
	return &model.AudioFrame{
		Data:              data,
		SampleRate:        sampleRate,
		NumChannels:       1,
		SamplesPerChannel: uint32(samples),
	}
}
