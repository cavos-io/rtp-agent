package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/library/utils/testutils"
	"github.com/gorilla/websocket"
)

func TestDropUnsupportedParams(t *testing.T) {
	params := map[string]any{
		"temperature": 0.7,
		"max_tokens":  100,
	}

	// o1 model should drop temperature
	dropped := dropUnsupportedParams("o1-preview", params)
	if _, ok := dropped["temperature"]; ok {
		t.Error("temperature should have been dropped for o1 model")
	}
	if _, ok := dropped["max_tokens"]; !ok {
		t.Error("max_tokens should NOT have been dropped")
	}

	// gpt-4o should keep all
	kept := dropUnsupportedParams("gpt-4o", params)
	if _, ok := kept["temperature"]; !ok {
		t.Error("temperature should have been kept for gpt-4o")
	}
}

func TestLLM_Initialization(t *testing.T) {
	l := NewLLM("gpt-4o", "key", "secret")
	if l.model != "gpt-4o" {
		t.Errorf("Expected gpt-4o, got %s", l.model)
	}
}

func TestSTT_Stream(t *testing.T) {
	server := testutils.NewWebSocketMockServer(func(conn *websocket.Conn) {
		// Read session.create
		_, _, _ = conn.ReadMessage()
		
		// Send session.created
		_ = conn.WriteJSON(map[string]interface{}{"type": "session.created"})
		
		// Send a transcript
		_ = conn.WriteJSON(map[string]interface{}{
			"type":       "final_transcript",
			"transcript": "hello world",
			"request_id": "req-1",
		})
		
		// Close
		_ = conn.WriteJSON(map[string]interface{}{"type": "session.closed"})
	})
	defer server.Close()

	s := NewSTT("deepgram/nova-3", "key", "secret", WithSTTBaseURL(testutils.GetWSURL(server.URL)))
	stream, err := s.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	defer stream.Close()

	// Wait for events
	// 1. StartOfSpeech (automatically emitted by processTranscript)
	// 2. RecognitionUsage
	// 3. FinalTranscript
	// 4. EndOfSpeech
	
	ev, err := stream.Next()
	if err != nil {
		t.Fatalf("Next (1) failed: %v", err)
	}
	
	// We might need to call Next() multiple times to get the transcript
	found := false
	for i := 0; i < 5; i++ {
		if ev.Alternatives != nil && len(ev.Alternatives) > 0 && ev.Alternatives[0].Text == "hello world" {
			found = true
			break
		}
		ev, _ = stream.Next()
	}

	if !found {
		t.Error("Did not find transcript 'hello world'")
	}

	// Just verifying we can get a transcript
}

func TestTTS_Stream(t *testing.T) {
	server := testutils.NewWebSocketMockServer(func(conn *websocket.Conn) {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var pkt map[string]any
			json.Unmarshal(msg, &pkt)
			
			if pkt["type"] == "input_transcript" {
				_ = conn.WriteJSON(map[string]interface{}{
					"type":  "output_audio",
					"audio": "AAAA", // fake base64
				})
				return
			}
		}
	})
	defer server.Close()

	ttsInstance := NewTTS("cartesia/sonic-3", "key", "secret", WithTTSBaseURL(testutils.GetWSURL(server.URL)))
	stream, err := ttsInstance.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	defer stream.Close()

	fmt.Println("Pushing text...")
	stream.PushText("Hello world. This is a longer sentence to ensure tokenization.")
	fmt.Println("Flushing...")
	stream.Flush()

	type nextRes struct {
		audio *tts.SynthesizedAudio
		err   error
	}
	ch := make(chan nextRes, 1)
	go func() {
		fmt.Println("Waiting for audio...")
		a, err := stream.Next()
		ch <- nextRes{a, err}
	}()

	select {
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for audio")
	case res := <-ch:
		fmt.Println("Got audio!")
		if res.err != nil {
			t.Fatalf("Next failed: %v", res.err)
		}
		if len(res.audio.Frame.Data) == 0 {
			t.Error("Expected audio data")
		}
	}
}

func TestCreateAccessToken(t *testing.T) {
	// Just verify it doesn't panic
	_, err := CreateAccessToken("key", "secret", 10*time.Second)
	if err != nil {
		t.Errorf("CreateAccessToken failed: %v", err)
	}
}
