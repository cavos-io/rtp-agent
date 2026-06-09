package gnani

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

const (
	defaultSTTLanguage       = "en-IN"
	defaultSTTSampleRate     = 16000
	gnaniSTTStreamChunkBytes = 1024
)

type STT struct {
	apiKey         string
	baseURL        string
	language       string
	sampleRate     int
	organizationID string
	userID         string
}

type STTOption func(*STT)

func WithSTTBaseURL(baseURL string) STTOption {
	return func(s *STT) {
		if baseURL != "" {
			s.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithSTTLanguage(language string) STTOption {
	return func(s *STT) {
		if language != "" {
			s.language = language
		}
	}
}

func WithSTTSampleRate(sampleRate int) STTOption {
	return func(s *STT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithSTTOrganizationID(organizationID string) STTOption {
	return func(s *STT) {
		s.organizationID = organizationID
	}
}

func WithSTTUserID(userID string) STTOption {
	return func(s *STT) {
		s.userID = userID
	}
}

func NewSTT(apiKey string, opts ...STTOption) *STT {
	provider := &STT{
		apiKey:     resolveGnaniAPIKey(apiKey),
		baseURL:    defaultBaseURL,
		language:   defaultSTTLanguage,
		sampleRate: defaultSTTSampleRate,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *STT) Label() string { return "gnani.STT" }
func (s *STT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: false, Diarization: false, OfflineRecognize: true}
}

func (s *STT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if err := validateGnaniSTTAPIKey(s.apiKey); err != nil {
		return nil, err
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildGnaniSTTWebsocketURL(s).String(), buildGnaniSTTWebsocketHeaders(s, language))
	if err != nil {
		return nil, fmt.Errorf("failed to dial gnani stt websocket: %w", err)
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &gnaniSTTStream{
		conn:     conn,
		ctx:      streamCtx,
		cancel:   cancel,
		language: resolveSTTLanguage(s, language),
		chunker:  newGnaniSTTAudioChunker(),
		events:   make(chan *stt.SpeechEvent, 100),
		errCh:    make(chan error, 1),
	}
	go stream.readLoop()
	return stream, nil
}

func buildGnaniSTTWebsocketURL(s *STT) *url.URL {
	baseURL := strings.TrimRight(s.baseURL, "/")
	switch {
	case strings.HasPrefix(baseURL, "https://"):
		baseURL = "wss://" + strings.TrimPrefix(baseURL, "https://")
	case strings.HasPrefix(baseURL, "http://"):
		baseURL = "ws://" + strings.TrimPrefix(baseURL, "http://")
	case !strings.HasPrefix(baseURL, "wss://") && !strings.HasPrefix(baseURL, "ws://"):
		baseURL = "wss://" + baseURL
	}
	wsURL, err := url.Parse(baseURL + "/stt/v3/stream")
	if err != nil {
		return &url.URL{Scheme: "wss", Host: strings.TrimPrefix(baseURL, "wss://"), Path: "/stt/v3/stream"}
	}
	return wsURL
}

func buildGnaniSTTWebsocketHeaders(s *STT, language string) http.Header {
	headers := make(http.Header)
	headers.Set("x-api-key-id", s.apiKey)
	headers.Set("lang_code", resolveSTTLanguage(s, language))
	return headers
}

func (s *STT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	if err := validateGnaniSTTAPIKey(s.apiKey); err != nil {
		return nil, err
	}

	var audio bytes.Buffer
	for _, frame := range frames {
		audio.Write(frame.Data)
	}

	req, err := buildSTTRequest(ctx, s, audio.Bytes(), language)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gnani stt error: %s", string(respBody))
	}

	var result gnaniSTTResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return gnaniSpeechEventFromResponse(result, resolveSTTLanguage(s, language))
}

func validateGnaniSTTAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("gnani API key is required, either as argument or set %s environment variable", gnaniAPIKeyEnv)
	}
	return nil
}

func buildSTTRequest(ctx context.Context, s *STT, audio []byte, language string) (*http.Request, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreatePart(textprotoMIMEHeader(map[string]string{
		"Content-Disposition": `form-data; name="audio_file"; filename="audio.wav"`,
		"Content-Type":        "audio/wav",
	}))
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(audio); err != nil {
		return nil, err
	}
	if err := writer.WriteField("language_code", resolveSTTLanguage(s, language)); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.baseURL, "/")+"/stt/v3", body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-API-Key-ID", s.apiKey)
	if s.organizationID != "" {
		req.Header.Set("X-Organization-ID", s.organizationID)
	}
	if s.userID != "" {
		req.Header.Set("X-API-User-ID", s.userID)
	}
	return req, nil
}

type gnaniSTTResponse struct {
	Transcript string `json:"transcript"`
	RequestID  string `json:"request_id"`
}

func gnaniSpeechEventFromResponse(resp gnaniSTTResponse, language string) (*stt.SpeechEvent, error) {
	return &stt.SpeechEvent{
		Type:      stt.SpeechEventFinalTranscript,
		RequestID: resp.RequestID,
		Alternatives: []stt.SpeechData{
			{
				Language:   language,
				Text:       resp.Transcript,
				Confidence: 1.0,
			},
		},
	}, nil
}

func resolveSTTLanguage(s *STT, language string) string {
	if language != "" {
		return language
	}
	return s.language
}

func textprotoMIMEHeader(values map[string]string) textproto.MIMEHeader {
	header := make(textproto.MIMEHeader, len(values))
	for key, value := range values {
		header.Set(key, value)
	}
	return header
}

type gnaniSTTStream struct {
	conn     *websocket.Conn
	ctx      context.Context
	cancel   context.CancelFunc
	language string
	chunker  *gnaniSTTAudioChunker
	events   chan *stt.SpeechEvent
	errCh    chan error
	mu       sync.Mutex
	closed   bool
}

func (s *gnaniSTTStream) PushFrame(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, chunk := range s.chunker.Push(frame.Data) {
		if err := s.conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
			return err
		}
	}
	return nil
}

func (s *gnaniSTTStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeBufferedChunksLocked()
}

func (s *gnaniSTTStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	_ = s.writeBufferedChunksLocked()
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *gnaniSTTStream) Next() (*stt.SpeechEvent, error) {
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

func (s *gnaniSTTStream) readLoop() {
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
		events, err := gnaniSTTEventsFromStreamMessage(payload, s.language)
		if err != nil {
			s.errCh <- err
			return
		}
		for _, event := range events {
			s.events <- event
		}
	}
}

func (s *gnaniSTTStream) writeBufferedChunksLocked() error {
	for _, chunk := range s.chunker.Flush() {
		if err := s.conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
			return err
		}
	}
	return nil
}

