package agent

import (
	"context"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/model"
)

type mockAudioReceiver struct {
	audio chan *model.AudioFrame
}

func (m *mockAudioReceiver) Start(ctx context.Context) error { return nil }
func (m *mockAudioReceiver) Stream() <-chan *model.AudioFrame { return m.audio }
func (m *mockAudioReceiver) NotifyPlaybackFinished(pos time.Duration, interrupted bool) error {
	return nil
}
func (m *mockAudioReceiver) Close() error { return nil }

type mockVideoGenerator struct {
	stream chan interface{}
}

func (m *mockVideoGenerator) PushAudio(frame *model.AudioFrame) error { return nil }
func (m *mockVideoGenerator) Stream() <-chan interface{}             { return m.stream }
func (m *mockVideoGenerator) ClearBuffer() error                     { return nil }
func (m *mockVideoGenerator) Close() error                           { return nil }

type mockAVSync struct{}

func (m *mockAVSync) Push(frame interface{}) error { return nil }
func (m *mockAVSync) Close() error               { return nil }

func TestAvatarRunner(t *testing.T) {
	audio := make(chan *model.AudioFrame, 1)
	video := make(chan interface{}, 1)
	
	audioRecv := &mockAudioReceiver{audio: audio}
	videoGen := &mockVideoGenerator{stream: video}
	avSync := &mockAVSync{}
	
	opts := AvatarOptions{
		AudioSampleRate: 48000,
		AudioChannels:   1,
		VideoFPS:        30,
	}

	r := NewAvatarRunner(nil, audioRecv, videoGen, opts, avSync, false)
	err := r.Start(context.Background())
	if err != nil {
		t.Fatalf("Failed to start AvatarRunner: %v", err)
	}
	defer r.Stop()

	// Push audio frame to receiver
	audio <- &model.AudioFrame{Data: []byte("audio"), SampleRate: 48000, NumChannels: 1, SamplesPerChannel: 480}
	
	// Push video frame to generator
	video <- &model.VideoFrame{Data: []byte("video"), Width: 1024, Height: 768}
	
	// Give it a moment to process
	time.Sleep(100 * time.Millisecond)
}

func TestQueueIO(t *testing.T) {
	io := NewQueueIO()
	err := io.SendAvatarData(context.Background(), []byte("data"))
	if err != nil {
		t.Errorf("SendAvatarData failed: %v", err)
	}
	
	data := <-io.ReadQueue()
	if string(data) != "data" {
		t.Errorf("Expected 'data', got %q", string(data))
	}
}
