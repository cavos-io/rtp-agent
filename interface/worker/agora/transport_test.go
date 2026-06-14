package agora

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/interface/worker"
)

type fakeChannelClient struct {
	joinOptions  worker.AgoraOptions
	handler      EventHandler
	audioHandler AudioHandler
	pcmFrame     PCMFrame
	publishCtx   context.Context
	joinErr      error
	leaveErr     error
	publishErr   error
	leaveCount   int
	publishCount int
	joined       bool
	left         bool
}

func (f *fakeChannelClient) Join(ctx context.Context, opts worker.AgoraOptions, handler EventHandler, audioHandler AudioHandler) error {
	f.joinOptions = opts
	f.handler = handler
	f.audioHandler = audioHandler
	if f.joinErr != nil {
		return f.joinErr
	}
	f.joined = true
	return nil
}

func (f *fakeChannelClient) Leave(ctx context.Context) error {
	f.leaveCount++
	if f.leaveErr != nil {
		return f.leaveErr
	}
	f.left = true
	return nil
}

func (f *fakeChannelClient) PublishPCM(ctx context.Context, frame PCMFrame) error {
	f.publishCount++
	f.publishCtx = ctx
	f.pcmFrame = frame
	if f.publishErr != nil {
		return f.publishErr
	}
	return nil
}

func (f *fakeChannelClient) emit(event Event) {
	f.handler(event)
}

func (f *fakeChannelClient) emitAudio(frame *model.AudioFrame) {
	f.audioHandler(frame)
}

func TestTransportJoinValidatesOptions(t *testing.T) {
	tr := NewTransport(worker.AgoraOptions{}, &fakeChannelClient{})

	err := tr.Join(context.Background())
	if err == nil {
		t.Fatal("Join() error = nil, want validation error")
	}
}

func TestTransportJoinPassesOptionsToClient(t *testing.T) {
	client := &fakeChannelClient{}
	opts := worker.AgoraOptions{
		AppID:   "app",
		Channel: "support",
		UID:     "agent",
		Token:   "token",
	}
	tr := NewTransport(opts, client)

	if err := tr.Join(context.Background()); err != nil {
		t.Fatalf("Join() error = %v", err)
	}
	if !client.joined {
		t.Fatal("client joined = false, want true")
	}
	if client.joinOptions.Channel != "support" {
		t.Fatalf("client channel = %q, want support", client.joinOptions.Channel)
	}
}

func TestTransportJoinGeneratesTokenFromCertificate(t *testing.T) {
	client := &fakeChannelClient{}
	opts := worker.AgoraOptions{
		AppID:          "970CA35de60c44645bbae8a215061b33",
		AppCertificate: "5CFd2fd1755d40ecb72977518be15d3b",
		Channel:        "support",
		UID:            "agent",
	}
	tr := NewTransport(opts, client)

	if err := tr.Join(context.Background()); err != nil {
		t.Fatalf("Join() error = %v", err)
	}
	if client.joinOptions.Token == "" {
		t.Fatal("client token is empty, want generated RTC token")
	}
	if client.joinOptions.Token == opts.Token {
		t.Fatal("client token was not generated")
	}
}

func TestTransportLeaveDelegatesToClient(t *testing.T) {
	client := &fakeChannelClient{}
	tr := NewTransport(worker.AgoraOptions{AppID: "app", Channel: "support"}, client)

	if err := tr.Leave(context.Background()); err != nil {
		t.Fatalf("Leave() error = %v", err)
	}
	if !client.left {
		t.Fatal("client left = false, want true")
	}
}

func TestTransportReturnsClientErrors(t *testing.T) {
	joinErr := errors.New("join failed")
	tr := NewTransport(worker.AgoraOptions{AppID: "app", Channel: "support"}, &fakeChannelClient{joinErr: joinErr})

	if err := tr.Join(context.Background()); !errors.Is(err, joinErr) {
		t.Fatalf("Join() error = %v, want %v", err, joinErr)
	}
}

