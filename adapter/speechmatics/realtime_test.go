package speechmatics

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/utils/images"
	"github.com/gorilla/websocket"
)

func TestSpeechmaticsRealtimeModelRequiresAPIKey(t *testing.T) {
	t.Setenv(speechmaticsAPIKeyEnv, "")

	_, err := NewRealtimeModel("")

	if err == nil || !strings.Contains(err.Error(), speechmaticsAPIKeyEnv) {
		t.Fatalf("NewRealtimeModel error = %v, want missing API key error", err)
	}
}

func TestSpeechmaticsRealtimeModelMetadataAndCapabilities(t *testing.T) {
	t.Setenv(speechmaticsAPIKeyEnv, "env-key")

	rtModel, err := NewRealtimeModel("")
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	if rtModel.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", rtModel.apiKey)
	}
	if got := rtModel.Label(); got != "speechmatics.RealtimeModel" {
		t.Fatalf("Label() = %q, want speechmatics.RealtimeModel", got)
	}
	if got := rtModel.Model(); got != "flow" {
		t.Fatalf("Model() = %q, want flow", got)
	}
	if got := rtModel.Provider(); got != "Speechmatics" {
		t.Fatalf("Provider() = %q, want Speechmatics", got)
	}

	caps := rtModel.Capabilities()
	if !caps.TurnDetection || !caps.UserTranscription || !caps.AudioOutput || !caps.AutoToolReplyGeneration {
		t.Fatalf("capabilities = %#v, want full duplex voice model defaults", caps)
	}
	if caps.MessageTruncation || caps.ManualFunctionCalls || caps.PerResponseToolChoice {
		t.Fatalf("capabilities = %#v, want unsupported optional controls disabled", caps)
	}
	if !caps.MutableInstructions || !caps.MutableChatContext || !caps.MutableTools || !caps.SupportsSay {
		t.Fatalf("capabilities = %#v, want mutable instructions/context/tools and say support", caps)
	}
}

func TestSpeechmaticsRealtimeModelOptionsAndSessionSnapshot(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key",
		WithRealtimeWebsocketDisabled(),
		WithRealtimeBaseURL("wss://flow.example/v1"),
		WithRealtimeModel("flow-pro"),
		WithRealtimeVoice("theo"),
		WithRealtimeSystemPrompt("base"),
		WithRealtimeInputSampleRate(24000),
		WithRealtimeOutputSampleRate(48000),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	session, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	rtSession := session.(*speechmaticsRealtimeSession)

	if rtSession.baseURL != "wss://flow.example/v1" {
		t.Fatalf("session baseURL = %q, want snapshot", rtSession.baseURL)
	}
	if rtSession.model != "flow-pro" || rtSession.voice != "theo" || rtSession.instructions != "base" {
		t.Fatalf("session options = %q/%q/%q, want snapshot", rtSession.model, rtSession.voice, rtSession.instructions)
	}
	if rtSession.inputSampleRate != 24000 || rtSession.outputSampleRate != 48000 {
		t.Fatalf("session rates = %d/%d, want 24000/48000", rtSession.inputSampleRate, rtSession.outputSampleRate)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow-pro")
}

func TestSpeechmaticsRealtimeSessionConnectsAndRelaysWebsocketEvents(t *testing.T) {
	messages := make(chan map[string]any, 2)
	requests := make(chan *http.Request, 1)
	releaseServer := make(chan struct{})
	defer close(releaseServer)
	dialer := newSpeechmaticsRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, r *http.Request) {
		requests <- r
		for i := 0; i < 2; i++ {
			var message map[string]any
			if err := conn.ReadJSON(&message); err != nil {
				t.Errorf("ReadJSON client message error = %v", err)
				return
			}
			messages <- message
		}
		if err := conn.WriteJSON(map[string]any{"type": "input_audio_buffer.speech_started"}); err != nil {
			t.Errorf("WriteJSON server event error = %v", err)
			return
		}
		<-releaseServer
	})
	rtModel, err := NewRealtimeModel("test-key",
		WithRealtimeBaseURL("http://flow.example/v1"),
		WithRealtimeWebsocketDialer(dialer),
		WithRealtimeConnectOptions(llm.APIConnectOptions{MaxRetry: 0, Timeout: time.Second}),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	session, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	req := assertSpeechmaticsRealtimeRequest(t, requests)
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer key", got)
	}
	first := assertSpeechmaticsRealtimeOutboundJSON(t, messages)
	if first["type"] != "session.create" || first["model"] != "flow" {
		t.Fatalf("initial websocket message = %#v, want session.create flow", first)
	}
	if err := session.UpdateInstructions("socket update"); err != nil {
		t.Fatalf("UpdateInstructions error = %v", err)
	}
	second := assertSpeechmaticsRealtimeOutboundJSON(t, messages)
	if second["type"] != "session.update" || second["instructions"] != "socket update" {
		t.Fatalf("update websocket message = %#v, want instructions update", second)
	}
	assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeSpeechStarted)
}

func TestSpeechmaticsRealtimeSessionInitialSendFailureReturnsConnectionError(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key",
		WithRealtimeBaseURL("http://flow.example/v1"),
		WithRealtimeWebsocketDialer(newSpeechmaticsRealtimeWriteFailAfterDialer(t, errors.New("write refused"))),
		WithRealtimeConnectOptions(llm.APIConnectOptions{MaxRetry: 0, Timeout: time.Second}),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	_, err = rtModel.Session()

	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Session error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(connectionErr.Error(), "failed to initialize Speechmatics realtime session") ||
		!strings.Contains(connectionErr.Error(), "write refused") {
		t.Fatalf("APIConnectionError = %q, want initial send context", connectionErr.Error())
	}
}

func TestSpeechmaticsRealtimeSessionReconnectsAfterProviderClose(t *testing.T) {
	messages := make(chan map[string]any, 3)
	dialCount := atomic.Int32{}
	releaseSecond := make(chan struct{})
	defer close(releaseSecond)
	dialer := newSpeechmaticsRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
		dial := dialCount.Add(1)
		var initial map[string]any
		if err := conn.ReadJSON(&initial); err != nil {
			t.Errorf("ReadJSON initial message error = %v", err)
			return
		}
		messages <- initial
		if dial == 1 {
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, "recycle"), time.Now().Add(time.Second))
			_ = conn.Close()
			return
		}
		var update map[string]any
		if err := conn.ReadJSON(&update); err != nil {
			t.Errorf("ReadJSON update message error = %v", err)
			return
		}
		messages <- update
		<-releaseSecond
	})
	rtModel, err := NewRealtimeModel("test-key",
		WithRealtimeBaseURL("http://flow.example/v1"),
		WithRealtimeWebsocketDialer(dialer),
		WithRealtimeConnectOptions(llm.APIConnectOptions{MaxRetry: 0, Timeout: time.Second}),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}

	session, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	first := assertSpeechmaticsRealtimeOutboundJSON(t, messages)
	if first["type"] != "session.create" {
		t.Fatalf("first initial message = %#v, want session.create", first)
	}
	assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeSessionReconnected)
	second := assertSpeechmaticsRealtimeOutboundJSON(t, messages)
	if second["type"] != "session.create" {
		t.Fatalf("reconnect initial message = %#v, want session.create", second)
	}
	if err := session.UpdateInstructions("after reconnect"); err != nil {
		t.Fatalf("UpdateInstructions error = %v", err)
	}
	update := assertSpeechmaticsRealtimeOutboundJSON(t, messages)
	if update["type"] != "session.update" || update["instructions"] != "after reconnect" {
		t.Fatalf("post-reconnect update = %#v, want instructions update", update)
	}
}

