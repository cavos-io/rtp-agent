package worker

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

type fakeRoomIOTextResponder struct {
	calls []string
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

func TestRoomIODefaultTextInputInterruptsBeforeGenerateReply(t *testing.T) {
	responder := &fakeRoomIOTextResponder{}

	if err := roomIODefaultTextInput(context.Background(), responder, "hello"); err != nil {
		t.Fatalf("roomIODefaultTextInput() error = %v", err)
	}

	want := []string{"interrupt", "generate:hello"}
	if !reflect.DeepEqual(responder.calls, want) {
		t.Fatalf("calls = %#v, want %#v", responder.calls, want)
	}
}

func TestRoomIOHandleAgentStateChangedPublishesReferenceAttribute(t *testing.T) {
	var got map[string]string
	rio := &RoomIO{
		agentStatePublisher: func(attrs map[string]string) {
			got = attrs
		},
		agentStatePublishEnabled: func() bool {
			return true
		},
	}

	rio.handleAgentStateChanged(agent.AgentStateChangedEvent{NewState: agent.AgentStateThinking})

	if got[RoomIOAgentStateAttribute] != string(agent.AgentStateThinking) {
		t.Fatalf("published agent state attributes = %#v, want %s=%s", got, RoomIOAgentStateAttribute, agent.AgentStateThinking)
	}
}

func TestRoomIOHandleAgentStateChangedSkipsWhenRoomDisconnected(t *testing.T) {
	called := false
	rio := &RoomIO{
		agentStatePublisher: func(map[string]string) {
			called = true
		},
		agentStatePublishEnabled: func() bool {
			return false
		},
	}

	rio.handleAgentStateChanged(agent.AgentStateChangedEvent{NewState: agent.AgentStateSpeaking})

	if called {
		t.Fatal("agent state publisher was called while room was disconnected")
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
