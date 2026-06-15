package telnyx

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

const (
	defaultTelnyxSTTBaseURL             = "wss://api.telnyx.com/v2/speech-to-text/transcription"
	defaultTelnyxSTTLanguage            = "en"
	defaultTelnyxSTTTranscriptionEngine = "telnyx"
	defaultTelnyxSTTSampleRate          = 16000
	telnyxSTTNumChannels                = 1
	telnyxAPIKeyEnv                     = "TELNYX_API_KEY"
)

type TelnyxSTT struct {
	apiKey              string
	baseURL             string
	language            string
	transcriptionEngine string
	interimResults      bool
	sampleRate          int
}

type TelnyxSTTOption func(*TelnyxSTT)

func WithTelnyxSTTBaseURL(baseURL string) TelnyxSTTOption {
	return func(s *TelnyxSTT) {
		if baseURL != "" {
			s.baseURL = baseURL
		}
	}
}

func WithTelnyxSTTLanguage(language string) TelnyxSTTOption {
	return func(s *TelnyxSTT) {
		if language != "" {
			s.language = language
		}
	}
}

func WithTelnyxSTTTranscriptionEngine(engine string) TelnyxSTTOption {
	return func(s *TelnyxSTT) {
		if engine != "" {
			s.transcriptionEngine = engine
		}
	}
}

func WithTelnyxSTTSampleRate(sampleRate int) TelnyxSTTOption {
	return func(s *TelnyxSTT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func NewTelnyxSTT(apiKey string, opts ...TelnyxSTTOption) *TelnyxSTT {
	if apiKey == "" {
		apiKey = os.Getenv(telnyxAPIKeyEnv)
	}
	provider := &TelnyxSTT{
		apiKey:              apiKey,
		baseURL:             defaultTelnyxSTTBaseURL,
		language:            defaultTelnyxSTTLanguage,
		transcriptionEngine: defaultTelnyxSTTTranscriptionEngine,
		interimResults:      true,
		sampleRate:          defaultTelnyxSTTSampleRate,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *TelnyxSTT) Label() string { return "telnyx.STT" }
func (s *TelnyxSTT) Model() string { return s.transcriptionEngine }
func (s *TelnyxSTT) Provider() string {
	return "telnyx"
}

func (s *TelnyxSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{
		Streaming:        true,
		InterimResults:   s.interimResults,
		Diarization:      false,
		OfflineRecognize: true,
	}
}

func (s *TelnyxSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if err := validateTelnyxAPIKey(s.apiKey); err != nil {
		return nil, err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildTelnyxSTTStreamURL(s, language), buildTelnyxSTTHeaders(s))
	if err != nil {
		return nil, fmt.Errorf("failed to dial telnyx stt websocket: %w", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, createTelnyxStreamingWAVHeader(s.sampleRate, telnyxSTTNumChannels)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &telnyxSTTStream{
		conn:   conn,
		events: make(chan *stt.SpeechEvent, 100),
		errCh:  make(chan error, 1),
		ctx:    streamCtx,
		cancel: cancel,
		state: &telnyxSTTStreamState{
			language: resolveTelnyxSTTLanguage(s, language),
		},
	}
	go stream.readLoop()
	return stream, nil
}

func (s *TelnyxSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	stream, err := s.Stream(ctx, language)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	for _, frame := range frames {
		if err := stream.PushFrame(frame); err != nil {
			return nil, err
		}
	}
	if err := stream.Flush(); err != nil {
		return nil, err
	}
	var finalText bytes.Buffer
	resolvedLanguage := resolveTelnyxSTTLanguage(s, language)
	for {
		event, err := stream.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		if event.Type == stt.SpeechEventFinalTranscript && len(event.Alternatives) > 0 {
			finalText.WriteString(event.Alternatives[0].Text)
			break
		}
	}
	return &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{{
			Language:   resolvedLanguage,
			Text:       finalText.String(),
			Confidence: stt.DefaultTranscriptConfidence(finalText.String()),
		}},
	}, nil
}

func validateTelnyxAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("telnyx API key required. Set TELNYX_API_KEY or provide api_key")
	}
	return nil
}

func buildTelnyxSTTStreamURL(s *TelnyxSTT, language string) string {
	u, err := url.Parse(s.baseURL)
	if err != nil {
		return s.baseURL
	}
	q := u.Query()
	q.Set("transcription_engine", s.transcriptionEngine)
	q.Set("language", resolveTelnyxSTTLanguage(s, language))
	q.Set("input_format", "wav")
	u.RawQuery = q.Encode()
	return u.String()
}

func buildTelnyxSTTHeaders(s *TelnyxSTT) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+s.apiKey)
	return headers
}

func resolveTelnyxSTTLanguage(s *TelnyxSTT, language string) string {
	if language != "" {
		return language
	}
	return s.language
}

func createTelnyxStreamingWAVHeader(sampleRate int, numChannels int) []byte {
	bytesPerSample := 2
	byteRate := sampleRate * numChannels * bytesPerSample
	blockAlign := numChannels * bytesPerSample
	dataSize := uint32(0x7fffffff)
	fileSize := uint32(36) + dataSize

	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], fileSize)
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], uint16(numChannels))
	binary.LittleEndian.PutUint32(header[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(header[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(header[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(header[34:36], 16)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], dataSize)
	return header
}

type telnyxSTTStream struct {
	conn   *websocket.Conn
	events chan *stt.SpeechEvent
	errCh  chan error
	mu     sync.Mutex
	closed bool
	ctx    context.Context
	cancel context.CancelFunc
	state  *telnyxSTTStreamState
}

func (s *telnyxSTTStream) PushFrame(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, frame.Data)
}

func (s *telnyxSTTStream) Flush() error {
	return nil
}

func (s *telnyxSTTStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *telnyxSTTStream) Next() (*stt.SpeechEvent, error) {
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
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

func (s *telnyxSTTStream) readLoop() {
	defer close(s.events)
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) && err != io.EOF {
				s.errCh <- err
			}
			return
		}
		if msgType != websocket.TextMessage {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal(payload, &data); err != nil {
			s.errCh <- err
			return
		}
		events, err := processTelnyxSTTEvent(s.state, data)
		if err != nil {
			s.errCh <- err
			return
		}
		for _, event := range events {
			s.events <- event
		}
	}
}

type telnyxSTTStreamState struct {
	language string
	speaking bool
}

func processTelnyxSTTEvent(state *telnyxSTTStreamState, data map[string]any) ([]*stt.SpeechEvent, error) {
	transcript, _ := data["transcript"].(string)
	if transcript == "" {
		return nil, nil
	}
	events := []*stt.SpeechEvent{}
	if !state.speaking {
		state.speaking = true
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech})
	}
	alternative := stt.SpeechData{
		Language:   state.language,
		Text:       transcript,
		Confidence: telnyxAnyFloat(data["confidence"]),
	}
	isFinal, _ := data["is_final"].(bool)
	if isFinal {
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventFinalTranscript, Alternatives: []stt.SpeechData{alternative}})
		state.speaking = false
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech})
		return events, nil
	}
	events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventInterimTranscript, Alternatives: []stt.SpeechData{alternative}})
	return events, nil
}

func telnyxAnyFloat(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	default:
		return 0
	}
}