func TestSpeechmaticsRealtimeSessionReconnectReplaysTools(t *testing.T) {
	messages := make(chan map[string]any, 4)
	dialCount := atomic.Int32{}
	releaseSecond := make(chan struct{})
	defer close(releaseSecond)
	dialer := newSpeechmaticsRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
		dial := dialCount.Add(1)
		var initial map[string]any
		if err := conn.ReadJSON(&initial); err != nil {
			t.Errorf("ReadJSON initial message error = %v", err)
			return
		}
		messages <- initial
		var tools map[string]any
		if err := conn.ReadJSON(&tools); err != nil {
			t.Errorf("ReadJSON tools message error = %v", err)
			return
		}
		messages <- tools
		if dial == 1 {
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, "recycle"), time.Now().Add(time.Second))
			_ = conn.Close()
			return
		}
		<-releaseSecond
	})
	rtModel, err := NewRealtimeModel("test-key",
		WithRealtimeBaseURL("http://flow.example/v1"),
		WithRealtimeWebsocketDialer(dialer),
		WithRealtimeConnectOptions(llm.APIConnectOptions{MaxRetry: 0, Timeout: time.Second}),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	first := assertSpeechmaticsRealtimeOutboundJSON(t, messages)
	if first["type"] != "session.create" {
		t.Fatalf("first initial = %#v, want session.create", first)
	}
	if err := session.UpdateTools([]llm.Tool{speechmaticsRealtimeTestTool{
		name:        "lookup_weather",
		description: "look up weather",
		parameters:  map[string]any{"type": "object"},
	}}); err != nil {
		t.Fatalf("UpdateTools error = %v", err)
	}
	firstTools := assertSpeechmaticsRealtimeOutboundJSON(t, messages)
	assertSpeechmaticsRealtimeToolsUpdate(t, firstTools, "lookup_weather")
	assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeSessionReconnected)
	second := assertSpeechmaticsRealtimeOutboundJSON(t, messages)
	if second["type"] != "session.create" {
		t.Fatalf("reconnect initial = %#v, want session.create", second)
	}
	replayedTools := assertSpeechmaticsRealtimeOutboundJSON(t, messages)
	assertSpeechmaticsRealtimeToolsUpdate(t, replayedTools, "lookup_weather")
}

func TestSpeechmaticsRealtimeSessionReconnectReplaysChatContext(t *testing.T) {
	messages := make(chan map[string]any, 3)
	dialCount := atomic.Int32{}
	releaseFirstClose := make(chan struct{})
	releaseSecond := make(chan struct{})
	defer close(releaseSecond)
	dialer := newSpeechmaticsRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
		dial := dialCount.Add(1)
		var initial map[string]any
		if err := conn.ReadJSON(&initial); err != nil {
			t.Errorf("ReadJSON initial message error = %v", err)
			return
		}
		messages <- initial
		if dial == 1 {
			<-releaseFirstClose
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, "recycle"), time.Now().Add(time.Second))
			_ = conn.Close()
			return
		}
		var contextMessage map[string]any
		if err := conn.ReadJSON(&contextMessage); err != nil {
			t.Errorf("ReadJSON chat context message error = %v", err)
			return
		}
		messages <- contextMessage
		<-releaseSecond
	})
	rtModel, err := NewRealtimeModel("test-key",
		WithRealtimeBaseURL("http://flow.example/v1"),
		WithRealtimeWebsocketDialer(dialer),
		WithRealtimeConnectOptions(llm.APIConnectOptions{MaxRetry: 0, Timeout: time.Second}),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	first := assertSpeechmaticsRealtimeOutboundJSON(t, messages)
	if first["type"] != "session.create" {
		t.Fatalf("first initial = %#v, want session.create", first)
	}
	chatCtx := llm.NewChatContext()
	chatCtx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user-1", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}
	if err := session.UpdateChatContext(chatCtx); err != nil {
		t.Fatalf("UpdateChatContext error = %v", err)
	}
	close(releaseFirstClose)
	assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeSessionReconnected)
	second := assertSpeechmaticsRealtimeOutboundJSON(t, messages)
	if second["type"] != "session.create" {
		t.Fatalf("reconnect initial = %#v, want session.create", second)
	}
	replayedContext := assertSpeechmaticsRealtimeOutboundJSON(t, messages)
	assertSpeechmaticsRealtimeChatContextMessage(t, replayedContext, "user", "hello")
}

func TestSpeechmaticsRealtimeSessionSuppressesSendErrorWhenReconnectSucceeds(t *testing.T) {
	messages := make(chan map[string]any, 3)
	dialCount := atomic.Int32{}
	failConnCh := make(chan *speechmaticsRealtimeFailWriteConn, 1)
	releaseSecond := make(chan struct{})
	defer close(releaseSecond)
	dialer := newSpeechmaticsRealtimeTestWebsocketDialerWithConn(t,
		func(conn net.Conn, dial int) net.Conn {
			if dial != 1 {
				return conn
			}
			failConn := &speechmaticsRealtimeFailWriteConn{Conn: conn, err: errors.New("send refused")}
			failConnCh <- failConn
			return failConn
		},
		func(conn *websocket.Conn, _ *http.Request) {
			dial := dialCount.Add(1)
			var initial map[string]any
			if err := conn.ReadJSON(&initial); err != nil {
				t.Errorf("ReadJSON initial message error = %v", err)
				return
			}
			messages <- initial
			if dial == 1 {
				for {
					if _, _, err := conn.ReadMessage(); err != nil {
						return
					}
				}
			}
			<-releaseSecond
		},
	)
	rtModel, err := NewRealtimeModel("test-key",
		WithRealtimeBaseURL("http://flow.example/v1"),
		WithRealtimeWebsocketDialer(dialer),
		WithRealtimeConnectOptions(llm.APIConnectOptions{MaxRetry: 0, Timeout: time.Second}),
	)
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	defer session.Close()

	first := assertSpeechmaticsRealtimeOutboundJSON(t, messages)
	if first["type"] != "session.create" {
		t.Fatalf("first initial = %#v, want session.create", first)
	}
	failConn := <-failConnCh
	failConn.fail.Store(true)
	if err := session.UpdateInstructions("after reconnect"); err != nil {
		t.Fatalf("UpdateInstructions error = %v", err)
	}

	assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeSessionReconnected)
	second := assertSpeechmaticsRealtimeOutboundJSON(t, messages)
	if second["type"] != "session.create" || second["instructions"] != "after reconnect" {
		t.Fatalf("reconnect initial = %#v, want session.create with updated instructions", second)
	}
}

func TestSpeechmaticsRealtimeSessionIdleInterruptDoesNotCancel(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key", WithRealtimeWebsocketDisabled())
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt error = %v", err)
	}

	assertSpeechmaticsRealtimeNoCommand(t, session)
}

func TestSpeechmaticsRealtimeSessionPendingGenerateReplyIsInterruptible(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key", WithRealtimeWebsocketDisabled())
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*speechmaticsRealtimeSession)
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{Instructions: "answer now", InstructionsSet: true}); err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}
	create := nextSpeechmaticsRealtimeCommand(t, session)
	if create["type"] != "response.create" {
		t.Fatalf("command = %#v, want response.create", create)
	}
	metadata, _ := create["metadata"].(map[string]any)
	clientEventID, _ := metadata["client_event_id"].(string)
	if clientEventID == "" {
		t.Fatalf("metadata = %#v, want client_event_id", create["metadata"])
	}

	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "response.cancel", "", nil)

	if ok := session.handleServerEvent(map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id":       "resp_generate",
			"metadata": map[string]any{"client_event_id": clientEventID},
		},
	}); !ok {
		t.Fatal("response.created event ignored")
	}
	created := assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	if created.Generation == nil || !created.Generation.UserInitiated {
		t.Fatalf("generation = %#v, want user initiated", created.Generation)
	}
}

func TestSpeechmaticsRealtimeSessionPendingGenerateReplyExpires(t *testing.T) {
	previousTimeout := speechmaticsRealtimePendingResponseTimeout
	speechmaticsRealtimePendingResponseTimeout = 0
	t.Cleanup(func() { speechmaticsRealtimePendingResponseTimeout = previousTimeout })

	rtModel, err := NewRealtimeModel("test-key", WithRealtimeWebsocketDisabled())
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*speechmaticsRealtimeSession)
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{Instructions: "answer now", InstructionsSet: true}); err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "response.create", "type", "response.create")

	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt error = %v", err)
	}
	assertSpeechmaticsRealtimeNoCommand(t, session)
}

