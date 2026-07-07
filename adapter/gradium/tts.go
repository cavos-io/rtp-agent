package gradium

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/library/tokenize"
	"github.com/gorilla/websocket"
)

const (
	defaultTTSModelEndpoint = "wss://api.gradium.ai/api/speech/tts"
	defaultTTSModelName     = "default"
	defaultTTSVoiceID       = "YTpq7expH9539ERJ"
	gradiumTTSSampleRate    = 48000
)

type GradiumTTS struct {
	apiKey          string
	modelEndpoint   string
	modelName       string
	voice           string
	voiceID         string
	pronunciationID string
	jsonConfig      map[string]any
}

type GradiumTTSOption func(*GradiumTTS)

func WithGradiumTTSModelEndpoint(endpoint string) GradiumTTSOption {
	return func(t *GradiumTTS) {
		if endpoint != "" {
			t.modelEndpoint = strings.TrimRight(endpoint, "/")
		}
	}
}

func WithGradiumTTSModelName(modelName string) GradiumTTSOption {
	return func(t *GradiumTTS) {
		if modelName != "" {
			t.modelName = modelName
		}
	}
}

func WithGradiumTTSVoiceID(voiceID string) GradiumTTSOption {
	return func(t *GradiumTTS) {
		t.voiceID = voiceID
	}
}

func WithGradiumTTSPronunciationID(pronunciationID string) GradiumTTSOption {
	return func(t *GradiumTTS) {
		t.pronunciationID = pronunciationID
	}
}

func WithGradiumTTSJSONConfig(jsonConfig map[string]any) GradiumTTSOption {
	return func(t *GradiumTTS) {
		t.jsonConfig = jsonConfig
	}
}

func NewGradiumTTS(apiKey string, voice string, opts ...GradiumTTSOption) *GradiumTTS {
	provider := &GradiumTTS{
		apiKey:        resolveGradiumAPIKey(apiKey),
		modelEndpoint: defaultTTSModelEndpoint,
		modelName:     defaultTTSModelName,
		voice:         voice,
		voiceID:       defaultTTSVoiceID,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (t *GradiumTTS) Label() string { return "gradium.TTS" }
func (t *GradiumTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *GradiumTTS) SampleRate() int  { return gradiumTTSSampleRate }
func (t *GradiumTTS) NumChannels() int { return 1 }
func (t *GradiumTTS) Model() string    { return "unknown" }
func (t *GradiumTTS) Provider() string { return "Gradium" }

func (t *GradiumTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if err := validateGradiumAPIKey(t.apiKey); err != nil {
		return nil, err
	}

	return &gradiumTTSWebsocketChunkedStream{
		ctx:           ctx,
		modelEndpoint: t.modelEndpoint,
		headers:       buildGradiumTTSHeaders(t),
		setup:         mustBuildGradiumTTSSetup(t),
		text:          text,
		sampleRate:    t.SampleRate(),
	}, nil
}

func buildGradiumTTSHeaders(t *GradiumTTS) http.Header {
	headers := make(http.Header)
	headers.Set("x-api-key", t.apiKey)
	headers.Set("x-api-source", "livekit")
	return headers
}

func buildGradiumTTSSetup(t *GradiumTTS) (map[string]any, error) {
	setup := map[string]any{
		"type":          "setup",
		"model_name":    t.modelName,
		"output_format": "pcm",
	}
	if t.voice != "" {
		setup["voice"] = t.voice
	}
	if t.voiceID != "" {
		setup["voice_id"] = t.voiceID
	}
	if t.pronunciationID != "" {
		setup["pronunciation_id"] = t.pronunciationID
	}
	if t.jsonConfig != nil {
		payload, err := json.Marshal(t.jsonConfig)
		if err != nil {
			return nil, err
		}
		setup["json_config"] = string(payload)
	}
	return setup, nil
}

func mustBuildGradiumTTSSetup(t *GradiumTTS) map[string]any {
	setup, err := buildGradiumTTSSetup(t)
	if err != nil {
		return map[string]any{}
	}
	return setup
}

func buildGradiumTTSTextMessage(text string) map[string]any {
	return map[string]any{"type": "text", "text": text}
}

func buildGradiumTTSEndMessage() map[string]any {
	return map[string]any{"type": "end_of_stream"}
}

func writeGradiumTTSMessage(conn *websocket.Conn, message map[string]any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, payload)
}

func (t *GradiumTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if err := validateGradiumAPIKey(t.apiKey); err != nil {
		return nil, err
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, t.modelEndpoint, buildGradiumTTSHeaders(t))
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("failed to dial gradium tts websocket: %v", err))
	}
	if err := writeGradiumTTSMessage(conn, mustBuildGradiumTTSSetup(t)); err != nil {
		conn.Close()
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	return &gradiumTTSSynthesizeStream{
		conn:       conn,
		ctx:        streamCtx,
		cancel:     cancel,
		sampleRate: t.SampleRate(),
	}, nil
}

func validateGradiumAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("gradium API key is required; pass api_key or set GRADIUM_API_KEY environment variable")
	}
	return nil
}

type gradiumTTSWebsocketChunkedStream struct {
	ctx           context.Context
	modelEndpoint string
	headers       http.Header
	setup         map[string]any
	text          string
	conn          *websocket.Conn
	sampleRate    int
	completed     bool
	started       bool
	closed        bool
}

func (s *gradiumTTSWebsocketChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.completed {
		return nil, io.EOF
	}
	if s.closed {
		return nil, io.EOF
	}
	if err := s.ensureConnected(); err != nil {
		return nil, err
	}
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			var closeErr *websocket.CloseError
			if errors.As(err, &closeErr) || err == io.EOF {
				s.completed = true
				return &tts.SynthesizedAudio{IsFinal: true}, nil
			}
			return nil, err
		}
		if msgType == websocket.CloseMessage {
			s.completed = true
			return &tts.SynthesizedAudio{IsFinal: true}, nil
		}
		if msgType != websocket.TextMessage {
			continue
		}
		audio, done, err := gradiumTTSAudioFromMessage(payload, s.sampleRate)
		if err != nil {
			return nil, err
		}
		if audio != nil {
			if done {
				s.completed = true
			}
			return audio, nil
		}
		if done {
			s.completed = true
			return &tts.SynthesizedAudio{IsFinal: true}, nil
		}
	}
}

func (s *gradiumTTSWebsocketChunkedStream) ensureConnected() error {
	if s.started {
		return nil
	}
	if s.conn != nil {
		s.started = true
		return nil
	}
	s.started = true
	if s.conn != nil {
		return nil
	}
	conn, _, err := websocket.DefaultDialer.DialContext(s.ctx, s.modelEndpoint, s.headers)
	if err != nil {
		s.closed = true
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		return llm.NewAPIConnectionError(fmt.Sprintf("failed to dial gradium tts websocket: %v", err))
	}
	setup, err := json.Marshal(s.setup)
	if err != nil {
		_ = conn.Close()
		s.closed = true
		return err
	}
	if err := conn.WriteMessage(websocket.TextMessage, setup); err != nil {
		_ = conn.Close()
		s.closed = true
		return err
	}
	if err := writeGradiumTTSMessage(conn, buildGradiumTTSTextMessage(s.text)); err != nil {
		_ = conn.Close()
		s.closed = true
		return err
	}
	if err := writeGradiumTTSMessage(conn, buildGradiumTTSEndMessage()); err != nil {
		_ = conn.Close()
		s.closed = true
		return err
	}
	s.conn = conn
	return nil
}

func (s *gradiumTTSWebsocketChunkedStream) Close() error {
	s.closed = true
	if s.conn == nil {
		return nil
	}
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

type gradiumTTSSynthesizeStream struct {
	conn       *websocket.Conn
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	closed     bool
	inputEnded bool
	sampleRate int
	finalDone  bool
	pending    string

	writeMessage func(map[string]any) error
}

func (s *gradiumTTSSynthesizeStream) PushText(text string) error {
	if text == "" {
		return nil
	}
	if s.isInputClosed() {
		return io.ErrClosedPipe
	}
	s.pending += text
	tokens := tokenize.SplitWords(s.pending, false, false, false)
	if len(tokens) <= 1 {
		return nil
	}
	for _, token := range tokens[:len(tokens)-1] {
		if err := s.writeMessageData(buildGradiumTTSTextMessage(token.Token + " ")); err != nil {
			return err
		}
		s.consumePendingToken(token.Token)
	}
	return nil
}

func (s *gradiumTTSSynthesizeStream) Flush() error {
	s.mu.Lock()
	if s.closed || s.inputEnded {
		s.mu.Unlock()
		return io.ErrClosedPipe
	}
	s.finalDone = false
	s.mu.Unlock()
	return s.flushPendingWords()
}

func (s *gradiumTTSSynthesizeStream) EndInput() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return io.ErrClosedPipe
	}
	if s.inputEnded {
		s.mu.Unlock()
		return nil
	}
	s.finalDone = false
	s.mu.Unlock()
	if err := s.flushPendingWords(); err != nil {
		return err
	}
	if err := s.writeMessageData(buildGradiumTTSEndMessage()); err != nil {
		return err
	}
	s.mu.Lock()
	s.inputEnded = true
	s.mu.Unlock()
	return nil
}

