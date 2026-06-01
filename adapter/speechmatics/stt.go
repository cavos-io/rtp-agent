package speechmatics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sync"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/gorilla/websocket"
)

type SpeechmaticsSTT struct {
	apiKey string
}

func NewSpeechmaticsSTT(apiKey string) *SpeechmaticsSTT {
	return &SpeechmaticsSTT{
		apiKey: apiKey,
	}
}

func (s *SpeechmaticsSTT) Label() string { return "speechmatics.STT" }
func (s *SpeechmaticsSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: true, AlignedTranscript: "chunk", OfflineRecognize: false}
}

func (s *SpeechmaticsSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if language == "" {
		language = "en"
	}

	// Speechmatics API websocket URL
	u := url.URL{Scheme: "wss", Host: "en.rt.speechmatics.com", Path: "/v2"}

	header := make(map[string][]string)
	header["Authorization"] = []string{"Bearer " + s.apiKey}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), header)
	if err != nil {
		return nil, err
	}

	stream := &speechmaticsSTTStream{
		conn:   conn,
		events: make(chan *stt.SpeechEvent, 10),
		errCh:  make(chan error, 1),
	}

	// Initialize Speechmatics session
	initMsg := map[string]interface{}{
		"message": "StartRecognition",
		"audio_format": map[string]interface{}{
			"type":        "raw",
			"encoding":    "pcm_s16le",
			"sample_rate": 16000,
		},
		"transcription_config": map[string]interface{}{
			"language":        language,
			"enable_partials": true,
		},
	}

	if err := conn.WriteJSON(initMsg); err != nil {
		conn.Close()
		return nil, err
	}

	go stream.readLoop()

	return stream, nil
}

func (s *SpeechmaticsSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	return nil, fmt.Errorf("speechmatics offline recognize is not implemented")
}

type speechmaticsSTTStream struct {
	conn   *websocket.Conn
	events chan *stt.SpeechEvent
	errCh  chan error
	mu     sync.Mutex
	closed bool
}

type smResponse struct {
	Message  string `json:"message"`
	Metadata struct {
		Transcript string  `json:"transcript"`
		StartTime  float64 `json:"start_time"`
		EndTime    float64 `json:"end_time"`
	} `json:"metadata"`
	Results []struct {
		Alternatives []struct {
			Content    string  `json:"content"`
			Confidence float64 `json:"confidence"`
		} `json:"alternatives"`
		Type      string  `json:"type"`
		StartTime float64 `json:"start_time"`
		EndTime   float64 `json:"end_time"`
	} `json:"results"`
}

func (s *speechmaticsSTTStream) readLoop() {
	defer close(s.events)
	for {
		_, message, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				s.errCh <- err
			}
			return
		}

		var resp smResponse
		if err := json.Unmarshal(message, &resp); err != nil {
			continue
		}

		if resp.Message == "AddPartialTranscript" || resp.Message == "AddTranscript" {
			if event := speechmaticsTranscriptEvent(resp); event != nil {
				s.events <- event
			}
		} else if resp.Message == "EndOfTranscript" {
			return
		} else if resp.Message == "Error" {
			s.errCh <- fmt.Errorf("speechmatics error: %s", string(message))
			return
		}
	}
}

func speechmaticsTranscriptEvent(resp smResponse) *stt.SpeechEvent {
	eventType := stt.SpeechEventInterimTranscript
	if resp.Message == "AddTranscript" {
		eventType = stt.SpeechEventFinalTranscript
	}

	transcript := ""
	var totalConfidence float64
	var minStart, maxEnd float64
	hasTiming := false
	var words []stt.TimedString

	for _, result := range resp.Results {
		if len(result.Alternatives) == 0 {
			continue
		}
		alt := result.Alternatives[0]
		switch result.Type {
		case "word":
			transcript += alt.Content + " "
			words = append(words, stt.TimedString{
				Text:       alt.Content,
				StartTime:  result.StartTime,
				EndTime:    result.EndTime,
				Confidence: alt.Confidence,
			})
		case "punctuation":
			if transcript != "" {
				transcript = transcript[:len(transcript)-1] + alt.Content + " "
			} else {
				transcript = alt.Content + " "
			}
		}

		totalConfidence += alt.Confidence
		if !hasTiming {
			minStart = result.StartTime
			hasTiming = true
		}
		maxEnd = result.EndTime
	}

	if hasTiming {
		if transcript != "" {
			transcript = transcript[:len(transcript)-1]
		}
		return &stt.SpeechEvent{
			Type: eventType,
			Alternatives: []stt.SpeechData{
				{
					Text:       transcript,
					Confidence: totalConfidence / float64(len(resp.Results)),
					StartTime:  minStart,
					EndTime:    maxEnd,
					Words:      words,
				},
			},
		}
	}

	if resp.Metadata.Transcript == "" {
		return nil
	}
	return &stt.SpeechEvent{
		Type: eventType,
		Alternatives: []stt.SpeechData{
			{
				Text:       resp.Metadata.Transcript,
				Confidence: 1.0,
				StartTime:  resp.Metadata.StartTime,
				EndTime:    resp.Metadata.EndTime,
			},
		},
	}
}

func (s *speechmaticsSTTStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	// Speechmatics accepts raw binary audio frames
	return s.conn.WriteMessage(websocket.BinaryMessage, frame.Data)
}

func (s *speechmaticsSTTStream) Flush() error {
	return nil
}

func (s *speechmaticsSTTStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.conn.WriteJSON(map[string]interface{}{"message": "EndOfStream"})
	return s.conn.Close()
}

func (s *speechmaticsSTTStream) Next() (*stt.SpeechEvent, error) {
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