func TestSpeechmaticsRealtimeSessionTruncateAudioMatchesReference(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key", WithRealtimeWebsocketDisabled())
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	if err := session.Truncate(llm.RealtimeTruncateOptions{
		MessageID:      "msg_123",
		Modalities:     []string{"text", "audio"},
		AudioEndMillis: 1500,
	}); err != nil {
		t.Fatalf("Truncate error = %v", err)
	}

	command := nextSpeechmaticsRealtimeCommand(t, session)
	if command["type"] != "conversation.item.truncate" ||
		command["item_id"] != "msg_123" ||
		command["content_index"] != 0 ||
		command["audio_end_ms"] != 1500 {
		t.Fatalf("truncate command = %#v, want reference audio truncate", command)
	}
}

func TestSpeechmaticsRealtimeSessionTruncateTextRewritesRemoteMessage(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key", WithRealtimeWebsocketDisabled())
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*speechmaticsRealtimeSession)
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")
	if ok := session.handleServerEvent(map[string]any{
		"type": "conversation.item.added",
		"item": map[string]any{
			"id":   "msg_123",
			"type": "message",
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "output_text", "text": "full transcript"},
			},
		},
	}); !ok {
		t.Fatal("conversation.item.added ignored")
	}
	assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeRemoteItemAdded)

	transcript := "played transcript"
	if err := session.Truncate(llm.RealtimeTruncateOptions{
		MessageID:       "msg_123",
		Modalities:      []string{"text"},
		AudioTranscript: &transcript,
	}); err != nil {
		t.Fatalf("Truncate error = %v", err)
	}

	deleteCommand := nextSpeechmaticsRealtimeCommand(t, session)
	if deleteCommand["type"] != "conversation.item.delete" || deleteCommand["item_id"] != "msg_123" {
		t.Fatalf("delete command = %#v, want conversation.item.delete msg_123", deleteCommand)
	}
	createCommand := nextSpeechmaticsRealtimeCommand(t, session)
	assertSpeechmaticsRealtimeChatContextMessage(t, createCommand, "assistant", "played transcript")
}

