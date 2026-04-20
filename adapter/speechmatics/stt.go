package speechmatics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
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
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: false, OfflineRecognize: true}
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
			"type": "raw",
			"encoding": "pcm_s16le",
			"sample_rate": 16000,
		},
		"transcription_config": map[string]interface{}{
			"language": language,
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
	url := "https://asr.api.speechmatics.com/v2/jobs"

	var buf bytes.Buffer
	for _, f := range frames {
		buf.Write(f.Data)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("data_file", "audio.wav")
	part.Write(buf.Bytes())
	
	config := map[string]interface{}{
		"type": "transcription",
		"transcription_config": map[string]interface{}{
			"language": "en",
		},
	}
	if language != "" {
		config["transcription_config"].(map[string]interface{})["language"] = language
	}
	configBytes, _ := json.Marshal(config)
	writer.WriteField("config", string(configBytes))
	writer.Close()

	req, _ := http.NewRequestWithContext(ctx, "POST", url, body)
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		ID string `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	return &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			{Text: fmt.Sprintf("[Speechmatics Job ID: %s]", result.ID)},
		},
	}, nil
}

type speechmaticsSTTStream struct {
	conn   *websocket.Conn
	events chan *stt.SpeechEvent
	errCh  chan error
	mu     sync.Mutex
	closed bool
}

type smResponse struct {
	Message string `json:"message"`
	Metadata struct {
		Transcript string  `json:"transcript"`
		StartTime  float64 `json:"start_time"`
		EndTime    float64 `json:"end_time"`
	} `json:"metadata"`
	Results []struct {
		Alternatives []struct {
			Content string `json:"content"`
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
			eventType := stt.SpeechEventInterimTranscript
			if resp.Message == "AddTranscript" {
				eventType = stt.SpeechEventFinalTranscript
			}

			// Concatenate all word alternatives
			transcript := ""
			var totalConfidence float64
			var minStart, maxEnd float64
			hasWords := false

			for _, result := range resp.Results {
				if len(result.Alternatives) > 0 {
					alt := result.Alternatives[0]
					if result.Type == "word" {
						transcript += alt.Content + " "
					} else if result.Type == "punctuation" {
						transcript = transcript[:len(transcript)-1] + alt.Content + " "
					}
					
					totalConfidence += alt.Confidence
					
					if !hasWords {
						minStart = result.StartTime
						hasWords = true
					}
					maxEnd = result.EndTime
				}
			}

			if hasWords {
				s.events <- &stt.SpeechEvent{
					Type: eventType,
					Alternatives: []stt.SpeechData{
						{
							Text:       transcript[:len(transcript)-1], // trim trailing space
							Confidence: totalConfidence / float64(len(resp.Results)),
							StartTime:  minStart,
							EndTime:    maxEnd,
						},
					},
				}
			} else if resp.Metadata.Transcript != "" {
				s.events <- &stt.SpeechEvent{
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
		} else if resp.Message == "EndOfTranscript" {
			return
		} else if resp.Message == "Error" {
			s.errCh <- fmt.Errorf("speechmatics error: %s", string(message))
			return
		}
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

