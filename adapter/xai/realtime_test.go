package xai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/gorilla/websocket"
)

func TestXaiRealtimeDefaultsMatchReference(t *testing.T) {
	model := NewXaiRealtimeModel("test-key")

	if model.Model() != "grok-voice-think-fast-1.0" {
		t.Fatalf("Model() = %q, want reference default realtime model", model.Model())
	}
	if model.Provider() != "xAI Realtime API" {
		t.Fatalf("Provider() = %q, want reference provider label", model.Provider())
	}
	caps := model.Capabilities()
	if !caps.AudioOutput {
		t.Fatal("AudioOutput = false, want audio modality enabled")
	}
	if !caps.TurnDetection {
		t.Fatal("TurnDetection = false, want server VAD support")
	}
	if !caps.UserTranscription {
		t.Fatal("UserTranscription = false, want default input audio transcription")
	}
	if caps.PerResponseToolChoice {
		t.Fatal("PerResponseToolChoice = true, want xAI reference false")
	}
	if !caps.MutableTools || !caps.MutableChatContext || !caps.MutableInstructions {
		t.Fatalf("mutable capabilities = %#v, want mutable tools/chat/instructions", caps)
	}

	var _ llm.RealtimeModel = model
}

func TestNewXaiRealtimeModelUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("XAI_API_KEY", "env-key")

	model := NewXaiRealtimeModel("")

	if model.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env-key", model.apiKey)
	}
}

func TestXaiRealtimeSessionRequiresXAIAPIKeyBeforeDial(t *testing.T) {
	t.Setenv("XAI_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "openai-key")
	model := NewXaiRealtimeModel("")

	_, err := model.Session()
	if err == nil {
		t.Fatal("Session() error = nil, want xAI API key error")
	}
	if !strings.Contains(err.Error(), "XAI_API_KEY") {
		t.Fatalf("Session() error = %q, want XAI_API_KEY guidance", err)
	}
}

func TestXaiRealtimeUpdateToolsUsesReferenceProviderToolPayloads(t *testing.T) {
	messages := make(chan map[string]any, 2)
	handlerDone := make(chan struct{})
	handlerErr := make(chan error, 1)
	dialer := newXaiRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
		defer close(handlerDone)
		for i := 0; i < 2; i++ {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				handlerErr <- fmt.Errorf("read websocket message: %w", err)
				return
			}
			var msg map[string]any
			if err := json.Unmarshal(payload, &msg); err != nil {
				handlerErr <- fmt.Errorf("decode websocket message: %w", err)
				return
			}
			messages <- msg
		}
	})

	model := NewXaiRealtimeModel("test-key",
		WithXaiRealtimeBaseURL("ws://xai.test/v1/realtime"),
		WithXaiRealtimeWebsocketDialer(dialer),
	)
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}

	<-messages
	if err := session.UpdateTools([]llm.Tool{
		&WebSearchTool{},
		&XSearchTool{AllowedHandles: []string{"livekit"}},
		&FileSearchTool{VectorStoreIDs: []string{"vs_1"}, MaxNumResults: 4},
	}); err != nil {
		t.Fatalf("UpdateTools() error = %v", err)
	}

	var update map[string]any
	select {
	case update = <-messages:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tools update")
	}
	if update["type"] != "session.update" {
		t.Fatalf("update type = %#v, want session.update", update["type"])
	}
	sessionPayload := update["session"].(map[string]any)
	tools := sessionPayload["tools"].([]any)
	if len(tools) != 3 {
		t.Fatalf("len(tools) = %d, want 3: %#v", len(tools), tools)
	}
	webSearch := tools[0].(map[string]any)
	if webSearch["type"] != "web_search" {
		t.Fatalf("web search tool = %#v", webSearch)
	}
	xSearch := tools[1].(map[string]any)
	handles := xSearch["allowed_x_handles"].([]any)
	if xSearch["type"] != "x_search" || len(handles) != 1 || handles[0] != "livekit" {
		t.Fatalf("x search tool = %#v", xSearch)
	}
	fileSearch := tools[2].(map[string]any)
	vectorStores := fileSearch["vector_store_ids"].([]any)
	if fileSearch["type"] != "file_search" || len(vectorStores) != 1 || vectorStores[0] != "vs_1" || fileSearch["max_num_results"] != float64(4) {
		t.Fatalf("file search tool = %#v", fileSearch)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for websocket handler")
	}
	select {
	case err := <-handlerErr:
		t.Fatal(err)
	default:
	}
}

