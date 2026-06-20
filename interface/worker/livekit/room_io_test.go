package livekit

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	logutil "github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/utils/images"
	"github.com/livekit/protocol/livekit"
	livekitlogger "github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/webrtc/v4"
	"github.com/twitchtv/twirp"
)

type fakeRoomIOTextResponder struct {
	calls []string
}

func (f *fakeRoomIOTextResponder) ClaimUserTurn(ctx context.Context, fn func(context.Context) error) error {
	f.calls = append(f.calls, "claim-begin")
	err := fn(ctx)
	f.calls = append(f.calls, "claim-end")
	return err
}

func (f *fakeRoomIOTextResponder) Interrupt(force bool) error {
	f.calls = append(f.calls, "interrupt")
	return nil
}

func (f *fakeRoomIOTextResponder) GenerateReply(ctx context.Context, userInput string) (*agent.SpeechHandle, error) {
	f.calls = append(f.calls, "generate:"+userInput)
	return agent.NewSpeechHandle(true, agent.DefaultInputDetails()), nil
}

func TestRoomIOAudioTrackPublicationOptionsUseReferenceDefaults(t *testing.T) {
	rio := &RoomIO{}

	opts := rio.audioTrackPublicationOptions()

	if opts.Name != "roomio_audio" {
		t.Fatalf("audio track name = %q, want roomio_audio", opts.Name)
	}
	if opts.Source != livekit.TrackSource_MICROPHONE {
		t.Fatalf("audio track source = %v, want MICROPHONE", opts.Source)
	}
}

func TestRoomIOAudioTrackPublicationOptionsPreserveConfiguredName(t *testing.T) {
	rio := &RoomIO{
		Options: RoomOptions{
			AudioTrackName: "agent-output",
		},
	}

	opts := rio.audioTrackPublicationOptions()

	if opts.Name != "agent-output" {
		t.Fatalf("audio track name = %q, want agent-output", opts.Name)
	}
	if opts.Source != livekit.TrackSource_MICROPHONE {
		t.Fatalf("audio track source = %v, want MICROPHONE", opts.Source)
	}
}

func TestRoomIOAudioOutputCodecUsesStandardOpusChannels(t *testing.T) {
	codec := roomIOAudioOutputCodec()

	if codec.MimeType != webrtc.MimeTypeOpus {
		t.Fatalf("MimeType = %q, want %q", codec.MimeType, webrtc.MimeTypeOpus)
	}
	if codec.ClockRate != roomIOOpusClockRate {
		t.Fatalf("ClockRate = %d, want %d", codec.ClockRate, roomIOOpusClockRate)
	}
	if codec.Channels != 2 {
		t.Fatalf("Channels = %d, want 2 for standard Opus SDP negotiation", codec.Channels)
	}
}

func TestNewRoomIOUsesReferencePreConnectAudioTimeout(t *testing.T) {
	rio := NewRoomIO(lksdk.NewRoom(nil), &agent.AgentSession{}, RoomOptions{})

	if rio.preConnectAudio == nil {
		t.Fatal("preConnectAudio = nil, want handler enabled by default")
	}
	if rio.preConnectAudio.timeout != 3*time.Second {
		t.Fatalf("pre-connect audio timeout = %v, want 3s", rio.preConnectAudio.timeout)
	}
}

func TestNewRoomIOPreservesConfiguredPreConnectAudioTimeout(t *testing.T) {
	rio := NewRoomIO(lksdk.NewRoom(nil), &agent.AgentSession{}, RoomOptions{
		PreConnectAudioTimeout: 750 * time.Millisecond,
	})

	if rio.preConnectAudio == nil {
		t.Fatal("preConnectAudio = nil, want handler")
	}
	if rio.preConnectAudio.timeout != 750*time.Millisecond {
		t.Fatalf("pre-connect audio timeout = %v, want 750ms", rio.preConnectAudio.timeout)
	}
}

func TestNewRoomIOCanDisablePreConnectAudio(t *testing.T) {
	rio := NewRoomIO(&lksdk.Room{}, &agent.AgentSession{}, RoomOptions{
		DisablePreConnectAudio: true,
	})

	if rio.preConnectAudio != nil {
		t.Fatalf("preConnectAudio = %#v, want nil when disabled", rio.preConnectAudio)
	}
}

func TestNewRoomIOCanDisableAudioInput(t *testing.T) {
	rio := NewRoomIO(&lksdk.Room{}, &agent.AgentSession{}, RoomOptions{
		DisableAudioInput: true,
	})

	if rio.preConnectAudio != nil {
		t.Fatalf("preConnectAudio = %#v, want nil when audio input disabled", rio.preConnectAudio)
	}
}

func TestRoomIOInputFrameUsesReferenceSampleRate(t *testing.T) {
	pcm := make([]byte, 960)
	frame := roomIOInputFrameFromPCM(pcm, roomIOOpusClockRate, 1)

	if frame.SampleRate != 24000 {
		t.Fatalf("SampleRate = %d, want reference RoomIO input rate 24000", frame.SampleRate)
	}
	if frame.NumChannels != 1 {
		t.Fatalf("NumChannels = %d, want mono reference input", frame.NumChannels)
	}
	if frame.SamplesPerChannel != 240 {
		t.Fatalf("SamplesPerChannel = %d, want 240 after 48k->24k resample", frame.SamplesPerChannel)
	}
	if got, want := len(frame.Data), int(frame.SamplesPerChannel*frame.NumChannels*2); got != want {
		t.Fatalf("frame data bytes = %d, want %d", got, want)
	}
}

func TestRoomIOInputStreamUsesReferenceFrameSize(t *testing.T) {
	stream := newRoomIOInputAudioStream()
	var frames []*model.AudioFrame
	for i := 0; i < 5; i++ {
		frame := roomIOInputFrameFromPCM(make([]byte, 960), roomIOOpusClockRate, 1)
		frames = append(frames, stream.Push(frame.Data)...)
	}

	if len(frames) != 1 {
		t.Fatalf("frames = %d, want one 50ms RoomIO input frame", len(frames))
	}
	if frames[0].SampleRate != 24000 {
		t.Fatalf("SampleRate = %d, want 24000", frames[0].SampleRate)
	}
	if frames[0].NumChannels != 1 {
		t.Fatalf("NumChannels = %d, want mono", frames[0].NumChannels)
	}
	if frames[0].SamplesPerChannel != 1200 {
		t.Fatalf("SamplesPerChannel = %d, want 1200 for 50ms at 24kHz", frames[0].SamplesPerChannel)
	}
	if got, want := len(frames[0].Data), int(frames[0].SamplesPerChannel*frames[0].NumChannels*2); got != want {
		t.Fatalf("frame data bytes = %d, want %d", got, want)
	}
}

func TestRoomIOInputFrameNormalizesPreConnectAudio(t *testing.T) {
	preConnect := &model.AudioFrame{
		Data:              make([]byte, 960),
		SampleRate:        roomIOOpusClockRate,
		NumChannels:       1,
		SamplesPerChannel: 480,
	}

	frame := roomIOInputFrameFromFrame(preConnect)

	if frame == preConnect {
		t.Fatal("normalized frame reused pre-connect frame pointer")
	}
	if frame.SampleRate != 24000 {
		t.Fatalf("SampleRate = %d, want reference RoomIO input rate 24000", frame.SampleRate)
	}
	if frame.NumChannels != 1 {
		t.Fatalf("NumChannels = %d, want mono reference input", frame.NumChannels)
	}
	if frame.SamplesPerChannel != 240 {
		t.Fatalf("SamplesPerChannel = %d, want 240 after 48k->24k resample", frame.SamplesPerChannel)
	}
	if got, want := len(frame.Data), int(frame.SamplesPerChannel*frame.NumChannels*2); got != want {
		t.Fatalf("frame data bytes = %d, want %d", got, want)
	}
}

func TestRoomIOInputSilenceFlushUsesReferenceDuration(t *testing.T) {
	frame := roomIOInputSilenceFlushFrame()

	if frame.SampleRate != 24000 {
		t.Fatalf("SampleRate = %d, want reference RoomIO input rate 24000", frame.SampleRate)
	}
	if frame.NumChannels != 1 {
		t.Fatalf("NumChannels = %d, want mono reference input", frame.NumChannels)
	}
	if frame.SamplesPerChannel != 12000 {
		t.Fatalf("SamplesPerChannel = %d, want 0.5s of 24 kHz silence", frame.SamplesPerChannel)
	}
	if got, want := len(frame.Data), int(frame.SamplesPerChannel*frame.NumChannels*2); got != want {
		t.Fatalf("frame data bytes = %d, want %d", got, want)
	}
	for i, b := range frame.Data {
		if b != 0 {
			t.Fatalf("frame data[%d] = %d, want silence", i, b)
		}
	}
}

func TestNewRoomIORegistersReferenceChatTextHandler(t *testing.T) {
	room := lksdk.NewRoom(nil)
	_ = NewRoomIO(room, &agent.AgentSession{}, RoomOptions{})

	err := room.RegisterTextStreamHandler(RoomIOChatTopic, func(*lksdk.TextStreamReader, string) {})
	if err == nil {
		t.Fatal("RegisterTextStreamHandler(lk.chat) error = nil, want already registered")
	}
}

func TestNewRoomIOCanDisableTextInput(t *testing.T) {
	room := lksdk.NewRoom(nil)
	_ = NewRoomIO(room, &agent.AgentSession{}, RoomOptions{
		DisableTextInput: true,
	})

	err := room.RegisterTextStreamHandler(RoomIOChatTopic, func(*lksdk.TextStreamReader, string) {})
	if err != nil {
		t.Fatalf("RegisterTextStreamHandler(lk.chat) error = %v, want nil when disabled", err)
	}
}

func TestRoomIOAttachRoomRegistersDeferredRoomHandlers(t *testing.T) {
	rio := NewRoomIO(nil, &agent.AgentSession{}, RoomOptions{})
	if rio.Room != nil {
		t.Fatal("Room = non-nil before AttachRoom, want deferred room binding")
	}
	if rio.preConnectAudio != nil {
		t.Fatal("preConnectAudio = non-nil before AttachRoom, want deferred handler registration")
	}

	room := lksdk.NewRoom(nil)
	rio.AttachRoom(room)
	defer rio.Close()

	if rio.Room != room {
		t.Fatal("RoomIO did not attach the room")
	}
	if rio.preConnectAudio == nil {
		t.Fatal("preConnectAudio = nil after AttachRoom, want registered pre-connect handler")
	}
	err := room.RegisterTextStreamHandler(RoomIOChatTopic, func(*lksdk.TextStreamReader, string) {})
	if err == nil {
		t.Fatal("RegisterTextStreamHandler(lk.chat) error = nil, want existing RoomIO chat handler")
	}
}

func TestRoomIOAttachRoomRegistersPreConnectAudioHandler(t *testing.T) {
	rio := NewRoomIO(nil, &agent.AgentSession{}, RoomOptions{DisableTextInput: true})
	room := lksdk.NewRoom(nil)

	err := room.RegisterByteStreamHandler(PreConnectAudioBufferStream, func(*lksdk.ByteStreamReader, string) {})
	if err != nil {
		t.Fatalf("RegisterByteStreamHandler before AttachRoom() error = %v", err)
	}
	room.UnregisterByteStreamHandler(PreConnectAudioBufferStream)

	rio.AttachRoom(room)
	t.Cleanup(func() {
		_ = rio.Close()
	})

	err = room.RegisterByteStreamHandler(PreConnectAudioBufferStream, func(*lksdk.ByteStreamReader, string) {})
	if err == nil {
		t.Fatal("RegisterByteStreamHandler after AttachRoom() error = nil, want existing pre-connect handler")
	}
}

func TestRoomIOAttachRoomSkipsPreConnectAudioHandlerWhenDisabled(t *testing.T) {
	rio := NewRoomIO(nil, &agent.AgentSession{}, RoomOptions{
		DisablePreConnectAudio: true,
		DisableTextInput:       true,
	})
	room := lksdk.NewRoom(nil)

	rio.AttachRoom(room)
	t.Cleanup(func() {
		_ = rio.Close()
	})

	err := room.RegisterByteStreamHandler(PreConnectAudioBufferStream, func(*lksdk.ByteStreamReader, string) {})
	if err != nil {
		t.Fatalf("RegisterByteStreamHandler after disabled AttachRoom() error = %v, want nil", err)
	}
}

func TestNewRoomIOCanDisableAudioOutput(t *testing.T) {
	assistant := &agent.PipelineAgent{}
	session := &agent.AgentSession{Assistant: assistant}

	_ = NewRoomIO(lksdk.NewRoom(nil), session, RoomOptions{
		DisableAudioOutput: true,
	})

	if assistant.PublishAudio != nil {
		t.Fatal("PublishAudio configured despite disabled room audio output")
	}
}

func TestRoomIOStartSkipsTrackWhenAudioOutputDisabled(t *testing.T) {
	rio := &RoomIO{Options: RoomOptions{DisableAudioOutput: true}}

	if err := rio.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v, want nil with disabled audio output", err)
	}
}

func TestRoomIOLocalTrackSubscriptionReleasesAudioOutput(t *testing.T) {
	track := newRoomIOTestAudioTrack(t)
	pub := lksdk.NewLocalTrackPublication(lksdk.TrackKindAudio, track, lksdk.TrackPublicationOptions{}, nil, nil)
	rio := &RoomIO{
		audioPublication: pub,
		audioSubscribed:  make(chan struct{}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- rio.waitForAudioSubscription(ctx)
	}()

	select {
	case err := <-done:
		t.Fatalf("waitForAudioSubscription returned before subscription: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	rio.GetCallback().OnLocalTrackSubscribed(pub, nil)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("waitForAudioSubscription error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waitForAudioSubscription did not return after local track subscription")
	}
}

func TestRoomIOBlocksUserAwayUntilAudioSubscribed(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{UserAwayTimeout: 0.01})
	rio := NewRoomIO(nil, session, RoomOptions{})
	rio.audioSubscribed = make(chan struct{})
	rio.audioSubOnce = sync.Once{}

	session.UpdateAgentState(agent.AgentStateListening)

	select {
	case ev := <-session.UserStateChangedCh:
		t.Fatalf("unexpected user state event before audio subscription = %q -> %q", ev.OldState, ev.NewState)
	case <-time.After(40 * time.Millisecond):
	}

	rio.markAudioSubscribed()

	select {
	case ev := <-session.UserStateChangedCh:
		if ev.OldState != agent.UserStateListening || ev.NewState != agent.UserStateAway {
			t.Fatalf("event states = %q -> %q, want listening -> away after audio subscription", ev.OldState, ev.NewState)
		}
	case <-time.After(time.Second):
		t.Fatal("UserStateChangedCh did not receive away event after audio subscription")
	}
}

func TestRoomIOBlocksUserAwayBeforeAudioOutputStarts(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{UserAwayTimeout: 0.01})
	_ = NewRoomIO(nil, session, RoomOptions{})

	session.UpdateAgentState(agent.AgentStateListening)

	select {
	case ev := <-session.UserStateChangedCh:
		t.Fatalf("unexpected user state before audio output subscription = %q -> %q", ev.OldState, ev.NewState)
	case <-time.After(40 * time.Millisecond):
	}
}

