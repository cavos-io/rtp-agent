package worker

import (
	"context"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

func TestPreConnectAudioPublishFulfillsExistingWaiter(t *testing.T) {
	handler := NewPreConnectAudioHandler(nil, time.Second)
	frame := &model.AudioFrame{
		Data:              []byte{1, 2, 3, 4},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}

	received := make(chan []*model.AudioFrame, 1)
	go func() {
		received <- handler.WaitForData(context.Background(), "track-a")
	}()

	waitForPreConnectBufferWaiter(t, handler, "track-a")
	handler.publishBuffer("track-a", &PreConnectAudioBuffer{
		Timestamp: time.Now(),
		Frames:    []*model.AudioFrame{frame},
	})

	select {
	case frames := <-received:
		if len(frames) != 1 || frames[0] != frame {
			t.Fatalf("WaitForData() frames = %#v, want published frame", frames)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitForData() did not return published pre-connect audio")
	}
}

func TestPreConnectAudioLatePublishAfterTimeoutIsNotReused(t *testing.T) {
	handler := NewPreConnectAudioHandler(nil, 10*time.Millisecond)
	frame := &model.AudioFrame{
		Data:              []byte{1, 2, 3, 4},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}

	if frames := handler.WaitForData(context.Background(), "track-timeout"); frames != nil {
		t.Fatalf("WaitForData() before publish = %#v, want nil after timeout", frames)
	}

	handler.publishBuffer("track-timeout", &PreConnectAudioBuffer{
		Timestamp: time.Now(),
		Frames:    []*model.AudioFrame{frame},
	})

	if frames := handler.WaitForData(context.Background(), "track-timeout"); frames != nil {
		t.Fatalf("WaitForData() after late publish = %#v, want nil", frames)
	}
}

func TestPreConnectAudioStaleBufferReturnsEmptyFrames(t *testing.T) {
	handler := NewPreConnectAudioHandler(nil, time.Second)
	handler.publishBuffer("track-stale", &PreConnectAudioBuffer{
		Timestamp: time.Now().Add(-2 * time.Second),
		Frames: []*model.AudioFrame{{
			Data:              []byte{1, 2, 3, 4},
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: 2,
		}},
	})

	frames := handler.WaitForData(context.Background(), "track-stale")
	if frames == nil {
		t.Fatal("WaitForData() stale frames = nil, want empty slice")
	}
	if len(frames) != 0 {
		t.Fatalf("WaitForData() stale frames len = %d, want 0", len(frames))
	}
}

func waitForPreConnectBufferWaiter(t *testing.T, handler *PreConnectAudioHandler, trackID string) {
	t.Helper()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			t.Fatalf("pre-connect waiter for %q was not registered", trackID)
		case <-ticker.C:
			handler.mu.Lock()
			_, ok := handler.buffers[trackID]
			handler.mu.Unlock()
			if ok {
				return
			}
		}
	}
}
