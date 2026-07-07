package livekit

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	livekitproto "github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
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

func TestPreConnectAudioFailedBufferFulfillsExistingWaiter(t *testing.T) {
	handler := NewPreConnectAudioHandler(nil, time.Second)
	received := make(chan []*model.AudioFrame, 1)
	go func() {
		received <- handler.WaitForData(context.Background(), "track-invalid")
	}()

	waitForPreConnectBufferWaiter(t, handler, "track-invalid")
	handler.failBuffer("track-invalid")

	select {
	case frames := <-received:
		if frames != nil {
			t.Fatalf("WaitForData() failed buffer frames = %#v, want nil", frames)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitForData() did not return after pre-connect audio failure")
	}
}

func TestPreConnectAudioMissingTrackIDIgnored(t *testing.T) {
	handler := NewPreConnectAudioHandler(nil, time.Millisecond)
	reader := lksdk.NewByteStreamReader(lksdk.ByteStreamInfo{}, nil)

	handler.readAudioTask(reader, "caller-a")

	handler.mu.Lock()
	bufferCount := len(handler.buffers)
	handler.mu.Unlock()
	if bufferCount != 0 {
		t.Fatalf("buffers len after missing trackId = %d, want 0", bufferCount)
	}
}

func TestPreConnectAudioMissingFormatFailsKnownTrack(t *testing.T) {
	for _, tt := range []struct {
		name  string
		attrs map[string]string
	}{
		{
			name:  "missing sample rate",
			attrs: map[string]string{"trackId": "track-missing-sample-rate", "channels": "1"},
		},
		{
			name:  "missing channels",
			attrs: map[string]string{"trackId": "track-missing-channels", "sampleRate": "24000"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			room := lksdk.NewRoom(nil)
			handler := NewPreConnectAudioHandler(room, time.Second)
			handler.Register()

			room.OnStreamHeader(&livekitproto.DataStream_Header{
				StreamId:   "stream-" + tt.attrs["trackId"],
				Topic:      PreConnectAudioBufferStream,
				Attributes: tt.attrs,
				ContentHeader: &livekitproto.DataStream_Header_ByteHeader{
					ByteHeader: &livekitproto.DataStream_ByteHeader{},
				},
			}, "caller-a")

			done := make(chan []*model.AudioFrame, 1)
			go func() {
				done <- handler.WaitForData(context.Background(), tt.attrs["trackId"])
			}()

			select {
			case frames := <-done:
				if frames != nil {
					t.Fatalf("WaitForData() missing format frames = %#v, want nil", frames)
				}
			case <-time.After(50 * time.Millisecond):
				t.Fatal("WaitForData() blocked after missing format failure")
			}
		})
	}
}

func TestPreConnectAudioCloseIgnoresLateBuffer(t *testing.T) {
	handler := NewPreConnectAudioHandler(nil, time.Millisecond)
	frame := &model.AudioFrame{
		Data:              []byte{1, 2, 3, 4},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}

	handler.Close()
	handler.publishBuffer("track-after-close", &PreConnectAudioBuffer{
		Timestamp: time.Now(),
		Frames:    []*model.AudioFrame{frame},
	})

	if frames := handler.WaitForData(context.Background(), "track-after-close"); frames != nil {
		t.Fatalf("WaitForData() after close and late publish = %#v, want nil", frames)
	}
}

func TestPreConnectAudioWaitAfterCloseReturnsImmediately(t *testing.T) {
	handler := NewPreConnectAudioHandler(nil, time.Second)
	handler.Close()

	done := make(chan []*model.AudioFrame, 1)
	go func() {
		done <- handler.WaitForData(context.Background(), "track-after-close")
	}()

	select {
	case frames := <-done:
		if frames != nil {
			t.Fatalf("WaitForData() after close = %#v, want nil", frames)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("WaitForData() blocked after handler close")
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

func TestPreConnectAudioDuplicateCompletedBufferResetsClosedSlot(t *testing.T) {
	handler := NewPreConnectAudioHandler(nil, time.Second)
	newFrame := &model.AudioFrame{
		Data:              []byte{3, 4},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}
	completed := make(chan *PreConnectAudioBuffer, 1)
	close(completed)

	handler.buffers["track-dup"] = completed
	handler.publishBuffer("track-dup", &PreConnectAudioBuffer{
		Timestamp: time.Now(),
		Frames:    []*model.AudioFrame{newFrame},
	})

	frames := handler.WaitForData(context.Background(), "track-dup")
	if len(frames) != 1 || frames[0] != newFrame {
		t.Fatalf("WaitForData() duplicate completed closed-slot frames = %#v, want latest frame", frames)
	}
}

func TestPreConnectAudioAfterConnectStillWaitsForData(t *testing.T) {
	handler := NewPreConnectAudioHandler(nil, time.Second)
	handler.afterConnect = true
	frame := &model.AudioFrame{
		Data:              []byte{1, 2, 3, 4},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}

	done := make(chan []*model.AudioFrame, 1)
	go func() {
		done <- handler.WaitForData(context.Background(), "track-late")
	}()

	select {
	case frames := <-done:
		t.Fatalf("WaitForData() returned before data after room connection: %#v", frames)
	case <-time.After(50 * time.Millisecond):
	}

	handler.publishBuffer("track-late", &PreConnectAudioBuffer{
		Timestamp: time.Now(),
		Frames:    []*model.AudioFrame{frame},
	})

	select {
	case frames := <-done:
		if len(frames) != 1 || frames[0] != frame {
			t.Fatalf("WaitForData() frames = %#v, want published frame after room connection", frames)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitForData() did not return published pre-connect audio after room connection")
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

func TestPreConnectAudioOpusRejectsInvalidDecoderConfig(t *testing.T) {
	frames, err := readPreConnectOpusFrames(bytes.NewReader(nil), 123, 1)

	if err == nil {
		t.Fatal("readPreConnectOpusFrames() invalid decoder config error = nil")
	}
	if frames != nil {
		t.Fatalf("readPreConnectOpusFrames() frames = %#v, want nil on decoder setup error", frames)
	}
}

func TestPreConnectAudioRawPCMReadErrorDropsPartialFrames(t *testing.T) {
	readErr := errors.New("stream failed")
	reader := &preConnectErrReader{
		data: []byte{1, 0},
		err:  readErr,
	}

	frames, err := readPreConnectRawPCMFrames(reader, 10, 1)

	if !errors.Is(err, readErr) {
		t.Fatalf("readPreConnectRawPCMFrames() error = %v, want %v", err, readErr)
	}
	if frames != nil {
		t.Fatalf("readPreConnectRawPCMFrames() frames = %#v, want nil on read error", frames)
	}
}

func TestRoomIOPreConnectAudioWaitReturnsRawFrames(t *testing.T) {
	data := make([]byte, 24)
	for i := 0; i < 12; i++ {
		data[i*2] = byte(i + 1)
	}

	frames, err := readPreConnectRawPCMFrames(bytes.NewReader(data), 100, 1)
	if err != nil {
		t.Fatalf("readPreConnectRawPCMFrames() error = %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("frames len = %d, want raw byte-stream frame plus flushed tail", len(frames))
	}
	if !bytes.Equal(frames[0].Data, data[:20]) || !bytes.Equal(frames[1].Data, data[20:]) {
		t.Fatalf("raw frame data = %v/%v, want %v/%v", frames[0].Data, frames[1].Data, data[:20], data[20:])
	}
}

func TestRoomIOPreConnectAudioMissingTrackIDIgnored(t *testing.T) {
	handler := NewPreConnectAudioHandler(nil, time.Millisecond)
	reader := lksdk.NewByteStreamReader(lksdk.ByteStreamInfo{}, nil)

	handler.readAudioTask(reader, "caller-a")

	handler.mu.Lock()
	defer handler.mu.Unlock()
	if len(handler.buffers) != 0 {
		t.Fatalf("buffers len after missing trackId = %d, want 0", len(handler.buffers))
	}
}

func TestRoomIOPreConnectAudioMissingFormatFails(t *testing.T) {
	room := lksdk.NewRoom(nil)
	handler := NewPreConnectAudioHandler(room, time.Second)
	handler.Register()
	received := make(chan []*model.AudioFrame, 1)
	go func() {
		received <- handler.WaitForData(context.Background(), "track-missing-format")
	}()
	waitForPreConnectBufferWaiter(t, handler, "track-missing-format")

	room.OnStreamHeader(&livekitproto.DataStream_Header{
		StreamId:   "stream-track-missing-format",
		Topic:      PreConnectAudioBufferStream,
		Attributes: map[string]string{"trackId": "track-missing-format"},
		ContentHeader: &livekitproto.DataStream_Header_ByteHeader{
			ByteHeader: &livekitproto.DataStream_ByteHeader{},
		},
	}, "caller-a")

	select {
	case frames := <-received:
		if frames != nil {
			t.Fatalf("WaitForData() missing format frames = %#v, want nil", frames)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitForData() blocked after missing format failure")
	}
}

func TestRoomIOPreConnectAudioTimeoutReturnsEmpty(t *testing.T) {
	handler := NewPreConnectAudioHandler(nil, time.Millisecond)

	if frames := handler.WaitForData(context.Background(), "track-timeout"); frames != nil {
		t.Fatalf("WaitForData() timeout frames = %#v, want nil", frames)
	}
}

func TestRoomIOPreConnectAudioOldBufferDiscarded(t *testing.T) {
	handler := NewPreConnectAudioHandler(nil, time.Second)
	handler.publishBuffer("track-stale", &PreConnectAudioBuffer{
		Timestamp: time.Now().Add(-2 * time.Second),
		Frames: []*model.AudioFrame{{
			Data:              []byte{1, 2},
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: 1,
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

func TestRoomIOPreConnectAudioDuplicateCompletedBufferResets(t *testing.T) {
	handler := NewPreConnectAudioHandler(nil, time.Second)
	newFrame := &model.AudioFrame{
		Data:              []byte{3, 4},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}
	completed := make(chan *PreConnectAudioBuffer, 1)
	close(completed)

	handler.buffers["track-dup"] = completed
	handler.publishBuffer("track-dup", &PreConnectAudioBuffer{
		Timestamp: time.Now(),
		Frames:    []*model.AudioFrame{newFrame},
	})

	frames := handler.WaitForData(context.Background(), "track-dup")
	if len(frames) != 1 || frames[0] != newFrame {
		t.Fatalf("WaitForData() duplicate completed buffer frames = %#v, want latest frame", frames)
	}
}

func TestRoomIOPreConnectAudioCloseCancelsReaders(t *testing.T) {
	handler := NewPreConnectAudioHandler(nil, time.Second)
	handler.Close()

	done := make(chan []*model.AudioFrame, 1)
	go func() {
		done <- handler.WaitForData(context.Background(), "track-after-close")
	}()

	select {
	case frames := <-done:
		if frames != nil {
			t.Fatalf("WaitForData() after close = %#v, want nil", frames)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("WaitForData() blocked after pre-connect handler close")
	}

	handler.publishBuffer("track-after-close", &PreConnectAudioBuffer{
		Timestamp: time.Now(),
		Frames: []*model.AudioFrame{{
			Data:              []byte{1, 2},
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: 1,
		}},
	})
	if frames := handler.WaitForData(context.Background(), "track-after-close"); frames != nil {
		t.Fatalf("WaitForData() after close and late publish = %#v, want nil", frames)
	}
}

type preConnectErrReader struct {
	data []byte
	err  error
	done bool
}

func (r *preConnectErrReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	copy(p, r.data)
	return len(r.data), r.err
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
