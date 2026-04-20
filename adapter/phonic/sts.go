package phonic

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/gorilla/websocket"
)

type PhonicModel struct {
	apiKey       string
	voiceID      string
	systemPrompt string
}

func NewPhonicModel(apiKey string, voiceID string, systemPrompt string) *PhonicModel {
	if voiceID == "" {
		voiceID = "merritt" // Default model name as per docs
	}
	return &PhonicModel{
		apiKey:       apiKey,
		voiceID:      voiceID,
		systemPrompt: systemPrompt,
	}
}

func (m *PhonicModel) Session() (llm.RealtimeSession, error) {
	u := "wss://api.phonic.co/v1/sts/ws"
	header := make(http.Header)
	header.Set("Authorization", "Bearer "+m.apiKey)

	conn, _, err := websocket.DefaultDialer.Dial(u, header)
	if err != nil {
		return nil, fmt.Errorf("failed to dial phonic websocket: %w", err)
	}

	session := &phonicSession{
		conn:    conn,
		eventCh: make(chan llm.RealtimeEvent, 100),
		model:   m,
	}

	// Send initial config
	configMsg := map[string]interface{}{
		"type": "config",
		"config": map[string]interface{}{
			"model":         m.voiceID,
			"input_format":  "pcm_16000",
			"output_format": "pcm_24000",
			"system_prompt": m.systemPrompt,
		},
	}
	if err := conn.WriteJSON(configMsg); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to send phonic config: %w", err)
	}

	go session.readLoop()
	return session, nil
}

func (m *PhonicModel) Close() error { return nil }

type phonicSession struct {
	conn    *websocket.Conn
	eventCh chan llm.RealtimeEvent
	mu      sync.Mutex
	closed  bool
	model   *PhonicModel
}

func (s *phonicSession) readLoop() {
	defer close(s.eventCh)
	for {
		_, message, err := s.conn.ReadMessage()
		if err != nil {
			if !s.closed {
				s.eventCh <- llm.RealtimeEvent{Type: llm.RealtimeEventTypeError, Error: err}
			}
			return
		}

		var msg map[string]interface{}
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}

		msgType, _ := msg["type"].(string)
		switch msgType {
		case "audio_chunk":
			audioBase64, _ := msg["audio"].(string)
			data, _ := base64.StdEncoding.DecodeString(audioBase64)
			s.eventCh <- llm.RealtimeEvent{
				Type: llm.RealtimeEventTypeAudio,
				Data: data,
			}
		case "user_started_speaking":
			s.eventCh <- llm.RealtimeEvent{Type: llm.RealtimeEventTypeSpeechStarted}
		case "user_finished_speaking":
			s.eventCh <- llm.RealtimeEvent{Type: llm.RealtimeEventTypeSpeechStopped}
		case "input_text":
			text, _ := msg["text"].(string)
			s.eventCh <- llm.RealtimeEvent{
				Type: llm.RealtimeEventTypeText,
				Text: text,
			}
		case "ready_to_start_conversation":
			logger.Logger.Infow("Phonic session ready")
		}
	}
}

func (s *phonicSession) PushAudio(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("session closed")
	}

	// Phonic expects 16kHz PCM for input in our config
	// Basic implementation: assuming input is already 16kHz or handled by resampler before calling
	audioBase64 := base64.StdEncoding.EncodeToString(frame.Data)
	msg := map[string]interface{}{
		"type":  "audio_chunk",
		"audio": audioBase64,
	}
	return s.conn.WriteJSON(msg)
}

func (s *phonicSession) UpdateInstructions(instructions string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg := map[string]interface{}{
		"type": "update_config",
		"config": map[string]interface{}{
			"system_prompt": instructions,
		},
	}
	return s.conn.WriteJSON(msg)
}

func (s *phonicSession) UpdateChatContext(chatCtx *llm.ChatContext) error {
	// Phonic manages its own context, but we could send a full context update if supported
	return nil
}

func (s *phonicSession) UpdateTools(tools []interface{}) error {
	// Phonic supports tool calling, would need to map Go tools to their schema
	return nil
}

func (s *phonicSession) Interrupt() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg := map[string]interface{}{
		"type": "interrupt",
	}
	return s.conn.WriteJSON(msg)
}

func (s *phonicSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return s.conn.Close()
}

func (s *phonicSession) EventCh() <-chan llm.RealtimeEvent {
	return s.eventCh
}