func TestSpeechmaticsRealtimeSessionControlMethods(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key", WithRealtimeWebsocketDisabled())
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	if err := session.UpdateInstructions("new instructions"); err != nil {
		t.Fatalf("UpdateInstructions error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "session.update", "instructions", "new instructions")
	if err := session.UpdateOptions(llm.RealtimeSessionOptions{Voice: "megan", VoiceSet: true}); err != nil {
		t.Fatalf("UpdateOptions voice error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "session.update", "voice", "megan")
	if err := session.GenerateReply(llm.RealtimeGenerateReplyOptions{Instructions: "answer now", InstructionsSet: true}); err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "response.create", "instructions", "new instructions\nanswer now")
	if err := session.Say("hello"); err != nil {
		t.Fatalf("Say error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "response.create", "text", "hello")
	if err := session.PushAudio(&model.AudioFrame{Data: []byte{0x01, 0x02}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err != nil {
		t.Fatalf("PushAudio error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "input_audio_buffer.append", "audio", []byte{0x01, 0x02})
	if err := session.CommitAudio(); err != nil {
		t.Fatalf("CommitAudio error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "input_audio_buffer.commit", "", nil)
	if err := session.ClearAudio(); err != nil {
		t.Fatalf("ClearAudio error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "input_audio_buffer.clear", "", nil)
	rtSession := session.(*speechmaticsRealtimeSession)
	if ok := rtSession.handleServerEvent(map[string]any{"type": "response.created", "response_id": "resp_interrupt"}); !ok {
		t.Fatal("response.created event ignored")
	}
	assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	if err := session.Interrupt(); err != nil {
		t.Fatalf("Interrupt error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "response.cancel", "", nil)
	if err := session.Truncate(llm.RealtimeTruncateOptions{}); err != nil {
		t.Fatalf("Truncate error = %v", err)
	}
	if err := session.PushVideo(&images.VideoFrame{}); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("PushVideo error = %v, want unsupported", err)
	}
}

func TestSpeechmaticsRealtimeGenerateReplyPreservesPerResponseTools(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key", WithRealtimeWebsocketDisabled())
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	toolChoice := map[string]any{
		"type": "function",
		"name": "lookup_weather",
	}
	err = session.GenerateReply(llm.RealtimeGenerateReplyOptions{
		Tools: []llm.Tool{speechmaticsRealtimeTestTool{
			name:        "lookup_weather",
			description: "look up weather",
			parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{"type": "string"},
				},
			},
		}},
		ToolChoice: toolChoice,
	})
	if err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}
	command := nextSpeechmaticsRealtimeCommand(t, session)
	if command["type"] != "response.create" {
		t.Fatalf("command type = %#v, want response.create", command["type"])
	}
	tools, ok := command["tools"].([]map[string]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one formatted function tool", command["tools"])
	}
	tool := tools[0]
	if tool["type"] != "function" || tool["name"] != "lookup_weather" || tool["description"] != "look up weather" {
		t.Fatalf("tool = %#v, want function lookup_weather", tool)
	}
	parameters, ok := tool["parameters"].(map[string]any)
	if !ok || parameters["type"] != "object" {
		t.Fatalf("parameters = %#v, want object schema", tool["parameters"])
	}
	gotToolChoice, ok := command["tool_choice"].(map[string]any)
	if !ok || gotToolChoice["type"] != "function" || gotToolChoice["name"] != "lookup_weather" {
		t.Fatalf("tool_choice = %#v, want original map", command["tool_choice"])
	}
}

func TestSpeechmaticsRealtimeGenerateReplyPrependsSessionInstructions(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key", WithRealtimeWebsocketDisabled(), WithRealtimeSystemPrompt("base prompt"))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")
	if err := session.UpdateInstructions("updated base"); err != nil {
		t.Fatalf("UpdateInstructions error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "session.update", "instructions", "updated base")

	err = session.GenerateReply(llm.RealtimeGenerateReplyOptions{
		Instructions:    "answer concisely",
		InstructionsSet: true,
	})
	if err != nil {
		t.Fatalf("GenerateReply error = %v", err)
	}

	assertSpeechmaticsRealtimeCommand(t, session, "response.create", "instructions", "updated base\nanswer concisely")
}

func TestSpeechmaticsRealtimeSessionBuffersBurstAudioCommandsInOrder(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key", WithRealtimeWebsocketDisabled())
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	for i := 0; i < 300; i++ {
		frame := &model.AudioFrame{
			Data:              []byte{byte(i % 251)},
			SampleRate:        16000,
			NumChannels:       1,
			SamplesPerChannel: 1,
		}
		if err := session.PushAudio(frame); err != nil {
			t.Fatalf("PushAudio #%d error = %v, want buffered command", i, err)
		}
	}
	for i := 0; i < 300; i++ {
		command := nextSpeechmaticsRealtimeCommand(t, session)
		if command["type"] != "input_audio_buffer.append" {
			t.Fatalf("command #%d type = %#v, want input_audio_buffer.append", i, command["type"])
		}
		data, ok := command["audio"].([]byte)
		if !ok || len(data) != 1 || data[0] != byte(i%251) {
			t.Fatalf("command #%d audio = %#v, want ordered byte %d", i, command["audio"], byte(i%251))
		}
	}
}

func TestSpeechmaticsRealtimeSessionCloseIsIdempotent(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key", WithRealtimeWebsocketDisabled())
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("first Close error = %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("second Close error = %v", err)
	}
	if _, ok := <-session.EventCh(); ok {
		t.Fatal("EventCh still open after Close")
	}
	if err := session.PushAudio(&model.AudioFrame{Data: []byte{0x01, 0x02}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1}); err != nil {
		t.Fatalf("PushAudio after Close error = %v, want nil", err)
	}
}

func TestSpeechmaticsRealtimeSessionIgnoresLateClientEventsAfterClose(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key", WithRealtimeWebsocketDisabled())
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	session, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	if err := session.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	rtSession := session.(*speechmaticsRealtimeSession)

	lateCalls := []struct {
		name string
		call func() error
	}{
		{name: "UpdateInstructions", call: func() error { return session.UpdateInstructions("late") }},
		{name: "UpdateOptions", call: func() error { return session.UpdateOptions(llm.RealtimeSessionOptions{Voice: "late", VoiceSet: true}) }},
		{name: "GenerateReply", call: func() error {
			return session.GenerateReply(llm.RealtimeGenerateReplyOptions{Instructions: "late", InstructionsSet: true})
		}},
		{name: "Say", call: func() error { return session.Say("late") }},
		{name: "PushAudio", call: func() error {
			return session.PushAudio(&model.AudioFrame{Data: []byte{0x01}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1})
		}},
		{name: "CommitAudio", call: session.CommitAudio},
		{name: "ClearAudio", call: session.ClearAudio},
		{name: "Interrupt", call: session.Interrupt},
		{name: "Truncate", call: func() error { return session.Truncate(llm.RealtimeTruncateOptions{}) }},
		{name: "PushVideo", call: func() error { return session.PushVideo(&images.VideoFrame{}) }},
	}
	for _, tc := range lateCalls {
		if err := tc.call(); err != nil {
			t.Fatalf("%s after Close error = %v, want nil", tc.name, err)
		}
	}
	if rtSession.instructions != defaultSpeechmaticsRealtimeSystemPrompt {
		t.Fatalf("instructions after late update = %q, want original", rtSession.instructions)
	}
	if rtSession.voice != defaultSpeechmaticsRealtimeVoice {
		t.Fatalf("voice after late update = %q, want original", rtSession.voice)
	}
}

func TestSpeechmaticsRealtimeSessionServerEventsEmitReferenceGenerationStreams(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key", WithRealtimeWebsocketDisabled(), WithRealtimeOutputSampleRate(24000))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*speechmaticsRealtimeSession)
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	if ok := session.handleServerEvent(map[string]any{"type": "input_audio_buffer.speech_started"}); !ok {
		t.Fatal("speech_started event ignored")
	}
	assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeSpeechStarted)
	if ok := session.handleServerEvent(map[string]any{
		"type":       "conversation.item.input_audio_transcription.completed",
		"item_id":    "msg_user_1",
		"transcript": "hello",
	}); !ok {
		t.Fatal("input transcription event ignored")
	}
	transcript := assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)
	if transcript.InputTranscription == nil || !transcript.InputTranscription.IsFinal || transcript.InputTranscription.Transcript != "hello" {
		t.Fatalf("input transcription = %#v, want final hello", transcript.InputTranscription)
	}

	if ok := session.handleServerEvent(map[string]any{"type": "response.created", "response_id": "resp_1"}); !ok {
		t.Fatal("response.created event ignored")
	}
	created := assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	if created.Generation == nil || created.Generation.ResponseID != "resp_1" {
		t.Fatalf("generation = %#v, want resp_1", created.Generation)
	}
	if ok := session.handleServerEvent(map[string]any{"type": "response.output_item.added", "item_id": "msg_agent_1"}); !ok {
		t.Fatal("output item event ignored")
	}
	message := assertSpeechmaticsRealtimeMessage(t, created.Generation.MessageCh)
	if message.MessageID != "msg_agent_1" {
		t.Fatalf("message id = %q, want msg_agent_1", message.MessageID)
	}

	audio := []byte{1, 2, 3, 4}
	for _, event := range []map[string]any{
		{"type": "response.output_audio_transcript.delta", "item_id": "msg_agent_1", "delta": "Hi "},
		{"type": "response.output_text.delta", "item_id": "msg_agent_1", "delta": "there"},
		{"type": "response.output_audio.delta", "item_id": "msg_agent_1", "delta": base64.StdEncoding.EncodeToString(audio)},
		{"type": "response.output_item.done", "item_id": "msg_agent_1"},
	} {
		if ok := session.handleServerEvent(event); !ok {
			t.Fatalf("server event ignored: %#v", event)
		}
	}
	if got := assertSpeechmaticsRealtimeText(t, message.TextCh); got != "Hi " {
		t.Fatalf("first text delta = %q, want Hi ", got)
	}
	if got := assertSpeechmaticsRealtimeText(t, message.TextCh); got != "there" {
		t.Fatalf("second text delta = %q, want there", got)
	}
	frame := assertSpeechmaticsRealtimeAudio(t, message.AudioCh)
	if frame.SampleRate != 24000 || frame.NumChannels != 1 || int(frame.SamplesPerChannel) != len(audio)/2 {
		t.Fatalf("audio frame = rate %d channels %d samples %d, want 24000/1/%d", frame.SampleRate, frame.NumChannels, frame.SamplesPerChannel, len(audio)/2)
	}
	if !bytes.Equal(frame.Data, audio) {
		t.Fatalf("audio data = %#v, want %#v", frame.Data, audio)
	}
	assertSpeechmaticsRealtimeClosedText(t, message.TextCh)
	assertSpeechmaticsRealtimeClosedAudio(t, message.AudioCh)
}

func TestSpeechmaticsRealtimeSessionConversationItemAddedEmitsRemoteItem(t *testing.T) {
	session := &speechmaticsRealtimeSession{
		eventCh: make(chan llm.RealtimeEvent, 1),
	}

	ok := session.handleServerEvent(map[string]any{
		"type":             "conversation.item.added",
		"previous_item_id": "prev_123",
		"item": map[string]any{
			"id":   "msg_123",
			"type": "message",
			"role": "user",
			"content": []any{
				map[string]any{"type": "input_text", "text": "hello"},
			},
		},
	})

	if !ok {
		t.Fatal("conversation.item.added ignored, want remote item event")
	}
	event := assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeRemoteItemAdded)
	if event.RemoteItem == nil {
		t.Fatal("RemoteItem = nil, want payload")
	}
	if event.RemoteItem.PreviousItemID != "prev_123" || !event.RemoteItem.PreviousItemIDSet {
		t.Fatalf("RemoteItem previous = %#v, want explicit prev_123", event.RemoteItem)
	}
	msg, ok := event.RemoteItem.Item.(*llm.ChatMessage)
	if !ok {
		t.Fatalf("RemoteItem.Item = %T, want *llm.ChatMessage", event.RemoteItem.Item)
	}
	if msg.ID != "msg_123" || msg.Role != llm.ChatRoleUser || msg.TextContent() != "hello" {
		t.Fatalf("message = %#v, want user text message", msg)
	}
}

func TestSpeechmaticsRealtimeSessionRemoteFunctionOutputAllowsReferenceEmptyStrings(t *testing.T) {
	session := &speechmaticsRealtimeSession{
		eventCh: make(chan llm.RealtimeEvent, 1),
	}

	ok := session.handleServerEvent(map[string]any{
		"type": "conversation.item.added",
		"item": map[string]any{
			"id":      "out_empty",
			"type":    "function_call_output",
			"call_id": "",
			"output":  "",
		},
	})

	if !ok {
		t.Fatal("conversation.item.added function output ignored")
	}
	event := assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeRemoteItemAdded)
	output, ok := event.RemoteItem.Item.(*llm.FunctionCallOutput)
	if !ok {
		t.Fatalf("RemoteItem.Item = %T, want *llm.FunctionCallOutput", event.RemoteItem.Item)
	}
	if output.ID != "out_empty" || output.CallID != "" || output.Output != "" {
		t.Fatalf("function output = %#v, want empty call_id/output preserved", output)
	}
}

func TestSpeechmaticsRealtimeSessionInputTranscriptCompletedDerivesConfidence(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key", WithRealtimeWebsocketDisabled())
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*speechmaticsRealtimeSession)
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	if ok := session.handleServerEvent(map[string]any{
		"type":          "conversation.item.input_audio_transcription.completed",
		"item_id":       "msg_user_1",
		"content_index": 2,
		"transcript":    "hello",
		"logprobs": []any{
			map[string]any{"logprob": -0.1},
			map[string]any{"logprob": -0.3},
		},
	}); !ok {
		t.Fatal("input transcription event ignored")
	}
	ev := assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)
	if ev.InputTranscription == nil {
		t.Fatal("InputTranscription = nil, want transcription payload")
	}
	wantConfidence := math.Exp((-0.1 + -0.3) / 2)
	if ev.InputTranscription.ItemID != "msg_user_1" || ev.InputTranscription.ContentIndex != 2 || ev.InputTranscription.Transcript != "hello" || !ev.InputTranscription.IsFinal {
		t.Fatalf("InputTranscription = %#v, want final msg_user_1 transcript", ev.InputTranscription)
	}
	if ev.InputTranscription.Confidence == nil || math.Abs(*ev.InputTranscription.Confidence-wantConfidence) > 1e-9 {
		t.Fatalf("Confidence = %#v, want %.12f", ev.InputTranscription.Confidence, wantConfidence)
	}

	if ok := session.handleServerEvent(map[string]any{
		"type":       "conversation.item.input_audio_transcription.completed",
		"item_id":    "msg_user_2",
		"transcript": "empty",
		"logprobs":   []any{},
	}); !ok {
		t.Fatal("input transcription event with empty logprobs ignored")
	}
	ev = assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)
	if ev.InputTranscription.Confidence != nil {
		t.Fatalf("Confidence = %#v, want nil for empty logprobs", ev.InputTranscription.Confidence)
	}
}

func TestSpeechmaticsRealtimeSessionFinalInputTranscriptUpdatesRemoteItem(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key", WithRealtimeWebsocketDisabled())
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*speechmaticsRealtimeSession)
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	if ok := session.handleServerEvent(map[string]any{
		"type": "conversation.item.added",
		"item": map[string]any{
			"id":   "msg_user_1",
			"type": "message",
			"role": "user",
			"content": []any{
				map[string]any{"type": "input_audio"},
			},
		},
	}); !ok {
		t.Fatal("conversation item added event ignored")
	}
	assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeRemoteItemAdded)

	if ok := session.handleServerEvent(map[string]any{
		"type":       "conversation.item.input_audio_transcription.completed",
		"item_id":    "msg_user_1",
		"transcript": "hello world",
		"logprobs": []any{
			map[string]any{"logprob": -0.2},
		},
	}); !ok {
		t.Fatal("input transcription event ignored")
	}
	assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)

	session.mu.Lock()
	tracked, _ := session.remoteItems["msg_user_1"].(*llm.ChatMessage)
	session.mu.Unlock()
	if tracked == nil {
		t.Fatal("tracked item = nil, want chat message")
	}
	if len(tracked.Content) != 1 || tracked.Content[0].Text != "hello world" {
		t.Fatalf("tracked content = %#v, want final transcript", tracked.Content)
	}
	wantConfidence := math.Exp(-0.2)
	if tracked.TranscriptConfidence == nil || math.Abs(*tracked.TranscriptConfidence-wantConfidence) > 1e-9 {
		t.Fatalf("tracked confidence = %#v, want %.12f", tracked.TranscriptConfidence, wantConfidence)
	}
}

func TestSpeechmaticsRealtimeSessionAudioTranscriptDeltaEmitsReferenceTimedText(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key", WithRealtimeWebsocketDisabled())
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*speechmaticsRealtimeSession)
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	if ok := session.handleServerEvent(map[string]any{"type": "response.created", "response_id": "resp_timed"}); !ok {
		t.Fatal("response.created event ignored")
	}
	created := assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	if ok := session.handleServerEvent(map[string]any{"type": "response.output_item.added", "item_id": "msg_timed"}); !ok {
		t.Fatal("response.output_item.added ignored")
	}
	message := assertSpeechmaticsRealtimeMessage(t, created.Generation.MessageCh)
	if ok := session.handleServerEvent(map[string]any{
		"type":       "response.output_audio_transcript.delta",
		"item_id":    "msg_timed",
		"delta":      "hello",
		"start_time": 1.25,
	}); !ok {
		t.Fatal("audio transcript delta ignored")
	}
	if got := assertSpeechmaticsRealtimeText(t, message.TextCh); got != "hello" {
		t.Fatalf("text delta = %q, want hello", got)
	}
	timed := assertSpeechmaticsRealtimeTimedText(t, message.TimedTextCh)
	if timed.Text != "hello" || timed.StartTime != 1.25 {
		t.Fatalf("timed text = %#v, want hello at 1.25", timed)
	}
}

func TestSpeechmaticsRealtimeSessionContentPartAddedSetsReferenceModalities(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key", WithRealtimeWebsocketDisabled())
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*speechmaticsRealtimeSession)
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	if ok := session.handleServerEvent(map[string]any{"type": "response.created", "response_id": "resp_text"}); !ok {
		t.Fatal("response.created event ignored")
	}
	created := assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	if ok := session.handleServerEvent(map[string]any{
		"type": "response.output_item.added",
		"item": map[string]any{"id": "msg_text", "type": "message"},
	}); !ok {
		t.Fatal("response.output_item.added ignored")
	}
	message := assertSpeechmaticsRealtimeMessage(t, created.Generation.MessageCh)

	if ok := session.handleServerEvent(map[string]any{
		"type":    "response.content_part.added",
		"item_id": "msg_text",
		"part":    map[string]any{"type": "text"},
	}); !ok {
		t.Fatal("response.content_part.added ignored")
	}

	modalities := assertSpeechmaticsRealtimeModalities(t, message.ModalitiesCh)
	if len(modalities) != 1 || modalities[0] != "text" {
		t.Fatalf("modalities = %#v, want text-only", modalities)
	}
}

