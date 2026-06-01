package worker

import (
	"context"
	"testing"
	"time"

	"github.com/cavos-io/conversation-worker/core/agent"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

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
