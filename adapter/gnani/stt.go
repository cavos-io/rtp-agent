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
	defaultSTTLanguage   = "en-IN"
	defaultSTTSampleRate = 16000
	sttStreamChunkBytes  = 1024
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
		apiKey:     apiKey,
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
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildSTTWebsocketURL(s), buildSTTWebsocketHeaders(s, language))
	if err != nil {
		return nil, fmt.Errorf("failed to dial gnani stt websocket: %w", err)
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &sttStream{
		conn:       conn,
		ctx:        streamCtx,
		cancel:     cancel,
		language:   resolveSTTLanguage(s, language),
		sampleRate: s.sampleRate,
		chunkBytes: sttStreamChunkBytes,
		events:     make(chan *stt.SpeechEvent, 100),
		errCh:      make(chan error, 1),
	}
	go stream.readLoop()
	return stream, nil
}

func buildSTTWebsocketURL(s *STT) string {
	u, err := url.Parse(strings.TrimRight(s.baseURL, "/"))
	if err != nil {
		return strings.TrimRight(s.baseURL, "/") + "/stt/v3/stream"
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	case "":
		u.Scheme = "wss"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/stt/v3/stream"
	u.RawQuery = ""
	return u.String()
}

func buildSTTWebsocketHeaders(s *STT, language string) http.Header {
	headers := make(http.Header)
	headers.Set("x-api-key-id", s.apiKey)
	headers.Set("lang_code", resolveSTTLanguage(s, language))
	return headers
}

func (s *STT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
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

type sttStream struct {
	conn         *websocket.Conn
	ctx          context.Context
	cancel       context.CancelFunc
	language     string
	sampleRate   int
	chunkBytes   int
	pendingAudio []byte
	events       chan *stt.SpeechEvent
	errCh        chan error
	mu           sync.Mutex
	closed       bool
}

func (s *sttStream) PushFrame(frame *model.AudioFrame) error {
	for _, chunk := range s.chunksForFrame(frame) {
		if err := s.conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
			return err
		}
	}
	return nil
}

func (s *sttStream) Flush() error {
	for _, chunk := range s.flushPendingAudio() {
		if err := s.conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
			return err
		}
	}
	return nil
}

func (s *sttStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	_ = s.Flush()
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *sttStream) Next() (*stt.SpeechEvent, error) {
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

func (s *sttStream) chunksForFrame(frame *model.AudioFrame) [][]byte {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	chunkBytes := s.chunkBytes
	if chunkBytes <= 0 {
		chunkBytes = sttStreamChunkBytes
	}
	s.pendingAudio = append(s.pendingAudio, frame.Data...)
	chunks := make([][]byte, 0, len(s.pendingAudio)/chunkBytes)
	for len(s.pendingAudio) >= chunkBytes {
		chunk := bytes.Clone(s.pendingAudio[:chunkBytes])
		chunks = append(chunks, chunk)
		s.pendingAudio = s.pendingAudio[chunkBytes:]
	}
	return chunks
}

func (s *sttStream) flushPendingAudio() [][]byte {
	if len(s.pendingAudio) == 0 {
		return nil
	}
	chunk := bytes.Clone(s.pendingAudio)
	s.pendingAudio = nil
	return [][]byte{chunk}
}

func (s *sttStream) readLoop() {
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
		event, err := sttEventFromWebsocketMessage(payload, s.language)
		if err != nil {
			s.errCh <- err
			return
		}
		if event != nil {
			s.events <- event
		}
	}
}

func sttEventFromWebsocketMessage(payload []byte, language string) (*stt.SpeechEvent, error) {
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
		return &stt.SpeechEvent{
			Type:      stt.SpeechEventFinalTranscript,
			RequestID: message.SegmentID,
			Alternatives: []stt.SpeechData{{
				Language:   language,
				Text:       message.Text,
				Confidence: 1.0,
			}},
		}, nil
	case "speech_start", "vad_start":
		return &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech}, nil
	case "speech_end", "vad_end":
		return &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech}, nil
	case "processing", "connected", "":
		return nil, nil
	case "error":
		if message.Message == "" {
			message.Message = "unknown gnani stt stream error"
		}
		return nil, fmt.Errorf("gnani stt stream error: %s", message.Message)
	default:
		return nil, nil
	}
}

func textprotoMIMEHeader(values map[string]string) textproto.MIMEHeader {
	header := make(textproto.MIMEHeader, len(values))
	for key, value := range values {
		header.Set(key, value)
	}
	return header
}