func TestSpeechmaticsRealtimeSessionOutputDoneEventsAreReferenceNoops(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key", WithRealtimeWebsocketDisabled())
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*speechmaticsRealtimeSession)
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	if ok := session.handleServerEvent(map[string]any{"type": "response.created", "response_id": "resp_done"}); !ok {
		t.Fatal("response.created event ignored")
	}
	assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)

	for _, eventType := range []string{
		"response.output_text.done",
		"response.text.done",
		"response.output_audio.done",
		"response.audio.done",
	} {
		if ok := session.handleServerEvent(map[string]any{"type": eventType}); !ok {
			t.Fatalf("%s ignored, want reference no-op handled", eventType)
		}
	}
	assertSpeechmaticsRealtimeNoCommand(t, session)
}

func TestSpeechmaticsRealtimeSessionInputTranscriptDeltasAccumulateLikeReference(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key", WithRealtimeWebsocketDisabled())
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*speechmaticsRealtimeSession)
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	for _, event := range []map[string]any{
		{"type": "conversation.item.input_audio_transcription.delta", "item_id": "msg_user_1", "content_index": 0, "delta": "hel"},
		{"type": "conversation.item.input_audio_transcription.delta", "item_id": "msg_user_1", "content_index": 0, "delta": "lo"},
	} {
		if ok := session.handleServerEvent(event); !ok {
			t.Fatalf("input transcript delta ignored: %#v", event)
		}
	}
	first := assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)
	if first.InputTranscription == nil || first.InputTranscription.Transcript != "hel" || first.InputTranscription.IsFinal {
		t.Fatalf("first partial = %#v, want hel final=false", first.InputTranscription)
	}
	second := assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)
	if second.InputTranscription == nil || second.InputTranscription.Transcript != "hello" || second.InputTranscription.IsFinal {
		t.Fatalf("second partial = %#v, want accumulated hello final=false", second.InputTranscription)
	}

	if ok := session.handleServerEvent(map[string]any{
		"type":          "conversation.item.input_audio_transcription.completed",
		"item_id":       "msg_user_1",
		"content_index": 0,
		"transcript":    "hello world",
	}); !ok {
		t.Fatal("input transcript completed ignored")
	}
	final := assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)
	if final.InputTranscription == nil || final.InputTranscription.Transcript != "hello world" || !final.InputTranscription.IsFinal {
		t.Fatalf("final transcript = %#v, want final hello world", final.InputTranscription)
	}

	if ok := session.handleServerEvent(map[string]any{
		"type":          "conversation.item.input_audio_transcription.delta",
		"item_id":       "msg_user_1",
		"content_index": 0,
		"delta":         "new",
	}); !ok {
		t.Fatal("post-final input transcript delta ignored")
	}
	afterFinal := assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)
	if afterFinal.InputTranscription == nil || afterFinal.InputTranscription.Transcript != "new" || afterFinal.InputTranscription.IsFinal {
		t.Fatalf("post-final partial = %#v, want reset accumulator with new final=false", afterFinal.InputTranscription)
	}
}

