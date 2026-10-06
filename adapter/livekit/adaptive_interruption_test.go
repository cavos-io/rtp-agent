package livekit

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/gorilla/websocket"
)

func TestAdaptiveInterruptionLiveKitStream(t *testing.T) {
	frames := make(chan []byte, 1)
	errors := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bargein" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			errors <- &testInterruptionError{"missing endpoint or auth"}
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			errors <- err
			return
		}
		defer conn.Close()
		_, payload, err := conn.ReadMessage()
		if err != nil {
			errors <- err
			return
		}
		var create struct {
			Type     string `json:"type"`
			Settings struct {
				SampleRate  int    `json:"sample_rate"`
				NumChannels int    `json:"num_channels"`
				Encoding    string `json:"encoding"`
			} `json:"settings"`
		}
		if err = json.Unmarshal(payload, &create); err != nil {
			errors <- err
			return
		}
		if create.Type != "session.create" || create.Settings.SampleRate != 16000 || create.Settings.NumChannels != 1 || create.Settings.Encoding != "s16le" {
			errors <- &testInterruptionError{"wrong session settings"}
			return
		}
		_ = conn.WriteJSON(map[string]any{"type": "session.created", "default_threshold": 0.5})
		for i := 0; i < 10; i++ {
			_, payload, err = conn.ReadMessage()
			if err != nil {
				errors <- err
				return
			}
			if i == 0 {
				frames <- payload
			}
			if i < 9 {
				_ = conn.WriteJSON(map[string]any{"type": "inference_done", "created_at": binary.LittleEndian.Uint64(payload[:8])})
			}
		}
		_ = conn.WriteJSON(map[string]any{"type": "bargein_detected", "created_at": binary.LittleEndian.Uint64(payload[:8]), "probabilities": []float64{0.9}})
	}))
	defer server.Close()
	detector := NewAdaptiveInterruptionDetector("key", "secret", WithAdaptiveInterruptionURL(server.URL))
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := stream.StartAgentSpeech(); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().Add(-100 * time.Millisecond)
	if err := stream.StartOverlap(startedAt); err != nil {
		t.Fatal(err)
	}
	frame := &model.AudioFrame{Data: make([]byte, 3200), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1600}
	for i := 0; i < 10; i++ {
		if err := stream.PushFrame(frame); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case payload := <-frames:
		if len(payload) < 8+3200 {
			t.Fatalf("binary payload = %d bytes, want timestamp and PCM", len(payload))
		}
	case err := <-errors:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("no audio sent")
	}
	event, err := stream.Next()
	if err != nil {
		t.Fatal(err)
	}
	if !event.IsInterruption || event.OverlapStartedAt == nil || !event.OverlapStartedAt.Equal(startedAt) || event.NumRequests != 10 {
		t.Fatalf("event = %#v", event)
	}
}

func TestAdaptiveInterruptionLiveKitTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, _, _ = conn.ReadMessage()
		_ = conn.WriteJSON(map[string]any{"type": "session.created", "default_threshold": 0.5})
		_, _, _ = conn.ReadMessage()
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := NewAdaptiveInterruptionDetector("key", "secret", WithAdaptiveInterruptionURL(server.URL)).Stream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := stream.StartAgentSpeech(); err != nil {
		t.Fatal(err)
	}
	if err := stream.StartOverlap(time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: make([]byte, 3200), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1600}); err != nil {
		t.Fatal(err)
	}
	_, err = stream.Next()
	if err == nil || err == context.DeadlineExceeded {
		t.Fatalf("Next error = %v, want inference timeout before context deadline", err)
	}
}

type testInterruptionError struct{ text string }

func (e *testInterruptionError) Error() string { return e.text }

func TestAdaptiveInterruptionBurstDoesNotDisableDetector(t *testing.T) {
	s := &adaptiveInterruptionStream{ctx: context.Background(), requestReady: make(chan struct{}, 1), pending: make(map[uint64]time.Time)}
	if err := s.StartAgentSpeech(); err != nil {
		t.Fatal(err)
	}
	if err := s.StartOverlap(time.Now()); err != nil {
		t.Fatal(err)
	}
	frame := &model.AudioFrame{Data: make([]byte, 3200), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1600}
	// RTC audio can arrive in a burst before the network writer is scheduled.
	for i := 0; i < 10; i++ {
		if err := s.PushFrame(frame); err != nil {
			t.Fatalf("buffered frame %d disabled detection: %v", i+1, err)
		}
	}
	if len(s.requests) != 10 || s.numRequests != 10 {
		t.Fatalf("queued=%d counted=%d, want 10", len(s.requests), s.numRequests)
	}
}

func TestAdaptiveInterruptionAudioBoundaries(t *testing.T) {
	s := &adaptiveInterruptionStream{ctx: context.Background(), requestReady: make(chan struct{}, 1), events: make(chan adaptiveResult, 8), pending: make(map[uint64]time.Time)}
	frame := &model.AudioFrame{Data: make([]byte, 640), SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 320}
	push := func(n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			if err := s.PushFrame(frame); err != nil {
				t.Fatal(err)
			}
		}
	}
	push(50)
	if len(s.buffer) != 0 || len(s.requests) != 0 {
		t.Fatal("idle audio entered detector")
	}
	if err := s.StartAgentSpeech(); err != nil {
		t.Fatal(err)
	}
	push(100)
	if err := s.StartOverlap(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	push(50)
	if len(s.requests) != 10 {
		t.Fatalf("1 second overlap sent %d requests, want 10", len(s.requests))
	}
	first := s.requests[0]
	if len(first) != 8+(adaptivePrefixSamples+adaptiveBatchSamples)*2 {
		t.Fatalf("first window bytes = %d", len(first))
	}
	s.requests = nil
	if err := s.EndAgentSpeech(time.Now()); err != nil {
		t.Fatal(err)
	}
	ev, err := s.Next()
	if err != nil || !ev.AgentEnded || ev.IsInterruption || ev.NumRequests != 10 {
		t.Fatalf("agent-ended verdict = %#v, %v", ev, err)
	}
	push(50)
	if len(s.buffer) != 0 || len(s.requests) != 0 {
		t.Fatal("audio continued after agent ended")
	}
	if err := s.StartAgentSpeech(); err != nil {
		t.Fatal(err)
	}
	if err := s.StartOverlap(time.Now()); err != nil {
		t.Fatal(err)
	}
	push(5)
	if payload := s.requests[0]; len(payload) != 8+adaptiveBatchSamples*2 {
		t.Fatalf("new speech retained old audio: %d bytes", len(payload))
	}
	s.requests = nil
	if err := s.EndOverlap(time.Now(), false); err != nil {
		t.Fatal(err)
	}
	ev, err = s.Next()
	if err != nil || ev.AgentEnded || ev.NumRequests != 1 {
		t.Fatalf("backchannel = %#v, %v", ev, err)
	}
	push(50)
	if len(s.requests) != 0 {
		t.Fatal("requests continued after user stopped")
	}
	if err := s.StartOverlap(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	push(5)
	if payload := s.requests[0]; len(payload) != 8+(adaptiveSampleRate+2*adaptiveBatchSamples)*2 {
		t.Fatalf("second overlap lost speech context: %d bytes", len(payload))
	}
}
