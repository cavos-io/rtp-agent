package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/gorilla/websocket"
)

func TestRealtimeModel_Session(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		for {
			mt, message, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var ev map[string]any
			json.Unmarshal(message, &ev)
			
			switch ev["type"] {
			case "session.update":
				conn.WriteMessage(mt, []byte(`{"type": "session.updated"}`))
			case "input_audio_buffer.append":
				conn.WriteMessage(mt, []byte(`{"type": "response.audio.delta", "delta": "audio_data"}`))
			}
		}
	}))
	defer s.Close()

	wsURL := "ws" + strings.TrimPrefix(s.URL, "http")
	
	m := NewRealtimeModel("key", "gpt-4o", WithRealtimeBaseURL(wsURL))
	session, err := m.Session()
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}
	defer session.Close()

	err = session.UpdateInstructions("be helpful")
	if err != nil {
		t.Errorf("UpdateInstructions failed: %v", err)
	}

	err = session.PushAudio(&model.AudioFrame{Data: []byte("audio")})
	if err != nil {
		t.Errorf("PushAudio failed: %v", err)
	}

	select {
	case ev := <-session.EventCh():
		if ev.Type != llm.RealtimeEventTypeAudio {
			t.Errorf("Expected audio event, got %v", ev.Type)
		}
	case <-time.After(1 * time.Second):
		t.Error("Timed out waiting for event")
	}
}