func TestSpeechmaticsRealtimeSessionInputTranscriptFailedFinalizesReferencePartial(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key", WithRealtimeWebsocketDisabled())
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*speechmaticsRealtimeSession)
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	if ok := session.handleServerEvent(map[string]any{
		"type": "conversation.item.added",
		"item": map[string]any{
			"id":      "msg_user_failed",
			"type":    "message",
			"role":    "user",
			"content": []any{map[string]any{"type": "input_audio"}},
		},
	}); !ok {
		t.Fatal("conversation item added event ignored")
	}
	assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeRemoteItemAdded)

	if ok := session.handleServerEvent(map[string]any{
		"type":          "conversation.item.input_audio_transcription.delta",
		"item_id":       "msg_user_failed",
		"content_index": 1,
		"delta":         "half ",
	}); !ok {
		t.Fatal("input transcript delta ignored")
	}
	partial := assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)
	if partial.InputTranscription == nil || partial.InputTranscription.Transcript != "half " || partial.InputTranscription.IsFinal {
		t.Fatalf("partial transcript = %#v, want half final=false", partial.InputTranscription)
	}
	if ok := session.handleServerEvent(map[string]any{
		"type":          "conversation.item.input_audio_transcription.failed",
		"item_id":       "msg_user_failed",
		"content_index": 1,
	}); !ok {
		t.Fatal("input transcript failed ignored")
	}
	final := assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)
	if final.InputTranscription == nil || final.InputTranscription.Transcript != "half " || !final.InputTranscription.IsFinal {
		t.Fatalf("failed transcript final = %#v, want accumulated partial final=true", final.InputTranscription)
	}
	session.mu.Lock()
	tracked, _ := session.remoteItems["msg_user_failed"].(*llm.ChatMessage)
	session.mu.Unlock()
	if tracked == nil || len(tracked.Content) != 1 || tracked.Content[0].Text != "half " {
		t.Fatalf("tracked failed transcript = %#v, want finalized partial", tracked)
	}

	if ok := session.handleServerEvent(map[string]any{
		"type":          "conversation.item.input_audio_transcription.failed",
		"item_id":       "msg_user_failed",
		"content_index": 1,
	}); ok {
		t.Fatal("second failed event handled, want ignored after accumulator cleared")
	}
}

func TestSpeechmaticsRealtimeSessionOutputItemDoneEmitsReferenceFunctionCall(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key", WithRealtimeWebsocketDisabled())
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*speechmaticsRealtimeSession)
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	if ok := session.handleServerEvent(map[string]any{
		"type":        "response.created",
		"response_id": "resp_tools",
	}); !ok {
		t.Fatal("response.created event ignored")
	}
	created := assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)

	if ok := session.handleServerEvent(map[string]any{
		"type": "response.output_item.added",
		"item": map[string]any{
			"id":   "fc_123",
			"type": "function_call",
		},
	}); !ok {
		t.Fatal("function call output item added ignored")
	}
	assertSpeechmaticsRealtimeNoMessage(t, created.Generation.MessageCh)

	if ok := session.handleServerEvent(map[string]any{
		"type": "response.output_item.done",
		"item": map[string]any{
			"id":        "fc_123",
			"type":      "function_call",
			"call_id":   "call_123",
			"name":      "lookup",
			"arguments": `{"city":"Paris"}`,
		},
	}); !ok {
		t.Fatal("function call output item ignored")
	}
	call := assertSpeechmaticsRealtimeFunctionCall(t, created.Generation.FunctionCh)
	if call.ID != "fc_123" || call.CallID != "call_123" || call.Name != "lookup" || call.Arguments != `{"city":"Paris"}` {
		t.Fatalf("function call = %#v, want reference function call item", call)
	}
}

func TestSpeechmaticsRealtimeSessionFunctionCallAllowsReferenceEmptyArguments(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key", WithRealtimeWebsocketDisabled())
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*speechmaticsRealtimeSession)
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	if ok := session.handleServerEvent(map[string]any{"type": "response.created", "response_id": "resp_empty_args"}); !ok {
		t.Fatal("response.created event ignored")
	}
	created := assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	if ok := session.handleServerEvent(map[string]any{
		"type": "response.output_item.done",
		"item": map[string]any{
			"id":        "fc_empty",
			"type":      "function_call",
			"call_id":   "call_empty",
			"name":      "noop",
			"arguments": "",
		},
	}); !ok {
		t.Fatal("function call with empty arguments ignored")
	}
	call := assertSpeechmaticsRealtimeFunctionCall(t, created.Generation.FunctionCh)
	if call.ID != "fc_empty" || call.CallID != "call_empty" || call.Name != "noop" || call.Arguments != "" {
		t.Fatalf("function call = %#v, want empty arguments preserved", call)
	}
}

func TestSpeechmaticsRealtimeSessionResponseDoneEmitsReferenceMetrics(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key", WithRealtimeWebsocketDisabled(), WithRealtimeModel("flow-pro"))
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*speechmaticsRealtimeSession)
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow-pro")

	if ok := session.handleServerEvent(map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_metrics"}}); !ok {
		t.Fatal("response.created event ignored")
	}
	created := assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	if ok := session.handleServerEvent(map[string]any{"type": "response.output_item.added", "item_id": "msg_metrics"}); !ok {
		t.Fatal("response.output_item.added ignored")
	}
	message := assertSpeechmaticsRealtimeMessage(t, created.Generation.MessageCh)
	if ok := session.handleServerEvent(map[string]any{"type": "response.output_text.delta", "item_id": "msg_metrics", "delta": "hello"}); !ok {
		t.Fatal("response.output_text.delta ignored")
	}
	if got := assertSpeechmaticsRealtimeText(t, message.TextCh); got != "hello" {
		t.Fatalf("text delta = %q, want hello", got)
	}

	if ok := session.handleServerEvent(map[string]any{
		"type": "response.done",
		"response": map[string]any{
			"id":     "resp_metrics",
			"status": "cancelled",
			"usage": map[string]any{
				"input_tokens":  3,
				"output_tokens": 2,
				"total_tokens":  5,
			},
		},
	}); !ok {
		t.Fatal("response.done ignored")
	}
	assertSpeechmaticsRealtimeClosedText(t, message.TextCh)
	metricsEvent := assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeMetricsCollected)
	metrics := metricsEvent.Metrics
	if metrics == nil {
		t.Fatal("metrics = nil")
	}
	if metrics.RequestID != "resp_metrics" || !metrics.Cancelled {
		t.Fatalf("metrics id/cancelled = %q/%v, want resp_metrics/true", metrics.RequestID, metrics.Cancelled)
	}
	if metrics.Label != "speechmatics.RealtimeModel" || metrics.Metadata == nil || metrics.Metadata.ModelName != "flow-pro" || metrics.Metadata.ModelProvider != "Speechmatics" {
		t.Fatalf("metrics model metadata = %#v/%#v, want Speechmatics flow-pro", metrics.Label, metrics.Metadata)
	}
	if metrics.InputTokens != 3 || metrics.OutputTokens != 2 || metrics.TotalTokens != 5 {
		t.Fatalf("metrics tokens = %d/%d/%d, want 3/2/5", metrics.InputTokens, metrics.OutputTokens, metrics.TotalTokens)
	}
	if metrics.TTFT < 0 || metrics.Duration < 0 || metrics.TokensPerSecond < 0 {
		t.Fatalf("metrics timing = ttft %f duration %f tps %f, want non-negative", metrics.TTFT, metrics.Duration, metrics.TokensPerSecond)
	}
}

