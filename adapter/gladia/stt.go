package gladia

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/gorilla/websocket"
)

type GladiaSTT struct {
	apiKey string
	apiURL string
}

type STTOption func(*GladiaSTT)

func WithSTTBaseURL(url string) STTOption {
	return func(s *GladiaSTT) {
		s.apiURL = url
	}
}

func NewGladiaSTT(apiKey string, opts ...STTOption) *GladiaSTT {
	s := &GladiaSTT{
		apiKey: apiKey,
		apiURL: "wss://api.gladia.io/audio/text/audio-transcription",
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *GladiaSTT) Label() string { return "gladia.STT" }
func (s *GladiaSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: false, OfflineRecognize: false}
}

func (s *GladiaSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if language == "" {
		language = "en"
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, s.apiURL, nil)
	if err != nil {
		return nil, err
	}

	// Initialize Gladia session
	initMsg := map[string]interface{}{
		"x_gladia_key": s.apiKey,
		"language_behavior": "automatic single language",
		"sample_rate": 16000,
		"encoding": "wav/pcm",
	}

	if err := conn.WriteJSON(initMsg); err != nil {
		conn.Close()
		return nil, err
	}

	stream := &gladiaSTTStream{
		conn:   conn,
		events: make(chan *stt.SpeechEvent, 10),
		errCh:  make(chan error, 1),
	}

	go stream.readLoop()

	return stream, nil
}

func (s *GladiaSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	return nil, fmt.Errorf("offline recognize is not natively supported by GladiaSTT via simple upload. Use Stream instead")
}

type gladiaSTTStream struct {
	conn   *websocket.Conn
	events chan *stt.SpeechEvent
	errCh  chan error
	mu     sync.Mutex
	closed bool
}

type gladiaResponse struct {
	Type        string `json:"type"`
	Transcription string `json:"transcription"`
	Confidence  float64 `json:"confidence"`
	Language    string `json:"language"`
	TimeBegin   float64 `json:"time_begin"`
	TimeEnd     float64 `json:"time_end"`
}

func (s *gladiaSTTStream) readLoop() {
	defer close(s.events)
	for {
		_, message, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				s.errCh <- err
			}
			return
		}

		var resp gladiaResponse
		if err := json.Unmarshal(message, &resp); err != nil {
			continue
		}

		if resp.Type == "final" || resp.Type == "partial" {
			eventType := stt.SpeechEventInterimTranscript
			if resp.Type == "final" {
				eventType = stt.SpeechEventFinalTranscript
			}

			s.events <- &stt.SpeechEvent{
				Type: eventType,
				Alternatives: []stt.SpeechData{
					{
						Text:       resp.Transcription,
						Confidence: resp.Confidence,
						StartTime:  resp.TimeBegin,
						EndTime:    resp.TimeEnd,
					},
				},
			}
		}
	}
}

func (s *gladiaSTTStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}

	// Gladia expects raw base64 or binary. For websocket we can send binary pcm blocks.
	// Gladia requires audio to be sent as JSON in some cases:
	// {"frames": "base64encoded..."}
	// Let's assume standard binary payload is supported or wrap it:
	// Actually, Gladia WebSocket requires base64 encoded chunks in JSON
	b64 := base64.StdEncoding.EncodeToString(frame.Data)
	msg := map[string]interface{}{
		"frames": b64,
	}

	return s.conn.WriteJSON(msg)
}

func (s *gladiaSTTStream) Flush() error {
	return nil
}

func (s *gladiaSTTStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.conn.Close()
}

func (s *gladiaSTTStream) Next() (*stt.SpeechEvent, error) {
	select {
	case event, ok := <-s.events:
		if !ok {
			select {
			case err := <-s.errCh:
				return nil, err
			default:
				return nil, io.EOF
			}
		}
		return event, nil
	case err := <-s.errCh:
		return nil, err
	}
}

