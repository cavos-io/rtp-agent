package agora

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

type fakeChannelClient struct {
	joinOptions  Options
	handler      EventHandler
	audioHandler AudioHandler
	pcmFrame     PCMFrame
	joinCtx      context.Context
	publishCtx   context.Context
	joinErr      error
	leaveErr     error
	publishErr   error
	blockJoin    bool
	leaveCount   int
	publishCount int
	joined       bool
	left         bool
}

func (f *fakeChannelClient) Join(ctx context.Context, opts Options, handler EventHandler, audioHandler AudioHandler) error {
	f.joinCtx = ctx
	f.joinOptions = opts
	f.handler = handler
	f.audioHandler = audioHandler
	if f.joinErr != nil {
		return f.joinErr
	}
	if f.blockJoin {
		<-ctx.Done()
		return ctx.Err()
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
	tr := NewTransport(Options{}, &fakeChannelClient{})

	err := tr.Join(context.Background())
	if err == nil {
		t.Fatal("Join() error = nil, want validation error")
	}
}

func TestTransportJoinPassesOptionsToClient(t *testing.T) {
	client := &fakeChannelClient{}
	opts := Options{
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
	if client.joinOptions.AppID != "app" {
		t.Fatalf("client app ID = %q, want app", client.joinOptions.AppID)
	}
	if client.joinOptions.UID != "agent" {
		t.Fatalf("client UID = %q, want agent", client.joinOptions.UID)
	}
	if client.joinOptions.Token != "token" {
		t.Fatalf("client token = %q, want token", client.joinOptions.Token)
	}
}

func TestTransportJoinRejectsCanceledContext(t *testing.T) {
	client := &fakeChannelClient{}
	tr := NewTransport(Options{AppID: "app", Channel: "support"}, client)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := tr.Join(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Join() error = %v, want context canceled", err)
	}
	if client.joinCtx != nil {
		t.Fatal("Join() reached channel client after context cancellation")
	}
	if client.joined {
		t.Fatal("client joined after context cancellation")
	}
}

func TestTransportJoinGeneratesTokenFromCertificate(t *testing.T) {
	client := &fakeChannelClient{}
	opts := Options{
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
	tr := NewTransport(Options{AppID: "app", Channel: "support"}, client)

	if err := tr.Leave(context.Background()); err != nil {
		t.Fatalf("Leave() error = %v", err)
	}
	if !client.left {
		t.Fatal("client left = false, want true")
	}
}

func TestTransportPublishPCMRequiresJoinedChannel(t *testing.T) {
	client := &fakeChannelClient{}
	tr := NewTransport(worker.AgoraOptions{AppID: "app", Channel: "support"}, client)
	frame := PCMFrame{
		Data:       []byte{1, 2, 3, 4},
		SampleRate: 100,
		Channels:   2,
	}

	err := tr.PublishPCM(context.Background(), frame)
	if err == nil {
		t.Fatal("PublishPCM() before Join error = nil, want not joined error")
	}
	if !strings.Contains(err.Error(), "not joined") {
		t.Fatalf("PublishPCM() before Join error = %v, want not joined error", err)
	}
	if client.publishCount != 0 {
		t.Fatalf("publish count before Join = %d, want 0", client.publishCount)
	}

	if err := tr.Join(context.Background()); err != nil {
		t.Fatalf("Join() error = %v", err)
	}
	if err := tr.PublishPCM(context.Background(), frame); err != nil {
		t.Fatalf("PublishPCM() after Join error = %v", err)
	}
	if err := tr.Leave(context.Background()); err != nil {
		t.Fatalf("Leave() error = %v", err)
	}

	err = tr.PublishPCM(context.Background(), frame)
	if err == nil {
		t.Fatal("PublishPCM() after Leave error = nil, want not joined error")
	}
	if !strings.Contains(err.Error(), "not joined") {
		t.Fatalf("PublishPCM() after Leave error = %v, want not joined error", err)
	}
	if client.publishCount != 1 {
		t.Fatalf("publish count = %d, want 1", client.publishCount)
	}
}

func TestTransportReturnsClientErrors(t *testing.T) {
	joinErr := errors.New("join failed")
	tr := NewTransport(Options{AppID: "app", Channel: "support"}, &fakeChannelClient{joinErr: joinErr})

	if err := tr.Join(context.Background()); !errors.Is(err, joinErr) {
		t.Fatalf("Join() error = %v, want %v", err, joinErr)
	}
}

func TestTransportForwardsClientEvents(t *testing.T) {
	client := &fakeChannelClient{}
	tr := NewTransport(Options{AppID: "app", Channel: "support"}, client)

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

func TestTransportPrioritizesErrorEventsWhenBufferIsFull(t *testing.T) {
	client := &fakeChannelClient{}
	tr := NewTransport(Options{AppID: "app", Channel: "support"}, client)

	if err := tr.Join(context.Background()); err != nil {
		t.Fatalf("Join() error = %v", err)
	}
	for i := 0; i < cap(tr.events); i++ {
		client.emit(Event{Kind: EventUserJoined, Channel: "support", UserID: fmt.Sprintf("user-%d", i)})
	}
	client.emit(Event{Kind: EventError, Channel: "support", Reason: 110, Err: errors.New("sdk error")})

	for i := 0; i < cap(tr.events); i++ {
		select {
		case event := <-tr.Events():
			if event.Kind == EventError {
				if event.Reason != 110 {
					t.Fatalf("error reason = %d, want 110", event.Reason)
				}
				return
			}
		case <-time.After(time.Second):
			t.Fatal("timed out draining transport events")
		}
	}
	t.Fatal("transport dropped error event when buffer was full")
}

func TestTransportForwardsClientAudioFrames(t *testing.T) {
	client := &fakeChannelClient{}
	tr := NewTransport(Options{AppID: "app", Channel: "support"}, client)
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
	tr := NewTransport(Options{AppID: "app", Channel: "support"}, client)

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

func TestTransportCloseCancelsInProgressJoin(t *testing.T) {
	client := &fakeChannelClient{blockJoin: true}
	tr := NewTransport(Options{AppID: "app", Channel: "support"}, client)
	joinDone := make(chan error, 1)

	go func() {
		joinDone <- tr.Join(context.Background())
	}()

	deadline := time.After(time.Second)
	for {
		if client.joinCtx != nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for Join to reach channel client")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := tr.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case err := <-joinDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Join() error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Join() did not return after Close canceled the transport")
	}
}

func TestTransportJoinAfterCloseReturnsError(t *testing.T) {
	client := &fakeChannelClient{}
	tr := NewTransport(Options{AppID: "app", Channel: "support"}, client)

	if err := tr.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	err := tr.Join(context.Background())
	if err == nil {
		t.Fatal("Join() error = nil, want closed transport error")
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Fatalf("Join() error = %v, want closed transport error", err)
	}
	if client.joined {
		t.Fatal("client joined after transport close")
	}
}

func TestTransportPublishPCMValidatesAndDelegates(t *testing.T) {
	client := &fakeChannelClient{}
	tr := NewTransport(Options{AppID: "app", Channel: "support"}, client)
	frame := PCMFrame{
		Data:       []byte{1, 2, 3, 4},
		SampleRate: 100,
		Channels:   2,
		StartPTSMS: 42,
	}

	if err := tr.Join(context.Background()); err != nil {
		t.Fatalf("Join() error = %v", err)
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
	tr := NewTransport(Options{AppID: "app", Channel: "support"}, client)
	frame := PCMFrame{
		Data:       []byte{1, 2, 3, 4},
		SampleRate: 100,
		Channels:   2,
	}

	if err := tr.Join(context.Background()); err != nil {
		t.Fatalf("Join() error = %v", err)
	}
	var nilCtx context.Context
	if err := tr.PublishPCM(nilCtx, frame); err != nil {
		t.Fatalf("PublishPCM() error = %v", err)
	}
	if client.publishCtx == nil {
		t.Fatal("PublishPCM() passed nil context to channel client")
	}
}

func TestTransportPublishPCMRejectsCanceledContext(t *testing.T) {
	client := &fakeChannelClient{}
	tr := NewTransport(Options{AppID: "app", Channel: "support"}, client)
	frame := PCMFrame{
		Data:       []byte{1, 2, 3, 4},
		SampleRate: 100,
		Channels:   2,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := tr.PublishPCM(ctx, frame)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PublishPCM() error = %v, want context canceled", err)
	}
	if client.publishCount != 0 {
		t.Fatalf("publish count = %d, want 0", client.publishCount)
	}
}

func TestTransportPublishPCMAfterCloseReturnsError(t *testing.T) {
	client := &fakeChannelClient{}
	tr := NewTransport(Options{AppID: "app", Channel: "support"}, client)
	frame := PCMFrame{
		Data:       []byte{1, 2, 3, 4},
		SampleRate: 100,
		Channels:   2,
	}

	if err := tr.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	err := tr.PublishPCM(context.Background(), frame)
	if err == nil {
		t.Fatal("PublishPCM() error = nil, want closed transport error")
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Fatalf("PublishPCM() error = %v, want closed transport error", err)
	}
	if client.publishCount != 0 {
		t.Fatalf("publish count = %d, want 0", client.publishCount)
	}
}

func TestTransportPublishPCMRejectsNonTenMillisecondFrames(t *testing.T) {
	client := &fakeChannelClient{}
	tr := NewTransport(Options{AppID: "app", Channel: "support"}, client)
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
	tr := NewTransport(Options{AppID: "app", Channel: "support"}, client)
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