func TestXaiRealtimeFinalInputTranscriptionDoesNotDuplicateAudioTranscript(t *testing.T) {
	handlerDone := make(chan struct{})
	handlerErr := make(chan error, 1)
	readyForSync := make(chan struct{})
	unexpectedSync := make(chan string, 4)
	dialer := newXaiRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
		defer close(handlerDone)
		if _, _, err := conn.ReadMessage(); err != nil {
			handlerErr <- fmt.Errorf("read initial session update: %w", err)
			return
		}
		if err := conn.WriteJSON(map[string]any{
			"type":             "conversation.item.added",
			"previous_item_id": nil,
			"item": map[string]any{
				"id":   "item_123",
				"type": "message",
				"role": "user",
				"content": []map[string]any{
					{"type": "input_audio", "transcript": "hello"},
				},
			},
		}); err != nil {
			handlerErr <- fmt.Errorf("write item added: %w", err)
			return
		}
		if err := conn.WriteJSON(map[string]any{
			"type":          "conversation.item.input_audio_transcription.completed",
			"item_id":       "item_123",
			"content_index": 0,
			"transcript":    "hello",
		}); err != nil {
			handlerErr <- fmt.Errorf("write final transcription: %w", err)
			return
		}
		<-readyForSync
		for {
			if err := conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
				handlerErr <- fmt.Errorf("set read deadline: %w", err)
				return
			}
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			unexpectedSync <- string(payload)
		}
	})

	model := NewXaiRealtimeModel("test-key",
		WithXaiRealtimeBaseURL("ws://xai.test/v1/realtime"),
		WithXaiRealtimeWebsocketDialer(dialer),
	)
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}

	assertXaiRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeRemoteItemAdded)
	assertXaiRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeInputAudioTranscriptionCompleted)
	close(readyForSync)
	if err := session.UpdateChatContext(&llm.ChatContext{
		Items: []llm.ChatItem{
			&llm.ChatMessage{
				ID:      "item_123",
				Role:    llm.ChatRoleUser,
				Content: []llm.ChatContent{{Text: "hello"}},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateChatContext() error = %v", err)
	}
	select {
	case msg := <-unexpectedSync:
		t.Fatalf("unexpected chat context sync message after duplicate transcription: %s", msg)
	case <-handlerDone:
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for websocket handler")
	}
	select {
	case err := <-handlerErr:
		t.Fatal(err)
	default:
	}
}

func TestXaiRealtimeNilPreviousItemIDAppendsToRemoteTail(t *testing.T) {
	handlerDone := make(chan struct{})
	handlerErr := make(chan error, 1)
	readyForSync := make(chan struct{})
	unexpectedSync := make(chan string, 4)
	dialer := newXaiRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
		defer close(handlerDone)
		if _, _, err := conn.ReadMessage(); err != nil {
			handlerErr <- fmt.Errorf("read initial session update: %w", err)
			return
		}
		for _, item := range []struct {
			id   string
			text string
		}{
			{id: "item_first", text: "first"},
			{id: "item_second", text: "second"},
		} {
			if err := conn.WriteJSON(map[string]any{
				"type":             "conversation.item.added",
				"previous_item_id": nil,
				"item": map[string]any{
					"id":      item.id,
					"type":    "message",
					"role":    "user",
					"content": []map[string]any{{"type": "input_text", "text": item.text}},
				},
			}); err != nil {
				handlerErr <- fmt.Errorf("write item added: %w", err)
				return
			}
		}
		<-readyForSync
		for {
			if err := conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
				handlerErr <- fmt.Errorf("set read deadline: %w", err)
				return
			}
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			unexpectedSync <- string(payload)
		}
	})

	model := NewXaiRealtimeModel("test-key",
		WithXaiRealtimeBaseURL("ws://xai.test/v1/realtime"),
		WithXaiRealtimeWebsocketDialer(dialer),
	)
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}

	assertXaiRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeRemoteItemAdded)
	assertXaiRealtimeEventType(t, session.EventCh(), llm.RealtimeEventTypeRemoteItemAdded)
	close(readyForSync)
	if err := session.UpdateChatContext(&llm.ChatContext{
		Items: []llm.ChatItem{
			&llm.ChatMessage{ID: "item_first", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "first"}}},
			&llm.ChatMessage{ID: "item_second", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "second"}}},
		},
	}); err != nil {
		t.Fatalf("UpdateChatContext() error = %v", err)
	}
	select {
	case msg := <-unexpectedSync:
		t.Fatalf("unexpected chat context sync message after nil previous_item_id append: %s", msg)
	case <-handlerDone:
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for websocket handler")
	}
	select {
	case err := <-handlerErr:
		t.Fatal(err)
	default:
	}
}