func TestSpeechmaticsRealtimeSessionResponseDoneAppendsAudioTranscriptToRemoteItem(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key", WithRealtimeWebsocketDisabled())
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*speechmaticsRealtimeSession)
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	if ok := session.handleServerEvent(map[string]any{
		"type": "conversation.item.added",
		"item": map[string]any{
			"id":      "msg_audio",
			"type":    "message",
			"role":    "assistant",
			"content": []any{},
		},
	}); !ok {
		t.Fatal("conversation.item.added ignored")
	}
	assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeRemoteItemAdded)
	if ok := session.handleServerEvent(map[string]any{"type": "response.created", "response_id": "resp_audio"}); !ok {
		t.Fatal("response.created ignored")
	}
	created := assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	if ok := session.handleServerEvent(map[string]any{"type": "response.output_item.added", "item_id": "msg_audio"}); !ok {
		t.Fatal("response.output_item.added ignored")
	}
	message := assertSpeechmaticsRealtimeMessage(t, created.Generation.MessageCh)
	for _, event := range []map[string]any{
		{"type": "response.output_audio_transcript.delta", "item_id": "msg_audio", "delta": "Hi "},
		{"type": "response.audio_transcript.delta", "item_id": "msg_audio", "delta": "there"},
	} {
		if ok := session.handleServerEvent(event); !ok {
			t.Fatalf("audio transcript delta ignored: %#v", event)
		}
	}
	if got := assertSpeechmaticsRealtimeText(t, message.TextCh); got != "Hi " {
		t.Fatalf("first text delta = %q, want Hi ", got)
	}
	if got := assertSpeechmaticsRealtimeText(t, message.TextCh); got != "there" {
		t.Fatalf("second text delta = %q, want there", got)
	}

	if ok := session.handleServerEvent(map[string]any{
		"type":     "response.done",
		"response": map[string]any{"id": "resp_audio", "status": "completed"},
	}); !ok {
		t.Fatal("response.done ignored")
	}
	assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeMetricsCollected)
	session.mu.Lock()
	tracked, _ := session.remoteItems["msg_audio"].(*llm.ChatMessage)
	session.mu.Unlock()
	if tracked == nil || len(tracked.Content) != 1 || tracked.Content[0].Text != "Hi there" {
		t.Fatalf("tracked content = %#v, want appended audio transcript", tracked)
	}
}

func TestSpeechmaticsRealtimeSessionResponseDoneFailedEmitsReferenceError(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key", WithRealtimeWebsocketDisabled())
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*speechmaticsRealtimeSession)
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	if ok := session.handleServerEvent(map[string]any{"type": "response.created", "response_id": "resp_failed"}); !ok {
		t.Fatal("response.created event ignored")
	}
	assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeGenerationCreated)
	if ok := session.handleServerEvent(map[string]any{
		"type": "response.done",
		"response": map[string]any{
			"id":     "resp_failed",
			"status": "failed",
			"status_details": map[string]any{
				"error": map[string]any{
					"type": "invalid_request_error",
					"code": "inference_rate_limit_exceeded",
				},
			},
		},
	}); !ok {
		t.Fatal("response.done ignored")
	}
	assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeMetricsCollected)
	errorEvent := assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeError)
	var apiErr *llm.APIError
	if !errors.As(errorEvent.Error, &apiErr) {
		t.Fatalf("event error = %T %v, want APIError", errorEvent.Error, errorEvent.Error)
	}
	if apiErr.Message != "Speechmatics response failed with error type: invalid_request_error" {
		t.Fatalf("APIError message = %q", apiErr.Message)
	}
	body, ok := apiErr.Body.(map[string]any)
	if !ok || body["code"] != "inference_rate_limit_exceeded" {
		t.Fatalf("APIError body = %#v, want provider error body with code", apiErr.Body)
	}
	if !apiErr.Retryable {
		t.Fatal("APIError Retryable = false, want true")
	}
}

func TestSpeechmaticsRealtimeSessionProviderErrorEmitsReferenceError(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key", WithRealtimeWebsocketDisabled())
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*speechmaticsRealtimeSession)
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	if ok := session.handleServerEvent(map[string]any{
		"type": "error",
		"error": map[string]any{
			"message": "rate limited",
			"code":    "too_many_requests",
		},
	}); !ok {
		t.Fatal("provider error ignored")
	}
	errorEvent := assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeError)
	var apiErr *llm.APIError
	if !errors.As(errorEvent.Error, &apiErr) {
		t.Fatalf("event error = %T %v, want APIError", errorEvent.Error, errorEvent.Error)
	}
	if apiErr.Message != "Speechmatics returned an error" {
		t.Fatalf("APIError message = %q", apiErr.Message)
	}
	body, ok := apiErr.Body.(map[string]any)
	if !ok || body["code"] != "too_many_requests" {
		t.Fatalf("APIError body = %#v, want provider error body", apiErr.Body)
	}
	if !apiErr.Retryable {
		t.Fatal("APIError Retryable = false, want true")
	}

	if ok := session.handleServerEvent(map[string]any{
		"type":  "error",
		"error": map[string]any{"message": "Cancellation failed: response not found"},
	}); ok {
		t.Fatal("cancellation failure error handled, want ignored")
	}
}

func TestSpeechmaticsRealtimeSessionServerJSONDispatchesReferenceEvents(t *testing.T) {
	rtModel, err := NewRealtimeModel("test-key", WithRealtimeWebsocketDisabled())
	if err != nil {
		t.Fatalf("NewRealtimeModel error = %v", err)
	}
	sessionInterface, err := rtModel.Session()
	if err != nil {
		t.Fatalf("Session error = %v", err)
	}
	session := sessionInterface.(*speechmaticsRealtimeSession)
	assertSpeechmaticsRealtimeCommand(t, session, "session.create", "model", "flow")

	if ok := session.handleServerJSON([]byte(`{"type":"input_audio_buffer.speech_stopped"}`)); !ok {
		t.Fatal("speech stopped JSON ignored")
	}
	event := assertSpeechmaticsRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeSpeechStopped)
	if event.SpeechStopped == nil || !event.SpeechStopped.UserTranscriptionEnabled {
		t.Fatalf("speech stopped = %#v, want user transcription enabled", event.SpeechStopped)
	}
	if ok := session.handleServerJSON([]byte(`{`)); ok {
		t.Fatal("malformed JSON handled, want ignored")
	}
	if ok := session.handleServerJSON([]byte(`{"type":"provider.future_event"}`)); ok {
		t.Fatal("unknown JSON event handled, want ignored")
	}
}

func assertSpeechmaticsRealtimeCommand(t *testing.T, session llm.RealtimeSession, wantType, key string, want any) {
	t.Helper()
	command := nextSpeechmaticsRealtimeCommand(t, session)
	if command["type"] != wantType {
		t.Fatalf("command type = %#v, want %q in %#v", command["type"], wantType, command)
	}
	if key == "" {
		return
	}
	got := command[key]
	if key == "audio" {
		gotBytes, _ := got.([]byte)
		wantBytes, _ := want.([]byte)
		if string(gotBytes) != string(wantBytes) {
			t.Fatalf("command[%q] = %v, want %v", key, gotBytes, wantBytes)
		}
		return
	}
	if got != want {
		t.Fatalf("command[%q] = %#v, want %#v", key, got, want)
	}
}

func assertSpeechmaticsRealtimeRequest(t *testing.T, ch <-chan *http.Request) *http.Request {
	t.Helper()
	select {
	case req := <-ch:
		return req
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for websocket request")
	}
	return nil
}

func assertSpeechmaticsRealtimeOutboundJSON(t *testing.T, ch <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case message := <-ch:
		return message
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for websocket message")
	}
	return nil
}

func assertSpeechmaticsRealtimeToolsUpdate(t *testing.T, message map[string]any, wantName string) {
	t.Helper()
	if message["type"] != "session.update" {
		t.Fatalf("tools message type = %#v, want session.update in %#v", message["type"], message)
	}
	tools, ok := message["tools"].([]any)
	if !ok {
		typedTools, typedOK := message["tools"].([]map[string]any)
		if !typedOK {
			t.Fatalf("tools = %#v, want tool list", message["tools"])
		}
		tools = make([]any, 0, len(typedTools))
		for _, tool := range typedTools {
			tools = append(tools, tool)
		}
	}
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
	tool, ok := tools[0].(map[string]any)
	if !ok || tool["name"] != wantName {
		t.Fatalf("tool = %#v, want %s", tools[0], wantName)
	}
}

func assertSpeechmaticsRealtimeChatContextMessage(t *testing.T, message map[string]any, wantRole, wantText string) {
	t.Helper()
	if message["type"] != "conversation.item.create" {
		t.Fatalf("chat context message type = %#v, want conversation.item.create in %#v", message["type"], message)
	}
	if message["role"] != wantRole || message["text"] != wantText {
		t.Fatalf("chat context message = %#v, want role %q text %q", message, wantRole, wantText)
	}
}