func (s *gradiumTTSSynthesizeStream) flushPendingWords() error {
	tokens := tokenize.SplitWords(s.pending, false, false, false)
	for _, token := range tokens {
		if err := s.writeMessageData(buildGradiumTTSTextMessage(token.Token + " ")); err != nil {
			return err
		}
	}
	s.pending = ""
	return nil
}

func (s *gradiumTTSSynthesizeStream) consumePendingToken(token string) {
	tokenIdx := strings.Index(s.pending, token)
	if tokenIdx < 0 {
		s.pending = strings.TrimSpace(strings.TrimPrefix(s.pending, token))
		return
	}
	s.pending = strings.TrimLeftFunc(s.pending[tokenIdx+len(token):], func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
}

func (s *gradiumTTSSynthesizeStream) writeMessageData(message map[string]any) error {
	if s.isClosed() {
		return io.ErrClosedPipe
	}
	if s.writeMessage != nil {
		return s.writeMessage(message)
	}
	return writeGradiumTTSMessage(s.conn, message)
}

func (s *gradiumTTSSynthesizeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	if s.conn == nil {
		return nil
	}
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *gradiumTTSSynthesizeStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *gradiumTTSSynthesizeStream) isInputClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed || s.inputEnded
}

func (s *gradiumTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
	if s.isClosed() {
		return nil, io.EOF
	}
	select {
	case <-s.ctx.Done():
		if s.isClosed() {
			return nil, io.EOF
		}
		return nil, s.ctx.Err()
	default:
	}
	s.mu.Lock()
	if s.finalDone {
		s.mu.Unlock()
		return nil, io.EOF
	}
	conn := s.conn
	sampleRate := s.sampleRate
	s.mu.Unlock()

	audio, err := (&gradiumTTSWebsocketChunkedStream{conn: conn, sampleRate: sampleRate}).Next()
	if err != nil {
		return nil, err
	}
	if audio != nil && audio.IsFinal {
		s.mu.Lock()
		s.finalDone = true
		s.mu.Unlock()
	}
	return audio, nil
}

func gradiumTTSAudioFromMessage(payload []byte, sampleRate int) (*tts.SynthesizedAudio, bool, error) {
	var message struct {
		Type  string `json:"type"`
		Audio string `json:"audio"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, false, llm.NewAPIConnectionError(err.Error())
	}
	switch message.Type {
	case "audio":
		audio, err := gradiumDecodeBase64Audio(message.Audio)
		if err != nil {
			return nil, false, llm.NewAPIConnectionError(err.Error())
		}
		if len(audio) == 0 {
			return nil, false, nil
		}
		return gradiumTTSAudioFrame(audio, sampleRate), false, nil
	case "end_of_stream":
		return &tts.SynthesizedAudio{IsFinal: true}, true, nil
	default:
		return nil, false, nil
	}
}

func gradiumDecodeBase64Audio(data string) ([]byte, error) {
	clean := make([]byte, 0, len(data))
	dataChars := 0
	for i := 0; i < len(data); i++ {
		b := data[i]
		switch {
		case b >= 'A' && b <= 'Z',
			b >= 'a' && b <= 'z',
			b >= '0' && b <= '9',
			b == '+',
			b == '/':
			clean = append(clean, b)
			dataChars++
		case b == '=':
			clean = append(clean, b)
		}
	}
	if dataChars == 0 {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(string(clean))
}

func gradiumTTSAudioFrame(audio []byte, sampleRate int) *tts.SynthesizedAudio {
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              bytes.Clone(audio),
			SampleRate:        uint32(sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(len(audio) / 2),
		},
	}
}