func TestXaiRealtimeIgnoresUnknownFunctionCalls(t *testing.T) {
	handlerDone := make(chan struct{})
	handlerErr := make(chan error, 1)
	releaseServer := make(chan struct{})
	dialer := newXaiRealtimeTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
		defer close(handlerDone)
		if _, _, err := conn.ReadMessage(); err != nil {
			handlerErr <- fmt.Errorf("read initial session update: %w", err)
			return
		}
		if err := conn.WriteJSON(map[string]any{
			"type": "response.output_item.done",
			"item": map[string]any{
				"id":        "item_unknown_tool",
				"type":      "function_call",
				"call_id":   "call_unknown",
				"name":      "unknown_tool",
				"arguments": "{}",
			},
		}); err != nil {
			handlerErr <- fmt.Errorf("write unknown function call: %w", err)
			return
		}
		<-releaseServer
	})

	model := NewXaiRealtimeModel("test-key",
		WithXaiRealtimeBaseURL("ws://xai.test/v1/realtime"),
		WithXaiRealtimeWebsocketDialer(dialer),
	)
	session, err := model.Session()
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}

	select {
	case ev := <-session.EventCh():
		t.Fatalf("unexpected event for unknown function call: %#v", ev)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseServer)
	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for websocket handler")
	}
	select {
	case err := <-handlerErr:
		t.Fatal(err)
	default:
	}
}

func assertXaiRealtimeEventType(t *testing.T, eventCh <-chan llm.RealtimeEvent, want llm.RealtimeEventType) llm.RealtimeEvent {
	t.Helper()
	select {
	case ev := <-eventCh:
		if ev.Type != want {
			t.Fatalf("event type = %q, want %q", ev.Type, want)
		}
		return ev
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s event", want)
		return llm.RealtimeEvent{}
	}
}

func newXaiRealtimeTestWebsocketDialer(t *testing.T, handler func(*websocket.Conn, *http.Request)) func(string, http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	upgrader := websocket.Upgrader{}
	return func(endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		clientConn, serverConn := net.Pipe()
		listener := newXaiSingleConnListener(serverConn)
		server := &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Errorf("Upgrade error = %v", err)
					return
				}
				defer conn.Close()
				handler(conn, r)
			}),
		}
		serverErrCh := make(chan error, 1)
		go func() {
			if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
				serverErrCh <- err
			}
		}()
		t.Cleanup(func() {
			_ = server.Close()
			_ = listener.Close()
			_ = clientConn.Close()
			_ = serverConn.Close()
		})

		dialer := websocket.Dialer{
			NetDialContext: func(context.Context, string, string) (net.Conn, error) {
				return clientConn, nil
			},
		}
		conn, response, err := dialer.Dial(endpoint, headers)
		select {
		case serverErr := <-serverErrCh:
			if err == nil {
				err = serverErr
			}
		default:
		}
		return conn, response, err
	}
}

type xaiSingleConnListener struct {
	mu     sync.Mutex
	once   sync.Once
	conn   net.Conn
	closed chan struct{}
}

func newXaiSingleConnListener(conn net.Conn) *xaiSingleConnListener {
	return &xaiSingleConnListener{
		conn:   conn,
		closed: make(chan struct{}),
	}
}

func (l *xaiSingleConnListener) Accept() (net.Conn, error) {
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

func (l *xaiSingleConnListener) Close() error {
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

func (l *xaiSingleConnListener) Addr() net.Addr {
	return xaiDummyAddr("pipe")
}

type xaiDummyAddr string

func (a xaiDummyAddr) Network() string { return string(a) }
func (a xaiDummyAddr) String() string  { return string(a) }