type gnaniSTTAudioChunker struct {
	buffer []byte
}

func newGnaniSTTAudioChunker() *gnaniSTTAudioChunker {
	return &gnaniSTTAudioChunker{}
}

func (c *gnaniSTTAudioChunker) Push(audio []byte) [][]byte {
	if len(audio) == 0 {
		return nil
	}
	c.buffer = append(c.buffer, audio...)
	var chunks [][]byte
	for len(c.buffer) >= gnaniSTTStreamChunkBytes {
		chunk := make([]byte, gnaniSTTStreamChunkBytes)
		copy(chunk, c.buffer[:gnaniSTTStreamChunkBytes])
		chunks = append(chunks, chunk)
		c.buffer = c.buffer[gnaniSTTStreamChunkBytes:]
	}
	return chunks
}

func (c *gnaniSTTAudioChunker) Flush() [][]byte {
	if len(c.buffer) == 0 {
		return nil
	}
	chunk := make([]byte, len(c.buffer))
	copy(chunk, c.buffer)
	c.buffer = c.buffer[:0]
	return [][]byte{chunk}
}

func gnaniSTTEventsFromStreamMessage(payload []byte, defaultLanguage string) ([]*stt.SpeechEvent, error) {
	var message struct {
		Type      string `json:"type"`
		Text      string `json:"text"`
		SegmentID string `json:"segment_id"`
		Message   string `json:"message"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, err
	}
	switch message.Type {
	case "transcript":
		if message.Text == "" {
			return nil, nil
		}
		return []*stt.SpeechEvent{{
			Type:      stt.SpeechEventFinalTranscript,
			RequestID: message.SegmentID,
			Alternatives: []stt.SpeechData{{
				Language:   defaultLanguage,
				Text:       message.Text,
				Confidence: 1.0,
			}},
		}}, nil
	case "speech_start", "vad_start":
		return []*stt.SpeechEvent{{Type: stt.SpeechEventStartOfSpeech}}, nil
	case "speech_end", "vad_end":
		return []*stt.SpeechEvent{{Type: stt.SpeechEventEndOfSpeech}}, nil
	case "error":
		if message.Message == "" {
			message.Message = string(payload)
		}
		return nil, fmt.Errorf("gnani stt stream error: %s", message.Message)
	default:
		return nil, nil
	}
}
