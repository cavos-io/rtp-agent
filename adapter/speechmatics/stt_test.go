package speechmatics

import (
	"context"
	"net/http"
	"testing"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/library/utils/testutils"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/gorilla/websocket"
)

func TestSpeechmaticsSTT_Recognize(t *testing.T) {
	mockResponse := `{"id": "sm-123"}`
	server := testutils.NewJSONMockServer(mockResponse, http.StatusOK)
	defer server.Close()

	s := NewSpeechmaticsSTT("fake-key", 
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)

	frames := []*model.AudioFrame{
		{Data: []byte{0x01, 0x02}},
	}

	event, err := s.Recognize(context.Background(), frames, "en")
	if err != nil {
		t.Fatalf("Recognize failed: %v", err)
	}

	if event.Type != stt.SpeechEventFinalTranscript {
		t.Errorf("Expected final transcript event, got %v", event.Type)
	}

	if len(event.Alternatives) == 0 || event.Alternatives[0].Text != "[Speechmatics Job ID: sm-123]" {
		t.Errorf("Unexpected transcript: %v", event.Alternatives)
	}
}

func TestSpeechmaticsSTT_Stream(t *testing.T) {
	mockResponses := []string{
		`{"message": "AddPartialTranscript", "results": [{"alternatives": [{"content": "Hello", "confidence": 0.9}], "type": "word", "start_time": 0.1, "end_time": 0.5}]}`,
		`{"message": "AddTranscript", "results": [{"alternatives": [{"content": "Hello", "confidence": 0.9}], "type": "word", "start_time": 0.1, "end_time": 0.5}, {"alternatives": [{"content": "world", "confidence": 0.9}], "type": "word", "start_time": 0.5, "end_time": 1.0}]}`,
	}
	server := testutils.NewWebSocketMockServer(func(conn *websocket.Conn) {
		// Read init msg
		_, _, _ = conn.ReadMessage()
		// Send responses
		for _, resp := range mockResponses {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(resp))
		}
		// Wait for client to close or send a message to avoid premature close
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
	})
	defer server.Close()

	s := NewSpeechmaticsSTT("fake-key", 
		WithWSURL(server.URL),
	)

	stream, err := s.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	defer stream.Close()

	// Push a frame to trigger some activity (though mock doesn't need it)
	stream.PushFrame(&model.AudioFrame{Data: []byte{0x00, 0x01}})

	// 1st event: Partial
	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next (1) failed: %v", err)
	}
	if event.Type != stt.SpeechEventInterimTranscript || event.Alternatives[0].Text != "Hello" {
		t.Errorf("Unexpected 1st event: %v", event)
	}

	// 2nd event: Final
	event, err = stream.Next()
	if err != nil {
		t.Fatalf("Next (2) failed: %v", err)
	}
	if event.Type != stt.SpeechEventFinalTranscript || event.Alternatives[0].Text != "Hello world" {
		t.Errorf("Unexpected 2nd event: %v", event)
	}
}