func TestRoomIOAudioSubscriptionTimeoutReleasesUserAwayGate(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{UserAwayTimeout: 0.01})
	rio := NewRoomIO(nil, session, RoomOptions{AudioSubscriptionTimeout: 20 * time.Millisecond})
	rio.audioSubscribed = make(chan struct{})
	rio.audioSubOnce = sync.Once{}

	session.UpdateAgentState(agent.AgentStateListening)

	select {
	case ev := <-session.UserStateChangedCh:
		t.Fatalf("unexpected user state before subscription timeout = %q -> %q", ev.OldState, ev.NewState)
	case <-time.After(15 * time.Millisecond):
	}

	if err := rio.waitForAudioSubscription(context.Background()); err != nil {
		t.Fatalf("waitForAudioSubscription error = %v", err)
	}

	select {
	case ev := <-session.UserStateChangedCh:
		if ev.OldState != agent.UserStateListening || ev.NewState != agent.UserStateAway {
			t.Fatalf("event states = %q -> %q, want listening -> away after subscription timeout", ev.OldState, ev.NewState)
		}
	case <-time.After(time.Second):
		t.Fatal("UserStateChangedCh did not receive away event after subscription timeout")
	}
}

func TestRoomIOAudioSubscriptionWaitFallsBackAfterTimeout(t *testing.T) {
	rio := &RoomIO{
		Options: RoomOptions{
			AudioSubscriptionTimeout: 20 * time.Millisecond,
		},
		audioSubscribed: make(chan struct{}),
	}

	started := time.Now()
	if err := rio.waitForAudioSubscription(context.Background()); err != nil {
		t.Fatalf("waitForAudioSubscription error = %v, want nil fallback after timeout", err)
	}
	if elapsed := time.Since(started); elapsed < 20*time.Millisecond {
		t.Fatalf("waitForAudioSubscription returned after %v, want timeout wait", elapsed)
	}
}

