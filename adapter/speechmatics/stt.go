package speechmatics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sync"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

type SpeechmaticsSTT struct {
	apiKey            string
	language          string
	sampleRate        int
	audioEncoding     string
	domain            string
	outputLocale      string
	includePartials   *bool
	enableDiarization *bool
}

type SpeechmaticsSTTOption func(*SpeechmaticsSTT)

func WithSpeechmaticsSTTLanguage(language string) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		if language != "" {
			s.language = language
		}
	}
}

func WithSpeechmaticsSTTSampleRate(sampleRate int) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithSpeechmaticsSTTAudioEncoding(encoding string) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		if encoding != "" {
			s.audioEncoding = encoding
		}
	}
}

func WithSpeechmaticsSTTDomain(domain string) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.domain = domain
	}
}

func WithSpeechmaticsSTTOutputLocale(outputLocale string) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.outputLocale = outputLocale
	}
}

func WithSpeechmaticsSTTIncludePartials(enabled bool) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.includePartials = &enabled
	}
}

func WithSpeechmaticsSTTEnableDiarization(enabled bool) SpeechmaticsSTTOption {
	return func(s *SpeechmaticsSTT) {
		s.enableDiarization = &enabled
	}
}

func NewSpeechmaticsSTT(apiKey string, opts ...SpeechmaticsSTTOption) *SpeechmaticsSTT {
	provider := &SpeechmaticsSTT{
		apiKey:        apiKey,
		language:      "en",
		sampleRate:    16000,
		audioEncoding: "pcm_s16le",
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *SpeechmaticsSTT) Label() string { return "speechmatics.STT" }
func (s *SpeechmaticsSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: true, AlignedTranscript: "chunk", OfflineRecognize: false}
}

func (s *SpeechmaticsSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
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

	initMsg := buildSpeechmaticsSTTStartMessage(s, language)

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

func buildSpeechmaticsSTTStartMessage(s *SpeechmaticsSTT, language string) map[string]interface{} {
	if language == "" {
		language = s.language
	}
	config := map[string]interface{}{
		"language": language,
	}
	if s.includePartials != nil {
		config["enable_partials"] = *s.includePartials
	} else {
		config["enable_partials"] = true
	}
	if s.domain != "" {
		config["domain"] = s.domain
	}
	if s.outputLocale != "" {
		config["output_locale"] = s.outputLocale
	}
	if s.enableDiarization != nil {
		if *s.enableDiarization {
			config["diarization"] = "speaker"
		} else {
			config["diarization"] = "none"
		}
	}
	return map[string]interface{}{
		"message": "StartRecognition",
		"audio_format": map[string]interface{}{
			"type":        "raw",
			"encoding":    s.audioEncoding,
			"sample_rate": s.sampleRate,
		},
		"transcription_config": config,
	}
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