func TestTransportForwardsClientEvents(t *testing.T) {
	client := &fakeChannelClient{}
	tr := NewTransport(worker.AgoraOptions{AppID: "app", Channel: "support"}, client)

	if err := tr.Join(context.Background()); err != nil {
		t.Fatalf("Join() error = %v", err)
	}
	client.emit(Event{Kind: EventConnected, Channel: "support"})

	select {
	case event := <-tr.Events():
		if event.Kind != EventConnected {
			t.Fatalf("event kind = %q, want %q", event.Kind, EventConnected)
		}
		if event.Channel != "support" {
			t.Fatalf("event channel = %q, want support", event.Channel)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestTransportForwardsClientAudioFrames(t *testing.T) {
	client := &fakeChannelClient{}
	tr := NewTransport(worker.AgoraOptions{AppID: "app", Channel: "support"}, client)
	received := make(chan *model.AudioFrame, 1)
	tr.SetAudioHandler(func(frame *model.AudioFrame) {
		received <- frame
	})

	if err := tr.Join(context.Background()); err != nil {
		t.Fatalf("Join() error = %v", err)
	}
	client.emitAudio(&model.AudioFrame{
		Data:              []byte{1, 2, 3, 4},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	})

	select {
	case frame := <-received:
		if frame.SampleRate != 16000 {
			t.Fatalf("frame sample rate = %d, want 16000", frame.SampleRate)
		}
		if frame.SamplesPerChannel != 2 {
			t.Fatalf("frame samples per channel = %d, want 2", frame.SamplesPerChannel)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for audio frame")
	}
}

func TestTransportCloseLeavesClientAndClosesEvents(t *testing.T) {
	client := &fakeChannelClient{}
	tr := NewTransport(worker.AgoraOptions{AppID: "app", Channel: "support"}, client)

	if err := tr.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := tr.Close(context.Background()); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if client.leaveCount != 1 {
		t.Fatalf("leave count = %d, want 1", client.leaveCount)
	}
	if _, ok := <-tr.Events(); ok {
		t.Fatal("events channel open after Close(), want closed")
	}
}

func TestTransportPublishPCMValidatesAndDelegates(t *testing.T) {
	client := &fakeChannelClient{}
	tr := NewTransport(worker.AgoraOptions{AppID: "app", Channel: "support"}, client)
	frame := PCMFrame{
		Data:       []byte{1, 2, 3, 4},
		SampleRate: 100,
		Channels:   2,
		StartPTSMS: 42,
	}

	if err := tr.PublishPCM(context.Background(), frame); err != nil {
		t.Fatalf("PublishPCM() error = %v", err)
	}
	if client.publishCount != 1 {
		t.Fatalf("publish count = %d, want 1", client.publishCount)
	}
	if client.pcmFrame.StartPTSMS != 42 {
		t.Fatalf("start PTS = %d, want 42", client.pcmFrame.StartPTSMS)
	}
}

func TestTransportPublishPCMNormalizesNilContext(t *testing.T) {
	client := &fakeChannelClient{}
	tr := NewTransport(worker.AgoraOptions{AppID: "app", Channel: "support"}, client)
	frame := PCMFrame{
		Data:       []byte{1, 2, 3, 4},
		SampleRate: 100,
		Channels:   2,
	}

	var nilCtx context.Context
	if err := tr.PublishPCM(nilCtx, frame); err != nil {
		t.Fatalf("PublishPCM() error = %v", err)
	}
	if client.publishCtx == nil {
		t.Fatal("PublishPCM() passed nil context to channel client")
	}
}

func TestTransportPublishPCMRejectsNonTenMillisecondFrames(t *testing.T) {
	client := &fakeChannelClient{}
	tr := NewTransport(worker.AgoraOptions{AppID: "app", Channel: "support"}, client)
	frame := PCMFrame{
		Data:       []byte{1, 2},
		SampleRate: 100,
		Channels:   2,
	}

	err := tr.PublishPCM(context.Background(), frame)
	if err == nil {
		t.Fatal("PublishPCM() error = nil, want 10 ms validation error")
	}
	if !strings.Contains(err.Error(), "10 ms") {
		t.Fatalf("PublishPCM() error = %v, want 10 ms validation error", err)
	}
	if client.publishCount != 0 {
		t.Fatalf("publish count = %d, want 0", client.publishCount)
	}
}

func TestTransportPublishPCMRejectsTwentyMillisecondFrames(t *testing.T) {
	client := &fakeChannelClient{}
	tr := NewTransport(worker.AgoraOptions{AppID: "app", Channel: "support"}, client)
	frame := PCMFrame{
		Data:       make([]byte, 640),
		SampleRate: 16000,
		Channels:   1,
	}

	err := tr.PublishPCM(context.Background(), frame)
	if err == nil {
		t.Fatal("PublishPCM() error = nil, want exact 10 ms validation error")
	}
	if !strings.Contains(err.Error(), "exactly 10 ms") {
		t.Fatalf("PublishPCM() error = %v, want exactly 10 ms validation error", err)
	}
	if client.publishCount != 0 {
		t.Fatalf("publish count = %d, want 0", client.publishCount)
	}
}
