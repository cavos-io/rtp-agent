package worker

import (
	"bytes"
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

func TestPreConnectAudioRawPCMUsesReferenceByteStreamFlush(t *testing.T) {
	data := make([]byte, 24)
	for i := 0; i < 12; i++ {
		data[i*2] = byte(i + 1)
	}

	frames, err := readPreConnectRawPCMFrames(bytes.NewReader(data), 100, 1)
	if err != nil {
		t.Fatalf("readPreConnectRawPCMFrames() error = %v", err)
	}

	if len(frames) != 2 {
		t.Fatalf("frames len = %d, want byte-stream chunk and flushed tail", len(frames))
	}
	if frames[0].SampleRate != 100 || frames[0].NumChannels != 1 || frames[0].SamplesPerChannel != 10 {
		t.Fatalf("first frame shape = %d/%d/%d, want 100/1/10", frames[0].SampleRate, frames[0].NumChannels, frames[0].SamplesPerChannel)
	}
	if !bytes.Equal(frames[0].Data, data[:20]) {
		t.Fatalf("first frame data = %v, want %v", frames[0].Data, data[:20])
	}
	if frames[1].SampleRate != 100 || frames[1].NumChannels != 1 || frames[1].SamplesPerChannel != 2 {
		t.Fatalf("tail frame shape = %d/%d/%d, want 100/1/2", frames[1].SampleRate, frames[1].NumChannels, frames[1].SamplesPerChannel)
	}
	if !bytes.Equal(frames[1].Data, data[20:]) {
		t.Fatalf("tail frame data = %v, want %v", frames[1].Data, data[20:])
	}
}

func TestPreConnectAudioRawPCMRejectsInvalidAudioShape(t *testing.T) {
	if _, err := readPreConnectRawPCMFrames(bytes.NewReader([]byte{1, 0}), 0, 1); err == nil {
		t.Fatal("readPreConnectRawPCMFrames() zero sample rate error = nil")
	}
	if _, err := readPreConnectRawPCMFrames(bytes.NewReader([]byte{1, 0}), 100, 0); err == nil {
		t.Fatal("readPreConnectRawPCMFrames() zero channels error = nil")
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