func TestRoomIOPublishAudioWaitsForSubscriptionBeforeEncoding(t *testing.T) {
	encoder := &recordingRoomIOEncoder{encoded: []byte{0x01, 0x02}}
	rio := &RoomIO{
		audioTrack:      newRoomIOTestAudioTrack(t),
		encoder:         encoder,
		audioSubscribed: make(chan struct{}),
	}

	frame := &model.AudioFrame{
		Data:              make([]byte, 960*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 960,
	}
	published := make(chan error, 1)
	go func() {
		published <- rio.PublishAudio(context.Background(), frame)
	}()

	select {
	case err := <-published:
		t.Fatalf("PublishAudio returned before subscription with error %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if len(encoder.calls) != 0 {
		t.Fatalf("encoder calls = %d, want 0 before audio subscription", len(encoder.calls))
	}

	rio.markAudioSubscribed()

	select {
	case err := <-published:
		if err != nil {
			t.Fatalf("PublishAudio after subscription error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("PublishAudio did not return after audio subscription")
	}
	if len(encoder.calls) == 0 {
		t.Fatal("encoder was not called after audio subscription")
	}
}

func TestRoomIOPublishAudioPendingWaiterSurvivesAudioStart(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	rio := NewRoomIO(nil, session, RoomOptions{})
	encoder := &recordingRoomIOEncoder{encoded: []byte{0x01, 0x02}}
	rio.mu.Lock()
	pending := rio.audioSubscribed
	rio.audioTrack = newRoomIOTestAudioTrack(t)
	rio.encoder = encoder
	rio.mu.Unlock()

	frame := &model.AudioFrame{
		Data:              make([]byte, 960*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 960,
	}
	done := make(chan error, 1)
	go func() {
		done <- rio.PublishAudio(context.Background(), frame)
	}()

	select {
	case err := <-done:
		t.Fatalf("PublishAudio returned before subscription ready: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	rio.setAudioOutputTrack(newRoomIOTestAudioTrack(t), "", nil)
	rio.markAudioSubscribed()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("PublishAudio error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("PublishAudio waiter did not unblock after audio start subscription")
	}
	if pending == nil {
		t.Fatal("pending subscription channel was nil")
	}
	if len(encoder.calls) != 1 {
		t.Fatalf("encoder calls = %d, want 1 after subscription", len(encoder.calls))
	}
}

func TestRoomIOPublishAudioBeforeTrackStartWaitsForSubscription(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	rio := NewRoomIO(nil, session, RoomOptions{})
	encoder := &recordingRoomIOEncoder{encoded: []byte{0x01, 0x02}}
	rio.mu.Lock()
	rio.encoder = encoder
	rio.mu.Unlock()

	frame := &model.AudioFrame{
		Data:              make([]byte, 960*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 960,
	}
	done := make(chan error, 1)
	go func() {
		done <- rio.PublishAudio(context.Background(), frame)
	}()

	select {
	case err := <-done:
		t.Fatalf("PublishAudio returned before audio output track start: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	rio.setAudioOutputTrack(newRoomIOTestAudioTrack(t), "", nil)
	rio.markAudioSubscribed()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("PublishAudio error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("PublishAudio did not unblock after audio output track start")
	}
	if len(encoder.calls) != 1 {
		t.Fatalf("encoder calls = %d, want 1 after track start subscription", len(encoder.calls))
	}
}

func TestRoomIOPublishAudioWaitForSubscriptionHonorsContext(t *testing.T) {
	encoder := &recordingRoomIOEncoder{encoded: []byte{0x01, 0x02}}
	rio := &RoomIO{
		audioTrack:      newRoomIOTestAudioTrack(t),
		encoder:         encoder,
		audioSubscribed: make(chan struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	frame := &model.AudioFrame{
		Data:              make([]byte, 960*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 960,
	}
	published := make(chan error, 1)
	go func() {
		published <- rio.PublishAudio(ctx, frame)
	}()

	select {
	case err := <-published:
		t.Fatalf("PublishAudio returned before cancellation with error %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	cancel()

	select {
	case err := <-published:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("PublishAudio error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("PublishAudio did not return after context cancellation")
	}
	if len(encoder.calls) != 0 {
		t.Fatalf("encoder calls = %d, want 0 when subscription wait is canceled", len(encoder.calls))
	}
}

func TestRoomIOPublishAudioSubscriptionWaitFallsBackAfterTimeout(t *testing.T) {
	encoder := &recordingRoomIOEncoder{encoded: []byte{0x01, 0x02}}
	rio := &RoomIO{
		Options: RoomOptions{
			AudioSubscriptionTimeout: 20 * time.Millisecond,
		},
		audioTrack:      newRoomIOTestAudioTrack(t),
		encoder:         encoder,
		audioSubscribed: make(chan struct{}),
	}

	frame := &model.AudioFrame{
		Data:              make([]byte, 960*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 960,
	}
	started := time.Now()
	if err := rio.PublishAudio(context.Background(), frame); err != nil {
		t.Fatalf("PublishAudio error = %v, want nil fallback after timeout", err)
	}
	if elapsed := time.Since(started); elapsed < 20*time.Millisecond {
		t.Fatalf("PublishAudio returned after %v, want bounded subscription wait", elapsed)
	}
	if len(encoder.calls) == 0 {
		t.Fatal("encoder was not called after subscription timeout fallback")
	}
}

func TestRoomIOPublishAudioSubscriptionTimeoutFallsBackOnce(t *testing.T) {
	encoder := &recordingRoomIOEncoder{encoded: []byte{0x01, 0x02}}
	rio := &RoomIO{
		Options: RoomOptions{
			AudioSubscriptionTimeout: 20 * time.Millisecond,
		},
		audioTrack:      newRoomIOTestAudioTrack(t),
		encoder:         encoder,
		audioSubscribed: make(chan struct{}),
	}

	frame := &model.AudioFrame{
		Data:              make([]byte, 960*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 960,
	}
	if err := rio.PublishAudio(context.Background(), frame); err != nil {
		t.Fatalf("PublishAudio(first) error = %v", err)
	}
	started := time.Now()
	if err := rio.PublishAudio(context.Background(), frame); err != nil {
		t.Fatalf("PublishAudio(second) error = %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 20*time.Millisecond {
		t.Fatalf("second PublishAudio waited %v, want subscription fallback reused", elapsed)
	}
}

func TestRoomIOAudioSubscriptionTimeoutReleasesConcurrentPublishWaiters(t *testing.T) {
	encoder := &recordingRoomIOEncoder{encoded: []byte{0x01, 0x02}}
	rio := &RoomIO{
		Options: RoomOptions{
			AudioSubscriptionTimeout: 40 * time.Millisecond,
		},
		audioTrack:      newRoomIOTestAudioTrack(t),
		encoder:         encoder,
		audioSubscribed: make(chan struct{}),
	}

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- rio.waitForAudioSubscription(context.Background())
	}()

	time.Sleep(20 * time.Millisecond)

	frame := &model.AudioFrame{
		Data:              make([]byte, 960*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 960,
	}
	publishDone := make(chan error, 1)
	go func() {
		publishDone <- rio.PublishAudio(context.Background(), frame)
	}()

	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("waitForAudioSubscription error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waitForAudioSubscription did not return after timeout fallback")
	}

	select {
	case err := <-publishDone:
		if err != nil {
			t.Fatalf("PublishAudio error = %v", err)
		}
	case <-time.After(10 * time.Millisecond):
		t.Fatal("PublishAudio waiter did not release with subscription timeout fallback")
	}
	if len(encoder.calls) == 0 {
		t.Fatal("encoder was not called after shared subscription timeout fallback")
	}
}

func TestRoomIOPublishAudioSubscriptionTimeoutReleasesUserAwayGate(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{UserAwayTimeout: 0.01})
	encoder := &recordingRoomIOEncoder{encoded: []byte{0x01, 0x02}}
	rio := &RoomIO{
		AgentSession: session,
		Options: RoomOptions{
			AudioSubscriptionTimeout: 20 * time.Millisecond,
		},
		audioTrack:      newRoomIOTestAudioTrack(t),
		encoder:         encoder,
		audioSubscribed: make(chan struct{}),
	}
	session.SetUserAwayTimerGate(rio.userAwayTimerBlocked)
	session.UpdateAgentState(agent.AgentStateListening)

	select {
	case ev := <-session.UserStateChangedCh:
		t.Fatalf("unexpected user state before publish fallback = %q -> %q", ev.OldState, ev.NewState)
	case <-time.After(15 * time.Millisecond):
	}

	frame := &model.AudioFrame{
		Data:              make([]byte, 960*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 960,
	}
	if err := rio.PublishAudio(context.Background(), frame); err != nil {
		t.Fatalf("PublishAudio error = %v", err)
	}

	select {
	case ev := <-session.UserStateChangedCh:
		if ev.OldState != agent.UserStateListening || ev.NewState != agent.UserStateAway {
			t.Fatalf("event states = %q -> %q, want listening -> away after publish subscription timeout", ev.OldState, ev.NewState)
		}
	case <-time.After(time.Second):
		t.Fatal("UserStateChangedCh did not receive away event after publish subscription timeout")
	}
}

func TestRoomIOPlaybackEventsFollowCaptureAndFlush(t *testing.T) {
	rio := &RoomIO{audioTrack: newRoomIOTestAudioTrack(t)}
	var started []PlaybackStartedEvent
	var finished []PlaybackFinishedEvent
	rio.OnPlaybackStarted(func(ev PlaybackStartedEvent) {
		started = append(started, ev)
	})
	rio.OnPlaybackFinished(func(ev PlaybackFinishedEvent) {
		finished = append(finished, ev)
	})
	frame := &model.AudioFrame{
		Data:              make([]byte, 960*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 960,
	}

	if err := rio.PublishAudio(context.Background(), frame); err != nil {
		t.Fatalf("PublishAudio error = %v", err)
	}
	if len(started) != 1 {
		t.Fatalf("playback_started events = %d, want 1", len(started))
	}
	if started[0].CreatedAt.IsZero() {
		t.Fatal("playback_started CreatedAt is zero")
	}
	done := make(chan PlaybackFinishedEvent, 1)
	errCh := make(chan error, 1)
	go func() {
		ev, err := rio.WaitForPlayout(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		done <- ev
	}()
	select {
	case ev := <-done:
		t.Fatalf("WaitForPlayout returned before Flush: %#v", ev)
	case err := <-errCh:
		t.Fatalf("WaitForPlayout error before Flush: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	rio.Flush()

	select {
	case err := <-errCh:
		t.Fatalf("WaitForPlayout error = %v", err)
	case ev := <-done:
		if ev.Interrupted {
			t.Fatal("PlaybackFinishedEvent.Interrupted = true, want false after Flush")
		}
		if ev.PlaybackPosition != 20*time.Millisecond {
			t.Fatalf("PlaybackPosition = %v, want 20ms", ev.PlaybackPosition)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitForPlayout did not return after Flush")
	}
	if len(finished) != 1 {
		t.Fatalf("playback_finished events = %d, want 1", len(finished))
	}
	if finished[0].Interrupted || finished[0].PlaybackPosition != 20*time.Millisecond {
		t.Fatalf("playback_finished event = %#v, want non-interrupted 20ms", finished[0])
	}
}

func TestRoomIOPlaybackFinishedIncludesAudioDiagnostics(t *testing.T) {
	rio := &RoomIO{audioTrack: newRoomIOTestAudioTrack(t)}
	frame := &model.AudioFrame{
		Data:              make([]byte, 480*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 480,
	}
	if err := rio.PublishAudio(context.Background(), frame); err != nil {
		t.Fatalf("PublishAudio(first) error = %v", err)
	}
	if err := rio.PublishAudio(context.Background(), frame); err != nil {
		t.Fatalf("PublishAudio(second) error = %v", err)
	}

	rio.Flush()
	ev, err := rio.WaitForPlayout(context.Background())
	if err != nil {
		t.Fatalf("WaitForPlayout error = %v", err)
	}
	if ev.AudioFrames != 2 {
		t.Fatalf("PlaybackFinishedEvent.AudioFrames = %d, want 2", ev.AudioFrames)
	}
	if ev.AudioBytes != len(frame.Data)*2 {
		t.Fatalf("PlaybackFinishedEvent.AudioBytes = %d, want %d", ev.AudioBytes, len(frame.Data)*2)
	}
	if ev.AudioSampleRate != frame.SampleRate {
		t.Fatalf("PlaybackFinishedEvent.AudioSampleRate = %d, want %d", ev.AudioSampleRate, frame.SampleRate)
	}
	if ev.AudioChannels != frame.NumChannels {
		t.Fatalf("PlaybackFinishedEvent.AudioChannels = %d, want %d", ev.AudioChannels, frame.NumChannels)
	}
}

func TestRoomIOPublishAudioRecordsMissingTrackDiagnostic(t *testing.T) {
	rio := &RoomIO{}
	frame := &model.AudioFrame{
		Data:              make([]byte, 160*2),
		SampleRate:        8000,
		NumChannels:       1,
		SamplesPerChannel: 160,
	}

	if err := rio.PublishAudio(context.Background(), frame); err != nil {
		t.Fatalf("PublishAudio without track error = %v, want nil diagnostic", err)
	}
	stats := rio.AudioOutputDiagnostics()
	if stats.FramesReceived != 1 {
		t.Fatalf("FramesReceived = %d, want 1", stats.FramesReceived)
	}
	if stats.FramesPublished != 0 {
		t.Fatalf("FramesPublished = %d, want 0 without track", stats.FramesPublished)
	}
	if stats.LastError == "" {
		t.Fatal("LastError empty, want missing-track diagnostic")
	}
	if stats.LastInputSampleRate != frame.SampleRate {
		t.Fatalf("LastInputSampleRate = %d, want %d", stats.LastInputSampleRate, frame.SampleRate)
	}
}

func TestRoomIOPublishAudioResamplesPCMToOpusClockRate(t *testing.T) {
	encoder := &recordingRoomIOEncoder{encoded: []byte{0x01, 0x02}}
	rio := &RoomIO{
		audioTrack: newRoomIOTestAudioTrack(t),
		encoder:    encoder,
	}
	frame := &model.AudioFrame{
		Data:              make([]byte, 160*2),
		SampleRate:        8000,
		NumChannels:       1,
		SamplesPerChannel: 160,
	}

	if err := rio.PublishAudio(context.Background(), frame); err != nil {
		t.Fatalf("PublishAudio error = %v", err)
	}

	if got, want := len(encoder.pcm), 960*2; got != want {
		t.Fatalf("encoder PCM length = %d, want %d bytes for 20ms at 48kHz", got, want)
	}
	if rio.playbackPosition != 20*time.Millisecond {
		t.Fatalf("playback position = %v, want original 20ms duration", rio.playbackPosition)
	}
	stats := rio.AudioOutputDiagnostics()
	if stats.LastInputSampleRate != 8000 {
		t.Fatalf("LastInputSampleRate = %d, want 8000", stats.LastInputSampleRate)
	}
	if stats.LastPublishedSampleRate != roomIOOpusClockRate {
		t.Fatalf("LastPublishedSampleRate = %d, want %d", stats.LastPublishedSampleRate, roomIOOpusClockRate)
	}
}

func TestRoomIOPublishAudioChunksLongPCMForOpus(t *testing.T) {
	encoder := &recordingRoomIOEncoder{encoded: []byte{0x01, 0x02}, maxPCMBytes: 960 * 2}
	rio := &RoomIO{
		audioTrack: newRoomIOTestAudioTrack(t),
		encoder:    encoder,
	}
	frame := &model.AudioFrame{
		Data:              make([]byte, 99108*2),
		SampleRate:        44100,
		NumChannels:       1,
		SamplesPerChannel: 99108,
	}

	if err := rio.PublishAudio(context.Background(), frame); err != nil {
		t.Fatalf("PublishAudio error = %v", err)
	}

	if len(encoder.calls) <= 1 {
		t.Fatalf("encoder calls = %d, want long PCM split into multiple Opus frames", len(encoder.calls))
	}
	for i, call := range encoder.calls {
		if len(call) > 960*2 {
			t.Fatalf("encoder call %d PCM bytes = %d, want at most 20ms Opus frame", i, len(call))
		}
	}
	if encoder.calls[len(encoder.calls)-1] == nil {
		t.Fatal("last encoder call missing")
	}
}

func TestRoomIOPublishAudioHonorsCanceledContextBeforeEncoding(t *testing.T) {
	encoder := &recordingRoomIOEncoder{encoded: []byte{0x01, 0x02}}
	rio := &RoomIO{
		audioTrack: newRoomIOTestAudioTrack(t),
		encoder:    encoder,
	}
	frame := &model.AudioFrame{
		Data:              make([]byte, 960*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 960,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := rio.PublishAudio(ctx, frame)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PublishAudio error = %v, want context.Canceled", err)
	}
	if len(encoder.calls) != 0 {
		t.Fatalf("encoder calls = %d, want 0 after canceled context", len(encoder.calls))
	}
}

func TestRoomIOPauseAudioOutputDefersPublishUntilResume(t *testing.T) {
	encoder := &recordingRoomIOEncoder{encoded: []byte{0x01, 0x02}}
	rio := &RoomIO{
		audioTrack: newRoomIOTestAudioTrack(t),
		encoder:    encoder,
	}
	frame := &model.AudioFrame{
		Data:              make([]byte, 960*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 960,
	}

	rio.PauseAudioOutput()
	published := make(chan error, 1)
	go func() {
		published <- rio.PublishAudio(context.Background(), frame)
	}()

	select {
	case err := <-published:
		t.Fatalf("PublishAudio returned while paused with error %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if len(encoder.calls) != 0 {
		t.Fatalf("encoder calls = %d, want 0 while audio output is paused", len(encoder.calls))
	}

	rio.ResumeAudioOutput()

	select {
	case err := <-published:
		if err != nil {
			t.Fatalf("PublishAudio after resume error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("PublishAudio did not resume")
	}
	if len(encoder.calls) != 1 {
		t.Fatalf("encoder calls = %d, want 1 after resume", len(encoder.calls))
	}
}

func TestRoomIOClearBufferDropsPausedPublish(t *testing.T) {
	encoder := &recordingRoomIOEncoder{encoded: []byte{0x01, 0x02}}
	rio := &RoomIO{
		audioTrack: newRoomIOTestAudioTrack(t),
		encoder:    encoder,
	}
	frame := &model.AudioFrame{
		Data:              make([]byte, 960*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 960,
	}

	rio.PauseAudioOutput()
	published := make(chan error, 1)
	go func() {
		published <- rio.PublishAudio(context.Background(), frame)
	}()

	select {
	case err := <-published:
		t.Fatalf("PublishAudio returned while paused with error %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	rio.ClearBuffer()

	select {
	case err := <-published:
		if err != nil {
			t.Fatalf("PublishAudio after ClearBuffer error = %v, want nil dropped publish", err)
		}
	case <-time.After(time.Second):
		t.Fatal("PublishAudio did not unblock after ClearBuffer")
	}
	if len(encoder.calls) != 0 {
		t.Fatalf("encoder calls = %d, want 0 after ClearBuffer drops paused publish", len(encoder.calls))
	}
}

func TestRoomIOClearBufferFinishesPlaybackAsInterrupted(t *testing.T) {
	rio := &RoomIO{audioTrack: newRoomIOTestAudioTrack(t)}
	frame := &model.AudioFrame{
		Data:              make([]byte, 480*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 480,
	}
	if err := rio.PublishAudio(context.Background(), frame); err != nil {
		t.Fatalf("PublishAudio error = %v", err)
	}

	rio.ClearBuffer()

	ev, err := rio.WaitForPlayout(context.Background())
	if err != nil {
		t.Fatalf("WaitForPlayout error = %v", err)
	}
	if !ev.Interrupted {
		t.Fatal("PlaybackFinishedEvent.Interrupted = false, want true after ClearBuffer")
	}
	if ev.PlaybackPosition >= 10*time.Millisecond {
		t.Fatalf("PlaybackPosition = %v, want less than full pushed duration after ClearBuffer", ev.PlaybackPosition)
	}
}

func TestRoomIOClearBufferReportsOnlyPlayedAudioPosition(t *testing.T) {
	rio := &RoomIO{audioTrack: newRoomIOTestAudioTrack(t)}
	frame := &model.AudioFrame{
		Data:              make([]byte, 48000*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 48000,
	}
	if err := rio.PublishAudio(context.Background(), frame); err != nil {
		t.Fatalf("PublishAudio error = %v", err)
	}

	rio.ClearBuffer()

	ev, err := rio.WaitForPlayout(context.Background())
	if err != nil {
		t.Fatalf("WaitForPlayout error = %v", err)
	}
	if !ev.Interrupted {
		t.Fatal("PlaybackFinishedEvent.Interrupted = false, want true after ClearBuffer")
	}
	if ev.PlaybackPosition >= 100*time.Millisecond {
		t.Fatalf("PlaybackPosition = %v, want only elapsed played audio after immediate ClearBuffer", ev.PlaybackPosition)
	}
}

func TestRoomIOPlaybackFinishedIncludesSynchronizedTranscript(t *testing.T) {
	rio := &RoomIO{audioTrack: newRoomIOTestAudioTrack(t)}
	frame := &model.AudioFrame{
		Data:              make([]byte, 480*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 480,
	}
	if err := rio.PublishAudio(context.Background(), frame); err != nil {
		t.Fatalf("PublishAudio error = %v", err)
	}
	rio.handleAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{
		Transcript: "hello",
		IsFinal:    false,
	})
	rio.handleAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{
		Transcript: " there",
		IsFinal:    false,
	})

	rio.ClearBuffer()

	ev, err := rio.WaitForPlayout(context.Background())
	if err != nil {
		t.Fatalf("WaitForPlayout error = %v", err)
	}
	if !ev.Interrupted {
		t.Fatal("PlaybackFinishedEvent.Interrupted = false, want true after ClearBuffer")
	}
	if ev.SynchronizedTranscript != "hello there" {
		t.Fatalf("SynchronizedTranscript = %q, want accumulated transcript", ev.SynchronizedTranscript)
	}
}

func TestRoomIOPlaybackStartedKeepsEarlySynchronizedTranscript(t *testing.T) {
	rio := &RoomIO{audioTrack: newRoomIOTestAudioTrack(t)}
	rio.handleAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{
		Transcript: "early transcript",
		IsFinal:    false,
	})
	frame := &model.AudioFrame{
		Data:              make([]byte, 480*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 480,
	}
	if err := rio.PublishAudio(context.Background(), frame); err != nil {
		t.Fatalf("PublishAudio error = %v", err)
	}

	rio.Flush()

	ev, err := rio.WaitForPlayout(context.Background())
	if err != nil {
		t.Fatalf("WaitForPlayout error = %v", err)
	}
	if ev.SynchronizedTranscript != "early transcript" {
		t.Fatalf("SynchronizedTranscript = %q, want transcript emitted before playback start", ev.SynchronizedTranscript)
	}
}

func TestRoomIOPlaybackFinishedDoesNotCarryLateTranscriptToNextSegment(t *testing.T) {
	rio := &RoomIO{audioTrack: newRoomIOTestAudioTrack(t)}
	frame := &model.AudioFrame{
		Data:              make([]byte, 480*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 480,
	}
	if err := rio.PublishAudio(context.Background(), frame); err != nil {
		t.Fatalf("PublishAudio(first) error = %v", err)
	}
	rio.Flush()
	if _, err := rio.WaitForPlayout(context.Background()); err != nil {
		t.Fatalf("WaitForPlayout(first) error = %v", err)
	}
	rio.handleAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{
		Transcript: "late transcript",
		IsFinal:    true,
	})
	if err := rio.PublishAudio(context.Background(), frame); err != nil {
		t.Fatalf("PublishAudio(second) error = %v", err)
	}

	rio.Flush()
	ev, err := rio.WaitForPlayout(context.Background())
	if err != nil {
		t.Fatalf("WaitForPlayout(second) error = %v", err)
	}
	if ev.SynchronizedTranscript != "" {
		t.Fatalf("SynchronizedTranscript = %q, want no late transcript carried to next segment", ev.SynchronizedTranscript)
	}
}

func TestRoomIOWaitForPlayoutCancellationRemovesWaiter(t *testing.T) {
	rio := &RoomIO{audioTrack: newRoomIOTestAudioTrack(t)}
	if err := rio.PublishAudio(context.Background(), &model.AudioFrame{
		Data:              make([]byte, 960*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 960,
	}); err != nil {
		t.Fatalf("PublishAudio error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	waitErr := make(chan error, 1)
	go func() {
		_, err := rio.WaitForPlayout(ctx)
		waitErr <- err
	}()

	waitForPlaybackWaiters(t, rio, 1)
	cancel()

	select {
	case err := <-waitErr:
		if err != context.Canceled {
			t.Fatalf("WaitForPlayout error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitForPlayout did not return after cancellation")
	}
	waitForPlaybackWaiters(t, rio, 0)
}

func TestRoomIOOffPlaybackStartedRemovesMatchingHandler(t *testing.T) {
	rio := &RoomIO{audioTrack: newRoomIOTestAudioTrack(t)}
	removed := make(chan PlaybackStartedEvent, 1)
	kept := make(chan PlaybackStartedEvent, 1)
	callback := func(ev PlaybackStartedEvent) {
		removed <- ev
	}
	rio.OnPlaybackStarted(callback)
	rio.OnPlaybackStarted(func(ev PlaybackStartedEvent) {
		kept <- ev
	})
	rio.OffPlaybackStarted(callback)

	if err := rio.PublishAudio(context.Background(), &model.AudioFrame{
		Data:              make([]byte, 960*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 960,
	}); err != nil {
		t.Fatalf("PublishAudio error = %v", err)
	}

	select {
	case ev := <-removed:
		t.Fatalf("removed playback_started handler received event: %#v", ev)
	default:
	}
	select {
	case ev := <-kept:
		if ev.CreatedAt.IsZero() {
			t.Fatal("kept playback_started CreatedAt is zero")
		}
	case <-time.After(time.Second):
		t.Fatal("remaining playback_started handler did not receive event")
	}
}

func TestRoomIOOffPlaybackStartedUsesCallbackIdentity(t *testing.T) {
	rio := &RoomIO{audioTrack: newRoomIOTestAudioTrack(t)}
	first := make(chan PlaybackStartedEvent, 1)
	second := make(chan PlaybackStartedEvent, 1)
	makeCallback := func(ch chan<- PlaybackStartedEvent) func(PlaybackStartedEvent) {
		return func(ev PlaybackStartedEvent) {
			ch <- ev
		}
	}
	firstCallback := makeCallback(first)
	secondCallback := makeCallback(second)

	rio.OnPlaybackStarted(firstCallback)
	rio.OnPlaybackStarted(secondCallback)
	rio.OffPlaybackStarted(secondCallback)

	if err := rio.PublishAudio(context.Background(), &model.AudioFrame{
		Data:              make([]byte, 960*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 960,
	}); err != nil {
		t.Fatalf("PublishAudio error = %v", err)
	}

	select {
	case ev := <-first:
		if ev.CreatedAt.IsZero() {
			t.Fatal("first playback_started CreatedAt is zero")
		}
	case <-time.After(time.Second):
		t.Fatal("first playback_started handler did not receive event")
	}
	select {
	case ev := <-second:
		t.Fatalf("removed second playback_started handler received event: %#v", ev)
	default:
	}
}

func TestRoomIOPlaybackStartedHandlerPanicDoesNotBlockOtherHandlers(t *testing.T) {
	rio := &RoomIO{audioTrack: newRoomIOTestAudioTrack(t)}
	kept := make(chan PlaybackStartedEvent, 1)
	rio.OnPlaybackStarted(func(PlaybackStartedEvent) {
		panic("playback started handler failed")
	})
	rio.OnPlaybackStarted(func(ev PlaybackStartedEvent) {
		kept <- ev
	})

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("PublishAudio panic = %v, want playback_started handler panic isolated", recovered)
		}
	}()

	if err := rio.PublishAudio(context.Background(), &model.AudioFrame{
		Data:              make([]byte, 960*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 960,
	}); err != nil {
		t.Fatalf("PublishAudio error = %v", err)
	}

	select {
	case ev := <-kept:
		if ev.CreatedAt.IsZero() {
			t.Fatal("remaining playback_started handler CreatedAt is zero")
		}
	case <-time.After(time.Second):
		t.Fatal("remaining playback_started handler did not receive event")
	}
}

func TestRoomIOOffPlaybackFinishedRemovesMatchingHandler(t *testing.T) {
	rio := &RoomIO{audioTrack: newRoomIOTestAudioTrack(t)}
	removed := make(chan PlaybackFinishedEvent, 1)
	kept := make(chan PlaybackFinishedEvent, 1)
	callback := func(ev PlaybackFinishedEvent) {
		removed <- ev
	}
	rio.OnPlaybackFinished(callback)
	rio.OnPlaybackFinished(func(ev PlaybackFinishedEvent) {
		kept <- ev
	})
	rio.OffPlaybackFinished(callback)

	if err := rio.PublishAudio(context.Background(), &model.AudioFrame{
		Data:              make([]byte, 960*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 960,
	}); err != nil {
		t.Fatalf("PublishAudio error = %v", err)
	}
	rio.Flush()

	select {
	case ev := <-removed:
		t.Fatalf("removed playback_finished handler received event: %#v", ev)
	default:
	}
	select {
	case ev := <-kept:
		if ev.Interrupted || ev.PlaybackPosition != 20*time.Millisecond {
			t.Fatalf("kept playback_finished event = %#v, want non-interrupted 20ms", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("remaining playback_finished handler did not receive event")
	}
}

func TestRoomIOPlaybackFinishedHandlerPanicDoesNotBlockOtherHandlers(t *testing.T) {
	rio := &RoomIO{audioTrack: newRoomIOTestAudioTrack(t)}
	kept := make(chan PlaybackFinishedEvent, 1)
	rio.OnPlaybackFinished(func(PlaybackFinishedEvent) {
		panic("playback finished handler failed")
	})
	rio.OnPlaybackFinished(func(ev PlaybackFinishedEvent) {
		kept <- ev
	})

	if err := rio.PublishAudio(context.Background(), &model.AudioFrame{
		Data:              make([]byte, 960*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 960,
	}); err != nil {
		t.Fatalf("PublishAudio error = %v", err)
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("Flush panic = %v, want playback_finished handler panic isolated", recovered)
		}
	}()

	rio.Flush()

	select {
	case ev := <-kept:
		if ev.Interrupted || ev.PlaybackPosition != 20*time.Millisecond {
			t.Fatalf("remaining playback_finished event = %#v, want non-interrupted 20ms", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("remaining playback_finished handler did not receive event")
	}
}

func TestNewRoomIOCreatesMultimodalAssistantWithRealtimeModel(t *testing.T) {
	session := &agent.AgentSession{
		ChatCtx:       llm.NewChatContext(),
		RealtimeModel: &fakeRoomIORealtimeModel{session: &fakeRoomIORealtimeSession{}},
	}

	_ = NewRoomIO(lksdk.NewRoom(nil), session, RoomOptions{})

	if _, ok := session.Assistant.(*agent.MultimodalAgent); !ok {
		t.Fatalf("session assistant = %T, want *agent.MultimodalAgent", session.Assistant)
	}
}

func newRoomIOTestAudioTrack(t *testing.T) *lksdk.LocalTrack {
	t.Helper()
	track, err := lksdk.NewLocalSampleTrack(webrtc.RTPCodecCapability{
		MimeType:  webrtc.MimeTypeOpus,
		ClockRate: 48000,
		Channels:  1,
	})
	if err != nil {
		t.Fatalf("NewLocalSampleTrack error = %v", err)
	}
	return track
}

type recordingRoomIOEncoder struct {
	pcm         []byte
	calls       [][]byte
	encoded     []byte
	maxPCMBytes int
}

func (e *recordingRoomIOEncoder) Encode(pcm []byte) ([]byte, error) {
	e.pcm = append([]byte(nil), pcm...)
	e.calls = append(e.calls, append([]byte(nil), pcm...))
	if e.maxPCMBytes > 0 && len(pcm) > e.maxPCMBytes {
		return nil, fmt.Errorf("pcm too large: %d", len(pcm))
	}
	return append([]byte(nil), e.encoded...), nil
}

func (e *recordingRoomIOEncoder) Close() error {
	return nil
}

func TestRoomIOCloseUnregistersChatTextHandler(t *testing.T) {
	room := lksdk.NewRoom(nil)
	rio := NewRoomIO(room, &agent.AgentSession{}, RoomOptions{})

	if err := rio.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	err := room.RegisterTextStreamHandler(RoomIOChatTopic, func(*lksdk.TextStreamReader, string) {})
	if err != nil {
		t.Fatalf("RegisterTextStreamHandler after RoomIO.Close() error = %v, want nil", err)
	}
}

func TestRoomIOCloseDropsPausedPublish(t *testing.T) {
	encoder := &recordingRoomIOEncoder{encoded: []byte{0x01, 0x02}}
	rio := &RoomIO{
		audioTrack: newRoomIOTestAudioTrack(t),
		encoder:    encoder,
	}
	frame := &model.AudioFrame{
		Data:              make([]byte, 960*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 960,
	}

	rio.PauseAudioOutput()
	published := make(chan error, 1)
	go func() {
		published <- rio.PublishAudio(context.Background(), frame)
	}()

	select {
	case err := <-published:
		t.Fatalf("PublishAudio returned while paused with error %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	if err := rio.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case err := <-published:
		if err != nil {
			t.Fatalf("PublishAudio after Close error = %v, want nil dropped publish", err)
		}
	case <-time.After(time.Second):
		t.Fatal("PublishAudio did not unblock after Close")
	}
	if len(encoder.calls) != 0 {
		t.Fatalf("encoder calls = %d, want 0 after Close drops paused publish", len(encoder.calls))
	}
}

func TestRoomIOCloseUnblocksPublishWaitingForSubscription(t *testing.T) {
	encoder := &recordingRoomIOEncoder{encoded: []byte{0x01, 0x02}}
	rio := &RoomIO{
		Options: RoomOptions{
			AudioSubscriptionTimeout: time.Hour,
		},
		audioTrack:      newRoomIOTestAudioTrack(t),
		encoder:         encoder,
		audioSubscribed: make(chan struct{}),
	}
	frame := &model.AudioFrame{
		Data:              make([]byte, 960*2),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 960,
	}

	published := make(chan error, 1)
	go func() {
		published <- rio.PublishAudio(context.Background(), frame)
	}()

	select {
	case err := <-published:
		t.Fatalf("PublishAudio returned before Close with error %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	if err := rio.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case err := <-published:
		if err != nil {
			t.Fatalf("PublishAudio after Close error = %v, want nil dropped publish", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("PublishAudio did not unblock after Close")
	}
	if len(encoder.calls) != 0 {
		t.Fatalf("encoder calls = %d, want dropped publish after Close", len(encoder.calls))
	}
}

func TestRoomIOCloseClearsSessionListeners(t *testing.T) {
	agentStateCancelled := make(chan struct{})
	userStateCancelled := make(chan struct{})
	userTranscriptionCancelled := make(chan struct{})
	agentTranscriptionCancelled := make(chan struct{})
	sessionCloseCancelled := make(chan struct{})
	rio := &RoomIO{
		agentStateCancel:         closeOnce(agentStateCancelled),
		userStateCancel:          closeOnce(userStateCancelled),
		userTranscriptionCancel:  closeOnce(userTranscriptionCancelled),
		agentTranscriptionCancel: closeOnce(agentTranscriptionCancelled),
		sessionCloseCancel:       closeOnce(sessionCloseCancelled),
	}

	if err := rio.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	assertClosed(t, agentStateCancelled, "agent state listener")
	assertClosed(t, userStateCancelled, "user state listener")
	assertClosed(t, userTranscriptionCancelled, "user transcription listener")
	assertClosed(t, agentTranscriptionCancelled, "agent transcription listener")
	assertClosed(t, sessionCloseCancelled, "session close listener")
	if rio.agentStateCancel != nil {
		t.Fatal("agentStateCancel still set after Close")
	}
	if rio.userStateCancel != nil {
		t.Fatal("userStateCancel still set after Close")
	}
	if rio.userTranscriptionCancel != nil {
		t.Fatal("userTranscriptionCancel still set after Close")
	}
	if rio.agentTranscriptionCancel != nil {
		t.Fatal("agentTranscriptionCancel still set after Close")
	}
	if rio.sessionCloseCancel != nil {
		t.Fatal("sessionCloseCancel still set after Close")
	}
}

func TestRoomIODefaultTextInputInterruptsBeforeGenerateReply(t *testing.T) {
	responder := &fakeRoomIOTextResponder{}

	if err := roomIODefaultTextInput(context.Background(), responder, "hello"); err != nil {
		t.Fatalf("roomIODefaultTextInput() error = %v", err)
	}

	want := []string{"claim-begin", "interrupt", "generate:hello", "claim-end"}
	if !reflect.DeepEqual(responder.calls, want) {
		t.Fatalf("calls = %#v, want %#v", responder.calls, want)
	}
}

func TestRoomIOHandleAgentStateChangedPublishesReferenceAttribute(t *testing.T) {
	var got map[string]string
	dispatcher := &fakeClientEventsDispatcher{}
	rio := &RoomIO{
		agentStatePublisher: func(attrs map[string]string) {
			got = attrs
		},
		agentStatePublishEnabled: func() bool {
			return true
		},
		clientEvents: dispatcher,
	}

	rio.handleAgentStateChanged(agent.AgentStateChangedEvent{NewState: agent.AgentStateThinking})

	if got[RoomIOAgentStateAttribute] != string(agent.AgentStateThinking) {
		t.Fatalf("published agent state attributes = %#v, want %s=%s", got, RoomIOAgentStateAttribute, agent.AgentStateThinking)
	}
	if len(dispatcher.agentStates) != 1 || dispatcher.agentStates[0] != agent.AgentStateThinking {
		t.Fatalf("dispatched agent states = %#v, want thinking", dispatcher.agentStates)
	}
}

func TestRoomIOHandleAgentStateChangedSkipsWhenRoomDisconnected(t *testing.T) {
	called := false
	dispatcher := &fakeClientEventsDispatcher{}
	rio := &RoomIO{
		agentStatePublisher: func(map[string]string) {
			called = true
		},
		agentStatePublishEnabled: func() bool {
			return false
		},
		clientEvents: dispatcher,
	}

	rio.handleAgentStateChanged(agent.AgentStateChangedEvent{NewState: agent.AgentStateSpeaking})

	if called {
		t.Fatal("agent state publisher was called while room was disconnected")
	}
	if len(dispatcher.agentStates) != 0 {
		t.Fatalf("dispatched agent states = %#v, want none while disconnected", dispatcher.agentStates)
	}
}

func TestRoomIOAgentStateListenerDoesNotConsumeLegacySessionChannel(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	published := make(chan map[string]string, 1)
	rio := &RoomIO{
		AgentSession: session,
		agentStatePublisher: func(attrs map[string]string) {
			published <- attrs
		},
		agentStatePublishEnabled: func() bool {
			return true
		},
	}
	rio.startAgentStateListener()
	t.Cleanup(func() {
		if rio.agentStateCancel != nil {
			rio.agentStateCancel()
		}
	})

	session.UpdateAgentState(agent.AgentStateThinking)

	select {
	case attrs := <-published:
		if attrs[RoomIOAgentStateAttribute] != string(agent.AgentStateThinking) {
			t.Fatalf("published agent state attributes = %#v, want %s=%s", attrs, RoomIOAgentStateAttribute, agent.AgentStateThinking)
		}
	case <-time.After(time.Second):
		t.Fatal("RoomIO did not publish the agent state change")
	}
	select {
	case ev := <-session.AgentStateChangedCh:
		if ev.NewState != agent.AgentStateThinking {
			t.Fatalf("legacy agent state event = %#v, want thinking", ev)
		}
	default:
		t.Fatal("RoomIO consumed the legacy agent state channel event")
	}
}

func TestRoomIOHandleUserStateChangedDispatchesClientEvent(t *testing.T) {
	dispatcher := &fakeClientEventsDispatcher{}
	rio := &RoomIO{
		agentStatePublishEnabled: func() bool {
			return true
		},
		clientEvents: dispatcher,
	}

	rio.handleUserStateChanged(agent.UserStateChangedEvent{NewState: agent.UserStateSpeaking})

	if len(dispatcher.userStates) != 1 || dispatcher.userStates[0] != agent.UserStateSpeaking {
		t.Fatalf("dispatched user states = %#v, want speaking", dispatcher.userStates)
	}
}

func TestRoomIOUserStateListenerDoesNotConsumeLegacySessionChannel(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	dispatcher := &channelClientEventsDispatcher{
		userStates: make(chan agent.UserState, 1),
	}
	rio := &RoomIO{
		AgentSession: session,
		agentStatePublishEnabled: func() bool {
			return true
		},
		clientEvents: dispatcher,
	}
	rio.startUserStateListener()
	t.Cleanup(func() {
		if rio.userStateCancel != nil {
			rio.userStateCancel()
		}
	})

	session.UpdateUserState(agent.UserStateSpeaking)

	select {
	case state := <-dispatcher.userStates:
		if state != agent.UserStateSpeaking {
			t.Fatalf("dispatched user state = %q, want speaking", state)
		}
	case <-time.After(time.Second):
		t.Fatal("RoomIO did not dispatch the user state change")
	}
	select {
	case ev := <-session.UserStateChangedCh:
		if ev.NewState != agent.UserStateSpeaking {
			t.Fatalf("legacy user state event = %#v, want speaking", ev)
		}
	default:
		t.Fatal("RoomIO consumed the legacy user state channel event")
	}
}

func TestRoomIOPublishesAgentOutputTranscriptionStream(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	published := make(chan roomIOPublishedText, 1)
	rio := &RoomIO{
		AgentSession: session,
		transcriptionTextPublisher: func(text string, opts lksdk.StreamTextOptions) {
			published <- roomIOPublishedText{text: text, opts: opts}
		},
	}
	rio.startAgentTranscriptionListener()
	defer rio.agentTranscriptionCancel()

	session.EmitAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{
		Transcript: "assistant transcript",
		IsFinal:    false,
		Language:   "en",
	})

	select {
	case got := <-published:
		if got.text != "assistant transcript" {
			t.Fatalf("published text = %q, want assistant transcript", got.text)
		}
		if got.opts.Topic != RoomIOTranscriptionTopic {
			t.Fatalf("published topic = %q, want %q", got.opts.Topic, RoomIOTranscriptionTopic)
		}
		if got.opts.Attributes[RoomIOTranscriptionFinalAttribute] != "false" {
			t.Fatalf("final attribute = %q, want false", got.opts.Attributes[RoomIOTranscriptionFinalAttribute])
		}
		segmentID := got.opts.Attributes[RoomIOTranscriptionSegmentIDAttribute]
		if !strings.HasPrefix(segmentID, "SG_") {
			t.Fatalf("segment id = %q, want SG_ prefix", segmentID)
		}
	case <-time.After(time.Second):
		t.Fatal("agent transcription stream was not published")
	}
}

func TestRoomIOReusesAgentTranscriptionSegmentUntilFinal(t *testing.T) {
	published := make(chan roomIOPublishedText, 3)
	rio := &RoomIO{
		transcriptionTextPublisher: func(text string, opts lksdk.StreamTextOptions) {
			published <- roomIOPublishedText{text: text, opts: opts}
		},
	}

	rio.handleAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{
		Transcript: "Halo,",
		IsFinal:    false,
		Language:   "id",
	})
	rio.handleAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{
		Transcript: " ada yang bisa saya bantu?",
		IsFinal:    false,
		Language:   "id",
	})
	rio.handleAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{
		Transcript: "Halo, ada yang bisa saya bantu?",
		IsFinal:    true,
		Language:   "id",
	})

	first := receivePublishedText(t, published, "first agent transcript")
	second := receivePublishedText(t, published, "second agent transcript")
	final := receivePublishedText(t, published, "final agent transcript")

	segmentID := first.opts.Attributes[RoomIOTranscriptionSegmentIDAttribute]
	if segmentID == "" {
		t.Fatal("first segment id is empty")
	}
	if second.opts.Attributes[RoomIOTranscriptionSegmentIDAttribute] != segmentID {
		t.Fatalf("second segment id = %q, want %q", second.opts.Attributes[RoomIOTranscriptionSegmentIDAttribute], segmentID)
	}
	if final.opts.Attributes[RoomIOTranscriptionSegmentIDAttribute] != segmentID {
		t.Fatalf("final segment id = %q, want %q", final.opts.Attributes[RoomIOTranscriptionSegmentIDAttribute], segmentID)
	}
	if final.opts.Attributes[RoomIOTranscriptionFinalAttribute] != "true" {
		t.Fatalf("final attribute = %q, want true", final.opts.Attributes[RoomIOTranscriptionFinalAttribute])
	}
	if second.text != "Halo, ada yang bisa saya bantu?" {
		t.Fatalf("second text = %q, want accumulated utterance", second.text)
	}
	if final.text != "Halo, ada yang bisa saya bantu?" {
		t.Fatalf("final text = %q, want full utterance", final.text)
	}
}

func TestRoomIOSpeechCreatedResetsAgentTranscriptionSegment(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	published := make(chan roomIOPublishedText, 4)
	rio := &RoomIO{
		AgentSession: session,
		transcriptionTextPublisher: func(text string, opts lksdk.StreamTextOptions) {
			published <- roomIOPublishedText{text: text, opts: opts}
		},
	}
	rio.startAgentTranscriptionListener()
	defer rio.agentTranscriptionCancel()

	// First speech: emit a delta (not final) to establish a segment ID.
	session.EmitAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{
		Transcript: "hello",
		IsFinal:    false,
	})
	first := receivePublishedText(t, published, "first delta")
	firstSegmentID := first.opts.Attributes[RoomIOTranscriptionSegmentIDAttribute]
	if firstSegmentID == "" {
		t.Fatal("first segment id must not be empty")
	}

	// New speech created before the first segment ends.
	session.EmitSpeechCreated(agent.SpeechCreatedEvent{
		SpeechHandle: agent.NewSpeechHandle(false, agent.DefaultInputDetails()),
		Source:       "say",
	})

	// Give the goroutine time to process SpeechCreated and reset state.
	time.Sleep(20 * time.Millisecond)

	// Second speech delta must use a new segment ID.
	session.EmitAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{
		Transcript: "world",
		IsFinal:    false,
	})
	second := receivePublishedText(t, published, "second delta after new speech")
	secondSegmentID := second.opts.Attributes[RoomIOTranscriptionSegmentIDAttribute]
	if secondSegmentID == "" {
		t.Fatal("second segment id must not be empty")
	}
	if firstSegmentID == secondSegmentID {
		t.Fatalf("segment id must reset on SpeechCreated: both = %q", firstSegmentID)
	}
}

func TestRoomIOPublishesAgentOutputTranscriptionTrackID(t *testing.T) {
	published := make(chan roomIOPublishedText, 1)
	rio := &RoomIO{
		audioTrackID: "TR_agent_audio",
		transcriptionTextPublisher: func(text string, opts lksdk.StreamTextOptions) {
			published <- roomIOPublishedText{text: text, opts: opts}
		},
	}

	rio.handleAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{
		Transcript: "assistant transcript",
		IsFinal:    true,
	})

	select {
	case got := <-published:
		if got.opts.Attributes[RoomIOTranscriptionTrackIDAttribute] != "TR_agent_audio" {
			t.Fatalf("track id attribute = %q, want TR_agent_audio", got.opts.Attributes[RoomIOTranscriptionTrackIDAttribute])
		}
	case <-time.After(time.Second):
		t.Fatal("agent transcription stream was not published")
	}
}

func TestRoomIOPublishesAgentOutputLegacyTranscriptionPacket(t *testing.T) {
	published := make(chan *livekit.Transcription, 1)
	rio := &RoomIO{
		audioTrackID: "TR_agent_audio",
		transcriptionParticipantIdentity: func() string {
			return "agent-local"
		},
		transcriptionPacketPublisher: func(transcription *livekit.Transcription) error {
			published <- transcription
			return nil
		},
	}

	rio.handleAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{
		Transcript: "assistant transcript",
		IsFinal:    true,
		Language:   "en",
	})

	select {
	case got := <-published:
		if got.TranscribedParticipantIdentity != "agent-local" {
			t.Fatalf("participant identity = %q, want agent-local", got.TranscribedParticipantIdentity)
		}
		if got.TrackId != "TR_agent_audio" {
			t.Fatalf("track id = %q, want TR_agent_audio", got.TrackId)
		}
		if len(got.Segments) != 1 {
			t.Fatalf("segments len = %d, want 1", len(got.Segments))
		}
		segment := got.Segments[0]
		if !strings.HasPrefix(segment.Id, "SG_") {
			t.Fatalf("segment id = %q, want SG_ prefix", segment.Id)
		}
		if segment.Text != "assistant transcript" || !segment.Final || segment.Language != "en" {
			t.Fatalf("segment = %#v, want final en assistant transcript", segment)
		}
	case <-time.After(time.Second):
		t.Fatal("legacy transcription packet was not published")
	}
}

func TestRoomIOPublishesUserInputLegacyTranscriptionPacket(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	published := make(chan *livekit.Transcription, 1)
	rio := &RoomIO{
		AgentSession:                   session,
		userTranscriptionTrackID:       "TR_user_audio",
		userTranscriptionParticipantID: "caller-a",
		transcriptionPacketPublisher: func(transcription *livekit.Transcription) error {
			published <- transcription
			return nil
		},
	}
	rio.startUserTranscriptionListener()
	defer rio.userTranscriptionCancel()

	session.EmitUserInputTranscribed(agent.UserInputTranscribedEvent{
		Transcript: "caller transcript",
		IsFinal:    true,
		Language:   "en",
	})

	select {
	case got := <-published:
		if got.TranscribedParticipantIdentity != "caller-a" {
			t.Fatalf("participant identity = %q, want caller-a", got.TranscribedParticipantIdentity)
		}
		if got.TrackId != "TR_user_audio" {
			t.Fatalf("track id = %q, want TR_user_audio", got.TrackId)
		}
		if len(got.Segments) != 1 {
			t.Fatalf("segments len = %d, want 1", len(got.Segments))
		}
		segment := got.Segments[0]
		if !strings.HasPrefix(segment.Id, "SG_") {
			t.Fatalf("segment id = %q, want SG_ prefix", segment.Id)
		}
		if segment.Text != "caller transcript" || !segment.Final || segment.Language != "en" {
			t.Fatalf("segment = %#v, want final en caller transcript", segment)
		}
	case <-time.After(time.Second):
		t.Fatal("user transcription packet was not published")
	}
}

func TestRoomIOSetParticipantClearsStaleUserTranscriptionTarget(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	published := make(chan *livekit.Transcription, 1)
	rio := &RoomIO{
		AgentSession:                   session,
		userTranscriptionTrackID:       "TR_user_audio",
		userTranscriptionParticipantID: "caller-a",
		transcriptionPacketPublisher: func(transcription *livekit.Transcription) error {
			published <- transcription
			return nil
		},
	}

	rio.SetParticipant("caller-b")
	rio.handleUserInputTranscribed(agent.UserInputTranscribedEvent{
		Transcript: "stale caller transcript",
		IsFinal:    true,
		Language:   "en",
	})

	select {
	case got := <-published:
		t.Fatalf("published user transcription with stale target: %#v", got)
	default:
	}
	if trackID, participantID := rio.userTranscriptionTarget(); trackID != "" || participantID != "" {
		t.Fatalf("user transcription target = (%q, %q), want cleared", trackID, participantID)
	}
}

func TestRoomIOUnsetParticipantClearsUserTranscriptionTarget(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	published := make(chan *livekit.Transcription, 1)
	rio := &RoomIO{
		AgentSession:                   session,
		userTranscriptionTrackID:       "TR_user_audio",
		userTranscriptionParticipantID: "caller-a",
		transcriptionPacketPublisher: func(transcription *livekit.Transcription) error {
			published <- transcription
			return nil
		},
	}

	rio.UnsetParticipant()
	rio.handleUserInputTranscribed(agent.UserInputTranscribedEvent{
		Transcript: "stale caller transcript",
		IsFinal:    true,
		Language:   "en",
	})

	select {
	case got := <-published:
		t.Fatalf("published user transcription with stale target: %#v", got)
	default:
	}
	if trackID, participantID := rio.userTranscriptionTarget(); trackID != "" || participantID != "" {
		t.Fatalf("user transcription target = (%q, %q), want cleared", trackID, participantID)
	}
}

func TestRoomIOParticipantDisconnectClearsUserTranscriptionTarget(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	published := make(chan *livekit.Transcription, 1)
	rio := &RoomIO{
		AgentSession:                   session,
		userTranscriptionTrackID:       "TR_user_audio",
		userTranscriptionParticipantID: "caller-a",
		Options: RoomOptions{
			ParticipantIdentity: "caller-a",
		},
		participantAvailable: true,
		transcriptionPacketPublisher: func(transcription *livekit.Transcription) error {
			published <- transcription
			return nil
		},
	}

	rio.handleParticipantDisconnected("caller-a", livekit.DisconnectReason_DUPLICATE_IDENTITY)
	rio.handleUserInputTranscribed(agent.UserInputTranscribedEvent{
		Transcript: "stale caller transcript",
		IsFinal:    true,
		Language:   "en",
	})

	select {
	case got := <-published:
		t.Fatalf("published user transcription with disconnected target: %#v", got)
	default:
	}
	if trackID, participantID := rio.userTranscriptionTarget(); trackID != "" || participantID != "" {
		t.Fatalf("user transcription target = (%q, %q), want cleared", trackID, participantID)
	}
}

func TestRoomIOPublishesUserInputTranscriptionStream(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	published := make(chan roomIOPublishedText, 1)
	rio := &RoomIO{
		AgentSession:                   session,
		userTranscriptionTrackID:       "TR_user_audio",
		userTranscriptionParticipantID: "caller-a",
		transcriptionTextPublisher: func(text string, opts lksdk.StreamTextOptions) {
			published <- roomIOPublishedText{text: text, opts: opts}
		},
	}
	rio.startUserTranscriptionListener()
	defer rio.userTranscriptionCancel()

	session.EmitUserInputTranscribed(agent.UserInputTranscribedEvent{
		Transcript: "caller transcript",
		IsFinal:    true,
		Language:   "en",
	})

	select {
	case got := <-published:
		if got.text != "caller transcript" {
			t.Fatalf("published text = %q, want caller transcript", got.text)
		}
		if got.opts.Topic != RoomIOTranscriptionTopic {
			t.Fatalf("published topic = %q, want %q", got.opts.Topic, RoomIOTranscriptionTopic)
		}
		if got.opts.Attributes[RoomIOTranscriptionFinalAttribute] != "true" {
			t.Fatalf("final attribute = %q, want true", got.opts.Attributes[RoomIOTranscriptionFinalAttribute])
		}
		if got.opts.Attributes[RoomIOTranscriptionTrackIDAttribute] != "TR_user_audio" {
			t.Fatalf("track id attribute = %q, want TR_user_audio", got.opts.Attributes[RoomIOTranscriptionTrackIDAttribute])
		}
		segmentID := got.opts.Attributes[RoomIOTranscriptionSegmentIDAttribute]
		if !strings.HasPrefix(segmentID, "SG_") {
			t.Fatalf("segment id = %q, want SG_ prefix", segmentID)
		}
	case <-time.After(time.Second):
		t.Fatal("user transcription stream was not published")
	}
}

func TestRoomIOReusesUserTranscriptionSegmentUntilFinal(t *testing.T) {
	published := make(chan roomIOPublishedText, 3)
	rio := &RoomIO{
		userTranscriptionTrackID:       "TR_user_audio",
		userTranscriptionParticipantID: "caller-a",
		transcriptionTextPublisher: func(text string, opts lksdk.StreamTextOptions) {
			published <- roomIOPublishedText{text: text, opts: opts}
		},
	}

	rio.handleUserInputTranscribed(agent.UserInputTranscribedEvent{
		Transcript: "halo",
		IsFinal:    false,
		Language:   "id",
	})
	rio.handleUserInputTranscribed(agent.UserInputTranscribedEvent{
		Transcript: "halo semua",
		IsFinal:    false,
		Language:   "id",
	})
	rio.handleUserInputTranscribed(agent.UserInputTranscribedEvent{
		Transcript: "halo semua apa kabar",
		IsFinal:    true,
		Language:   "id",
	})

	first := receivePublishedText(t, published, "first user transcript")
	second := receivePublishedText(t, published, "second user transcript")
	final := receivePublishedText(t, published, "final user transcript")

	segmentID := first.opts.Attributes[RoomIOTranscriptionSegmentIDAttribute]
	if segmentID == "" {
		t.Fatal("first segment id is empty")
	}
	if second.opts.Attributes[RoomIOTranscriptionSegmentIDAttribute] != segmentID {
		t.Fatalf("second segment id = %q, want %q (same as first)", second.opts.Attributes[RoomIOTranscriptionSegmentIDAttribute], segmentID)
	}
	if final.opts.Attributes[RoomIOTranscriptionSegmentIDAttribute] != segmentID {
		t.Fatalf("final segment id = %q, want %q (same as first)", final.opts.Attributes[RoomIOTranscriptionSegmentIDAttribute], segmentID)
	}
	if final.opts.Attributes[RoomIOTranscriptionFinalAttribute] != "true" {
		t.Fatalf("final attribute = %q, want true", final.opts.Attributes[RoomIOTranscriptionFinalAttribute])
	}
}

func TestRoomIOResetsUserTranscriptionSegmentAfterFinal(t *testing.T) {
	published := make(chan roomIOPublishedText, 2)
	rio := &RoomIO{
		userTranscriptionTrackID:       "TR_user_audio",
		userTranscriptionParticipantID: "caller-a",
		transcriptionTextPublisher: func(text string, opts lksdk.StreamTextOptions) {
			published <- roomIOPublishedText{text: text, opts: opts}
		},
	}

	rio.handleUserInputTranscribed(agent.UserInputTranscribedEvent{
		Transcript: "utterance one",
		IsFinal:    true,
		Language:   "id",
	})
	rio.handleUserInputTranscribed(agent.UserInputTranscribedEvent{
		Transcript: "utterance two",
		IsFinal:    true,
		Language:   "id",
	})

	first := receivePublishedText(t, published, "first utterance")
	second := receivePublishedText(t, published, "second utterance")

	firstID := first.opts.Attributes[RoomIOTranscriptionSegmentIDAttribute]
	secondID := second.opts.Attributes[RoomIOTranscriptionSegmentIDAttribute]
	if firstID == "" || secondID == "" {
		t.Fatalf("segment ids must not be empty: first=%q second=%q", firstID, secondID)
	}
	if firstID == secondID {
		t.Fatalf("segment id after final must reset: both = %q", firstID)
	}
}

func TestRoomIOSetParticipantResetsUserTranscriptionSegmentOnTargetChange(t *testing.T) {
	published := make(chan roomIOPublishedText, 2)
	rio := &RoomIO{
		userTranscriptionTrackID:       "TR_user_audio",
		userTranscriptionParticipantID: "caller-a",
		transcriptionTextPublisher: func(text string, opts lksdk.StreamTextOptions) {
			published <- roomIOPublishedText{text: text, opts: opts}
		},
	}

	rio.handleUserInputTranscribed(agent.UserInputTranscribedEvent{
		Transcript: "interim utterance",
		IsFinal:    false,
		Language:   "id",
	})
	interim := receivePublishedText(t, published, "interim utterance")
	interimID := interim.opts.Attributes[RoomIOTranscriptionSegmentIDAttribute]
	if interimID == "" {
		t.Fatal("interim segment id must not be empty")
	}

	rio.SetParticipant("caller-b")

	rio.mu.Lock()
	rio.userTranscriptionTrackID = "TR_user_audio_b"
	rio.userTranscriptionParticipantID = "caller-b"
	rio.mu.Unlock()

	rio.handleUserInputTranscribed(agent.UserInputTranscribedEvent{
		Transcript: "next utterance",
		IsFinal:    true,
		Language:   "id",
	})
	next := receivePublishedText(t, published, "next utterance after participant change")
	nextID := next.opts.Attributes[RoomIOTranscriptionSegmentIDAttribute]
	if nextID == "" {
		t.Fatal("next segment id must not be empty")
	}
	if nextID == interimID {
		t.Fatalf("segment id must reset after participant change: both = %q", nextID)
	}
}

func TestRoomIODisableAudioResetsUserTranscriptionSegment(t *testing.T) {
	published := make(chan roomIOPublishedText, 2)
	rio := &RoomIO{
		userTranscriptionTrackID:       "TR_user_audio",
		userTranscriptionParticipantID: "caller-a",
		transcriptionTextPublisher: func(text string, opts lksdk.StreamTextOptions) {
			published <- roomIOPublishedText{text: text, opts: opts}
		},
	}

	rio.handleUserInputTranscribed(agent.UserInputTranscribedEvent{
		Transcript: "interim utterance",
		IsFinal:    false,
		Language:   "id",
	})
	interim := receivePublishedText(t, published, "interim utterance")
	interimID := interim.opts.Attributes[RoomIOTranscriptionSegmentIDAttribute]
	if interimID == "" {
		t.Fatal("interim segment id must not be empty")
	}

	rio.disableAudioIOForSimulator()

	rio.mu.Lock()
	rio.userTranscriptionTrackID = "TR_user_audio_new"
	rio.userTranscriptionParticipantID = "caller-a"
	rio.mu.Unlock()

	rio.handleUserInputTranscribed(agent.UserInputTranscribedEvent{
		Transcript: "next utterance",
		IsFinal:    true,
		Language:   "id",
	})
	next := receivePublishedText(t, published, "next utterance after audio disabled")
	nextID := next.opts.Attributes[RoomIOTranscriptionSegmentIDAttribute]
	if nextID == "" {
		t.Fatal("next segment id must not be empty")
	}
	if nextID == interimID {
		t.Fatalf("segment id must reset after audio disabled: both = %q", nextID)
	}
}

func TestRoomIOClearTranscriptionTargetResetsUserTranscriptionSegment(t *testing.T) {
	published := make(chan roomIOPublishedText, 2)
	rio := &RoomIO{
		userTranscriptionTrackID:       "TR_user_audio",
		userTranscriptionParticipantID: "caller-a",
		transcriptionTextPublisher: func(text string, opts lksdk.StreamTextOptions) {
			published <- roomIOPublishedText{text: text, opts: opts}
		},
	}

	rio.handleUserInputTranscribed(agent.UserInputTranscribedEvent{
		Transcript: "interim utterance",
		IsFinal:    false,
		Language:   "id",
	})
	interim := receivePublishedText(t, published, "interim utterance")
	interimID := interim.opts.Attributes[RoomIOTranscriptionSegmentIDAttribute]
	if interimID == "" {
		t.Fatal("interim segment id must not be empty")
	}

	rio.clearUserTranscriptionTargetForParticipant("caller-a")

	rio.mu.Lock()
	rio.userTranscriptionTrackID = "TR_user_audio_new"
	rio.userTranscriptionParticipantID = "caller-a"
	rio.mu.Unlock()

	rio.handleUserInputTranscribed(agent.UserInputTranscribedEvent{
		Transcript: "next utterance",
		IsFinal:    true,
		Language:   "id",
	})
	next := receivePublishedText(t, published, "next utterance after target cleared")
	nextID := next.opts.Attributes[RoomIOTranscriptionSegmentIDAttribute]
	if nextID == "" {
		t.Fatal("next segment id must not be empty")
	}
	if nextID == interimID {
		t.Fatalf("segment id must reset after target cleared: both = %q", nextID)
	}
}

func TestRoomIOCanDisableAgentTranscriptionOutput(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	published := make(chan roomIOPublishedText, 1)
	rio := &RoomIO{
		AgentSession: session,
		Options: RoomOptions{
			DisableTranscriptionOutput: true,
		},
		transcriptionTextPublisher: func(text string, opts lksdk.StreamTextOptions) {
			published <- roomIOPublishedText{text: text, opts: opts}
		},
	}
	rio.startAgentTranscriptionListener()
	if rio.agentTranscriptionCancel != nil {
		defer rio.agentTranscriptionCancel()
	}

	session.EmitAgentOutputTranscribed(agent.AgentOutputTranscribedEvent{
		Transcript: "assistant transcript",
		IsFinal:    true,
	})

	select {
	case got := <-published:
		t.Fatalf("published agent transcription despite disabled output: %#v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRoomIOCanDisableUserTranscriptionOutput(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	published := make(chan roomIOPublishedText, 1)
	rio := &RoomIO{
		AgentSession:                   session,
		userTranscriptionTrackID:       "TR_user_audio",
		userTranscriptionParticipantID: "caller-a",
		Options: RoomOptions{
			DisableTranscriptionOutput: true,
		},
		transcriptionTextPublisher: func(text string, opts lksdk.StreamTextOptions) {
			published <- roomIOPublishedText{text: text, opts: opts}
		},
	}
	rio.startUserTranscriptionListener()
	if rio.userTranscriptionCancel != nil {
		defer rio.userTranscriptionCancel()
	}

	session.EmitUserInputTranscribed(agent.UserInputTranscribedEvent{
		Transcript: "caller transcript",
		IsFinal:    true,
	})

	select {
	case got := <-published:
		t.Fatalf("published user transcription despite disabled output: %#v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

type roomIOPublishedText struct {
	text string
	opts lksdk.StreamTextOptions
}

func TestRoomIOHandleAgentSessionCloseDeletesRoomWhenEnabled(t *testing.T) {
	calls := make(chan string, 2)
	rio := &RoomIO{
		Options: RoomOptions{
			DeleteRoomOnClose: true,
			DeleteRoom: func(_ context.Context, roomName string) error {
				calls <- roomName
				return nil
			},
		},
		roomName: func() string {
			return "room-a"
		},
	}

	rio.handleAgentSessionClose(agent.CloseEvent{Reason: agent.CloseReasonParticipantDisconnected})
	select {
	case gotRoomName := <-calls:
		if gotRoomName != "room-a" {
			t.Fatalf("DeleteRoom roomName = %q, want room-a", gotRoomName)
		}
	case <-time.After(time.Second):
		t.Fatal("DeleteRoom was not called")
	}
	waitForRoomDeleteIdle(t, rio)

	rio.handleAgentSessionClose(agent.CloseEvent{Reason: agent.CloseReasonParticipantDisconnected})
	select {
	case gotRoomName := <-calls:
		if gotRoomName != "room-a" {
			t.Fatalf("second DeleteRoom roomName = %q, want room-a", gotRoomName)
		}
	case <-time.After(time.Second):
		t.Fatal("second DeleteRoom was not called after first completed")
	}
}

func TestRoomIOHandleAgentSessionCloseIgnoresDeleteRoomNotFound(t *testing.T) {
	recorder := &roomIORecordingLogger{}
	oldLogger := logutil.Logger
	logutil.SetLogger(recorder)
	t.Cleanup(func() { logutil.SetLogger(oldLogger) })

	rio := &RoomIO{
		Options: RoomOptions{
			DeleteRoomOnClose: true,
			DeleteRoom: func(context.Context, string) error {
				return twirp.NewError(twirp.NotFound, "requested room does not exist")
			},
		},
		roomName: func() string {
			return "room-a"
		},
	}

	rio.handleAgentSessionClose(agent.CloseEvent{Reason: agent.CloseReasonParticipantDisconnected})
	waitForRoomDeleteIdle(t, rio)
	if len(recorder.warnMessages) != 0 {
		t.Fatalf("warn messages = %#v, want none for reference idempotent not_found room delete", recorder.warnMessages)
	}
}

func TestRoomIOHandleAgentSessionCloseWarnsOnDeleteRoomUnknownError(t *testing.T) {
	recorder := &roomIORecordingLogger{}
	oldLogger := logutil.Logger
	logutil.SetLogger(recorder)
	t.Cleanup(func() { logutil.SetLogger(oldLogger) })

	rio := &RoomIO{
		Options: RoomOptions{
			DeleteRoomOnClose: true,
			DeleteRoom: func(context.Context, string) error {
				return errors.New("boom")
			},
		},
		roomName: func() string {
			return "room-a"
		},
	}

	rio.handleAgentSessionClose(agent.CloseEvent{Reason: agent.CloseReasonParticipantDisconnected})
	waitForRoomDeleteIdle(t, rio)
	if !stringSliceContains(recorder.warnMessages, "failed to delete room on agent session close") {
		t.Fatalf("warn messages = %#v, want delete-room warning for non-not_found error", recorder.warnMessages)
	}
}

func waitForRoomDeleteIdle(t *testing.T, rio *RoomIO) {
	t.Helper()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		if !rio.isDeletingRoom() {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline:
			t.Fatal("deletingRoom was not cleared")
		}
	}
}

type roomIORecordingLogger struct {
	warnMessages []string
}

func (l *roomIORecordingLogger) Debugw(string, ...any) {}
func (l *roomIORecordingLogger) Infow(string, ...any)  {}
func (l *roomIORecordingLogger) Warnw(msg string, err error, keysAndValues ...any) {
	l.warnMessages = append(l.warnMessages, msg)
}
func (l *roomIORecordingLogger) Errorw(string, error, ...any) {}
func (l *roomIORecordingLogger) WithValues(keysAndValues ...any) livekitlogger.Logger {
	return l
}
func (l *roomIORecordingLogger) WithUnlikelyValues(keysAndValues ...any) livekitlogger.UnlikelyLogger {
	return livekitlogger.GetDiscardLogger().WithUnlikelyValues(keysAndValues...)
}
func (l *roomIORecordingLogger) WithName(name string) livekitlogger.Logger {
	return l
}
func (l *roomIORecordingLogger) WithComponent(component string) livekitlogger.Logger {
	return l
}
func (l *roomIORecordingLogger) WithCallDepth(depth int) livekitlogger.Logger {
	return l
}
func (l *roomIORecordingLogger) WithItemSampler() livekitlogger.Logger {
	return l
}
func (l *roomIORecordingLogger) WithoutSampler() livekitlogger.Logger {
	return l
}
func (l *roomIORecordingLogger) WithDeferredValues() (livekitlogger.Logger, livekitlogger.DeferredFieldResolver) {
	return livekitlogger.GetDiscardLogger().WithDeferredValues()
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestRoomIOHandleAgentSessionCloseDoesNotBlockOnRoomDelete(t *testing.T) {
	deleteStarted := make(chan struct{})
	releaseDelete := make(chan struct{})
	deleteDone := make(chan struct{})
	rio := &RoomIO{
		Options: RoomOptions{
			DeleteRoomOnClose: true,
			DeleteRoom: func(context.Context, string) error {
				close(deleteStarted)
				<-releaseDelete
				close(deleteDone)
				return nil
			},
		},
		roomName: func() string {
			return "room-a"
		},
	}

	returned := make(chan struct{})
	go func() {
		rio.handleAgentSessionClose(agent.CloseEvent{Reason: agent.CloseReasonParticipantDisconnected})
		close(returned)
	}()

	select {
	case <-deleteStarted:
	case <-time.After(time.Second):
		t.Fatal("DeleteRoom was not started")
	}
	select {
	case <-returned:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("handleAgentSessionClose blocked waiting for DeleteRoom")
	}
	if !rio.isDeletingRoom() {
		t.Fatal("deletingRoom = false while DeleteRoom is in flight")
	}

	close(releaseDelete)
	select {
	case <-deleteDone:
	case <-time.After(time.Second):
		t.Fatal("DeleteRoom did not finish after release")
	}
}

func TestRoomIOCloseWaitsForInFlightRoomDelete(t *testing.T) {
	deleteStarted := make(chan struct{})
	releaseDelete := make(chan struct{})
	rio := &RoomIO{
		Options: RoomOptions{
			DeleteRoomOnClose: true,
			DeleteRoom: func(context.Context, string) error {
				close(deleteStarted)
				<-releaseDelete
				return nil
			},
		},
		roomName: func() string {
			return "room-a"
		},
	}

	rio.handleAgentSessionClose(agent.CloseEvent{Reason: agent.CloseReasonParticipantDisconnected})
	select {
	case <-deleteStarted:
	case <-time.After(time.Second):
		t.Fatal("DeleteRoom was not started")
	}

	closeReturned := make(chan error, 1)
	go func() {
		closeReturned <- rio.Close()
	}()

	select {
	case err := <-closeReturned:
		t.Fatalf("Close() returned before DeleteRoom finished: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseDelete)
	select {
	case err := <-closeReturned:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() did not return after DeleteRoom finished")
	}
}

func TestRoomIOHandleAgentSessionCloseSkipsRoomDeleteWhenDisabled(t *testing.T) {
	called := false
	rio := &RoomIO{
		Options: RoomOptions{
			DeleteRoom: func(context.Context, string) error {
				called = true
				return nil
			},
		},
	}

	rio.handleAgentSessionClose(agent.CloseEvent{Reason: agent.CloseReasonParticipantDisconnected})

	if called {
		t.Fatal("DeleteRoom was called when DeleteRoomOnClose was disabled")
	}
}

func TestRoomIOHandleChatTextInputDispatchesConfiguredCallback(t *testing.T) {
	session := &agent.AgentSession{}
	var gotSession *agent.AgentSession
	var gotEvent TextInputEvent
	called := false
	rio := &RoomIO{
		AgentSession: session,
		textInput: func(_ context.Context, sess *agent.AgentSession, ev TextInputEvent) error {
			called = true
			gotSession = sess
			gotEvent = ev
			return nil
		},
	}

	rio.handleChatTextInput(context.Background(), "hello from chat", lksdk.TextStreamInfo{}, "caller")

	if !called {
		t.Fatal("text input callback was not called")
	}
	if gotSession != session {
		t.Fatal("text input callback received a different session")
	}
	if gotEvent.Text != "hello from chat" {
		t.Fatalf("TextInputEvent.Text = %q, want hello from chat", gotEvent.Text)
	}
	if gotEvent.ParticipantIdentity != "caller" {
		t.Fatalf("TextInputEvent.ParticipantIdentity = %q, want caller", gotEvent.ParticipantIdentity)
	}
}

func TestRoomIOHandleChatTextInputRecoversCallbackPanic(t *testing.T) {
	session := &agent.AgentSession{}
	rio := &RoomIO{
		AgentSession: session,
		textInput: func(context.Context, *agent.AgentSession, TextInputEvent) error {
			panic("text input callback panic")
		},
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("handleChatTextInput panic = %v, want recovered", recovered)
		}
	}()

	rio.handleChatTextInput(context.Background(), "hello from chat", lksdk.TextStreamInfo{}, "caller")
}

func TestRoomIOHandleChatTextInputIgnoresUnlinkedParticipant(t *testing.T) {
	session := &agent.AgentSession{}
	called := false
	rio := &RoomIO{
		AgentSession: session,
		Options: RoomOptions{
			ParticipantIdentity: "linked-user",
		},
		textInput: func(context.Context, *agent.AgentSession, TextInputEvent) error {
			called = true
			return nil
		},
	}

	rio.handleChatTextInput(context.Background(), "ignored", lksdk.TextStreamInfo{}, "other-user")

	if called {
		t.Fatal("text input callback was called for unlinked participant")
	}
}

func TestRoomIOHandleChatTextInputIgnoresUnknownParticipant(t *testing.T) {
	session := &agent.AgentSession{}
	called := false
	rio := &RoomIO{
		Room:         lksdk.NewRoom(nil),
		AgentSession: session,
		textInput: func(context.Context, *agent.AgentSession, TextInputEvent) error {
			called = true
			return nil
		},
	}

	rio.handleChatTextInput(context.Background(), "ignored", lksdk.TextStreamInfo{}, "missing-user")

	if called {
		t.Fatal("text input callback was called for unknown participant")
	}
}

func TestRoomIOSetParticipantSwitchesTextInputFilter(t *testing.T) {
	session := &agent.AgentSession{}
	var calls []string
	rio := &RoomIO{
		AgentSession: session,
		Options: RoomOptions{
			ParticipantIdentity: "caller-a",
		},
		textInput: func(_ context.Context, _ *agent.AgentSession, ev TextInputEvent) error {
			calls = append(calls, ev.ParticipantIdentity)
			return nil
		},
	}

	rio.handleChatTextInput(context.Background(), "ignored", lksdk.TextStreamInfo{}, "caller-b")
	rio.SetParticipant("caller-b")
	rio.handleChatTextInput(context.Background(), "accepted", lksdk.TextStreamInfo{}, "caller-b")
	rio.handleChatTextInput(context.Background(), "ignored", lksdk.TextStreamInfo{}, "caller-a")

	want := []string{"caller-b"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("text input calls = %#v, want %#v", calls, want)
	}
}

func TestRoomIOSetParticipantPreservesAvailableSameParticipant(t *testing.T) {
	session := &agent.AgentSession{}
	rio := &RoomIO{
		AgentSession: session,
	}
	if !rio.handleParticipantConnected("caller-a", lksdk.ParticipantStandard, nil, "agent-local") {
		t.Fatal("handleParticipantConnected(caller-a) = false, want true")
	}

	rio.SetParticipant("caller-a")
	rio.handleParticipantDisconnected("caller-a", livekit.DisconnectReason_CLIENT_INITIATED)

	select {
	case ev := <-session.CloseEvents():
		if ev.Reason != agent.CloseReasonParticipantDisconnected {
			t.Fatalf("CloseEvent.Reason = %q, want participant_disconnected", ev.Reason)
		}
	default:
		t.Fatal("session did not receive participant-disconnected close event")
	}
}

func TestRoomIOSetParticipantLinksAlreadyConnectedParticipant(t *testing.T) {
	session := &agent.AgentSession{}
	rio := &RoomIO{
		AgentSession: session,
	}
	if !rio.handleParticipantConnected("caller-a", lksdk.ParticipantStandard, nil, "agent-local") {
		t.Fatal("handleParticipantConnected(caller-a) = false, want true")
	}
	if rio.handleParticipantConnected("caller-b", lksdk.ParticipantStandard, nil, "agent-local") {
		t.Fatal("handleParticipantConnected(caller-b) = true, want false while caller-a is linked")
	}

	rio.SetParticipant("caller-b")
	rio.handleParticipantDisconnected("caller-b", livekit.DisconnectReason_CLIENT_INITIATED)

	select {
	case ev := <-session.CloseEvents():
		if ev.Reason != agent.CloseReasonParticipantDisconnected {
			t.Fatalf("CloseEvent.Reason = %q, want participant_disconnected", ev.Reason)
		}
	default:
		t.Fatal("session did not receive participant-disconnected close event")
	}
}

func TestRoomIOUnsetParticipantClearsTextInputFilter(t *testing.T) {
	session := &agent.AgentSession{}
	var calls []string
	rio := &RoomIO{
		AgentSession: session,
		Options: RoomOptions{
			ParticipantIdentity: "caller-a",
		},
		textInput: func(_ context.Context, _ *agent.AgentSession, ev TextInputEvent) error {
			calls = append(calls, ev.ParticipantIdentity)
			return nil
		},
	}

	rio.UnsetParticipant()
	rio.handleChatTextInput(context.Background(), "accepted", lksdk.TextStreamInfo{}, "caller-b")

	want := []string{"caller-b"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("text input calls = %#v, want %#v", calls, want)
	}
}

func TestRoomIOShouldHandleParticipantMatchesLinkedParticipant(t *testing.T) {
	rio := &RoomIO{Options: RoomOptions{ParticipantIdentity: "caller-a"}}

	if !rio.shouldHandleParticipant("caller-a") {
		t.Fatal("shouldHandleParticipant(caller-a) = false, want true for linked participant")
	}
	if rio.shouldHandleParticipant("caller-b") {
		t.Fatal("shouldHandleParticipant(caller-b) = true, want false for non-linked participant")
	}
}

func TestRoomIOLinkedParticipantReportsIdentityAndAvailability(t *testing.T) {
	rio := &RoomIO{Options: RoomOptions{ParticipantIdentity: "caller-a"}}

	identity, available := rio.LinkedParticipant()
	if identity != "caller-a" || available {
		t.Fatalf("LinkedParticipant() = (%q, %v), want configured unavailable participant", identity, available)
	}

	if !rio.handleParticipantConnected("caller-a", lksdk.ParticipantStandard, nil, "agent-local") {
		t.Fatal("handleParticipantConnected(caller-a) = false, want true")
	}
	identity, available = rio.LinkedParticipant()
	if identity != "caller-a" || !available {
		t.Fatalf("LinkedParticipant() after connect = (%q, %v), want available caller-a", identity, available)
	}

	rio.handleParticipantDisconnected("caller-a", livekit.DisconnectReason_DUPLICATE_IDENTITY)
	identity, available = rio.LinkedParticipant()
	if identity != "caller-a" || available {
		t.Fatalf("LinkedParticipant() after disconnect = (%q, %v), want unavailable caller-a", identity, available)
	}
}

func TestRoomIOShouldHandleParticipantAllowsAnyWhenUnset(t *testing.T) {
	rio := &RoomIO{}

	if !rio.shouldHandleParticipant("caller-b") {
		t.Fatal("shouldHandleParticipant(caller-b) = false, want true when participant is unset")
	}
}

func TestRoomIOShouldAcceptParticipantUsesReferenceDefaultKinds(t *testing.T) {
	rio := &RoomIO{}

	tests := []struct {
		name string
		kind lksdk.ParticipantKind
		want bool
	}{
		{"standard", lksdk.ParticipantStandard, true},
		{"sip", lksdk.ParticipantSIP, true},
		{"connector", lksdk.ParticipantConnector, true},
		{"agent", lksdk.ParticipantAgent, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rio.shouldAcceptParticipant("caller", tt.kind, nil, "agent-local"); got != tt.want {
				t.Fatalf("shouldAcceptParticipant(%s) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestRoomIOShouldAcceptParticipantUsesConfiguredKinds(t *testing.T) {
	rio := &RoomIO{Options: RoomOptions{
		ParticipantKinds: []lksdk.ParticipantKind{lksdk.ParticipantAgent},
	}}

	if !rio.shouldAcceptParticipant("agent-a", lksdk.ParticipantAgent, nil, "agent-local") {
		t.Fatal("shouldAcceptParticipant(agent) = false, want true for configured kind")
	}
	if rio.shouldAcceptParticipant("caller-a", lksdk.ParticipantSIP, nil, "agent-local") {
		t.Fatal("shouldAcceptParticipant(sip) = true, want false when SIP is not configured")
	}
}

func TestRoomIOShouldAcceptParticipantSkipsPublishOnBehalfWhenUnlinked(t *testing.T) {
	rio := &RoomIO{}

	if rio.shouldAcceptParticipant(
		"agent-output",
		lksdk.ParticipantStandard,
		map[string]string{RoomIOPublishOnBehalfAttribute: "agent-local"},
		"agent-local",
	) {
		t.Fatal("shouldAcceptParticipant(publish-on-behalf) = true, want false when participant is unlinked")
	}

	rio.SetParticipant("agent-output")
	if !rio.shouldAcceptParticipant(
		"agent-output",
		lksdk.ParticipantStandard,
		map[string]string{RoomIOPublishOnBehalfAttribute: "agent-local"},
		"agent-local",
	) {
		t.Fatal("shouldAcceptParticipant(linked publish-on-behalf) = false, want true for explicit linked participant")
	}
}

func TestRoomIOHandleParticipantConnectedLinksFirstAcceptedParticipant(t *testing.T) {
	rio := &RoomIO{}

	if !rio.handleParticipantConnected("caller-a", lksdk.ParticipantStandard, nil, "agent-local") {
		t.Fatal("handleParticipantConnected(caller-a) = false, want true for first accepted participant")
	}
	if got := rio.participantIdentity(); got != "caller-a" {
		t.Fatalf("participantIdentity() = %q, want caller-a", got)
	}
	if rio.shouldHandleParticipant("caller-b") {
		t.Fatal("shouldHandleParticipant(caller-b) = true, want false after linking first participant")
	}
}

func TestRoomIOHandleParticipantConnectedDisablesAudioForSimulator(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	session.UpdateUserState(agent.UserStateSpeaking)
	recorder := NewRecorderIO(session)
	recorder.started = true
	rio := &RoomIO{
		AgentSession:    session,
		Recorder:        recorder,
		preConnectAudio: &PreConnectAudioHandler{},
	}

	if !rio.handleParticipantConnected(
		"caller-a",
		lksdk.ParticipantStandard,
		map[string]string{RoomIOSimulatorAttribute: "true"},
		"agent-local",
	) {
		t.Fatal("handleParticipantConnected(simulator) = false, want true")
	}

	if got := rio.participantIdentity(); got != "caller-a" {
		t.Fatalf("participantIdentity() = %q, want caller-a", got)
	}
	if rio.preConnectAudio != nil {
		t.Fatal("preConnectAudio = non-nil, want disabled for simulator participant")
	}

	frame := &model.AudioFrame{
		Data:              []byte{0, 0},
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}
	if err := rio.PublishAudio(context.Background(), frame); err != nil {
		t.Fatalf("PublishAudio(simulator) error = %v", err)
	}
	if recorder.OutputStartTime != nil {
		t.Fatal("recorder output was recorded after simulator disabled audio output")
	}
	if got := session.UserState(); got != agent.UserStateListening {
		t.Fatalf("session UserState() = %q, want listening after simulator disabled audio", got)
	}
}

func TestRoomIOHandleParticipantConnectedSkipsUnacceptedParticipant(t *testing.T) {
	rio := &RoomIO{}

	if rio.handleParticipantConnected(
		"agent-output",
		lksdk.ParticipantStandard,
		map[string]string{RoomIOPublishOnBehalfAttribute: "agent-local"},
		"agent-local",
	) {
		t.Fatal("handleParticipantConnected(publish-on-behalf) = true, want false")
	}
	if got := rio.participantIdentity(); got != "" {
		t.Fatalf("participantIdentity() = %q, want empty", got)
	}
}

func TestRoomIOHandleParticipantDisconnectedClosesSessionForLinkedParticipant(t *testing.T) {
	session := &agent.AgentSession{}
	rio := &RoomIO{
		AgentSession: session,
		Options: RoomOptions{
			ParticipantIdentity: "caller-a",
		},
	}
	if !rio.handleParticipantConnected("caller-a", lksdk.ParticipantStandard, nil, "agent-local") {
		t.Fatal("handleParticipantConnected(caller-a) = false, want true")
	}

	rio.handleParticipantDisconnected("caller-a", livekit.DisconnectReason_CLIENT_INITIATED)

	select {
	case ev := <-session.CloseEvents():
		if ev.Reason != agent.CloseReasonParticipantDisconnected {
			t.Fatalf("CloseEvent.Reason = %q, want participant_disconnected", ev.Reason)
		}
	default:
		t.Fatal("session did not receive participant-disconnected close event")
	}
}

func TestRoomIOHandleParticipantDisconnectedIgnoresUnavailableConfiguredParticipant(t *testing.T) {
	session := &agent.AgentSession{}
	rio := &RoomIO{
		AgentSession: session,
		Options: RoomOptions{
			ParticipantIdentity: "caller-a",
		},
	}

	rio.handleParticipantDisconnected("caller-a", livekit.DisconnectReason_CLIENT_INITIATED)

	select {
	case ev := <-session.CloseEvents():
		t.Fatalf("unexpected close event before participant was linked: %#v", ev)
	default:
	}
}

func TestRoomIOHandleParticipantDisconnectedIgnoresUnlinkedParticipant(t *testing.T) {
	session := &agent.AgentSession{}
	rio := &RoomIO{
		AgentSession: session,
		Options: RoomOptions{
			ParticipantIdentity: "caller-a",
		},
	}

	rio.handleParticipantDisconnected("caller-b", livekit.DisconnectReason_CLIENT_INITIATED)

	select {
	case ev := <-session.CloseEvents():
		t.Fatalf("unexpected close event: %#v", ev)
	default:
	}
}

func TestRoomIOHandleParticipantDisconnectedCanBeDisabled(t *testing.T) {
	session := &agent.AgentSession{}
	rio := &RoomIO{
		AgentSession: session,
		Options: RoomOptions{
			ParticipantIdentity:      "caller-a",
			DisableCloseOnDisconnect: true,
		},
	}

	rio.handleParticipantDisconnected("caller-a", livekit.DisconnectReason_CLIENT_INITIATED)

	select {
	case ev := <-session.CloseEvents():
		t.Fatalf("unexpected close event: %#v", ev)
	default:
	}
}

func TestRoomIOHandleParticipantDisconnectedIgnoresNonCloseReasons(t *testing.T) {
	session := &agent.AgentSession{}
	rio := &RoomIO{
		AgentSession: session,
		Options: RoomOptions{
			ParticipantIdentity: "caller-a",
		},
	}

	rio.handleParticipantDisconnected("caller-a", livekit.DisconnectReason_DUPLICATE_IDENTITY)

	select {
	case ev := <-session.CloseEvents():
		t.Fatalf("unexpected close event: %#v", ev)
	default:
	}
}

func TestRoomIOHandleParticipantDisconnectedSkipsCloseWhileDeletingRoom(t *testing.T) {
	session := &agent.AgentSession{}
	rio := &RoomIO{
		AgentSession: session,
		Options: RoomOptions{
			ParticipantIdentity: "caller-a",
		},
		deletingRoom: true,
	}
	if !rio.handleParticipantConnected("caller-a", lksdk.ParticipantStandard, nil, "agent-local") {
		t.Fatal("handleParticipantConnected(caller-a) = false, want true")
	}

	rio.handleParticipantDisconnected("caller-a", livekit.DisconnectReason_CLIENT_INITIATED)

	select {
	case ev := <-session.CloseEvents():
		t.Fatalf("unexpected close event while deleting room: %#v", ev)
	default:
	}
}

func TestRoomIOHandleParticipantDisconnectedAllowsLinkedParticipantReconnect(t *testing.T) {
	rio := &RoomIO{}

	if !rio.handleParticipantConnected("caller-a", lksdk.ParticipantStandard, nil, "agent-local") {
		t.Fatal("handleParticipantConnected(caller-a) = false, want true for initial participant")
	}

	rio.handleParticipantDisconnected("caller-a", livekit.DisconnectReason_DUPLICATE_IDENTITY)

	if !rio.handleParticipantConnected("caller-a", lksdk.ParticipantStandard, nil, "agent-local") {
		t.Fatal("handleParticipantConnected(caller-a reconnect) = false, want true after linked participant disconnect")
	}
	if got := rio.participantIdentity(); got != "caller-a" {
		t.Fatalf("participantIdentity() = %q, want caller-a", got)
	}
}

func TestRoomIOCloseUnregistersPreConnectAudioHandler(t *testing.T) {
	room := lksdk.NewRoom(nil)
	rio := NewRoomIO(room, &agent.AgentSession{}, RoomOptions{})

	if err := rio.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	err := room.RegisterByteStreamHandler(PreConnectAudioBufferStream, func(*lksdk.ByteStreamReader, string) {})
	if err != nil {
		t.Fatalf("RegisterByteStreamHandler after RoomIO.Close() error = %v, want nil", err)
	}
}

func TestRoomIOCloseStopsRecorder(t *testing.T) {
	recorder := NewRecorderIO(&agent.AgentSession{})
	recorder.started = true
	rio := &RoomIO{Recorder: recorder}

	if err := rio.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if !recorder.closed {
		t.Fatal("recorder.closed = false, want RoomIO.Close to stop recorder")
	}
}

func TestRoomIOCallbackForwardsSipDTMFToSession(t *testing.T) {
	session := &agent.AgentSession{}
	rio := &RoomIO{AgentSession: session}
	cb := rio.GetCallback()

	cb.OnDataPacket(&livekit.SipDTMF{Digit: "#", Code: 11}, lksdk.DataReceiveParams{
		SenderIdentity: "caller",
	})

	select {
	case ev := <-session.SipDTMFEvents():
		if ev.Digit != "#" {
			t.Fatalf("SipDTMFEvent.Digit = %q, want #", ev.Digit)
		}
		if ev.Code != 11 {
			t.Fatalf("SipDTMFEvent.Code = %d, want 11", ev.Code)
		}
		if ev.SenderIdentity != "caller" {
			t.Fatalf("SipDTMFEvent.SenderIdentity = %q, want caller", ev.SenderIdentity)
		}
	default:
		t.Fatal("session did not receive SIP DTMF event")
	}
}

type fakeClientEventsDispatcher struct {
	agentStates []agent.AgentState
	userStates  []agent.UserState
}

func (f *fakeClientEventsDispatcher) DispatchAgentState(state agent.AgentState) {
	f.agentStates = append(f.agentStates, state)
}

func (f *fakeClientEventsDispatcher) DispatchUserState(state agent.UserState) {
	f.userStates = append(f.userStates, state)
}

type channelClientEventsDispatcher struct {
	agentStates chan agent.AgentState
	userStates  chan agent.UserState
}

func (c *channelClientEventsDispatcher) DispatchAgentState(state agent.AgentState) {
	c.agentStates <- state
}

func (c *channelClientEventsDispatcher) DispatchUserState(state agent.UserState) {
	c.userStates <- state
}

func closeOnce(ch chan struct{}) context.CancelFunc {
	var once sync.Once
	return func() {
		once.Do(func() {
			close(ch)
		})
	}
}

func assertClosed(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatalf("%s was not cancelled", label)
	}
}

func waitForPlaybackWaiters(t *testing.T, rio *RoomIO, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		rio.mu.Lock()
		got := len(rio.playbackWaiters)
		rio.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	rio.mu.Lock()
	got := len(rio.playbackWaiters)
	rio.mu.Unlock()
	t.Fatalf("playback waiters = %d, want %d", got, want)
}

func receivePublishedText(t *testing.T, ch <-chan roomIOPublishedText, label string) roomIOPublishedText {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(time.Second):
		t.Fatalf("%s was not published", label)
		return roomIOPublishedText{}
	}
}

type fakeRoomIORealtimeModel struct {
	session llm.RealtimeSession
}

func (f *fakeRoomIORealtimeModel) Capabilities() llm.RealtimeCapabilities {
	return llm.RealtimeCapabilities{}
}

func (f *fakeRoomIORealtimeModel) Session() (llm.RealtimeSession, error) {
	if f.session != nil {
		return f.session, nil
	}
	return &fakeRoomIORealtimeSession{}, nil
}

func (f *fakeRoomIORealtimeModel) Close() error { return nil }

type fakeRoomIORealtimeSession struct{}

func (f *fakeRoomIORealtimeSession) UpdateInstructions(string) error { return nil }

func (f *fakeRoomIORealtimeSession) UpdateChatContext(*llm.ChatContext) error { return nil }

func (f *fakeRoomIORealtimeSession) UpdateTools([]llm.Tool) error { return nil }

func (f *fakeRoomIORealtimeSession) UpdateOptions(llm.RealtimeSessionOptions) error { return nil }

func (f *fakeRoomIORealtimeSession) GenerateReply(llm.RealtimeGenerateReplyOptions) error {
	return nil
}

func (f *fakeRoomIORealtimeSession) Say(string) error { return nil }

func (f *fakeRoomIORealtimeSession) Truncate(llm.RealtimeTruncateOptions) error { return nil }

func (f *fakeRoomIORealtimeSession) Interrupt() error { return nil }

func (f *fakeRoomIORealtimeSession) Close() error { return nil }

func (f *fakeRoomIORealtimeSession) EventCh() <-chan llm.RealtimeEvent {
	ch := make(chan llm.RealtimeEvent)
	close(ch)
	return ch
}

func (f *fakeRoomIORealtimeSession) PushAudio(*model.AudioFrame) error { return nil }

func (f *fakeRoomIORealtimeSession) PushVideo(*images.VideoFrame) error { return nil }

func (f *fakeRoomIORealtimeSession) CommitAudio() error { return nil }

func (f *fakeRoomIORealtimeSession) ClearAudio() error { return nil }