func newSpeechmaticsRealtimeTestWebsocketDialer(t *testing.T, handler func(*websocket.Conn, *http.Request)) SpeechmaticsRealtimeWebsocketDialer {
	t.Helper()
	return newSpeechmaticsRealtimeTestWebsocketDialerWithConn(t, nil, handler)
}

func newSpeechmaticsRealtimeTestWebsocketDialerWithConn(t *testing.T, wrapConn func(net.Conn, int) net.Conn, handler func(*websocket.Conn, *http.Request)) SpeechmaticsRealtimeWebsocketDialer {
	t.Helper()
	upgrader := websocket.Upgrader{}
	dialCount := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		handler(conn, r)
	}))
	t.Cleanup(server.Close)

	dialer := websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			conn, err := net.Dial("tcp", server.Listener.Addr().String())
			if err != nil || wrapConn == nil {
				return conn, err
			}
			return wrapConn(conn, int(dialCount.Add(1))), nil
		},
	}
	return func(endpoint string, header http.Header) (*websocket.Conn, *http.Response, error) {
		return dialer.Dial(endpoint, header)
	}
}

func newSpeechmaticsRealtimeWriteFailAfterDialer(t *testing.T, writeErr error) SpeechmaticsRealtimeWebsocketDialer {
	t.Helper()
	upgrader := websocket.Upgrader{}
	return func(endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		clientConn, serverConn := net.Pipe()
		failConn := &speechmaticsRealtimeFailWriteConn{Conn: clientConn, err: writeErr}
		listener := newSpeechmaticsSingleConnListener(serverConn)
		server := &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Errorf("Upgrade error = %v", err)
					return
				}
				defer conn.Close()
				for {
					if _, _, err := conn.ReadMessage(); err != nil {
						return
					}
				}
			}),
		}
		go func() {
			_ = server.Serve(listener)
		}()
		t.Cleanup(func() {
			_ = server.Close()
			_ = listener.Close()
			_ = clientConn.Close()
			_ = serverConn.Close()
		})

		dialer := websocket.Dialer{
			NetDialContext: func(context.Context, string, string) (net.Conn, error) {
				return failConn, nil
			},
		}
		conn, response, err := dialer.Dial(endpoint, headers)
		if err != nil {
			return nil, response, err
		}
		failConn.fail.Store(true)
		return conn, response, nil
	}
}

type speechmaticsRealtimeFailWriteConn struct {
	net.Conn
	fail atomic.Bool
	err  error
}

func (c *speechmaticsRealtimeFailWriteConn) Write(p []byte) (int, error) {
	if c.fail.Load() {
		if c.err != nil {
			return 0, c.err
		}
		return 0, errors.New("write failed")
	}
	return c.Conn.Write(p)
}

type speechmaticsSingleConnListener struct {
	mu     sync.Mutex
	once   sync.Once
	conn   net.Conn
	closed chan struct{}
}

func newSpeechmaticsSingleConnListener(conn net.Conn) *speechmaticsSingleConnListener {
	return &speechmaticsSingleConnListener{
		conn:   conn,
		closed: make(chan struct{}),
	}
}

func (l *speechmaticsSingleConnListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if l.conn != nil {
		conn := l.conn
		l.conn = nil
		l.mu.Unlock()
		return conn, nil
	}
	l.mu.Unlock()

	<-l.closed
	return nil, net.ErrClosed
}

func (l *speechmaticsSingleConnListener) Close() error {
	l.once.Do(func() {
		close(l.closed)
		l.mu.Lock()
		if l.conn != nil {
			_ = l.conn.Close()
			l.conn = nil
		}
		l.mu.Unlock()
	})
	return nil
}

func (l *speechmaticsSingleConnListener) Addr() net.Addr {
	return speechmaticsDummyAddr("pipe")
}

type speechmaticsDummyAddr string

func (a speechmaticsDummyAddr) Network() string { return string(a) }
func (a speechmaticsDummyAddr) String() string  { return string(a) }

func nextSpeechmaticsRealtimeCommand(t *testing.T, session llm.RealtimeSession) map[string]any {
	t.Helper()
	rtSession := session.(*speechmaticsRealtimeSession)
	select {
	case command := <-rtSession.commandCh:
		return command
	case <-time.After(time.Second):
		t.Fatal("missing realtime command")
	}
	return nil
}

func assertSpeechmaticsRealtimeNoCommand(t *testing.T, session llm.RealtimeSession) {
	t.Helper()
	rtSession := session.(*speechmaticsRealtimeSession)
	select {
	case command := <-rtSession.commandCh:
		t.Fatalf("command = %#v, want no command", command)
	case <-time.After(25 * time.Millisecond):
	}
}

func assertSpeechmaticsRealtimeEventType(t *testing.T, ch <-chan llm.RealtimeEvent, want llm.RealtimeEventType) llm.RealtimeEvent {
	t.Helper()
	select {
	case event, ok := <-ch:
		if !ok {
			t.Fatalf("event channel closed, want %s", want)
		}
		if event.Type != want {
			t.Fatalf("event type = %s, want %s", event.Type, want)
		}
		return event
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", want)
	}
	return llm.RealtimeEvent{}
}

func assertSpeechmaticsRealtimeMessage(t *testing.T, ch <-chan llm.MessageGeneration) llm.MessageGeneration {
	t.Helper()
	select {
	case message, ok := <-ch:
		if !ok {
			t.Fatal("message channel closed")
		}
		return message
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message generation")
	}
	return llm.MessageGeneration{}
}

func assertSpeechmaticsRealtimeNoMessage(t *testing.T, ch <-chan llm.MessageGeneration) {
	t.Helper()
	select {
	case message, ok := <-ch:
		if !ok {
			t.Fatal("message channel closed")
		}
		t.Fatalf("message = %#v, want no message generation", message)
	case <-time.After(25 * time.Millisecond):
	}
}

func assertSpeechmaticsRealtimeText(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case text, ok := <-ch:
		if !ok {
			t.Fatal("text channel closed")
		}
		return text
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for text delta")
	}
	return ""
}

func assertSpeechmaticsRealtimeAudio(t *testing.T, ch <-chan *model.AudioFrame) *model.AudioFrame {
	t.Helper()
	select {
	case frame, ok := <-ch:
		if !ok {
			t.Fatal("audio channel closed")
		}
		return frame
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for audio delta")
	}
	return nil
}

func assertSpeechmaticsRealtimeModalities(t *testing.T, ch <-chan []string) []string {
	t.Helper()
	select {
	case modalities, ok := <-ch:
		if !ok {
			t.Fatal("modalities channel closed")
		}
		return modalities
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for modalities")
	}
	return nil
}

func assertSpeechmaticsRealtimeTimedText(t *testing.T, ch <-chan llm.RealtimeTimedText) llm.RealtimeTimedText {
	t.Helper()
	select {
	case timed, ok := <-ch:
		if !ok {
			t.Fatal("timed text channel closed")
		}
		return timed
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for timed text delta")
	}
	return llm.RealtimeTimedText{}
}

func assertSpeechmaticsRealtimeClosedText(t *testing.T, ch <-chan string) {
	t.Helper()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("text channel still open")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for closed text channel")
	}
}

func assertSpeechmaticsRealtimeClosedAudio(t *testing.T, ch <-chan *model.AudioFrame) {
	t.Helper()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("audio channel still open")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for closed audio channel")
	}
}

func assertSpeechmaticsRealtimeFunctionCall(t *testing.T, ch <-chan *llm.FunctionCall) *llm.FunctionCall {
	t.Helper()
	select {
	case call, ok := <-ch:
		if !ok {
			t.Fatal("function call channel closed")
		}
		return call
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for function call")
	}
	return nil
}

type speechmaticsRealtimeTestTool struct {
	name        string
	description string
	parameters  map[string]any
}

func (t speechmaticsRealtimeTestTool) ID() string          { return t.name }
func (t speechmaticsRealtimeTestTool) Name() string        { return t.name }
func (t speechmaticsRealtimeTestTool) Description() string { return t.description }
func (t speechmaticsRealtimeTestTool) Parameters() map[string]any {
	return t.parameters
}
func (t speechmaticsRealtimeTestTool) Execute(context.Context, string) (string, error) {
	return "", nil
}
